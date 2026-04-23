# serve

Markdown/HTML document server with live reload and inline comments.

## Architecture

```
src/serve/
  cli.py        — CLI entry point, subcommand dispatch (comments, resolve, agent-init)
  agent_init.py — Interactive agent integration setup wizard
  server.py     — aiohttp server: page, WebSocket, static files, comment API
  renderer.py   — markdown-it-py rendering with source line annotations
  templates.py  — HTML template, comment CSS/JS, reload script
  comments.py   — Comment model, document ID management, JSON persistence
  watcher.py    — File watcher for live reload
  dataurl.py    — Self-contained data URL generation
```

## Key Concepts

- **Document ID**: Each commented document gets a `comment-id` embedded in the file (YAML frontmatter for .md, meta tag for .html). This ties comments to the document regardless of file path.
- **Comment storage**: `~/.serve/comments/<doc-id>.json` — central location, not alongside documents.
- **Source line annotations**: The markdown renderer adds `data-source-lines` attributes to block elements so the browser JS can map text selections back to source line numbers.
- **Frontmatter stripping**: `renderer.py` strips YAML frontmatter before parsing, replacing with blank lines to preserve line numbering.
- **Directory mode**: When given a directory, the server uses a catch-all route to render files by type (markdown, HTML, code, PDF, plain text) and injects a sidebar for navigation. The sidebar state (expand/collapse, visibility) is persisted in localStorage. Comments work per-file via a `?file=` query param on the API.

## Comment API

When the server is running:
- `GET /api/comments` — list all comments
- `POST /api/comments` — create (fields: `text`, `anchor_text`, `block_text`, `source_line_start`, `source_line_end`, `parent_id`)
- `PATCH /api/comments/{id}` — update (`text`, `resolved`)
- `DELETE /api/comments/{id}` — delete (cascades to replies)

CLI (no server needed):
- `serve comments <file>` — list comments as JSON
- `serve resolve <file> <id>...` — mark comments resolved

## Commands

```bash
uv run serve file.md          # serve a single file
uv run serve .                # serve a directory (sidebar + all file types)
uv run serve comments file.md # list comments
uv run serve resolve file.md <id> # resolve comment
uv run serve agent-init       # set up agent integration (Claude Code)
```

## Keeping docs in sync

When CLI commands, flags, or behavior change, update all of these:

1. **Argparse help text** in `cli.py`
2. **README.md** usage section
3. **Skill file** at `~/.claude/skills/serve/SKILL.md`
4. **This file** (Comment API / Commands sections above)

Reinstall after changes: `uv tool install --force -e .`
