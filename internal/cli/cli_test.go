package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serve/internal/config"
)

func TestParseAcceptsFlagsAnywhere(t *testing.T) {
	fs := flags("x", "usage")
	all := fs.Bool("all", false, "")
	as := fs.String("as", "", "")
	pos, err := parse(fs, "usage", []string{"doc.md", "--all", "abc", "--as", "human", "--", "--not-a-flag", "text"})
	if err != nil {
		t.Fatal(err)
	}
	if !*all || *as != "human" || strings.Join(pos, "|") != "doc.md|abc|--not-a-flag|text" {
		t.Fatalf("all=%v as=%q pos=%v", *all, *as, pos)
	}
	if _, err := parse(flags("x", "usage"), "usage", []string{"--nope"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestParseAuthor(t *testing.T) {
	cfg := config.Config{Name: "Eric"}
	t.Setenv("SERVE_AUTHOR", "Claude")
	cases := map[string]string{"": "agent:Claude", "agent": "agent:Claude", "human": "human:Eric", "human:Dana": "human:Dana", "agent:Bot": "agent:Bot"}
	for in, want := range cases {
		a, err := parseAuthor(in, cfg)
		if err != nil || a.Kind+":"+a.Name != want {
			t.Errorf("%q: got %v %v, want %s", in, a, err, want)
		}
	}
	if _, err := parseAuthor("robot", cfg); err == nil {
		t.Error("bad kind accepted")
	}
}

func TestWriteClaudeMDReplacesItsOwnSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")
	os.WriteFile(p, []byte("# Mine\n\nKeep.\n\n# Inline Document Comments\n\nold\n\n# Later\n\nAlso keep.\n"), 0o644)
	if err := writeClaudeMD(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "Keep.") || !strings.Contains(s, "Also keep.") || strings.Contains(s, "\nold\n") || strings.Count(s, claudeMDMarker) != 1 {
		t.Fatalf("got:\n%s", s)
	}
	// Running it again changes nothing.
	writeClaudeMD(p)
	b2, _ := os.ReadFile(p)
	if string(b2) != s {
		t.Fatal("not idempotent")
	}
}

func TestLooksLikeCommand(t *testing.T) {
	if !looksLikeCommand("frobnicate") || looksLikeCommand("./doc.md") || looksLikeCommand("docs/") {
		t.Fatal("wrong classification")
	}
}
