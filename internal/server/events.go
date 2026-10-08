package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"serve/internal/logx"
	"serve/internal/paths"
	"serve/internal/render"
)

// Live updates travel over server-sent events: one stream per open tab,
// subscribed to the document it shows. A file change sends only the blocks
// that changed; a comment change (from the browser, or from an agent's
// `serve reply` in another process) sends the document's threads.

type sseMsg struct {
	event string
	data  []byte
}

type client struct {
	id   string
	doc  string // absolute path of the document shown, "" for other pages
	home bool
	mu   sync.Mutex
	dirs map[string]bool
	// assets are the files the document loads (images, stylesheets), and
	// assetDirs the folders watched for them; nil once the tab has gone.
	assets    map[string]bool
	assetDirs map[string]bool
	ch        chan sseMsg
	done      chan struct{}
}

type hub struct {
	mu      sync.Mutex
	clients map[string]*client
	seq     int64
}

func newHub() *hub { return &hub{clients: map[string]*client{}} }

func (h *hub) add(c *client) {
	h.mu.Lock()
	if old, ok := h.clients[c.id]; ok {
		close(old.done)
	}
	h.clients[c.id] = c
	h.mu.Unlock()
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	if cur, ok := h.clients[c.id]; ok && cur == c {
		delete(h.clients, c.id)
	}
	h.mu.Unlock()
}

func (h *hub) get(id string) *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clients[id]
}

func (h *hub) each(f func(*client)) {
	h.mu.Lock()
	list := make([]*client, 0, len(h.clients))
	for _, c := range h.clients {
		list = append(list, c)
	}
	h.mu.Unlock()
	for _, c := range list {
		f(c)
	}
}

func (h *hub) closeAll() {
	h.each(func(c *client) {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
	})
}

func (c *client) send(event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.ch <- sseMsg{event, b}:
	default:
		// A tab that has stopped reading gets a resync when it catches up
		// rather than an unbounded queue.
		select {
		case c.ch <- sseMsg{"resync", []byte("{}")}:
		default:
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.apiRole(r) == roleNone {
		http.Error(w, "missing or wrong token", http.StatusForbidden)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	q := r.URL.Query()
	c := &client{id: q.Get("client"), ch: make(chan sseMsg, 64), done: make(chan struct{}), dirs: map[string]bool{}, assetDirs: map[string]bool{}}
	if c.id == "" {
		c.id = randToken()[:12]
	}
	if p, ok := pathFromQuery(q.Get("path")); ok {
		if _, ok := s.folders.rootFor(p); ok {
			c.doc = p
		}
	}
	c.home = q.Get("home") == "1"
	s.hub.add(c)
	defer s.hub.remove(c)
	if c.doc != "" {
		s.files.watch(filepath.Dir(c.doc))
		defer s.files.unwatch(filepath.Dir(c.doc))
		if doc, _, err := s.readDoc(c.doc); err == nil {
			s.trackAssets(c, doc)
		}
	}
	defer func() {
		c.mu.Lock()
		dirs, assetDirs := c.dirs, c.assetDirs
		c.dirs, c.assetDirs = nil, nil
		c.mu.Unlock()
		for d := range dirs {
			s.files.unwatch(d)
		}
		for d := range assetDirs {
			s.files.unwatch(d)
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	write := func(m sseMsg) error {
		s.hub.mu.Lock()
		s.hub.seq++
		id := s.hub.seq
		s.hub.mu.Unlock()
		_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, m.event, m.data)
		fl.Flush()
		return err
	}
	cursor, _ := s.store.LastSeq()
	hello, _ := json.Marshal(map[string]any{"version": s.opt.Version, "assets": s.opt.Assets.Hash(), "client": c.id, "cursor": cursor})
	if err := write(sseMsg{"hello", hello}); err != nil {
		return
	}
	// A reconnecting tab missed whatever happened while it was away.
	if r.Header.Get("Last-Event-ID") != "" {
		_ = write(sseMsg{"resync", []byte("{}")})
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.done:
			return
		case m := <-c.ch:
			if write(m) != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// watchTreeDir registers a folder a tab's file tree is showing, so the tab
// hears about files appearing and disappearing in it.
func (s *Server) watchTreeDir(clientID, dir string) {
	c := s.hub.get(clientID)
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.dirs == nil || c.dirs[dir] {
		c.mu.Unlock()
		return
	}
	c.dirs[dir] = true
	c.mu.Unlock()
	s.files.watch(dir)
}

// trackAssets records the files a tab's document loads and watches the
// folders they are in, so a changed image reaches the tab.
func (s *Server) trackAssets(c *client, doc *render.Doc) {
	assets := s.assetFiles(c.doc, doc)
	dirs := map[string]bool{}
	for a := range assets {
		d := filepath.Dir(a)
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			dirs[d] = true
		}
	}
	// Held throughout, so a tab closing meanwhile cannot leave a folder watched.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.assetDirs == nil {
		return
	}
	for d := range dirs {
		if !c.assetDirs[d] {
			s.files.watch(d)
		}
	}
	for d := range c.assetDirs {
		if !dirs[d] {
			s.files.unwatch(d)
		}
	}
	c.assets, c.assetDirs = assets, dirs
}

// assetFiles resolves the URLs a document loads to files in opened folders.
func (s *Server) assetFiles(p string, doc *render.Doc) map[string]bool {
	out := map[string]bool{}
	for _, ref := range doc.Refs {
		u, err := url.Parse(ref)
		if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
			continue
		}
		var a string
		if strings.HasPrefix(u.Path, "/") {
			q, ok := paths.FromURLPath(u.Path)
			if !ok {
				continue
			}
			a = paths.Abs(q)
		} else {
			a = paths.Abs(filepath.Join(filepath.Dir(p), filepath.FromSlash(u.Path)))
		}
		if _, ok := s.folders.rootFor(a); ok && a != p {
			out[a] = true
		}
	}
	return out
}

func (c *client) loads(p string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.assets[p]
}

// --- file watching -----------------------------------------------------------------

type fileWatcher struct {
	s        *Server
	mu       sync.Mutex
	w        *fsnotify.Watcher
	refs     map[string]int
	timers   map[string]*time.Timer
	lastTops map[string][]render.Top
	lastRev  map[string]string
}

func newFileWatcher(s *Server) *fileWatcher {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		logx.Error("file watching is unavailable", err)
	}
	return &fileWatcher{s: s, w: w, refs: map[string]int{}, timers: map[string]*time.Timer{}, lastTops: map[string][]render.Top{}, lastRev: map[string]string{}}
}

func (f *fileWatcher) watch(dir string) {
	if f.w == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refs[dir]++
	if f.refs[dir] == 1 {
		if err := f.w.Add(dir); err != nil {
			logx.Error("could not watch a folder", err, logx.Path("dir", dir))
		}
	}
}

func (f *fileWatcher) unwatch(dir string) {
	if f.w == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refs[dir] == 0 {
		return
	}
	f.refs[dir]--
	if f.refs[dir] == 0 {
		delete(f.refs, dir)
		_ = f.w.Remove(dir)
	}
}

// seed records what a page was rendered from, so the first change after it
// sends only what differs.
func (f *fileWatcher) seed(p string, doc *render.Doc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.lastRev[p]; !ok {
		f.lastRev[p] = doc.Rev
		f.lastTops[p] = doc.Tops
	}
}

func (f *fileWatcher) debounce(key string, d time.Duration, fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.timers[key]; ok {
		t.Stop()
	}
	f.timers[key] = time.AfterFunc(d, func() {
		f.mu.Lock()
		delete(f.timers, key)
		f.mu.Unlock()
		fn()
	})
}

func (f *fileWatcher) run(ctx context.Context) {
	if f.w == nil {
		return
	}
	defer f.w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-f.w.Events:
			if !ok {
				return
			}
			name := filepath.Clean(ev.Name)
			base := filepath.Base(name)
			if strings.Contains(base, ".serve-tmp-") {
				continue
			}
			dir := filepath.Dir(name)
			watchedDoc, asset := false, false
			f.s.hub.each(func(c *client) {
				if c.doc == name {
					watchedDoc = true
				}
				if c.loads(name) {
					asset = true
				}
			})
			if watchedDoc {
				f.debounce("doc:"+name, 60*time.Millisecond, func() { f.s.refreshDoc(name) })
			}
			if asset && ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				f.debounce("asset:"+name, 120*time.Millisecond, func() { f.s.assetChanged(name) })
			}
			if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				f.debounce("tree:"+dir, 120*time.Millisecond, func() { f.s.treeChanged(dir) })
			}
		case err, ok := <-f.w.Errors:
			if !ok {
				return
			}
			logx.Error("file watcher error", err)
		}
	}
}

// refreshDoc re-renders a changed document and tells the tabs showing it.
func (s *Server) refreshDoc(p string) {
	doc, _, err := s.readDoc(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.hub.each(func(c *client) {
				if c.doc == p {
					c.send("gone", map[string]any{"path": p})
				}
			})
		}
		return
	}
	f := s.files
	f.mu.Lock()
	prevRev := f.lastRev[p]
	prevTops := f.lastTops[p]
	f.mu.Unlock()
	if doc.Rev == prevRev {
		return
	}
	dv, err := s.svc.OpenRendered(p, doc, false)
	if err != nil {
		logx.Error("could not place comments after a change", err)
		return
	}
	known := map[string]bool{}
	for _, t := range prevTops {
		known[t.Key] = true
	}
	prevSrc := map[string]bool{}
	for _, t := range prevTops {
		prevSrc[t.Src] = true
	}
	type topOut struct {
		Key     string `json:"key"`
		HTML    string `json:"html,omitempty"`
		Changed bool   `json:"changed,omitempty"`
	}
	tops := make([]topOut, len(doc.Tops))
	for i, t := range doc.Tops {
		tops[i] = topOut{Key: t.Key}
		if !known[t.Key] {
			tops[i].HTML = t.HTML
		}
		if prevTops != nil && !prevSrc[t.Src] {
			tops[i].Changed = true
		}
	}
	cursor, _ := s.store.LastSeq()
	payload := map[string]any{
		"rev": doc.Rev, "from": prevRev, "kind": doc.Kind, "tops": tops, "has_mermaid": doc.HasMermaid,
		"threads": s.apiThreads(dv), "cursor": cursor,
	}
	f.mu.Lock()
	f.lastRev[p] = doc.Rev
	f.lastTops[p] = doc.Tops
	f.mu.Unlock()
	s.hub.each(func(c *client) {
		if c.doc == p {
			s.trackAssets(c, doc)
			c.send("doc", payload)
		}
	})
}

// assetChanged tells the tabs whose document loads a file that it changed.
func (s *Server) assetChanged(p string) {
	msg := map[string]any{"url": paths.URLPath(p)}
	s.hub.each(func(c *client) {
		if c.loads(p) {
			c.send("asset", msg)
		}
	})
}

func (s *Server) treeChanged(dir string) {
	s.hub.each(func(c *client) {
		c.mu.Lock()
		watching := c.dirs[dir]
		c.mu.Unlock()
		if watching {
			c.send("tree", map[string]any{"dir": dir})
		}
	})
}

// pollStore watches the event log for changes made by any process and sends
// each affected tab its document's threads.
func (s *Server) pollStore(ctx context.Context) {
	last, _ := s.store.LastSeq()
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur, err := s.store.LastSeq()
		if err != nil || cur <= last {
			continue
		}
		evs, err := s.store.Events(last, "", 2000)
		if err != nil {
			continue
		}
		last = cur
		docs := map[string]bool{}
		for _, e := range evs {
			docs[e.DocID] = true
		}
		for id := range docs {
			d, err := s.store.DocumentByID(id)
			if err != nil {
				continue
			}
			var tabs []*client
			s.hub.each(func(c *client) {
				if c.doc == d.Path {
					tabs = append(tabs, c)
				}
			})
			if len(tabs) == 0 {
				continue
			}
			doc, _, err := s.readDoc(d.Path)
			if err != nil {
				continue
			}
			dv, err := s.svc.OpenRendered(d.Path, doc, false)
			if err != nil {
				continue
			}
			payload := map[string]any{"threads": s.apiThreads(dv), "cursor": cur, "rev": doc.Rev}
			for _, c := range tabs {
				c.send("threads", payload)
			}
		}
		s.hub.each(func(c *client) {
			c.send("counts", map[string]any{"cursor": cur})
		})
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
