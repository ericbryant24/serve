package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"serve/internal/anchor"
	"serve/internal/comments"
	"serve/internal/paths"
	"serve/internal/render"
	"serve/internal/store"
)

// registerAPI wires /_serve/api/. Every route needs the token; the ones that
// change files or the server need the owner's.
func (s *Server) registerAPI(mux *http.ServeMux) {
	guest := func(h http.HandlerFunc) http.HandlerFunc { return s.withRole(roleGuest, h) }
	owner := func(h http.HandlerFunc) http.HandlerFunc { return s.withRole(roleOwner, h) }

	mux.HandleFunc("GET /_serve/api/doc", guest(s.apiDoc))
	mux.HandleFunc("GET /_serve/api/threads", guest(s.apiThreadsList))
	mux.HandleFunc("POST /_serve/api/threads", guest(s.apiCreateThread))
	mux.HandleFunc("POST /_serve/api/threads/{id}/messages", guest(s.apiReply))
	mux.HandleFunc("PATCH /_serve/api/threads/{id}", guest(s.apiSetStatus))
	mux.HandleFunc("DELETE /_serve/api/threads/{id}", guest(s.apiDeleteThread))
	mux.HandleFunc("PATCH /_serve/api/messages/{id}", guest(s.apiEditMessage))
	mux.HandleFunc("DELETE /_serve/api/messages/{id}", guest(s.apiDeleteMessage))
	mux.HandleFunc("GET /_serve/api/tree", guest(s.apiTree))
	mux.HandleFunc("GET /_serve/api/search", guest(s.apiSearch))
	mux.HandleFunc("GET /_serve/api/changes", guest(s.apiChanges))
	mux.HandleFunc("POST /_serve/api/preview", guest(s.apiPreview))

	mux.HandleFunc("GET /_serve/api/home", owner(s.apiHome))
	mux.HandleFunc("GET /_serve/api/file", owner(s.apiGetFile))
	mux.HandleFunc("PUT /_serve/api/file", owner(s.apiPutFile))
	mux.HandleFunc("POST /_serve/api/merge", owner(s.apiMerge))
	mux.HandleFunc("POST /_serve/api/folders", owner(s.apiOpenFolder))
	mux.HandleFunc("DELETE /_serve/api/folders", owner(s.apiForgetFolder))
	mux.HandleFunc("POST /_serve/api/relink", owner(s.apiRelink))
	mux.HandleFunc("POST /_serve/api/reveal", owner(s.apiReveal))
	mux.HandleFunc("POST /_serve/api/shutdown", owner(s.apiShutdown))
	mux.HandleFunc("GET /_serve/api/status", owner(s.apiStatus))
	s.registerReportAPI(mux, owner)
}

func (s *Server) withRole(need role, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := s.apiRole(r)
		if got < need {
			if got == roleNone {
				writeError(w, http.StatusForbidden, "missing or wrong token")
			} else {
				writeError(w, http.StatusForbidden, "not allowed with a share link")
			}
			return
		}
		if r.Method != http.MethodGet && r.ContentLength != 0 {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") && !strings.HasPrefix(ct, "multipart/form-data") {
				writeError(w, http.StatusUnsupportedMediaType, "request body must be JSON")
				return
			}
		}
		h(w, r)
	}
}

func (s *Server) author(r *http.Request) store.Author {
	if s.apiRole(r) == roleGuest {
		name := strings.TrimSpace(r.Header.Get("X-Serve-Name"))
		if name == "" {
			name = "guest"
		}
		return store.Author{Kind: store.Human, Name: name}
	}
	return store.Author{Kind: store.Human, Name: s.cfg.Name}
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20)).Decode(v)
}

// docPath reads and checks the "path" a request names: it must be inside an
// opened folder.
func (s *Server) docPath(w http.ResponseWriter, v string) (string, bool) {
	p, ok := pathFromQuery(v)
	if !ok {
		writeError(w, 400, "path is required")
		return "", false
	}
	if _, ok := s.folders.rootFor(p); !ok {
		writeError(w, 403, "that folder is not open in serve")
		return "", false
	}
	return p, true
}

func (s *Server) threadsPayload(p string) (map[string]any, error) {
	doc, _, err := s.readDoc(p)
	if err != nil {
		return nil, err
	}
	dv, err := s.svc.OpenRendered(p, doc, false)
	if err != nil {
		return nil, err
	}
	cursor, _ := s.store.LastSeq()
	return map[string]any{"threads": s.apiThreads(dv), "cursor": cursor, "rev": doc.Rev}, nil
}

func (s *Server) apiDoc(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	doc, fi, err := s.readDoc(p)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	s.files.seed(p, doc)
	dv, err := s.svc.OpenRendered(p, doc, false)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	cursor, _ := s.store.LastSeq()
	writeJSON(w, 200, map[string]any{"doc": s.docData(p, doc, fi, true), "threads": s.apiThreads(dv), "cursor": cursor})
}

func (s *Server) apiThreadsList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	out, err := s.threadsPayload(p)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiCreateThread(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path      string               `json:"path"`
		Scope     string               `json:"scope"`
		Text      string               `json:"text"`
		Selection *comments.Selection  `json:"selection"`
		Element   *comments.ElementRef `json:"element"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := s.docPath(w, body.Path)
	if !ok {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeError(w, 400, "comment text is required")
		return
	}
	a := s.author(r)
	var v *comments.ThreadView
	var err error
	switch body.Scope {
	case store.ScopePage:
		v, err = s.svc.CreatePage(p, a, body.Text)
	case store.ScopeElement:
		if body.Element == nil {
			writeError(w, 400, "an element comment needs the element")
			return
		}
		v, err = s.svc.CreateElement(p, *body.Element, a, body.Text)
	case store.ScopeText, "":
		if body.Selection == nil {
			writeError(w, 400, "a text comment needs the selection")
			return
		}
		v, err = s.svc.CreateText(p, *body.Selection, a, body.Text)
	default:
		writeError(w, 400, "scope must be text, element or page")
		return
	}
	if errors.Is(err, comments.ErrStale) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, toAPIThread(*v))
}

func (s *Server) threadView(id string) (*apiThread, error) {
	t, err := s.store.Thread(id)
	if err != nil {
		return nil, err
	}
	d, err := s.store.DocumentByID(t.DocID)
	if err != nil {
		return nil, err
	}
	doc, _, err := s.readDoc(d.Path)
	if err != nil {
		v := comments.ThreadView{Thread: t, Awaiting: t.Awaiting()}
		at := toAPIThread(v)
		return &at, nil
	}
	dv, err := s.svc.OpenRendered(d.Path, doc, false)
	if err != nil {
		return nil, err
	}
	for _, v := range dv.Threads {
		if v.ID == id {
			at := toAPIThread(v)
			return &at, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not found")
	default:
		writeError(w, 500, err.Error())
	}
}

func (s *Server) apiReply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeError(w, 400, "reply text is required")
		return
	}
	if _, err := s.svc.Reply(r.PathValue("id"), s.author(r), body.Text); err != nil {
		s.storeError(w, err)
		return
	}
	t, err := s.threadView(r.PathValue("id"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 201, t)
}

func (s *Server) apiSetStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	if _, err := s.store.SetStatus(r.PathValue("id"), body.Status, s.author(r)); err != nil {
		s.storeError(w, err)
		return
	}
	t, err := s.threadView(r.PathValue("id"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) apiDeleteThread(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteThread(r.PathValue("id"), s.author(r)); err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiEditMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Text) == "" {
		writeError(w, 400, "text cannot be empty")
		return
	}
	m, err := s.store.EditMessage(r.PathValue("id"), s.author(r), body.Text)
	if err != nil {
		s.storeError(w, err)
		return
	}
	t, err := s.threadView(m.ThreadID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) apiDeleteMessage(w http.ResponseWriter, r *http.Request) {
	gone, err := s.store.DeleteMessage(r.PathValue("id"), s.author(r))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "thread_deleted": gone})
}

func (s *Server) apiTree(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	root, _ := s.folders.rootFor(p)
	entries, err := s.listDir(root, p)
	if err != nil {
		writeError(w, 404, "not a folder")
		return
	}
	if id := r.URL.Query().Get("client"); id != "" {
		s.watchTreeDir(id, p)
	}
	if entries == nil {
		entries = []entry{}
	}
	writeJSON(w, 200, map[string]any{"entries": entries})
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("root"))
	if !ok {
		return
	}
	res := s.searchFiles(p, r.URL.Query().Get("q"), 60)
	if res == nil {
		res = []entry{}
	}
	writeJSON(w, 200, map[string]any{"results": res})
}

// apiChanges compares the document as it is now with the revision the
// viewer's latest comment was written against, block by block.
func (s *Server) apiChanges(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	doc, _, err := s.readDoc(p)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	d, err := s.store.Document(p, false)
	if err != nil {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	rev, at, ok := s.store.LastHumanRev(d.ID)
	if !ok {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	oldText, ok := s.store.Revision(d.ID, rev)
	if !ok {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	if rev == doc.Rev {
		writeJSON(w, 200, map[string]any{"available": true, "since": at, "blocks": []any{}, "unchanged": true})
		return
	}
	old := render.Render(p, []byte(oldText), s.cfg.RenderOptions(p))
	writeJSON(w, 200, map[string]any{"available": true, "since": at, "blocks": diffTops(old.Tops, doc.Tops)})
}

type changeBlock struct {
	Kind string `json:"kind"` // same | added | removed
	HTML string `json:"html"`
}

// diffTops lines up two renders' top-level blocks by their source and marks
// what was added and removed.
func diffTops(a, b []render.Top) []changeBlock {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i].Src == b[j].Src {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []changeBlock
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i].Src == b[j].Src:
			out = append(out, changeBlock{"same", b[j].HTML})
			i++
			j++
		case j < m && (i >= n || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, changeBlock{"added", b[j].HTML})
			j++
		default:
			out = append(out, changeBlock{"removed", a[i].HTML})
			i++
		}
	}
	return out
}

func (s *Server) apiPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := s.docPath(w, body.Path)
	if !ok {
		return
	}
	doc := render.Render(p, []byte(body.Content), s.cfg.RenderOptions(p))
	writeJSON(w, 200, map[string]any{"html": doc.HTML, "kind": doc.Kind, "has_mermaid": doc.HasMermaid})
}

func (s *Server) apiHome(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.homeData())
}

func (s *Server) apiGetFile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.docPath(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	writeJSON(w, 200, map[string]string{"content": string(data), "rev": anchor.Hash(string(data))})
}

// apiPutFile saves an edit. It refuses to overwrite a file that changed
// since the editor loaded it, unless told to, and hands back what is on disk
// so the editor can offer a merge.
func (s *Server) apiPutFile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		BaseRev string `json:"base_rev"`
		Force   bool   `json:"force"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := s.docPath(w, body.Path)
	if !ok {
		return
	}
	cur, err := os.ReadFile(p)
	if err != nil {
		writeError(w, 404, "not found")
		return
	}
	if fi, err := os.Stat(p); err == nil && !editable(render.KindOf(p, cur), fi.Size()) {
		writeError(w, 403, "this file cannot be edited in serve")
		return
	}
	curRev := anchor.Hash(string(cur))
	if !body.Force && body.BaseRev != "" && body.BaseRev != curRev {
		writeJSON(w, 409, map[string]string{"error": "the file changed on disk", "content": string(cur), "rev": curRev})
		return
	}
	if err := writeFileAtomic(p, []byte(body.Content)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"rev": anchor.Hash(body.Content)})
}

// apiMerge applies the editor's changes (base to mine) on top of what is on
// disk now (theirs).
func (s *Server) apiMerge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Base   string `json:"base"`
		Mine   string `json:"mine"`
		Theirs string `json:"theirs"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	dmp := diffmatchpatch.New()
	patches := dmp.PatchMake(body.Base, body.Mine)
	merged, applied := dmp.PatchApply(patches, body.Theirs)
	failed := 0
	for _, ok := range applied {
		if !ok {
			failed++
		}
	}
	writeJSON(w, 200, map[string]any{"merged": merged, "clean": failed == 0, "failed": failed})
}

func (s *Server) apiOpenFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := pathFromQuery(body.Path)
	if !ok {
		writeError(w, 400, "path is required")
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		writeError(w, 404, "no such folder")
		return
	}
	if !fi.IsDir() {
		p = filepath.Dir(p)
	}
	if err := s.folders.add(p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": p, "url": paths.URLPath(p)})
}

func (s *Server) apiForgetFolder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, _ := pathFromQuery(body.Path)
	if err := s.folders.remove(p); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiRelink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := s.docPath(w, body.Path)
	if !ok {
		return
	}
	to, err := s.store.Document(p, true)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := s.store.Relink(body.From, to.ID, s.author(r)); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	out, err := s.threadsPayload(p)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiReveal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, 400, "bad request")
		return
	}
	p, ok := s.docPath(w, body.Path)
	if !ok {
		return
	}
	if err := reveal(p); err != nil {
		writeError(w, 501, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func reveal(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		if fi.IsDir() {
			return exec.Command("open", p).Start()
		}
		return exec.Command("open", "-R", p).Start()
	case "windows":
		return exec.Command("explorer", "/select,", p).Start()
	default:
		d := p
		if !fi.IsDir() {
			d = filepath.Dir(p)
		}
		return exec.Command("xdg-open", d).Start()
	}
}

func (s *Server) apiShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"ok": true})
	if s.shutdown != nil {
		go s.shutdown()
	}
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	fs, _ := s.store.Folders()
	open := 0
	s.hub.each(func(*client) { open++ })
	writeJSON(w, 200, map[string]any{"version": s.opt.Version, "pid": os.Getpid(), "port": s.port, "content_port": s.contentPort, "folders": fs, "tabs": open, "started": s.started})
}
