// Package config reads ~/.serve/config.json.
package config

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"serve/internal/paths"
	"serve/internal/render"
)

// Config is the user's settings. Every field is optional.
type Config struct {
	// Name is how messages written in the browser are signed.
	Name string `json:"name,omitempty"`
	// Port is the background server's port.
	Port int `json:"port,omitempty"`
	// Markdown rendering choices; the defaults match GitHub.
	Markdown struct {
		HardWraps   bool `json:"hard_wraps,omitempty"`
		Typographer bool `json:"typographer,omitempty"`
	} `json:"markdown"`
	// RawHTML lists folders whose markdown keeps raw HTML (scripts included)
	// instead of having it filtered.
	RawHTML []string `json:"raw_html,omitempty"`
	// RespectGitignore hides files .gitignore ignores from the file tree.
	RespectGitignore bool `json:"respect_gitignore,omitempty"`
}

// DefaultPort is the background server's port when nothing says otherwise.
const DefaultPort = 7070

// Path is ~/.serve/config.json.
func Path() string { return filepath.Join(paths.StateDir(), "config.json") }

// Load reads the config, returning defaults for anything missing or broken.
func Load() Config {
	var c Config
	if data, err := os.ReadFile(Path()); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if p, err := strconv.Atoi(os.Getenv("SERVE_PORT")); err == nil && p > 0 {
		c.Port = p
	}
	if c.Name == "" {
		c.Name = defaultName()
	}
	return c
}

func defaultName() string {
	if u, err := user.Current(); err == nil {
		if f := strings.Fields(u.Name); len(f) > 0 {
			return f[0]
		}
		if u.Username != "" {
			return u.Username
		}
	}
	return "you"
}

// AgentName is how CLI messages are signed: $SERVE_AUTHOR, else "agent".
func AgentName() string {
	if n := strings.TrimSpace(os.Getenv("SERVE_AUTHOR")); n != "" {
		return n
	}
	return "agent"
}

// RenderOptions are the render settings for a file.
func (c Config) RenderOptions(path string) render.Options {
	o := render.Options{Markdown: render.MarkdownOptions{HardWraps: c.Markdown.HardWraps, Typographer: c.Markdown.Typographer}}
	for _, d := range c.RawHTML {
		if strings.HasPrefix(d, "~") {
			d = filepath.Join(paths.Home(), d[1:])
		}
		if paths.Within(path, paths.Abs(d)) {
			o.Markdown.UnsafeHTML = true
		}
	}
	return o
}
