package render

import (
	"html"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/util"
)

// writeText renders source[s:e] the way goldmark's HTML writer renders a text
// node (backslash escapes removed, entity references resolved, NUL replaced)
// and records the result in lb run by run. It returns the HTML to write.
func writeText(source []byte, s, e int, lb *leafBuilder) string {
	var out strings.Builder
	litStart := s
	flushLiteral := func(upTo int) {
		if upTo > litStart {
			t := string(source[litStart:upTo])
			if lb != nil {
				lb.literal(litStart, upTo, t)
			}
			out.WriteString(html.EscapeString(t))
		}
	}
	emit := func(from, to int, text string) {
		flushLiteral(from)
		if lb != nil {
			lb.atomic(from, to, text)
		}
		out.WriteString(html.EscapeString(text))
		litStart = to
	}
	for i := s; i < e; i++ {
		c := source[i]
		switch {
		case c == '\\' && i+1 < e && util.IsPunct(source[i+1]):
			emit(i, i+2, string(source[i+1]))
			i++
		case c == 0:
			emit(i, i+1, "�")
		case c == '&':
			if text, end, ok := entityAt(source, i, e); ok {
				emit(i, end, text)
				i = end - 1
			}
		}
	}
	flushLiteral(e)
	return out.String()
}

// entityAt reads a character reference starting at source[i] ('&') and
// returns its decoded text and the end of the reference, matching what
// goldmark resolves: &name; for HTML5 names, &#123; and &#x1F;.
func entityAt(source []byte, i, limit int) (string, int, bool) {
	j := i + 1
	if j < limit && source[j] == '#' {
		j++
		hex := j < limit && (source[j] == 'x' || source[j] == 'X')
		if hex {
			j++
		}
		start := j
		for j < limit && j-start < 8 && (isDigit(source[j]) || (hex && isHexLetter(source[j]))) {
			j++
		}
		if j == start || j >= limit || source[j] != ';' {
			return "", 0, false
		}
		if (hex && j-start >= 7) || (!hex && j-start >= 8) {
			return "", 0, false
		}
		base := 10
		if hex {
			base = 16
		}
		v, err := strconv.ParseUint(string(source[start:j]), base, 32)
		if err != nil {
			return "", 0, false
		}
		return string(util.ToValidRune(rune(v))), j + 1, true
	}
	start := j
	for j < limit && (isDigit(source[j]) || isLetter(source[j])) {
		j++
	}
	if j == start || j >= limit || source[j] != ';' {
		return "", 0, false
	}
	ent, ok := util.LookUpHTML5EntityByName(string(source[start:j]))
	if !ok {
		return "", 0, false
	}
	return string(ent.Characters), j + 1, true
}

func isDigit(c byte) bool     { return c >= '0' && c <= '9' }
func isLetter(c byte) bool    { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isHexLetter(c byte) bool { return (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
