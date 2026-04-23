"""Bootstrap agent integration for the serve tool."""

import sys
from pathlib import Path

_AGENTS = [
    {"key": "claude", "name": "Claude Code"},
]

_SKILL_CONTENT = """\
---
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

Open `serve` in a new Terminal tab so it doesn't block the current session:

```bash
osascript \\
  -e 'tell application "Terminal" to activate' \\
  -e 'tell application "System Events" to keystroke "t" using command down' \\
  -e 'delay 0.3' \\
  -e 'tell application "Terminal" to do script "serve <absolute-path>" in front window'
```

- Single file: `serve /path/to/file.md` or `serve /path/to/page.html`
- Directory: `serve /path/to/docs/` (sidebar navigation for all files)
- Always resolve to an absolute path before launching.

### Check comments on a document

```bash
serve comments <file>
```

Returns JSON with all inline comments. Each comment includes:
- `anchor_text` — the highlighted text in the document
- `source_line_start` / `source_line_end` — line numbers in the source file
- `text` — the comment body
- `id` — unique comment identifier
- `resolved` — whether the comment has been resolved

### Resolve comments

```bash
serve resolve <file> <comment-id> [<comment-id>...]
```

Marks comments as resolved after addressing them.

## Workflow: Addressing Comments

When asked to check or address comments on a document:

1. Run `serve comments <file>` to read all comments
2. For each unresolved comment, fix the issue in the source file at the indicated lines
3. Run `serve resolve <file> <id>...` for each addressed comment
4. Summarize what was changed

## Notes

- Comments are stored at `~/.serve/comments/`, keyed by a `comment-id` embedded in each file's frontmatter (markdown) or meta tag (HTML).
- The server supports live reload — edits to the file are reflected in the browser immediately.
- Directory mode renders markdown, HTML, code (syntax-highlighted), PDFs, and plain text.
"""

_CLAUDE_MD_SECTION = """
# Inline Document Comments

The `serve` tool supports inline comments on markdown and HTML files. Comments are added via the browser UI (select text → comment) and stored centrally at `~/.serve/comments/`. Each commented file has a `comment-id` in its frontmatter (markdown) or meta tag (HTML).

**To read comments on a file:**
```bash
serve comments <file>
```
This outputs JSON with all comments, including `anchor_text` (the highlighted text), `source_line_start`/`source_line_end` (line numbers in the source file), and the comment `text`.

**To resolve comments after addressing them:**
```bash
serve resolve <file> <comment-id> [<comment-id>...]
```

When asked to check or address comments on a document, use `serve comments <file>` to read them, then fix the issues in the source file, then `serve resolve` each addressed comment.
"""

_CLAUDE_MD_MARKER = "# Inline Document Comments"


def _prompt_choice(prompt: str, options: list[str], default: int = 1) -> int:
    """Display a numbered menu and return the 1-based selection."""
    while True:
        print(prompt)
        for i, opt in enumerate(options, 1):
            print(f"  {i}. {opt}")
        raw = input(f"\nChoice [{default}]: ").strip()
        if not raw:
            return default
        try:
            choice = int(raw)
            if 1 <= choice <= len(options):
                return choice
        except ValueError:
            pass
        print("Invalid choice, try again.\n")


def _write_skill(path: Path) -> bool:
    """Write the skill file. Returns True if written, False if skipped."""
    if path.exists():
        answer = input(
            f"  ! Skill file already exists: {path}\n    Overwrite? [y/N]: "
        ).strip()
        if answer.lower() != "y":
            print("    Skipped.")
            return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(_SKILL_CONTENT)
    print(f"  \u2713 Skill file: {path}")
    return True


def _write_claude_md(path: Path) -> bool:
    """Append instructions to CLAUDE.md. Returns True if written."""
    if path.exists():
        content = path.read_text()
        if _CLAUDE_MD_MARKER in content:
            print(f"  ! CLAUDE.md already contains serve instructions: {path}")
            print("    Skipped.")
            return False
        if not content.endswith("\n"):
            content += "\n"
        content += _CLAUDE_MD_SECTION
        path.write_text(content)
        print(f"  \u2713 Instructions appended to: {path}")
    else:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(_CLAUDE_MD_SECTION.lstrip("\n"))
        print(f"  \u2713 Created: {path}")
    return True


def cmd_agent_init(argv: list[str]) -> None:
    """Interactive setup wizard for agent integration."""
    try:
        print("\nserve agent-init \u2014 Set up agent integration")
        print("\u2500" * 44 + "\n")

        # Select agent
        agent_idx = _prompt_choice(
            "Select agent:", [a["name"] for a in _AGENTS]
        )
        agent = _AGENTS[agent_idx - 1]
        print()

        # Select scope
        scope_idx = _prompt_choice(
            "Select scope:",
            [
                "User    \u2014 available in all projects (~/.claude/)",
                "Project \u2014 this project only (.claude/)",
            ],
        )
        scope = "user" if scope_idx == 1 else "project"
        print()

        # Resolve paths
        if scope == "user":
            skill_path = Path.home() / ".claude" / "skills" / "serve" / "SKILL.md"
            claude_md_path = Path.home() / ".claude" / "CLAUDE.md"
        else:
            cwd = Path.cwd()
            skill_path = cwd / ".claude" / "skills" / "serve" / "SKILL.md"
            claude_md_path = cwd / "CLAUDE.md"

        # Write files
        print("Writing files...")
        _write_skill(skill_path)

        if agent["key"] == "claude":
            _write_claude_md(claude_md_path)

        print("\nDone. You can now use /serve in Claude Code.")

    except KeyboardInterrupt:
        print("\nAborted.")
        sys.exit(130)
    except OSError as e:
        print(f"\nError writing files: {e}", file=sys.stderr)
        sys.exit(1)
