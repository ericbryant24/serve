package render

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"

	"serve/internal/anchor"
)

// legacyBlocks describes the document the way the old JSON comment store
// fingerprinted it, so imported comments can find their block: the same block
// kinds, the same text extraction, the same neighbours.
func legacyBlocks(root ast.Node, src []byte, doc *Doc, st *mdState) []anchor.Block {
	kinds := map[ast.NodeKind]bool{
		ast.KindHeading: true, ast.KindBlockquote: true, ast.KindCodeBlock: true,
		ast.KindFencedCodeBlock: true, ast.KindHTMLBlock: true, ast.KindList: true,
		ast.KindListItem: true, ast.KindParagraph: true, extast.KindTable: true,
		extast.KindTableHeader: true, extast.KindTableRow: true, extast.KindTableCell: true,
	}
	var nodes []ast.Node
	var out []anchor.Block
	order := map[ast.Node]int{}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || !kinds[n.Kind()] {
			return ast.WalkContinue, nil
		}
		t := anchor.Normalize(oldBlockText(n, src))
		if t == "" {
			return ast.WalkContinue, nil
		}
		s, e := nodeSpan(n, src)
		leaf := false
		if i, ok := st.nodes[n]; ok {
			leaf = st.blocks[i].Leaf
		}
		order[n] = len(out)
		nodes = append(nodes, n)
		out = append(out, anchor.Block{Start: s, End: e, Text: t, Hash: anchor.HashBlockText(t), Leaf: leaf, Find: doc.finder(s, e)})
		return ast.WalkContinue, nil
	})
	for i, n := range nodes {
		for p := n.PreviousSibling(); p != nil; p = p.PreviousSibling() {
			if j, ok := order[p]; ok {
				out[i].PrevText = contextText(out[j].Text)
				break
			}
		}
		for x := n.NextSibling(); x != nil; x = x.NextSibling() {
			if j, ok := order[x]; ok {
				out[i].NextText = contextText(out[j].Text)
				break
			}
		}
	}
	return out
}

// oldBlockText is the old store's approximation of a block's visible text.
func oldBlockText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if c != n && c.Type() == ast.TypeBlock && b.Len() > 0 {
			b.WriteByte(' ')
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.URL(src))
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		switch c.Kind() {
		case ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindHTMLBlock:
			lines := c.Lines()
			for i := 0; i < lines.Len(); i++ {
				l := lines.At(i)
				b.Write(src[l.Start:l.Stop])
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

const contextTextLimit = 200

func contextText(normalized string) string {
	if len(normalized) <= contextTextLimit {
		return normalized
	}
	cut := normalized[:contextTextLimit]
	if i := strings.LastIndexByte(cut, ' '); i > contextTextLimit/2 {
		cut = cut[:i]
	}
	return cut
}
