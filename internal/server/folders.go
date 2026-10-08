package server

import (
	"os"
	"path/filepath"
	"sort"
	"sync"

	"serve/internal/paths"
	"serve/internal/render"
	"serve/internal/store"
)

// folderSet is the folders the user has opened: the only places the server
// will serve files from. It is cached and re-read from the store when a path
// is not found in it, since the CLI adds folders from another process.
type folderSet struct {
	st   *store.Store
	mu   sync.Mutex
	list []string
}

func newFolderSet(st *store.Store) *folderSet {
	f := &folderSet{st: st}
	f.reload()
	return f
}

func (f *folderSet) reload() {
	fs, _ := f.st.Folders()
	var list []string
	for _, x := range fs {
		list = append(list, x.Path)
	}
	// Shallowest first, so rootFor finds the widest opened folder.
	sort.Slice(list, func(i, j int) bool { return len(list[i]) < len(list[j]) })
	f.mu.Lock()
	f.list = list
	f.mu.Unlock()
}

// rootFor returns the widest opened folder containing p.
func (f *folderSet) rootFor(p string) (string, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		f.mu.Lock()
		for _, d := range f.list {
			if paths.Within(p, d) {
				f.mu.Unlock()
				return d, true
			}
		}
		f.mu.Unlock()
		if attempt == 0 {
			f.reload()
		}
	}
	return "", false
}

func (f *folderSet) add(p string) error {
	if err := f.st.AddFolder(p); err != nil {
		return err
	}
	f.reload()
	return nil
}

func (f *folderSet) remove(p string) error {
	if err := f.st.RemoveFolder(p); err != nil {
		return err
	}
	f.reload()
	return nil
}

// renderCache keeps the last render of each file, reused while the file's
// size and modification time are unchanged.
type renderCache struct {
	mu sync.Mutex
	m  map[string]cachedDoc
}

type cachedDoc struct {
	size  int64
	mtime int64
	doc   *render.Doc
}

func newRenderCache() *renderCache { return &renderCache{m: map[string]cachedDoc{}} }

func (s *Server) readDoc(p string) (*render.Doc, os.FileInfo, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return nil, nil, err
	}
	s.cache.mu.Lock()
	c, ok := s.cache.m[p]
	s.cache.mu.Unlock()
	if ok && c.size == fi.Size() && c.mtime == fi.ModTime().UnixNano() {
		return c.doc, fi, nil
	}
	doc, err := s.svc.Read(p)
	if err != nil {
		return nil, nil, err
	}
	s.cache.mu.Lock()
	if len(s.cache.m) > 200 {
		s.cache.m = map[string]cachedDoc{}
	}
	s.cache.m[p] = cachedDoc{size: fi.Size(), mtime: fi.ModTime().UnixNano(), doc: doc}
	s.cache.mu.Unlock()
	return doc, fi, nil
}

func parentDir(p string) string { return filepath.Dir(p) }
