// Package anchor keeps a comment attached to the text it was made on while the
// document changes underneath it.
//
// An anchor is a byte range in a document's source text, recorded together
// with a hash of the revision the range belongs to. When the document changes,
// the old revision is diffed against the new one and the range is carried
// through the diff, the way an editor keeps a cursor in place while text is
// inserted around it. Having both versions means there is nothing to search
// for: the diff says exactly which characters survived.
//
// When the old revision is not available (a comment imported from the old
// store, or a revision past the size cap) Locate falls back to searching for
// the quoted text with its surrounding context, then to the block the comment
// was made in, then to its section.
//
// The package is pure: no I/O, so every behaviour can be pinned by a test that
// is just an old document, a new document, an anchor and the expected result.
package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// States an anchor can be in after mapping. The zero value means the
// commented text is intact, wherever it now sits.
const (
	StateOK       = ""
	StateChanged  = "changed"  // part or all of the range was rewritten
	StateDeleted  = "deleted"  // the range was removed and nothing replaced it
	StateUnplaced = "unplaced" // no way to tell where it belongs any more
)

// contextLen is how much text either side of a range is kept as context.
const contextLen = 32

// Element describes the element an element comment was made on. On markdown
// and static HTML the element also has a source range; Element is what finds
// it on a page whose DOM is built by script, and what labels it in listings.
type Element struct {
	Tag      string `json:"tag"`
	Label    string `json:"label,omitempty"`
	Selector string `json:"selector,omitempty"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
}

// Legacy carries the anchoring fields of a comment imported from the
// JSON-per-document store, used only until the comment is placed once.
type Legacy struct {
	BlockText string `json:"block_text,omitempty"`
	BlockHash string `json:"block_hash,omitempty"`
	PrevText  string `json:"prev_text,omitempty"`
	NextText  string `json:"next_text,omitempty"`
}

// Anchor is where a comment points.
type Anchor struct {
	// Rev is the hash of the revision Start and End refer to. Empty when the
	// anchor has never been placed against a known revision.
	Rev   string `json:"rev,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	// Quote is the source text of the range when the comment was made. It is
	// never updated, so a changed anchor can show what it used to say.
	Quote string `json:"quote,omitempty"`
	// Display is the text as the commenter saw it rendered, for the UI.
	Display string   `json:"display,omitempty"`
	Prefix  string   `json:"prefix,omitempty"`
	Suffix  string   `json:"suffix,omitempty"`
	Section string   `json:"section,omitempty"`
	State   string   `json:"state,omitempty"`
	Element *Element `json:"element,omitempty"`
	Legacy  *Legacy  `json:"legacy,omitempty"`
}

// Block is one block of the current document, as the fallback search sees it.
type Block struct {
	Start, End int    // source byte range
	Text       string // normalized rendered text
	Hash       string // HashBlockText(Text)
	PrevText   string // normalized text of the previous sibling block
	NextText   string // normalized text of the next sibling block
	// Find locates a phrase in the block's rendered text and returns its
	// source range. Optional; without it the phrase is searched in source.
	Find func(phrase string) (start, end int, ok bool)
	// Leaf is true for blocks that hold text directly (paragraphs, headings,
	// cells, code), the units a changed range is clamped to.
	Leaf bool
}

// Section is a heading and where its section starts in the source.
type Section struct {
	Path  string
	Start int
}

// Options is what the caller knows about the current document's structure.
// Everything is optional.
type Options struct {
	Blocks   []Block
	Sections []Section
}

// Hash names a revision of a document's text.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

// New records an anchor on text[start:end].
func New(text string, start, end int, section string) Anchor {
	start, end = clamp(start, end, len(text))
	start, end = alignRune(text, start), alignRune(text, end)
	p, s := Context(text, start, end)
	return Anchor{
		Rev:     Hash(text),
		Start:   start,
		End:     end,
		Quote:   text[start:end],
		Prefix:  p,
		Suffix:  s,
		Section: section,
	}
}

// Context returns up to contextLen bytes of text either side of a range,
// trimmed to whole characters.
func Context(text string, start, end int) (prefix, suffix string) {
	start, end = clamp(start, end, len(text))
	ps := alignRune(text, max(0, start-contextLen))
	se := alignRune(text, min(len(text), end+contextLen))
	return text[ps:start], text[end:se]
}

// Current returns the text an anchor covers in text, or "" when it is not
// placed or its offsets do not fit.
func Current(text string, a Anchor) string {
	if a.State == StateUnplaced || a.Start < 0 || a.End > len(text) || a.Start > a.End {
		return ""
	}
	return text[a.Start:a.End]
}

// Lines converts a byte range to 1-based first and last line numbers. A range
// ending just after a newline is reported as ending on the line before it.
func Lines(text string, start, end int) (int, int) {
	start, end = clamp(start, end, len(text))
	first := strings.Count(text[:start], "\n") + 1
	last := first + strings.Count(text[start:end], "\n")
	if end > start && text[end-1] == '\n' && last > first {
		last--
	}
	return first, last
}

// Map carries an anchor made against oldText through to newText.
func Map(oldText, newText string, a Anchor, opt Options) Anchor {
	out := a
	out.Rev = Hash(newText)
	if oldText == newText || a.State == StateUnplaced {
		return out
	}
	s, e := clamp(a.Start, a.End, len(oldText))
	h := diffHunks(oldText, newText)
	ns, ne := mapStart(h, s), mapEnd(h, e)
	if ne < ns {
		ne = ns
	}
	if s == e {
		// A point (an anchor whose text was already deleted) stays a point.
		ne = ns
	}
	out.Start, out.End = ns, ne
	out.State = classify(newText, out, a)

	if out.State == StateDeleted || (out.State == StateChanged && similarity(a.Quote, newText[ns:ne]) < 0.5) {
		// The diff sees a moved section as a deletion here and an insertion
		// somewhere else. If the exact quote is elsewhere with its context,
		// the text moved rather than went away.
		if qs, ok := findQuote(newText, a, ns); ok {
			out.Start, out.End, out.State = qs, qs+len(a.Quote), StateOK
			return out
		}
	}
	if out.State == StateChanged {
		out.Start, out.End = clampToBlock(out.Start, out.End, e-s, opt.Blocks)
	}
	return out
}

// Locate places an anchor in text without the revision it was made against.
func Locate(text string, a Anchor, opt Options) Anchor {
	out := a
	out.Rev = Hash(text)
	if a.Quote != "" {
		if qs, ok := findQuote(text, a, a.Start); ok {
			out.Start, out.End, out.State = qs, qs+len(a.Quote), StateOK
			return out
		}
	}
	if a.Legacy != nil && len(opt.Blocks) > 0 {
		if i, exact := resolveLegacyBlock(a, opt.Blocks); i >= 0 {
			b := opt.Blocks[i]
			phrase := a.Display
			if phrase == "" {
				phrase = a.Quote
			}
			if a.Element == nil && phrase != "" {
				if ps, pe, ok := findInBlock(text, b, phrase); ok {
					out.Start, out.End, out.State = ps, pe, StateOK
					return out
				}
				out.Start, out.End, out.State = b.Start, b.End, StateChanged
				return out
			}
			out.Start, out.End = b.Start, b.End
			out.State = StateOK
			if !exact {
				out.State = StateChanged
			}
			return out
		}
	}
	if a.Section != "" {
		for _, sec := range opt.Sections {
			if sec.Path == a.Section {
				out.Start, out.End, out.State = sec.Start, sec.Start, StateDeleted
				return out
			}
		}
	}
	out.Start, out.End, out.State = 0, 0, StateUnplaced
	return out
}

// classify names the state of a freshly mapped range.
func classify(text string, mapped, orig Anchor) string {
	if mapped.Start == mapped.End {
		if orig.Quote == "" && orig.Start == orig.End {
			return orig.State
		}
		return StateDeleted
	}
	cur := text[mapped.Start:mapped.End]
	if cur == orig.Quote || collapseSpace(cur) == collapseSpace(orig.Quote) {
		return StateOK
	}
	return StateChanged
}

// clampToBlock stops a changed range from spreading across a rewrite much
// larger than the text originally commented on: it is cut back to the end of
// the block where the replacement starts.
func clampToBlock(start, end, origLen int, blocks []Block) (int, int) {
	if end-start <= 3*origLen+80 {
		return start, end
	}
	for _, b := range blocks {
		if b.Leaf && start >= b.Start && start < b.End {
			if end > b.End {
				end = b.End
			}
			return start, end
		}
	}
	return start, end
}

// findQuote looks for the anchor's quote in text, using its context to choose
// between occurrences and to reject a short quote found with none of its
// context. hint is where the anchor is expected to be.
func findQuote(text string, a Anchor, hint int) (int, bool) {
	q := a.Quote
	if q == "" {
		return 0, false
	}
	type cand struct{ at, score int }
	var cands []cand
	for from := 0; from <= len(text)-len(q); {
		i := strings.Index(text[from:], q)
		if i < 0 {
			break
		}
		at := from + i
		score := commonSuffix(a.Prefix, text[:at]) + commonPrefix(a.Suffix, text[at+len(q):])
		cands = append(cands, cand{at, score})
		from = at + max(1, len(q))
	}
	if len(cands) == 0 {
		return 0, false
	}
	best, second := -1, -1
	for i, c := range cands {
		if best < 0 || c.score > cands[best].score ||
			(c.score == cands[best].score && abs(c.at-hint) < abs(cands[best].at-hint)) {
			second = best
			best = i
		} else if second < 0 || c.score > cands[second].score {
			second = i
		}
	}
	b := cands[best]
	hasContext := a.Prefix != "" || a.Suffix != ""
	switch {
	case len(cands) == 1 && (len(q) >= 12 || !hasContext || b.score >= 6):
		return b.at, true
	case len(cands) > 1 && b.score >= 6 && b.score-cands[second].score >= 4:
		return b.at, true
	case len(cands) > 1 && !hasContext && len(q) >= 12:
		return b.at, true
	}
	return 0, false
}

// findInBlock finds a phrase inside a block and returns its source range.
func findInBlock(text string, b Block, phrase string) (int, int, bool) {
	if b.Find != nil {
		return b.Find(phrase)
	}
	if b.Start < 0 || b.End > len(text) || b.Start > b.End {
		return 0, 0, false
	}
	if i := strings.Index(text[b.Start:b.End], phrase); i >= 0 {
		return b.Start + i, b.Start + i + len(phrase), true
	}
	return 0, 0, false
}

func commonSuffix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func clamp(start, end, n int) (int, int) {
	start = min(max(start, 0), n)
	end = min(max(end, start), n)
	return start, end
}

// alignRune moves a byte offset back to the start of the character it is in.
func alignRune(s string, i int) int {
	for i > 0 && i < len(s) && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
