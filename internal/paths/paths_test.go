package paths

import (
	"path/filepath"
	"testing"
)

func TestURLPathRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	h := homeResolved()
	cases := []struct{ path, url string }{
		{filepath.Join(h, "Projects", "my doc.md"), "/~/Projects/my%20doc.md"},
		{h, "/~"},
		{"/opt/notes/a#b.md", "/opt/notes/a%23b.md"},
	}
	for _, c := range cases {
		if got := URLPath(c.path); got != c.url {
			t.Errorf("URLPath(%q) = %q, want %q", c.path, got, c.url)
		}
	}
	if p, ok := FromURLPath("/~/Projects/my doc.md"); !ok || p != filepath.Join(h, "Projects", "my doc.md") {
		t.Errorf("FromURLPath: %q %v", p, ok)
	}
	if !Within("/a/b/c", "/a/b") || Within("/a/bc", "/a/b") || !Within("/a/b", "/a/b") {
		t.Error("Within")
	}
}
