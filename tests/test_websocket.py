"""
Black-box tests for WebSocket notifications.
Uses websockets' synchronous client (websockets 16.0+) to avoid event loop
conflicts with pytest-playwright when running the full suite together.
"""

import json
import threading
import time
from pathlib import Path

import httpx
import pytest
from websockets.sync.client import connect

from conftest import ServeServer, make_comment


def collect_messages(ws_url: str, trigger_fn, timeout: float = 4.0) -> list[dict]:
    """Connect to ws_url (sync), call trigger_fn(), collect messages until timeout."""
    received: list[dict] = []
    with connect(ws_url) as ws:
        ws.socket.settimeout(0.5)
        time.sleep(0.1)  # let server register connection
        trigger_fn()
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                msg = ws.recv(timeout=min(0.5, deadline - time.monotonic()))
                received.append(json.loads(msg))
            except TimeoutError:
                continue  # keep polling until overall deadline
            except Exception:
                break
    return received


class TestWebSocketConnection:
    def test_websocket_connects(self, md_server: ServeServer):
        ws_url = f"ws://localhost:{md_server.port}/ws"
        with connect(ws_url) as ws:
            assert ws.socket is not None


class TestCommentNotifications:
    def test_post_comment_broadcasts_update(self, md_server: ServeServer):
        ws_url = f"ws://localhost:{md_server.port}/ws"
        messages = collect_messages(
            ws_url,
            lambda: httpx.post(
                f"{md_server.base_url}/api/comments",
                json={"text": "WS trigger"},
            ),
        )
        assert any(m.get("type") == "comments-updated" for m in messages)

    def test_patch_comment_broadcasts_update(self, md_server: ServeServer):
        ws_url = f"ws://localhost:{md_server.port}/ws"
        c = make_comment(md_server)
        messages = collect_messages(
            ws_url,
            lambda: httpx.patch(
                f"{md_server.base_url}/api/comments/{c['id']}",
                json={"resolved": True},
            ),
        )
        assert any(m.get("type") == "comments-updated" for m in messages)

    def test_delete_comment_broadcasts_update(self, md_server: ServeServer):
        ws_url = f"ws://localhost:{md_server.port}/ws"
        c = make_comment(md_server)
        messages = collect_messages(
            ws_url,
            lambda: httpx.delete(f"{md_server.base_url}/api/comments/{c['id']}"),
        )
        assert any(m.get("type") == "comments-updated" for m in messages)


class TestFileChangeNotifications:
    def test_file_change_broadcasts_reload(self, md_server: ServeServer, md_file: Path):
        ws_url = f"ws://localhost:{md_server.port}/ws"

        def trigger():
            time.sleep(0.1)
            md_file.write_text(md_file.read_text() + "\n\nNew content added.\n")

        messages = collect_messages(ws_url, trigger, timeout=4.0)
        assert any(m.get("type") == "reload" for m in messages)
