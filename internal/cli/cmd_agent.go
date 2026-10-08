package cli

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill.md
var skillContent string

const claudeMDMarker = "# Inline Document Comments"

const claudeMDSection = `# Inline Document Comments

The ` + "`serve`" + ` tool shows documents in the browser, where people leave inline comments. Comments live in serve's database (` + "`~/.serve/serve.db`" + `); **source files are never modified by commenting.**

- ` + "`serve comments <file> --awaiting agent`" + ` lists threads waiting for you, as JSON. ` + "`source_line_start`" + `/` + "`source_line_end`" + ` are where each passage is now; ` + "`location.state`" + ` says whether it is intact (` + "`ok`" + `), rewritten (` + "`changed`" + `), or gone (` + "`deleted`" + `).
- ` + "`serve reply <file> <id> <text>`" + ` adds to a thread; ` + "`serve resolve <file> <id> --note <text>`" + ` replies and resolves in one step.
- ` + "`serve wait <file> --since <cursor> --from human`" + ` blocks until the next comment after a listing's cursor, prints it and exits; run it in the background to react to feedback as it lands.

When asked to address comments on a document, run ` + "`serve comments <file>`" + `, fix each passage at its lines, then ` + "`serve resolve <file> <id> --note`" + ` with what you did.
`

const agentUsage = `Usage: serve agent-init [--user|--project] [--yes]

Install the serve skill for Claude Code, and a short section about serve in
CLAUDE.md. --user (the default) writes to ~/.claude/, --project to ./.claude/
and ./CLAUDE.md. Re-run after upgrading serve to refresh the skill.`

func cmdAgentInit(args []string) error {
	fs := flags("agent-init", agentUsage)
	project := fs.Bool("project", false, "")
	fs.Bool("user", true, "")
	yes := fs.Bool("yes", false, "")
	if _, err := parse(fs, agentUsage, args); err != nil {
		return err
	}
	var skillPath, mdPath string
	if *project {
		cwd, _ := os.Getwd()
		skillPath = filepath.Join(cwd, ".claude", "skills", "serve", "SKILL.md")
		mdPath = filepath.Join(cwd, "CLAUDE.md")
	} else {
		home, _ := os.UserHomeDir()
		skillPath = filepath.Join(home, ".claude", "skills", "serve", "SKILL.md")
		mdPath = filepath.Join(home, ".claude", "CLAUDE.md")
	}
	in := bufio.NewReader(os.Stdin)
	if _, err := os.Stat(skillPath); err == nil && !*yes {
		fmt.Printf("Replace the existing skill at %s? [y/N] ", skillPath)
		a, _ := in.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(a)) != "y" {
			fmt.Println("Left the skill as it is.")
			skillPath = ""
		}
	}
	if skillPath != "" {
		if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(skillPath, []byte(skillContent), 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote", skillPath)
	}
	if err := writeClaudeMD(mdPath); err != nil {
		return err
	}
	fmt.Println("Done. Claude Code now knows how to work with serve's comments.")
	return nil
}

// writeClaudeMD adds the serve section to CLAUDE.md, or replaces the one an
// earlier serve wrote (from its heading to the next top-level heading).
func writeClaudeMD(path string) error {
	data, err := os.ReadFile(path)
	content := string(data)
	if err != nil {
		content = ""
	}
	if i := strings.Index(content, claudeMDMarker); i >= 0 {
		rest := content[i+len(claudeMDMarker):]
		end := len(content)
		if j := strings.Index(rest, "\n# "); j >= 0 {
			end = i + len(claudeMDMarker) + j + 1
		}
		content = content[:i] + claudeMDSection + "\n" + strings.TrimLeft(content[end:], "\n")
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if content != "" {
			content += "\n"
		}
		content += claudeMDSection
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("Updated", path)
	return nil
}
