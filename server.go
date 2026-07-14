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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ---------------------------------------------------------------------------
// WebSocket upgrader
// ---------------------------------------------------------------------------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ---------------------------------------------------------------------------
// .serveignore
// ---------------------------------------------------------------------------

const defaultServeIgnore = `# .serveignore — files and directories to hide from the sidebar
# gitignore-style patterns: trailing / matches directories only, * is a wildcard

.git/
node_modules/
__pycache__/
dist/
build/
vendor/
target/
*.pyc
*.o
*.class
`

func parseServeIgnore(content string) []string {
	var patterns []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

func loadServeIgnore(root string) []string {
	p := filepath.Join(root, ".serveignore")
	data, err := os.ReadFile(p)
	if err != nil {
		return parseServeIgnore(defaultServeIgnore)
	}
	return parseServeIgnore(string(data))
}

func matchesIgnorePatterns(name string, isDir bool, patterns []string) bool {
	for _, pat := range patterns {
		dirOnly := strings.HasSuffix(pat, "/")
		p := strings.TrimSuffix(pat, "/")
		if dirOnly && !isDir {
			continue
		}
		if matched, _ := filepath.Match(p, name); matched {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// File tree
// ---------------------------------------------------------------------------

func buildFileTree(root string, rel string, patterns []string) []FileNode {
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
		if matchesIgnorePatterns(entry.Name(), entry.IsDir(), patterns) {
			continue
		}
		var relPath string
		if rel != "" {
			relPath = rel + "/" + entry.Name()
		} else {
			relPath = entry.Name()
		}
		if entry.IsDir() {
			children := buildFileTree(root, relPath, patterns)
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

// rootState is the served-directory snapshot. It is swapped atomically so the
// "go up a directory" action can re-root a running server without locking every
// request handler: each handler loads one consistent snapshot per request.
type rootState struct {
	baseDir     string // absolute, symlink-resolved
	dirName     string // display name shown in the sidebar header
	faviconSeed string
	// openPath is the "home" file rendered at "/", relative to baseDir. It is
	// set when a single file was served (so "/" shows that file) and empty when
	// a directory was served (then "/" redirects to a default file). On re-root
	// it is re-based so it keeps pointing at the originally-served file.
	openPath string
}

type Server struct {
	host          string
	port          int
	openBrowserFn func(url string)

	// root is the current served directory, swapped atomically on "go up".
	root atomic.Pointer[rootState]

	mu sync.Mutex
	// wsClients maps each live connection to its write mutex. gorilla/websocket
	// permits only one concurrent writer per connection and panics
	// ("concurrent write to websocket connection") otherwise; broadcast runs
	// from several goroutines at once (file watcher, comment watcher, HTTP
	// comment handlers), so every write is serialized through the per-conn mutex.
	wsClients map[*websocket.Conn]*sync.Mutex
	// commentStores is keyed by storeKey (inode-derived), NOT file path.
	// Keying by storeKey is what makes the cache correct when an external
	// editor rewrites a file via the write-temp-then-rename idiom: the path
	// stays the same but the inode (and so the storeKey) changes. Each
	// request recomputes the current storeKey, so writes always land in the
	// file the rest of the toolchain reads from.
	commentStores map[string]*CommentStore

	// file tree cache. Rebuilt when the tree changes (treeDirty) or the root
	// changes (treeRoot no longer matches the current baseDir).
	treeMu     sync.Mutex
	cachedTree []FileNode
	treeDirty  bool
	treeRoot   string

	// watcher lifecycle. The directory watcher is stopped and restarted when
	// the root changes; watchStop signals the running watcher to exit.
	watchMu   sync.Mutex
	watchStop chan struct{}
}

// NewServer roots the server at target's directory. A directory target is used
// as-is; a file target roots at its parent and renders that file at "/".
func NewServer(target, host string, port int) *Server {
	s := &Server{
		host:          host,
		port:          port,
		wsClients:     map[*websocket.Conn]*sync.Mutex{},
		commentStores: map[string]*CommentStore{},
	}
	abs, _ := filepath.Abs(target)
	baseDir, openPath := abs, ""
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		baseDir = filepath.Dir(abs)
		openPath = filepath.Base(abs)
	}
	s.setRoot(baseDir, openPath)
	return s
}

// setRoot stores a new served-directory snapshot. baseDir is symlink-resolved so
// requests (which resolve symlinks before the sandbox check) don't 403 when the
// root itself sits under a symlink — e.g. macOS /tmp -> /private/tmp.
func (s *Server) setRoot(baseDir, openPath string) {
	if resolved, err := filepath.EvalSymlinks(baseDir); err == nil {
		baseDir = resolved
	}
	dirName := filepath.Base(baseDir)
	if dirName == "" || dirName == "." {
		dirName = baseDir
	}
	s.root.Store(&rootState{baseDir: baseDir, dirName: dirName, faviconSeed: baseDir, openPath: openPath})
}

// ---------------------------------------------------------------------------
// WebSocket broadcast
// ---------------------------------------------------------------------------

func (s *Server) broadcast(msg map[string]interface{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.Lock()
	type client struct {
		conn *websocket.Conn
		wmu  *sync.Mutex
	}
	clients := make([]client, 0, len(s.wsClients))
	for conn, wmu := range s.wsClients {
		clients = append(clients, client{conn, wmu})
	}
	s.mu.Unlock()
	var failed []*websocket.Conn
	for _, c := range clients {
		// Serialize writes per connection (gorilla panics on concurrent writes)
		// and cap how long a stalled reader can block: without a deadline a
		// backgrounded or dead-but-unclosed tab would wedge every future
		// broadcast behind its write mutex.
		c.wmu.Lock()
		_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := c.conn.WriteMessage(websocket.TextMessage, data)
		c.wmu.Unlock()
		if err != nil {
			failed = append(failed, c.conn)
		}
	}
	if len(failed) > 0 {
		s.mu.Lock()
		for _, conn := range failed {
			delete(s.wsClients, conn)
			conn.Close()
		}
		s.mu.Unlock()
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

func (s *Server) getStoreForFile(fp string) (*CommentStore, error) {
	key := storeKeyForFile(fp)
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.commentStores[key]; ok {
		return store, nil
	}
	store := NewCommentStoreForFile(fp, commentStoreDir())
	s.commentStores[key] = store
	return store, nil
}

func (s *Server) getStore(r *http.Request) (*CommentStore, error) {
	fp := s.fileFromRequest(r)
	if fp == "" {
		return nil, fmt.Errorf("file parameter missing or invalid")
	}
	return s.getStoreForFile(fp)
}

// fileFromRequest resolves the ?file= query param to an absolute path inside the
// current root, rejecting traversal and out-of-root symlinks. When ?file= is
// absent it falls back to the home file (openPath), so serving a single file
// keeps working without an explicit file param.
func (s *Server) fileFromRequest(r *http.Request) string {
	root := s.root.Load()
	rel := r.URL.Query().Get("file")
	if rel == "" {
		rel = root.openPath
	}
	if rel == "" {
		return ""
	}
	fp := filepath.Clean(filepath.Join(root.baseDir, rel))
	relFP, err := filepath.Rel(root.baseDir, fp)
	if err != nil || strings.HasPrefix(relFP, "..") {
		return ""
	}
	if fi, err := os.Stat(fp); err != nil || fi.IsDir() {
		return ""
	}
	// Resolve symlinks and verify the real path stays inside baseDir
	if resolved, err := filepath.EvalSymlinks(fp); err == nil {
		resolvedRel, relErr := filepath.Rel(root.baseDir, resolved)
		if relErr != nil || strings.HasPrefix(resolvedRel, "..") {
			return ""
		}
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
	s.wsClients[conn] = &sync.Mutex{}
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
// HTTP handlers — page serving
// ---------------------------------------------------------------------------

// handleDirRoot serves "/". When a single file was served it renders that file
// (openPath); otherwise it redirects to a sensible default file in the root.
func (s *Server) handleDirRoot(w http.ResponseWriter, r *http.Request) {
	root := s.root.Load()
	if root.openPath != "" {
		s.renderFile(w, r, root.openPath)
		return
	}
	def := findDefaultFile(root.baseDir)
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

// fileMetaJSON returns a JSON object describing the currently-viewed file for
// the sidebar header: its name plus formatted created and modified dates.
// "created" is empty when the platform can't report a birth time.
func fileMetaJSON(fi os.FileInfo) string {
	const layout = "Jan 2, 2006 3:04 PM"
	m := map[string]string{
		"name":     fi.Name(),
		"modified": fi.ModTime().Format(layout),
		"created":  "",
	}
	if bt, ok := fileBirthTime(fi); ok && !bt.IsZero() {
		m["created"] = bt.Format(layout)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (s *Server) handleDirFile(w http.ResponseWriter, r *http.Request) {
	relPath := strings.TrimPrefix(r.URL.Path, "/")
	s.renderFile(w, r, relPath)
}

// renderFile serves relPath (relative to the current root) as an HTML page with
// the sidebar, dispatching on file type. It enforces the root sandbox.
func (s *Server) renderFile(w http.ResponseWriter, r *http.Request, relPath string) {
	root := s.root.Load()
	fp := filepath.Clean(filepath.Join(root.baseDir, relPath))
	relFP, relErr := filepath.Rel(root.baseDir, fp)
	if relErr != nil || strings.HasPrefix(relFP, "..") {
		http.Error(w, "Forbidden", 403)
		return
	}
	// Resolve symlinks and verify the real path stays inside baseDir
	if resolved, err := filepath.EvalSymlinks(fp); err == nil {
		resolvedRel, relErr := filepath.Rel(root.baseDir, resolved)
		if relErr != nil || strings.HasPrefix(resolvedRel, "..") {
			http.Error(w, "Forbidden", 403)
			return
		}
	}
	fi, err := os.Stat(fp)
	if err != nil || fi.IsDir() {
		http.Error(w, "Not Found", 404)
		return
	}

	ext := strings.ToLower(filepath.Ext(fp))
	sidebar := &[2]string{root.dirName, relPath}
	tree := s.fileTree()

	// Raw access. ?dl=1 forces a download (Content-Disposition: attachment) so
	// the sidebar's drag-out reliably materializes a real file in Chromium;
	// plain ?raw=1 (used by inline <img>/<embed>) must NOT force a download.
	if r.URL.Query().Get("raw") == "1" {
		if r.URL.Query().Get("dl") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(fp)))
		}
		http.ServeFile(w, r, fp)
		return
	}

	// Embedded assets served raw when browser doesn't want HTML
	accept := r.Header.Get("Accept")
	if embeddableExts[ext] && !strings.Contains(accept, "text/html") {
		http.ServeFile(w, r, fp)
		return
	}

	metaJS := fileMetaJSON(fi)
	opts := wrapOptions{sidebar: sidebar, fileTree: tree, faviconPath: root.faviconSeed, fileMetaJS: metaJS}

	switch ext {
	case ".md":
		marp := isMarpDoc(fp)
		var htmlStr string
		if marp && r.URL.Query().Get("present") == "1" {
			htmlStr = renderMarp(fp, sidebar, tree, root.faviconSeed)
		} else {
			opts.isMarp = marp
			htmlStr, err = renderMarkdown(fp, opts)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		if coms := commentsForPath(fp); len(coms) > 0 {
			htmlStr = injectCommentAnchors(htmlStr, coms)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlStr)

	case ".html", ".htm":
		data, rerr := os.ReadFile(fp)
		if rerr != nil {
			http.Error(w, rerr.Error(), 500)
			return
		}
		htmlStr := injectReloadScript(string(data), sidebar, tree, root.faviconSeed, metaJS, true, false)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"files": s.fileTree()})
}

// ---------------------------------------------------------------------------
// HTTP handlers — comment API
// ---------------------------------------------------------------------------

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	if s.fileFromRequest(r) == "" {
		writeJSON(w, map[string]interface{}{"comments": []interface{}{}})
		return
	}
	store, err := s.getStore(r)
	if err != nil {
		http.Error(w, "failed to load comments", 500)
		return
	}
	comments, err := store.List()
	if err != nil {
		http.Error(w, "failed to load comments", 500)
		return
	}
	if comments == nil {
		comments = []Comment{}
	}
	writeJSON(w, map[string]interface{}{"comments": comments})
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	if s.fileFromRequest(r) == "" {
		http.Error(w, "file parameter is required", 400)
		return
	}
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
	if strings.TrimSpace(body.Text) == "" {
		http.Error(w, "text is required", 400)
		return
	}
	store, storeErr := s.getStore(r)
	if storeErr != nil {
		http.Error(w, storeErr.Error(), 500)
		return
	}
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
	if s.fileFromRequest(r) == "" {
		http.Error(w, "file parameter is required", 400)
		return
	}
	id := r.PathValue("id")
	var body struct {
		Text     *string `json:"text"`
		Resolved *bool   `json:"resolved"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if body.Text != nil && strings.TrimSpace(*body.Text) == "" {
		http.Error(w, "text cannot be empty", 400)
		return
	}
	store, err := s.getStore(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	comment, err := store.Update(id, body.Text, body.Resolved)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if comment == nil {
		writeJSONError(w, 404, "Not found")
		return
	}
	s.notifyCommentsUpdated()
	writeJSON(w, comment)
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	if s.fileFromRequest(r) == "" {
		http.Error(w, "file parameter is required", 400)
		return
	}
	id := r.PathValue("id")
	store, err := s.getStore(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found, err := store.Delete(id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !found {
		writeJSONError(w, 404, "Not found")
		return
	}
	s.notifyCommentsUpdated()
	writeJSON(w, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// HTTP handlers — edit API
// ---------------------------------------------------------------------------

// resolveTarget returns the absolute file path for an edit/file/preview request,
// taken from the ?file= query param (falling back to the home file).
func (s *Server) resolveTarget(r *http.Request) string {
	return s.fileFromRequest(r)
}

func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	fp := s.resolveTarget(r)
	if fp == "" {
		http.Error(w, "not found", 404)
		return
	}
	if !isEditableFile(fp) {
		http.Error(w, "not editable", 403)
		return
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]string{"content": string(data)})
}

func (s *Server) handleEditFile(w http.ResponseWriter, r *http.Request) {
	fp := s.resolveTarget(r)
	if fp == "" {
		http.Error(w, "not found", 404)
		return
	}
	if !isEditableFile(fp) {
		http.Error(w, "not editable", 403)
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if err := os.WriteFile(fp, []byte(body.Content), 0644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	writeJSON(w, map[string]string{"html": renderMarkdownSource(body.Content)})
}

// ---------------------------------------------------------------------------
// Port finding & startup
// ---------------------------------------------------------------------------

const portSearchRange = 10

func findPort(host string, startPort int) (int, bool) {
	for offset := 0; offset <= portSearchRange; offset++ {
		port := startPort + offset
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		if err == nil {
			ln.Close()
			return port, true
		}
	}
	return 0, false
}

func (s *Server) Start(ctx context.Context) error {
	port, ok := findPort(s.host, s.port)
	if !ok {
		return fmt.Errorf("no available port in range %d–%d", s.port, s.port+portSearchRange)
	}
	s.port = port

	root := s.root.Load()
	si := filepath.Join(root.baseDir, ".serveignore")
	if _, err := os.Stat(si); os.IsNotExist(err) {
		if err := os.WriteFile(si, []byte(defaultServeIgnore), 0644); err == nil {
			fmt.Println("Created .serveignore")
		}
	}

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
	mux.HandleFunc("/api/reroot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleReroot(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/file", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.handleGetFile(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/edit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handleEditFile(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	mux.HandleFunc("/api/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.handlePreview(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			s.handleDirRoot(w, r)
			return
		}
		s.handleDirFile(w, r)
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.host, port),
		Handler: mux,
	}

	url := fmt.Sprintf("http://%s:%d", s.host, port)
	fmt.Printf("Serving %s at %s\n", root.baseDir, url)
	fmt.Println("Press Ctrl+C to stop")

	if s.openBrowserFn != nil {
		go s.openBrowserFn(url)
	}

	// Start watchers
	s.rewatch()
	go func() {
		if err := watchComments(s.notifyCommentsUpdated); err != nil {
			fmt.Fprintf(os.Stderr, "comment watcher: %v\n", err)
		}
	}()

	// Shutdown on context cancel
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Directory watcher lifecycle (restartable so "go up" can re-root)
// ---------------------------------------------------------------------------

// rewatch stops any running directory watcher and starts a fresh one on the
// current root. Called once at startup and again after each re-root.
func (s *Server) rewatch() {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watchStop != nil {
		close(s.watchStop)
	}
	stop := make(chan struct{})
	s.watchStop = stop
	baseDir := s.root.Load().baseDir
	go func() {
		err := watchDirectory(baseDir, stop, func() {
			s.treeMu.Lock()
			s.treeDirty = true
			s.treeMu.Unlock()
			s.notifyReload()
			s.notifyFileTree(s.fileTree())
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "watcher: %v\n", err)
		}
	}()
}

// handleReroot re-roots the server one directory up and reports the old root's
// base name so the client can rewrite its URL to keep viewing the same file.
// serve binds to localhost, so this only widens what the local server exposes.
func (s *Server) handleReroot(w http.ResponseWriter, r *http.Request) {
	cur := s.root.Load()
	parent := filepath.Dir(cur.baseDir)
	if parent == cur.baseDir { // already at the filesystem root
		writeJSON(w, map[string]interface{}{"ok": false, "atRoot": true})
		return
	}
	prefix := filepath.Base(cur.baseDir)
	// Re-base the home file so "/" keeps pointing at the originally-served file.
	openPath := cur.openPath
	if openPath != "" {
		openPath = prefix + "/" + openPath
	}
	s.setRoot(parent, openPath)
	s.treeMu.Lock()
	s.treeDirty = true
	s.treeMu.Unlock()
	s.rewatch()
	newRoot := s.root.Load()
	writeJSON(w, map[string]interface{}{"ok": true, "prefix": prefix, "dirName": newRoot.dirName})
}

// ---------------------------------------------------------------------------
// Comment anchor injection
// ---------------------------------------------------------------------------

func commentsForPath(fp string) []Comment {
	store := NewCommentStoreForFile(fp, commentStoreDir())
	comments, _ := store.List()
	return comments
}

// injectCommentAnchors inserts an invisible span marker before each comment's
// anchor text in the rendered HTML. The JS uses these markers to find the
// exact anchor position without text searching, which is reliable even when
// multiple elements share the same source-line annotation (e.g. table cells).
// Longer anchor texts are processed first to prevent shorter substrings from
// splitting a longer match. Matches inside <script>/<style> blocks are
// skipped — injecting a span there would break the embedded JS/CSS (e.g.
// anchor "new" hitting `var ws = new WebSocket(...)` in the reload script).
func injectCommentAnchors(rendered string, comments []Comment) string {
	sorted := make([]Comment, len(comments))
	copy(sorted, comments)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].AnchorText) > len(sorted[j].AnchorText)
	})
	for _, c := range sorted {
		if c.AnchorText == "" {
			continue
		}
		// Goldmark HTML-escapes &, <, >, " in text content. Try escaped first.
		search := htmlEscape(c.AnchorText)
		if strings.Index(rendered, search) < 0 {
			search = c.AnchorText
		}

		// If we have a source line, start the search from the first element
		// annotated at that line. This picks the right occurrence when the same
		// anchor text appears on multiple lines of the document.
		startPos := 0
		if c.SourceLineStart != nil && *c.SourceLineStart > 0 {
			lineHint := fmt.Sprintf(`data-source-lines="%d-`, *c.SourceLineStart)
			if hp := strings.Index(rendered, lineHint); hp >= 0 {
				startPos = hp
			}
		}

		// Recompute skip regions each iteration — previously-inserted markers
		// shift offsets — and find the first match outside any of them.
		skip := scriptStyleRegions(rendered)
		idx := findOutsideRegions(rendered, search, startPos, skip)
		if idx < 0 {
			continue
		}
		marker := `<span data-comment-anchor="` + c.ID + `" style="display:none"></span>`
		rendered = rendered[:idx] + marker + rendered[idx:]
	}
	return rendered
}

// scriptStyleRegions returns [start, end) byte ranges that span the *body* of
// every <script> and <style> element in html. Tag names are matched
// case-insensitively. Unterminated tags are treated as extending to EOF.
func scriptStyleRegions(html string) [][2]int {
	lower := strings.ToLower(html)
	var regions [][2]int
	for _, tag := range []string{"script", "style"} {
		open := "<" + tag
		closeTag := "</" + tag + ">"
		pos := 0
		for {
			s := strings.Index(lower[pos:], open)
			if s < 0 {
				break
			}
			s += pos
			gt := strings.IndexByte(lower[s:], '>')
			if gt < 0 {
				break
			}
			bodyStart := s + gt + 1
			c := strings.Index(lower[bodyStart:], closeTag)
			end := len(html)
			if c >= 0 {
				end = bodyStart + c
			}
			regions = append(regions, [2]int{bodyStart, end})
			pos = end + len(closeTag)
			if pos > len(html) {
				pos = len(html)
			}
		}
	}
	return regions
}

func findOutsideRegions(haystack, needle string, start int, regions [][2]int) int {
	pos := start
	for {
		rel := strings.Index(haystack[pos:], needle)
		if rel < 0 {
			return -1
		}
		abs := pos + rel
		inside := false
		for _, r := range regions {
			if abs >= r[0] && abs < r[1] {
				pos = r[1]
				inside = true
				break
			}
		}
		if !inside {
			return abs
		}
	}
}

// ---------------------------------------------------------------------------
// JSON helper
// ---------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ---------------------------------------------------------------------------
// Directory file tree cache
// ---------------------------------------------------------------------------

func (s *Server) fileTree() []FileNode {
	baseDir := s.root.Load().baseDir
	s.treeMu.Lock()
	defer s.treeMu.Unlock()
	// Rebuild when the tree changed (treeDirty) or the root moved (treeRoot).
	// .serveignore is reloaded on every rebuild so live edits to it take effect.
	if s.treeDirty || s.treeRoot != baseDir {
		s.cachedTree = buildFileTree(baseDir, "", loadServeIgnore(baseDir))
		s.treeDirty = false
		s.treeRoot = baseDir
	}
	return s.cachedTree
}
