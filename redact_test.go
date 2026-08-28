package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		name string
		in   string
		mode PathMode
		want string
	}{
		{"under home keeps basename", filepath.Join(home, "Projects", "acme", "q3.md"), PathShape, "~/…/…/q3.md"},
		{"under home ext only", filepath.Join(home, "Projects", "acme", "q3.md"), PathExtOnly, "~/…/…/….md"},
		{"home root file", filepath.Join(home, "notes.md"), PathShape, "~/notes.md"},
		{"home itself", home, PathShape, "~"},
		{"absolute outside home", "/var/log/serve.log", PathShape, "/…/…/serve.log"},
		{"absolute ext only", "/var/log/serve.log", PathExtOnly, "/…/…/….log"},
		{"relative", "docs/design/spec.md", PathShape, "…/…/spec.md"},
		{"bare filename", "spec.md", PathShape, "spec.md"},
		{"no extension", "/etc/hosts", PathExtOnly, "/…/…"},
		{"empty", "", PathShape, ""},
		{"root", "/", PathShape, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactPath(c.in, c.mode); got != c.want {
				t.Errorf("redactPath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactPathNeverLeaksHomeSegments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := filepath.Join(home, "Projects", "northwind-utilities", "renewal-risk.md")
	got := redactPath(p, PathExtOnly)
	for _, leak := range []string{"Projects", "northwind-utilities", "renewal-risk", filepath.Base(home)} {
		if strings.Contains(got, leak) {
			t.Fatalf("redacted path %q still contains %q", got, leak)
		}
	}
}

func TestRedactTextPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	secret := filepath.Join(home, "Projects", "acme", "q3.md")

	in := "open " + secret + ": permission denied"
	got := redactTextPaths(in)

	if strings.Contains(got, "acme") || strings.Contains(got, "Projects") {
		t.Fatalf("path survived redaction: %q", got)
	}
	if !strings.HasPrefix(got, "open ") || !strings.HasSuffix(got, ": permission denied") {
		t.Fatalf("surrounding text mangled: %q", got)
	}
	if !strings.Contains(got, "q3.md") {
		t.Fatalf("basename should survive in PathShape mode: %q", got)
	}
}

func TestScanSecretsPositive(t *testing.T) {
	cases := []struct {
		kind string
		in   string
	}{
		{"aws-access-key", "key = AKIAIOSFODNN7EXAMPLE"},
		{"github-token", "ghp_" + strings.Repeat("a", 36)},
		{"github-pat", "github_pat_" + strings.Repeat("b", 30)},
		{"slack-token", "xoxb-1234567890-abcdefghij"},
		{"google-api-key", "AIza" + strings.Repeat("c", 35)},
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"url-credentials", "postgres://admin:hunter2pass@db.internal:5432/x"},
		{"auth-header", "Authorization: Bearer abcdefghijklmnop"},
		{"credential-assignment", `api_key: "sk-live-abcdefgh12345"`},
		{"email", "reported by ann.smith@example.com"},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			hits := scanSecrets(c.in)
			if len(hits) == 0 {
				t.Fatalf("no hit for %s in %q", c.kind, c.in)
			}
			found := false
			for _, h := range hits {
				if h.Kind == c.kind {
					found = true
				}
			}
			if !found {
				t.Errorf("expected kind %q, got %+v", c.kind, hits)
			}
		})
	}
}

// A flag that fires on everything is a flag nobody reads. These must stay quiet.
func TestScanSecretsNegative(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"git sha", "commit a94a8fe5ccb19ba61c4c0873d391e987982fbbd3"},
		{"short sha", "fixed in 00c890a"},
		{"uuid", "id 550e8400-e29b-41d4-a716-446655440000"},
		{"sha256", "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
		{"png base64", "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk"},
		{"prose about passwords", "The password field should be masked in the UI."},
		{"empty assignment", `password: ""`},
		{"placeholder", "token: <redacted>"},
		{"semver", "serve v1.4.2 on darwin/arm64"},
		{"hex color", "background: #f6f8fa;"},
		{"plain url", "https://github.com/ericbryant24/serve/issues/12"},
		{"go version", "go1.24.7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if hits := scanSecrets(c.in); len(hits) != 0 {
				t.Errorf("false positive on %q: %+v", c.in, hits)
			}
		})
	}
}

func TestScanSecretsNoOverlappingHits(t *testing.T) {
	in := `api_key: "ghp_` + strings.Repeat("a", 36) + `"`
	hits := scanSecrets(in)
	for i := 1; i < len(hits); i++ {
		if hits[i].Start < hits[i-1].End {
			t.Fatalf("overlapping hits: %+v", hits)
		}
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
}
