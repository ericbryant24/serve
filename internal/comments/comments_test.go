package comments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serve/internal/anchor"
	"serve/internal/config"
	"serve/internal/store"
)

const spec = `# Payment retry spec

## Goals

We retry failed card payments up to three times over five days.

Retries stop when the customer updates their card.

## Non-goals

We do not retry ACH payments.

## Open questions

- Should retries skip weekends?
- Who owns the dunning emails?
`

var human = store.Author{Kind: store.Human, Name: "Eric"}

func setup(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "serve.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(p, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: st, Config: config.Config{}}, p
}

// selectText makes a browser-style selection of the first occurrence of
// phrase in a leaf's rendered text.
func selectText(t *testing.T, s *Service, path, phrase string) Selection {
	t.Helper()
	doc, err := s.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range doc.Leaves() {
		if i := strings.Index(b.Text, phrase); i >= 0 {
			rs := utf16Len(b.Text[:i])
			return Selection{StartKey: b.Key, StartOffset: rs, EndKey: b.Key, EndOffset: rs + utf16Len(phrase), Quote: phrase}
		}
	}
	t.Fatalf("%q not on the page", phrase)
	return Selection{}
}

func edit(t *testing.T, p, old, new string) {
	t.Helper()
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), old) {
		t.Fatalf("%q not in file", old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(dv *DocView, id string) ThreadView {
	for _, v := range dv.Threads {
		if v.ID == id {
			return v
		}
	}
	return ThreadView{}
}

func TestTheReviewLoop(t *testing.T) {
	s, p := setup(t)
	three, err := s.CreateText(p, selectText(t, s, p, "up to three times"), human, "Why three? Stripe recommends four.")
	if err != nil {
		t.Fatal(err)
	}
	weekends, _ := s.CreateText(p, selectText(t, s, p, "skip weekends"), human, "Yes, skip weekends.")
	ach, _ := s.CreateText(p, selectText(t, s, p, "ACH payments"), human, "What about SEPA?")
	page, _ := s.CreatePage(p, human, "Needs a rollout section.")

	if three.Location.LineStart != 5 || three.Location.State != LocOK {
		t.Fatalf("new comment at %+v", three.Location)
	}

	// The agent edits the file the way it would to address the comments.
	edit(t, p, "## Goals", "## Summary\n\nCard retries recover revenue that would otherwise churn.\n\n## Goals")
	edit(t, p, "up to three times over five days.", "up to four times over seven days, with backoff between attempts.")
	edit(t, p, "We do not retry ACH payments.", "ACH and SEPA payments are out of scope for this version.")
	edit(t, p, "skip weekends?", "skip weekends and bank holidays?")

	dv, err := s.Open(p, false)
	if err != nil {
		t.Fatal(err)
	}
	got := find(dv, three.ID)
	if got.Location.State != LocChanged || got.Location.LineStart != 9 || !strings.HasPrefix(got.Location.Current, "up to four times") {
		t.Errorf("addressed comment: %+v", got.Location)
	}
	if len(got.Placement.Segments) != 1 || !strings.HasPrefix(got.Placement.Segments[0].Text, "up to four") {
		t.Errorf("addressed comment placement: %+v", got.Placement)
	}
	if got.Anchor.Quote != "up to three times" {
		t.Errorf("original quote lost: %q", got.Anchor.Quote)
	}
	w := find(dv, weekends.ID)
	if w.Location.State != LocOK || w.Location.Current != "skip weekends" || w.Location.LineStart != 19 {
		t.Errorf("untouched comment: %+v", w.Location)
	}
	a := find(dv, ach.ID)
	if a.Location.State != LocChanged || !strings.Contains(a.Location.Current, "SEPA") {
		t.Errorf("rewritten paragraph: %+v", a.Location)
	}
	if find(dv, page.ID).Location.State != LocPage {
		t.Errorf("page comment state")
	}

	// The remap was saved: a second open finds every anchor already current.
	th, _ := s.Store.Thread(three.ID)
	if th.Anchor.Rev != anchor.Hash(mustRead(t, p)) {
		t.Errorf("remapped anchor not persisted")
	}
}

func TestStaleSelectionFallsBackToTheQuote(t *testing.T) {
	s, p := setup(t)
	sel := selectText(t, s, p, "dunning emails")
	edit(t, p, "# Payment retry spec", "# Payment retry spec\n\nA new first paragraph.")
	sel.StartKey, sel.EndKey = "gone", "gone"
	v, err := s.CreateText(p, sel, human, "who?")
	if err != nil {
		t.Fatal(err)
	}
	if v.Location.Current != "dunning emails" {
		t.Fatalf("got %+v", v.Location)
	}
}

func TestElementComment(t *testing.T) {
	s, p := setup(t)
	doc, _ := s.Read(p)
	var key string
	for _, b := range doc.Blocks {
		if b.Kind == "ul" {
			key = b.Key
		}
	}
	v, err := s.CreateElement(p, ElementRef{Key: key, Element: anchor.Element{Tag: "ul", Label: "Should retries skip weekends?"}}, human, "Reorder these")
	if err != nil {
		t.Fatal(err)
	}
	if v.Placement.Element != key || v.Location.LineStart != 15 || v.Location.LineEnd != 16 {
		t.Fatalf("element: %+v %+v", v.Placement, v.Location)
	}
}

func mustRead(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
