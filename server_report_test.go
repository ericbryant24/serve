package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func reportMux(t *testing.T, host string) (*http.ServeMux, *Server) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s := &Server{host: host, wsClients: map[*websocket.Conn]*sync.Mutex{}}
	mux := http.NewServeMux()
	s.registerReportRoutes(mux)
	return mux, s
}

func do(mux *http.ServeMux, method, path, remote string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestIsLoopbackHost(t *testing.T) {
	yes := []string{"", "localhost", "LocalHost", "127.0.0.1", "::1", "[::1]", "127.5.5.5"}
	no := []string{"0.0.0.0", "192.168.1.10", "example.com", "10.0.0.1", "::"}
	for _, h := range yes {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false", h)
		}
	}
	for _, h := range no {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true", h)
		}
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	yes := []string{"127.0.0.1:5555", "[::1]:5555", "127.0.0.1"}
	no := []string{"192.168.1.9:5555", "10.1.2.3:80", "", "not-an-addr"}
	for _, a := range yes {
		if !isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = false", a)
		}
	}
	for _, a := range no {
		if isLoopbackAddr(a) {
			t.Errorf("isLoopbackAddr(%q) = true", a)
		}
	}
}

// A report holds a screenshot of the served document. On a non-loopback bind
// these routes would be a remote screenshot endpoint, so every one of them has
// to refuse. This test must never be relaxed.
func TestReportAPIRefusedOnNonLoopbackBind(t *testing.T) {
	mux, _ := reportMux(t, "0.0.0.0")

	routes := []struct{ method, path string }{
		{"GET", "/api/report"},
		{"POST", "/api/report"},
		{"GET", "/api/report/env"},
		{"GET", "/api/report/aabbccddeeff"},
		{"PATCH", "/api/report/aabbccddeeff"},
		{"DELETE", "/api/report/aabbccddeeff"},
		{"POST", "/api/report/aabbccddeeff/attachment"},
		{"GET", "/api/report/aabbccddeeff/attachment/112233445566"},
		{"POST", "/api/report/aabbccddeeff/file"},
		{"POST", "/api/report/aabbccddeeff/reveal"},
	}
	for _, r := range routes {
		w := do(mux, r.method, r.path, "127.0.0.1:1234", "")
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", r.method, r.path, w.Code)
		}
	}
}

func TestReportAPIRefusedForRemoteClient(t *testing.T) {
	mux, _ := reportMux(t, "localhost")
	w := do(mux, "GET", "/api/report", "192.168.1.44:5555", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("remote client got %d, want 403", w.Code)
	}
}

func TestReportAPIAllowedOverLoopback(t *testing.T) {
	mux, _ := reportMux(t, "localhost")
	w := do(mux, "GET", "/api/report", "127.0.0.1:5555", "")
	if w.Code != http.StatusOK {
		t.Fatalf("loopback client got %d, want 200", w.Code)
	}
}

func TestReportAPIRemoteOverride(t *testing.T) {
	t.Setenv("SERVE_REPORT_ALLOW_REMOTE", "1")
	mux, _ := reportMux(t, "0.0.0.0")
	w := do(mux, "GET", "/api/report", "192.168.1.44:5555", "")
	if w.Code != http.StatusOK {
		t.Fatalf("override got %d, want 200", w.Code)
	}
}

func TestReportAPIRoundTrip(t *testing.T) {
	mux, _ := reportMux(t, "localhost")
	const local = "127.0.0.1:5555"

	w := do(mux, "POST", "/api/report", local,
		`{"kind":"bug","title":"Overflow","body":"the table runs over","view_kind":"markdown","with_log":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var rv reportReview
	if err := json.Unmarshal(w.Body.Bytes(), &rv); err != nil {
		t.Fatal(err)
	}
	id := rv.Report.ID
	if len(rv.Attachments) != 1 || rv.Attachments[0].Kind != AttachLog {
		t.Fatalf("expected one log attachment, got %+v", rv.Attachments)
	}
	if rv.Attachments[0].Included {
		t.Error("the log was included without being asked for")
	}
	if !strings.Contains(rv.Markdown, "the table runs over") {
		t.Error("markdown does not contain the body")
	}

	// Upload a screenshot.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("kind", AttachScreenshot)
	mw.WriteField("mode", CaptureStructural)
	fw, _ := mw.CreateFormFile("file", "screenshot.png")
	fw.Write([]byte("\x89PNG fake"))
	mw.Close()

	req := httptest.NewRequest("POST", "/api/report/"+id+"/attachment", &buf)
	req.RemoteAddr = local
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("attachment upload = %d: %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &rv)
	if len(rv.Attachments) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(rv.Attachments))
	}

	var shot attachmentReview
	for _, a := range rv.Attachments {
		if a.Kind == AttachScreenshot {
			shot = a
		}
	}
	if shot.ID == "" {
		t.Fatal("screenshot attachment not found")
	}
	if shot.Included {
		t.Error("the screenshot was included without being asked for")
	}

	// Opt it in; the markdown should then list it.
	w = do(mux, "PATCH", "/api/report/"+id, local,
		`{"include":"`+shot.ID+`","included":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch = %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &rv)
	if !strings.Contains(rv.Markdown, shot.File) {
		t.Error("markdown does not list the included attachment")
	}

	// The attachment bytes come back for the preview pane.
	w = do(mux, "GET", "/api/report/"+id+"/attachment/"+shot.ID, local, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "PNG fake") {
		t.Fatalf("attachment fetch = %d, body %q", w.Code, w.Body.String())
	}

	w = do(mux, "DELETE", "/api/report/"+id, local, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
	w = do(mux, "GET", "/api/report/"+id, local, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", w.Code)
	}
}

func TestReportCreateRequiresTitle(t *testing.T) {
	mux, _ := reportMux(t, "localhost")
	w := do(mux, "POST", "/api/report", "127.0.0.1:5555", `{"kind":"bug","title":"   "}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("blank title = %d, want 400", w.Code)
	}
}

func TestReportReviewSurfacesSecrets(t *testing.T) {
	mux, _ := reportMux(t, "localhost")
	w := do(mux, "POST", "/api/report", "127.0.0.1:5555",
		`{"kind":"bug","title":"auth broken","body":"tried with ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d", w.Code)
	}
	var rv reportReview
	json.Unmarshal(w.Body.Bytes(), &rv)
	if rv.SecretCount == 0 {
		t.Fatal("a github token in the body was not flagged")
	}
	if len(rv.BodySecrets) == 0 || rv.BodySecrets[0].Kind != "github-token" {
		t.Fatalf("body secrets = %+v", rv.BodySecrets)
	}
}

func TestFilingBlockedByPolicy(t *testing.T) {
	t.Setenv("SERVE_REPORT_UPLOAD", "never")
	mux, _ := reportMux(t, "localhost")
	const local = "127.0.0.1:5555"

	w := do(mux, "POST", "/api/report", local, `{"kind":"bug","title":"x"}`)
	var rv reportReview
	json.Unmarshal(w.Body.Bytes(), &rv)
	if rv.UploadAllowed {
		t.Error("review says upload is allowed under the never policy")
	}
	if rv.UploadBlocked == "" {
		t.Error("no reason given for the block")
	}

	w = do(mux, "POST", "/api/report/"+rv.Report.ID+"/file", local, `{}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("file under never policy = %d, want 403", w.Code)
	}
}

// withoutClientID simulates a build where no OAuth App has been registered.
// Clearing the env var is not enough once a client id is compiled in.
func withoutClientID(t *testing.T) {
	t.Helper()
	t.Setenv("SERVE_GITHUB_CLIENT_ID", "")
	prev := ghClientID
	ghClientID = ""
	t.Cleanup(func() { ghClientID = prev })
}

// stubGitHub points every handler-initiated GitHub call at h. Handler tests
// must call this: without it a filing test would hit the real API.
func stubGitHub(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := newGHClientFn
	newGHClientFn = func() *ghClient {
		return &ghClient{
			deviceCodeURL:  srv.URL + "/login/device/code",
			accessTokenURL: srv.URL + "/login/oauth/access_token",
			apiBase:        srv.URL,
			http:           srv.Client(),
			sleep:          func(time.Duration) {},
		}
	}
	t.Cleanup(func() { newGHClientFn = prev; srv.Close() })
}

func TestFilingWithoutClientIDIsExplained(t *testing.T) {
	withoutClientID(t)
	mux, _ := reportMux(t, "localhost")
	const local = "127.0.0.1:5555"

	w := do(mux, "POST", "/api/report", local, `{"kind":"bug","title":"x"}`)
	var rv reportReview
	json.Unmarshal(w.Body.Bytes(), &rv)

	w = do(mux, "POST", "/api/report/"+rv.Report.ID+"/file", local, `{}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("file without a client id = %d, want 501", w.Code)
	}
	if !strings.Contains(w.Body.String(), "serve report export") {
		t.Errorf("the error should point at the export fallback, got %q", w.Body.String())
	}
}

// The filing handler is a two-call dance: the first returns a code to show the
// reporter, the second blocks until they approve and then creates the issue.
func TestFileReportDeviceFlowThenIssue(t *testing.T) {
	approved := false
	stubGitHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			writeJSONResp(w, map[string]interface{}{
				"device_code": "dc", "user_code": "WDJB-MJHT",
				"verification_uri": "https://github.com/login/device",
				"expires_in":       900, "interval": 1,
			})
		case "/login/oauth/access_token":
			if !approved {
				approved = true
				writeJSONResp(w, map[string]string{"error": "authorization_pending"})
				return
			}
			writeJSONResp(w, map[string]string{"access_token": "gho_x"})
		default: // issue creation
			w.WriteHeader(201)
			writeJSONResp(w, map[string]string{"html_url": "https://github.com/o/r/issues/3"})
		}
	}))
	t.Setenv("SERVE_GITHUB_CLIENT_ID", "test-client")

	mux, _ := reportMux(t, "localhost")
	const local = "127.0.0.1:5555"

	w := do(mux, "POST", "/api/report", local, `{"kind":"bug","title":"Overflow"}`)
	var rv reportReview
	json.Unmarshal(w.Body.Bytes(), &rv)
	id := rv.Report.ID

	// First call: no token yet, so the reporter gets a code to enter.
	w = do(mux, "POST", "/api/report/"+id+"/file", local, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("first file call = %d: %s", w.Code, w.Body.String())
	}
	var step map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &step)
	if step["status"] != "authorize" || step["user_code"] != "WDJB-MJHT" {
		t.Fatalf("expected a user code, got %v", step)
	}

	// Second call: poll through to approval, then create the issue.
	w = do(mux, "POST", "/api/report/"+id+"/file", local,
		`{"device_code":"dc","interval":1,"expires_in":900}`)
	if w.Code != http.StatusOK {
		t.Fatalf("second file call = %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &step)
	if step["status"] != "filed" || step["issue_url"] != "https://github.com/o/r/issues/3" {
		t.Fatalf("expected a filed issue, got %v", step)
	}

	// The report must remember it was filed, so it cannot be filed twice.
	rep, err := globalReportStore().Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Filed() || rep.Upload.IssueURL == "" {
		t.Fatalf("report was not marked filed: %+v", rep.Upload)
	}

	w = do(mux, "POST", "/api/report/"+id+"/file", local, `{}`)
	json.Unmarshal(w.Body.Bytes(), &step)
	if step["status"] != "filed" {
		t.Fatalf("re-filing should report the existing issue, got %v", step)
	}
}
