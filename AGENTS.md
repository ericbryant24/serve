# serve

Document previewer with inline review comments that an agent reads and answers from the CLI. One background server per user serves every opened folder; files are at URLs that are their own paths.

## Layout

```
main.go                 — calls cli.Main (version set with -ldflags "-X main.version=…")
internal/cli/           — every command; skill.md is what `serve agent-init` installs
internal/anchor/        — anchors as source ranges, carried through edits by diff (pure, no I/O)
internal/render/        — markdown (goldmark), code (Chroma), HTML body text; leaf keys and text runs
internal/store/         — SQLite (modernc, cgo-free): documents, revisions, threads, messages, events, folders
internal/comments/      — joins the three: places threads in the current file; one-time import of the old JSON store
internal/server/        — the background server: pages, API, event streams, file watching, security checks
internal/daemon/        — how the CLI finds, starts, stops and calls the server (~/.serve/daemon.json)
internal/config/        — ~/.serve/config.json
internal/logx/          — in-memory log ring, redacted as events are written
internal/reports/       — local bug reports, zip export
internal/paths/         — filesystem path ↔ URL path, inode lookup
web/src/                — the browser app (TypeScript + Preact), bundled into web/dist by `go generate ./web`
web/build/              — the bundler (esbuild's Go API; no Node needed to build, only to fetch packages)
tests/                  — integration tests against the built binary (pytest + Playwright)
```

## How it fits together

- **Process model**: `serve <path>` calls `daemon.Ensure`, which starts `serve daemon` detached if no server of this version is running (it replaces an older one), adds the folder with `POST /_serve/api/folders`, and opens the URL. The server writes `~/.serve/daemon.json` (pid, ports, token; mode 0600) and removes it on exit. `serve status` also lists servers left by older versions (serve processes listening on a port that are not the daemon); `serve stop --all` stops them.
- **URLs**: a file's URL path is its absolute path with the home directory as `~` (`/~/Projects/x/doc.md`). `/` is the start page. `/_serve/` is reserved for assets (`/_serve/assets/`), the API (`/_serve/api/`), events (`/_serve/events`) and health.
- **Two ports**: the app port (7070 by default) serves serve's pages and API. The content port (the next free one) serves raw files and HTML pages with `embed.js` injected, never pages or API. HTML files show in an iframe from the content port, so their scripts have a different origin from the app.
- **Opened folders**: the server serves only paths inside a folder in the `folders` table. The sidebar root is the widest opened folder containing the file. A viewer can start the tree at a folder inside it (right-click, **Start the file tree here**); that choice is browser-only (`localStorage`), and ↑ moves the tree up until it reaches the opened folder, where ↑ opens the folder above.
- **Security**: Host must be localhost, an IP literal or the configured host (blocks DNS rebinding). Every `/_serve/api` call needs `X-Serve-Token` (GETs may pass `?token=`, for `EventSource` and `<img>`); the page gets the token in a meta tag. Origin, when sent, must match. Bodies must be JSON or multipart. Requests from another machine need the share token (cookie `serve_share`, set from `?share=`), which allows reading and commenting but not editing, folders, reports or shutdown.
- **Rendering**: every block element gets `data-b` (a key hashed from its kind and source, numbered when repeated). Blocks that hold text directly (paragraph, heading, table cell, tight-list text, code block, line of a code file, balanced HTML block) also get `data-t` and are leaves: `render.Block.Runs` map each stretch of the leaf's rendered text (UTF-16 units, as JavaScript counts) to source bytes. The custom node renderers in `markdown.go` record runs as they write; `render_test.go` checks every leaf's runs against the textContent a browser parser produces. Defaults match GitHub: no hard wraps, no Typographer, GitHub heading ids, alerts, footnotes; raw HTML is filtered (`sanitize.go`) unless the folder is in `raw_html`. Frontmatter is blanked byte for byte before parsing, so offsets stay file offsets.
- **Anchors**: a thread's anchor is `{rev, start, end, quote, prefix, suffix, section}` in source bytes (`anchor.Anchor`). On every read, `comments.Service.Open` maps anchors whose `rev` differs from the file's current hash: `anchor.Map` diffs the stored revision against the current text word by word (go-diff) and carries the range through, giving state ok / changed / deleted; before calling text deleted it looks for the exact quote with its context elsewhere (a moved section), and a changed range is clamped to its block. Without the old revision (imported comments), `anchor.Locate` tries quote and context, then the old store's block fingerprint, then the section; failing all, the anchor is `unplaced`. Results are written back with compare-and-set on `rev`, and revisions no anchor (or the latest human message) refers to are dropped.
- **Placement**: `comments.ThreadView` carries `location` (lines, current source text, rendered text, state: for agents) and `placement` (leaf segments, element key or point: for the browser). The browser wraps `<mark class="cm">` around the segments by walking text nodes, with no text search; it falls back to the segment's text inside that one leaf if the page is stale.
- **Threads**: a thread has a scope (text, element, page), a status, and flat messages. Every message has an author `{kind: human|agent, name}`; `awaiting` is the side that did not write the last one. Ids are 8 characters from a 32-letter alphabet; `Store.ResolveID` accepts any unique prefix of a thread or message id.
- **Events and live updates**: every store change appends to `events` with a sequence number. The CLI's `wait`/`watch --since` read from a cursor. The server polls `MAX(seq)` every 250ms and sends affected tabs a `threads` event. File changes come from fsnotify on the directories open tabs show (ref-counted); the server re-renders and sends a `doc` event with the new top-level blocks (HTML only for keys the tab has not seen) and the threads. The browser swaps changed blocks, keeps the rest (Mermaid output included), keeps the top visible block in place, and tints blocks whose source changed. Files a document loads (`render.Doc.Refs`: images, media, stylesheets, scripts) are watched too, folder by folder per tab; a change sends `asset` with the file's URL, and the browser re-fetches that file under a `?v=` stamp (markdown) or reloads the frame (HTML). Raw files are served `no-cache` with an ETag of exact mtime and size, since `Last-Modified` is only to the second. A tab that reconnects gets `resync`; one whose bundle hash differs from the server's reloads.
- **Element comments**: comment mode (`c` with no selection) outlines the block under the pointer and comments on the one clicked; dragging selects text. On markdown the anchor is the block's source range; on HTML it is the element's text range in the page's body text plus selector, label and sibling text for pages built by script.
- **Body text** (HTML files): text nodes under `<body>` outside script, style, template, noscript and textarea, skipping whitespace-only nodes. `render/htmlfile.go` and `web/src/embed.ts` must agree on this rule.
- **Editing**: `PUT /_serve/api/file` takes `base_rev`; a mismatch is 409 with the current text, and the editor offers merge (go-diff patch of base→mine applied to theirs), take theirs, or overwrite. Writes are atomic (temp file beside the target, renamed over it), so document identity follows the path (same path, new inode = same document).
- **Document identity**: same path and inode → match; same path, new inode → atomic save; same inode at a new path whose old path is gone → move. A new file with no comments is offered threads from a vanished document with the same name or similar text (relink banner). `serve gc` lists documents whose files are gone.
- **Import**: the first process to open the store imports `~/.serve/comments/*.json` in one transaction, without events, marked by `meta.import_v1`. Replies at any depth flatten into their root's thread; a reply's author is human if the browser sent it (it carried an anchor or scope), else agent. The JSON files are left alone.
- **Reports**: captured in the browser (`web/src/report/`), stored under `~/.serve/reports/<id>/`, exported as a zip with only the attachments the reporter included. Nothing is uploaded.

## Comment API

All under `/_serve/api/`, JSON, token required.

- `GET threads?path=` · `POST threads` `{path, scope: text|element|page, text, selection?, element?}` (409 when a selection no longer matches the file) · `POST threads/{id}/messages` `{text}` · `PATCH threads/{id}` `{status: open|resolved}` · `DELETE threads/{id}` · `PATCH messages/{id}` `{text}` · `DELETE messages/{id}` (the first message deletes the thread)
- `GET doc?path=` · `GET tree?path=&client=` · `GET search?root=&q=` · `GET changes?path=` · `POST preview` · `GET file?path=` · `PUT file` `{path, content, base_rev, force}` · `POST merge` · `POST folders` / `DELETE folders` `{path}` · `POST relink` `{from, path}` · `POST reveal` · `GET home` · `GET status` · `POST shutdown`
- `GET reports` · `POST reports` · `GET|PATCH|DELETE reports/{id}` · `POST reports/{id}/attachments` · `GET reports/{id}/attachments/{aid}` · `GET reports/{id}/export` · `POST reports/{id}/reveal`
- `GET /_serve/events?path=&client=&token=` (server-sent events: hello, doc, asset, threads, tree, counts, gone, resync)

## Commands

```bash
serve [path] [--port N] [--host H] [--no-open]   # open (starts the background server)
serve comments <file> [--all] [--awaiting agent|human] [--format json|text]
serve reply <file> <id> <text|-> [--as agent|human[:name]]
serve resolve <file> <id>... [--note <text>]
serve reopen <file> <id>...
serve edit <file> <message-id> <text|->
serve delete <file> <id>
serve wait [file] [--since N] [--new] [--from human|agent] [--timeout S]   # exit 0 / 124 / 130
serve watch [file] [--since N] [--new] [--from human|agent]
serve inbox [--for agent|human] [--json]
serve export <file> [--html] [--data-url] [-o FILE]
serve status [--json] · serve stop [--all] · serve home
serve report [list|show|export|open|rm]
serve gc [--prune]
serve agent-init [--user|--project] [--yes]
serve daemon [--port N] [--host H]               # the server, in the foreground
```

`list`, `ls` and `kill` still work (status / stop; `kill <pid>` only stops an older serve's server).

## Build and install

```bash
go generate ./web        # after changing web/src (needs `npm ci` in web/ once)
go build -o serve . && go install .
```

Always build both: the integration tests run `./serve`; `go install` puts the binary on PATH (`$(go env GOBIN)`, else `$(go env GOPATH)/bin`). `web/dist` is committed so `go install` works without Node.

## Tests

```bash
go test ./...                              # unit tests
cd web && npx tsc --noEmit -p .            # type-check the browser app
uv --directory tests run pytest -q         # integration and browser tests against ./serve
```

Every serve process the integration tests start has its own throwaway HOME and port (`conftest.py`), so the real `~/.serve` is never touched. A test that spawns serve must go through the `cli`/`daemon` fixtures.

## Keeping docs in sync

When commands, flags or behaviour change, update: the usage strings in `internal/cli`, `README.md`, `internal/cli/skill.md` (and `~/.claude/skills/serve/SKILL.md` by running `serve agent-init`), and this file. `AGENTS.md` is a copy of this file for other agents.
