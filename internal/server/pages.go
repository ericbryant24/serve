package server

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"serve/internal/comments"
	"serve/internal/paths"
	"serve/internal/render"
	"serve/internal/store"
)

// Every page is the same shell: the app's script and styles, plus the page's
// data as JSON. A document's rendered HTML is in that data too, so the first
// paint does not wait for a second request.

type crumb struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Opened bool   `json:"opened"`
}

type rootInfo struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Name    string `json:"name"`
	Display string `json:"display"`
}

type topRef struct {
	Key string `json:"key"`
	Src string `json:"src"`
}

type docData struct {
	Kind        string           `json:"kind"`
	Rev         string           `json:"rev"`
	Title       string           `json:"title"`
	HTML        string           `json:"html,omitempty"`
	Tops        []topRef         `json:"tops,omitempty"`
	HasMermaid  bool             `json:"has_mermaid,omitempty"`
	IsMarp      bool             `json:"is_marp,omitempty"`
	Editable    bool             `json:"editable,omitempty"`
	Lang        string           `json:"lang,omitempty"`
	Modified    string           `json:"modified,omitempty"`
	Size        int64            `json:"size"`
	Headings    []render.Heading `json:"headings,omitempty"`
	Raw         string           `json:"raw"`
	Embed       string           `json:"embed,omitempty"`
	Frontmatter bool             `json:"frontmatter,omitempty"`
}

type relinkCandidate struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Display string `json:"display"`
	Threads int    `json:"threads"`
}

type pageData struct {
	View          string            `json:"view"`
	Version       string            `json:"version"`
	Assets        string            `json:"assets"`
	User          string            `json:"user"`
	Role          string            `json:"role"`
	Path          string            `json:"path,omitempty"`
	URL           string            `json:"url,omitempty"`
	Display       string            `json:"display,omitempty"`
	Name          string            `json:"name,omitempty"`
	Root          *rootInfo         `json:"root,omitempty"`
	Crumbs        []crumb           `json:"crumbs,omitempty"`
	Doc           *docData          `json:"doc,omitempty"`
	Threads       []apiThread       `json:"threads"`
	Cursor        int64             `json:"cursor"`
	Entries       []entry           `json:"entries,omitempty"`
	Readme        *docData          `json:"readme,omitempty"`
	Suggestions   []suggestion      `json:"suggestions,omitempty"`
	Nearest       string            `json:"nearest,omitempty"`
	NearestURL    string            `json:"nearest_url,omitempty"`
	Relink        []relinkCandidate `json:"relink,omitempty"`
	Home          *homeData         `json:"home,omitempty"`
	ContentOrigin string            `json:"content_origin,omitempty"`
	Embedded      bool              `json:"embedded,omitempty"`
	Message       string            `json:"message,omitempty"`
}

func (s *Server) basePage(r *http.Request, view string, rl role) *pageData {
	cursor, _ := s.store.LastSeq()
	d := &pageData{View: view, Version: s.opt.Version, Assets: s.opt.Assets.Hash(), User: s.cfg.Name, Cursor: cursor, Threads: []apiThread{}}
	d.Role = "owner"
	if rl == roleGuest {
		d.Role = "guest"
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	d.ContentOrigin = "http://" + net.JoinHostPort(host, strconv.Itoa(s.contentPort))
	d.Embedded = r.URL.Query().Get("embed") == "1"
	return d
}

func (s *Server) crumbs(p string) []crumb {
	var out []crumb
	home := paths.Abs(paths.Home())
	for d := p; ; d = filepath.Dir(d) {
		name := filepath.Base(d)
		if d == home {
			name = "~"
		}
		if d == filepath.Dir(d) {
			break // the filesystem root needs no crumb of its own
		}
		_, opened := s.folders.rootFor(d)
		out = append([]crumb{{Name: name, URL: paths.URLPath(d), Opened: opened}}, out...)
		if d == home {
			break
		}
	}
	return out
}

func (s *Server) rootInfo(root string) *rootInfo {
	return &rootInfo{Path: root, URL: paths.URLPath(root), Name: filepath.Base(root), Display: paths.Display(root)}
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	rl := s.pageRole(r)
	if rl == roleNone {
		http.Error(w, "This serve only answers requests from this computer, or with a share link.", http.StatusForbidden)
		return
	}
	if rl == roleGuest && r.URL.Query().Get("share") != "" {
		http.SetCookie(w, &http.Cookie{Name: "serve_share", Value: s.shareToken, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
	if r.URL.Path == "/" {
		if rl != roleOwner {
			s.writePage(w, http.StatusForbidden, s.messagePage(r, rl, "Only this computer can see the start page."), "serve")
			return
		}
		d := s.basePage(r, "home", rl)
		d.Home = s.homeData()
		s.writePage(w, 200, d, "serve")
		return
	}
	p, ok := paths.FromURLPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p = paths.Abs(p)
	root, ok := s.folders.rootFor(p)
	if !ok {
		if rl != roleOwner {
			http.Error(w, "not shared", http.StatusForbidden)
			return
		}
		d := s.basePage(r, "closed", rl)
		d.Path, d.URL, d.Display, d.Name = p, paths.URLPath(p), paths.Display(p), filepath.Base(p)
		d.Crumbs = s.crumbs(p)
		near := p
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			near = nearestDir(p)
		}
		d.Nearest, d.NearestURL = paths.Display(near), paths.URLPath(near)
		s.writePage(w, http.StatusForbidden, d, filepath.Base(p))
		return
	}
	s.folders.st.TouchFolder(root)
	q := r.URL.Query()
	fi, err := os.Stat(p)
	if err != nil {
		if q.Has("raw") || !acceptsHTML(r) {
			http.NotFound(w, r)
			return
		}
		s.writePage(w, http.StatusNotFound, s.notFoundPage(r, rl, root, p), filepath.Base(p)+" — not found")
		return
	}
	if fi.IsDir() {
		s.writePage(w, 200, s.dirPage(r, rl, root, p), filepath.Base(p))
		return
	}
	if q.Has("raw") || !acceptsHTML(r) {
		if q.Get("dl") == "1" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(p)))
		}
		serveFile(w, r, p, fi)
		return
	}
	if q.Get("present") == "1" && render.IsMarkdown(p) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(renderMarp(p)))
		return
	}
	d, err := s.docPage(r, rl, root, p, fi)
	if err != nil {
		s.writePage(w, 500, s.messagePage(r, rl, "Could not read this file: "+err.Error()), filepath.Base(p))
		return
	}
	title := filepath.Base(p)
	if d.Doc != nil && d.Doc.Title != "" && d.Doc.Kind == render.KindMarkdown {
		title = d.Doc.Title
	}
	s.writePage(w, 200, d, title)
}

func acceptsHTML(r *http.Request) bool {
	a := r.Header.Get("Accept")
	return a == "" || strings.Contains(a, "text/html")
}

func (s *Server) messagePage(r *http.Request, rl role, msg string) *pageData {
	d := s.basePage(r, "message", rl)
	d.Message = msg
	return d
}

func editable(kind string, size int64) bool {
	return (kind == render.KindMarkdown || kind == render.KindCode || kind == render.KindText || kind == render.KindHTML) && size <= 2<<20
}

func (s *Server) docData(p string, doc *render.Doc, fi os.FileInfo, withHTML bool) *docData {
	dd := &docData{
		Kind: doc.Kind, Rev: doc.Rev, Title: doc.Title, HasMermaid: doc.HasMermaid, IsMarp: doc.IsMarp,
		Lang: doc.Lang, Headings: doc.Headings, Raw: paths.URLPath(p) + "?raw=1", Frontmatter: len(doc.Frontmatter) > 0,
	}
	if fi != nil {
		dd.Size = fi.Size()
		dd.Modified = fi.ModTime().UTC().Format(time.RFC3339)
		dd.Editable = editable(doc.Kind, fi.Size())
	}
	if withHTML {
		dd.HTML = doc.HTML
	}
	for _, t := range doc.Tops {
		dd.Tops = append(dd.Tops, topRef{Key: t.Key, Src: t.Src})
	}
	if doc.Kind == render.KindHTML {
		dd.Embed = paths.URLPath(p) + "?frame=1"
	}
	return dd
}

func (s *Server) docPage(r *http.Request, rl role, root, p string, fi os.FileInfo) (*pageData, error) {
	doc, fi2, err := s.readDoc(p)
	if err != nil {
		return nil, err
	}
	if fi2 != nil {
		fi = fi2
	}
	dv, err := s.svc.OpenRendered(p, doc, false)
	if err != nil {
		return nil, err
	}
	s.files.seed(p, doc)
	d := s.basePage(r, "doc", rl)
	d.Path, d.URL, d.Display, d.Name = p, paths.URLPath(p), paths.Display(p), filepath.Base(p)
	d.Root = s.rootInfo(root)
	d.Crumbs = s.crumbs(p)
	d.Doc = s.docData(p, doc, fi, true)
	d.Threads = s.apiThreads(dv)
	if len(dv.Threads) == 0 && rl == roleOwner {
		d.Relink = s.relinkCandidates(p, doc)
	}
	return d, nil
}

func (s *Server) dirPage(r *http.Request, rl role, root, p string) *pageData {
	d := s.basePage(r, "dir", rl)
	d.Path, d.URL, d.Display, d.Name = p, paths.URLPath(p), paths.Display(p), filepath.Base(p)
	d.Root = s.rootInfo(root)
	d.Crumbs = s.crumbs(p)
	d.Entries, _ = s.listDir(root, p)
	if rm := readmeIn(p); rm != "" {
		if doc, fi, err := s.readDoc(rm); err == nil && doc.Kind == render.KindMarkdown {
			d.Readme = s.docData(rm, doc, fi, true)
			d.Readme.Title = filepath.Base(rm)
		}
	}
	return d
}

func (s *Server) notFoundPage(r *http.Request, rl role, root, p string) *pageData {
	d := s.basePage(r, "notfound", rl)
	d.Path, d.URL, d.Display, d.Name = p, paths.URLPath(p), paths.Display(p), filepath.Base(p)
	d.Root = s.rootInfo(root)
	d.Crumbs = s.crumbs(p)
	d.Suggestions = s.moveSuggestions(root, p)
	near := nearestDir(p)
	if !paths.Within(near, root) {
		near = root
	}
	d.Nearest, d.NearestURL = paths.Display(near), paths.URLPath(near)
	d.Entries, _ = s.listDir(root, near)
	return d
}

// relinkCandidates are documents with comments whose files are gone and that
// look like this file: the same name, or mostly the same text.
func (s *Server) relinkCandidates(p string, doc *render.Doc) []relinkCandidate {
	docs, err := s.store.Documents()
	if err != nil {
		return nil
	}
	base := strings.ToLower(filepath.Base(p))
	var out []relinkCandidate
	for i, d := range docs {
		if i > 400 || d.Path == p {
			continue
		}
		if _, err := os.Stat(d.Path); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		match := strings.ToLower(filepath.Base(d.Path)) == base
		if !match && doc.Source != "" && d.ContentHash != "" {
			if old, ok := s.store.Revision(d.ID, d.ContentHash); ok {
				match = similarText(old, doc.Source) >= 0.6
			}
		}
		if !match {
			continue
		}
		ths, _ := s.store.Threads(d.ID)
		if len(ths) == 0 {
			continue
		}
		out = append(out, relinkCandidate{ID: d.ID, Path: d.Path, Display: paths.Display(d.Path), Threads: len(ths)})
	}
	return out
}

func similarText(a, b string) float64 {
	wa, wb := map[string]int{}, map[string]int{}
	for _, w := range strings.Fields(strings.ToLower(a)) {
		wa[w]++
	}
	for _, w := range strings.Fields(strings.ToLower(b)) {
		wb[w]++
	}
	shared, total := 0, 0
	for w, n := range wa {
		total += n
		shared += min(n, wb[w])
	}
	for _, n := range wb {
		total += n
	}
	if total == 0 {
		return 0
	}
	return 2 * float64(shared) / float64(total)
}

// --- the start page --------------------------------------------------------------------

type homeFolder struct {
	Path    string `json:"path"`
	URL     string `json:"url"`
	Display string `json:"display"`
	Name    string `json:"name"`
	Exists  bool   `json:"exists"`
	Used    string `json:"used"`
}

type homeDoc struct {
	Path     string `json:"path"`
	URL      string `json:"url"`
	Display  string `json:"display"`
	Name     string `json:"name"`
	Open     int    `json:"open"`
	Awaiting int    `json:"awaiting"`
	Updated  string `json:"updated"`
}

type inboxItem struct {
	ThreadID string        `json:"thread_id"`
	Path     string        `json:"path"`
	URL      string        `json:"url"`
	Display  string        `json:"display"`
	Quote    string        `json:"quote,omitempty"`
	Scope    string        `json:"scope"`
	Last     store.Message `json:"last"`
	Count    int           `json:"count"`
}

type homeData struct {
	Folders []homeFolder `json:"folders"`
	Recent  []homeDoc    `json:"recent"`
	Inbox   []inboxItem  `json:"inbox"`
}

func (s *Server) homeData() *homeData {
	h := &homeData{Folders: []homeFolder{}, Recent: []homeDoc{}, Inbox: []inboxItem{}}
	fs, _ := s.store.Folders()
	for _, f := range fs {
		_, err := os.Stat(f.Path)
		h.Folders = append(h.Folders, homeFolder{Path: f.Path, URL: paths.URLPath(f.Path), Display: paths.Display(f.Path), Name: filepath.Base(f.Path), Exists: err == nil, Used: f.LastUsed})
	}
	open, _ := s.store.OpenThreads()
	byDoc := map[string][]*store.Thread{}
	for _, t := range open {
		byDoc[t.DocID] = append(byDoc[t.DocID], t)
	}
	recent, _ := s.store.RecentDocuments(30)
	for _, d := range recent {
		hd := homeDoc{Path: d.Path, URL: paths.URLPath(d.Path), Display: paths.Display(d.Path), Name: filepath.Base(d.Path), Updated: d.LastSeen}
		for _, t := range byDoc[d.ID] {
			hd.Open++
			if t.Awaiting() == store.Human {
				hd.Awaiting++
			}
		}
		if _, err := os.Stat(d.Path); err != nil && hd.Open == 0 {
			continue
		}
		h.Recent = append(h.Recent, hd)
	}
	for _, t := range open {
		if t.Awaiting() != store.Human || len(t.Messages) == 0 {
			continue
		}
		d, err := s.store.DocumentByID(t.DocID)
		if err != nil {
			continue
		}
		q := t.Anchor.Display
		if q == "" {
			q = t.Anchor.Quote
		}
		h.Inbox = append(h.Inbox, inboxItem{ThreadID: t.ID, Path: d.Path, URL: paths.URLPath(d.Path), Display: paths.Display(d.Path), Quote: q, Scope: t.Scope, Last: t.Messages[len(t.Messages)-1], Count: len(t.Messages)})
	}
	return h
}

// --- threads as the browser sees them ----------------------------------------------------

type apiMessage struct {
	store.Message
	HTML string `json:"html"`
}

type apiThread struct {
	comments.ThreadView
	Messages []apiMessage `json:"messages"`
}

func messageHTML(body string) string {
	return render.Markdown(body, render.MarkdownOptions{NoBlockAttrs: true}).HTML
}

func (s *Server) apiThreads(dv *comments.DocView) []apiThread {
	out := make([]apiThread, 0, len(dv.Threads))
	for _, v := range dv.Threads {
		out = append(out, toAPIThread(v))
	}
	return out
}

func toAPIThread(v comments.ThreadView) apiThread {
	at := apiThread{ThreadView: v}
	for _, m := range v.Messages {
		at.Messages = append(at.Messages, apiMessage{Message: m, HTML: messageHTML(m.Body)})
	}
	if at.Messages == nil {
		at.Messages = []apiMessage{}
	}
	if v.Anchor.Legacy != nil {
		// Old-store matching data is of no use to the page.
		a := v.Anchor
		a.Legacy = nil
		t := *v.Thread
		t.Anchor = a
		at.Thread = &t
	}
	return at
}

// --- the shell --------------------------------------------------------------------------

// frameAncestors lets apps on this machine (the threads desk, say) show
// serve in a frame, by port or by a *.localhost name (threads.localhost,
// through a local proxy), and stops any other site from framing it to trick
// a click. Browsers send *.localhost only to this machine.
const frameAncestors = "frame-ancestors 'self' http://localhost:* http://127.0.0.1:* http://*.localhost:*"

var themeBoot = `(function(){try{var t=localStorage.getItem('serve-theme');if(t==='light'||t==='dark')document.documentElement.setAttribute('data-theme',t);}catch(e){}})();`

func (s *Server) writePage(w http.ResponseWriter, status int, d *pageData, title string) {
	data, err := json.Marshal(d)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	v := s.opt.Assets.Hash()
	token := s.token
	if d.Role == "guest" {
		token = s.shareToken
	}
	icon := ""
	if d.Root != nil {
		icon = favicon(d.Root.Path)
	} else {
		icon = favicon("serve")
	}
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString("<title>" + stdhtml.EscapeString(title) + "</title>\n")
	b.WriteString(`<meta name="serve-token" content="` + token + `">` + "\n")
	b.WriteString(`<link rel="icon" href="` + icon + `">` + "\n")
	b.WriteString("<script>" + themeBoot + "</script>\n")
	b.WriteString(`<link rel="stylesheet" href="/_serve/assets/app.css?v=` + v + `">` + "\n")
	b.WriteString(`<link rel="stylesheet" href="/_serve/assets/chroma.css?v=` + v + `">` + "\n")
	b.WriteString("</head>\n<body>\n<div id=\"app\"></div>\n")
	b.WriteString(`<script id="serve-data" type="application/json">`)
	b.Write(data)
	b.WriteString("</script>\n")
	b.WriteString(`<script type="module" src="/_serve/assets/app.js?v=` + v + `"></script>` + "\n")
	b.WriteString("</body>\n</html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", frameAncestors)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

var faviconEmojis = []string{"📘", "📕", "📗", "📙", "📓", "📝", "📋", "📄", "📃", "📜", "📰", "📚", "📖", "📔", "🔬", "🧪", "🧬", "🔭", "💡", "🎨", "🌈", "🔥", "💧", "🌱", "🚀", "🛸", "🌍", "🌊", "🌋", "🎲", "🎯", "🎮", "🦁", "🦅", "🦉", "🐙", "🦋"}
var faviconColors = []string{"#264653", "#2a9d8f", "#e9c46a", "#f4a261", "#e76f51", "#606c38", "#283618", "#dda15e", "#bc6c25", "#6d6875", "#b5838d", "#e5989b", "#457b9d", "#1d3557", "#a8dadc", "#2b2d42", "#8d99ae", "#ef233c"}

// favicon gives each opened folder its own tab icon, so tabs from different
// folders are told apart at a glance.
func favicon(seed string) string {
	h := md5.Sum([]byte(seed))
	n := int(h[0])<<8 | int(h[1])
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" rx="22" fill="%s"/><text x="50" y="70" font-size="58" text-anchor="middle">%s</text></svg>`,
		faviconColors[n%len(faviconColors)], faviconEmojis[(n>>4)%len(faviconEmojis)])
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
}
