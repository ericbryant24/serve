package render

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"serve/internal/anchor"
)

// MaxInlineBytes is the largest file serve renders; past it a file gets an
// information page with a download link.
const MaxInlineBytes = 8 << 20

// Options are the per-render choices.
type Options struct {
	Markdown MarkdownOptions
}

var markdownExts = map[string]bool{".md": true, ".markdown": true, ".mdown": true, ".mkd": true, ".mkdn": true}
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".bmp": true, ".ico": true, ".avif": true}

// KindOf decides how a file is shown from its name and its first bytes.
func KindOf(path string, head []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case markdownExts[ext]:
		return KindMarkdown
	case ext == ".html" || ext == ".htm":
		return KindHTML
	case ext == ".pdf":
		return KindPDF
	case imageExts[ext]:
		return KindImage
	}
	if looksText(head) {
		return KindCode
	}
	return KindBinary
}

// IsMarkdown reports whether path is a markdown file.
func IsMarkdown(path string) bool { return markdownExts[strings.ToLower(filepath.Ext(path))] }

func looksText(head []byte) bool {
	if len(head) > 8192 {
		head = head[:8192]
	}
	if strings.IndexByte(string(head), 0) >= 0 {
		return false
	}
	// A multi-byte character may be cut at the end of the sample.
	for i := 0; i < 4 && len(head) > 0 && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	return utf8.Valid(head)
}

// Render renders a file's contents according to its kind. Kinds serve shows
// with a viewer of their own (PDF, image, binary) get a Doc with no blocks.
func Render(path string, data []byte, opt Options) *Doc {
	kind := KindOf(path, data)
	source := string(data)
	switch kind {
	case KindMarkdown:
		return Markdown(source, opt.Markdown)
	case KindHTML:
		return HTMLFile(source)
	case KindCode:
		return Code(source, filepath.Base(path))
	}
	return &Doc{Kind: kind, Rev: anchor.Hash(source)}
}
