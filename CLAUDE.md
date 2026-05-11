# serve

Markdown/HTML document server with live reload and inline comments.

## Architecture

```
main.go           — CLI entry point, subcommand dispatch (comments, resolve, list, kill, agent-init)
server.go         — net/http server: page, WebSocket, static files, comment API
renderer.go       — goldmark rendering with source line annotations, Chroma syntax highlighting
templates.go      — HTML template, comment CSS/JS, reload script
comments.go       — Comment model, document ID management, JSON persistence
watcher.go        — fsnotify file watcher with trailing-edge debounce (50ms)
instances.go      — Process discovery via ps/lsof (no registry)
marp.go           — Marp slide deck support
dataurl.go        — Self-contained data URL generation
agent_init.go     — Interactive agent integration setup wizard
comments_test.go  — Unit tests for comment store, document ID, watcher filter
```

## Key Concepts

- **Document ID**: Each commented document gets a `comment-id` embedded in the file (YAML frontmatter for .md, meta tag for .html). This ties comments to the document regardless of file path.
- **Comment storage**: `~/.serve/comments/<doc-id>.json` — central location, not alongside documents.
- **Source line annotations**: The renderer adds `data-source-lines` attributes to block elements so the browser JS can map text selections back to source line numbers.
- **Frontmatter stripping**: `renderer.go` strips YAML frontmatter before parsing, replacing with blank lines to preserve line numbering.
- **Directory mode**: When given a directory, the server uses a catch-all route to render files by type (markdown, HTML, code, PDF, plain text) and injects a sidebar for navigation. The sidebar state (expand/collapse, visibility) is persisted in localStorage. Comments work per-file via a `?file=` query param on the API.
- **Watcher debounce**: Trailing-edge 50ms — coalesces rapid bursts (e.g. Claude Code editing multiple files) into a single reload. Ignores `node_modules`, `__pycache__`, `dist`, `build`, `vendor`, `target`.

## Comment API

When the server is running:
- `GET /api/comments` — list all comments
- `POST /api/comments` — create (fields: `text`, `anchor_text`, `block_text`, `source_line_start`, `source_line_end`, `parent_id`)
- `PATCH /api/comments/{id}` — update (`text`, `resolved`)
- `DELETE /api/comments/{id}` — delete (cascades to replies at any depth)

CLI (no server needed):
- `serve comments <file>` — list comments as JSON
- `serve resolve <file> <id>...` — mark comments resolved

## Commands

```bash
serve file.md          # serve a single file
serve .                # serve a directory (sidebar + all file types)
serve comments file.md # list comments
serve resolve file.md <id> # resolve comment
serve agent-init       # set up agent integration (Claude Code)
serve list             # list running instances (also: --json)
serve kill <pid>       # stop one (also: --port N, --all, --force)
```

## Rebuild & Install

```bash
go build -o serve . && go install .
```

Always build both: `./serve` is what the test suite uses; `go install .` updates the binary on your PATH.

## Tests

```bash
# Go unit tests (comment store, document ID, watcher filter)
go test ./...

# Integration tests against the installed binary
uv --directory tests run pytest -v

# Performance benchmarks
uv --directory tests run pytest test_performance.py -s -v
```

## Keeping docs in sync

When CLI commands, flags, or behavior change, update all of these:

1. **Help text** in `main.go` (`printServeUsage`, subcommand usage strings)
2. **README.md** usage section
3. **Skill file** at `~/.claude/skills/serve/SKILL.md`
4. **This file** (Comment API / Commands sections above)

Rebuild after changes: `go build -o serve . && go install .`
