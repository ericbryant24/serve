package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// generateDataURL powers the `serve --data-url` CLI flag (there is no HTTP
// endpoint for it). These assertions mirror the black-box coverage that used to
// live in the pytest suite against the removed /__data_url route.
func TestGenerateDataURL_Markdown(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(fp, []byte("# Title\n\nHello World from the doc.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	url, err := generateDataURL(fp, "markdown")
	if err != nil {
		t.Fatal(err)
	}

	const prefix = "data:text/html;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("data URL should start with %q", prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatalf("data URL payload is not valid base64: %v", err)
	}
	html := string(decoded)
	if !strings.Contains(html, "Hello World") {
		t.Error("rendered page content missing from data URL")
	}
	// A data URL is self-contained with no server, so the live-reload WebSocket
	// script must be stripped.
	if strings.Contains(html, "function connect()") {
		t.Error("reload WebSocket script should be stripped from the data URL")
	}
}
