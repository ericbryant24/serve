package server

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"serve/internal/paths"
)

// ContentHandler serves the content port: raw files from opened folders, and
// HTML pages with the small script that draws comments inside the iframe.
// It never serves serve's own pages or API.
func (s *Server) ContentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if s.pageRole(r) == roleNone {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/_serve/embed.js" {
			data, ok := s.opt.Assets.Open("embed.js")
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(data)
			return
		}
		p, ok := paths.FromURLPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		p = paths.Abs(p)
		if _, ok := s.folders.rootFor(p); !ok {
			http.Error(w, "that folder is not open in serve", http.StatusForbidden)
			return
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", frameAncestors)
		if r.URL.Query().Get("frame") == "1" && (strings.HasSuffix(strings.ToLower(p), ".html") || strings.HasSuffix(strings.ToLower(p), ".htm")) {
			data, err := os.ReadFile(p)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(injectEmbed(data, s.appOrigin(r), s.opt.Assets.Hash()))
			return
		}
		serveFile(w, r, p, fi)
	})
}

// serveFile serves a file from an opened folder. Files change under an open
// page, so the browser checks before reusing one, and the ETag (exact mtime
// and size) catches a change Last-Modified, to the second, would miss.
func serveFile(w http.ResponseWriter, r *http.Request, p string, fi os.FileInfo) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", `"`+strconv.FormatInt(fi.ModTime().UnixNano(), 36)+"-"+strconv.FormatInt(fi.Size(), 36)+`"`)
	http.ServeFile(w, r, p)
}

// appOrigin is the app's origin as seen from a request to the content port.
func (s *Server) appOrigin(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(s.port))
}

// injectEmbed adds the comment script to an HTML page, as late as possible so
// it runs after the page's own markup is parsed.
func injectEmbed(page []byte, appOrigin, v string) []byte {
	tag := []byte(`<script src="/_serve/embed.js?v=` + v + `" data-serve-app="` + appOrigin + `"></script>`)
	lower := bytes.ToLower(page)
	if i := bytes.LastIndex(lower, []byte("</body>")); i >= 0 {
		return append(append(append([]byte{}, page[:i]...), tag...), page[i:]...)
	}
	if i := bytes.LastIndex(lower, []byte("</html>")); i >= 0 {
		return append(append(append([]byte{}, page[:i]...), tag...), page[i:]...)
	}
	return append(append([]byte{}, page...), tag...)
}
