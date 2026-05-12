package main

import (
	"strings"
	"testing"
)

// mustContain fails the test if s does not contain sub.
func mustContain(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected output to contain %q", sub)
	}
}

// mustNotContain fails the test if s contains sub.
func mustNotContain(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("expected output NOT to contain %q", sub)
	}
}

// ---------------------------------------------------------------------------
// findTagEnd
// ---------------------------------------------------------------------------

func TestFindTagEnd_Simple(t *testing.T) {
	html := "<div>"
	got := findTagEnd(html, 0)
	if got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

func TestFindTagEnd_WithAttributes(t *testing.T) {
	html := `<div class="foo">`
	got := findTagEnd(html, 0)
	if got != len(html) {
		t.Fatalf("got %d, want %d", got, len(html))
	}
}

func TestFindTagEnd_GtInsideDoubleQuote(t *testing.T) {
	html := `<div onclick="a > b">text`
	got := findTagEnd(html, 0)
	want := strings.Index(html, ">text") // the real closing >
	if got != want+1 {
		t.Fatalf("got %d, want %d (should skip '>' inside attribute)", got, want+1)
	}
}

func TestFindTagEnd_GtInsideSingleQuote(t *testing.T) {
	html := `<div onclick='a > b'>text`
	got := findTagEnd(html, 0)
	want := strings.Index(html, ">text")
	if got != want+1 {
		t.Fatalf("got %d, want %d", got, want+1)
	}
}

func TestFindTagEnd_Unterminated(t *testing.T) {
	html := "<div class=\"foo\""
	got := findTagEnd(html, 0)
	if got != -1 {
		t.Fatalf("got %d, want -1 for unterminated tag", got)
	}
}

// ---------------------------------------------------------------------------
// annotateHTMLSourceLines
// ---------------------------------------------------------------------------

func TestAnnotate_BlockTagGetsAnnotated(t *testing.T) {
	in := "<p>hello</p>"
	out := annotateHTMLSourceLines(in)
	if !strings.Contains(out, `data-source-lines="`) {
		t.Errorf("expected annotation, got: %s", out)
	}
	if !strings.Contains(out, "<p ") {
		t.Errorf("expected opening <p with space, got: %s", out)
	}
}

func TestAnnotate_NonBlockTagPassesThrough(t *testing.T) {
	in := "<span>hello</span>"
	out := annotateHTMLSourceLines(in)
	if strings.Contains(out, "data-source-lines") {
		t.Errorf("span should not be annotated, got: %s", out)
	}
	if out != in {
		t.Errorf("non-block tag output changed: got %q, want %q", out, in)
	}
}

func TestAnnotate_GtInAttribute(t *testing.T) {
	// '>' inside an attribute value must not end the tag prematurely.
	in := `<div onclick="a > b">content</div>`
	out := annotateHTMLSourceLines(in)
	if !strings.Contains(out, `onclick="a > b"`) {
		t.Errorf("attribute value corrupted: %s", out)
	}
	if !strings.Contains(out, "data-source-lines") {
		t.Errorf("div should be annotated: %s", out)
	}
}

func TestAnnotate_HTMLComment(t *testing.T) {
	// Comments containing '>' must pass through unchanged.
	in := "<!-- x > y --><p>text</p>"
	out := annotateHTMLSourceLines(in)
	if !strings.Contains(out, "<!-- x > y -->") {
		t.Errorf("comment corrupted: %s", out)
	}
	if !strings.Contains(out, "data-source-lines") {
		t.Errorf("<p> after comment should be annotated: %s", out)
	}
}

func TestAnnotate_ScriptWithLt(t *testing.T) {
	// '<' inside a script block must not be parsed as a tag start.
	in := "<script>\nif (a < b) { doSomething(); }\n</script><p>after</p>"
	out := annotateHTMLSourceLines(in)
	if !strings.Contains(out, "if (a < b)") {
		t.Errorf("script content corrupted: %s", out)
	}
	if !strings.Contains(out, "data-source-lines") {
		t.Errorf("<p> after script should be annotated: %s", out)
	}
}

func TestAnnotate_StyleBlock(t *testing.T) {
	in := "<style>div > p { color: red; }</style><div>text</div>"
	out := annotateHTMLSourceLines(in)
	if !strings.Contains(out, "div > p { color: red; }") {
		t.Errorf("style content corrupted: %s", out)
	}
	if !strings.Contains(out, `<div `) && !strings.Contains(out, `<div>`) {
		t.Errorf("div missing from output: %s", out)
	}
}

func TestAnnotate_SelfClosingTagNotAnnotated(t *testing.T) {
	in := "<br/><p>text</p>"
	out := annotateHTMLSourceLines(in)
	// <br/> should pass through unchanged
	if !strings.Contains(out, "<br/>") {
		t.Errorf("br/ corrupted: %s", out)
	}
}

func TestAnnotate_AlreadyAnnotatedNotDoubled(t *testing.T) {
	in := `<p data-source-lines="3-3">text</p>`
	out := annotateHTMLSourceLines(in)
	count := strings.Count(out, "data-source-lines")
	if count != 1 {
		t.Errorf("expected 1 data-source-lines attribute, got %d: %s", count, out)
	}
}

func TestAnnotate_ClosingTagNotAnnotated(t *testing.T) {
	in := "<div>text</div>"
	out := annotateHTMLSourceLines(in)
	if strings.Contains(out, "</div data-source-lines") {
		t.Errorf("closing tag should not be annotated: %s", out)
	}
}

// ---------------------------------------------------------------------------
// wrapMarkdown
// ---------------------------------------------------------------------------

func TestWrapMarkdown_BasicStructure(t *testing.T) {
	out := wrapMarkdown("Hello World", "<p>content</p>", wrapOptions{})
	mustContain(t, out, "<!DOCTYPE html>")
	mustContain(t, out, "<title>Hello World</title>")
	mustContain(t, out, `id="serve-content"`)
	mustContain(t, out, "<p>content</p>")
	mustContain(t, out, `id="serve-reload-script"`)
	mustContain(t, out, "</html>")
}

func TestWrapMarkdown_TitleXSSEscaped(t *testing.T) {
	out := wrapMarkdown(`<script>alert(1)</script>`, "", wrapOptions{})
	mustContain(t, out, "&lt;script&gt;")
	// The title tag should not contain a raw unescaped <script> that would execute.
	// Count <script> tags: only the legitimate ones from our own scripts should appear.
	if strings.Contains(out, "<title><script>") {
		t.Error("title XSS not escaped: raw <script> found inside <title>")
	}
}

func TestWrapMarkdown_CommentUIPresent(t *testing.T) {
	out := wrapMarkdown("title", "", wrapOptions{})
	mustContain(t, out, `id="comment-btn"`)
	mustContain(t, out, `id="comment-panel"`)
}

func TestWrapMarkdown_ContentNotDoubleEscaped(t *testing.T) {
	// Pre-rendered HTML must be inserted verbatim — not double-escaped.
	out := wrapMarkdown("title", "<p>Hello &amp; world</p>", wrapOptions{})
	mustContain(t, out, "<p>Hello &amp; world</p>")
	mustNotContain(t, out, "&amp;amp;")
}

func TestWrapMarkdown_HasReloadScript(t *testing.T) {
	out := wrapMarkdown("title", "", wrapOptions{})
	mustContain(t, out, `id="serve-reload-script"`)
	mustContain(t, out, "WebSocket")
}

// ---------------------------------------------------------------------------
// wrapCode / wrapPlain — no comment UI
// ---------------------------------------------------------------------------

func TestWrapCode_NoCommentUI(t *testing.T) {
	out := wrapCode("main.go", "<div>highlighted</div>", wrapOptions{})
	mustContain(t, out, `id="serve-content"`)
	mustContain(t, out, `id="serve-reload-script"`)
	mustNotContain(t, out, `id="comment-btn"`)
	mustNotContain(t, out, `id="comment-panel"`)
}

func TestWrapPlain_ContentEscaped(t *testing.T) {
	out := wrapPlain("file.txt", "<script>alert(1)</script>", wrapOptions{})
	mustContain(t, out, "&lt;script&gt;alert(1)&lt;/script&gt;")
	// Verify the literal string doesn't appear unescaped as an executable tag.
	// (legitimate <script> tags from our own scripts are expected, but not
	// one containing "alert(1)")
	mustNotContain(t, out, "<script>alert(1)</script>")
}

func TestWrapPlain_NoCommentUI(t *testing.T) {
	out := wrapPlain("readme.txt", "hello", wrapOptions{})
	mustNotContain(t, out, `id="comment-btn"`)
	mustNotContain(t, out, `id="comment-panel"`)
}

// ---------------------------------------------------------------------------
// Sidebar injection
// ---------------------------------------------------------------------------

func TestWrapMarkdown_SidebarPresent(t *testing.T) {
	sidebar := &[2]string{"My Project", "/docs/index.md"}
	out := wrapMarkdown("title", "", wrapOptions{sidebar: sidebar})
	mustContain(t, out, `id="serve-sidebar"`)
	mustContain(t, out, "My Project")
	mustContain(t, out, `id="serve-sidebar-toggle"`)
	mustContain(t, out, `window.__servePath`)
	mustContain(t, out, `window.__serveFileTree`)
}

func TestWrapMarkdown_SidebarDirNameEscaped(t *testing.T) {
	sidebar := &[2]string{`<b>Evil</b>`, "/path"}
	out := wrapMarkdown("title", "", wrapOptions{sidebar: sidebar})
	mustContain(t, out, "&lt;b&gt;Evil&lt;/b&gt;")
	mustNotContain(t, out, `<b>Evil</b>`)
}

func TestWrapMarkdown_NoSidebarByDefault(t *testing.T) {
	out := wrapMarkdown("title", "", wrapOptions{})
	mustNotContain(t, out, `id="serve-sidebar"`)
	mustNotContain(t, out, `id="serve-sidebar-toggle"`)
	// window.__servePath also appears in comment.js, so we can't assert its
	// absence — just verify the sidebar HTML elements are not injected.
}

func TestWrapMarkdown_SidebarWithFileTree(t *testing.T) {
	sidebar := &[2]string{"dir", "/cur"}
	tree := []FileNode{
		{Name: "a.md", Path: "a.md", Type: "file"},
		{Name: "sub", Path: "sub", Type: "dir"},
	}
	out := wrapMarkdown("title", "", wrapOptions{sidebar: sidebar, fileTree: tree})
	mustContain(t, out, `"a.md"`)
	mustContain(t, out, `"sub"`)
}

// ---------------------------------------------------------------------------
// wrapPDF / wrapImage / wrapFileInfo
// ---------------------------------------------------------------------------

func TestWrapPDF_Structure(t *testing.T) {
	out := wrapPDF("doc.pdf", "docs/doc.pdf", wrapOptions{})
	mustContain(t, out, `<embed `)
	mustContain(t, out, `type="application/pdf"`)
	mustContain(t, out, `?raw=1`)
	mustNotContain(t, out, `id="comment-btn"`)
}

func TestWrapImage_Structure(t *testing.T) {
	out := wrapImage("photo.png", "imgs/photo.png", wrapOptions{})
	mustContain(t, out, `<img `)
	mustContain(t, out, `serve-image`)
	mustContain(t, out, `?raw=1`)
}

func TestWrapImage_AltEscaped(t *testing.T) {
	out := wrapImage(`<bad>`, "img.png", wrapOptions{})
	mustContain(t, out, `alt="&lt;bad&gt;"`)
	mustNotContain(t, out, `alt="<bad>"`)
}

func TestWrapFileInfo_Structure(t *testing.T) {
	out := wrapFileInfo("archive.zip", "files/archive.zip", 123456, wrapOptions{})
	mustContain(t, out, `class="file-info"`)
	mustContain(t, out, "ZIP")
	mustContain(t, out, "Download")
	mustContain(t, out, "120.6 KB")
}

func TestWrapFileInfo_TitleEscaped(t *testing.T) {
	out := wrapFileInfo(`<bad>.zip`, "bad.zip", 0, wrapOptions{})
	mustNotContain(t, out, `<h2><bad>`)
	mustContain(t, out, "&lt;bad&gt;")
}

// ---------------------------------------------------------------------------
// faviconLink
// ---------------------------------------------------------------------------

func TestFaviconLink_ContainsDataURI(t *testing.T) {
	link := faviconLink("/some/path.md")
	mustContain(t, link, `<link rel="icon"`)
	mustContain(t, link, `data:image/svg+xml;base64,`)
}

func TestFaviconLink_DeterministicForSamePath(t *testing.T) {
	a := faviconLink("/foo/bar.md")
	b := faviconLink("/foo/bar.md")
	if a != b {
		t.Error("faviconLink should be deterministic")
	}
}

func TestFaviconLink_DifferentForDifferentPaths(t *testing.T) {
	a := faviconLink("/foo/bar.md")
	b := faviconLink("/foo/baz.md")
	if a == b {
		t.Error("faviconLink should differ for different paths")
	}
}

// ---------------------------------------------------------------------------
// formatSize
// ---------------------------------------------------------------------------

func TestFormatSize(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range cases {
		got := formatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}
