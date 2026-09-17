package main

import (
	"strings"
	"testing"
)

// tree used by the suggestion tests:
//
//	docs/
//	  deep/plan.md
//	  plan.md
//	  notes.txt
//	plan.html
//	README.md
func suggestionTree() []FileNode {
	return []FileNode{
		{Name: "docs", Path: "docs", Type: "dir", Children: []FileNode{
			{Name: "deep", Path: "docs/deep", Type: "dir", Children: []FileNode{
				{Name: "plan.md", Path: "docs/deep/plan.md", Type: "file"},
			}},
			{Name: "notes.txt", Path: "docs/notes.txt", Type: "file"},
			{Name: "plan.md", Path: "docs/plan.md", Type: "file"},
		}},
		{Name: "plan.html", Path: "plan.html", Type: "file"},
		{Name: "README.md", Path: "README.md", Type: "file"},
	}
}

func TestFindPathSuggestionsPrefersNearestSameName(t *testing.T) {
	got := findPathSuggestions(suggestionTree(), "plan.md")
	if len(got) == 0 {
		t.Fatal("expected suggestions for a moved file")
	}
	// Both same-filename matches rank ahead of the .html one, and the shallower
	// docs/plan.md ahead of docs/deep/plan.md.
	want := []string{"docs/plan.md", "docs/deep/plan.md", "plan.html"}
	for i, w := range want {
		if i >= len(got) || got[i].Path != w {
			t.Fatalf("suggestion %d = %v, want %s (all: %v)", i, got, w, got)
		}
	}
	if !got[0].Moved() {
		t.Error("same-filename match should report Moved()")
	}
	if got[2].Moved() {
		t.Error("same-stem match should not report Moved()")
	}
}

func TestFindPathSuggestionsPrefersSameFolder(t *testing.T) {
	// The missing file was in docs/, so docs/plan.md beats the root-level match.
	got := findPathSuggestions(suggestionTree(), "docs/gone/plan.md")
	if len(got) == 0 || got[0].Path != "docs/plan.md" {
		t.Fatalf("got %v, want docs/plan.md first", got)
	}
}

func TestFindPathSuggestionsSimilarName(t *testing.T) {
	tree := []FileNode{{Name: "plan-v2.md", Path: "plan-v2.md", Type: "file"}}
	got := findPathSuggestions(tree, "plan.md")
	if len(got) != 1 || got[0].Reason != "similar name" {
		t.Fatalf("got %v, want one similar-name suggestion", got)
	}
	if got[0].Moved() {
		t.Error("a similar name is not a move")
	}
}

func TestFindPathSuggestionsNoneForUnrelatedNames(t *testing.T) {
	tree := []FileNode{
		{Name: "README.md", Path: "README.md", Type: "file"},
		{Name: "docs", Path: "docs", Type: "dir", Children: []FileNode{
			{Name: "notes.txt", Path: "docs/notes.txt", Type: "file"},
		}},
	}
	if got := findPathSuggestions(tree, "quarterly-budget.md"); len(got) != 0 {
		t.Fatalf("got %v, want no suggestions", got)
	}
}

func TestFindPathSuggestionsSkipsDirsAndSelf(t *testing.T) {
	tree := []FileNode{
		{Name: "plan.md", Path: "plan.md", Type: "dir"},
		{Name: "keep.md", Path: "keep.md", Type: "file"},
	}
	if got := findPathSuggestions(tree, "plan.md"); len(got) != 0 {
		t.Fatalf("got %v, want no suggestions (a dir is not a candidate)", got)
	}
}

func TestFindPathSuggestionsCapped(t *testing.T) {
	var tree []FileNode
	for _, d := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		tree = append(tree, FileNode{Name: d, Path: d, Type: "dir", Children: []FileNode{
			{Name: "plan.md", Path: d + "/plan.md", Type: "file"},
		}})
	}
	if got := findPathSuggestions(tree, "plan.md"); len(got) != maxPathSuggestions {
		t.Fatalf("got %d suggestions, want %d", len(got), maxPathSuggestions)
	}
}

func TestNearestExistingDir(t *testing.T) {
	tree := suggestionTree()

	dir, entries := nearestExistingDir(tree, "docs/gone.md")
	if dir != "docs" || len(entries) != 3 {
		t.Fatalf("got (%q, %d entries), want (docs, 3)", dir, len(entries))
	}

	// A missing intermediate folder falls back to the nearest one that exists.
	dir, _ = nearestExistingDir(tree, "docs/nope/deeper/gone.md")
	if dir != "docs" {
		t.Fatalf("got %q, want docs", dir)
	}

	// Nothing in common with the tree lands at the root.
	dir, entries = nearestExistingDir(tree, "nope/gone.md")
	if dir != "" || len(entries) != len(tree) {
		t.Fatalf("got (%q, %d entries), want the root", dir, len(entries))
	}

	dir, entries = nearestExistingDir(tree, "gone.md")
	if dir != "" || len(entries) != len(tree) {
		t.Fatalf("got (%q, %d entries), want the root", dir, len(entries))
	}
}

func TestFindDirNode(t *testing.T) {
	tree := suggestionTree()
	if entries, ok := findDirNode(tree, ""); !ok || len(entries) != len(tree) {
		t.Error("empty path should return the root entries")
	}
	if entries, ok := findDirNode(tree, "docs/deep"); !ok || len(entries) != 1 {
		t.Errorf("docs/deep = (%v, %v), want its one file", entries, ok)
	}
	if _, ok := findDirNode(tree, "docs/plan.md"); ok {
		t.Error("a file path is not a directory")
	}
	if _, ok := findDirNode(tree, "nope"); ok {
		t.Error("missing directory should report false")
	}
}

func TestWrapNotFoundOffersNavigation(t *testing.T) {
	tree := suggestionTree()
	opts := wrapOptions{sidebar: &[2]string{"proj", "plan.md"}, fileTree: tree}
	listDir, entries := nearestExistingDir(tree, "plan.md")
	out := wrapNotFound("plan.md", "proj", findPathSuggestions(tree, "plan.md"), listDir, entries, opts)

	mustContain(t, out, "Not found")
	mustContain(t, out, `<code>plan.md</code>`)
	mustContain(t, out, `href="/docs/plan.md"`)  // where it probably went
	mustContain(t, out, `href="/README.md"`)     // the folder listing
	mustContain(t, out, `id="serve-sidebar"`)    // the tree is still there
	mustContain(t, out, "Comments are keyed to") // moves keep their thread

	// No #serve-content: the reload script must fall back to a full page load,
	// so a returning file comes back as a document page with its own scripts.
	if strings.Contains(out, `id="serve-content"`) {
		t.Error("not-found page should not carry #serve-content")
	}
}

func TestWrapNotFoundOffersTheWayUp(t *testing.T) {
	out := wrapNotFound("doc.md", "proj", nil, "", nil, wrapOptions{sidebar: &[2]string{"proj", "doc.md"}})
	mustContain(t, out, "This folder is empty")
	mustContain(t, out, `id="serve-nf-up"`)

	// No sidebar, no re-root control to drive: don't offer a dead button.
	bare := wrapNotFound("doc.md", "proj", nil, "", nil, wrapOptions{})
	if strings.Contains(bare, "serve-nf-up") {
		t.Error("the way up needs the sidebar's re-root button")
	}
}

func TestWrapNotFoundEscapesPath(t *testing.T) {
	out := wrapNotFound(`<img src=x onerror=alert(1)>.md`, `"dir`, nil, "", nil, wrapOptions{})
	if strings.Contains(out, "<img src=x") {
		t.Error("missing path was not escaped")
	}
	mustContain(t, out, "&lt;img src=x")
}

func TestRenderListingLinksAndEmpty(t *testing.T) {
	out := renderListing([]FileNode{
		{Name: "sub dir", Path: "sub dir", Type: "dir"},
		{Name: "a&b.md", Path: "a&b.md", Type: "file"},
	})
	mustContain(t, out, `href="/sub%20dir"`) // spaces are URL-escaped
	mustContain(t, out, `>sub dir/<`)        // dirs are marked with a slash
	mustContain(t, out, `href="/a&amp;b.md"`)

	mustContain(t, renderListing(nil), "This folder is empty")
}

func TestWrapDirIndex(t *testing.T) {
	out := wrapDirIndex("docs", "proj", []FileNode{{Name: "notes.txt", Path: "docs/notes.txt", Type: "file"}}, wrapOptions{})
	mustContain(t, out, "proj/docs/")
	mustContain(t, out, `href="/docs/notes.txt"`)
	mustContain(t, wrapDirIndex("", "proj", nil, wrapOptions{}), "This folder is empty")
}
