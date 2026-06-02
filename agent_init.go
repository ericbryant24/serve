package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const skillContent = `---
name: serve
description: Preview markdown/HTML/code/PDF documents with live reload and read or resolve inline comments. Use when the user wants to preview a doc, check feedback, address comments, or stream comment events live.
allowed-tools:
  - Bash(serve *)
  - Bash(open *)
  - Bash(osascript *)
---

# serve — Document Server with Inline Comments

Serve a markdown/HTML file or a whole directory locally with live reload, syntax-highlighted code, Mermaid diagrams, PDF embedding, and browser-based inline comments. Source files are never modified — comments live centrally at ` + "`~/.serve/comments/`" + ` keyed by inode.

## Commands

### Preview a document or directory

Launch in a new terminal so the current session isn't blocked.

macOS:

` + "```bash" + `
osascript \
  -e 'tell application "Terminal" to activate' \
  -e 'tell application "System Events" to keystroke "t" using command down' \
  -e 'delay 0.3' \
  -e 'tell application "Terminal" to do script "serve <absolute-path>" in front window'
` + "```" + `

Other platforms (or when no terminal is desired):

` + "```bash" + `
serve <absolute-path> --no-open &
` + "```" + `

- Single file: ` + "`serve /abs/path/file.md`" + ` or ` + "`serve /abs/path/page.html`" + `
- Directory: ` + "`serve /abs/path/docs/`" + ` (sidebar handles every file type)
- Always resolve to an absolute path first.

### List comments on a document

` + "```bash" + `
serve comments <file>
` + "```" + `

Returns JSON. Each comment includes:
- ` + "`id`" + ` — unique identifier
- ` + "`anchor_text`" + ` — the highlighted passage
- ` + "`text`" + ` — the comment body
- ` + "`source_line_start`" + ` / ` + "`source_line_end`" + ` — line numbers in the source
- ` + "`resolved`" + ` — boolean
- ` + "`parent_id`" + ` — set on replies

### Resolve comments

` + "```bash" + `
serve resolve <file> <id> [<id>...]
` + "```" + `

Mark comments resolved after addressing them.

### Stream comment changes as JSONL

` + "```bash" + `
serve watch <file>              # one file
serve watch                     # every file in the comment store
serve watch <file> --new        # only new_comment / new_reply events
` + "```" + `

One JSON object per line. Event types: ` + "`initial`" + ` (replays existing unresolved comments at startup), ` + "`new_comment`" + `, ` + "`new_reply`" + `, ` + "`edited`" + `, ` + "`resolved`" + `, ` + "`unresolved`" + `, ` + "`deleted`" + `. Never polls. Park on stdout to react to feedback the moment it lands.

## Workflow: address pending comments

1. Run ` + "`serve comments <file>`" + ` to read the JSON.
2. For each unresolved comment, fix the source at ` + "`source_line_start`" + `–` + "`source_line_end`" + `. The ` + "`anchor_text`" + ` is the exact passage the comment points at.
3. Run ` + "`serve resolve <file> <id>...`" + ` for each comment addressed.
4. Summarize what changed.

## Workflow: react to comments as they arrive

1. Launch ` + "`serve watch <file> --new`" + ` (or pipe it through another process).
2. Each emitted event carries ` + "`comment_id`" + `, ` + "`anchor_text`" + `, ` + "`text`" + `, and source line numbers — everything needed to act.
3. Fix the source, then ` + "`serve resolve <file> <comment-id>`" + `.

## Notes

- **Source files are never modified.** Comments are stored at ` + "`~/.serve/comments/<key>.json`" + `, keyed by the source file's inode and device number on Unix (path hash on Windows). Comments follow files through ` + "`mv`" + ` and ` + "`git mv`" + `.
- The store self-heals across atomic-rewrite saves (VS Code, JetBrains, vim with default settings). Each ` + "`.json`" + ` records its source ` + "`path`" + `, and a missing-store read finds orphans by path and migrates them.
- Live reload via WebSocket — saves reflect in the browser within ~50ms.
- ` + "`serve list`" + ` shows what's currently running; ` + "`serve kill <pid>`" + ` or ` + "`serve kill --all`" + ` stops instances. ` + "`serve home`" + ` opens a browser dashboard at ` + "`http://localhost:7070`" + ` with every running instance and Open/Kill buttons — useful when the user has many docs in flight.
`

const claudeMDSection = `
# Inline Document Comments

The ` + "`serve`" + ` tool lets reviewers leave inline comments on markdown and HTML files via the browser UI (select text → comment). Comments are stored centrally at ` + "`~/.serve/comments/`" + `, keyed by the source file's inode. **Source files are never modified.**

**Read comments on a file:**
` + "```bash" + `
serve comments <file>
` + "```" + `
Returns JSON with each comment's ` + "`id`" + `, ` + "`anchor_text`" + ` (highlighted passage), ` + "`text`" + `, and ` + "`source_line_start`" + `/` + "`source_line_end`" + ` (line numbers in the source).

**Resolve comments after addressing them:**
` + "```bash" + `
serve resolve <file> <comment-id> [<comment-id>...]
` + "```" + `

**Stream comment changes live (no polling):**
` + "```bash" + `
serve watch <file> --new
` + "```" + `
One JSON event per line for each new comment or reply. Park this on stdout when the user wants you to react to feedback as it lands rather than batch-processing on demand.

When asked to address comments on a document, run ` + "`serve comments <file>`" + ` to read them, fix the issues at the indicated source lines, then ` + "`serve resolve`" + ` each one.
`

const claudeMDMarker = "# Inline Document Comments"

func cmdAgentInit() error {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("\nserve agent-init — Set up agent integration")
	fmt.Println(strings.Repeat("─", 44))
	fmt.Println()

	// Select agent (currently only Claude Code)
	fmt.Println("Select agent:")
	fmt.Println("  1. Claude Code")
	fmt.Print("\nChoice [1]: ")
	agentChoice, _ := reader.ReadString('\n')
	agentChoice = strings.TrimSpace(agentChoice)
	if agentChoice != "" && agentChoice != "1" {
		fmt.Println("Invalid choice.")
		return nil
	}
	fmt.Println()

	// Select scope
	fmt.Println("Select scope:")
	fmt.Println("  1. User    — available in all projects (~/.claude/)")
	fmt.Println("  2. Project — this project only (.claude/)")
	fmt.Print("\nChoice [1]: ")
	scopeChoice, _ := reader.ReadString('\n')
	scopeChoice = strings.TrimSpace(scopeChoice)
	fmt.Println()

	var skillPath, claudeMDPath string
	if scopeChoice == "2" {
		cwd, _ := os.Getwd()
		skillPath = filepath.Join(cwd, ".claude", "skills", "serve", "SKILL.md")
		claudeMDPath = filepath.Join(cwd, "CLAUDE.md")
	} else {
		home, _ := os.UserHomeDir()
		skillPath = filepath.Join(home, ".claude", "skills", "serve", "SKILL.md")
		claudeMDPath = filepath.Join(home, ".claude", "CLAUDE.md")
	}

	fmt.Println("Writing files...")

	// Write skill file
	if err := writeSkill(skillPath, reader); err != nil {
		return err
	}

	// Write CLAUDE.md section
	if err := writeClaudeMD(claudeMDPath); err != nil {
		return err
	}

	fmt.Println("\nDone. You can now use /serve in Claude Code.")
	return nil
}

func writeSkill(path string, reader *bufio.Reader) error {
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("  ! Skill file already exists: %s\n    Overwrite? [y/N]: ", path)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(ans)
		if strings.ToLower(ans) != "y" {
			fmt.Println("    Skipped.")
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(skillContent), 0644); err != nil {
		return err
	}
	fmt.Printf("  ✓ Skill file: %s\n", path)
	return nil
}

func writeClaudeMD(path string) error {
	if _, err := os.Stat(path); err == nil {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), claudeMDMarker) {
			fmt.Printf("  ! CLAUDE.md already contains serve instructions: %s\n    Skipped.\n", path)
			return nil
		}
		content := string(data)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += claudeMDSection
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		fmt.Printf("  ✓ Instructions appended to: %s\n", path)
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		content := strings.TrimLeft(claudeMDSection, "\n")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		fmt.Printf("  ✓ Created: %s\n", path)
	}
	return nil
}
