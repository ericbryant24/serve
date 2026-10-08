# serve v2: redesign plan

This is a plan to rebuild serve around the loop it exists for: a person comments on a document in the browser, an agent reads the comments from the CLI, edits the file, replies and resolves. That loop works today, but the parts that carry it (anchoring, replies, the agent CLI, live updates) were added one at a time and don't fit together. The plan keeps what works and replaces the rest in phases, starting with a few fixes worth shipping on the current code.

This is built (October 2026). Section 6 says how it was built and where the build differs from the plan, and section 7 records the decisions it rests on. `CLAUDE.md` describes the code as it is.

## 1. What's wrong today

The evidence comes from reading the code, running a sandboxed copy, and counting over the real comment store (`~/.serve/comments`: 99 documents, 485 comments, 497 replies).

### Addressing a comment usually orphans it

- Of the 242 text and element comments on markdown files that still exist (resolved ones included, since they still display), 148 can't find their block and 22 more find the block but not the quoted phrase. About 70% can't be shown where they were made.
- The main cause is the loop itself. An anchor is the quoted text plus a hash of its block, and addressing the comment changes that text. In a sandbox test, an agent changing "up to three times" to "up to four times", the fix the comment asked for, orphaned the comment. Rewriting the only paragraph under "Non-goals" orphaned that one too, although both neighbouring blocks were unchanged.
- Orphaned comments, resolved ones included, pile up in a yellow "Unanchored Comments" box at the bottom of the page (`static/comment.js:176`).
- There are three separate ways of finding a comment again: Go block fingerprints for markdown (`anchor.go`), a multi-stage text search plus markers spliced into the rendered HTML by string search for everything else (`comment.js:296`, `server.go:1219`), and selector-and-label matching for element comments (`comment.js:638`). Each fails differently.
- Line numbers are recorded when a comment is made and never updated, and `serve comments` reports them as-is. After any edit above a comment the agent gets the wrong lines. The serve skill tells the agent both to "fix the issue in the source file at the indicated lines" and not to trust the lines.

### Replies have no model

- A reply is a comment with a `parent_id`. The browser sends a copy of the parent's anchor with every reply (14 replies in the store carry one), the HTTP API accepts a parent that doesn't exist, replies can be resolved on their own (77 are), and `serve reply` accepts any id, so replying to the latest message nests a reply under a reply.
- The browser only shows a root's direct children (`comment.js:399`). The store holds 9 nested replies that the browser has never displayed.
- Nothing records who wrote a message. Human and agent messages look the same, and neither side can ask which threads are waiting on them.
- Clicking Reply closes the thread and opens a separate floating form labelled "Write a comment..." (or "Comment on the whole page"). Submitting closes everything, so to see your reply you click the highlight again. Messages can't be edited from the UI, delete has no confirm or undo, timestamps never update, and comment text is plain even though agent replies are often lists or code.
- Reply does nothing on unanchored threads (`comment.js:491` handles only resolve and delete).
- A reply that arrives while a thread is open doesn't appear in it.

### The agent loop needs workarounds

- `serve comments` returns a flat list with stale line numbers and no filter for open threads.
- `serve wait` only fires on events after it starts, so the skill has the agent sweep, arm, then sweep again to cover the gap. `serve watch` keeps running after its reader exits, until the next event.
- `serve list` treats `watch`, `wait`, `reply` and `report` processes as servers because they're missing from the subcommand list (`instances.go:34`). On this machine it lists an agent's `serve watch`, running since Sep 24, as an instance, and `serve kill --all` would stop it.

### Storage can lose writes

- Every change loads the whole JSON file, edits it and rewrites it in place (`comments.go:201`). The lock is per process, so the browser and a CLI `serve resolve` writing at the same moment can drop one change, and a reader can see a half-written file.
- Viewing a file with no comments reads every store in the directory looking for an orphan to adopt (`comments.go:404`), twice per page view. Viewing a file with comments can write to its store, which wakes every watcher.
- 34 stores belong to files that have been deleted, and nothing cleans them up.

### Any website can write your files

Verified against a sandboxed server:

- A cross-origin `POST /api/edit` with `Content-Type: text/plain`, which browsers send without a preflight, overwrote the served file. `POST /api/reroot` from a foreign origin also succeeded, so a page can climb to `/` and then rewrite any `.md` or `.txt` file you own, including `CLAUDE.md` files and skills that agents read.
- There's no `Host` check, so DNS rebinding can read anything served, and the WebSocket accepts any origin (`server.go:27`).
- `serve home` sends SIGTERM to any PID you own when any website asks (`home.go:117`). It never checks the PID belongs to serve.
- Raw HTML files run on the same origin as the API, so an untrusted HTML file you open has the same powers.

### One process per folder, and everything reloads everything

- Each `serve` is its own server on the next free port from 8000. This machine has 7 running, some for over a week. Discovery parses `ps` and `lsof`, and URLs change between runs.
- Every instance watches the shared comment directory. A comment on any file, in any instance, makes every open markdown tab re-download its page and swap the content (`templates.go:163`). Any file change in a folder reloads every tab in that folder, whatever it shows.
- Each page carries about 170 KB of inlined script and CSS plus the whole file tree, refetched on every live reload. The tree is built eagerly, so "up" into a large folder walks every file under it.
- Starting serve writes a `.serveignore` into the folder (`server.go:961`).

### The browser UI is assembled from separate scripts

- The comment, vim, sidebar, edit, zoom, report and reload scripts talk through `window.__...` globals and key-handler ordering (comment shortcuts bind on `window` so vim's handler on `document` runs first). HTML is built by string concatenation with inline styles.
- Up to eight floating controls (count badge, page comment, comment mode, edit, report, zoom, vim, present) position themselves independently, with CSS rules to step around each other.
- No dark mode. Mermaid loads from a CDN on every page, so diagrams fail offline.
- Code, plain text, PDF and image pages can't be commented on.
- On raw HTML pages serve injects its CSS into your document and rewrites your markup to add `data-source-lines`.
- The editor is a textarea with no conflict check. Saving overwrites whatever the agent wrote in the meantime, and Escape discards your edits without asking (`static/edit.js:155`).

### Rendering differs from GitHub

Single newlines become `<br>` (`renderer.go:672`), Typographer rewrites quotes and dashes (which is also why anchoring needs a normalisation step), duplicate headings get the same id, and footnotes and `> [!NOTE]` alerts aren't supported.

### Some features cost more than they return

The bug-report feature (structural screenshots, redaction, GitHub device flow, token storage, loopback-guarded routes) is about 4,600 lines with its tests, 22% of the codebase. GitHub has no API for attachments, so filing still ends with dragging files into the issue by hand.

## 2. What stays

- Go, a single binary, goldmark and Chroma.
- Comments live outside the repo, and commenting never modifies a source file.
- The CLI is the agent interface and works with no server running.
- Comments follow a file through `mv`, `git mv` and editors' atomic saves.
- Page comments, element comments, Marp and the Finder Quick Action.

## 3. The redesign

### 3.1 One background server with path URLs

`serve docs/spec.md` starts a background server if none is running (default port 7070, recorded in `~/.serve/daemon.json`), adds the folder to the folders you've opened, and opens `http://localhost:7070/~/Projects/serve/docs/spec.md`. The URL is the file's path, so it survives restarts and can be bookmarked, and going up a folder is just the parent URL. The server only serves inside folders you've opened. Going above one is an explicit click that adds the parent.

- `http://localhost:7070/` replaces `serve home`: recent folders, recently commented documents, and every thread waiting on you.
- `serve status` and `serve stop` replace `list` and `kill`, which stay as aliases for one release.
- The CLI compares versions with the running server and restarts it after an upgrade. Open tabs reconnect by themselves.
- Nothing is written into served folders. `.serveignore` is honoured when present, the defaults are built in, and `.gitignore` can be honoured as an option.

The cost is a long-lived process with version skew and crashes to handle. In return, port hunting, process discovery and per-instance watchers all go away.

### 3.2 Storage: one SQLite database

`~/.serve/serve.db`, using the pure-Go `modernc.org/sqlite` in WAL mode so cross-compiling stays cgo-free.

| Table | Holds |
|---|---|
| `documents` | id, current path, device and inode, content hash, last seen |
| `revisions` | document text by hash, kept only while an anchor refers to it |
| `threads` | id, document, anchor, status, who created and resolved it and when |
| `messages` | id, thread, author, body (markdown), created, edited |
| `events` | sequence number, document, thread, message, type, author, time |

Transactions stop lost updates between the browser, the CLI and agents. Every change gets an event with a sequence number, which the CLI uses as a cursor (3.5). The server picks up changes made by the CLI by polling the newest sequence number four times a second and reading the events past the last one it saw. `serve export <file>` prints a document's threads as JSON for anyone who wants plain files.

Matching a file to its document: the same path and inode is a match. The same inode at a new path is a move. The same path with a new inode is an atomic save or a branch switch. A new file whose content closely matches a document whose file disappeared gets a prompt in the UI ("Comments from `old/path.md`?") instead of being adopted silently. `serve gc` lists documents whose files are gone.

### 3.3 Anchoring: source ranges carried through diffs

An anchor is a range in the file's source text, stored with the revision it belongs to. When the file changes, serve diffs that revision against the current text and carries the range through the diff, the way an editor keeps your cursor in place while text changes around it. Having both versions, serve knows which characters survived and has no need to search for the quote.

Each anchor stores:

- `rev`: hash of the revision the range refers to
- `start` and `end`: byte offsets in that revision
- `quote`, plus about 32 characters of `prefix` and `suffix`
- `section`: the heading path, such as `Payment retry spec › Non-goals`, for display and as a last resort

Carrying a range through a word-level diff (`github.com/sergi/go-diff` with semantic cleanup) has three outcomes:

| Outcome | When | What you see |
|---|---|---|
| Moved | Every character in the range survived | The highlight in its new place, nothing else |
| Changed | Part or all of the range was rewritten | A highlight on the new text tagged "edited"; the thread shows the old quote and the new text |
| Deleted | The range was removed and nothing replaced it | A marker in the margin where the text was |

Two guards keep the outcomes honest. Before calling a range deleted, serve looks for the exact quote with its context elsewhere in the file, which catches a section that was moved rather than removed. A changed range is clamped to the block where the replacement starts, so rewriting a whole section doesn't stretch one comment across all of it.

The three-to-four fix lands in "changed": the comment stays on its sentence and shows ~~up to three times~~ → up to four times. So does the rewritten Non-goals paragraph. Resolved threads are hidden from the page by default, so addressed comments stop cluttering it, and nothing is ever appended to the bottom of the page.

The mapping is deterministic, so its result is written back (new `rev`, `start`, `end`) and the old revision is dropped once no anchor uses it. Stored text is capped at 2 MB per document. Past the cap, and for comments imported from the old store, serve falls back to quote-and-context fuzzy matching near the expected position (go-diff's Bitap matcher), then to today's block fingerprint, then to the section. A comment that gets through all of those unplaced is listed in the comment panel under "Couldn't place", with its section.

**Highlights are placed by offset.** While rendering, the server records for each block that holds text which stretch of its rendered text came from which source bytes. It sends each thread's range as (block, start, end) in the block's rendered text, and the browser wraps exactly those characters in `<mark>`, with no text search. For a new selection the browser sends the block and offsets it starts and ends in, and the server converts them to source bytes with the same table. With Typographer off the table is nearly 1:1; entities, escapes and line breaks are the cases it exists for. All the mapping lives in Go.

**Every text format uses the same engine.** Code and plain text are their own source, so ranges map directly, and comments become available on them, including clicking line numbers to comment on lines. Static HTML gets the same treatment by tokenising the source with offsets. A page that builds its DOM in JavaScript has no source to map, so its comments use quote-and-context matching in the page. Element comments anchor to the element's source range on markdown and static HTML and move with the same diffs. Page comments have no anchor.

**The agent gets current line numbers.** `serve comments` reports where each anchor is now, the text there now, and the text when the comment was made.

The engine is a pure Go package tested with tables of (old document, new document, anchor, expected outcome). The sandbox scenarios above, and a replay of the 303 real comments against their files' current text, become test cases.

### 3.4 Threads

A thread has a status (open or resolved) and an ordered list of messages. There are no replies to replies. `serve reply` accepts a thread id or the id of any message in it and appends to the thread. Only threads can be resolved.

Every message has an author, `{kind: "human" | "agent", name}`. The browser posts as the human, named in `~/.serve/config.json` (default: your OS user name). The CLI posts as the agent, named by `SERVE_AUTHOR` (default `agent`), and `--as` overrides it. Each thread then has `awaiting`: whichever side didn't write the last message. That one field answers "what needs me?" for both sides.

Ids are 8 base32 characters, and any unique prefix works, as in git.

### 3.5 Agent interface

`serve comments <file>` lists open threads (`--all` includes resolved, `--awaiting agent` or `--awaiting human` filters) and prints a `cursor`. The JSON keeps the field names agents already use and makes them accurate:

```json
{
  "file": "/Users/you/Projects/payments/docs/spec.md",
  "cursor": 1842,
  "threads": [{
    "id": "a1f3k9qe",
    "status": "open",
    "awaiting": "agent",
    "anchor_text": "up to three times",
    "source_line_start": 9,
    "source_line_end": 9,
    "location": { "state": "changed", "section": "Payment retry spec › Goals", "current_text": "up to four times over seven days" },
    "messages": [
      { "id": "m2c8q1vd", "author": { "kind": "human", "name": "Eric" }, "text": "Why three? Stripe recommends four.", "created_at": "2026-10-08T14:07:25Z" }
    ]
  }]
}
```

`--format text` prints the same information compactly for reading in a terminal.

- `serve reply <file> <id> <text>`, and `serve resolve <file> <id> [--note <text>]`, which replies and resolves in one step since that's the most common agent action.
- `serve reopen`, `serve edit` and `serve delete` round out the set.
- `serve wait <file> --since <cursor>` returns at once if a matching event happened after the cursor and otherwise blocks for the next one. The skill's sweep, arm, sweep-again routine becomes: list, work, then wait from the cursor you listed at. `--from human` ignores the agent's own messages.
- `serve watch` keeps its event names (`new_comment`, `new_reply`, `edited`, `resolved`, `unresolved`, `deleted`), adds `thread_id`, `author` and the current location, accepts `--since`, and exits as soon as its reader goes away.
- `serve inbox` lists threads awaiting the agent across every document.
- Every command rejects unknown flags and takes `--json`.

### 3.6 Browser UI

One app, written in TypeScript with Preact, bundled at `go generate` time through esbuild's Go API (so building serve needs no Node) and served as one cached file. Preact is there for the parts that change while you use them: a message arrives in an open thread, a file's thread count goes up, a thread resolves. It re-renders those from state and leaves everything else alone, so a reply box you're typing in keeps its focus and draft. Doing that by hand is what grew `comment.js` to 960 lines, and why an open thread today doesn't show a reply that arrives. The document itself is server-rendered HTML that Preact never touches.

- **Layout.** The file tree on the left, with a filter box (`/`) and an open-thread count next to each file. The document in the middle. Threads in a right-hand margin, each level with its highlight and stacked when they'd overlap, as in Google Docs; narrow windows fold the margin into a panel. A top bar with the path as a breadcrumb (each segment links to its folder), the file's dates, and Edit, Comment, Present and a view menu (width, text size, theme). The floating buttons go.
- **Threads.** The quote (old and new text when it changed), then messages with an author chip ("You", "Claude"), relative times that stay current, and markdown bodies. Every open thread ends in a reply box that stays open after sending. Resolve acts on the thread; edit and delete (with undo) are in a menu on each message. Resolved threads collapse to one line and are hidden from the page unless "Show resolved" is on.
- **Commenting.** One comment mode, switched on with `c` or the Comment button and off with Esc. In it, the block or element under the pointer is outlined. Clicking comments on that element; dragging selects text and comments on the selection. The mode's bar has a "Whole page" button for page comments. Links and the page's own buttons don't fire while the mode is on: clicks are swallowed, while the mouse-down that starts a selection is kept from the page's handlers but not cancelled, so text still selects. Outside the mode the page reads and copies normally, with no button popping up on every selection, and selecting text then pressing `c` goes straight to a comment on it. The composer opens in the margin with the quote or element shown, and unsent drafts are kept per anchor.
- **Live updates.** Server-sent events per open document, resumable with `Last-Event-ID`. A file change sends only the blocks whose hash changed. The browser swaps those blocks, keeps the block at the top of the window where it was, and leaves rendered Mermaid diagrams alone when their source didn't change. Changed blocks get a brief tint, so you see what the agent just edited, and "Changes since my last comment" shows the diff inline using the stored revisions.
- **Keyboard.** One command registry instead of separate scripts competing for key events: `c` comment, Esc leave comment mode, `]` and `[` for next and previous thread, `r` reply, `e` resolve, `/` filter files, `?` help.
- **Look.** Theme variables, dark mode that follows the system with a toggle, GitHub-like typography, and a print stylesheet without the chrome. Mermaid is bundled and loaded only on pages with diagrams.
- Each thread has a link (`#thread-a1f3`).

### 3.7 Rendering

- Markdown matches GitHub: GFM plus footnotes, alerts, task lists and GitHub's heading ids (de-duplicated). Hard wraps and Typographer are off by default and can be turned on in config. Frontmatter shows as a collapsible table.
- Raw HTML inside markdown is sanitised (scripts and event handlers removed), with a per-folder opt-out.
- Raw HTML files render in a sandboxed iframe on a second origin that has no API token. A small script inside draws highlights and reports selections to the app by `postMessage`. serve stops modifying and restyling your HTML.
- PDFs and images keep their viewers and get page comments.
- Marp stays as it is.

### 3.8 Editing

CodeMirror 6 with markdown highlighting, next to a live preview from the same renderer. A save carries the revision it started from. If the file changed in the meantime, serve shows both versions and lets you keep yours, take theirs, or merge. Writes are atomic. Escape never discards, and closing with unsaved changes asks first. Anchors follow your edits through the same diff mapping.

### 3.9 Security

- Requests must carry a `Host` of `localhost`, `127.0.0.1`, `[::1]` or the configured host, which blocks DNS rebinding.
- Every state-changing request needs an `X-Serve-Token` header holding a per-server secret that the page gets in a meta tag. A custom header forces a CORS preflight, which serve refuses for other origins. `Origin` is checked as well, on event streams too.
- Raw HTML runs on its own origin without the token (3.7).
- Binding to a non-loopback host requires a share token in the URL and turns off editing and going up a folder.
- No endpoint kills processes. The server only stops itself.

### 3.10 What gets smaller

- **Reports** stay on your machine. Capture, write-time redaction and the review step (choosing what's included) stay. An Export button in the report dialog, and `serve report export <id>`, write one zip (`report.md`, the screenshot, the log) for people to send you. GitHub filing goes: the device flow, token storage, upload policy and filing routes, about 1,300 lines with their tests.
- **Vim mode** goes. Its main use, commenting from the keyboard, is covered by `c` and comment mode.
- **`--data-url`** becomes `serve export --html <file>`, which writes a self-contained HTML file. A data URL stays available as a flag.
- **`serve home`** becomes the server's start page.

## 4. Code layout

```
main.go             entry point (kept at the root so `go install .` and release builds are unchanged)
internal/cli/       subcommands
internal/store/     SQLite schema, documents, threads, events
internal/anchor/    range mapping and fallback matching (pure, no I/O)
internal/render/    markdown, code, HTML tokenising, source maps, sanitising
internal/comments/  placing threads in the current file, and the one-time import of ~/.serve/comments
internal/server/    HTTP, auth, event streams, file watching
internal/daemon/    finding, starting and stopping the background server
web/                TypeScript + Preact app, bundled into web/dist and embedded
```

## 5. Moving existing data

On first run v2 imports `~/.serve/comments/*.json`:

- Each root comment becomes a thread. Its anchor is placed against the file's current text with the fallback chain. When that works it's stored as a range on the current revision; otherwise it's kept as an imported anchor and listed under "Couldn't place".
- Replies at any depth become messages in their root's thread, in time order. Resolved flags on replies are dropped. A reply whose parent is missing becomes a page comment.
- Authors are worked out from how each message was made: the old browser sent its anchor (or a page scope) with every reply and `serve reply` sent neither, so roots and browser replies are the person, and the rest are the agent.
- The JSON files are left as they are, as a backup, and an import marker prevents a second import.

The skill (`~/.claude/skills/serve/SKILL.md`), `README.md`, `CLAUDE.md` and the comment section of the global `~/.claude/CLAUDE.md` change in the same phase as the CLI. Since the field names agents read today are kept, older prompts still mostly work.

## 6. Build order

The plan was to ship each phase on its own. All five were built in one go instead, so Phase 1's adapter (letting the old browser UI run on the new store) was never needed and was skipped. Phase 0 shipped first, on the old code, as planned.

**Phase 0: fixes on the current code** (6 changes)

1. `Host` and `Origin` checks on every API route and the WebSocket; require `application/json` bodies.
2. `serve home` only kills PIDs it lists as serve instances.
3. Add `watch`, `wait`, `reply` and `report` to the subcommand list.
4. Show replies at any depth in the browser, and make Reply work on unanchored threads.
5. Leave resolved comments out of the "Unanchored Comments" box.
6. Write comment stores atomically (temp file and rename) under a file lock.

**Phase 1: core** (store, anchoring, threads, CLI)

The new `store`, `anchor` and `importv1` packages, with the CLI moved onto them: authors, `awaiting`, cursors and current line numbers. The current server's comment API moves onto the new store through an adapter that returns the old JSON shape (threads flattened, current quote as `anchor_text`), so today's browser UI keeps working and places comments correctly.

**Phase 2: browser app**

The app shell, comment margin and threads, offset-placed highlights and selection mapping, event streams with block-level updates, the keyboard registry, comment mode with text selection, dark mode, and the CodeMirror editor with conflict handling.

**Phase 3: background server**

One server, path URLs, the opened-folder list, the start page with the inbox, `status` and `stop`, and the token and share mode.

**Phase 4: rendering and the rest**

GitHub-matching markdown, sanitising, isolated HTML, comments on code and text, local reports with zip export, and `export`.

Testing: unit tests for every package (anchoring scenarios, renderer offsets checked against a browser parser, store concurrency, server security), and 59 integration and Playwright tests against the built binary. A dry run of the import on a copy of the real store brought in 494 threads and 995 messages; 98 imported comments land on their text, and 154, whose text had already gone before the import (most of them resolved), are listed as couldn't place.

## 7. Decisions

1. One background server with path URLs, replacing one process per folder (3.1).
2. SQLite for storage, replacing per-document JSON files (3.2).
3. Reports stay local and export as a zip; no GitHub filing (3.10).
4. Preact for the app around the document (3.6).
5. Hard wraps and Typographer off by default, to match GitHub (3.7). Documents that rely on a single newline becoming a line break render differently.
6. HTML inside markdown is sanitised by default, with a per-folder opt-out (3.7).
7. Vim mode goes, and commenting is one comment mode that handles elements, text selections and the whole page (3.6).
