package server

import (
	"context"
	stdhtml "html"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// renderMarp renders a Marp deck for presenting, with marp-cli (or npx).
func renderMarp(p string) string {
	cmd := marpCmd()
	name := stdhtml.EscapeString(filepath.Base(p))
	if cmd == nil {
		return marpPage(name, `<p>This document is a Marp deck, but neither <code>marp</code> nor <code>npx</code> is on your PATH.</p><pre>npm install -g @marp-team/marp-cli</pre>`)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := append(cmd[1:], "--html", p, "-o", "-")
	out, err := exec.CommandContext(ctx, cmd[0], args...).Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg = string(ee.Stderr)
		}
		return marpPage(name, `<p>marp-cli failed:</p><pre>`+stdhtml.EscapeString(msg)+`</pre>`)
	}
	if strings.TrimSpace(string(out)) == "" {
		return marpPage(name, "<p>marp-cli produced no output.</p>")
	}
	return string(out)
}

func marpCmd() []string {
	if p, err := exec.LookPath("marp"); err == nil {
		return []string{p}
	}
	if p, err := exec.LookPath("npx"); err == nil {
		return []string{p, "-y", "@marp-team/marp-cli"}
	}
	return nil
}

func marpPage(name, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + name + `</title>` +
		`<style>body{font:15px/1.5 -apple-system,system-ui,sans-serif;max-width:680px;margin:80px auto;padding:0 24px}` +
		`pre{background:#f4f4f4;padding:16px;border-radius:6px;overflow-x:auto;white-space:pre-wrap}</style></head><body>` +
		`<h1>` + name + `</h1>` + body + `</body></html>`
}
