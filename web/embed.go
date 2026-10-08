// Package web holds serve's browser app: the TypeScript sources under src/,
// and the bundle built from them into dist/, which is embedded in the binary.
// Rebuild the bundle with `go generate ./web` (no Node needed; esbuild runs as
// a Go tool). The dependencies under node_modules come from `npm ci`.
package web

//go:generate go run ./build

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"sort"
	"sync"
)

//go:embed dist
var dist embed.FS

// Files serves the built bundle.
type Files struct {
	once sync.Once
	hash string
}

var files = &Files{}

// Assets returns the embedded bundle.
func Assets() *Files { return files }

// Open returns a file from the bundle.
func (f *Files) Open(name string) ([]byte, bool) {
	b, err := dist.ReadFile("dist/" + name)
	return b, err == nil
}

// Hash identifies this build of the bundle, for cache busting.
func (f *Files) Hash() string {
	f.once.Do(func() {
		h := sha256.New()
		var names []string
		_ = fs.WalkDir(dist, "dist", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				names = append(names, p)
			}
			return nil
		})
		sort.Strings(names)
		for _, n := range names {
			b, _ := dist.ReadFile(n)
			h.Write([]byte(n))
			h.Write(b)
		}
		f.hash = hex.EncodeToString(h.Sum(nil))[:12]
	})
	return f.hash
}
