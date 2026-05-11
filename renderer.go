package main

import (
	"bytes"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	goldmarkrenderer "github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ---------------------------------------------------------------------------
// Chroma formatter (shared)
// ---------------------------------------------------------------------------

var chromaFormatter = chromahtml.New(
	chromahtml.WithClasses(true),
	chromahtml.WithLineNumbers(false),
)

func generatePygmentsCSS() string {
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	var buf bytes.Buffer
	_ = chromaFormatter.WriteCSS(&buf, style)
	return buf.String()
}

// ---------------------------------------------------------------------------
// Frontmatter stripping
// ---------------------------------------------------------------------------

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n.*?\n---\s*\n`)

func stripFrontmatter(source string) string {
	loc := frontmatterRe.FindStringIndex(source)
	if loc == nil {
		return source
	}
	matched := source[:loc[1]]
	lines := strings.Count(matched, "\n")
	return strings.Repeat("\n", lines) + source[loc[1]:]
}

// ---------------------------------------------------------------------------
// data-source-lines AST transformer
// ---------------------------------------------------------------------------

// sourceLineAttrKey is the goldmark attribute key for data-source-lines.
var sourceLineAttrKey = []byte("data-source-lines")

type sourceLineTransformer struct{}

func (t *sourceLineTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if n.Type() != ast.TypeBlock {
			return ast.WalkContinue, nil
		}
		lines := n.Lines()
		if lines != nil && lines.Len() > 0 {
			// Leaf blocks: annotate on the way down (entering).
			if entering {
				first := lines.At(0)
				last := lines.At(lines.Len() - 1)
				startLine := lineNumber(src, first.Start) + 1 // 1-indexed
				endLine := lineNumber(src, last.Stop) + 1
				n.SetAttribute(sourceLineAttrKey, []byte(fmt.Sprintf("%d-%d", startLine, endLine)))
			}
		} else if !entering {
			// Container blocks (list, blockquote, table, …): annotate on the way
			// back up so that children are already annotated when we aggregate.
			setSourceLinesFromChildren(n, src)
		}
		return ast.WalkContinue, nil
	})
}

func setSourceLinesFromChildren(n ast.Node, src []byte) {
	first, last := -1, -1
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		v, ok := child.Attribute(sourceLineAttrKey)
		if !ok || v == nil {
			continue
		}
		val := string(v.([]byte))
		parts := strings.SplitN(val, "-", 2)
		if len(parts) != 2 {
			continue
		}
		var s, e int
		fmt.Sscanf(parts[0], "%d", &s)
		fmt.Sscanf(parts[1], "%d", &e)
		if first == -1 || s < first {
			first = s
		}
		if e > last {
			last = e
		}
	}
	if first != -1 {
		n.SetAttribute(sourceLineAttrKey, []byte(fmt.Sprintf("%d-%d", first, last)))
	}
}

// lineNumber returns the 0-indexed line number for a byte offset in src.
func lineNumber(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	return bytes.Count(src[:offset], []byte("\n"))
}

// ---------------------------------------------------------------------------
// Custom node renderer
// ---------------------------------------------------------------------------

// sourceLineRenderer wraps goldmark's default HTML renderer and injects
// data-source-lines attributes on all block-level elements.
type sourceLineRenderer struct {
	html.Config
}

func newSourceLineRenderer(opts ...html.Option) *sourceLineRenderer {
	r := &sourceLineRenderer{
		Config: html.NewConfig(),
	}
	for _, opt := range opts {
		opt.SetHTMLOption(&r.Config)
	}
	return r
}

func (r *sourceLineRenderer) RegisterFuncs(reg goldmarkrenderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindDocument, r.renderDocument)
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
	reg.Register(ast.KindCodeBlock, r.renderCodeBlock)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCodeBlock)
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)
	reg.Register(ast.KindList, r.renderList)
	reg.Register(ast.KindListItem, r.renderListItem)
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindTextBlock, r.renderTextBlock)
	reg.Register(ast.KindThematicBreak, r.renderThematicBreak)
	// GFM extension table nodes
	reg.Register(extast.KindTable, r.renderTable)
	reg.Register(extast.KindTableHeader, r.renderTableHeader)
	reg.Register(extast.KindTableRow, r.renderTableRow)
	reg.Register(extast.KindTableCell, r.renderTableCell)
}

func sourceAttr(n ast.Node) string {
	v, ok := n.Attribute(sourceLineAttrKey)
	if !ok || v == nil {
		return ""
	}
	b, ok := v.([]byte)
	if !ok {
		return ""
	}
	return string(b)
}

func writeSourceAttr(w util.BufWriter, n ast.Node) {
	if a := sourceAttr(n); a != "" {
		_, _ = fmt.Fprintf(w, ` data-source-lines="%s"`, a)
	}
}

func (r *sourceLineRenderer) renderDocument(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

// headingSlug generates a URL-safe id from heading text (lowercase, hyphens).
func headingSlug(node ast.Node, source []byte) string {
	var raw strings.Builder
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || n == node {
			return ast.WalkContinue, nil
		}
		if t, ok := n.(*ast.Text); ok {
			raw.Write(t.Segment.Value(source))
		} else if n.Kind() == ast.KindString {
			// ast.String nodes carry Value directly (used for escaped text)
			if s, ok2 := n.(*ast.String); ok2 {
				raw.Write(s.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	var id strings.Builder
	for _, r := range strings.ToLower(raw.String()) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			id.WriteRune(r)
		case r == ' ', r == '_':
			id.WriteRune('-')
		}
	}
	return strings.Trim(id.String(), "-")
}

func (r *sourceLineRenderer) renderHeading(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Heading)
	tag := fmt.Sprintf("h%d", n.Level)
	if entering {
		_, _ = fmt.Fprintf(w, "<%s", tag)
		writeSourceAttr(w, n)
		if id := headingSlug(n, source); id != "" {
			_, _ = fmt.Fprintf(w, ` id="%s"`, id)
		}
		_ = w.WriteByte('>')
	} else {
		_, _ = fmt.Fprintf(w, "</%s>\n", tag)
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderBlockquote(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<blockquote")
		writeSourceAttr(w, node)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</blockquote>\n")
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.CodeBlock)
		_, _ = w.WriteString("<pre")
		writeSourceAttr(w, n)
		_, _ = w.WriteString("><code>")
		lines := n.Lines()
		for i := 0; i < lines.Len(); i++ {
			line := lines.At(i)
			_, _ = w.Write(escapeHTML(source[line.Start:line.Stop]))
		}
		_, _ = w.WriteString("</code></pre>\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *sourceLineRenderer) renderFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(source))
	sa := sourceAttr(n)
	dataAttr := ""
	if sa != "" {
		dataAttr = fmt.Sprintf(` data-source-lines="%s"`, sa)
	}

	var content bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		content.Write(source[line.Start:line.Stop])
	}
	code := content.String()

	// Mermaid
	if lang == "mermaid" {
		_, _ = fmt.Fprintf(w, "<pre class=\"mermaid\"%s>%s</pre>\n", dataAttr, code)
		return ast.WalkSkipChildren, nil
	}

	// Syntax highlighting with chroma
	if lang != "" {
		lexer := lexers.Get(lang)
		if lexer == nil {
			lexer = lexers.Fallback
		}
		style := styles.Get("monokai")
		if style == nil {
			style = styles.Fallback
		}
		iterator, err := lexer.Tokenise(nil, code)
		if err == nil {
			var buf bytes.Buffer
			if err2 := chromaFormatter.Format(&buf, style, iterator); err2 == nil {
				// Replace the outer <div class="chroma"> with data-source-lines attr
				highlighted := buf.String()
				if dataAttr != "" {
					highlighted = strings.Replace(highlighted, `<div class="chroma">`, `<div class="chroma"`+dataAttr+`>`, 1)
				}
				_, _ = w.WriteString(highlighted)
				return ast.WalkSkipChildren, nil
			}
		}
	}

	// Fallback: plain pre/code
	_, _ = fmt.Fprintf(w, "<pre%s><code", dataAttr)
	if lang != "" {
		_, _ = fmt.Fprintf(w, ` class="language-%s"`, lang)
	}
	_, _ = fmt.Fprintf(w, ">%s</code></pre>\n", htmlEscapeString(code))
	return ast.WalkSkipChildren, nil
}

func (r *sourceLineRenderer) renderHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.HTMLBlock)
	sa := sourceAttr(n)

	var content bytes.Buffer
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		line := lines.At(i)
		content.Write(source[line.Start:line.Stop])
	}
	// Closing line
	if n.HasClosure() {
		closure := n.ClosureLine
		content.Write(source[closure.Start:closure.Stop])
	}

	if sa != "" {
		_, _ = fmt.Fprintf(w, "<div data-source-lines=\"%s\">%s</div>\n", sa, content.String())
	} else {
		_, _ = w.Write(content.Bytes())
	}
	return ast.WalkSkipChildren, nil
}

func (r *sourceLineRenderer) renderList(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.List)
	tag := "ul"
	if n.IsOrdered() {
		tag = "ol"
	}
	if entering {
		_, _ = fmt.Fprintf(w, "<%s", tag)
		writeSourceAttr(w, n)
		if n.IsOrdered() && n.Start != 1 {
			_, _ = fmt.Fprintf(w, ` start="%d"`, n.Start)
		}
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = fmt.Fprintf(w, "</%s>\n", tag)
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderListItem(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.ListItem)
	if entering {
		_, _ = w.WriteString("<li")
		writeSourceAttr(w, n)
		_ = w.WriteByte('>')
		// If first child is a TextBlock, unwrap it (tight list)
		if fc := n.FirstChild(); fc != nil {
			if _, ok := fc.(*ast.TextBlock); ok {
				return ast.WalkContinue, nil
			}
		}
		_ = w.WriteByte('\n')
	} else {
		_, _ = w.WriteString("</li>\n")
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderParagraph(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<p")
		writeSourceAttr(w, node)
		_ = w.WriteByte('>')
	} else {
		_, _ = w.WriteString("</p>\n")
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderTextBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	// TextBlock is used inside list items; render inline only
	if !entering {
		if node.NextSibling() != nil {
			_ = w.WriteByte('\n')
		}
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderThematicBreak(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<hr")
		writeSourceAttr(w, node)
		_, _ = w.WriteString(" />\n")
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<table")
		writeSourceAttr(w, node)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</table>\n")
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderTableHeader(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<thead")
		writeSourceAttr(w, node)
		_, _ = w.WriteString(">\n<tr>\n")
	} else {
		_, _ = w.WriteString("</tr>\n</thead>\n")
		if node.NextSibling() != nil {
			_, _ = w.WriteString("<tbody>\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderTableRow(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<tr")
		writeSourceAttr(w, node)
		_, _ = w.WriteString(">\n")
	} else {
		_, _ = w.WriteString("</tr>\n")
		if node.Parent().LastChild() == node {
			_, _ = w.WriteString("</tbody>\n")
		}
	}
	return ast.WalkContinue, nil
}

func (r *sourceLineRenderer) renderTableCell(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*extast.TableCell)
	tag := "td"
	if n.Parent().Kind() == extast.KindTableHeader {
		tag = "th"
	}
	if entering {
		_, _ = fmt.Fprintf(w, "<%s", tag)
		writeSourceAttr(w, n)
		if n.Alignment != extast.AlignNone {
			_, _ = fmt.Fprintf(w, ` style="text-align:%s"`, n.Alignment.String())
		}
		_ = w.WriteByte('>')
	} else {
		_, _ = fmt.Fprintf(w, "</%s>\n", tag)
	}
	return ast.WalkContinue, nil
}

// ---------------------------------------------------------------------------
// Extension table renderer
// ---------------------------------------------------------------------------

// We need to handle table nodes from the extension package.
// goldmark extension/table uses its own AST kinds.
// We register them via a separate renderer registered at lower priority
// so our source-line renderer has priority.

// ---------------------------------------------------------------------------
// HTML escape helpers
// ---------------------------------------------------------------------------

func escapeHTML(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("&"), []byte("&amp;"))
	b = bytes.ReplaceAll(b, []byte("<"), []byte("&lt;"))
	b = bytes.ReplaceAll(b, []byte(">"), []byte("&gt;"))
	b = bytes.ReplaceAll(b, []byte(`"`), []byte("&quot;"))
	return b
}

func htmlEscapeString(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// ---------------------------------------------------------------------------
// Goldmark instance
// ---------------------------------------------------------------------------

func buildGoldmark() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Typographer,
		),
		goldmark.WithParserOptions(
			parser.WithASTTransformers(
				util.Prioritized(&sourceLineTransformer{}, 100),
			),
		),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithXHTML(),
			html.WithUnsafe(),
			goldmarkrenderer.WithNodeRenderers(
				util.Prioritized(newSourceLineRenderer(), 1),
			),
		),
	)
}

var markdownParser = buildGoldmark()

// ---------------------------------------------------------------------------
// render() — markdown file → full HTML document
// ---------------------------------------------------------------------------

func renderMarkdown(filePath string, opts wrapOptions) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	source := stripFrontmatter(string(data))
	var buf bytes.Buffer
	if err := markdownParser.Convert([]byte(source), &buf); err != nil {
		return "", err
	}
	pygmentsCSS := generatePygmentsCSS()
	title := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	return wrapMarkdown(title, buf.String(), pygmentsCSS, opts), nil
}

// ---------------------------------------------------------------------------
// renderCodeFile() — syntax-highlighted source file → full HTML
// ---------------------------------------------------------------------------

func renderCodeFile(filePath string, opts wrapOptions) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	source := string(data)
	name := filepath.Base(filePath)

	lexer := lexers.Match(name)
	if lexer == nil {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		return wrapPlain(name, source, opts), nil
	}

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return wrapPlain(name, source, opts), nil
	}
	var buf bytes.Buffer
	if err := chromaFormatter.Format(&buf, style, iterator); err != nil {
		return wrapPlain(name, source, opts), nil
	}
	pygmentsCSS := generatePygmentsCSS()
	return wrapCode(name, buf.String(), pygmentsCSS, opts), nil
}

// ---------------------------------------------------------------------------
// renderPDF() / renderImage()
// ---------------------------------------------------------------------------

func renderPDF(filePath, urlPath string, opts wrapOptions) string {
	name := filepath.Base(filePath)
	return wrapPDF(name, urlPath, opts)
}

func renderImage(filePath, urlPath string, opts wrapOptions) string {
	name := filepath.Base(filePath)
	return wrapImage(name, urlPath, opts)
}

// ---------------------------------------------------------------------------
// canRenderAsCode() / isTextFile()
// ---------------------------------------------------------------------------

func canRenderAsCode(filePath string) bool {
	name := filepath.Base(filePath)
	l := lexers.Match(name)
	return l != nil
}

func isTextFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	textExts := map[string]bool{
		".txt": true, ".log": true, ".cfg": true, ".ini": true,
		".conf": true, ".env": true, ".csv": true, ".tsv": true,
		".json": true, ".yaml": true, ".yml": true, ".toml": true,
		".xml": true, ".svg": true, ".md": true, ".rst": true, ".tex": true,
	}
	if textExts[ext] {
		return true
	}
	mimeType := mime.TypeByExtension(ext)
	return strings.HasPrefix(mimeType, "text/")
}
