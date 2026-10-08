package render

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"

	"serve/internal/anchor"
)

// HTML files are shown as they are, in an isolated frame, so serve does not
// render them. What it does compute is the page's text, so comments can still
// be anchored to source ranges: the text of every text node under <body>
// that is not inside script, style, template, noscript or textarea, skipping
// nodes that are only whitespace. The script inside the frame walks the DOM
// by the same rule, so offsets in that text mean the same thing on both
// sides. A page that builds its DOM in JavaScript will not match; its comments
// fall back to quote-and-context search in the page.

// BodyKey is the key of the one leaf an HTML file has.
const BodyKey = "body"

var skipTextIn = map[string]bool{
	"script": true, "style": true, "template": true, "noscript": true, "textarea": true,
	"head": true, "title": true,
}

// HTMLFile describes an HTML file for anchoring.
func HTMLFile(source string) *Doc {
	doc := &Doc{Kind: KindHTML, Source: source, Rev: anchor.Hash(source)}
	src := []byte(source)
	z := html.NewTokenizer(bytes.NewReader(src))
	lb := &leafBuilder{}
	pos := 0
	var stack []string
	skipping := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		raw := z.Raw()
		start := pos
		pos += len(raw)
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			n := string(name)
			if isVoid(n) {
				continue
			}
			stack = append(stack, n)
			if skipTextIn[n] {
				skipping++
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			n := string(name)
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == n {
					for _, popped := range stack[i:] {
						if skipTextIn[popped] {
							skipping--
						}
					}
					stack = stack[:i]
					break
				}
			}
		case html.TextToken:
			if skipping > 0 || strings.TrimSpace(string(raw)) == "" {
				continue
			}
			recordHTMLText(src, start, start+len(raw), lb)
		}
	}
	b := Block{Key: BodyKey, Kind: "body", Start: 0, End: len(source), Parent: -1}
	lb.finish(&b)
	doc.Blocks = []Block{b}
	doc.index()
	if t := htmlTitle(src); t != "" {
		doc.Title = t
	}
	doc.Refs = Refs(source)
	return doc
}

func htmlTitle(src []byte) string {
	z := html.NewTokenizer(bytes.NewReader(src))
	in := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken:
			name, _ := z.TagName()
			in = string(name) == "title"
		case html.TextToken:
			if in {
				return strings.TrimSpace(string(z.Text()))
			}
		case html.EndTagToken:
			in = false
		}
	}
}
