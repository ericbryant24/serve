package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Configuration
//
// The only setting today is whether reports may be uploaded. It exists so an
// organisation can adopt serve with filing switched off rather than banning
// the tool outright: local capture and `serve report export` keep working, and
// feedback routes through whatever channel their policy already approves.
// ---------------------------------------------------------------------------

const (
	UploadPrompt = "prompt" // ask, then file on GitHub (default)
	UploadNever  = "never"  // local capture and export only
)

type ReportsConfig struct {
	Upload string `json:"upload"`
}

type Config struct {
	Reports ReportsConfig `json:"reports"`
}

func configPath() string { return filepath.Join(homeDir(), ".serve", "config.json") }

func loadConfig() Config {
	c := Config{Reports: ReportsConfig{Upload: UploadPrompt}}
	data, err := os.ReadFile(configPath())
	if err != nil {
		return c
	}
	// A malformed config must not disable the app; fall back to defaults.
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{Reports: ReportsConfig{Upload: UploadPrompt}}
	}
	if c.Reports.Upload == "" {
		c.Reports.Upload = UploadPrompt
	}
	return c
}

// uploadPolicy resolves the effective policy. The environment variable wins so
// it can be set by MDM or a shared shell profile without editing dotfiles.
func uploadPolicy() string {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("SERVE_REPORT_UPLOAD"))); v != "" {
		if v == UploadNever {
			return UploadNever
		}
		return UploadPrompt
	}
	if loadConfig().Reports.Upload == UploadNever {
		return UploadNever
	}
	return UploadPrompt
}

func uploadAllowed() bool { return uploadPolicy() != UploadNever }

// uploadBlockedReason explains, in the words the user needs to act on it, why
// filing is unavailable.
func uploadBlockedReason() string {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv("SERVE_REPORT_UPLOAD"))); v == UploadNever {
		return "Filing is disabled by SERVE_REPORT_UPLOAD=never. Use `serve report export <id>` instead."
	}
	return "Filing is disabled by \"reports.upload\": \"never\" in " + configPath() +
		". Use `serve report export <id>` instead."
}
