package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *CommentStore {
	t.Helper()
	return NewCommentStore("test-doc", t.TempDir())
}

// ---------------------------------------------------------------------------
// CommentStore CRUD
// ---------------------------------------------------------------------------

func TestAddAndList(t *testing.T) {
	s := newTestStore(t)
	c, err := s.Add("hello", "anchor", "block", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "hello" {
		t.Fatalf("got text %q, want %q", c.Text, "hello")
	}

	comments, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
}

func TestUpdate(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.Add("original", "", "", nil, nil, nil)

	newText := "updated"
	updated, err := s.Update(c.ID, &newText, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil {
		t.Fatal("Update returned nil for existing comment")
	}
	if updated.Text != "updated" {
		t.Fatalf("got %q, want %q", updated.Text, "updated")
	}

	resolved := true
	updated, _ = s.Update(c.ID, nil, &resolved)
	if !updated.Resolved {
		t.Fatal("expected Resolved=true")
	}
}

func TestUpdateNonExistent(t *testing.T) {
	s := newTestStore(t)
	result, err := s.Update("no-such-id", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatal("expected nil for missing comment")
	}
}

func TestDeleteDirect(t *testing.T) {
	s := newTestStore(t)
	c, _ := s.Add("to delete", "", "", nil, nil, nil)

	found, err := s.Delete(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Delete returned false for existing comment")
	}

	comments, _ := s.List()
	if len(comments) != 0 {
		t.Fatalf("got %d comments after delete, want 0", len(comments))
	}
}

func TestDeleteNonExistent(t *testing.T) {
	s := newTestStore(t)
	found, err := s.Delete("ghost")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("Delete returned true for missing comment")
	}
}

// ---------------------------------------------------------------------------
// Cascade delete
// ---------------------------------------------------------------------------

func TestCascadeDeleteChildren(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Add("parent", "", "", nil, nil, nil)
	child, _ := s.Add("child", "", "", nil, nil, &parent.ID)

	s.Delete(parent.ID)

	comments, _ := s.List()
	for _, c := range comments {
		if c.ID == child.ID {
			t.Fatal("child not deleted when parent was deleted")
		}
	}
}

func TestCascadeDeleteGrandchildren(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Add("parent", "", "", nil, nil, nil)
	child, _ := s.Add("child", "", "", nil, nil, &parent.ID)
	grandchild, _ := s.Add("grandchild", "", "", nil, nil, &child.ID)

	s.Delete(parent.ID)

	comments, _ := s.List()
	for _, c := range comments {
		if c.ID == grandchild.ID {
			t.Fatal("grandchild not deleted when root was deleted")
		}
	}
	if len(comments) != 0 {
		t.Fatalf("got %d comments after cascade delete, want 0", len(comments))
	}
}

func TestCascadeDeleteDeepChain(t *testing.T) {
	s := newTestStore(t)
	ids := make([]string, 5)
	var parentID *string
	for i := range ids {
		c, _ := s.Add("level", "", "", nil, nil, parentID)
		ids[i] = c.ID
		id := c.ID
		parentID = &id
	}

	s.Delete(ids[0])

	comments, _ := s.List()
	if len(comments) != 0 {
		t.Fatalf("got %d comments after deep cascade delete, want 0", len(comments))
	}
}

func TestDeleteLeafLeavesParent(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Add("parent", "", "", nil, nil, nil)
	child, _ := s.Add("child", "", "", nil, nil, &parent.ID)

	s.Delete(child.ID)

	comments, _ := s.List()
	if len(comments) != 1 || comments[0].ID != parent.ID {
		t.Fatal("deleting leaf should not remove parent")
	}
}

// ---------------------------------------------------------------------------
// Document ID management
// ---------------------------------------------------------------------------

func TestSetAndGetDocumentIDMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Hello\n\nSome content.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	id, err := ensureDocumentID(path)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	got := getDocumentID(path)
	if got != id {
		t.Fatalf("getDocumentID returned %q, want %q", got, id)
	}

	// Second call must return the same ID (idempotent)
	id2, err := ensureDocumentID(path)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("ensureDocumentID not idempotent: got %q then %q", id, id2)
	}
}

func TestSetAndGetDocumentIDHTML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	if err := os.WriteFile(path, []byte("<html><head></head><body>hi</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}

	id, err := ensureDocumentID(path)
	if err != nil {
		t.Fatal(err)
	}

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), `name="comment-id"`) {
		t.Fatal("comment-id meta tag not injected into HTML")
	}

	got := getDocumentID(path)
	if got != id {
		t.Fatalf("getDocumentID returned %q, want %q", got, id)
	}
}

func TestGetDocumentIDMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.md")
	if err := os.WriteFile(path, []byte("# No ID yet\n"), 0644); err != nil {
		t.Fatal(err)
	}

	id := getDocumentID(path)
	if id != "" {
		t.Fatalf("expected empty string, got %q", id)
	}
}

func TestEnsureDocumentIDReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.md")
	if err := os.WriteFile(path, []byte("# Read only\n"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0444)
	t.Cleanup(func() { os.Chmod(path, 0644) })

	_, err := ensureDocumentID(path)
	if err == nil {
		t.Fatal("expected error for read-only file, got nil")
	}
}

// ---------------------------------------------------------------------------
// .serveignore
// ---------------------------------------------------------------------------

func TestParseServeIgnore(t *testing.T) {
	content := "# comment\n\nnode_modules/\n*.pyc\n  # indented comment  \nbuild/\n"
	got := parseServeIgnore(content)
	want := []string{"node_modules/", "*.pyc", "build/"}
	if len(got) != len(want) {
		t.Fatalf("parseServeIgnore: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pattern[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestMatchesIgnorePatterns(t *testing.T) {
	patterns := parseServeIgnore(defaultServeIgnore)
	cases := []struct {
		name  string
		isDir bool
		want  bool
	}{
		{"node_modules", true, true},
		{"node_modules", false, false}, // dir-only pattern, file named node_modules shouldn't match
		{".git", true, true},
		{"__pycache__", true, true},
		{"dist", true, true},
		{"build", true, true},
		{"vendor", true, true},
		{"target", true, true},
		{"foo.pyc", false, true},
		{"foo.o", false, true},
		{"foo.class", false, true},
		{"main.go", false, false},
		{"README.md", false, false},
		{".env", false, true},         // dotfiles always hidden
		{".hidden_file.md", false, true},
		{".serveignore", false, true},
		{".serveignore", true, true},
	}
	for _, tc := range cases {
		got := matchesIgnorePatterns(tc.name, tc.isDir, patterns)
		if got != tc.want {
			t.Errorf("matchesIgnorePatterns(%q, isDir=%v) = %v, want %v", tc.name, tc.isDir, got, tc.want)
		}
	}
}

func TestLoadServeIgnoreCreatesDefault(t *testing.T) {
	dir := t.TempDir()
	patterns := loadServeIgnore(dir)
	if len(patterns) == 0 {
		t.Fatal("expected non-empty patterns from default")
	}
	// loadServeIgnore is read-only; the file should NOT be created
	if _, err := os.Stat(filepath.Join(dir, ".serveignore")); err == nil {
		t.Error("loadServeIgnore should not create .serveignore (that's Start()'s job)")
	}
}

func TestLoadServeIgnoreReadsExisting(t *testing.T) {
	dir := t.TempDir()
	custom := "# custom\nmy_output/\n*.tmp\n"
	if err := os.WriteFile(filepath.Join(dir, ".serveignore"), []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}
	patterns := loadServeIgnore(dir)
	want := []string{"my_output/", "*.tmp"}
	if len(patterns) != len(want) {
		t.Fatalf("got %v, want %v", patterns, want)
	}
	for i, p := range want {
		if patterns[i] != p {
			t.Errorf("pattern[%d]: got %q, want %q", i, patterns[i], p)
		}
	}
}

func TestBuildFileTreeServeignore(t *testing.T) {
	dir := t.TempDir()
	// Create some files and dirs
	os.MkdirAll(filepath.Join(dir, "node_modules", "react"), 0755)
	os.WriteFile(filepath.Join(dir, "node_modules", "react", "index.js"), []byte(""), 0644)
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte(""), 0644)

	patterns := parseServeIgnore(defaultServeIgnore)
	tree := buildFileTree(dir, "", patterns)

	// Flatten names for easy checking
	names := map[string]bool{}
	var walk func([]FileNode)
	walk = func(nodes []FileNode) {
		for _, n := range nodes {
			names[n.Name] = true
			walk(n.Children)
		}
	}
	walk(tree)

	if names["node_modules"] {
		t.Error("node_modules should be excluded")
	}
	if !names["src"] {
		t.Error("src should be included")
	}
	if !names["README.md"] {
		t.Error("README.md should be included")
	}
	if names[".env"] {
		t.Error(".env should be hidden (dotfile)")
	}
}

// ---------------------------------------------------------------------------
// isIgnoredPath
// ---------------------------------------------------------------------------

func TestIsIgnoredPathHidden(t *testing.T) {
	root := "/project"
	cases := []struct {
		path    string
		ignored bool
	}{
		{"/project/.git/config", true},
		{"/project/src/main.go", false},
		{"/project/node_modules/react/index.js", true},
		{"/project/__pycache__/foo.pyc", true},
		{"/project/src/__pycache__/bar.pyc", true},
		{"/project/vendor/lib.go", true},
		{"/project/dist/bundle.js", true},
		{"/project/src/dist/file.js", true},
		{"/project/README.md", false},
		{"/project/build/output.bin", true},
	}
	for _, tc := range cases {
		got := isIgnoredPath(tc.path, root)
		if got != tc.ignored {
			t.Errorf("isIgnoredPath(%q) = %v, want %v", tc.path, got, tc.ignored)
		}
	}
}
