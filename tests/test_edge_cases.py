"""Integration tests for edge cases identified in the gap analysis."""

import json
import os
import threading
import time
from pathlib import Path

import httpx
import pytest
from websockets.sync.client import connect

from conftest import (
    COMMENTS_DIR,
    ServeServer,
    _free_port,
    _start_server,
    make_comment,
)


# ---------------------------------------------------------------------------
# Directory mode: missing ?file= on mutating operations
# ---------------------------------------------------------------------------

class TestDirectoryMissingFileParam:
    def test_post_without_file_param_returns_400(self, dir_server: ServeServer):
        r = dir_server.post("/api/comments", json={"text": "No file param"})
        assert r.status_code == 400

    def test_patch_without_file_param_returns_400(self, dir_server: ServeServer, dir_tree: Path):
        c = dir_server.post(
            "/api/comments?file=README.md",
            json={"text": "A comment"},
        ).json()
        r = dir_server.patch(f"/api/comments/{c['id']}", json={"text": "Updated"})
        assert r.status_code == 400

    def test_delete_without_file_param_returns_400(self, dir_server: ServeServer, dir_tree: Path):
        c = dir_server.post(
            "/api/comments?file=README.md",
            json={"text": "A comment"},
        ).json()
        r = dir_server.delete(f"/api/comments/{c['id']}")
        assert r.status_code == 400

    def test_post_without_file_does_not_pollute_empty_store(self, dir_server: ServeServer):
        # Should not create a comment in an __empty__ store
        dir_server.post("/api/comments", json={"text": "No file param"})
        empty_store = COMMENTS_DIR / "__empty__.json"
        assert not empty_store.exists()


# ---------------------------------------------------------------------------
# PATCH validation: empty text
# ---------------------------------------------------------------------------

class TestPatchTextValidation:
    def test_empty_text_in_patch_rejected(self, md_server: ServeServer):
        c = make_comment(md_server, text="Original")
        r = md_server.patch(f"/api/comments/{c['id']}", json={"text": ""})
        assert r.status_code == 400

    def test_whitespace_only_text_in_patch_rejected(self, md_server: ServeServer):
        c = make_comment(md_server, text="Original")
        r = md_server.patch(f"/api/comments/{c['id']}", json={"text": "   "})
        assert r.status_code == 400

    def test_patch_resolved_only_no_text_is_valid(self, md_server: ServeServer):
        c = make_comment(md_server, text="Original")
        r = md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        assert r.status_code == 200
        assert r.json()["text"] == "Original"

    def test_original_text_unchanged_after_rejected_patch(self, md_server: ServeServer):
        c = make_comment(md_server, text="Keep me")
        md_server.patch(f"/api/comments/{c['id']}", json={"text": ""})
        comments = md_server.get("/api/comments").json()["comments"]
        assert comments[0]["text"] == "Keep me"


# ---------------------------------------------------------------------------
# Deep cascade delete (integration)
# ---------------------------------------------------------------------------

class TestDeepCascadeDeleteIntegration:
    def test_grandchild_removed_when_parent_deleted(self, md_server: ServeServer):
        parent = make_comment(md_server, text="Parent")
        child = md_server.post(
            "/api/comments",
            json={"text": "Child", "parent_id": parent["id"]},
        ).json()
        grandchild = md_server.post(
            "/api/comments",
            json={"text": "Grandchild", "parent_id": child["id"]},
        ).json()

        assert len(md_server.get("/api/comments").json()["comments"]) == 3
        md_server.delete(f"/api/comments/{parent['id']}")

        remaining = md_server.get("/api/comments").json()["comments"]
        ids = {c["id"] for c in remaining}
        assert parent["id"] not in ids
        assert child["id"] not in ids
        assert grandchild["id"] not in ids
        assert remaining == []

    def test_multi_level_chain_fully_removed(self, md_server: ServeServer):
        root = make_comment(md_server, text="Root")
        l1 = md_server.post("/api/comments", json={"text": "L1", "parent_id": root["id"]}).json()
        l2 = md_server.post("/api/comments", json={"text": "L2", "parent_id": l1["id"]}).json()
        l3 = md_server.post("/api/comments", json={"text": "L3", "parent_id": l2["id"]}).json()

        md_server.delete(f"/api/comments/{root['id']}")
        assert md_server.get("/api/comments").json()["comments"] == []


# ---------------------------------------------------------------------------
# Resolve idempotency
# ---------------------------------------------------------------------------

class TestResolveIdempotency:
    def test_resolving_already_resolved_is_200(self, md_server: ServeServer):
        c = make_comment(md_server)
        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        r = md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        assert r.status_code == 200
        assert r.json()["resolved"] is True

    def test_unresolving_already_unresolved_is_200(self, md_server: ServeServer):
        c = make_comment(md_server)
        r = md_server.patch(f"/api/comments/{c['id']}", json={"resolved": False})
        assert r.status_code == 200
        assert r.json()["resolved"] is False


# ---------------------------------------------------------------------------
# WebSocket: multiple clients all receive broadcast
# ---------------------------------------------------------------------------

class TestWebSocketMultipleClients:
    def test_all_clients_receive_comment_update(self, md_server: ServeServer):
        ws_url = f"ws://localhost:{md_server.port}/ws"
        received: list[list[dict]] = [[], []]

        def collect(ws, store):
            ws.socket.settimeout(0.5)
            deadline = time.monotonic() + 4.0
            while time.monotonic() < deadline:
                try:
                    msg = ws.recv(timeout=0.5)
                    store.append(json.loads(msg))
                except TimeoutError:
                    continue
                except Exception:
                    break

        with connect(ws_url) as ws1, connect(ws_url) as ws2:
            time.sleep(0.1)
            t1 = threading.Thread(target=collect, args=(ws1, received[0]))
            t2 = threading.Thread(target=collect, args=(ws2, received[1]))
            t1.start()
            t2.start()
            time.sleep(0.1)
            httpx.post(
                f"{md_server.base_url}/api/comments",
                json={"text": "multi-client test"},
            )
            t1.join(timeout=5)
            t2.join(timeout=5)

        assert any(m.get("type") == "comments-updated" for m in received[0])
        assert any(m.get("type") == "comments-updated" for m in received[1])


# ---------------------------------------------------------------------------
# Symlink escape protection
# ---------------------------------------------------------------------------

class TestSymlinkEscape:
    def test_symlink_pointing_outside_base_is_forbidden(self, dir_tree: Path):
        symlink = dir_tree / "escape.md"
        try:
            symlink.symlink_to("/etc/passwd")
        except OSError:
            pytest.skip("Cannot create symlink in this environment")

        port = _free_port()
        proc, base_url = _start_server(str(dir_tree), port)
        try:
            r = httpx.get(f"{base_url}/escape.md", follow_redirects=True)
            assert r.status_code in (403, 404, 500)
            # Confirm the server is still healthy
            health = httpx.get(f"{base_url}/api/comments", timeout=2.0)
            assert health.status_code == 200
        finally:
            proc.terminate()
            proc.wait(timeout=10)
            symlink.unlink(missing_ok=True)


# ---------------------------------------------------------------------------
# Read-only file: comments are stored externally, so read-only files work fine
# ---------------------------------------------------------------------------

class TestReadOnlyFile:
    def test_read_only_md_accepts_comments(self, md_file: Path):
        # The server stores comments in ~/.serve/comments/, never in the source
        # file. A read-only markdown file should accept comments normally.
        os.chmod(md_file, 0o444)
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        try:
            r = httpx.post(
                f"{base_url}/api/comments",
                json={"text": "Test comment"},
                timeout=5.0,
            )
            assert r.status_code == 200
            # Server remains functional
            health = httpx.get(f"{base_url}/api/comments", timeout=2.0)
            assert health.status_code == 200
            assert len(health.json()["comments"]) == 1
        finally:
            os.chmod(md_file, 0o644)
            proc.terminate()
            proc.wait(timeout=10)


# Note: data-url content behavior (page content present, reload script stripped)
# is covered by TestGenerateDataURL_Markdown in Go now that data-url is CLI-only.


# ---------------------------------------------------------------------------
# Binary file with code extension: no crash in directory mode
# ---------------------------------------------------------------------------

class TestBinaryCodeFile:
    def test_binary_py_file_returns_200_not_crash(self, dir_tree: Path):
        binary_py = dir_tree / "compiled.py"
        binary_py.write_bytes(b"\x00\x01\x02binary\xff\xfe" * 20)

        port = _free_port()
        proc, base_url = _start_server(str(dir_tree), port)
        try:
            r = httpx.get(f"{base_url}/compiled.py", timeout=5.0)
            assert r.status_code == 200
            health = httpx.get(f"{base_url}/api/comments", timeout=2.0)
            assert health.status_code == 200
        finally:
            proc.terminate()
            proc.wait(timeout=10)
            binary_py.unlink(missing_ok=True)
