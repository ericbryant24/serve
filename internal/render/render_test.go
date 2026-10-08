package render

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const corpus = `---
title: "Retry spec"
marp: false
tags: [a, b]
---

# Payment retry spec

Intro with *emphasis*, **strong**, ` + "`code span`" + `, a [link](https://example.com "t"),
and an autolink <https://example.org/x>. Entities: &amp; &copy; &#169; &#x1F600; and
escapes \*not emphasis\* \[x\]. A soft
break and a hard
break, and a backslash\
break. Bare URL https://github.com/ericbryant24/serve here. Emoji 🎉 and 中文.

## Lists

- tight item one
- tight item **two**
  - nested item
- [ ] task open
- [x] task done

1. loose one

2. loose two

   with a second paragraph

## Quote and alert

> A plain quote
> over two lines.

> [!WARNING]
> Careful with ~~this~~ that.

## Table

| Name | Value | Note |
|:-----|------:|:----:|
| a    | 1     | *x*  |
| b ` + "`c`" + ` | 2 | [l](u) |

## Code

` + "```go" + `
func main() {
	fmt.Println("hi <there> & bye")
}
` + "```" + `

` + "```mermaid" + `
graph TD; A-->B
` + "```" + `

    indented code
    block

` + "```" + `
no language
` + "```" + `

<div align="center">
  <b>Bold &amp; centred</b>
  <script>alert(1)</script>
</div>

<details>
<summary>More</summary>

Hidden text.

</details>

Inline <kbd>Ctrl</kbd>+<kbd>C</kbd> and <span onclick="x()">span</span>.

Footnote here[^1] and image ![alt text](pic.png).

[^1]: The footnote text.

## Repeated

Same text.

Same text.
`

// textContent mimics the DOM property.
func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func attr(n *html.Node, k string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val, true
		}
	}
	return "", false
}

// leafTexts parses rendered HTML the way a browser would and returns the
// textContent of every element marked as a leaf.
func leafTexts(t *testing.T, h string) map[string]string {
	t.Helper()
	nodes, err := html.ParseFragment(strings.NewReader(h), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if k, ok := attr(n, "data-b"); ok {
				if _, leaf := attr(n, "data-t"); leaf {
					out[k] = textContent(n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// checkDoc verifies that every leaf's recorded text is exactly what a browser
// would report as its textContent, and that runs map back to source.
func checkDoc(t *testing.T, d *Doc) {
	t.Helper()
	got := leafTexts(t, d.HTML)
	leaves := d.Leaves()
	if len(leaves) == 0 {
		t.Fatal("no leaves")
	}
	for _, b := range leaves {
		dom, ok := got[b.Key]
		if !ok {
			t.Errorf("leaf %s (%s) missing from HTML", b.Key, b.Kind)
			continue
		}
		if dom != b.Text {
			t.Errorf("leaf %s (%s):\n DOM  %q\n runs %q", b.Key, b.Kind, dom, b.Text)
		}
		r := 0
		for _, run := range b.Runs {
			if run.R != r {
				t.Errorf("leaf %s: run starts at %d, expected %d", b.Key, run.R, r)
			}
			r += run.RLen
			if run.Literal && d.Source[run.S:run.E] != run.Text {
				t.Errorf("leaf %s: literal run %q but source has %q", b.Key, run.Text, d.Source[run.S:run.E])
			}
			if run.S > run.E {
				t.Errorf("leaf %s: run source range reversed", b.Key)
			}
		}
		if r != b.RLen || r != utf16Len(b.Text) {
			t.Errorf("leaf %s: runs cover %d of %d", b.Key, r, b.RLen)
		}
	}
}

func TestMarkdownLeavesMatchTheDOM(t *testing.T) {
	for _, opt := range []MarkdownOptions{{}, {HardWraps: true}, {Typographer: true}, {UnsafeHTML: true}} {
		d := Markdown(corpus, opt)
		checkDoc(t, d)
	}
}

func TestSelectionRoundTrip(t *testing.T) {
	d := Markdown(corpus, MarkdownOptions{})
	cases := []string{"emphasis", "Bold & centred", "code span", "task open", "The footnote text", "Careful with", "fmt.Println", "a [link]"}
	for _, want := range cases {
		found := false
		for _, b := range d.Leaves() {
			i := strings.Index(b.Text, want)
			if i < 0 {
				continue
			}
			found = true
			rs := utf16Len(b.Text[:i])
			re := rs + utf16Len(want)
			s, e, ok := d.SourceFromSelection(b.Key, rs, b.Key, re)
			if !ok {
				t.Fatalf("%q: selection not mapped", want)
			}
			segs := d.Segments(s, e)
			if len(segs) != 1 || segs[0].Text != want {
				t.Errorf("%q: source %q maps back to %+v", want, d.Source[s:e], segs)
			}
		}
		if !found && want != "a [link]" {
			t.Errorf("%q not found in any leaf", want)
		}
	}
}

func TestSelectionAcrossBlocks(t *testing.T) {
	d := Markdown("First paragraph here.\n\nSecond paragraph here.\n", MarkdownOptions{})
	ls := d.Leaves()
	s, e, ok := d.SourceFromSelection(ls[0].Key, 6, ls[1].Key, 6)
	if !ok || d.Source[s:e] != "paragraph here.\n\nSecond" {
		t.Fatalf("got %q", d.Source[s:e])
	}
	segs := d.Segments(s, e)
	if len(segs) != 2 || segs[0].Text != "paragraph here." || segs[1].Text != "Second" {
		t.Fatalf("segments %+v", segs)
	}
}

func TestEscapesMapAsAWhole(t *testing.T) {
	d := Markdown("Use \\*stars\\* &amp; more.\n", MarkdownOptions{})
	b := d.Leaves()[0]
	if b.Text != "Use *stars* & more." {
		t.Fatalf("text %q", b.Text)
	}
	i := strings.Index(b.Text, "&")
	s, e, _ := d.SourceFromSelection(b.Key, i, b.Key, i+1)
	if d.Source[s:e] != "&amp;" {
		t.Fatalf("entity maps to %q", d.Source[s:e])
	}
}

func TestHeadingIDsAreGitHubStyleAndUnique(t *testing.T) {
	d := Markdown("# Hello, World!\n\n## Hello, World!\n\n## Ünïcode `code` test\n", MarkdownOptions{})
	ids := []string{d.Headings[0].ID, d.Headings[1].ID, d.Headings[2].ID}
	want := []string{"hello-world", "hello-world-1", "ünïcode-code-test"}
	for i := range ids {
		if ids[i] != want[i] {
			t.Errorf("id %d = %q, want %q", i, ids[i], want[i])
		}
	}
	if d.Headings[1].Path != "Hello, World! › Hello, World!" {
		t.Errorf("path %q", d.Headings[1].Path)
	}
}

func TestSanitizing(t *testing.T) {
	d := Markdown("<div><script>alert(1)</script><a href=\"javascript:x\" onclick=\"y\">l</a><img src=\"x.png\" onerror=\"z\"></div>\n\n[x](javascript:alert(1))\n", MarkdownOptions{})
	for _, bad := range []string{"<script", "alert(1)</", "javascript:", "onclick", "onerror"} {
		if strings.Contains(d.HTML, bad) {
			t.Errorf("rendered HTML still contains %q:\n%s", bad, d.HTML)
		}
	}
	u := Markdown("<div><script>ok()</script></div>\n", MarkdownOptions{UnsafeHTML: true})
	if !strings.Contains(u.HTML, "<script>ok()</script>") {
		t.Errorf("unsafe mode filtered the HTML: %s", u.HTML)
	}
}

func TestNoHardWrapsByDefault(t *testing.T) {
	d := Markdown("one\ntwo\n", MarkdownOptions{})
	if strings.Contains(d.HTML, "<br") {
		t.Fatalf("soft break rendered as <br>: %s", d.HTML)
	}
	h := Markdown("one\ntwo\n", MarkdownOptions{HardWraps: true})
	if !strings.Contains(h.HTML, "<br") {
		t.Fatalf("hard wraps option ignored")
	}
}

func TestAlerts(t *testing.T) {
	d := Markdown("> [!NOTE]\n> Useful information.\n", MarkdownOptions{})
	if !strings.Contains(d.HTML, `markdown-alert-note`) || strings.Contains(d.HTML, "[!NOTE]") {
		t.Fatalf("alert not rendered: %s", d.HTML)
	}
	checkDoc(t, d)
}

func TestFrontmatterIsShownNotParsed(t *testing.T) {
	d := Markdown("---\ntitle: X\nmarp: true\n---\n\n# Body\n", MarkdownOptions{})
	if !d.IsMarp || len(d.Frontmatter) != 2 || !strings.Contains(d.HTML, "<details class=\"frontmatter\"") {
		t.Fatalf("frontmatter: %+v %s", d.Frontmatter, d.HTML)
	}
	if strings.Contains(d.HTML, "<hr") {
		t.Fatalf("frontmatter fences rendered as rules")
	}
	if d.Headings[0].Start != strings.Index(d.Source, "# Body") {
		t.Fatalf("heading offset %d not in file coordinates", d.Headings[0].Start)
	}
}

func TestKeysAreStableAcrossUnrelatedEdits(t *testing.T) {
	a := Markdown("# T\n\nAlpha.\n\nBeta.\n", MarkdownOptions{})
	b := Markdown("# T\n\nInserted.\n\nAlpha.\n\nBeta.\n", MarkdownOptions{})
	keyOf := func(d *Doc, text string) string {
		for _, l := range d.Leaves() {
			if l.Text == text {
				return l.Key
			}
		}
		return ""
	}
	if keyOf(a, "Beta.") == "" || keyOf(a, "Beta.") != keyOf(b, "Beta.") {
		t.Fatalf("key changed for an untouched block")
	}
}

func TestCodeFileLines(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tprintln(\"<hi>\")\n}\n"
	d := Code(src, "main.go")
	if d.Kind != KindCode || len(d.Leaves()) != 6 {
		t.Fatalf("kind %s, %d lines", d.Kind, len(d.Leaves()))
	}
	checkDoc(t, d)
	l := d.Leaves()[3]
	s, e, _ := d.SourceFromSelection(l.Key, 1, l.Key, 8)
	if d.Source[s:e] != "println" {
		t.Fatalf("got %q", d.Source[s:e])
	}
	plain := Code("just text\nmore\n", "notes.unknownext")
	checkDoc(t, plain)
}

func TestHTMLFileBodyText(t *testing.T) {
	src := "<!doctype html><html><head><title>T</title><style>p{}</style></head>\n<body>\n  <h1>Hello &amp; welcome</h1>\n  <p>Para <b>bold</b>.</p>\n<script>var x = 1;</script>\n</body></html>"
	d := HTMLFile(src)
	b := d.Leaves()[0]
	if b.Text != "Hello & welcomePara bold." {
		t.Fatalf("body text %q", b.Text)
	}
	i := strings.Index(b.Text, "bold")
	s, e, _ := d.SourceFromSelection(BodyKey, i, BodyKey, i+4)
	if d.Source[s:e] != "bold" {
		t.Fatalf("got %q", d.Source[s:e])
	}
	if d.Title != "T" {
		t.Fatalf("title %q", d.Title)
	}
}

func TestChromaCSSHasBothThemes(t *testing.T) {
	css := ChromaCSS()
	if !strings.Contains(css, `:root[data-theme="dark"] .chroma`) || !strings.Contains(css, "prefers-color-scheme: dark") {
		t.Fatalf("missing dark theme rules")
	}
}

func TestRefsListsWhatThePageLoads(t *testing.T) {
	page := `<p><img src="img/a.png" alt=""> <a href="other.md">link</a></p>
<picture><source srcset="b.webp 1x, c.webp 2x"><img src="img/a.png"></picture>
<video src="v.mp4" poster="p.jpg"></video>
<link rel="stylesheet" href="style.css"><script src="app.js"></script>
<svg><image href="d.svg"/></svg>`
	got := strings.Join(Refs(page), " ")
	want := "img/a.png b.webp c.webp v.mp4 p.jpg style.css app.js d.svg"
	if got != want {
		t.Fatalf("Refs = %q, want %q", got, want)
	}
	if d := Markdown("![chart](img/chart.png)\n", MarkdownOptions{}); strings.Join(d.Refs, " ") != "img/chart.png" {
		t.Fatalf("markdown Refs = %q", d.Refs)
	}
}
