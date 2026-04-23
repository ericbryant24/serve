# serve

A local document server with live reload, inline comments, and a sidebar for navigating directories. Renders markdown and HTML with full styling, syntax-highlights code files, embeds PDFs and images, and handles everything else with a download page.

## Install

Requires Python 3.13+.

### Quick install (recommended)

Install [uv](https://docs.astral.sh/uv/getting-started/installation/) if you don't have it:

```bash
# macOS / Linux
curl -LsSf https://astral.sh/uv/install.sh | sh

# Windows
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

Then install `serve`:

```bash
uv tool install git+https://github.com/ericbryant24/serve
```

This gives you the `serve` command globally. To update later:

```bash
uv tool install --force git+https://github.com/ericbryant24/serve
```

### With pip

```bash
pip install git+https://github.com/ericbryant24/serve
```

### From source

```bash
git clone https://github.com/ericbryant24/serve.git
cd serve
uv tool install -e .
# or: pip install -e .
```

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

## Directory Mode

Pass a directory path (or no argument if there's no `index.html`) to serve all files with a sidebar navigation panel.

- **Markdown** (`.md`) — rendered with GitHub-flavored styling + comments
- **HTML** (`.html`, `.htm`) — served with injected reload script + comments
- **Code files** (`.json`, `.yaml`, `.py`, `.js`, etc.) — syntax-highlighted via Pygments
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

Each document gets a unique `comment-id` embedded in the file on first comment:

- **Markdown**: `comment-id` field in YAML frontmatter
- **HTML**: `<meta name="comment-id">` tag

Comments are stored centrally at `~/.serve/comments/<doc-id>.json`. Because the ID is embedded in the file, comments survive file moves and renames.

## Agent Integration

Set up integration with AI coding agents so they can preview documents, read comments, and resolve feedback:

```bash
serve agent-init
```

Interactive wizard that writes the necessary skill file and instructions. Currently supports Claude Code, with user-level (all projects) or project-level scope.

## Features

- Live reload via WebSocket (watches file and assets for changes)
- Directory serving with sidebar file navigation
- Syntax highlighting for 100+ languages via Pygments
- Mermaid diagram rendering
- GitHub-flavored markdown styling
- PDF embedding
- Self-contained data URL export with inlined images
