"""HTTP server with WebSocket live reload."""

import asyncio
import mimetypes
import webbrowser
from dataclasses import asdict
from pathlib import Path

import aiohttp
from aiohttp import web

from serve.comments import CommentStore, ensure_document_id, get_document_id
from serve.dataurl import generate_data_url
from serve.renderer import (
    can_render_as_code,
    is_text_file,
    render,
    render_code_file,
    render_image,
    render_pdf,
)
from serve.templates import inject_reload_script
from serve.watcher import watch, watch_directory


# ---------------------------------------------------------------------------
# File tree builder for directory mode
# ---------------------------------------------------------------------------

def _build_file_tree(root: Path, rel: Path | None = None) -> list[dict]:
    """Recursively build a file tree from *root*, skipping hidden entries."""
    base = root if rel is None else root / rel
    dirs: list[dict] = []
    files: list[dict] = []
    try:
        entries = sorted(base.iterdir(), key=lambda p: p.name.lower())
    except PermissionError:
        return []
    for entry in entries:
        if entry.name.startswith("."):
            continue
        rel_path = str(entry.relative_to(root))
        if entry.is_dir():
            children = _build_file_tree(root, entry.relative_to(root))
            if children:  # only include non-empty dirs
                dirs.append({"name": entry.name, "path": rel_path, "type": "dir", "children": children})
        elif entry.is_file():
            files.append({"name": entry.name, "path": rel_path, "type": "file"})
    return dirs + files


_DEFAULT_FILE_PRIORITY = ["README.md", "readme.md", "index.md", "index.html"]


def _find_default_file(root: Path) -> str | None:
    """Find the best default file to show when navigating to /."""
    for name in _DEFAULT_FILE_PRIORITY:
        if (root / name).is_file():
            return name
    # Fall back to first .md, then first .html
    md_files = sorted(root.rglob("*.md"))
    if md_files:
        return str(md_files[0].relative_to(root))
    html_files = sorted(root.rglob("*.html"))
    if html_files:
        return str(html_files[0].relative_to(root))
    return None


class Server:
    def __init__(
        self,
        file_path: Path,
        *,
        mode: str,
        host: str = "localhost",
        port: int = 8000,
        open_browser: bool = True,
    ) -> None:
        self.mode = mode
        self.host = host
        self.port = port
        self.open_browser = open_browser
        self._ws_clients: set[web.WebSocketResponse] = set()

        if mode == "directory":
            self.file_path = None
            self.base_dir = file_path.resolve()
            self._dir_name = self.base_dir.name or str(self.base_dir)
            self._doc_id: str | None = None
        else:
            self.file_path = file_path.resolve()
            self.base_dir = self.file_path.parent
            self._dir_name = None
            self._doc_id = get_document_id(self.file_path)

        self._comment_store: CommentStore | None = None
        # Per-file comment stores for directory mode
        self._comment_stores: dict[str, CommentStore] = {}

    # ------------------------------------------------------------------
    # Single-file page handler
    # ------------------------------------------------------------------

    async def _handle_page(self, request: web.Request) -> web.Response:
        """Serve the HTML page (single-file mode)."""
        if self.mode == "markdown":
            html = render(self.file_path)
        else:
            html = self.file_path.read_text(encoding="utf-8")
            html = inject_reload_script(html, favicon_path=str(self.file_path))
        return web.Response(text=html, content_type="text/html")

    # ------------------------------------------------------------------
    # Directory mode handlers
    # ------------------------------------------------------------------

    async def _handle_dir_root(self, request: web.Request) -> web.Response:
        """Redirect / to the default file in directory mode."""
        default = _find_default_file(self.base_dir)
        if default:
            raise web.HTTPFound(f"/{default}")
        return web.Response(text="No servable files found in directory.", status=404)

    async def _handle_dir_file(self, request: web.Request) -> web.Response:
        """Serve any file under the directory with appropriate rendering."""
        rel_path = request.match_info["path"]
        file_path = (self.base_dir / rel_path).resolve()

        # Security: don't escape the base directory
        if not str(file_path).startswith(str(self.base_dir)):
            raise web.HTTPForbidden()

        if not file_path.is_file():
            raise web.HTTPNotFound()

        suffix = file_path.suffix.lower()
        sidebar = (self._dir_name, rel_path)

        # Raw file access (for PDF embed src, images, etc.)
        if request.query.get("raw") == "1":
            return web.FileResponse(file_path)

        # Markdown
        if suffix == ".md":
            html = render(file_path, sidebar=sidebar)
            return web.Response(text=html, content_type="text/html")

        # HTML
        if suffix in {".html", ".htm"}:
            html = file_path.read_text(encoding="utf-8")
            html = inject_reload_script(html, sidebar=sidebar, favicon_path=str(file_path))
            return web.Response(text=html, content_type="text/html")

        # PDF
        if suffix == ".pdf":
            html = render_pdf(file_path, rel_path, sidebar=sidebar)
            return web.Response(text=html, content_type="text/html")

        # Syntax-highlighted code
        if can_render_as_code(file_path):
            html = render_code_file(file_path, sidebar=sidebar)
            return web.Response(text=html, content_type="text/html")

        # Plain text
        if is_text_file(file_path):
            html = render_code_file(file_path, sidebar=sidebar)
            return web.Response(text=html, content_type="text/html")

        # Images
        _IMAGE_EXTENSIONS = {".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico"}
        if suffix in _IMAGE_EXTENSIONS:
            html = render_image(file_path, rel_path, sidebar=sidebar)
            return web.Response(text=html, content_type="text/html")

        # Everything else: show file info page with download link
        from serve.templates import wrap_file_info
        size = file_path.stat().st_size
        html = wrap_file_info(file_path.name, rel_path, size, sidebar=sidebar, favicon_path=str(file_path))
        return web.Response(text=html, content_type="text/html")

    async def _handle_file_tree(self, request: web.Request) -> web.Response:
        """Return the file tree as JSON."""
        tree = _build_file_tree(self.base_dir)
        return web.json_response({"files": tree})

    # ------------------------------------------------------------------
    # Per-file comment helpers for directory mode
    # ------------------------------------------------------------------

    def _get_file_from_request(self, request: web.Request) -> Path | None:
        """Resolve the file path from a ?file= query param (directory mode)."""
        rel = request.query.get("file")
        if not rel:
            return None
        fp = (self.base_dir / rel).resolve()
        if not str(fp).startswith(str(self.base_dir)):
            return None
        if not fp.is_file():
            return None
        return fp

    def _get_store_for_file(self, file_path: Path) -> CommentStore:
        """Get or create a CommentStore for a specific file."""
        key = str(file_path)
        if key not in self._comment_stores:
            doc_id = get_document_id(file_path)
            if doc_id is None:
                doc_id = ensure_document_id(file_path)
            self._comment_stores[key] = CommentStore(doc_id)
        return self._comment_stores[key]

    # ------------------------------------------------------------------
    # WebSocket & data URL
    # ------------------------------------------------------------------

    async def _handle_websocket(self, request: web.Request) -> web.WebSocketResponse:
        """Handle WebSocket connections for live reload."""
        ws = web.WebSocketResponse()
        await ws.prepare(request)
        self._ws_clients.add(ws)
        try:
            async for msg in ws:
                if msg.type == aiohttp.WSMsgType.ERROR:
                    break
        finally:
            self._ws_clients.discard(ws)
        return ws

    async def _handle_data_url(self, request: web.Request) -> web.Response:
        """Generate and return a data URL for the current page."""
        url = generate_data_url(self.file_path, self.mode)
        return web.Response(text=url, content_type="text/plain")

    # ------------------------------------------------------------------
    # Comment API
    # ------------------------------------------------------------------

    def _get_store(self, request: web.Request | None = None) -> CommentStore:
        """Get the comment store, resolving per-file in directory mode."""
        if self.mode == "directory" and request is not None:
            fp = self._get_file_from_request(request)
            if fp:
                return self._get_store_for_file(fp)
            # No file param — return empty-ish store
            return CommentStore("__empty__")
        # Single-file mode
        if self._comment_store is None:
            if self._doc_id is None:
                self._doc_id = ensure_document_id(self.file_path)
            self._comment_store = CommentStore(self._doc_id)
        return self._comment_store

    async def _handle_list_comments(self, request: web.Request) -> web.Response:
        """List all comments for this document."""
        if self.mode == "directory":
            fp = self._get_file_from_request(request)
            if not fp:
                return web.json_response({"comments": []})
            doc_id = get_document_id(fp)
            if not doc_id:
                return web.json_response({"comments": []})
            store = self._get_store_for_file(fp)
        else:
            if self._doc_id is None:
                return web.json_response({"comments": []})
            store = self._get_store(request)
        comments = store.list_comments()
        return web.json_response({"comments": [asdict(c) for c in comments]})

    async def _handle_create_comment(self, request: web.Request) -> web.Response:
        """Create a new comment."""
        body = await request.json()
        store = self._get_store(request)
        comment = store.add_comment(
            text=body["text"],
            anchor_text=body.get("anchor_text", ""),
            block_text=body.get("block_text", ""),
            source_line_start=body.get("source_line_start"),
            source_line_end=body.get("source_line_end"),
            parent_id=body.get("parent_id"),
        )
        return web.json_response(asdict(comment))

    async def _handle_update_comment(self, request: web.Request) -> web.Response:
        """Update a comment's text or resolved status."""
        comment_id = request.match_info["id"]
        body = await request.json()
        store = self._get_store(request)
        comment = store.update_comment(
            comment_id,
            text=body.get("text"),
            resolved=body.get("resolved"),
        )
        if not comment:
            return web.json_response({"error": "Not found"}, status=404)
        return web.json_response(asdict(comment))

    async def _handle_delete_comment(self, request: web.Request) -> web.Response:
        """Delete a comment and its replies."""
        comment_id = request.match_info["id"]
        store = self._get_store(request)
        if not store.delete_comment(comment_id):
            return web.json_response({"error": "Not found"}, status=404)
        return web.json_response({"ok": True})

    # ------------------------------------------------------------------
    # Reload notification
    # ------------------------------------------------------------------

    async def notify_reload(self) -> None:
        """Notify all connected browsers to reload."""
        closed = set()
        for ws in self._ws_clients:
            try:
                await ws.send_json({"type": "reload"})
            except ConnectionResetError:
                closed.add(ws)
        self._ws_clients -= closed

    # ------------------------------------------------------------------
    # Port finding & startup
    # ------------------------------------------------------------------

    async def _find_port(self) -> int:
        """Find an available port, starting from self.port."""
        for offset in range(11):
            port = self.port + offset
            try:
                server = await asyncio.get_event_loop().create_server(
                    asyncio.Protocol, self.host, port
                )
                server.close()
                await server.wait_closed()
                return port
            except OSError:
                continue
        return self.port  # Fall back, let aiohttp raise if still busy

    async def start(self) -> None:
        """Start the server and file watcher."""
        app = web.Application()
        app.router.add_get("/ws", self._handle_websocket)
        app.router.add_get("/api/comments", self._handle_list_comments)
        app.router.add_post("/api/comments", self._handle_create_comment)
        app.router.add_patch("/api/comments/{id}", self._handle_update_comment)
        app.router.add_delete("/api/comments/{id}", self._handle_delete_comment)
        app.router.add_get("/api/files", self._handle_file_tree)

        if self.mode == "directory":
            app.router.add_get("/", self._handle_dir_root)
            app.router.add_get("/{path:.*}", self._handle_dir_file)
        else:
            app.router.add_get("/", self._handle_page)
            app.router.add_get("/__data_url", self._handle_data_url)
            app.router.add_static("/", self.base_dir, show_index=False)

        port = await self._find_port()
        runner = web.AppRunner(app)
        await runner.setup()
        site = web.TCPSite(runner, self.host, port)
        await site.start()

        url = f"http://{self.host}:{port}"
        if self.mode == "directory":
            print(f"Serving {self.base_dir} at {url}")
        else:
            print(f"Serving {self.file_path.name} at {url}")
        print("Press Ctrl+C to stop")

        if self.open_browser:
            webbrowser.open(url)

        # Start watching for file changes
        if self.mode == "directory":
            watcher_task = asyncio.create_task(
                watch_directory(self.base_dir, self.notify_reload)
            )
        else:
            watcher_task = asyncio.create_task(
                watch(
                    self.file_path,
                    self.notify_reload,
                    markdown_mode=(self.mode == "markdown"),
                )
            )

        try:
            # Keep running until cancelled
            await asyncio.Event().wait()
        finally:
            watcher_task.cancel()
            await runner.cleanup()
