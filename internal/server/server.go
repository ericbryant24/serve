// Package server is serve's background server: one process per user, serving
// any folder the user has opened, at URLs that are the files' own paths.
//
// It listens on two ports. The app port (7070 by default) serves serve's own
// pages and its API. The content port (the next one up) serves the user's raw
// files, including HTML pages, which the app shows in an iframe. Being a
// different origin, a page on the content port can neither read the app's
// pages (where the API token is) nor call the API, so an HTML file of unknown
// origin runs with no power over the user's files.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"serve/internal/comments"
	"serve/internal/config"
	"serve/internal/daemon"
	"serve/internal/logx"
	"serve/internal/paths"
	"serve/internal/render"
	"serve/internal/reports"
	"serve/internal/store"
)

// Options configure a server.
type Options struct {
	Host    string
	Port    int
	Version string
	Store   *store.Store
	Config  config.Config
	// Assets are the built web app files (app.js, app.css, ...).
	Assets AssetSource
	// NoDaemonFile skips writing ~/.serve/daemon.json (tests).
	NoDaemonFile bool
}

// AssetSource serves the web app's files.
type AssetSource interface {
	Open(name string) ([]byte, bool)
	Hash() string
}

// Server is the running server.
type Server struct {
	opt         Options
	cfg         config.Config
	store       *store.Store
	svc         *comments.Service
	reports     *reports.ReportStore
	token       string
	shareToken  string
	port        int
	contentPort int
	started     time.Time
	hub         *hub
	files       *fileWatcher
	cache       *renderCache
	folders     *folderSet
	shutdown    context.CancelFunc
	mu          sync.Mutex
}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// New makes a server.
func New(o Options) *Server {
	s := &Server{
		opt:        o,
		cfg:        o.Config,
		store:      o.Store,
		svc:        &comments.Service{Store: o.Store, Config: o.Config},
		reports:    reports.NewStore(),
		token:      randToken(),
		shareToken: randToken()[:16],
		started:    time.Now(),
		cache:      newRenderCache(),
	}
	s.folders = newFolderSet(o.Store)
	s.hub = newHub()
	s.files = newFileWatcher(s)
	return s
}

// Token is the API token pages and the CLI use.
func (s *Server) Token() string { return s.token }

// Run listens and serves until ctx ends or a shutdown request arrives.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	s.shutdown = cancel
	defer cancel()

	host := s.opt.Host
	if host == "" {
		host = "localhost"
	}
	appLn, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(s.opt.Port)))
	if err != nil {
		return fmt.Errorf("port %d is in use: %w", s.opt.Port, err)
	}
	var contentLn net.Listener
	for p := s.opt.Port + 1; p <= s.opt.Port+20; p++ {
		if contentLn, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); err == nil {
			break
		}
	}
	if contentLn == nil {
		appLn.Close()
		return fmt.Errorf("no free port for page content above %d", s.opt.Port)
	}
	s.port = appLn.Addr().(*net.TCPAddr).Port
	s.contentPort = contentLn.Addr().(*net.TCPAddr).Port

	if !s.opt.NoDaemonFile {
		if err := daemon.Write(daemon.Info{
			PID: os.Getpid(), Port: s.port, ContentPort: s.contentPort, Host: s.opt.Host,
			Token: s.token, ShareToken: s.shareToken, Version: s.opt.Version, Started: s.started.UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		defer daemon.Remove(os.Getpid())
	}

	if res, err := s.svc.ImportV1(comments.V1Dir()); err != nil {
		logx.Error("importing comments from the old store failed", err)
	} else if res != nil && res.Threads > 0 {
		logx.Logf("info", "imported comments from the old store", logx.Int("documents", res.Documents), logx.Int("threads", res.Threads))
	}

	app := &http.Server{Handler: s.AppHandler(), ReadHeaderTimeout: 10 * time.Second}
	content := &http.Server{Handler: s.ContentHandler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	go func() { errc <- app.Serve(appLn) }()
	go func() { errc <- content.Serve(contentLn) }()
	go s.pollStore(ctx)
	go s.files.run(ctx)
	logx.Logf("info", "server started", logx.Int("port", s.port), logx.Int("content_port", s.contentPort))

	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	s.hub.closeAll()
	shut, c2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer c2()
	_ = app.Shutdown(shut)
	_ = content.Shutdown(shut)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Ports reports the ports the server is listening on.
func (s *Server) Ports() (int, int) { return s.port, s.contentPort }

// --- request checks -------------------------------------------------------

// allowedHost refuses requests addressed to a name other than this machine:
// a DNS-rebinding page reaches serve under the attacker's own domain name.
func (s *Server) allowedHost(hostHeader string) bool {
	h := hostHeader
	if hh, _, err := net.SplitHostPort(hostHeader); err == nil {
		h = hh
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	if h == "" {
		return false
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || net.ParseIP(h) != nil {
		return true
	}
	return strings.EqualFold(h, strings.Trim(s.opt.Host, "[]"))
}

func isLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// role is what a request may do: everything (the person at this machine),
// read and comment (someone given a share link), or nothing.
type role int

const (
	roleNone role = iota
	roleGuest
	roleOwner
)

// pageRole decides who is viewing a page, which decides the token the page
// gets.
func (s *Server) pageRole(r *http.Request) role {
	if isLoopbackRemote(r) {
		return roleOwner
	}
	if c, err := r.Cookie("serve_share"); err == nil && c.Value == s.shareToken {
		return roleGuest
	}
	if r.URL.Query().Get("share") == s.shareToken {
		return roleGuest
	}
	return roleNone
}

// apiRole checks a request's token. The token travels in a header (or, for
// an event stream, which cannot set headers, a query parameter). A page on
// another site cannot read it, and cannot send the header without a CORS
// preflight that serve never answers.
func (s *Server) apiRole(r *http.Request) role {
	tok := r.Header.Get("X-Serve-Token")
	if tok == "" && r.Method == http.MethodGet {
		// An event stream or an <img> cannot set headers. Reads only: a
		// change always needs the header.
		tok = r.URL.Query().Get("token")
	}
	switch {
	case tok == "":
		return roleNone
	case tok == s.token && isLoopbackRemote(r):
		return roleOwner
	case tok == s.shareToken:
		return roleGuest
	}
	return roleNone
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// AppHandler serves serve's pages, assets and API.
func (s *Server) AppHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_serve/health", s.handleHealth)
	mux.HandleFunc("GET /_serve/assets/{name...}", s.handleAsset)
	mux.HandleFunc("GET /_serve/events", s.handleEvents)
	s.registerAPI(mux)
	mux.HandleFunc("GET /{path...}", s.handlePage)
	return s.guard(mux)
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/_serve/api/") || strings.HasPrefix(r.URL.Path, "/_serve/events") {
			if !sameOrigin(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"version": s.opt.Version, "pid": os.Getpid(), "started": s.started.UTC().Format(time.RFC3339)})
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "chroma.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write([]byte(chromaCSS()))
		return
	}
	data, ok := s.opt.Assets.Open(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	if r.URL.Query().Get("v") != "" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(data)
}

var chromaOnce sync.Once
var chromaText string

func chromaCSS() string {
	chromaOnce.Do(func() { chromaText = render.ChromaCSS() })
	return chromaText
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	}
	return "application/octet-stream"
}

// --- helpers ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// pathFromQuery reads a filesystem path from a request's "path" parameter,
// which may be a URL path (/~/x.md) or an absolute filesystem path.
func pathFromQuery(v string) (string, bool) {
	if v == "" {
		return "", false
	}
	if u, err := url.PathUnescape(v); err == nil {
		v = u
	}
	if p, ok := paths.FromURLPath(v); ok {
		return paths.Abs(p), true
	}
	return "", false
}
