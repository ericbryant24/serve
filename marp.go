package main

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	marpFrontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?\n)---\s*\n`)
	sectionOpenRe     = regexp.MustCompile(`(?i)<section\b([^>]*)>`)
)

// isMarpDoc returns true if filePath is a markdown file with `marp: true`.
func isMarpDoc(filePath string) bool {
	if !strings.EqualFold(extOf(filePath), ".md") {
		return false
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	fm := parseFrontmatter(string(data))
	if fm == nil {
		return false
	}
	v := strings.ToLower(fm["marp"])
	return v == "true" || v == "yes" || v == "on" || v == "1"
}

func parseFrontmatter(content string) map[string]string {
	m := marpFrontmatterRe.FindStringSubmatch(content)
	if m == nil {
		return nil
	}
	fm := map[string]string{}
	for _, line := range strings.Split(m[1], "\n") {
		if !strings.Contains(line, ":") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v, _ := strings.Cut(line, ":")
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		fm[strings.TrimSpace(k)] = v
	}
	return fm
}

func extOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' {
			break
		}
	}
	return ""
}

// slideLineRanges computes 1-indexed (start, end) line ranges for each slide.
func slideLineRanges(content string) [][2]int {
	lines := strings.Split(content, "\n")
	n := len(lines)

	bodyStart := 0
	if n > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < n; i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				bodyStart = i + 1
				break
			}
		}
	}

	var ranges [][2]int
	slideStart := bodyStart
	for i := bodyStart; i < n; i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			ranges = append(ranges, [2]int{slideStart + 1, i})
			slideStart = i + 1
		}
	}
	if slideStart < n {
		ranges = append(ranges, [2]int{slideStart + 1, n})
	} else if len(ranges) == 0 {
		ranges = append(ranges, [2]int{bodyStart + 1, bodyStart + 1})
	}
	return ranges
}

func injectSectionSourceLines(htmlStr string, ranges [][2]int) string {
	idx := 0
	return sectionOpenRe.ReplaceAllStringFunc(htmlStr, func(match string) string {
		if strings.Contains(strings.ToLower(match), "data-source-lines") {
			return match
		}
		if idx >= len(ranges) {
			return match
		}
		r := ranges[idx]
		idx++
		// Extract the attributes part from the match
		sub := sectionOpenRe.FindStringSubmatch(match)
		attrs := ""
		if len(sub) > 1 {
			attrs = sub[1]
		}
		return fmt.Sprintf(`<section%s data-source-lines="%d-%d">`, attrs, r[0], r[1])
	})
}

func resolveMarpCmd() []string {
	if path, err := exec.LookPath("marp"); err == nil {
		_ = path
		return []string{"marp"}
	}
	if path, err := exec.LookPath("npx"); err == nil {
		_ = path
		return []string{"npx", "-y", "@marp-team/marp-cli"}
	}
	return nil
}

func missingMarpPage(filePath string, sidebar *[2]string, fileTree []FileNode, faviconPath string, bare bool) string {
	name := html.EscapeString(baseNameOf(filePath))
	body := `<!doctype html><html><head>` +
		`<meta charset="utf-8">` +
		`<title>` + name + ` — marp-cli required</title>` +
		`<style>` +
		`body{font:14px/1.5 -apple-system,Segoe UI,sans-serif;max-width:680px;` +
		`margin:80px auto;padding:0 24px;color:#222}` +
		`code{background:#f4f4f4;padding:2px 6px;border-radius:3px;` +
		`font-family:Menlo,monospace;font-size:13px}` +
		`pre{background:#f4f4f4;padding:16px;border-radius:6px;overflow-x:auto}` +
		`h1{font-size:20px}.warn{color:#b54708}` +
		`</style></head><body>` +
		`<h1>Marp deck — <code>` + name + `</code></h1>` +
		`<p class="warn">This document declares <code>marp: true</code>, ` +
		`but neither <code>marp</code> nor <code>npx</code> is on your PATH.</p>` +
		`<p>Install one of:</p>` +
		`<pre>npm install -g @marp-team/marp-cli</pre>` +
		`<p>or install Node so the <code>npx</code> fallback works.</p>` +
		`<p>The page will reload automatically once the file is saved.</p>` +
		`</body></html>`
	return injectReloadScript(body, sidebar, fileTree, faviconPath, false, bare)
}

func marpErrorPage(filePath, stderr string, sidebar *[2]string, fileTree []FileNode, faviconPath string, bare bool) string {
	name := html.EscapeString(baseNameOf(filePath))
	body := `<!doctype html><html><head>` +
		`<meta charset="utf-8">` +
		`<title>` + name + ` — marp error</title>` +
		`<style>` +
		`body{font:14px/1.5 -apple-system,Segoe UI,sans-serif;max-width:780px;` +
		`margin:60px auto;padding:0 24px;color:#222}` +
		`pre{background:#1e1e1e;color:#f4f4f4;padding:16px;border-radius:6px;` +
		`overflow-x:auto;white-space:pre-wrap;font:12px Menlo,monospace}` +
		`h1{font-size:20px;color:#b42318}` +
		`</style></head><body>` +
		`<h1>marp-cli failed on ` + name + `</h1>` +
		`<pre>` + html.EscapeString(stderr) + `</pre>` +
		`<p>Fix the slide source and save — the page will reload.</p>` +
		`</body></html>`
	return injectReloadScript(body, sidebar, fileTree, faviconPath, false, bare)
}

func renderMarp(filePath string, sidebar *[2]string, fileTree []FileNode, faviconPath string, bare bool) string {
	cmd := resolveMarpCmd()
	if cmd == nil {
		return missingMarpPage(filePath, sidebar, fileTree, faviconPath, bare)
	}

	args := append(cmd[1:], "--html", filePath, "-o", "-")
	c := exec.Command(cmd[0], args...)
	out, err := c.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return marpErrorPage(filePath, stderr, sidebar, fileTree, faviconPath, bare)
	}
	if strings.TrimSpace(string(out)) == "" {
		return marpErrorPage(filePath, "(no output)", sidebar, fileTree, faviconPath, bare)
	}

	data, readErr := os.ReadFile(filePath)
	var ranges [][2]int
	if readErr == nil {
		ranges = slideLineRanges(string(data))
	}
	htmlStr := injectSectionSourceLines(string(out), ranges)
	return injectReloadScript(htmlStr, sidebar, fileTree, faviconPath, false, bare)
}

func baseNameOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
