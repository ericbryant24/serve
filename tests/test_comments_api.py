"""
Black-box tests for the comment CRUD API.
Runs against whichever implementation SERVE_CMD points to.
"""

import json
import shutil
import time
from pathlib import Path

import httpx
import pytest

from conftest import (
    COMMENTS_DIR,
    FIXTURES_DIR,
    ServeServer,
    _free_port,
    _start_server,
    make_comment,
)


class TestGetComments:
    def test_empty_list_for_new_file(self, md_server: ServeServer):
        r = md_server.get("/api/comments")
        assert r.status_code == 200
        data = r.json()
        assert "comments" in data
        assert isinstance(data["comments"], list)
        assert data["comments"] == []

    def test_returns_list_after_post(self, md_server: ServeServer):
        make_comment(md_server)
        r = md_server.get("/api/comments")
        assert r.status_code == 200
        assert len(r.json()["comments"]) == 1


class TestPostComment:
    def test_creates_comment_with_all_fields(self, md_server: ServeServer):
        r = md_server.post(
            "/api/comments",
            json={
                "text": "My comment",
                "anchor_text": "simple markdown",
                "block_text": "This is a simple markdown document for testing.",
                "source_line_start": 3,
                "source_line_end": 3,
            },
        )
        assert r.status_code == 200
        c = r.json()
        assert c["text"] == "My comment"
        assert c["anchor_text"] == "simple markdown"
        assert c["block_text"] == "This is a simple markdown document for testing."
        assert c["source_line_start"] == 3
        assert c["source_line_end"] == 3
        assert c["resolved"] is False
        assert c["parent_id"] is None
        assert "id" in c
        assert "created_at" in c

    def test_resolved_defaults_to_false(self, md_server: ServeServer):
        c = make_comment(md_server)
        assert c["resolved"] is False

    def test_parent_id_null_by_default(self, md_server: ServeServer):
        c = make_comment(md_server)
        assert c["parent_id"] is None

    def test_optional_fields_can_be_omitted(self, md_server: ServeServer):
        r = md_server.post("/api/comments", json={"text": "Minimal comment"})
        assert r.status_code == 200
        c = r.json()
        assert c["text"] == "Minimal comment"
        assert c["resolved"] is False

    def test_creates_reply_with_parent_id(self, md_server: ServeServer):
        parent = make_comment(md_server, text="Parent")
        r = md_server.post(
            "/api/comments",
            json={"text": "Reply", "parent_id": parent["id"]},
        )
        assert r.status_code == 200
        reply = r.json()
        assert reply["parent_id"] == parent["id"]

    def test_get_returns_both_parent_and_reply(self, md_server: ServeServer):
        parent = make_comment(md_server, text="Parent")
        md_server.post(
            "/api/comments",
            json={"text": "Reply", "parent_id": parent["id"]},
        )
        comments = md_server.get("/api/comments").json()["comments"]
        assert len(comments) == 2
        ids = {c["id"] for c in comments}
        assert parent["id"] in ids


class TestPatchComment:
    def test_update_text(self, md_server: ServeServer):
        c = make_comment(md_server, text="Original")
        r = md_server.patch(f"/api/comments/{c['id']}", json={"text": "Updated"})
        assert r.status_code == 200
        assert r.json()["text"] == "Updated"

    def test_update_resolved(self, md_server: ServeServer):
        c = make_comment(md_server)
        r = md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        assert r.status_code == 200
        assert r.json()["resolved"] is True

    def test_unresolve(self, md_server: ServeServer):
        c = make_comment(md_server)
        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        r = md_server.patch(f"/api/comments/{c['id']}", json={"resolved": False})
        assert r.status_code == 200
        assert r.json()["resolved"] is False

    def test_partial_update_preserves_other_fields(self, md_server: ServeServer):
        c = make_comment(md_server, text="Keep me", anchor_text="simple markdown")
        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        updated = md_server.get("/api/comments").json()["comments"][0]
        assert updated["text"] == "Keep me"
        assert updated["resolved"] is True

    def test_unknown_id_returns_404(self, md_server: ServeServer):
        r = md_server.patch("/api/comments/no-such-id", json={"text": "x"})
        assert r.status_code == 404


class TestDeleteComment:
    def test_delete_removes_comment(self, md_server: ServeServer):
        c = make_comment(md_server)
        r = md_server.delete(f"/api/comments/{c['id']}")
        assert r.status_code == 200
        assert r.json().get("ok") is True
        remaining = md_server.get("/api/comments").json()["comments"]
        assert all(x["id"] != c["id"] for x in remaining)

    def test_delete_cascades_to_replies(self, md_server: ServeServer):
        parent = make_comment(md_server, text="Parent")
        md_server.post(
            "/api/comments",
            json={"text": "Reply", "parent_id": parent["id"]},
        )
        assert len(md_server.get("/api/comments").json()["comments"]) == 2
        md_server.delete(f"/api/comments/{parent['id']}")
        remaining = md_server.get("/api/comments").json()["comments"]
        assert remaining == []

    def test_delete_child_does_not_cascade(self, md_server: ServeServer):
        parent = make_comment(md_server, text="Parent")
        reply = md_server.post(
            "/api/comments",
            json={"text": "Reply", "parent_id": parent["id"]},
        ).json()
        md_server.delete(f"/api/comments/{reply['id']}")
        remaining = md_server.get("/api/comments").json()["comments"]
        assert len(remaining) == 1
        assert remaining[0]["id"] == parent["id"]

    def test_unknown_id_returns_404(self, md_server: ServeServer):
        r = md_server.delete("/api/comments/no-such-id")
        assert r.status_code == 404


class TestCommentPersistence:
    def test_comments_survive_server_restart(self, md_file: Path):
        import re
        port = _free_port()
        proc1, base_url = _start_server(str(md_file), port)
        try:
            r = httpx.post(
                f"{base_url}/api/comments",
                json={"text": "Persistent comment"},
            )
            assert r.status_code == 200
            comment_id = r.json()["id"]
        finally:
            proc1.terminate()
            proc1.wait(timeout=10)

        port2 = _free_port()
        proc2, base_url2 = _start_server(str(md_file), port2)
        try:
            comments = httpx.get(f"{base_url2}/api/comments").json()["comments"]
            ids = [c["id"] for c in comments]
            assert comment_id in ids
        finally:
            proc2.terminate()
            proc2.wait(timeout=10)
            content = md_file.read_text()
            m = re.search(r"comment-id:\s*([a-f0-9]+)", content)
            if m:
                (COMMENTS_DIR / f"{m.group(1)}.json").unlink(missing_ok=True)


class TestEmptyCommentValidation:
    def test_empty_text_rejected(self, md_server: ServeServer):
        r = md_server.post("/api/comments", json={"text": ""})
        assert r.status_code == 400

    def test_whitespace_only_text_rejected(self, md_server: ServeServer):
        r = md_server.post("/api/comments", json={"text": "   "})
        assert r.status_code == 400

    def test_missing_text_field_rejected(self, md_server: ServeServer):
        r = md_server.post("/api/comments", json={"anchor_text": "something"})
        assert r.status_code in (400, 422)


class TestDirectoryModeComments:
    def test_comments_scoped_per_file(self, dir_server: ServeServer):
        r1 = dir_server.post(
            "/api/comments?file=README.md",
            json={"text": "README comment"},
        )
        assert r1.status_code == 200

        r2 = dir_server.post(
            "/api/comments?file=sub/page.md",
            json={"text": "Sub page comment"},
        )
        assert r2.status_code == 200

        readme_comments = dir_server.get("/api/comments?file=README.md").json()["comments"]
        sub_comments = dir_server.get("/api/comments?file=sub/page.md").json()["comments"]

        assert len(readme_comments) == 1
        assert readme_comments[0]["text"] == "README comment"
        assert len(sub_comments) == 1
        assert sub_comments[0]["text"] == "Sub page comment"

    def test_empty_file_param_returns_empty(self, dir_server: ServeServer):
        r = dir_server.get("/api/comments")
        assert r.status_code == 200
        assert r.json()["comments"] == []

    def test_path_traversal_via_file_param_rejected(self, dir_server: ServeServer):
        # ?file=../../../etc/passwd should not expose files outside the base dir
        r = dir_server.get("/api/comments?file=../../../etc/passwd")
        assert r.status_code == 200
        assert r.json()["comments"] == []  # safely returns empty, not an error leak

    def test_sibling_dir_prefix_attack_rejected(self, dir_server: ServeServer, dir_tree):
        # Create a sibling directory whose name starts with the base dir name
        sibling = dir_tree.parent / (dir_tree.name + "-evil")
        sibling.mkdir(exist_ok=True)
        secret = sibling / "secret.md"
        secret.write_text("---\ncomment-id: evilid\n---\n\n# Secret\n")
        try:
            r = dir_server.get(f"/api/comments?file=../{dir_tree.name}-evil/secret.md")
            # Must not expose comments from outside base dir
            assert r.status_code == 200
            assert r.json()["comments"] == []
        finally:
            import shutil
            shutil.rmtree(sibling, ignore_errors=True)
