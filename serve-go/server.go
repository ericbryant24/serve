package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// WebSocket upgrader
// ---------------------------------------------------------------------------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ---------------------------------------------------------------------------
// File tree
// ---------------------------------------------------------------------------

func buildFileTree(root string, rel string) []FileNode {
	base := root
	if rel != "" {
		base = filepath.Join(root, rel)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	var dirs, files []FileNode
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		var relPath string
		if rel != "" {
			relPath = rel + "/" + entry.Name()
		} else {
			relPath = entry.Name()
		}
		if entry.IsDir() {
			children := buildFileTree(root, relPath)
			if len(children) > 0 {
				dirs = append(dirs, FileNode{
					Name:     entry.Name(),
					Path:     relPath,
					Type:     "dir",
					Children: children,
				})
			}
		} else {
			files = append(files, FileNode{
				Name: entry.Name(),
				Path: relPath,
				Type: "file",
			})
		}
	}
	return append(dirs, files...)
}

var defaultFilePriority = []string{"README.md", "readme.md", "index.md", "index.html"}

func findDefaultFile(root string) string {
	for _, name := range defaultFilePriority {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			return name
		}
	}
	// First .md file
	var mdFiles []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) == ".md" {
			mdFiles = append(mdFiles, path)
		}
		return nil
	})
	if len(mdFiles) > 0 {
		sort.Strings(mdFiles)
		rel, _ := filepath.Rel(root, mdFiles[0])
		return rel
	}
	return ""
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type Server struct {
	mode            string
	host            string
	port            int
	openBrowser     bool
	openBrowserFn   func(url string)
	filePath        string // single-file mode
	baseDir         string
	dirName         string
	faviconSeed     string
	docID           string

	mu             sync.Mutex
	wsClients      map[*websocket.Conn]bool
	commentStore   *CommentStore
	commentStores  map[string]*CommentStore // directory mode
}

func NewServer(filePath string, mode, host string, port int, openBrowser bool) *Server {
	s := &Server{
		mode:          mode,
		host:          host,
		port:          port,
		openBrowser:   openBrowser,
		wsClients:     map[*websocket.Conn]bool{},
		commentStores: map[string]*CommentStore{},
	}

	if mode == "directory" {
		abs, _ := filepath.Abs(filePath)
		s.baseDir = abs
		s.dirName = filepath.Base(abs)
		if s.dirName == "" || s.dirName == "." {
			s.dirName = abs
		}
		s.faviconSeed = abs
	} else {
		abs, _ := filepath.Abs(filePath)
		s.filePath = abs
		s.baseDir = filepath.Dir(abs)
		s.faviconSeed = abs
		s.docID = getDocumentID(abs)
	}
	return s
}

// ---------------------------------------------------------------------------
// WebSocket broadcast
// ---------------------------------------------------------------------------

func (s *Server) broadcast(msg map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	for conn := range s.wsClients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			delete(s.wsClients, conn)
			conn.Close()
		}
	}
}

func (s *Server) notifyReload() {
	s.broadcast(map[string]interface{}{"type": "reload"})
}

func (s *Server) notifyCommentsUpdated() {
	s.broadcast(map[string]interface{}{"type": "comments-updated"})
}

func (s *Server) notifyFileTree(tree []FileNode) {
	s.broadcast(map[string]interface{}{"type": "filetree", "files": tree})
}

// ---------------------------------------------------------------------------
// Comment store helpers
// ---------------------------------------------------------------------------

func (s *Server) getStoreForFile(fp string) *CommentStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.commentStores[fp]; ok {
		return store
	}
	docID := getDocumentID(fp)
	if docID == "" {
		var err error
		docID, err = ensureDocumentID(fp)
		if err != nil {
			docID = "__error__"
		}
	}
	store := NewCommentStore(docID)
	s.commentStores[fp] = store
	return store
}

func (s *Server) getStore(r *http.Request) *CommentStore {
	if s.mode == "directory" {
		fp := s.fileFromRequest(r)
		if fp == "" {
			return NewCommentStore("__empty__")
		}
		return s.getStoreForFile(fp)
	}
	if s.commentStore == nil {
		if s.docID == "" {
			id, err := ensureDocumentID(s.filePath)
			if err == nil {
				s.docID = id
			}
		}
		s.commentStore = NewCommentStore(s.docID)
	}
	return s.commentStore
}

func (s *Server) fileFromRequest(r *http.Request) string {
	rel := r.URL.Query().Get("file")
	if rel == "" {
		return ""
	}
	fp := filepath.Clean(filepath.Join(s.baseDir, rel))
	if !strings.HasPrefix(fp, s.baseDir) {
		return ""
	}
	if fi, err := os.Stat(fp); err != nil || fi.IsDir() {
		return ""
	}
	return fp
}

// ---------------------------------------------------------------------------
// HTTP handlers — WebSocket
// ---------------------------------------------------------------------------

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.wsClients[conn] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.wsClients, conn)
		s.mu.Unlock()
		conn.Close()
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers — single-file mode
// ---------------------------------------------------------------------------

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	var htmlStr string
	var err error

	if s.mode == "markdown" {
		if isMarpDoc(s.filePath) && r.URL.Query().Get("present") == "1" {
			htmlStr = renderMarp(s.filePath, nil, nil, s.faviconSeed, true)
		} else {
			marp := isMarpDoc(s.filePath)
			htmlStr, err = renderMarkdown(s.filePath, wrapOptions{
				faviconPath: s.faviconSeed,
				isMarp:      marp,
			})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
	} else {
		data, rerr := os.ReadFile(s.filePath)
		if rerr != nil {
			http.Error(w, rerr.Error(), 500)
			return
		}
		htmlStr = injectReloadScript(string(data), nil, nil, s.faviconSeed, true, false)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, htmlStr)
}

func (s *Server) handleDataURL(w http.ResponseWriter, r *http.Request) {
	url, err := generateDataURL(s.filePath, s.mode)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, url)
}

// ---------------------------------------------------------------------------
// HTTP handlers — directory mode
// ---------------------------------------------------------------------------

func (s *Server) handleDirRoot(w http.ResponseWriter, r *http.Request) {
	def := findDefaultFile(s.baseDir)
	if def != "" {
		http.Redirect(w, r, "/"+def, http.StatusFound)
		return
	}
	http.Error(w, "No servable files found in directory.", 404)
}

var embeddableExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true,
	".webp": true, ".bmp": true, ".ico": true,
	".mp4": true, ".webm": true, ".ogg": true, ".mp3": true, ".wav": true, ".flac": true,
	".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
	".css": true, ".js": true,
}

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".webp": true, ".bmp": true, ".ico": true,
}

func (s *Server) handleDirFile(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Path
	if strings.HasPrefix(relPath, "/") {
		relPath = relPath[1:]
	}
	fp := filepath.Clean(filepath.Join(s.baseDir, relPath))
	if !strings.HasPrefix(fp, s.baseDir) {
		http.Error(w, "Forbidden", 403)
		return
	}
	fi, err := os.Stat(fp)
	if err != nil || fi.IsDir() {
		http.Error(w, "Not Found", 404)
		return
	}

	ext := strings.ToLower(filepath.Ext(fp))
	sidebar := &[2]string{s.dirName, relPath}
	tree := buildFileTree(s.baseDir, "")

	// Raw access
	if r.URL.Query().Get("raw") == "1" {
		http.ServeFile(w, r, fp)
		return
	}

	// Embedded assets served raw when browser doesn't want HTML
	accept := r.Header.Get("Accept")
	if embeddableExts[ext] && !strings.Contains(accept, "text/html") {
		http.ServeFile(w, r, fp)
		return
	}

	opts := wrapOptions{sidebar: sidebar, fileTree: tree, faviconPath: s.faviconSeed}

	switch ext {
	case ".md":
		marp := isMarpDoc(fp)
		var htmlStr string
		if marp && r.URL.Query().Get("present") == "1" {
			htmlStr = renderMarp(fp, sidebar, tree, s.faviconSeed, true)
		} else {
			opts.isMarp = marp
			htmlStr, err = renderMarkdown(fp, opts)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlStr)

	case ".html", ".htm":
		data, rerr := os.ReadFile(fp)
		if rerr != nil {
			http.Error(w, rerr.Error(), 500)
			return
		}
		htmlStr := injectReloadScript(string(data), sidebar, tree, s.faviconSeed, true, false)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlStr)

	case ".pdf":
		htmlStr := renderPDF(fp, relPath, opts)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlStr)

	default:
		if canRenderAsCode(fp) || isTextFile(fp) {
			htmlStr, err := renderCodeFile(fp, opts)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, htmlStr)
			return
		}
		if imageExts[ext] {
			htmlStr := renderImage(fp, relPath, opts)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, htmlStr)
			return
		}
		// File info page
		size := fi.Size()
		htmlStr := wrapFileInfo(fi.Name(), relPath, size, opts)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlStr)
	}
}

func (s *Server) handleFileTree(w http.ResponseWriter, r *http.Request) {
	tree := buildFileTree(s.baseDir, "")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"files": tree})
}

// ---------------------------------------------------------------------------
// HTTP handlers — comment API
// ---------------------------------------------------------------------------

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	if s.mode == "directory" {
		fp := s.fileFromRequest(r)
		if fp == "" {
			writeJSON(w, map[string]interface{}{"comments": []interface{}{}})
			return
		}
		if getDocumentID(fp) == "" {
			writeJSON(w, map[string]interface{}{"comments": []interface{}{}})
			return
		}
		store := s.getStoreForFile(fp)
		comments, _ := store.List()
		writeJSON(w, map[string]interface{}{"comments": comments})
		return
	}
	if s.docID == "" {
		writeJSON(w, map[string]interface{}{"comments": []interface{}{}})
		return
	}
	store := s.getStore(r)
	comments, _ := store.List()
	writeJSON(w, map[string]interface{}{"comments": comments})
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text            string  `json:"text"`
		AnchorText      string  `json:"anchor_text"`
		BlockText       string  `json:"block_text"`
		SourceLineStart *int    `json:"source_line_start"`
		SourceLineEnd   *int    `json:"source_line_end"`
		ParentID        *string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	store := s.getStore(r)
	comment, err := store.Add(body.Text, body.AnchorText, body.BlockText,
		body.SourceLineStart, body.SourceLineEnd, body.ParentID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.notifyCommentsUpdated()
	writeJSON(w, comment)
}

func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Text     *string `json:"text"`
		Resolved *bool   `json:"resolved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	store := s.getStore(r)
	comment, err := store.Update(id, body.Text, body.Resolved)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if comment == nil {
		http.Error(w, `{"error":"Not found"}`, 404)
		return
	}
	s.notifyCommentsUpdated()
	writeJSON(w, comment)
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	store := s.getStore(r)
	found, err := store.Delete(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !found {
		http.Error(w, `{"error":"Not found"}`, 404)
		return
	}
	s.notifyCommentsUpdated()
	writeJSON(w, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Port finding & startup
// ---------------------------------------------------------------------------

func findPort(host string, startPort int) int {
	for offset := 0; offset < 11; offset++ {
		port := startPort + offset
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			ln.Close()
			return port
		}
	}
	return startPort
}

func (s *Server) Start(ctx context.Context) error {
	port := findPort(s.host, s.port)
	s.port = port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/api/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListComments(w, r)
		case http.MethodPost:
			s.handleCreateComment(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/comments/{id}", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPatch:
			s.handleUpdateComment(w, r)
		case http.MethodDelete:
			s.handleDeleteComment(w, r)
		default:
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/files", s.handleFileTree)

	if s.mode == "directory" {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				s.handleDirRoot(w, r)
				return
			}
			s.handleDirFile(w, r)
		})
	} else {
		mux.HandleFunc("/__data_url", s.handleDataURL)
		mux.Handle("/", &staticOrPageHandler{
			s:          s,
			fileServer: http.FileServer(http.Dir(s.baseDir)),
		})
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.host, port),
		Handler: mux,
	}

	url := fmt.Sprintf("http://%s:%d", s.host, port)
	if s.mode == "directory" {
		fmt.Printf("Serving %s at %s\n", s.baseDir, url)
	} else {
		fmt.Printf("Serving %s at %s\n", filepath.Base(s.filePath), url)
	}
	fmt.Println("Press Ctrl+C to stop")

	if s.openBrowserFn != nil {
		go s.openBrowserFn(url)
	}

	// Start watchers
	go func() {
		if s.mode == "directory" {
			err := watchDirectory(s.baseDir, func() {
				s.notifyReload()
				tree := buildFileTree(s.baseDir, "")
				s.notifyFileTree(tree)
			})
			_ = err
		} else {
			err := watch(s.filePath, s.notifyReload, s.mode == "markdown")
			_ = err
		}
	}()
	go func() {
		_ = watchComments(s.notifyCommentsUpdated)
	}()

	// Shutdown on context cancel
	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON helper
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------------
// PID file helpers (for list/kill)
// ---------------------------------------------------------------------------

func pidFilePath() string {
	return filepath.Join(homeDir(), ".serve", "serve-go.pid")
}

func writePIDFile(port int) {
	dir := filepath.Join(homeDir(), ".serve")
	os.MkdirAll(dir, 0755)
	content := fmt.Sprintf("%d %d\n", os.Getpid(), port)
	os.WriteFile(pidFilePath(), []byte(content), 0644)
}

func removePIDFile() {
	os.Remove(pidFilePath())
}

// ---------------------------------------------------------------------------
// Static file handler for single-file mode (serve assets from base dir)
// ---------------------------------------------------------------------------

type staticOrPageHandler struct {
	s           *Server
	fileServer  http.Handler
}

func (h *staticOrPageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		h.s.handlePage(w, r)
		return
	}
	if r.URL.Path == "/__data_url" {
		h.s.handleDataURL(w, r)
		return
	}
	h.fileServer.ServeHTTP(w, r)
}

