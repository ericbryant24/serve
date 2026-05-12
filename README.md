# serve

A local document server with live reload, inline comments, and a sidebar for navigating directories. Renders markdown and HTML with full styling, syntax-highlights code files, embeds PDFs and images, and handles everything else with a download page.

## Install

Requires [Go](https://go.dev/dl/) 1.21+.

```bash
git clone https://github.com/ericbryant24/serve.git
cd serve
go install .
```

This places the binary in `$(go env GOPATH)/bin`, which Go adds to your `PATH` by default.

## Usage

```bash
# Serve a markdown file (opens browser, live reloads on save)
serve document.md

# Serve an HTML file
serve page.html

# Serve a directory (sidebar + all file types)
serve .
serve ./docs/

# Specify port and host
serve document.md -p 3000 --host 0.0.0.0

# Don't open browser automatically
serve document.md --no-open

# Generate a self-contained data URL (copied to clipboard)
serve document.md --data-url
```

### Managing running instances

```bash
# List every running serve (table view; --json for scripting)
serve list

# Stop a specific instance
serve kill <pid>
serve kill --port 8001

# Stop them all
serve kill --all
```

## Directory Mode

Pass a directory path to serve all files with a sidebar navigation panel.

- **Markdown** (`.md`) — rendered with GitHub-flavored styling + comments
- **HTML** (`.html`, `.htm`) — served with injected reload script + comments
- **Code files** (`.json`, `.yaml`, `.py`, `.js`, etc.) — syntax-highlighted via Chroma
- **PDF** (`.pdf`) — embedded viewer
- **Plain text** (`.txt`, `.log`, etc.) — rendered in a `<pre>` block
- **Other files** — served as raw static assets

The sidebar shows the directory tree, highlights the current file, and persists expand/collapse state across reloads. Toggle it with the button in the top-left corner.

## Inline Comments

Select text in the browser to add inline comments. Comments are highlighted in the document and support threaded replies, resolution, and deletion.

### Browser UI

1. Select text in the rendered document
2. Click the "Comment" button that appears
3. Write your comment and press Ctrl+Enter (or click Comment)
4. Click highlighted text to view the comment thread
5. Use Reply, Resolve, or Delete from the thread popover

### CLI

```bash
# List all comments on a document (JSON output)
serve comments document.md

# Resolve comments by ID
serve resolve document.md <comment-id> [<comment-id>...]
```

### REST API

When the server is running, comments are also available via HTTP:

```bash
# List comments
curl http://localhost:8000/api/comments

# Create a comment
curl -X POST http://localhost:8000/api/comments \
  -H 'Content-Type: application/json' \
  -d '{"text": "Fix this", "anchor_text": "selected text", "source_line_start": 5, "source_line_end": 5}'

# Resolve a comment
curl -X PATCH http://localhost:8000/api/comments/<id> \
  -H 'Content-Type: application/json' \
  -d '{"resolved": true}'

# Delete a comment
curl -X DELETE http://localhost:8000/api/comments/<id>
```

### How Comments Are Stored

Comments are stored centrally at `~/.serve/comments/<key>.json`. **Source files are never modified.**

The store key is derived from the file's inode and device number (Unix/macOS/Linux), so comments automatically follow the file when you rename or move it with `mv` or `git mv`. On Windows the key falls back to a hash of the absolute path.

## Agent Integration

Set up integration with AI coding agents so they can preview documents, read comments, and resolve feedback:

```bash
serve agent-init
```

Interactive wizard that writes the necessary skill file and instructions. Currently supports Claude Code, with user-level (all projects) or project-level scope.

## Features

- Live reload via WebSocket (watches file and assets for changes)
- Directory serving with sidebar file navigation
- Syntax highlighting for 100+ languages via Chroma
- Mermaid diagram rendering
- GitHub-flavored markdown styling
- PDF embedding
- Self-contained data URL export with inlined images
