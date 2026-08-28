package main

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Path redaction
//
// serve renders private documents, so a filesystem path is rarely innocuous:
// /Users/<name>/Projects/<client>/<subject>.md leaks the operator, the client
// and the subject in one string. Redaction keeps the *shape* of a path — how
// deep it is, whether it is absolute, what kind of file it points at — and
// drops the parts that identify anyone.
// ---------------------------------------------------------------------------

// PathMode controls how much of a path survives redaction.
type PathMode int

const (
	// PathShape replaces the home prefix with ~ and every intermediate
	// directory with an ellipsis, keeping the basename.
	//   /Users/ann/Projects/acme/q3.md -> ~/…/…/q3.md
	PathShape PathMode = iota

	// PathExtOnly additionally drops the basename, keeping the extension.
	// Used for anything auto-attached to a report, where a filename like
	// "acme-2026-layoffs.md" is itself the disclosure.
	//   /Users/ann/Projects/acme/q3.md -> ~/…/…/….md
	PathExtOnly
)

const ellipsis = "…"

// redactPath rewrites a filesystem path so it keeps its shape and loses its
// content. It is deliberately total: any input produces output safe to log.
func redactPath(p string, mode PathMode) string {
	if strings.TrimSpace(p) == "" {
		return p
	}

	clean := filepath.Clean(p)
	prefix, rest := "", clean

	if home := homeDir(); home != "" && home != "." {
		if r, ok := stripDirPrefix(clean, home); ok {
			prefix, rest = "~", r
		}
	}
	if prefix == "" && filepath.IsAbs(clean) {
		vol := filepath.VolumeName(clean) // "" on unix, "C:" on Windows
		prefix = vol + "/"
		rest = strings.TrimLeft(clean[len(vol):], `/\`)
	}

	parts := splitPathParts(rest)
	if len(parts) == 0 {
		if prefix == "" {
			return ellipsis
		}
		return strings.TrimSuffix(prefix, "/")
	}

	last := len(parts) - 1
	if mode == PathExtOnly {
		parts[last] = ellipsis + filepath.Ext(parts[last])
	}
	for i := 0; i < last; i++ {
		parts[i] = ellipsis
	}

	joined := strings.Join(parts, "/")
	switch {
	case prefix == "":
		return joined
	case strings.HasSuffix(prefix, "/"):
		return prefix + joined
	default:
		return prefix + "/" + joined
	}
}

// stripDirPrefix reports whether path sits under dir, returning the remainder.
func stripDirPrefix(path, dir string) (string, bool) {
	cleanDir := filepath.Clean(dir)
	if path == cleanDir {
		return "", true
	}
	withSep := cleanDir
	if !strings.HasSuffix(withSep, string(filepath.Separator)) {
		withSep += string(filepath.Separator)
	}
	if strings.HasPrefix(path, withSep) {
		return path[len(withSep):], true
	}
	return "", false
}

func splitPathParts(rest string) []string {
	fields := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	out := fields[:0]
	for _, f := range fields {
		if f != "" && f != "." {
			out = append(out, f)
		}
	}
	return out
}

// pathInTextRe finds path-shaped runs inside free text. Go's *PathError puts an
// absolute path directly into err.Error() ("open /Users/x/q3.md: permission
// denied"), so any error string reaching a log or a report has to go through
// here first.
var pathInTextRe = regexp.MustCompile(`(?:[A-Za-z]:\\|/)[^\s"'` + "`" + `,;()\[\]{}<>]{1,512}`)

// redactTextPaths rewrites every path-shaped run in s. Trailing punctuation is
// preserved so "open /x/y.md: denied" stays readable as "open ~/…/y.md: denied".
func redactTextPaths(s string) string {
	return pathInTextRe.ReplaceAllStringFunc(s, func(m string) string {
		trailing := ""
		for len(m) > 0 {
			c := m[len(m)-1]
			if c == ':' || c == '.' || c == ',' || c == ';' || c == '!' || c == '?' {
				trailing = string(c) + trailing
				m = m[:len(m)-1]
				continue
			}
			break
		}
		if m == "" || m == "/" {
			return m + trailing
		}
		return redactPath(m, PathShape) + trailing
	})
}

// ---------------------------------------------------------------------------
// Secret scanning
//
// scanSecrets FLAGS, it never edits. Silently stripping would teach people to
// trust the tool and would hide the exact thing they need to look at; the
// review gate highlights every hit and blocks filing until each is
// acknowledged. That makes false positives cheap and false negatives costly,
// which is the tradeoff these patterns are tuned for.
// ---------------------------------------------------------------------------

// SecretHit is a span of text that looks like a credential.
type SecretHit struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Kind    string `json:"kind"`
	Excerpt string `json:"excerpt"`
}

type secretPattern struct {
	kind string
	re   *regexp.Regexp
}

// Patterns are ordered most-specific first; overlapping hits keep the earlier
// (more specific) match.
var secretPatterns = []secretPattern{
	{"private-key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`)},
	{"github-pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,255}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	// Both JWT segments are base64url of JSON, so both begin "eyJ". Requiring
	// that twice is what keeps this off ordinary base64 blobs.
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{"url-credentials", regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s/:@]+:[^\s/@]{3,}@`)},
	{"auth-header", regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*(?:bearer|basic|token)\s+[A-Za-z0-9._~+/=-]{12,}`)},
	{"credential-assignment", regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|secret|access[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd)\b\s*[:=]\s*["']?[^\s"',;{}]{8,}`)},
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?\.[A-Za-z]{2,}\b`)},
}

// scanSecrets returns non-overlapping credential-shaped spans, in order.
func scanSecrets(s string) []SecretHit {
	var hits []SecretHit
	for _, p := range secretPatterns {
		for _, loc := range p.re.FindAllStringIndex(s, -1) {
			hits = append(hits, SecretHit{
				Start:   loc[0],
				End:     loc[1],
				Kind:    p.kind,
				Excerpt: excerpt(s[loc[0]:loc[1]]),
			})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Start != hits[j].Start {
			return hits[i].Start < hits[j].Start
		}
		return hits[i].End-hits[i].Start > hits[j].End-hits[j].Start
	})

	out := hits[:0]
	end := -1
	for _, h := range hits {
		if h.Start < end {
			continue // overlaps a hit we already kept
		}
		out = append(out, h)
		end = h.End
	}
	return out
}

func hasSecrets(s string) bool { return len(scanSecrets(s)) > 0 }

// excerpt shortens a matched span for display. It keeps the head, which is the
// part that identifies the credential type, and elides the body.
func excerpt(m string) string {
	m = strings.TrimSpace(strings.ReplaceAll(m, "\n", " "))
	const head = 12
	if len(m) <= head+4 {
		return m
	}
	return m[:head] + ellipsis
}
