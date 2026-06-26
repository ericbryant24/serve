package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestReply(t *testing.T) {
	s := newTestStore(t)
	parent, _ := s.Add("question", "anchor", "block", nil, nil, nil)

	reply, err := s.Reply(parent.ID, "here's an answer")
	if err != nil {
		t.Fatal(err)
	}
	if reply == nil {
		t.Fatal("Reply returned nil for existing parent")
	}
	if reply.ParentID == nil || *reply.ParentID != parent.ID {
		t.Fatalf("reply ParentID = %v, want %q", reply.ParentID, parent.ID)
	}
	if reply.Text != "here's an answer" {
		t.Fatalf("got %q, want %q", reply.Text, "here's an answer")
	}

	comments, _ := s.List()
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}
}

func TestReplyNonExistentParent(t *testing.T) {
	s := newTestStore(t)
	reply, err := s.Reply("no-such-id", "orphan reply")
	if err != nil {
		t.Fatal(err)
	}
	if reply != nil {
		t.Fatal("expected nil reply for missing parent")
	}
	comments, _ := s.List()
	if len(comments) != 0 {
		t.Fatalf("got %d comments, want 0 (no reply should be stored)", len(comments))
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
// storeKeyForFile
// ---------------------------------------------------------------------------

func tempFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStoreKeyForFile_Deterministic(t *testing.T) {
	f := tempFile(t, "doc.md")
	if a, b := storeKeyForFile(f), storeKeyForFile(f); a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}
}

func TestStoreKeyForFile_DifferentFiles(t *testing.T) {
	if storeKeyForFile(tempFile(t, "a.md")) == storeKeyForFile(tempFile(t, "b.md")) {
		t.Fatal("different files should produce different keys")
	}
}

func TestStoreKeyForFile_NoFileWrite(t *testing.T) {
	f := tempFile(t, "doc.md")
	before, _ := os.ReadFile(f)
	storeKeyForFile(f)
	after, _ := os.ReadFile(f)
	if string(before) != string(after) {
		t.Fatal("storeKeyForFile must not modify the file")
	}
}

func TestStoreKeyForFile_RenamePreservesKey(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "orig.md")
	renamed := filepath.Join(dir, "renamed.md")
	if err := os.WriteFile(orig, []byte("# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := storeKeyForFile(orig)
	if err := os.Rename(orig, renamed); err != nil {
		t.Fatal(err)
	}
	after := storeKeyForFile(renamed)
	// On Unix, inodes survive renames so the key must be identical.
	// On Windows the fallback (path hash) differs — detect via Stat_t assertion.
	if before != after {
		fi, _ := os.Stat(renamed)
		if fi != nil {
			if _, ok := fi.Sys().(*syscall.Stat_t); ok {
				t.Fatalf("rename changed key on Unix: %q → %q", before, after)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Orphan adoption (inode drift across atomic writes)
// ---------------------------------------------------------------------------

// atomicReplace mimics the write-temp-then-rename idiom used by VS Code,
// JetBrains IDEs, and Claude Code's Edit/Write tools. The path stays the
// same; the inode changes.
func atomicReplace(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".atomic-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptOrphan_AfterAtomicWrite(t *testing.T) {
	commentsDir := t.TempDir()
	docDir := t.TempDir()
	docPath := filepath.Join(docDir, "doc.md")
	if err := os.WriteFile(docPath, []byte("# original\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// First store, original inode. Add a comment.
	first := NewCommentStoreForFile(docPath, commentsDir)
	if _, err := first.Add("orphan me", "anchor", "block", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	firstKey := storeKeyForFile(docPath)

	// External editor atomic-write — inode changes.
	atomicReplace(t, docPath, "# rewritten\n")
	secondKey := storeKeyForFile(docPath)
	if firstKey == secondKey {
		t.Skip("atomic rewrite did not change inode on this filesystem; cannot reproduce")
	}

	// New store under the new key. Without adoption it would see nothing.
	second := NewCommentStoreForFile(docPath, commentsDir)
	comments, err := second.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || comments[0].Text != "orphan me" {
		t.Fatalf("expected adopted comment, got %v", comments)
	}

	// The old file must be gone (renamed onto the new key).
	if _, err := os.Stat(filepath.Join(commentsDir, firstKey+".json")); !os.IsNotExist(err) {
		t.Fatalf("orphan file should be gone after adoption, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(commentsDir, secondKey+".json")); err != nil {
		t.Fatalf("new key file should exist after adoption, stat err = %v", err)
	}
}

func TestAdoptOrphan_IgnoresMismatchedPath(t *testing.T) {
	commentsDir := t.TempDir()
	docDir := t.TempDir()
	docPath := filepath.Join(docDir, "doc.md")
	otherPath := filepath.Join(docDir, "other.md")
	if err := os.WriteFile(docPath, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	// Pre-populate an orphan that belongs to a different source path.
	stranger := NewCommentStoreForFile(otherPath, commentsDir)
	if _, err := stranger.Add("not mine", "", "", nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// docPath has no comments yet. A fresh load must NOT steal the stranger's.
	s := NewCommentStoreForFile(docPath, commentsDir)
	comments, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected empty, got %v", comments)
	}
}

func TestAdoptOrphan_IgnoresLegacyBareArray(t *testing.T) {
	commentsDir := t.TempDir()
	docDir := t.TempDir()
	docPath := filepath.Join(docDir, "doc.md")
	if err := os.WriteFile(docPath, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a legacy bare-array file under some other random key.
	legacy := []byte(`[{"id":"x","text":"legacy","created_at":"","resolved":false,"anchor_text":"","block_text":"","source_line_start":null,"source_line_end":null,"parent_id":null}]`)
	if err := os.WriteFile(filepath.Join(commentsDir, "deadbeef.json"), legacy, 0644); err != nil {
		t.Fatal(err)
	}

	s := NewCommentStoreForFile(docPath, commentsDir)
	comments, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 0 {
		t.Fatalf("legacy bare-array files must not be adopted, got %v", comments)
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
		{".env", false, false}, // dotfiles shown unless matched by a pattern
		{".hidden_file.md", false, false},
		{".serveignore", false, false},
		{".github", true, false},
		{".git", true, true}, // matches the .git/ pattern in defaultServeIgnore
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
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte(""), 0644)

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
	if !names[".env"] {
		t.Error(".env should be included (dotfiles are no longer hidden by default)")
	}
	if names[".git"] {
		t.Error(".git should be excluded (matches default .serveignore)")
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
		{"/project/.github/workflows/ci.yml", true}, // watcher skips all dotdirs
		{"/project/.venv/lib/site-packages/x.py", true},
	}
	for _, tc := range cases {
		got := isIgnoredPath(tc.path, root)
		if got != tc.ignored {
			t.Errorf("isIgnoredPath(%q) = %v, want %v", tc.path, got, tc.ignored)
		}
	}
}

// ---------------------------------------------------------------------------
// injectCommentAnchors
// ---------------------------------------------------------------------------

func TestInjectCommentAnchors_SkipsMatchesInsideScript(t *testing.T) {
	// A comment anchored on the literal word "new" must not be injected
	// inside the embedded reload script (var ws = new WebSocket...) — doing
	// so would emit a <span> inside <script>, breaking JS parsing and
	// killing every client-side behavior including hot reload.
	html := `<body><p>here is some text</p>` +
		`<script>var ws = new WebSocket("ws://x/y");</script>` +
		`<p>nothing new to see</p></body>`
	c := Comment{ID: "abc123", AnchorText: "new"}
	out := injectCommentAnchors(html, []Comment{c})

	scriptStart := strings.Index(out, "<script>")
	scriptEnd := strings.Index(out, "</script>")
	if scriptStart < 0 || scriptEnd < 0 || scriptEnd <= scriptStart {
		t.Fatalf("script block missing in output: %q", out)
	}
	if strings.Contains(out[scriptStart:scriptEnd], "data-comment-anchor") {
		t.Errorf("anchor span was injected inside <script>: %q", out[scriptStart:scriptEnd])
	}
	// And the *next* match (in the trailing <p>) should still get the anchor.
	if !strings.Contains(out, `<span data-comment-anchor="abc123"`) {
		t.Errorf("expected anchor span outside script; got: %q", out)
	}
}

func TestInjectCommentAnchors_SkipsMatchesInsideStyle(t *testing.T) {
	html := `<style>.new { color: red; }</style><p>brand new</p>`
	c := Comment{ID: "x", AnchorText: "new"}
	out := injectCommentAnchors(html, []Comment{c})
	styleEnd := strings.Index(out, "</style>")
	if strings.Contains(out[:styleEnd], "data-comment-anchor") {
		t.Errorf("anchor span was injected inside <style>: %q", out)
	}
}
