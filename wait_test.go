package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitQualifies(t *testing.T) {
	cases := []struct {
		event   string
		newOnly bool
		want    bool
	}{
		// initial replay is never a wake, in either mode
		{"initial", false, false},
		{"initial", true, false},
		// fresh comments/replies wake in both modes
		{"new_comment", false, true},
		{"new_comment", true, true},
		{"new_reply", false, true},
		{"new_reply", true, true},
		// mutations wake by default, but --new ignores them
		{"edited", false, true},
		{"edited", true, false},
		{"resolved", false, true},
		{"resolved", true, false},
		{"unresolved", false, true},
		{"unresolved", true, false},
		{"deleted", false, true},
		{"deleted", true, false},
	}
	for _, c := range cases {
		got := waitQualifies(map[string]any{"event": c.event}, c.newOnly)
		if got != c.want {
			t.Errorf("waitQualifies(%q, newOnly=%v) = %v, want %v", c.event, c.newOnly, got, c.want)
		}
	}
}

// silenceStdout redirects os.Stdout to /dev/null for the duration of a test so
// the event JSON that runWaitSingle emits doesn't pollute test output.
func silenceStdout(t *testing.T) func() {
	t.Helper()
	orig := os.Stdout
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	return func() { os.Stdout = orig; f.Close() }
}

func TestRunWaitSingle_EmitsOnNewComment(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate commentStoreDir()

	docDir := t.TempDir()
	docPath := filepath.Join(docDir, "page.md")
	if err := os.WriteFile(docPath, []byte("# Title\n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	defer silenceStdout(t)()

	// Add a comment once the watcher is up. runWaitSingle establishes its
	// fsnotify watch synchronously before blocking, so a short delay is enough.
	go func() {
		time.Sleep(300 * time.Millisecond)
		store := NewCommentStoreForFile(docPath, commentStoreDir())
		if _, err := store.Add("does wait catch this?", "body", "body", nil, nil, nil); err != nil {
			t.Errorf("Add: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if !runWaitSingle(ctx, docPath, false) {
		t.Fatal("expected runWaitSingle to return true after a new comment arrived")
	}
}

func TestRunWaitSingle_TimeoutReturnsFalse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	docDir := t.TempDir()
	docPath := filepath.Join(docDir, "page.md")
	if err := os.WriteFile(docPath, []byte("# Title\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	if runWaitSingle(ctx, docPath, false) {
		t.Fatal("expected runWaitSingle to return false when the context expires with no event")
	}
}
