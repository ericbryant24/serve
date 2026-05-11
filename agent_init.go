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
description: Serve markdown/HTML files with live reload and inline comments. Use when the user wants to preview documents, check comments, or resolve feedback.
allowed-tools:
  - Bash(serve *)
  - Bash(open *)
  - Bash(osascript *)
---

# serve — Document Server with Inline Comments

Serve markdown and HTML files with live reload, Mermaid diagrams, and browser-based inline comments.

## Commands

### Preview a document

Open ` + "`serve`" + ` in a new Terminal tab so it doesn't block the current session:

` + "```bash" + `
osascript \
  -e 'tell application "Terminal" to activate' \
  -e 'tell application "System Events" to keystroke "t" using command down' \
  -e 'delay 0.3' \
  -e 'tell application "Terminal" to do script "serve <absolute-path>" in front window'
` + "```" + `

- Single file: ` + "`serve /path/to/file.md`" + ` or ` + "`serve /path/to/page.html`" + `
- Directory: ` + "`serve /path/to/docs/`" + ` (sidebar navigation for all files)
- Always resolve to an absolute path before launching.

### Check comments on a document

` + "```bash" + `
serve comments <file>
` + "```" + `

Returns JSON with all inline comments. Each comment includes:
- ` + "`anchor_text`" + ` — the highlighted text in the document
- ` + "`source_line_start`" + ` / ` + "`source_line_end`" + ` — line numbers in the source file
- ` + "`text`" + ` — the comment body
- ` + "`id`" + ` — unique comment identifier
- ` + "`resolved`" + ` — whether the comment has been resolved

### Resolve comments

` + "```bash" + `
serve resolve <file> <comment-id> [<comment-id>...]
` + "```" + `

Marks comments as resolved after addressing them.

## Workflow: Addressing Comments

When asked to check or address comments on a document:

1. Run ` + "`serve comments <file>`" + ` to read all comments
2. For each unresolved comment, fix the issue in the source file at the indicated lines
3. Run ` + "`serve resolve <file> <id>...`" + ` for each addressed comment
4. Summarize what was changed

## Notes

- Comments are stored at ` + "`~/.serve/comments/`" + `, keyed by a ` + "`comment-id`" + ` embedded in each file's frontmatter (markdown) or meta tag (HTML).
- The server supports live reload — edits to the file are reflected in the browser immediately.
- Directory mode renders markdown, HTML, code (syntax-highlighted), PDFs, and plain text.
`

const claudeMDSection = `
# Inline Document Comments

The ` + "`serve`" + ` tool supports inline comments on markdown and HTML files. Comments are added via the browser UI (select text → comment) and stored centrally at ` + "`~/.serve/comments/`" + `. Each commented file has a ` + "`comment-id`" + ` in its frontmatter (markdown) or meta tag (HTML).

**To read comments on a file:**
` + "```bash" + `
serve comments <file>
` + "```" + `
This outputs JSON with all comments, including ` + "`anchor_text`" + ` (the highlighted text), ` + "`source_line_start`" + `/` + "`source_line_end`" + ` (line numbers in the source file), and the comment ` + "`text`" + `.

**To resolve comments after addressing them:**
` + "```bash" + `
serve resolve <file> <comment-id> [<comment-id>...]
` + "```" + `

When asked to check or address comments on a document, use ` + "`serve comments <file>`" + ` to read them, then fix the issues in the source file, then ` + "`serve resolve`" + ` each addressed comment.
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
