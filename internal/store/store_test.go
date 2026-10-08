package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"serve/internal/anchor"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

var human = Author{Kind: Human, Name: "Eric"}
var agent = Author{Kind: Agent, Name: "Claude"}

func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentFollowsMovesAndAtomicSaves(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	write(t, p, "hello")
	d1, err := s.Document(p, true)
	if err != nil {
		t.Fatal(err)
	}
	// Atomic save: new inode at the same path.
	tmp := filepath.Join(dir, ".doc.md.tmp")
	write(t, tmp, "hello again")
	if err := os.Rename(tmp, p); err != nil {
		t.Fatal(err)
	}
	d2, _ := s.Document(p, true)
	if d2.ID != d1.ID {
		t.Fatalf("atomic save made a new document")
	}
	// Move: same inode, new path.
	moved := filepath.Join(dir, "sub", "renamed.md")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	if err := os.Rename(p, moved); err != nil {
		t.Fatal(err)
	}
	d3, _ := s.Document(moved, true)
	if d3.ID != d1.ID {
		t.Fatalf("move made a new document")
	}
	if _, err := s.Document(filepath.Join(dir, "other.md"), false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestThreadLifecycleAndEvents(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	write(t, p, "We retry up to three times.")
	d, _ := s.Document(p, true)
	start, _ := s.LastSeq()
	text := "We retry up to three times."
	th, err := s.CreateThread(NewThread{DocID: d.ID, Scope: ScopeText, Anchor: anchor.New(text, 9, 26, ""), Author: human, Body: "Why three?", Text: text})
	if err != nil {
		t.Fatal(err)
	}
	if th.Awaiting() != Agent {
		t.Fatalf("awaiting %q", th.Awaiting())
	}
	if _, err := s.Reply(th.ID, agent, "Changed to four.", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Thread(th.ID)
	if len(got.Messages) != 2 || got.Awaiting() != Human {
		t.Fatalf("messages %d awaiting %q", len(got.Messages), got.Awaiting())
	}
	if _, err := s.SetStatus(th.ID, StatusResolved, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(th.ID, StatusResolved, agent); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(start, "", 0)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	want := []string{EvNewComment, EvNewReply, EvResolved}
	if len(types) != len(want) {
		t.Fatalf("events %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("events %v, want %v", types, want)
		}
	}
	if rev, ok := s.Revision(d.ID, th.Anchor.Rev); !ok || rev != text {
		t.Fatalf("revision not stored")
	}
}

func TestDeletingTheFirstMessageDeletesTheThread(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	write(t, p, "x")
	d, _ := s.Document(p, true)
	th, _ := s.CreateThread(NewThread{DocID: d.ID, Scope: ScopePage, Author: human, Body: "a"})
	r, _ := s.Reply(th.ID, agent, "b", "")
	gone, err := s.DeleteMessage(r.ID, human)
	if err != nil || gone {
		t.Fatalf("deleting a reply: gone=%v err=%v", gone, err)
	}
	gone, err = s.DeleteMessage(th.Messages[0].ID, human)
	if err != nil || !gone {
		t.Fatalf("deleting the first message: gone=%v err=%v", gone, err)
	}
	if _, err := s.Thread(th.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("thread still there")
	}
}

func TestResolveIDPrefixes(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	write(t, p, "x")
	d, _ := s.Document(p, true)
	th, _ := s.CreateThread(NewThread{DocID: d.ID, Scope: ScopePage, Author: human, Body: "a"})
	r, _ := s.Reply(th.ID, agent, "b", "")
	if tid, mid, err := s.ResolveID(d.ID, th.ID[:5]); err != nil || tid != th.ID || mid != "" {
		t.Fatalf("thread prefix: %s %s %v", tid, mid, err)
	}
	if tid, mid, err := s.ResolveID("", r.ID); err != nil || tid != th.ID || mid != r.ID {
		t.Fatalf("message id: %s %s %v", tid, mid, err)
	}
	if _, _, err := s.ResolveID(d.ID, "zzzzzzzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestConcurrentWritersLoseNothing(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	write(t, p, "x")
	d, _ := s.Document(p, true)
	th, _ := s.CreateThread(NewThread{DocID: d.ID, Scope: ScopePage, Author: human, Body: "a"})
	// A second handle stands in for another process.
	s2, err := Open(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = s.Reply(th.ID, agent, "from one", "") }()
		go func() { defer wg.Done(); _, _ = s2.Reply(th.ID, human, "from two", "") }()
	}
	wg.Wait()
	got, _ := s.Thread(th.ID)
	if len(got.Messages) != 41 {
		t.Fatalf("got %d messages, want 41", len(got.Messages))
	}
}

func TestUpdateAnchorsIsCompareAndSet(t *testing.T) {
	s, dir := open(t)
	p := filepath.Join(dir, "doc.md")
	old := "one two three"
	write(t, p, old)
	d, _ := s.Document(p, true)
	// An agent's comment: the human-comment revision is kept on purpose, for
	// "changes since my last comment", so it would mask the collection.
	th, _ := s.CreateThread(NewThread{DocID: d.ID, Scope: ScopeText, Anchor: anchor.New(old, 4, 7, ""), Author: agent, Body: "c", Text: old})
	new := "zero one two three"
	moved := anchor.Map(old, new, th.Anchor, anchor.Options{})
	if err := s.UpdateAnchors(d.ID, moved.Rev, new, []AnchorUpdate{{ThreadID: th.ID, FromRev: th.Anchor.Rev, Anchor: moved}}, true); err != nil {
		t.Fatal(err)
	}
	// A second update still expecting the old revision is ignored.
	stale := moved
	stale.Start = 0
	_ = s.UpdateAnchors(d.ID, moved.Rev, new, []AnchorUpdate{{ThreadID: th.ID, FromRev: th.Anchor.Rev, Anchor: stale}}, true)
	got, _ := s.Thread(th.ID)
	if got.Anchor.Start != 9 || got.Anchor.Rev != anchor.Hash(new) {
		t.Fatalf("anchor %+v", got.Anchor)
	}
	if _, ok := s.Revision(d.ID, th.Anchor.Rev); ok {
		t.Fatalf("old revision kept although nothing refers to it")
	}
}
