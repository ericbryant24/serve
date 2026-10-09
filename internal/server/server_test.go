package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serve/internal/comments"
	"serve/internal/config"
	"serve/internal/paths"
	"serve/internal/store"
)

type fakeAssets struct{}

func (fakeAssets) Open(name string) ([]byte, bool) {
	if name == "embed.js" || name == "app.js" {
		return []byte("//" + name), true
	}
	return nil, false
}
func (fakeAssets) Hash() string { return "test" }

type env struct {
	t    *testing.T
	s    *Server
	app  http.Handler
	cont http.Handler
	dir  string
	doc  string
}

func setup(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, err := store.Open(filepath.Join(home, ".serve", "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := paths.Abs(t.TempDir())
	doc := filepath.Join(dir, "spec.md")
	os.WriteFile(doc, []byte("# Spec\n\nWe retry up to three times.\n\nSecond paragraph.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "page.html"), []byte("<html><body><p>Hello <b>there</b></p></body></html>"), 0o644)
	if err := st.AddFolder(dir); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Port: 7070, Version: "test", Store: st, Config: config.Config{Name: "Eric"}, Assets: fakeAssets{}, NoDaemonFile: true})
	s.port, s.contentPort = 7070, 7071
	return &env{t: t, s: s, app: s.AppHandler(), cont: s.ContentHandler(), dir: dir, doc: doc}
}

type req struct {
	method, path, body, ctype, origin, host, token, remote string
}

func (e *env) do(h http.Handler, r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = "GET"
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	hr.Host = "localhost:7070"
	if r.host != "" {
		hr.Host = r.host
	}
	hr.RemoteAddr = "127.0.0.1:50000"
	if r.remote != "" {
		hr.RemoteAddr = r.remote
	}
	if r.body != "" {
		ct := r.ctype
		if ct == "" {
			ct = "application/json"
		}
		hr.Header.Set("Content-Type", ct)
	}
	if r.origin != "" {
		hr.Header.Set("Origin", r.origin)
	}
	if r.token != "-" {
		tok := r.token
		if tok == "" {
			tok = e.s.Token()
		}
		hr.Header.Set("X-Serve-Token", tok)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hr)
	return w
}

func (e *env) api(method, path string, body any) map[string]any {
	e.t.Helper()
	var b string
	if body != nil {
		j, _ := json.Marshal(body)
		b = string(j)
	}
	w := e.do(e.app, req{method: method, path: "/_serve/api/" + path, body: b})
	if w.Code >= 300 {
		e.t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

func TestRequestsFromOtherSitesAreRefused(t *testing.T) {
	e := setup(t)
	edit := `{"path":"` + e.doc + `","content":"pwned","base_rev":""}`
	cases := []struct {
		name string
		r    req
		want int
	}{
		{"no token", req{method: "PUT", path: "/_serve/api/file", body: edit, token: "-"}, 403},
		{"wrong token", req{method: "PUT", path: "/_serve/api/file", body: edit, token: "nope"}, 403},
		{"text/plain body", req{method: "PUT", path: "/_serve/api/file", body: edit, ctype: "text/plain"}, 415},
		{"another site's origin", req{method: "PUT", path: "/_serve/api/file", body: edit, origin: "https://evil.example"}, 403},
		{"dns rebinding host", req{path: "/_serve/api/home", host: "evil.example:7070"}, 403},
		{"remote client with the owner token", req{path: "/_serve/api/home", remote: "10.0.0.5:4000"}, 403},
		{"event stream without token", req{path: "/_serve/events", token: "-"}, 403},
		{"health needs nothing", req{path: "/_serve/health", token: "-"}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if w := e.do(e.app, c.r); w.Code != c.want {
				t.Fatalf("got %d, want %d: %s", w.Code, c.want, w.Body.String())
			}
		})
	}
	if b, _ := os.ReadFile(e.doc); string(b) == "pwned" {
		t.Fatal("the file was overwritten")
	}
}

func TestPagesOnlyServeOpenedFolders(t *testing.T) {
	e := setup(t)
	outside := filepath.Join(paths.Abs(t.TempDir()), "secret.md")
	os.WriteFile(outside, []byte("secret"), 0o644)
	if w := e.do(e.app, req{path: paths.URLPath(outside) + "?raw=1", token: "-"}); w.Code != 403 {
		t.Fatalf("file outside the opened folders: %d", w.Code)
	}
	if w := e.do(e.cont, req{path: paths.URLPath(outside), token: "-", host: "localhost:7071"}); w.Code != 403 {
		t.Fatalf("content port, outside: %d", w.Code)
	}
	w := e.do(e.app, req{path: paths.URLPath(e.doc), token: "-"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="serve-token" content="`+e.s.Token()) {
		t.Fatalf("page: %d", w.Code)
	}
	if w := e.do(e.app, req{path: paths.URLPath(e.doc), token: "-", remote: "10.0.0.5:4000"}); w.Code != 403 {
		t.Fatalf("remote page without a share link: %d", w.Code)
	}
}

// With a folder above several projects open, each project still gets its own
// tab icon: the icon follows the git repository a page is in, else the
// narrowest opened folder holding it.
func TestTabIconFollowsTheProject(t *testing.T) {
	e := setup(t)
	page := func(dir string) string {
		p := filepath.Join(dir, "docs", "x.md")
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("# X\n"), 0o644)
		return p
	}
	a, b, plain := filepath.Join(e.dir, "a"), filepath.Join(e.dir, "b"), filepath.Join(e.dir, "plain")
	os.MkdirAll(filepath.Join(a, ".git"), 0o755)
	os.MkdirAll(b, 0o755)
	os.WriteFile(filepath.Join(b, ".git"), []byte("gitdir: elsewhere\n"), 0o644) // a worktree
	icon := func(p string) string {
		w := e.do(e.app, req{path: paths.URLPath(p), token: "-"})
		_, rest, ok := strings.Cut(w.Body.String(), `<link rel="icon" href="`)
		if w.Code != 200 || !ok {
			t.Fatalf("%s: %d, no icon", p, w.Code)
		}
		href, _, _ := strings.Cut(rest, `"`)
		return href
	}
	inA, inB := icon(page(a)), icon(page(b))
	if inA != icon(a) {
		t.Error("a project's folder and its files have different icons")
	}
	if inA != favicon(a) || inB != favicon(b) {
		t.Error("the icon is not the project's")
	}
	if icon(page(plain)) != favicon(e.dir) {
		t.Error("outside any repository, the icon is not the opened folder's")
	}
	if err := e.s.folders.add(plain); err != nil {
		t.Fatal(err)
	}
	if icon(page(plain)) != favicon(plain) {
		t.Error("the narrowest opened folder does not pick the icon")
	}
}

func TestContentPortNeverServesTheAppOrTheAPI(t *testing.T) {
	e := setup(t)
	for _, p := range []string{"/_serve/api/home", "/_serve/events", "/_serve/assets/app.js"} {
		w := e.do(e.cont, req{path: p, host: "localhost:7071"})
		if w.Code == 200 && !strings.Contains(w.Body.String(), "not open") {
			t.Fatalf("%s on the content port answered %d: %s", p, w.Code, w.Body.String())
		}
	}
	page := filepath.Join(e.dir, "page.html")
	w := e.do(e.cont, req{path: paths.URLPath(page) + "?frame=1", host: "localhost:7071", token: "-"})
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "/_serve/embed.js") || strings.Contains(body, e.s.Token()) {
		t.Fatalf("embedded page: %d %s", w.Code, body)
	}
	if w := e.do(e.cont, req{method: "POST", path: paths.URLPath(page), host: "localhost:7071", body: "{}"}); w.Code != 405 {
		t.Fatalf("POST to the content port: %d", w.Code)
	}
}

func selection(t *testing.T, e *env, phrase string) comments.Selection {
	t.Helper()
	doc, _, err := e.s.readDoc(e.doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.Leaves() {
		if i := strings.Index(b.Text, phrase); i >= 0 {
			return comments.Selection{StartKey: b.Key, StartOffset: i, EndKey: b.Key, EndOffset: i + len(phrase), Quote: phrase}
		}
	}
	t.Fatalf("%q not found", phrase)
	return comments.Selection{}
}

func TestThreadAPI(t *testing.T) {
	e := setup(t)
	th := e.api("POST", "threads", map[string]any{"path": e.doc, "scope": "text", "text": "Why three?", "selection": selection(t, e, "up to three times")})
	id := th["id"].(string)
	if th["awaiting"] != "agent" || th["location"].(map[string]any)["line_start"].(float64) != 3 {
		t.Fatalf("created: %v", th)
	}
	msgs := th["messages"].([]any)
	if !strings.Contains(msgs[0].(map[string]any)["html"].(string), "Why three?") {
		t.Fatalf("message html missing")
	}
	r := e.api("POST", "threads/"+id+"/messages", map[string]any{"text": "It is **four** now."})
	if len(r["messages"].([]any)) != 2 {
		t.Fatalf("reply not in thread")
	}
	r = e.api("PATCH", "threads/"+id, map[string]any{"status": "resolved"})
	if r["status"] != "resolved" {
		t.Fatalf("not resolved: %v", r["status"])
	}
	list := e.api("GET", "threads?path="+paths.URLPath(e.doc), nil)
	if len(list["threads"].([]any)) != 1 {
		t.Fatalf("list: %v", list)
	}
	page := e.api("POST", "threads", map[string]any{"path": e.doc, "scope": "page", "text": "Overall fine"})
	e.api("DELETE", "threads/"+page["id"].(string), nil)
	if w := e.do(e.app, req{method: "PATCH", path: "/_serve/api/threads/zzzzzzzz", body: `{"status":"open"}`}); w.Code != 404 {
		t.Fatalf("unknown thread: %d", w.Code)
	}
}

func TestStaleSelectionIsReported(t *testing.T) {
	e := setup(t)
	w := e.do(e.app, req{method: "POST", path: "/_serve/api/threads", body: `{"path":"` + e.doc + `","scope":"text","text":"x","selection":{"start_key":"nope","start_offset":0,"end_key":"nope","end_offset":3,"quote":"not in the document"}}`})
	if w.Code != 409 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
}

func TestSaveRefusesToOverwriteAChangedFile(t *testing.T) {
	e := setup(t)
	f := e.api("GET", "file?path="+paths.URLPath(e.doc), nil)
	base := f["rev"].(string)
	os.WriteFile(e.doc, []byte("# Spec\n\nChanged by the agent.\n"), 0o644)
	w := e.do(e.app, req{method: "PUT", path: "/_serve/api/file", body: `{"path":"` + e.doc + `","content":"mine","base_rev":"` + base + `"}`})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Changed by the agent") {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	m := e.api("POST", "merge", map[string]any{"base": "a\nb\nc\n", "mine": "a\nB\nc\n", "theirs": "a\nb\nc\nd\n"})
	if m["merged"] != "a\nB\nc\nd\n" || m["clean"] != true {
		t.Fatalf("merge: %v", m)
	}
}

func TestTreeAndSearch(t *testing.T) {
	e := setup(t)
	os.MkdirAll(filepath.Join(e.dir, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(e.dir, "node_modules", "x", "y.md"), []byte("x"), 0o644)
	tree := e.api("GET", "tree?path="+paths.URLPath(e.dir), nil)
	var names []string
	for _, x := range tree["entries"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "page.html,spec.md" {
		t.Fatalf("tree: %v", names)
	}
	res := e.api("GET", "search?root="+paths.URLPath(e.dir)+"&q=spc", nil)
	if r := res["results"].([]any); len(r) != 1 || r[0].(map[string]any)["name"] != "spec.md" {
		t.Fatalf("search: %v", res)
	}
}

func TestShareLinksAllowCommentingButNotEditing(t *testing.T) {
	e := setup(t)
	remote := "10.0.0.5:4000"
	w := e.do(e.app, req{path: paths.URLPath(e.doc) + "?share=" + e.s.shareToken, token: "-", remote: remote})
	if w.Code != 200 || !strings.Contains(w.Body.String(), e.s.shareToken) || strings.Contains(w.Body.String(), e.s.Token()) {
		t.Fatalf("shared page: %d", w.Code)
	}
	body, _ := json.Marshal(map[string]any{"path": e.doc, "scope": "page", "text": "guest note"})
	if w := e.do(e.app, req{method: "POST", path: "/_serve/api/threads", body: string(body), token: e.s.shareToken, remote: remote}); w.Code != 201 {
		t.Fatalf("guest comment: %d %s", w.Code, w.Body.String())
	}
	if w := e.do(e.app, req{method: "PUT", path: "/_serve/api/file", body: `{"path":"` + e.doc + `","content":"x"}`, token: e.s.shareToken, remote: remote}); w.Code != 403 {
		t.Fatalf("guest edit: %d", w.Code)
	}
}

func TestEventStreamSendsThreadsWhenAnotherProcessReplies(t *testing.T) {
	e := setup(t)
	th := e.api("POST", "threads", map[string]any{"path": e.doc, "scope": "page", "text": "hello"})
	srv := httptest.NewServer(e.app)
	defer srv.Close()
	ctx := t.Context()
	go e.s.pollStore(ctx)
	rq, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/_serve/events?path="+paths.URLPath(e.doc)+"&token="+e.s.Token(), nil)
	rq.Host = "localhost:7070"
	resp, err := http.DefaultClient.Do(rq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if !bytes.Contains(buf[:n], []byte("event: hello")) {
		t.Fatalf("no hello: %s", buf[:n])
	}
	// Another process (the CLI) replies through its own store handle.
	st2, _ := store.Open(e.s.store.Path)
	defer st2.Close()
	st2.Reply(th["id"].(string), store.Author{Kind: store.Agent, Name: "Claude"}, "agent reply", "")
	got := ""
	for i := 0; i < 20 && !strings.Contains(got, "agent reply"); i++ {
		n, err := resp.Body.Read(buf)
		got += string(buf[:n])
		if err == io.EOF {
			break
		}
	}
	if !strings.Contains(got, "event: threads") || !strings.Contains(got, "agent reply") {
		t.Fatalf("no threads event: %s", got)
	}
}
