package anchor

import (
	"strings"
	"testing"
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

// anchorOn makes an anchor on the first occurrence of phrase in text.
func anchorOn(t *testing.T, text, phrase string) Anchor {
	t.Helper()
	i := strings.Index(text, phrase)
	if i < 0 {
		t.Fatalf("phrase %q not in text", phrase)
	}
	return New(text, i, i+len(phrase), "")
}

func TestMap(t *testing.T) {
	cases := []struct {
		name      string
		old, new  string
		phrase    string
		wantState string
		wantText  string // text covered after mapping
	}{
		{
			name:      "unrelated edit elsewhere",
			old:       spec,
			new:       strings.Replace(spec, "Who owns", "Which team owns", 1),
			phrase:    "up to three times",
			wantState: StateOK,
			wantText:  "up to three times",
		},
		{
			name:      "paragraph inserted above",
			old:       spec,
			new:       strings.Replace(spec, "## Goals", "## Summary\n\nCard retries recover revenue.\n\n## Goals", 1),
			phrase:    "up to three times",
			wantState: StateOK,
			wantText:  "up to three times",
		},
		{
			name:      "the fix the comment asked for",
			old:       spec,
			new:       strings.Replace(spec, "up to three times over five days.", "up to four times over seven days, with backoff between attempts.", 1),
			phrase:    "up to three times",
			wantState: StateChanged,
			wantText:  "up to four times",
		},
		{
			name:      "one word inside the quote",
			old:       spec,
			new:       strings.Replace(spec, "three", "four", 1),
			phrase:    "three",
			wantState: StateChanged,
			wantText:  "four",
		},
		{
			name:      "paragraph rewritten under unchanged neighbours",
			old:       spec,
			new:       strings.Replace(spec, "We do not retry ACH payments.", "ACH and SEPA payments are out of scope for this version.", 1),
			phrase:    "ACH payments",
			wantState: StateChanged,
			wantText:  "ACH and SEPA payments are out of scope for this version",
		},
		{
			name:      "paragraph deleted",
			old:       spec,
			new:       strings.Replace(spec, "Retries stop when the customer updates their card.\n\n", "", 1),
			phrase:    "customer updates their card",
			wantState: StateDeleted,
			wantText:  "",
		},
		{
			name: "section moved",
			old:  spec,
			new: strings.Replace(strings.Replace(spec, "## Non-goals\n\nWe do not retry ACH payments.\n\n", "", 1),
				"- Who owns the dunning emails?\n", "- Who owns the dunning emails?\n\n## Non-goals\n\nWe do not retry ACH payments.\n", 1),
			phrase:    "We do not retry ACH payments.",
			wantState: StateOK,
			wantText:  "We do not retry ACH payments.",
		},
		{
			name:      "list item extended",
			old:       spec,
			new:       strings.Replace(spec, "skip weekends?", "skip weekends and bank holidays?", 1),
			phrase:    "skip weekends",
			wantState: StateOK,
			wantText:  "skip weekends",
		},
		{
			name:      "text inserted right after the range stays outside it",
			old:       spec,
			new:       strings.Replace(spec, "card payments up", "card payments (Visa, Mastercard) up", 1),
			phrase:    "card payments",
			wantState: StateOK,
			wantText:  "card payments",
		},
		{
			name:      "text inserted right before the range stays outside it",
			old:       spec,
			new:       strings.Replace(spec, "failed card", "failed debit and card", 1),
			phrase:    "card payments",
			wantState: StateOK,
			wantText:  "card payments",
		},
		{
			name:      "reflowed across lines",
			old:       spec,
			new:       strings.Replace(spec, "up to three times over", "up to three\ntimes over", 1),
			phrase:    "up to three times",
			wantState: StateOK,
			wantText:  "up to three\ntimes",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := anchorOn(t, c.old, c.phrase)
			got := Map(c.old, c.new, a, Options{})
			if got.State != c.wantState {
				t.Errorf("state = %q, want %q (covers %q)", got.State, c.wantState, Current(c.new, got))
			}
			if cur := Current(c.new, got); cur != c.wantText {
				t.Errorf("covers %q, want %q", cur, c.wantText)
			}
			if got.Quote != c.phrase {
				t.Errorf("quote changed to %q", got.Quote)
			}
			if got.Rev != Hash(c.new) {
				t.Errorf("rev not updated")
			}
		})
	}
}

func TestMapStaysOnTheCommentedCopyOfRepeatedText(t *testing.T) {
	old := "Intro.\n\nSee the docs.\n\nMiddle.\n\nSee the docs.\n"
	second := strings.LastIndex(old, "See the docs.")
	a := New(old, second, second+len("See the docs."), "")
	new := "New first paragraph.\n\n" + old
	got := Map(old, new, a, Options{})
	want := strings.LastIndex(new, "See the docs.")
	if got.Start != want || got.State != StateOK {
		t.Fatalf("got start %d state %q, want %d ok", got.Start, got.State, want)
	}
}

func TestDeletedTextThatComesBackIsFoundAgain(t *testing.T) {
	a := anchorOn(t, spec, "Retries stop when the customer updates their card.")
	gone := strings.Replace(spec, "Retries stop when the customer updates their card.\n\n", "", 1)
	mid := Map(spec, gone, a, Options{})
	if mid.State != StateDeleted {
		t.Fatalf("state %q, want deleted", mid.State)
	}
	back := Map(gone, spec, mid, Options{})
	if back.State != StateOK || Current(spec, back) != a.Quote {
		t.Fatalf("after undo: state %q covers %q", back.State, Current(spec, back))
	}
}

func TestChangedRangeIsClampedToItsBlock(t *testing.T) {
	old := "# T\n\nShort line here.\n\nAnother paragraph.\n"
	a := anchorOn(t, old, "line")
	long := strings.Repeat("A much longer rewrite of the first paragraph. ", 6)
	new := "# T\n\n" + long + "\n\nAnother paragraph rewritten too, at length, so the diff merges both.\n"
	blocks := []Block{{Start: 5, End: 5 + len(long), Leaf: true}}
	got := Map(old, new, a, Options{Blocks: blocks})
	if got.End > 5+len(long) {
		t.Fatalf("range ran past its block: %d > %d", got.End, 5+len(long))
	}
}

func TestLocateWithoutTheOldRevision(t *testing.T) {
	a := anchorOn(t, spec, "skip weekends")
	a.Rev, a.Start, a.End = "", 0, 0
	edited := "Preamble.\n\n" + spec
	got := Locate(edited, a, Options{})
	if got.State != StateOK || Current(edited, got) != "skip weekends" {
		t.Fatalf("state %q covers %q", got.State, Current(edited, got))
	}
}

func TestLocateRefusesAShortQuoteWithNoContext(t *testing.T) {
	a := Anchor{Quote: "the", Prefix: "zzzz", Suffix: "qqqq"}
	got := Locate("the cat and the dog", a, Options{})
	if got.State != StateUnplaced {
		t.Fatalf("placed a short quote with no matching context: %+v", got)
	}
}

func TestLocateLegacyFallsBackToTheBlock(t *testing.T) {
	text := "# A\n\nFirst paragraph here.\n\nSecond paragraph, reworded completely now.\n"
	blocks := []Block{
		{Start: 0, End: 3, Text: "A", Hash: HashBlockText("A"), NextText: "First paragraph here.", Leaf: true},
		{Start: 5, End: 26, Text: "First paragraph here.", Hash: HashBlockText("First paragraph here."), PrevText: "A", NextText: "Second paragraph, reworded completely now.", Leaf: true},
		{Start: 28, End: 70, Text: "Second paragraph, reworded completely now.", Hash: HashBlockText("Second paragraph, reworded completely now."), PrevText: "First paragraph here.", Leaf: true},
	}
	a := Anchor{
		Quote: "original words",
		Legacy: &Legacy{
			BlockText: "Second paragraph, reworded slightly.",
			BlockHash: HashBlockText("Second paragraph, reworded slightly."),
			PrevText:  "First paragraph here.",
		},
	}
	got := Locate(text, a, Options{Blocks: blocks})
	if got.State != StateChanged || got.Start != 28 {
		t.Fatalf("got %+v", got)
	}
}

func TestLocateFallsBackToTheSection(t *testing.T) {
	a := Anchor{Quote: "text that is gone for good", Section: "Spec › Goals"}
	got := Locate("# Spec\n\n## Goals\n\nNew text.\n", a, Options{Sections: []Section{{Path: "Spec › Goals", Start: 8}}})
	if got.State != StateDeleted || got.Start != 8 || got.End != 8 {
		t.Fatalf("got %+v", got)
	}
}

func TestLines(t *testing.T) {
	text := "a\nbb\nccc\n"
	cases := []struct{ s, e, first, last int }{
		{0, 1, 1, 1},
		{2, 4, 2, 2},
		{2, 5, 2, 2}, // ends just after a newline
		{0, 8, 1, 3},
	}
	for _, c := range cases {
		f, l := Lines(text, c.s, c.e)
		if f != c.first || l != c.last {
			t.Errorf("Lines(%d,%d) = %d,%d want %d,%d", c.s, c.e, f, l, c.first, c.last)
		}
	}
}

func TestTokenizeRoundTrips(t *testing.T) {
	for _, s := range []string{spec, "héllo wörld — ok", "", "a  b\n\tc"} {
		if got := strings.Join(tokenize(s), ""); got != s {
			t.Fatalf("tokenize lost text: %q", got)
		}
	}
}

func TestMapHandlesManyDistinctWords(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 80000; i++ {
		b.WriteString("w")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(itoa(i))
		b.WriteByte(' ')
	}
	old := b.String() + "the commented phrase."
	a := anchorOn(t, old, "commented phrase")
	new := "prefix " + old
	got := Map(old, new, a, Options{})
	if got.State != StateOK || Current(new, got) != "commented phrase" {
		t.Fatalf("state %q covers %q", got.State, Current(new, got))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}
