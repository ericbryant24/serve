package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Report model
//
// A report is about the application, not about a document, so unlike comments
// it is keyed by a random id rather than by inode. Each report is a DIRECTORY
// so its attachments are ordinary files: the reporter can open the screenshot
// in Preview, or the log in their own editor, before deciding to send it. That
// inspectability is the point — it means trusting the review pane is optional.
// ---------------------------------------------------------------------------

const (
	ReportKindBug     = "bug"
	ReportKindFeature = "feature"

	AttachScreenshot = "screenshot"
	AttachLog        = "log"
	AttachRepro      = "repro"

	CaptureStructural = "structural"
	CaptureFull       = "full"

	UploadLocal = "local"
	UploadFiled = "filed"
)

// Env is the environment context attached to every report. Note what is
// absent: the file path, the directory name, and the document title. None of
// them help diagnose a bug enough to justify what they disclose.
type Env struct {
	ServeVersion string `json:"serve_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	GoVersion    string `json:"go_version"`
	Browser      string `json:"browser,omitempty"`
	ViewKind     string `json:"view_kind,omitempty"` // markdown | code | pdf | image | marp
}

func currentEnv() Env {
	return Env{
		ServeVersion: version,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		GoVersion:    runtime.Version(),
	}
}

// Attachment describes one file inside a report directory.
type Attachment struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`           // screenshot | log | repro
	Mode  string `json:"mode,omitempty"` // structural | full
	MIME  string `json:"mime"`
	Bytes int    `json:"bytes"`
	File  string `json:"file"` // basename, relative to the report directory

	// Included is false until a human says otherwise. This is a struct default
	// rather than a UI default on purpose: if the review gate has a bug, the
	// failure mode is an attachment that did not send.
	Included bool `json:"included"`
}

type UploadState struct {
	Status   string `json:"status"` // local | filed
	IssueURL string `json:"issue_url,omitempty"`
	FiledAt  string `json:"filed_at,omitempty"`
}

type Report struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Body        string       `json:"body"`
	CreatedAt   string       `json:"created_at"`
	Env         Env          `json:"env"`
	Attachments []Attachment `json:"attachments"`
	Upload      *UploadState `json:"upload,omitempty"`
}

// Filed reports whether this report has already been posted upstream.
func (r *Report) Filed() bool {
	return r.Upload != nil && r.Upload.Status == UploadFiled
}

// IncludedAttachments returns only the attachments the reporter opted in.
func (r *Report) IncludedAttachments() []Attachment {
	var out []Attachment
	for _, a := range r.Attachments {
		if a.Included {
			out = append(out, a)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// reportIDRe guards every id that arrives from an HTTP path or a CLI argument.
// Report ids index directly into the filesystem, so anything outside this
// alphabet is rejected before it reaches filepath.Join.
var reportIDRe = regexp.MustCompile(`^[a-f0-9]{6,64}$`)

func validReportID(id string) bool { return reportIDRe.MatchString(id) }

func reportsDir() string { return filepath.Join(homeDir(), ".serve", "reports") }

type ReportStore struct {
	dir string
	mu  sync.Mutex
}

func NewReportStore() *ReportStore { return NewReportStoreIn(reportsDir()) }

// NewReportStoreIn creates a store rooted at dir. Mode 0700, not the 0755 the
// comment store uses: a report can contain a screenshot of a private document.
func NewReportStoreIn(dir string) *ReportStore {
	os.MkdirAll(dir, 0700)
	return &ReportStore{dir: dir}
}

func (s *ReportStore) reportDir(id string) string { return filepath.Join(s.dir, id) }
func (s *ReportStore) jsonPath(id string) string  { return filepath.Join(s.dir, id, "report.json") }

func newReportID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%012x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// Create allocates an id and a directory, then writes the report.
func (s *ReportStore) Create(r Report) (*Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if r.Kind != ReportKindFeature {
		r.Kind = ReportKindBug
	}
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if r.Attachments == nil {
		r.Attachments = []Attachment{}
	}
	base := currentEnv()
	base.Browser, base.ViewKind = r.Env.Browser, r.Env.ViewKind
	r.Env = base

	for i := 0; i < 8; i++ {
		id := newReportID()
		dir := s.reportDir(id)
		if err := os.Mkdir(dir, 0700); err != nil {
			if os.IsExist(err) {
				continue
			}
			return nil, err
		}
		r.ID = id
		if err := s.writeLocked(&r); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		return &r, nil
	}
	return nil, fmt.Errorf("could not allocate a report id")
}

func (s *ReportStore) writeLocked(r *Report) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.jsonPath(r.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.jsonPath(r.ID))
}

func (s *ReportStore) readLocked(id string) (*Report, error) {
	data, err := os.ReadFile(s.jsonPath(id))
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *ReportStore) Get(id string) (*Report, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("invalid report id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id)
}

// List returns every stored report, newest first.
func (s *ReportStore) List() ([]Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Report{}, nil
		}
		return nil, err
	}
	out := []Report{}
	for _, e := range entries {
		if !e.IsDir() || !validReportID(e.Name()) {
			continue
		}
		r, err := s.readLocked(e.Name())
		if err != nil {
			continue // a half-written report should not break the listing
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Update applies mutate to the stored report and persists the result.
func (s *ReportStore) Update(id string, mutate func(*Report) error) (*Report, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("invalid report id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, err := s.readLocked(id)
	if err != nil {
		return nil, err
	}
	if err := mutate(r); err != nil {
		return nil, err
	}
	if err := s.writeLocked(r); err != nil {
		return nil, err
	}
	return r, nil
}

// Delete removes the report and every attachment with it.
func (s *ReportStore) Delete(id string) error {
	if !validReportID(id) {
		return fmt.Errorf("invalid report id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.jsonPath(id)); err != nil {
		return err
	}
	return os.RemoveAll(s.reportDir(id))
}

var attachExt = map[string]string{
	AttachScreenshot: ".png",
	AttachLog:        ".txt",
	AttachRepro:      ".md",
}

// AddAttachment writes data into the report directory. The filename is
// generated here rather than accepted from the caller, so a hostile or
// careless name can never escape the report directory.
func (s *ReportStore) AddAttachment(id, kind, mode, mime string, data []byte) (*Attachment, error) {
	if !validReportID(id) {
		return nil, fmt.Errorf("invalid report id")
	}
	ext, ok := attachExt[kind]
	if !ok {
		return nil, fmt.Errorf("unknown attachment kind %q", kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	r, err := s.readLocked(id)
	if err != nil {
		return nil, err
	}
	aid := newReportID()
	name := kind + "-" + aid + ext
	if err := os.WriteFile(filepath.Join(s.reportDir(id), name), data, 0600); err != nil {
		return nil, err
	}
	a := Attachment{
		ID:       aid,
		Kind:     kind,
		Mode:     mode,
		MIME:     mime,
		Bytes:    len(data),
		File:     name,
		Included: false, // always: opting in is a human act
	}
	r.Attachments = append(r.Attachments, a)
	if err := s.writeLocked(r); err != nil {
		return nil, err
	}
	return &a, nil
}

// AttachmentPath resolves an attachment to a path inside the report directory.
// filepath.Base on the stored name is belt-and-braces against a report.json
// that was hand-edited to point somewhere else.
func (s *ReportStore) AttachmentPath(id, aid string) (string, error) {
	r, err := s.Get(id)
	if err != nil {
		return "", err
	}
	for _, a := range r.Attachments {
		if a.ID == aid {
			return filepath.Join(s.reportDir(id), filepath.Base(a.File)), nil
		}
	}
	return "", fmt.Errorf("attachment not found")
}

func (s *ReportStore) Dir(id string) (string, error) {
	if !validReportID(id) {
		return "", fmt.Errorf("invalid report id")
	}
	return s.reportDir(id), nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// Markdown renders the report as the issue body that will be posted. It is the
// single source of truth for that text: the review gate displays exactly this,
// so what the reporter approves is byte-for-byte what gets sent.
func (r *Report) Markdown() string {
	var b strings.Builder

	body := strings.TrimSpace(r.Body)
	if body == "" {
		body = "_No description provided._"
	}
	b.WriteString(body)
	b.WriteString("\n\n---\n\n")

	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| serve | `%s` |\n", r.Env.ServeVersion)
	fmt.Fprintf(&b, "| platform | `%s/%s` |\n", r.Env.OS, r.Env.Arch)
	if r.Env.GoVersion != "" {
		fmt.Fprintf(&b, "| go | `%s` |\n", r.Env.GoVersion)
	}
	if r.Env.ViewKind != "" {
		fmt.Fprintf(&b, "| view | `%s` |\n", r.Env.ViewKind)
	}
	if r.Env.Browser != "" {
		fmt.Fprintf(&b, "| browser | `%s` |\n", strings.ReplaceAll(r.Env.Browser, "|", "\\|"))
	}

	if att := r.IncludedAttachments(); len(att) > 0 {
		b.WriteString("\n**Attached**\n\n")
		for _, a := range att {
			label := a.Kind
			if a.Mode != "" {
				label += " (" + a.Mode + ")"
			}
			fmt.Fprintf(&b, "- `%s` — %s, %s\n", a.File, label, formatSize(int64(a.Bytes)))
		}
	}
	return b.String()
}

// IssueTitle prefixes the title so issues sort usefully in the tracker.
func (r *Report) IssueTitle() string {
	t := strings.TrimSpace(r.Title)
	if t == "" {
		t = "(no title)"
	}
	return t
}

// IssueLabels maps report kind onto tracker labels.
func (r *Report) IssueLabels() []string {
	if r.Kind == ReportKindFeature {
		return []string{"enhancement"}
	}
	return []string{"bug"}
}
