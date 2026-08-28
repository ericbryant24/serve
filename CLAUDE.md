# serve

Markdown/HTML document server with live reload and inline comments.

## Architecture

```
main.go           — CLI entry point, subcommand dispatch (comments, resolve, watch, list, kill, agent-init)
server.go         — net/http server: directory rooting (atomic rootState, re-root), page rendering, WebSocket, comment API
renderer.go       — goldmark rendering with source line annotations, Chroma syntax highlighting
templates.go      — html/template page builder (wrapMarkdown, wrapCode, etc.) + inject helpers
static/page.gohtml — HTML page skeleton (embedded via go:embed)
static/*.css/js   — Comment UI, vim mode, zoom, sidebar, presentation assets (embedded)
comments.go       — Comment model, inode-based store key, JSON persistence (wrapped {path, comments} on disk)
reports.go        — Report model, directory-per-report store under ~/.serve/reports/
redact.go         — Path redaction (shape / extension-only) and credential scanning
logging.go        — In-memory ring of the last 500 events; redaction happens at write time
config.go         — Upload policy (prompt / never) via ~/.serve/config.json or SERVE_REPORT_UPLOAD
github.go         — GitHub device flow, issue creation, token cache at ~/.serve/github.json
server_reports.go — /api/report* routes, loopback guard, review payload
report_cmd.go     — `serve report` subcommand
watcher.go        — fsnotify directory watcher (restartable on re-root), trailing-edge debounce (50ms)
watch.go          — `serve watch` subcommand: JSONL event stream over the comment store
instances.go      — Process discovery via ps/lsof (no registry)
marp.go           — Marp slide deck support
dataurl.go        — Self-contained data URL generation
agent_init.go     — Interactive agent integration setup wizard
comments_test.go  — Unit tests for comment store, store key, watcher filter
templates_test.go — Unit tests for page rendering, XSS escaping, wrap functions
watch_test.go     — Unit tests for serve watch diff logic
```

## Key Concepts

- **Comment storage**: `~/.serve/comments/<key>.json` — central location, never alongside documents. The key is derived from the file's inode+device number on Unix (so comments survive `mv`/`git mv`), falling back to a path hash on Windows. Source files are never modified.
- **Store key**: `storeKeyForFile(path)` in `comments.go` — returns `"%x-%x" % (dev, ino)` on Unix via `fi.Sys().(*syscall.Stat_t)`, or `md5(abs_path)[:4]` as fallback.
- **Source line annotations**: The renderer adds `data-source-lines` attributes to block elements so the browser JS can map text selections back to source line numbers.
- **Frontmatter stripping**: `renderer.go` strips YAML frontmatter before parsing, replacing with blank lines to preserve line numbering.
- **Rooting**: The server always serves a directory. A directory target is used as-is; a file target roots at its parent and renders that file at `/` (its `openPath`). The root is held in an atomic `rootState` (baseDir, dirName, faviconSeed, openPath) so it can be swapped at runtime without locking every handler. The sidebar's "up" control (`POST /api/reroot`) re-roots one level up, re-bases `openPath`, and restarts the directory watcher. baseDir is symlink-resolved so serving under `/tmp` or `/var` (macOS symlinks) doesn't trip the sandbox check. A catch-all route renders files by type (markdown, HTML, code, PDF, plain text) with a sidebar; state (expand/collapse, visibility, width) persists in localStorage. Comments work per-file via a `?file=` query param, falling back to `openPath` when absent.
- **Watcher debounce**: Trailing-edge 50ms — coalesces rapid bursts (e.g. Claude Code editing multiple files) into a single reload. Ignores `node_modules`, `__pycache__`, `dist`, `build`, `vendor`, `target`.

## Reports

- **Storage**: `~/.serve/reports/<id>/` holds `report.json` plus attachment files. A directory per report so attachments are ordinary files the reporter can inspect with their own tools before sending. Mode 0700.
- **Opt-in attachments**: `Attachment.Included` defaults to false in the struct, not in the UI. If the review gate has a bug, the failure mode is an attachment that did not send.
- **Structural capture**: `static/report.js` clones the page, walks the original and clone in lockstep with two `TreeWalker`s, and wraps each text node in a span with transparent text over a solid ground. The glyphs still drive layout, so metrics and wrapping are identical while the words are unreadable. Media is replaced with placeholders, which is needed for correctness too: an SVG loaded into an `<img>` cannot fetch external resources.
- **Redaction is a write-time property**: `logf` field constructors (`fPath`, `fErr`) redact before anything enters the ring buffer, so no later bug can leak a path out of it. Use `fErr` for anything derived from an `error` — Go's `*PathError` embeds an absolute path in `Error()`.
- **Loopback only**: every `/api/report*` route refuses non-loopback requests. A report holds a screenshot of the served document, so on `--host 0.0.0.0` these routes would be a remote screenshot endpoint. `SERVE_REPORT_ALLOW_REMOTE=1` overrides.
- **Single rendering path**: `Report.Markdown()` builds the issue body, and the review gate displays that same call's output, so what the reporter approves is byte-for-byte what gets posted.
- **Filing**: GitHub device flow, no client secret, so `ghClientID` in `github.go` is a plain constant that ships in every build — no `-ldflags`, and a fresh install needs no setup beyond the reporter approving once. The browser dialog starts polling as soon as it shows the code, so approving on github.com advances the screen with no second click. `SERVE_GITHUB_DEVICE_URL`, `SERVE_GITHUB_TOKEN_URL` and `SERVE_GITHUB_API` override the endpoints (the integration suite points them at a stub); `SERVE_GITHUB_CLIENT_ID` overrides the client id. GitHub has no public REST endpoint for issue attachments, so filing posts the body and then reveals the report folder for the files to be dragged in.

## Comment API

When the server is running:
- `GET /api/comments` — list all comments
- `POST /api/comments` — create (fields: `text`, `anchor_text`, `block_text`, `source_line_start`, `source_line_end`, `parent_id`)
- `PATCH /api/comments/{id}` — update (`text`, `resolved`)
- `DELETE /api/comments/{id}` — delete (cascades to replies at any depth)

CLI (no server needed):
- `serve comments <file>` — list comments as JSON
- `serve reply <file> <id> <text>` — reply to a comment (threads under it via `parent_id`)
- `serve resolve <file> <id>...` — mark comments resolved
- `serve watch [file] [--new]` — stream comment-change events as JSONL
- `serve report [show|export|open|file|login|logout|rm]` — bug reports captured from the browser

## Commands

```bash
serve file.md             # serve file.md's directory, opened at file.md
serve .                   # serve a directory (sidebar + all file types)
serve comments file.md    # list comments
serve reply file.md <id> "looks good"  # reply to a comment
serve resolve file.md <id># resolve comment
serve watch file.md       # stream events for one file
serve watch               # stream events for every file in the store
serve watch file.md --new # filter to new_comment/new_reply only
serve report              # list captured bug reports
serve report export <id>  # print the issue markdown (no account, no network)
serve report file <id>    # file it as a GitHub issue
serve agent-init          # set up agent integration (Claude Code)
serve list                # list running instances (also: --json)
serve kill <pid>          # stop one (also: --port N, --all, --force)
serve home                # browser dashboard of all running instances (default port 7070)
```

`serve watch` writes one JSON object per line. Event types: `initial` (one
per existing unresolved comment at startup), `new_comment`, `new_reply`,
`edited`, `resolved`, `unresolved`, `deleted`. The comment store JSON now
includes a `path` field so the all-files watcher can populate `file` on
every event. Legacy bare-array stores still load.

## Rebuild & Install

```bash
go build -o serve . && go install .
```

Always build both: `./serve` is what the test suite uses; `go install .` writes the binary to `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if GOBIN is unset).

The module is named `serve`, so the produced binary is `serve`. Earlier the module was `serve-go`; if you still have a stale `serve` binary on PATH from before the rename, `go install .` may write to a *different* location than the one your shell finds — `which serve` will tell you which path is being run, and you should remove any duplicates so updates land where you expect.

## Tests

```bash
# Go unit tests (comment store, store key, watcher filter, page template)
go test ./...

# Integration tests against the installed binary
uv --directory tests run pytest -v

# Performance benchmarks
uv --directory tests run pytest test_performance.py -s -v
```

Every serve process the suite starts runs with `HOME` pointed at a throwaway
directory (`conftest.child_env`), so the real `~/.serve` is never written to.
This matters because the comment store is keyed by inode: without it a run
leaves comment files behind for temp files that no longer exist, and inode reuse
makes a later run's temp file inherit them — which looks like a document having
comments before the test created any. Any new test that spawns the binary must
pass `env=child_env()`. Set `SERVE_TEST_HOME=/some/path` to keep the directory
for debugging.

## Keeping docs in sync

When CLI commands, flags, or behavior change, update all of these:

1. **Help text** in `main.go` (`printServeUsage`, subcommand usage strings)
2. **README.md** usage section
3. **Skill file** at `~/.claude/skills/serve/SKILL.md`
4. **This file** (Comment API / Commands sections above)

Rebuild after changes: `go build -o serve . && go install .`
