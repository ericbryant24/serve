package anchor

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// hunk is one contiguous change: OldLen bytes at OldStart in the old text were
// replaced by NewLen bytes at NewStart in the new text. A pure insertion has
// OldLen 0, a pure deletion NewLen 0. Hunks are in document order.
type hunk struct {
	OldStart, OldLen int
	NewStart, NewLen int
}

// diffHunks diffs two texts word by word. Words rather than characters, so
// that rewording a phrase reads as one replacement instead of a scatter of
// surviving letters; semantic cleanup then folds short coincidental matches
// ("the", "a") inside a rewrite into the replacement around them.
func diffHunks(a, b string) []hunk {
	dict := map[string]rune{}
	var tokens []string
	next := rune(0xE000) // private use area, clear of anything diffmatchpatch treats specially
	encode := func(s string) []rune {
		toks := tokenize(s)
		out := make([]rune, len(toks))
		for i, t := range toks {
			r, ok := dict[t]
			if !ok {
				r = next
				dict[t] = r
				tokens = append(tokens, t)
				next++
				if next == 0xF900 {
					next = 0xF0000 // continue in supplementary private use
				}
			}
			out[i] = r
		}
		return out
	}
	ra, rb := encode(a), encode(b)
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = 2 * time.Second
	var diffs []diffmatchpatch.Diff
	var decode func(string) int
	if next > maxTokenRune {
		// More distinct words than the private use area holds: diff by
		// character instead. Coarser, but still exact about what survived.
		diffs = dmp.DiffCleanupSemantic(dmp.DiffMain(a, b, false))
		decode = func(s string) int { return len(s) }
	} else {
		diffs = dmp.DiffCleanupSemantic(dmp.DiffMainRunes(ra, rb, false))
		decode = func(s string) int {
			n := 0
			for _, r := range s {
				n += len(tokens[runeIndex(r)])
			}
			return n
		}
	}

	var hunks []hunk
	oldPos, newPos := 0, 0
	var cur *hunk
	flush := func() {
		if cur != nil {
			hunks = append(hunks, *cur)
			cur = nil
		}
	}
	for _, d := range diffs {
		n := decode(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			oldPos += n
			newPos += n
		case diffmatchpatch.DiffDelete:
			if cur == nil {
				cur = &hunk{OldStart: oldPos, NewStart: newPos}
			}
			cur.OldLen += n
			oldPos += n
		case diffmatchpatch.DiffInsert:
			if cur == nil {
				cur = &hunk{OldStart: oldPos, NewStart: newPos}
			}
			cur.NewLen += n
			newPos += n
		}
	}
	flush()
	return hunks
}

// maxTokenRune is the last private use code point a word can be encoded as.
const maxTokenRune = 0xFFFFD

func runeIndex(r rune) int {
	if r >= 0xF0000 {
		return int(r-0xF0000) + (0xF900 - 0xE000)
	}
	return int(r - 0xE000)
}

// tokenize splits text into runs of letters and digits, runs of whitespace,
// and single other characters. Concatenating the tokens gives back the text.
func tokenize(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		j := i + size
		switch {
		case isWordRune(r):
			for j < len(s) {
				r2, s2 := utf8.DecodeRuneInString(s[j:])
				if !isWordRune(r2) {
					break
				}
				j += s2
			}
		case unicode.IsSpace(r):
			for j < len(s) {
				r2, s2 := utf8.DecodeRuneInString(s[j:])
				if !unicode.IsSpace(r2) {
					break
				}
				j += s2
			}
		}
		out = append(out, s[i:j])
		i = j
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '\''
}

// mapStart carries the start of a range through the hunks. A start inside
// replaced text moves to the start of the replacement, so the rewrite is
// included; an insertion exactly at the start is left outside the range.
func mapStart(hs []hunk, p int) int {
	delta := 0
	for _, h := range hs {
		if p < h.OldStart {
			return p + delta
		}
		if h.OldLen == 0 && p == h.OldStart {
			return h.NewStart + h.NewLen
		}
		if h.OldLen > 0 && p < h.OldStart+h.OldLen {
			return h.NewStart
		}
		delta = (h.NewStart + h.NewLen) - (h.OldStart + h.OldLen)
	}
	return p + delta
}

// mapEnd carries the end of a range through the hunks. An end inside replaced
// text moves to the end of the replacement; an insertion exactly at the end,
// or a replacement starting exactly there, is left outside the range.
func mapEnd(hs []hunk, p int) int {
	delta := 0
	for _, h := range hs {
		if p < h.OldStart {
			return p + delta
		}
		if p == h.OldStart {
			return h.NewStart
		}
		if h.OldLen > 0 && p <= h.OldStart+h.OldLen {
			return h.NewStart + h.NewLen
		}
		delta = (h.NewStart + h.NewLen) - (h.OldStart + h.OldLen)
	}
	return p + delta
}

// similarity scores two strings from 0 (nothing in common) to 1 (identical)
// by the Sørensen–Dice coefficient over their words.
func similarity(a, b string) float64 {
	a, b = strings.ToLower(collapseSpace(a)), strings.ToLower(collapseSpace(b))
	if a == b {
		return 1
	}
	return dice(a, b)
}
