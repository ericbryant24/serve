package anchor

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Matching for comments imported from the JSON-per-document store, which
// recorded the block a comment was made in (its text, a hash of it, and its
// neighbours' text) rather than a source range. Once such a comment is placed
// it gets a real range and never comes back here.

const (
	fuzzyMatchFloor = 0.5
	neighborBonus   = 0.15
	anchorBonus     = 0.2
	contextMargin   = 0.05
)

// Normalize collapses a block's text to the form the old store hashed:
// whitespace runs become one space and typographic punctuation folds back to
// ASCII, so text read from the source and from a rendered page compare equal.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace, lastHyphen := false, false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace, lastHyphen = false, false
		}
		switch r {
		case '‘', '’', '‚', '‛', '′':
			b.WriteByte('\'')
			lastHyphen = false
		case '“', '”', '„', '‟', '″':
			b.WriteByte('"')
			lastHyphen = false
		case '-', '–', '—', '−':
			if !lastHyphen {
				b.WriteByte('-')
			}
			lastHyphen = true
		case '…':
			b.WriteString("...")
			lastHyphen = false
		default:
			b.WriteRune(r)
			lastHyphen = false
		}
	}
	return b.String()
}

// HashBlockText hashes normalized block text the way the old store did.
func HashBlockText(normalized string) string {
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:8])
}

// resolveLegacyBlock finds the block an imported comment was made in. The
// second result is true when the block was identified outright (same hash or
// same text) rather than by similarity.
func resolveLegacyBlock(a Anchor, blocks []Block) (int, bool) {
	l := a.Legacy
	if l.BlockHash != "" {
		if cands := blocksWhere(blocks, func(b Block) bool { return b.Hash == l.BlockHash }); len(cands) == 1 {
			return cands[0], true
		} else if len(cands) > 1 {
			return pickByContext(l, blocks, cands)
		}
	}
	norm := Normalize(l.BlockText)
	if norm != "" {
		if cands := blocksWhere(blocks, func(b Block) bool { return b.Text == norm }); len(cands) == 1 {
			return cands[0], true
		} else if len(cands) > 1 {
			return pickByContext(l, blocks, cands)
		}
	}
	best, bestScore := -1, 0.0
	phrase := Normalize(a.Display)
	if phrase == "" {
		phrase = Normalize(a.Quote)
	}
	for i, b := range blocks {
		score := dice(norm, b.Text)
		score += neighborBonus * dice(l.PrevText, b.PrevText)
		score += neighborBonus * dice(l.NextText, b.NextText)
		if phrase != "" && strings.Contains(b.Text, phrase) {
			score += anchorBonus
		}
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best >= 0 && bestScore >= fuzzyMatchFloor {
		return best, false
	}
	return -1, false
}

func blocksWhere(blocks []Block, f func(Block) bool) []int {
	var out []int
	for i, b := range blocks {
		if f(b) {
			out = append(out, i)
		}
	}
	return out
}

// pickByContext chooses between blocks with identical text by their
// neighbours. It reports a confident choice only when the neighbourhood
// actually told the copies apart.
func pickByContext(l *Legacy, blocks []Block, cands []int) (int, bool) {
	best, bestScore, runnerUp := cands[0], -1.0, -1.0
	for _, i := range cands {
		score := dice(l.PrevText, blocks[i].PrevText) + dice(l.NextText, blocks[i].NextText)
		if score > bestScore {
			best, runnerUp, bestScore = i, bestScore, score
		} else if score > runnerUp {
			runnerUp = score
		}
	}
	return best, bestScore-runnerUp > contextMargin
}

// dice scores two strings by the Sørensen–Dice coefficient over word bigrams
// (single words when either is too short to have a bigram).
func dice(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ag, bg := wordBigrams(a), wordBigrams(b)
	if len(ag) == 0 || len(bg) == 0 {
		ag, bg = wordCounts(a), wordCounts(b)
	}
	if len(ag) == 0 || len(bg) == 0 {
		return 0
	}
	shared, total := 0, 0
	for g, n := range ag {
		total += n
		if m, ok := bg[g]; ok {
			shared += min(n, m)
		}
	}
	for _, n := range bg {
		total += n
	}
	return 2 * float64(shared) / float64(total)
}

func wordCounts(s string) map[string]int {
	out := map[string]int{}
	for _, w := range strings.Fields(s) {
		out[w]++
	}
	return out
}

func wordBigrams(s string) map[string]int {
	words := strings.Fields(s)
	out := map[string]int{}
	for i := 0; i+1 < len(words); i++ {
		out[words[i]+" "+words[i+1]]++
	}
	return out
}
