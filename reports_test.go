package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *ReportStore {
	t.Helper()
	return NewReportStoreIn(t.TempDir())
}

func TestReportCreateAndGet(t *testing.T) {
	s := testStore(t)
	r, err := s.Create(Report{Kind: ReportKindBug, Title: "Table overflows", Body: "on narrow viewports"})
	if err != nil {
		t.Fatal(err)
	}
	if !validReportID(r.ID) {
		t.Fatalf("generated id %q does not satisfy validReportID", r.ID)
	}
	if r.CreatedAt == "" {
		t.Error("CreatedAt was not set")
	}
	if r.Env.OS == "" || r.Env.Arch == "" {
		t.Error("environment was not populated")
	}

	got, err := s.Get(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Table overflows" {
		t.Errorf("title = %q", got.Title)
	}
}

// An unknown kind must not become an unknown label upstream.
func TestReportKindDefaultsToBug(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Kind: "nonsense", Title: "x"})
	if r.Kind != ReportKindBug {
		t.Errorf("kind = %q, want %q", r.Kind, ReportKindBug)
	}
	f, _ := s.Create(Report{Kind: ReportKindFeature, Title: "x"})
	if f.Kind != ReportKindFeature {
		t.Errorf("feature kind was not preserved: %q", f.Kind)
	}
}

// The opt-in default is a property of the model, not of the review UI.
func TestAttachmentsStartExcluded(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Title: "x"})

	for _, kind := range []string{AttachScreenshot, AttachLog, AttachRepro} {
		a, err := s.AddAttachment(r.ID, kind, CaptureStructural, "application/octet-stream", []byte("data"))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if a.Included {
			t.Errorf("%s attachment was included by default", kind)
		}
	}
	got, _ := s.Get(r.ID)
	if len(got.IncludedAttachments()) != 0 {
		t.Errorf("IncludedAttachments returned %d, want 0", len(got.IncludedAttachments()))
	}
}

func TestAddAttachmentRejectsUnknownKind(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Title: "x"})
	if _, err := s.AddAttachment(r.ID, "../../etc/passwd", "", "text/plain", []byte("x")); err == nil {
		t.Fatal("expected an error for an unknown attachment kind")
	}
}

// Report ids index into the filesystem, so every entry point has to reject
// anything outside the id alphabet before it reaches filepath.Join.
func TestInvalidReportIDsRejected(t *testing.T) {
	s := testStore(t)
	bad := []string{"../escape", "..", "/etc/passwd", "abc", "", "ABCDEF12", "a/b", "12345"}
	for _, id := range bad {
		if validReportID(id) {
			t.Errorf("validReportID(%q) = true", id)
		}
		if _, err := s.Get(id); err == nil {
			t.Errorf("Get(%q) succeeded", id)
		}
		if err := s.Delete(id); err == nil {
			t.Errorf("Delete(%q) succeeded", id)
		}
		if _, err := s.AddAttachment(id, AttachLog, "", "text/plain", []byte("x")); err == nil {
			t.Errorf("AddAttachment(%q) succeeded", id)
		}
	}
}

func TestReportUpdateAndDelete(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Title: "before"})
	a, _ := s.AddAttachment(r.ID, AttachLog, "", "text/plain", []byte("log body"))

	updated, err := s.Update(r.ID, func(rep *Report) error {
		rep.Title = "after"
		for i := range rep.Attachments {
			rep.Attachments[i].Included = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "after" || len(updated.IncludedAttachments()) != 1 {
		t.Fatalf("update did not persist: %+v", updated)
	}

	path, err := s.AttachmentPath(r.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("attachment file missing: %v", err)
	}

	if err := s.Delete(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("deleting a report left its directory behind")
	}
	if err := s.Delete(r.ID); err == nil {
		t.Error("deleting a missing report should fail")
	}
}

func TestReportListNewestFirst(t *testing.T) {
	s := testStore(t)
	a, _ := s.Create(Report{Title: "first"})
	b, _ := s.Create(Report{Title: "second"})

	// Same-second creation is realistic, so force distinguishable timestamps.
	s.Update(a.ID, func(r *Report) error { r.CreatedAt = "2026-01-01T00:00:00Z"; return nil })
	s.Update(b.ID, func(r *Report) error { r.CreatedAt = "2026-06-01T00:00:00Z"; return nil })

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Title != "second" {
		t.Fatalf("expected newest first, got %+v", list)
	}
}

// A half-written report must not break the whole listing.
func TestReportListSkipsUnreadable(t *testing.T) {
	dir := t.TempDir()
	s := NewReportStoreIn(dir)
	good, _ := s.Create(Report{Title: "good"})

	broken := filepath.Join(dir, "aabbccddeeff")
	os.MkdirAll(broken, 0700)
	os.WriteFile(filepath.Join(broken, "report.json"), []byte("{not json"), 0600)

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("expected only the readable report, got %+v", list)
	}
}

func TestReportMarkdown(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Kind: ReportKindBug, Title: "Overflow", Body: "The table runs over."})
	a, _ := s.AddAttachment(r.ID, AttachScreenshot, CaptureStructural, "image/png", []byte("PNG"))

	md := s.mustGet(t, r.ID).Markdown()
	if !strings.Contains(md, "The table runs over.") {
		t.Error("body missing from markdown")
	}
	if strings.Contains(md, a.File) {
		t.Error("an excluded attachment was listed in the markdown")
	}

	s.Update(r.ID, func(rep *Report) error {
		rep.Attachments[0].Included = true
		return nil
	})
	md = s.mustGet(t, r.ID).Markdown()
	if !strings.Contains(md, a.File) {
		t.Error("an included attachment was not listed in the markdown")
	}
}

func TestReportMarkdownHandlesEmptyBody(t *testing.T) {
	s := testStore(t)
	r, _ := s.Create(Report{Title: "just a title"})
	if md := r.Markdown(); !strings.Contains(md, "No description provided") {
		t.Errorf("empty body produced %q", md)
	}
	_ = s
}

func TestIssueLabels(t *testing.T) {
	bug := &Report{Kind: ReportKindBug}
	if got := bug.IssueLabels(); len(got) != 1 || got[0] != "bug" {
		t.Errorf("bug labels = %v", got)
	}
	feat := &Report{Kind: ReportKindFeature}
	if got := feat.IssueLabels(); len(got) != 1 || got[0] != "enhancement" {
		t.Errorf("feature labels = %v", got)
	}
}

func (s *ReportStore) mustGet(t *testing.T, id string) *Report {
	t.Helper()
	r, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
