package main

import (
	"path/filepath"
	"sort"
	"strings"
)

// A file that moves on disk — a rename, a `git mv`, a refactor that shuffles
// docs into folders — used to strand the browser on a bare "Not Found" body:
// no sidebar, no links, nothing to click. The not-found page keeps the sidebar
// and adds two things to the body: where the file probably went (the same
// filename elsewhere under the root is what a move leaves behind) and a
// listing of the nearest folder that still exists.

const maxPathSuggestions = 6

// pathSuggestion is one candidate offered on the not-found page.
type pathSuggestion struct {
	Path   string // path relative to the served root
	Reason string // why it is being offered, shown next to the link
	rank   int    // lower sorts first; 0 is a likely move
	shared int    // leading path segments shared with the missing path
	depth  int    // how deep the candidate sits under the root
}

// Moved reports whether the suggestion is a same-filename match, the shape a
// move leaves behind (and the case where comments, being keyed by inode,
// follow the file to its new path).
func (s pathSuggestion) Moved() bool { return s.rank == 0 }

// findPathSuggestions ranks the files in tree by how likely each one is to be
// the file relPath used to name.
func findPathSuggestions(tree []FileNode, relPath string) []pathSuggestion {
	want := strings.ToLower(filepath.Base(relPath))
	if want == "" || want == "." || want == "/" {
		return nil
	}
	wantStem := stemOf(want)
	wantSegs := pathSegments(relPath)

	var out []pathSuggestion
	walkFileNodes(tree, func(n FileNode) {
		if n.Type != "file" || n.Path == relPath {
			return
		}
		name := strings.ToLower(n.Name)
		rank, reason := -1, ""
		switch {
		case name == want:
			rank, reason = 0, "same filename, different folder"
		case stemOf(name) == wantStem:
			rank, reason = 1, "same name, different extension"
		case similarNames(stemOf(name), wantStem):
			rank, reason = 2, "similar name"
		default:
			return
		}
		segs := pathSegments(n.Path)
		out = append(out, pathSuggestion{
			Path:   n.Path,
			Reason: reason,
			rank:   rank,
			shared: sharedPrefixLen(wantSegs, segs),
			depth:  len(segs),
		})
	})

	// Within a rank, the nearest candidate wins: the one sharing the most
	// leading folders with the missing path, then the shallowest. A file moved
	// one folder down should beat the same filename on the far side of the tree.
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
	if len(out) > maxPathSuggestions {
		out = out[:maxPathSuggestions]
	}
	return out
}

// pathSegments splits a root-relative path into its folder segments plus the
// filename, dropping empty segments.
func pathSegments(rel string) []string {
	var out []string
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg != "" && seg != "." {
			out = append(out, seg)
		}
	}
	return out
}

// sharedPrefixLen counts the leading folder segments two paths agree on. The
// filename itself is excluded, so it measures how close the folders are.
func sharedPrefixLen(a, b []string) int {
	n := 0
	for n < len(a)-1 && n < len(b)-1 && strings.EqualFold(a[n], b[n]) {
		n++
	}
	return n
}

func stemOf(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// similarNames reports whether two filename stems are close enough that one is
// worth offering for the other: either contains the other (plan → plan-v2) or
// they are within 30% of each other by edit distance (plan → plna).
func similarNames(a, b string) bool {
	if len(a) < 4 || len(b) < 4 {
		return false
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	longest := len(a)
	if len(b) > longest {
		longest = len(b)
	}
	return float64(longest-levenshtein(a, b))/float64(longest) >= 0.7
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func walkFileNodes(nodes []FileNode, fn func(FileNode)) {
	for _, n := range nodes {
		fn(n)
		if len(n.Children) > 0 {
			walkFileNodes(n.Children, fn)
		}
	}
}

// nearestExistingDir walks up from relPath's parent until it reaches a folder
// still present in tree, and returns that folder's path (empty for the served
// root) with its entries.
func nearestExistingDir(tree []FileNode, relPath string) (string, []FileNode) {
	dir := strings.Trim(filepath.ToSlash(filepath.Dir(filepath.Clean(relPath))), "/")
	for dir != "" && dir != "." {
		if entries, ok := findDirNode(tree, dir); ok {
			return dir, entries
		}
		next := strings.Trim(filepath.ToSlash(filepath.Dir(dir)), "/")
		if next == dir {
			break
		}
		dir = next
	}
	return "", tree
}

// findDirNode returns the entries of the directory at path (relative to the
// served root), or false when no such directory is in the tree.
func findDirNode(tree []FileNode, path string) ([]FileNode, bool) {
	path = strings.Trim(filepath.ToSlash(path), "/")
	if path == "" || path == "." {
		return tree, true
	}
	nodes := tree
	for _, seg := range strings.Split(path, "/") {
		found := false
		for _, n := range nodes {
			if n.Type == "dir" && n.Name == seg {
				nodes = n.Children
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return nodes, true
}
