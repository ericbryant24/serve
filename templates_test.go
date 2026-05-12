package main

import (
	"strings"
	"testing"
)

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
