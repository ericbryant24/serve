package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Report API
//
// Reports are global rather than scoped to the served directory, so the store
// is resolved per-call from $HOME instead of hanging off Server. Instances are
// cached by directory so tests can retarget $HOME freely.
// ---------------------------------------------------------------------------

var (
	reportStoreMu sync.Mutex
	reportStores  = map[string]*ReportStore{}
)

func globalReportStore() *ReportStore {
	dir := reportsDir()
	reportStoreMu.Lock()
	defer reportStoreMu.Unlock()
	if s, ok := reportStores[dir]; ok {
		return s
	}
	s := NewReportStoreIn(dir)
	reportStores[dir] = s
	return s
}

// maxScreenshotBytes caps an uploaded capture. A full-page PNG of a long
// document is large but not unbounded; this is well clear of realistic
// captures while keeping a runaway upload from filling the disk.
const maxScreenshotBytes = 12 << 20

// ---------------------------------------------------------------------------
// Loopback guard
//
// `serve --host 0.0.0.0` exists, and a report carries a screenshot of the
// user's private document. Without this guard the report API would be a remote
// screenshot endpoint on whatever network the machine is attached to. The
// guard is deliberately in the same file as the routes it protects.
// ---------------------------------------------------------------------------

func isLoopbackHost(h string) bool {
	h = strings.TrimSpace(h)
	if h == "" || strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func isLoopbackAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// reportAPIAllowed gates every /api/report* route.
func (s *Server) reportAPIAllowed(r *http.Request) bool {
	if os.Getenv("SERVE_REPORT_ALLOW_REMOTE") == "1" {
		return true
	}
	return isLoopbackHost(s.host) && isLoopbackAddr(r.RemoteAddr)
}

func (s *Server) guardReportAPI(w http.ResponseWriter, r *http.Request) bool {
	if s.reportAPIAllowed(r) {
		return true
	}
	logf("warn", "report api refused for a non-loopback request")
	http.Error(w,
		"The report API is available only over loopback. A report can contain a "+
			"screenshot of the served document, so it is not exposed on a non-local bind.",
		http.StatusForbidden)
	return false
}

// ---------------------------------------------------------------------------
// Review payload
// ---------------------------------------------------------------------------

// attachmentReview is what the review gate renders for one attachment.
type attachmentReview struct {
	Attachment
	Text    string      `json:"text,omitempty"`    // text attachments only
	Secrets []SecretHit `json:"secrets,omitempty"` // scanned from Text
}

// reportReview is the exact payload the reporter approves. Markdown here is
// produced by Report.Markdown — the same call the uploader makes — so what is
// shown is byte-for-byte what gets sent.
type reportReview struct {
	Report        *Report            `json:"report"`
	Title         string             `json:"title"`
	Markdown      string             `json:"markdown"`
	BodySecrets   []SecretHit        `json:"body_secrets,omitempty"`
	Attachments   []attachmentReview `json:"attachments"`
	SecretCount   int                `json:"secret_count"`
	UploadAllowed bool               `json:"upload_allowed"`
	UploadBlocked string             `json:"upload_blocked_reason,omitempty"`
	Destination   string             `json:"destination"`
	Authenticated bool               `json:"authenticated"`
}

func buildReportReview(store *ReportStore, r *Report) reportReview {
	owner, repo := githubTarget()
	rv := reportReview{
		Report:        r,
		Title:         r.IssueTitle(),
		Markdown:      r.Markdown(),
		BodySecrets:   scanSecrets(r.Title + "\n" + r.Body),
		Attachments:   []attachmentReview{},
		UploadAllowed: uploadAllowed(),
		Destination:   fmt.Sprintf("github.com/%s/%s", owner, repo),
		Authenticated: loadGHToken() != "",
	}
	if !rv.UploadAllowed {
		rv.UploadBlocked = uploadBlockedReason()
	}
	rv.SecretCount = len(rv.BodySecrets)

	for _, a := range r.Attachments {
		ar := attachmentReview{Attachment: a}
		if a.Kind == AttachLog || a.Kind == AttachRepro {
			if p, err := store.AttachmentPath(r.ID, a.ID); err == nil {
				if data, err := os.ReadFile(p); err == nil {
					ar.Text = string(data)
					ar.Secrets = scanSecrets(ar.Text)
					if a.Included {
						rv.SecretCount += len(ar.Secrets)
					}
				}
			}
		}
		rv.Attachments = append(rv.Attachments, ar)
	}
	return rv
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleReportEnv(w http.ResponseWriter, r *http.Request) {
	env := currentEnv()
	writeJSON(w, map[string]interface{}{
		"env":            env,
		"upload_allowed": uploadAllowed(),
		"authenticated":  loadGHToken() != "",
	})
}

func (s *Server) handleListReports(w http.ResponseWriter, r *http.Request) {
	reports, err := globalReportStore().List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]interface{}{"reports": reports})
}

func (s *Server) handleCreateReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     string `json:"kind"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		Browser  string `json:"browser"`
		ViewKind string `json:"view_kind"`
		Repro    string `json:"repro"`
		WithLog  bool   `json:"with_log"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		http.Error(w, "a title is required", 400)
		return
	}

	store := globalReportStore()
	rep, err := store.Create(Report{
		Kind:  in.Kind,
		Title: in.Title,
		Body:  in.Body,
		Env:   Env{Browser: in.Browser, ViewKind: in.ViewKind},
	})
	if err != nil {
		logErr("could not create report", err)
		http.Error(w, err.Error(), 500)
		return
	}

	if strings.TrimSpace(in.Repro) != "" {
		if _, err := store.AddAttachment(rep.ID, AttachRepro, "", "text/markdown", []byte(in.Repro)); err != nil {
			logErr("could not attach repro", err)
		}
	}
	if in.WithLog {
		if _, err := store.AddAttachment(rep.ID, AttachLog, "", "text/plain", []byte(snapshotLog())); err != nil {
			logErr("could not attach log", err)
		}
	}

	rep, err = store.Get(rep.ID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	logf("info", "report captured", fSafe("kind", rep.Kind), fInt("attachments", len(rep.Attachments)))
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, buildReportReview(store, rep))
}

func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request) {
	store := globalReportStore()
	rep, err := store.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, buildReportReview(store, rep))
}

func (s *Server) handleUpdateReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title    *string `json:"title"`
		Body     *string `json:"body"`
		Include  *string `json:"include"`  // attachment id to flip
		Included *bool   `json:"included"` // its new value
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	store := globalReportStore()
	rep, err := store.Update(r.PathValue("id"), func(rep *Report) error {
		if rep.Filed() {
			return fmt.Errorf("this report has already been filed")
		}
		if in.Title != nil {
			rep.Title = *in.Title
		}
		if in.Body != nil {
			rep.Body = *in.Body
		}
		if in.Include != nil && in.Included != nil {
			found := false
			for i := range rep.Attachments {
				if rep.Attachments[i].ID == *in.Include {
					rep.Attachments[i].Included = *in.Included
					found = true
				}
			}
			if !found {
				return fmt.Errorf("attachment not found")
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, buildReportReview(store, rep))
}

func (s *Server) handleDeleteReport(w http.ResponseWriter, r *http.Request) {
	if err := globalReportStore().Delete(r.PathValue("id")); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(maxScreenshotBytes); err != nil {
		http.Error(w, "could not read the upload", 400)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file in the upload", 400)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxScreenshotBytes+1))
	if err != nil {
		http.Error(w, "could not read the upload", 400)
		return
	}
	if len(data) > maxScreenshotBytes {
		http.Error(w, fmt.Sprintf("the capture is larger than %s", formatSize(maxScreenshotBytes)), 413)
		return
	}

	kind := r.FormValue("kind")
	if kind == "" {
		kind = AttachScreenshot
	}
	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}

	store := globalReportStore()
	if _, err := store.AddAttachment(id, kind, r.FormValue("mode"), mime, data); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	rep, err := store.Get(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, buildReportReview(store, rep))
}

func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	store := globalReportStore()
	id, aid := r.PathValue("id"), r.PathValue("aid")
	rep, err := store.Get(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	for _, a := range rep.Attachments {
		if a.ID != aid {
			continue
		}
		p, err := store.AttachmentPath(id, aid)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", a.MIME)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, p)
		return
	}
	http.Error(w, "not found", 404)
}

// handleFileReport runs the upload. Device flow is interactive, so this is a
// two-call dance: the first call returns a user code to display, the second
// blocks until the reporter approves on github.com.
func (s *Server) handleFileReport(w http.ResponseWriter, r *http.Request) {
	if !uploadAllowed() {
		http.Error(w, uploadBlockedReason(), http.StatusForbidden)
		return
	}
	var in struct {
		DeviceCode string `json:"device_code"`
		Interval   int    `json:"interval"`
		ExpiresIn  int    `json:"expires_in"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)

	store := globalReportStore()
	id := r.PathValue("id")
	rep, err := store.Get(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if rep.Filed() {
		writeJSON(w, map[string]interface{}{"status": "filed", "issue_url": rep.Upload.IssueURL})
		return
	}

	clientID := githubClientID()
	if clientID == "" {
		http.Error(w, errNoClientID.Error(), http.StatusNotImplemented)
		return
	}

	c := newGHClientFn()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	token := loadGHToken()
	if token == "" && in.DeviceCode == "" {
		dc, err := c.DeviceStart(ctx, clientID)
		if err != nil {
			logErr("device flow could not start", err)
			http.Error(w, err.Error(), 502)
			return
		}
		writeJSON(w, map[string]interface{}{
			"status":           "authorize",
			"user_code":        dc.UserCode,
			"verification_uri": dc.VerificationURI,
			"device_code":      dc.DeviceCode,
			"interval":         dc.Interval,
			"expires_in":       dc.ExpiresIn,
		})
		return
	}
	if token == "" {
		tok, err := c.DevicePoll(ctx, clientID, &DeviceCode{
			DeviceCode: in.DeviceCode,
			Interval:   in.Interval,
			ExpiresIn:  in.ExpiresIn,
		})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := saveGHToken(tok); err != nil {
			logErr("could not cache the github token", err)
		}
		token = tok
	}

	owner, repo := githubTarget()
	issueURL, err := c.CreateIssue(ctx, token, owner, repo, rep)
	if err != nil {
		logErr("could not create the issue", err)
		http.Error(w, err.Error(), 502)
		return
	}
	if _, err := store.Update(id, func(rep *Report) error {
		rep.Upload = &UploadState{
			Status:   UploadFiled,
			IssueURL: issueURL,
			FiledAt:  time.Now().UTC().Format(time.RFC3339),
		}
		return nil
	}); err != nil {
		logErr("issue was filed but the report could not be updated", err)
	}
	logf("info", "report filed")
	writeJSON(w, map[string]interface{}{"status": "filed", "issue_url": issueURL})
}

// openInFileManager opens a directory in the platform file manager. Reports
// live outside the served root, so this cannot reuse handleReveal.
func openInFileManager(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// handleRevealReport opens the report directory so the reporter can inspect
// attachments with their own tools, and drag the screenshot into the issue.
func (s *Server) handleRevealReport(w http.ResponseWriter, r *http.Request) {
	dir, err := globalReportStore().Dir(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if err := openInFileManager(dir); err != nil {
		http.Error(w, err.Error(), 501)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// registerReportRoutes wires the report API onto mux behind the loopback guard.
func (s *Server) registerReportRoutes(mux *http.ServeMux) {
	guarded := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !s.guardReportAPI(w, r) {
				return
			}
			h(w, r)
		}
	}
	byMethod := func(m map[string]http.HandlerFunc) http.HandlerFunc {
		return guarded(func(w http.ResponseWriter, r *http.Request) {
			if h, ok := m[r.Method]; ok {
				h(w, r)
				return
			}
			http.Error(w, "method not allowed", 405)
		})
	}

	mux.HandleFunc("/api/report/env", byMethod(map[string]http.HandlerFunc{
		http.MethodGet: s.handleReportEnv,
	}))
	mux.HandleFunc("/api/report", byMethod(map[string]http.HandlerFunc{
		http.MethodGet:  s.handleListReports,
		http.MethodPost: s.handleCreateReport,
	}))
	mux.HandleFunc("/api/report/{id}", byMethod(map[string]http.HandlerFunc{
		http.MethodGet:    s.handleGetReport,
		http.MethodPatch:  s.handleUpdateReport,
		http.MethodDelete: s.handleDeleteReport,
	}))
	mux.HandleFunc("/api/report/{id}/attachment", byMethod(map[string]http.HandlerFunc{
		http.MethodPost: s.handleUploadAttachment,
	}))
	mux.HandleFunc("/api/report/{id}/attachment/{aid}", byMethod(map[string]http.HandlerFunc{
		http.MethodGet: s.handleGetAttachment,
	}))
	mux.HandleFunc("/api/report/{id}/file", byMethod(map[string]http.HandlerFunc{
		http.MethodPost: s.handleFileReport,
	}))
	mux.HandleFunc("/api/report/{id}/reveal", byMethod(map[string]http.HandlerFunc{
		http.MethodPost: s.handleRevealReport,
	}))
}
