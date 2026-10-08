---
name: serve
description: Preview markdown, HTML and code files in the browser and work through inline review comments on them - read, reply, resolve, and wait for new ones. Use when the user wants to preview a document, check or address feedback on it, or have you react to comments as they arrive.
allowed-tools:
  - Bash(serve *)
---

# serve

serve shows documents in the browser and lets a person comment on them inline. You read the comments from the command line, change the file, reply and resolve. Comments live in serve's database (`~/.serve/serve.db`); commenting never touches the file.

## Open a document

```bash
serve <absolute-path>
```

Opens a file or folder in the browser and returns at once; a background server keeps running. The URL is the file's own path, for example `http://localhost:7070/~/Projects/x/doc.md`.

## Read comments

```bash
serve comments <file>                      # open threads, JSON
serve comments <file> --awaiting agent     # only threads waiting for you
serve comments <file> --format text        # readable summary
```

```json
{
  "file": "/Users/me/docs/spec.md",
  "cursor": 1842,
  "threads": [{
    "id": "a1f3k9qe",
    "status": "open",
    "awaiting": "agent",
    "scope": "text",
    "anchor_text": "up to three times",
    "text": "Why three? Stripe recommends four.",
    "source_line_start": 9,
    "source_line_end": 9,
    "location": { "state": "ok", "current_text": "up to three times", "section": "Spec › Goals" },
    "messages": [{ "id": "m2c8q1vd", "author": { "kind": "human", "name": "Eric" }, "text": "Why three? Stripe recommends four.", "created_at": "…" }]
  }]
}
```

- `source_line_start`/`source_line_end` are where the passage is **now**. They follow it through every edit, yours included, so trust them.
- `location.state`:
  - `ok`: the commented text is intact.
  - `changed`: it was rewritten since the comment (often by you, addressing it). `current_text` is what is there now; `anchor_text` is what the person commented on.
  - `deleted`: the text is gone; the lines say where it was.
  - `unplaced`: serve could not tell where it belongs; `section` says where it was.
  - `page`: the comment is about the whole document (`scope: "page"`), so there are no lines.
- `scope: "element"` means the person pointed at a whole block (a heading, a table, a diagram); the lines cover the block and `element.label` describes it.
- `awaiting: "agent"` means the last message is from the person: it is your turn. `awaiting: "human"` means you have replied and are waiting for them.
- `--all` includes resolved threads.

## Reply and resolve

```bash
serve reply <file> <id> "text"                   # add to the thread ("-" reads stdin)
serve resolve <file> <id>... --note "what I did" # reply with the note, then resolve
serve reopen <file> <id>
```

`<id>` is a thread id, any message id in it, or a unique prefix of either; a reply always goes at the end of the thread. After addressing a comment, resolve it with `--note` so the person sees what changed. If you need an answer first, reply with your question and leave the thread open.

## Address comments

1. `serve comments <file> --awaiting agent` and note the `cursor`.
2. For each thread, edit the file at `source_line_start`–`source_line_end` (check `location.current_text`), then `serve resolve <file> <id> --note "…"`.
3. Summarize what changed.

## React to comments as they arrive

`serve wait` blocks until the person comments or replies, prints that one event as JSON and exits. With `--since`, anything that happened after your last listing counts, so nothing slips through between listing and waiting.

1. `serve comments <file> --awaiting agent` (cursor `C`) and address everything.
2. Run `serve wait <file> --since C --from human --timeout 1800` in the background (`run_in_background: true`); you are woken when it exits. Exit code 124 means the timeout passed with nothing new.
3. When it exits, go back to step 1.

`serve watch [file]` streams the same events as JSON lines (`new_comment`, `new_reply`, `edited`, `resolved`, `unresolved`, `deleted`, each with a `cursor`) and exits when its reader does. Use it only when something reads it continuously.

## Other commands

- `serve inbox` lists threads waiting for you across every document.
- `serve status` and `serve stop` show and stop the background server.
- `serve report` lists bug reports the person captured in the browser; `serve report export <id> --markdown` prints one.
- Comments follow a file through `mv`, `git mv` and editor saves.
