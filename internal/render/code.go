package render

import (
	stdhtml "html"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"serve/internal/anchor"
)

// maxHighlightLines is the size past which a file is shown without syntax
// highlighting; the view stays usable on huge logs.
const maxHighlightLines = 20000

// Code renders a source file one line per leaf, highlighted when a lexer
// recognises the file. Line numbers come from CSS, so they are not part of the
// text and selections and copies contain only code.
func Code(source, filename string) *Doc {
	var l chroma.Lexer
	if strings.Count(source, "\n") < maxHighlightLines {
		l = lexers.Match(filename)
		if l == nil {
			l = lexers.Analyse(source)
		}
	}
	kind := KindText
	lang := ""
	if l != nil {
		kind = KindCode
		lang = l.Config().Name
		l = chroma.Coalesce(l)
	}
	doc := &Doc{Kind: kind, Source: source, Rev: anchor.Hash(source), Lang: lang}
	lineHTML := splitHighlighted(source, l)
	keys := keyer{}
	start := 0
	var all strings.Builder
	for i, h := range lineHTML {
		end := strings.IndexByte(source[start:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += start
		}
		text := source[start:end]
		text = strings.TrimSuffix(text, "\r")
		b := Block{Key: keys.key("line", text), Kind: "line", Start: start, End: start + len(text), Parent: -1}
		lb := &leafBuilder{lastE: start}
		lb.literal(start, start+len(text), text)
		lb.finish(&b)
		doc.Blocks = append(doc.Blocks, b)
		row := `<div class="cl" data-n="` + strconv.Itoa(i+1) + `"><span class="lc" data-b="` + b.Key + `" data-t="">` + h + "</span></div>\n"
		doc.Tops = append(doc.Tops, Top{Key: shortHash(row), Src: shortHash(text), HTML: row})
		all.WriteString(row)
		start = end + 1
		if end >= len(source) {
			break
		}
	}
	doc.HTML = all.String()
	doc.index()
	return doc
}

// splitHighlighted highlights source and splits the HTML into one string per
// line, closing and reopening spans across line breaks.
func splitHighlighted(source string, l chroma.Lexer) []string {
	lines := strings.Split(source, "\n")
	out := make([]string, len(lines))
	if l == nil {
		for i, ln := range lines {
			out[i] = stdhtml.EscapeString(strings.TrimSuffix(ln, "\r"))
		}
		return out
	}
	it, err := l.Tokenise(nil, source)
	if err != nil {
		return splitHighlighted(source, nil)
	}
	var cur strings.Builder
	li := 0
	got := 0
	for t := it(); t != chroma.EOF; t = it() {
		v := t.Value
		if got+len(v) > len(source) {
			v = v[:len(source)-got]
		}
		if v == "" {
			continue
		}
		if source[got:got+len(v)] != v {
			return splitHighlighted(source, nil)
		}
		got += len(v)
		cls := tokenClass(t.Type)
		parts := strings.Split(v, "\n")
		for pi, p := range parts {
			if pi > 0 {
				if li < len(out) {
					out[li] = cur.String()
				}
				li++
				cur.Reset()
			}
			p = strings.TrimSuffix(p, "\r")
			if p == "" {
				continue
			}
			if cls != "" {
				cur.WriteString(`<span class="` + cls + `">` + stdhtml.EscapeString(p) + "</span>")
			} else {
				cur.WriteString(stdhtml.EscapeString(p))
			}
		}
	}
	if li < len(out) {
		out[li] = cur.String()
	}
	if got != len(source) {
		return splitHighlighted(source, nil)
	}
	return out
}
