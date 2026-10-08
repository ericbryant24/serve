package render

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"strconv"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"serve/internal/anchor"
)

// MarkdownOptions are the rendering choices config can change. The defaults
// (all false) match how GitHub renders a .md file.
type MarkdownOptions struct {
	HardWraps   bool // a single newline becomes <br>
	Typographer bool // curly quotes and dashes
	UnsafeHTML  bool // pass raw HTML through unfiltered
	// NoBlockAttrs leaves out the data-b markers, for HTML that is not a
	// document (a comment's text).
	NoBlockAttrs bool
}

// mdState is one render's bookkeeping, shared by the node renderers.
type mdState struct {
	src       []byte
	opt       MarkdownOptions
	blocks    []Block
	nodes     map[ast.Node]int
	headingID map[ast.Node]string
	alerts    map[ast.Node]string
	leaf      *leafBuilder
	leafIdx   int
	mermaid   bool
}

// Markdown renders a markdown document.
func Markdown(source string, opt MarkdownOptions) *Doc {
	src := []byte(source)
	fm, fmEnd := parseFrontmatter(source)
	parse := src
	if fmEnd > 0 {
		// Blank the frontmatter out byte for byte, so every offset the parser
		// reports is an offset in the real file.
		parse = append([]byte(nil), src...)
		for i := 0; i < fmEnd; i++ {
			if parse[i] != '\n' {
				parse[i] = ' '
			}
		}
	}

	exts := []goldmark.Extender{extension.Table, extension.Strikethrough, extension.Linkify, extension.TaskList, extension.Footnote}
	if opt.Typographer {
		exts = append(exts, extension.Typographer)
	}
	md := goldmark.New(goldmark.WithExtensions(exts...))
	root := md.Parser().Parse(text.NewReader(parse))

	st := &mdState{
		src: parse, opt: opt,
		nodes: map[ast.Node]int{}, headingID: map[ast.Node]string{}, alerts: map[ast.Node]string{},
	}
	transformAlerts(root, parse, st.alerts)
	doc := &Doc{Kind: KindMarkdown, Source: source, Rev: anchor.Hash(source), Frontmatter: fm}
	keys := keyer{}
	if fmEnd > 0 {
		st.blocks = append(st.blocks, Block{Key: keys.key("frontmatter", source[:fmEnd]), Kind: "frontmatter", Start: 0, End: fmEnd, Parent: -1})
		doc.IsMarp = isMarp(fm)
	}
	assignBlocks(root, parse, st, keys)
	doc.Headings = collectHeadings(root, parse, st)
	for _, h := range doc.Headings {
		if h.Level == 1 && doc.Title == "" {
			doc.Title = h.Text
		}
	}

	r := renderer.NewRenderer(renderer.WithNodeRenderers(
		util.Prioritized(html.NewRenderer(html.WithXHTML()), 1000),
		util.Prioritized(extension.NewTableHTMLRenderer(), 500),
		util.Prioritized(extension.NewStrikethroughHTMLRenderer(), 500),
		util.Prioritized(extension.NewFootnoteHTMLRenderer(), 500),
		util.Prioritized(&mdRenderer{st}, 1),
	))
	var all strings.Builder
	if fmEnd > 0 {
		h := frontmatterHTML(fm, st.blocks[0].Key)
		doc.Tops = append(doc.Tops, Top{Key: shortHash(h), Src: shortHash(source[:fmEnd]), HTML: h})
		all.WriteString(h)
	}
	for c := root.FirstChild(); c != nil; c = c.NextSibling() {
		var buf bytes.Buffer
		_ = r.Render(&buf, parse, c)
		h := buf.String()
		s, e := nodeSpan(c, parse)
		doc.Tops = append(doc.Tops, Top{Key: shortHash(h), Src: shortHash(source[s:e]), HTML: h})
		all.WriteString(h)
	}
	doc.HTML = all.String()
	doc.Refs = Refs(doc.HTML)
	doc.Blocks = st.blocks
	doc.HasMermaid = st.mermaid
	doc.index()
	doc.legacy = legacyBlocks(root, parse, doc, st)
	return doc
}

// --- pre-pass: which nodes become blocks, and their source ranges ----------

func blockKind(n ast.Node) (string, bool) {
	switch n.Kind() {
	case ast.KindParagraph:
		return "p", true
	case ast.KindHeading:
		return "h" + strconv.Itoa(n.(*ast.Heading).Level), true
	case ast.KindTextBlock:
		if _, ok := n.Parent().(*ast.ListItem); ok {
			return "tb", true
		}
	case ast.KindBlockquote:
		return "blockquote", true
	case ast.KindList:
		if n.(*ast.List).IsOrdered() {
			return "ol", true
		}
		return "ul", true
	case ast.KindListItem:
		return "li", true
	case ast.KindCodeBlock, ast.KindFencedCodeBlock:
		return "pre", true
	case ast.KindHTMLBlock:
		return "html", true
	case ast.KindThematicBreak:
		return "hr", true
	case extast.KindTable:
		return "table", true
	case extast.KindTableHeader, extast.KindTableRow:
		return "tr", true
	case extast.KindTableCell:
		if _, ok := n.Parent().(*extast.TableHeader); ok {
			return "th", true
		}
		return "td", true
	}
	return "", false
}

func isLeafKind(kind string) bool {
	switch kind {
	case "p", "tb", "td", "th", "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func assignBlocks(root ast.Node, src []byte, st *mdState, keys keyer) {
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		kind, ok := blockKind(n)
		if !ok {
			return ast.WalkContinue, nil
		}
		s, e := nodeSpan(n, src)
		if e <= s && kind != "hr" {
			return ast.WalkContinue, nil
		}
		leaf := isLeafKind(kind)
		switch n.Kind() {
		case ast.KindFencedCodeBlock:
			fc := n.(*ast.FencedCodeBlock)
			if string(fc.Language(src)) == "mermaid" {
				kind = "mermaid"
			} else {
				leaf = codeIsContiguous(n)
			}
		case ast.KindCodeBlock:
			leaf = codeIsContiguous(n)
		case ast.KindHTMLBlock:
			hs, he, contiguous := htmlBlockRange(n.(*ast.HTMLBlock))
			if !contiguous || !balancedHTML(src[hs:he]) {
				return ast.WalkContinue, nil
			}
			leaf = true
		}
		if a, ok := st.alerts[n]; ok {
			kind = "alert-" + a
		}
		st.nodes[n] = len(st.blocks)
		st.blocks = append(st.blocks, Block{Key: keys.key(kind, string(src[s:e])), Kind: kind, Start: s, End: e, Leaf: leaf, Parent: -1})
		return ast.WalkContinue, nil
	})
}

// nodeSpan is a block's source range: whole lines, except table cells, which
// share their line with their neighbours.
func nodeSpan(n ast.Node, src []byte) (int, int) {
	s, e := rawSpan(n, src)
	if e < s {
		return s, s
	}
	if n.Kind() == extast.KindTableCell {
		return s, e
	}
	switch n.Kind() {
	case ast.KindFencedCodeBlock:
		fc := n.(*ast.FencedCodeBlock)
		if fc.Info != nil {
			s = fc.Info.Segment.Start
		} else if s > 0 {
			s = lineStart(src, s-1)
		}
		// Take in the closing fence when there is one.
		if e < len(src) {
			ns := e
			ne := lineEnd(src, ns)
			if t := bytes.TrimSpace(src[ns:ne]); bytes.HasPrefix(t, []byte("```")) || bytes.HasPrefix(t, []byte("~~~")) {
				e = ne
			}
		}
	}
	s = lineStart(src, s)
	e = lineEnd(src, max(s, e-1))
	return s, e
}

// rawSpan is the source a node's own lines or its children cover.
func rawSpan(n ast.Node, src []byte) (int, int) {
	s, e := -1, -1
	grow := func(a, b int) {
		if a < 0 || b < a {
			return
		}
		if s < 0 || a < s {
			s = a
		}
		if b > e {
			e = b
		}
	}
	if n.Type() == ast.TypeBlock {
		if l := n.Lines(); l != nil && l.Len() > 0 {
			grow(l.At(0).Start, l.At(l.Len()-1).Stop)
		}
		if hb, ok := n.(*ast.HTMLBlock); ok && hb.HasClosure() {
			grow(hb.ClosureLine.Start, hb.ClosureLine.Stop)
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == ast.TypeInline {
			if t, ok := c.(*ast.Text); ok {
				grow(t.Segment.Start, t.Segment.Stop)
				continue
			}
		}
		cs, ce := rawSpan(c, src)
		grow(cs, ce)
	}
	if s < 0 {
		return 0, 0
	}
	return s, e
}

func lineStart(src []byte, i int) int {
	i = min(max(i, 0), len(src))
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(src []byte, i int) int {
	i = min(max(i, 0), len(src))
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

// codeIsContiguous reports whether a code block's lines are one unbroken run
// of source with no padding, which is what lets its text map back exactly.
func codeIsContiguous(n ast.Node) bool {
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		if seg.Padding > 0 {
			return false
		}
		if i > 0 && seg.Start != l.At(i-1).Stop {
			return false
		}
	}
	return true
}

func htmlBlockRange(n *ast.HTMLBlock) (int, int, bool) {
	l := n.Lines()
	if l.Len() == 0 {
		return 0, 0, false
	}
	s, e := l.At(0).Start, l.At(l.Len()-1).Stop
	for i := 1; i < l.Len(); i++ {
		if l.At(i).Start != l.At(i-1).Stop {
			return 0, 0, false
		}
	}
	if n.HasClosure() {
		if n.ClosureLine.Start != e {
			return 0, 0, false
		}
		e = n.ClosureLine.Stop
	}
	return s, e, true
}

// --- headings -------------------------------------------------------------

func collectHeadings(root ast.Node, src []byte, st *mdState) []Heading {
	var out []Heading
	seen := map[string]int{}
	var stack []Heading
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		title := strings.TrimSpace(inlineText(h, src))
		slug := githubSlug(title)
		if c := seen[slug]; c > 0 {
			seen[slug] = c + 1
			slug = fmt.Sprintf("%s-%d", slug, c)
		} else {
			seen[slug] = 1
		}
		st.headingID[h] = slug
		for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		names := make([]string, 0, len(stack)+1)
		for _, p := range stack {
			names = append(names, p.Text)
		}
		names = append(names, title)
		s, _ := nodeSpan(h, src)
		hd := Heading{Level: h.Level, Text: title, ID: slug, Start: s, Path: strings.Join(names, " › ")}
		stack = append(stack, hd)
		out = append(out, hd)
		return ast.WalkSkipChildren, nil
	})
	return out
}

// githubSlug makes a heading id the way GitHub does: lower case, letters,
// numbers, hyphens and underscores kept, spaces to hyphens, the rest dropped.
func githubSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// inlineText is the rendered text of a node's inline content.
func inlineText(n ast.Node, src []byte) string {
	lb := &leafBuilder{}
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || c == n {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			writeText(src, t.Segment.Start, t.Segment.Stop, lb)
			if t.SoftLineBreak() || t.HardLineBreak() {
				lb.unmapped(" ")
			}
		case *ast.String:
			lb.unmapped(string(t.Value))
		case *ast.CodeSpan:
			for cc := t.FirstChild(); cc != nil; cc = cc.NextSibling() {
				if tt, ok := cc.(*ast.Text); ok {
					lb.unmapped(string(tt.Segment.Value(src)))
				}
			}
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			lb.unmapped(string(t.Label(src)))
		case *ast.Image:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return lb.text.String()
}

// --- GitHub alerts ----------------------------------------------------------

var alertTypes = map[string]string{"NOTE": "note", "TIP": "tip", "IMPORTANT": "important", "WARNING": "warning", "CAUTION": "caution"}

// transformAlerts turns a blockquote whose first line is [!NOTE] (or TIP,
// IMPORTANT, WARNING, CAUTION) into an alert, removing the marker.
func transformAlerts(root ast.Node, src []byte, alerts map[ast.Node]string) {
	var quotes []*ast.Blockquote
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if bq, ok := n.(*ast.Blockquote); ok && entering {
			quotes = append(quotes, bq)
		}
		return ast.WalkContinue, nil
	})
	for _, bq := range quotes {
		p, ok := bq.FirstChild().(*ast.Paragraph)
		if !ok || p.Lines().Len() == 0 {
			continue
		}
		first := p.Lines().At(0)
		marker := strings.TrimSpace(string(first.Value(src)))
		if !strings.HasPrefix(marker, "[!") || !strings.HasSuffix(marker, "]") {
			continue
		}
		typ, ok := alertTypes[strings.ToUpper(marker[2:len(marker)-1])]
		if !ok {
			continue
		}
		var drop []ast.Node
		for c := p.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok && t.Segment.Start < first.Stop {
				drop = append(drop, c)
				continue
			}
			if c.Type() == ast.TypeInline && c.Kind() != ast.KindText {
				// Inline nodes parsed out of the marker ("[" "!NOTE" "]").
				if s, _ := rawSpan(c, src); s < first.Stop && s >= first.Start {
					drop = append(drop, c)
					continue
				}
			}
			break
		}
		for _, d := range drop {
			p.RemoveChild(p, d)
		}
		if p.FirstChild() == nil {
			bq.RemoveChild(bq, p)
		}
		alerts[bq] = typ
	}
}

var alertTitles = map[string]string{"note": "Note", "tip": "Tip", "important": "Important", "warning": "Warning", "caution": "Caution"}

// --- node renderers ---------------------------------------------------------

type mdRenderer struct{ st *mdState }

func (r *mdRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindDocument, func(util.BufWriter, []byte, ast.Node, bool) (ast.WalkStatus, error) {
		return ast.WalkContinue, nil
	})
	reg.Register(ast.KindHeading, r.heading)
	reg.Register(ast.KindBlockquote, r.blockquote)
	reg.Register(ast.KindCodeBlock, r.codeBlock)
	reg.Register(ast.KindFencedCodeBlock, r.codeBlock)
	reg.Register(ast.KindHTMLBlock, r.htmlBlock)
	reg.Register(ast.KindList, r.list)
	reg.Register(ast.KindListItem, r.listItem)
	reg.Register(ast.KindParagraph, r.paragraph)
	reg.Register(ast.KindTextBlock, r.textBlock)
	reg.Register(ast.KindThematicBreak, r.thematicBreak)
	reg.Register(ast.KindText, r.text)
	reg.Register(ast.KindString, r.str)
	reg.Register(ast.KindCodeSpan, r.codeSpan)
	reg.Register(ast.KindAutoLink, r.autoLink)
	reg.Register(ast.KindRawHTML, r.rawHTML)
	reg.Register(extast.KindTable, r.table)
	reg.Register(extast.KindTableHeader, r.tableHeader)
	reg.Register(extast.KindTableRow, r.tableRow)
	reg.Register(extast.KindTableCell, r.tableCell)
	reg.Register(extast.KindTaskCheckBox, r.taskCheckBox)
	reg.Register(extast.KindFootnoteLink, r.footnoteLink)
	reg.Register(extast.KindFootnoteBacklink, r.footnoteBacklink)
}

// attrs writes a block's data-b (and data-t for a leaf) attributes.
func (r *mdRenderer) attrs(w util.BufWriter, n ast.Node) {
	i, ok := r.st.nodes[n]
	if !ok || r.st.opt.NoBlockAttrs {
		return
	}
	b := r.st.blocks[i]
	_, _ = w.WriteString(` data-b="` + b.Key + `"`)
	if b.Leaf {
		_, _ = w.WriteString(` data-t=""`)
	}
}

func (r *mdRenderer) openLeaf(n ast.Node) {
	if i, ok := r.st.nodes[n]; ok && r.st.blocks[i].Leaf {
		r.st.leaf = &leafBuilder{lastE: r.st.blocks[i].Start}
		r.st.leafIdx = i
	}
}

func (r *mdRenderer) closeLeaf(n ast.Node) {
	if i, ok := r.st.nodes[n]; ok && r.st.leaf != nil && r.st.leafIdx == i {
		r.st.leaf.finish(&r.st.blocks[i])
		r.st.leaf = nil
	}
}

func (r *mdRenderer) heading(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	if entering {
		fmt.Fprintf(w, "<h%d", n.Level)
		if id := r.st.headingID[n]; id != "" {
			_, _ = w.WriteString(` id="` + stdhtml.EscapeString(id) + `"`)
		}
		r.attrs(w, n)
		_ = w.WriteByte('>')
		r.openLeaf(n)
	} else {
		r.closeLeaf(n)
		fmt.Fprintf(w, "</h%d>\n", n.Level)
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) paragraph(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<p")
		r.attrs(w, n)
		_ = w.WriteByte('>')
		r.openLeaf(n)
	} else {
		r.closeLeaf(n)
		_, _ = w.WriteString("</p>\n")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) textBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	_, inItem := n.Parent().(*ast.ListItem)
	if entering {
		if inItem {
			_, _ = w.WriteString(`<span class="tb"`)
			r.attrs(w, n)
			_ = w.WriteByte('>')
			r.openLeaf(n)
		}
	} else {
		if inItem {
			r.closeLeaf(n)
			_, _ = w.WriteString("</span>")
		}
		if n.NextSibling() != nil {
			_ = w.WriteByte('\n')
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) blockquote(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if typ, ok := r.st.alerts[n]; ok {
		if entering {
			_, _ = w.WriteString(`<div class="markdown-alert markdown-alert-` + typ + `"`)
			r.attrs(w, n)
			_, _ = w.WriteString(`><p class="markdown-alert-title">` + alertTitles[typ] + "</p>\n")
		} else {
			_, _ = w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	}
	if entering {
		_, _ = w.WriteString("<blockquote")
		r.attrs(w, n)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</blockquote>\n")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) list(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.List)
	tag := "ul"
	if n.IsOrdered() {
		tag = "ol"
	}
	if entering {
		_, _ = w.WriteString("<" + tag)
		if n.IsOrdered() && n.Start != 1 {
			fmt.Fprintf(w, ` start="%d"`, n.Start)
		}
		if hasTaskItems(n) {
			_, _ = w.WriteString(` class="contains-task-list"`)
		}
		r.attrs(w, n)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</" + tag + ">\n")
	}
	return ast.WalkContinue, nil
}

func hasTaskItems(l *ast.List) bool {
	for li := l.FirstChild(); li != nil; li = li.NextSibling() {
		if fc := li.FirstChild(); fc != nil {
			if ic := fc.FirstChild(); ic != nil && ic.Kind() == extast.KindTaskCheckBox {
				return true
			}
		}
	}
	return false
}

func (r *mdRenderer) listItem(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<li")
		if fc := n.FirstChild(); fc != nil {
			if ic := fc.FirstChild(); ic != nil && ic.Kind() == extast.KindTaskCheckBox {
				_, _ = w.WriteString(` class="task-list-item"`)
			}
		}
		r.attrs(w, n)
		_ = w.WriteByte('>')
		if fc := n.FirstChild(); fc != nil {
			if _, ok := fc.(*ast.TextBlock); !ok {
				_ = w.WriteByte('\n')
			}
		}
	} else {
		_, _ = w.WriteString("</li>\n")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) thematicBreak(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<hr")
		r.attrs(w, n)
		_, _ = w.WriteString(" />\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) codeBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	lang := ""
	if fc, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(fc.Language(src))
	}
	lines := n.Lines()
	var code strings.Builder
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		code.Write(seg.Value(src))
	}
	if lang == "mermaid" {
		r.st.mermaid = true
		_, _ = w.WriteString(`<pre class="mermaid"`)
		r.attrs(w, n)
		_, _ = w.WriteString(">" + stdhtml.EscapeString(code.String()) + "</pre>\n")
		return ast.WalkSkipChildren, nil
	}
	_, _ = w.WriteString(`<div class="highlight"><pre class="chroma"`)
	if lang != "" {
		_, _ = w.WriteString(` data-lang="` + stdhtml.EscapeString(lang) + `"`)
	}
	r.attrs(w, n)
	_, _ = w.WriteString("><code>")
	r.openLeaf(n)
	if r.st.leaf != nil {
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			r.st.leaf.literal(seg.Start, seg.Stop, string(src[seg.Start:seg.Stop]))
		}
	}
	_, _ = w.WriteString(highlight(code.String(), lexerFor(lang, "")))
	r.closeLeaf(n)
	_, _ = w.WriteString("</code></pre></div>\n")
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) htmlBlock(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.HTMLBlock)
	if i, ok := r.st.nodes[n]; ok {
		s, e, _ := htmlBlockRange(n)
		_, _ = w.WriteString(`<div class="html-block"`)
		r.attrs(w, n)
		_ = w.WriteByte('>')
		r.openLeaf(n)
		_, _ = w.WriteString(htmlFragment(src, s, e, r.st.leaf, r.st.opt.UnsafeHTML))
		if r.st.leaf != nil {
			r.st.leaf.finish(&r.st.blocks[i])
			r.st.leaf = nil
		}
		_, _ = w.WriteString("</div>\n")
		return ast.WalkSkipChildren, nil
	}
	var raw bytes.Buffer
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		raw.Write(seg.Value(src))
	}
	if n.HasClosure() {
		raw.Write(n.ClosureLine.Value(src))
	}
	b := raw.Bytes()
	_, _ = w.WriteString(htmlFragment(b, 0, len(b), nil, r.st.opt.UnsafeHTML))
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) text(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Text)
	seg := n.Segment
	if n.IsRaw() {
		v := string(seg.Value(src))
		if r.st.leaf != nil {
			if seg.Padding == 0 {
				r.st.leaf.literal(seg.Start, seg.Stop, v)
			} else {
				r.st.leaf.atomic(seg.Start, seg.Stop, v)
			}
		}
		_, _ = w.WriteString(stdhtml.EscapeString(v))
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(writeText(src, seg.Start, seg.Stop, r.st.leaf))
	if n.HardLineBreak() || (n.SoftLineBreak() && r.st.opt.HardWraps) {
		_, _ = w.WriteString("<br />\n")
		if r.st.leaf != nil {
			r.st.leaf.unmapped("\n")
		}
	} else if n.SoftLineBreak() {
		_ = w.WriteByte('\n')
		if r.st.leaf != nil {
			r.st.leaf.unmapped("\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) str(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.String)
	v := string(n.Value)
	if r.st.leaf != nil {
		r.st.leaf.unmapped(v)
	}
	_, _ = w.WriteString(stdhtml.EscapeString(v))
	return ast.WalkContinue, nil
}

func (r *mdRenderer) codeSpan(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code>")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString("<code>")
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			v := t.Segment.Value(src)
			s, e := t.Segment.Start, t.Segment.Stop
			if bytes.HasSuffix(v, []byte("\n")) {
				body := string(v[:len(v)-1])
				if r.st.leaf != nil {
					r.st.leaf.literal(s, e-1, body)
					r.st.leaf.atomic(e-1, e, " ")
				}
				_, _ = w.WriteString(stdhtml.EscapeString(body) + " ")
			} else {
				if r.st.leaf != nil {
					r.st.leaf.literal(s, e, string(v))
				}
				_, _ = w.WriteString(stdhtml.EscapeString(string(v)))
			}
		case *ast.String:
			if r.st.leaf != nil {
				r.st.leaf.unmapped(string(t.Value))
			}
			_, _ = w.WriteString(stdhtml.EscapeString(string(t.Value)))
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) autoLink(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.AutoLink)
	url := string(util.URLEscape(n.URL(src), false))
	label := string(n.Label(src))
	if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(url), "mailto:") {
		url = "mailto:" + url
	}
	if html.IsDangerousURL([]byte(url)) {
		url = ""
	}
	_, _ = w.WriteString(`<a href="` + stdhtml.EscapeString(url) + `">` + stdhtml.EscapeString(label) + "</a>")
	if lb := r.st.leaf; lb != nil {
		blk := r.st.blocks[r.st.leafIdx]
		from := max(lb.lastE, blk.Start)
		if i := bytes.Index(src[from:blk.End], []byte(label)); i >= 0 && label != "" {
			lb.literal(from+i, from+i+len(label), label)
		} else {
			lb.unmapped(label)
		}
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) rawHTML(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.RawHTML)
	for i := 0; i < n.Segments.Len(); i++ {
		seg := n.Segments.At(i)
		_, _ = w.WriteString(htmlFragment(src, seg.Start, seg.Stop, nil, r.st.opt.UnsafeHTML))
	}
	return ast.WalkSkipChildren, nil
}

func (r *mdRenderer) table(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<table")
		r.attrs(w, n)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</table>\n")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) tableHeader(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<thead>\n<tr")
		r.attrs(w, n)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</tr>\n</thead>\n")
		if n.NextSibling() != nil {
			_, _ = w.WriteString("<tbody>\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) tableRow(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if n.PreviousSibling() == nil {
			_, _ = w.WriteString("<tbody>\n")
		}
		_, _ = w.WriteString("<tr")
		r.attrs(w, n)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</tr>\n")
		if n.NextSibling() == nil {
			_, _ = w.WriteString("</tbody>\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) tableCell(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*extast.TableCell)
	tag := "td"
	if _, ok := n.Parent().(*extast.TableHeader); ok {
		tag = "th"
	}
	if entering {
		_, _ = w.WriteString("<" + tag)
		if n.Alignment != extast.AlignNone {
			_, _ = w.WriteString(` align="` + n.Alignment.String() + `"`)
		}
		r.attrs(w, n)
		_ = w.WriteByte('>')
		r.openLeaf(n)
	} else {
		r.closeLeaf(n)
		_, _ = w.WriteString("</" + tag + ">\n")
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) taskCheckBox(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if node.(*extast.TaskCheckBox).IsChecked {
			_, _ = w.WriteString(`<input type="checkbox" class="task-list-item-checkbox" checked="" disabled="" />`)
		} else {
			_, _ = w.WriteString(`<input type="checkbox" class="task-list-item-checkbox" disabled="" />`)
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) footnoteLink(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*extast.FootnoteLink)
		is := strconv.Itoa(n.Index)
		ref := ""
		if n.RefIndex > 0 {
			ref = strconv.Itoa(n.RefIndex)
		}
		fmt.Fprintf(w, `<sup id="fnref%s:%s"><a href="#fn:%s" class="footnote-ref" role="doc-noteref">%s</a></sup>`, ref, is, is, is)
		if r.st.leaf != nil {
			r.st.leaf.unmapped(is)
		}
	}
	return ast.WalkContinue, nil
}

func (r *mdRenderer) footnoteBacklink(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*extast.FootnoteBacklink)
		is := strconv.Itoa(n.Index)
		ref := ""
		if n.RefIndex > 0 {
			ref = strconv.Itoa(n.RefIndex)
		}
		fmt.Fprintf(w, `&#160;<a href="#fnref%s:%s" class="footnote-backref" role="doc-backlink">&#x21a9;&#xfe0e;</a>`, ref, is)
		if r.st.leaf != nil {
			r.st.leaf.unmapped(" ↩︎")
		}
	}
	return ast.WalkContinue, nil
}

// --- code highlighting ------------------------------------------------------

func lexerFor(lang, filename string) chroma.Lexer {
	var l chroma.Lexer
	if lang != "" {
		l = lexers.Get(lang)
	}
	if l == nil && filename != "" {
		l = lexers.Match(filename)
	}
	if l == nil {
		return nil
	}
	return chroma.Coalesce(l)
}

// highlight renders code as spans with Chroma's class names. The text it
// produces is exactly code, so the leaf's runs stay valid; if the lexer
// would change the text, the code is written plain.
func highlight(code string, l chroma.Lexer) string {
	if l == nil || len(code) > 1<<20 {
		return stdhtml.EscapeString(code)
	}
	it, err := l.Tokenise(nil, code)
	if err != nil {
		return stdhtml.EscapeString(code)
	}
	var b strings.Builder
	got := 0
	for t := it(); t != chroma.EOF; t = it() {
		v := t.Value
		if got+len(v) > len(code) {
			v = v[:len(code)-got]
		}
		if v == "" {
			continue
		}
		if code[got:got+len(v)] != v {
			return stdhtml.EscapeString(code)
		}
		got += len(v)
		if cls := tokenClass(t.Type); cls != "" {
			b.WriteString(`<span class="` + cls + `">` + stdhtml.EscapeString(v) + "</span>")
		} else {
			b.WriteString(stdhtml.EscapeString(v))
		}
	}
	if got != len(code) {
		return stdhtml.EscapeString(code)
	}
	return b.String()
}

func tokenClass(tt chroma.TokenType) string {
	for t := tt; ; {
		if c, ok := chroma.StandardTypes[t]; ok {
			return c
		}
		switch {
		case t != t.SubCategory():
			t = t.SubCategory()
		case t != t.Category():
			t = t.Category()
		default:
			return ""
		}
	}
}
