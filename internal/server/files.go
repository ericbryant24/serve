package server

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"serve/internal/paths"
)

// Files the tree hides unless a .serveignore says otherwise.
const defaultIgnore = `.git/
node_modules/
__pycache__/
.venv/
dist/
build/
vendor/
target/
.next/
.DS_Store
*.pyc
*.o
*.class
`

type ignoreRules struct {
	patterns []string
}

func parseIgnore(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// rulesFor builds the ignore rules for a folder tree: .serveignore at its root
// when there is one (else the defaults), plus .gitignore when config asks.
func (s *Server) rulesFor(root string) *ignoreRules {
	r := &ignoreRules{}
	if data, err := os.ReadFile(filepath.Join(root, ".serveignore")); err == nil {
		r.patterns = parseIgnore(string(data))
	} else {
		r.patterns = parseIgnore(defaultIgnore)
	}
	if s.cfg.RespectGitignore {
		if data, err := os.ReadFile(filepath.Join(root, ".gitignore")); err == nil {
			r.patterns = append(r.patterns, parseIgnore(string(data))...)
		}
	}
	return r
}

func (r *ignoreRules) ignored(name string, isDir bool) bool {
	for _, pat := range r.patterns {
		neg := false
		if strings.HasPrefix(pat, "!") {
			neg, pat = true, pat[1:]
		}
		dirOnly := strings.HasSuffix(pat, "/")
		p := strings.Trim(pat, "/")
		if dirOnly && !isDir {
			continue
		}
		if ok, _ := filepath.Match(p, name); ok {
			return !neg
		}
	}
	return false
}

// entry is one item in a folder listing.
type entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	URL     string `json:"url"`
	Dir     bool   `json:"dir,omitempty"`
	Threads int    `json:"threads,omitempty"`
}

// listDir lists one folder level, folders first, with open thread counts.
func (s *Server) listDir(root, dir string) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	rules := s.rulesFor(root)
	counts, _ := s.store.OpenCounts()
	dirCounts := map[string]int{}
	for p, n := range counts {
		for d := filepath.Dir(p); paths.Within(d, dir) && d != dir; d = filepath.Dir(d) {
			dirCounts[d] += n
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	var out []entry
	for _, de := range des {
		name := de.Name()
		isDir := de.IsDir()
		if de.Type()&fs.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil {
				isDir = fi.IsDir()
			}
		}
		if rules.ignored(name, isDir) {
			continue
		}
		p := filepath.Join(dir, name)
		e := entry{Name: name, Path: p, URL: paths.URLPath(p), Dir: isDir}
		if isDir {
			e.Threads = dirCounts[p]
		} else {
			e.Threads = counts[p]
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// walkFiles visits files under root (relative paths), skipping ignored ones,
// up to limit files.
func (s *Server) walkFiles(root string, limit int, visit func(rel string)) {
	rules := s.rulesFor(root)
	n := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == root {
			return nil
		}
		if rules.ignored(d.Name(), d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		visit(filepath.ToSlash(rel))
		n++
		if n >= limit {
			return filepath.SkipAll
		}
		return nil
	})
}

// searchFiles ranks the files under root by how well their path matches q.
func (s *Server) searchFiles(root, q string, max int) []entry {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	var files []string
	s.walkFiles(root, 50000, func(rel string) { files = append(files, rel) })
	type scored struct {
		rel   string
		score int
	}
	var hits []scored
	for _, rel := range files {
		if sc, ok := fuzzyScore(strings.ToLower(rel), q); ok {
			hits = append(hits, scored{rel, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return len(hits[i].rel) < len(hits[j].rel)
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	counts, _ := s.store.OpenCounts()
	out := make([]entry, 0, len(hits))
	for _, h := range hits {
		p := filepath.Join(root, filepath.FromSlash(h.rel))
		out = append(out, entry{Name: h.rel, Path: p, URL: paths.URLPath(p), Threads: counts[p]})
	}
	return out
}

// fuzzyScore matches q as a subsequence of s, scoring consecutive runs and
// matches in the file name over scattered ones.
func fuzzyScore(s, q string) (int, bool) {
	base := s
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		base = s[i+1:]
	}
	score := 0
	if strings.Contains(base, q) {
		score += 100
		if strings.HasPrefix(base, q) {
			score += 50
		}
	} else if strings.Contains(s, q) {
		score += 40
	}
	si, run := 0, 0
	for _, qc := range q {
		found := false
		for si < len(s) {
			c := rune(s[si])
			si++
			if c == qc {
				found = true
				run++
				score += run
				break
			}
			run = 0
		}
		if !found {
			return 0, false
		}
	}
	return score, true
}

// --- not found ----------------------------------------------------------------------

type suggestion struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Display string `json:"display"`
	Reason  string `json:"reason"`
	rank    int
	shared  int
	depth   int
}

// moveSuggestions ranks files under root by how likely each is to be where a
// missing file went: the same file name elsewhere is what a move leaves.
func (s *Server) moveSuggestions(root, missing string) []suggestion {
	want := strings.ToLower(filepath.Base(missing))
	wantStem := stemOf(want)
	relMissing, _ := filepath.Rel(root, missing)
	wantSegs := strings.Split(filepath.ToSlash(relMissing), "/")
	var out []suggestion
	s.walkFiles(root, 50000, func(rel string) {
		name := strings.ToLower(filepath.Base(rel))
		rank, reason := -1, ""
		switch {
		case name == want:
			rank, reason = 0, "same file name, different folder"
		case stemOf(name) == wantStem:
			rank, reason = 1, "same name, different extension"
		case similarNames(stemOf(name), wantStem):
			rank, reason = 2, "similar name"
		default:
			return
		}
		segs := strings.Split(rel, "/")
		shared := 0
		for shared < len(segs)-1 && shared < len(wantSegs)-1 && segs[shared] == wantSegs[shared] {
			shared++
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		out = append(out, suggestion{Path: p, URL: paths.URLPath(p), Display: paths.Display(p), Reason: reason, rank: rank, shared: shared, depth: len(segs)})
	})
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.shared != b.shared {
			return a.shared > b.shared
		}
		return a.depth < b.depth
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func stemOf(name string) string { return strings.TrimSuffix(name, filepath.Ext(name)) }

func similarNames(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return min(len(a), len(b)) >= 4
	}
	d := levenshtein(a, b)
	return d <= max(1, min(len(a), len(b))/4)
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// nearestDir is the closest existing folder at or above p.
func nearestDir(p string) string {
	for d := filepath.Dir(p); ; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		if d == filepath.Dir(d) {
			return d
		}
	}
}

// readmeIn returns a README in a folder, if there is one.
func readmeIn(dir string) string {
	for _, n := range []string{"README.md", "readme.md", "Readme.md", "index.md"} {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// writeFileAtomic replaces a file's contents by writing a temp file beside it
// and renaming it into place, keeping the original's permissions.
func writeFileAtomic(p string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".serve-tmp-*")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	if _, err := w.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}
