// Package paths maps between filesystem paths and the URLs serve shows them
// at, and locates serve's own state under ~/.serve.
package paths

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Home is the user's home directory.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return string(filepath.Separator)
	}
	return h
}

// StateDir is serve's state directory, ~/.serve.
func StateDir() string { return filepath.Join(Home(), ".serve") }

// Abs returns an absolute, cleaned path with symlinks resolved where possible,
// so the same file always gets the same path.
func Abs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		a = filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		return r
	}
	// The file may not exist (yet); resolve its directory instead.
	if r, err := filepath.EvalSymlinks(filepath.Dir(a)); err == nil {
		return filepath.Join(r, filepath.Base(a))
	}
	return a
}

// homeResolved is the home directory with symlinks resolved, so it matches
// paths that went through Abs.
func homeResolved() string {
	h := Home()
	if r, err := filepath.EvalSymlinks(h); err == nil {
		return r
	}
	return h
}

// URLPath returns the URL path for a filesystem path: the absolute path with
// the home directory written as "~", so ~/Projects/x.md is /~/Projects/x.md.
func URLPath(p string) string {
	p = filepath.ToSlash(p)
	home := filepath.ToSlash(homeResolved())
	switch {
	case p == home:
		p = "/~"
	case strings.HasPrefix(p, home+"/"):
		p = "/~" + p[len(home):]
	case runtime.GOOS == "windows":
		p = "/" + p
	}
	u := url.URL{Path: p}
	return u.EscapedPath()
}

// FromURLPath is the inverse of URLPath. The bool is false for a path that
// is not a filesystem path.
func FromURLPath(u string) (string, bool) {
	if u == "" || u[0] != '/' {
		return "", false
	}
	if u == "/~" || strings.HasPrefix(u, "/~/") {
		return filepath.Join(homeResolved(), filepath.FromSlash(strings.TrimPrefix(u, "/~"))), true
	}
	if runtime.GOOS == "windows" {
		// /C:/Users/x -> C:\Users\x
		if len(u) >= 3 && u[2] == ':' {
			return filepath.FromSlash(u[1:]), true
		}
		return "", false
	}
	return filepath.Clean(u), true
}

// Within reports whether p is dir or inside it.
func Within(p, dir string) bool {
	if p == dir {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Display shortens a path for people: the home directory becomes ~.
func Display(p string) string {
	home := homeResolved()
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}
