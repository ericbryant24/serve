package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// isEditableFile
// ---------------------------------------------------------------------------

func TestIsEditableFile(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"doc.md", true},
		{"doc.markdown", true},
		{"notes.txt", true},
		{"notes.text", true},
		{"README.MD", true},   // case-insensitive
		{"server.go", false},
		{"index.html", false},
		{"style.css", false},
		{"data.json", false},
		{"image.png", false},
		{"noext", false},
	}
	for _, tc := range cases {
		got := isEditableFile(filepath.Join(t.TempDir(), tc.name))
		if got != tc.want {
			t.Errorf("isEditableFile(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// renderMarkdownSource
// ---------------------------------------------------------------------------

func TestRenderMarkdownSource_Basic(t *testing.T) {
	html := renderMarkdownSource("# Hello\n\nWorld")
	if !strings.Contains(html, "<h1") {
		t.Errorf("expected h1 in output, got: %q", html)
	}
	if !strings.Contains(html, "Hello") {
		t.Errorf("expected 'Hello' in output, got: %q", html)
	}
}

func TestRenderMarkdownSource_Empty(t *testing.T) {
	html := renderMarkdownSource("")
	// empty input → empty or just whitespace output, no error
	if strings.Contains(html, "error") {
		t.Errorf("unexpected error in output: %q", html)
	}
}

func TestRenderMarkdownSource_StripsFrontmatter(t *testing.T) {
	src := "---\ntitle: Test\n---\n\n# Body"
	html := renderMarkdownSource(src)
	if strings.Contains(html, "title: Test") {
		t.Errorf("frontmatter should be stripped, got: %q", html)
	}
	if !strings.Contains(html, "Body") {
		t.Errorf("expected 'Body' in output, got: %q", html)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newMarkdownServer(t *testing.T, content string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.md")
	if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Server{mode: "markdown", filePath: fp, baseDir: dir}
	return s, fp
}

func newDirServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (macOS /var → /private/var) so fileFromRequest passes.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}
	fp := filepath.Join(realDir, "doc.md")
	if err := os.WriteFile(fp, []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}
	s := &Server{mode: "directory", baseDir: realDir}
	return s, fp
}

// ---------------------------------------------------------------------------
// handleGetFile
// ---------------------------------------------------------------------------

func TestHandleGetFile_SingleMode(t *testing.T) {
	s, _ := newMarkdownServer(t, "# Hello\n")

	req := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	w := httptest.NewRecorder()
	s.handleGetFile(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var data map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["content"] != "# Hello\n" {
		t.Errorf("got content %q", data["content"])
	}
}

func TestHandleGetFile_NonEditable(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "server.go")
	os.WriteFile(fp, []byte("package main"), 0644)
	s := &Server{mode: "markdown", filePath: fp, baseDir: dir}

	req := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	w := httptest.NewRecorder()
	s.handleGetFile(w, req)

	if w.Code != 403 {
		t.Fatalf("got %d, want 403", w.Code)
	}
}

func TestHandleGetFile_DirMode(t *testing.T) {
	s, fp := newDirServer(t)
	rel, _ := filepath.Rel(s.baseDir, fp)

	req := httptest.NewRequest(http.MethodGet, "/api/file?file="+rel, nil)
	w := httptest.NewRecorder()
	s.handleGetFile(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d, want 200", w.Code)
	}
	var data map[string]string
	json.Unmarshal(w.Body.Bytes(), &data)
	if data["content"] != "# Hello" {
		t.Errorf("got content %q", data["content"])
	}
}

func TestHandleGetFile_DirMode_MissingParam(t *testing.T) {
	s, _ := newDirServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	w := httptest.NewRecorder()
	s.handleGetFile(w, req)

	if w.Code != 404 {
		t.Fatalf("got %d, want 404", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleEditFile
// ---------------------------------------------------------------------------

func TestHandleEditFile_SingleMode(t *testing.T) {
	s, fp := newMarkdownServer(t, "# Original\n")

	body, _ := json.Marshal(map[string]string{"content": "# Updated\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/edit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleEditFile(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var result map[string]bool
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result["ok"] {
		t.Error("expected ok: true")
	}

	written, _ := os.ReadFile(fp)
	if string(written) != "# Updated\n" {
		t.Errorf("file content = %q", written)
	}
}

func TestHandleEditFile_NonEditable(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "main.go")
	os.WriteFile(fp, []byte("package main"), 0644)
	s := &Server{mode: "markdown", filePath: fp, baseDir: dir}

	body, _ := json.Marshal(map[string]string{"content": "malicious"})
	req := httptest.NewRequest(http.MethodPost, "/api/edit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleEditFile(w, req)

	if w.Code != 403 {
		t.Fatalf("got %d, want 403", w.Code)
	}
	// File must not be modified
	got, _ := os.ReadFile(fp)
	if string(got) != "package main" {
		t.Error("file was modified despite 403 response")
	}
}

func TestHandleEditFile_BadJSON(t *testing.T) {
	s, _ := newMarkdownServer(t, "# Original\n")

	req := httptest.NewRequest(http.MethodPost, "/api/edit", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleEditFile(w, req)

	if w.Code != 400 {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestHandleEditFile_DirMode(t *testing.T) {
	s, fp := newDirServer(t)
	rel, _ := filepath.Rel(s.baseDir, fp)

	body, _ := json.Marshal(map[string]string{"content": "# New content\n"})
	req := httptest.NewRequest(http.MethodPost, "/api/edit?file="+rel, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleEditFile(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	written, _ := os.ReadFile(fp)
	if string(written) != "# New content\n" {
		t.Errorf("file content = %q", written)
	}
}

func TestHandleEditFile_DirMode_PathTraversal(t *testing.T) {
	s, _ := newDirServer(t)

	// Attempt path traversal
	body, _ := json.Marshal(map[string]string{"content": "pwned"})
	req := httptest.NewRequest(http.MethodPost, "/api/edit?file=../../etc/passwd", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleEditFile(w, req)

	// fileFromRequest returns "" for paths outside baseDir → 404
	if w.Code != 404 {
		t.Fatalf("got %d, want 404 (path traversal rejected)", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handlePreview
// ---------------------------------------------------------------------------

func TestHandlePreview_RendersMarkdown(t *testing.T) {
	s, _ := newMarkdownServer(t, "")

	body, _ := json.Marshal(map[string]string{"content": "# Preview\n\nSome text."})
	req := httptest.NewRequest(http.MethodPost, "/api/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePreview(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if !strings.Contains(result["html"], "<h1") {
		t.Errorf("expected h1 in preview html, got: %q", result["html"])
	}
	if !strings.Contains(result["html"], "Preview") {
		t.Errorf("expected 'Preview' in html, got: %q", result["html"])
	}
}

func TestHandlePreview_BadJSON(t *testing.T) {
	s, _ := newMarkdownServer(t, "")

	req := httptest.NewRequest(http.MethodPost, "/api/preview", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePreview(w, req)

	if w.Code != 400 {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestHandlePreview_EmptyContent(t *testing.T) {
	s, _ := newMarkdownServer(t, "")

	body, _ := json.Marshal(map[string]string{"content": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handlePreview(w, req)

	if w.Code != 200 {
		t.Fatalf("got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Edit button in rendered pages
// ---------------------------------------------------------------------------

func TestEditButtonPresentForMarkdown(t *testing.T) {
	s, _ := newMarkdownServer(t, "# Hello\n")
	html, err := renderMarkdown(s.filePath, wrapOptions{faviconPath: s.filePath})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "serve-edit-btn") {
		t.Error("edit button should be present for .md files")
	}
}

func TestEditButtonAbsentForHTML(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "index.html")
	os.WriteFile(fp, []byte("<html><body>test</body></html>"), 0644)

	// HTML files don't go through renderMarkdown; they use injectReloadScript.
	// Verify isEditableFile returns false so edit UI is not injected.
	if isEditableFile(fp) {
		t.Error("HTML files should not be editable")
	}
}

func TestEditButtonAbsentForCodeFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "main.go")
	os.WriteFile(fp, []byte("package main"), 0644)

	html, err := renderCodeFile(fp, wrapOptions{faviconPath: fp})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "serve-edit-btn") {
		t.Error("edit button should not be present for .go files")
	}
}

func TestEditButtonPresentForTxtFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "notes.txt")
	os.WriteFile(fp, []byte("plain text"), 0644)

	html, err := renderCodeFile(fp, wrapOptions{faviconPath: fp})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "serve-edit-btn") {
		t.Error("edit button should be present for .txt files")
	}
}

// ---------------------------------------------------------------------------
// resolveTarget
// ---------------------------------------------------------------------------

func TestResolveTarget_SingleMode(t *testing.T) {
	s, fp := newMarkdownServer(t, "")
	req := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	got := s.resolveTarget(req)
	if got != fp {
		t.Errorf("got %q, want %q", got, fp)
	}
}

func TestResolveTarget_DirMode(t *testing.T) {
	s, fp := newDirServer(t)
	rel, _ := filepath.Rel(s.baseDir, fp)
	req := httptest.NewRequest(http.MethodGet, "/api/file?file="+rel, nil)
	got := s.resolveTarget(req)
	if got != fp {
		t.Errorf("got %q, want %q", got, fp)
	}
}
