"""
Black-box tests for CLI subcommands: comments, resolve, list, kill.
"""

import json
import re
import subprocess
import time
from pathlib import Path

import httpx
import pytest

from conftest import (
    COMMENTS_DIR,
    child_env,
    ServeServer,
    _free_port,
    _serve_cmd_parts,
    _start_server,
    _wait_ready,
    make_comment,
)


def run_cli(*args: str) -> subprocess.CompletedProcess:
    cmd = _serve_cmd_parts() + list(args)
    return subprocess.run(
        cmd,
        capture_output=True,
        text=True,
        cwd=Path(__file__).parent.parent,
        env=child_env(),
    )


class TestCommentsSubcommand:
    def test_returns_json_for_new_file(self, md_file: Path):
        # A file without a comment-id returns empty comments (no doc_id yet)
        result = run_cli("comments", str(md_file))
        assert result.returncode == 0
        data = json.loads(result.stdout)
        assert "comments" in data
        assert data["comments"] == []

    def test_returns_comment_after_api_post(self, md_server: ServeServer, md_file: Path):
        c = make_comment(md_server, text="CLI visible comment")
        result = run_cli("comments", str(md_file))
        assert result.returncode == 0
        data = json.loads(result.stdout)
        # After server injects a doc_id, the CLI should also return file+doc_id
        assert "comments" in data
        ids = [x["id"] for x in data["comments"]]
        assert c["id"] in ids

    def test_doc_id_is_stable(self, md_server: ServeServer, md_file: Path):
        # The server uses an inode-based doc_id — source files are never modified.
        # The CLI must return the same doc_id the server used to store the comment.
        c = make_comment(md_server)
        result = run_cli("comments", str(md_file))
        assert result.returncode == 0
        data = json.loads(result.stdout)
        assert data.get("doc_id"), "doc_id should be present in CLI output"
        assert c["id"] in [x["id"] for x in data["comments"]], "comment created via API should be visible via CLI"
        # Source file must not be modified
        content = md_file.read_text()
        assert "comment-id" not in content, "server must not inject comment-id into source files"

    def test_html_file_works(self, html_file: Path):
        result = run_cli("comments", str(html_file))
        assert result.returncode == 0
        data = json.loads(result.stdout)
        assert "comments" in data

    def test_nonexistent_file_exits_nonzero(self):
        result = run_cli("comments", "/tmp/does_not_exist_serve_test.md")
        assert result.returncode != 0


class TestResolveSubcommand:
    def test_resolve_marks_comment_resolved(self, md_server: ServeServer, md_file: Path):
        c = make_comment(md_server, text="Resolve me")
        result = run_cli("resolve", str(md_file), c["id"])
        assert result.returncode == 0
        assert "Resolved" in result.stdout or "resolved" in result.stdout.lower()
        comments = md_server.get("/api/comments").json()["comments"]
        resolved = next((x for x in comments if x["id"] == c["id"]), None)
        assert resolved is not None
        assert resolved["resolved"] is True

    def test_resolve_prints_resolved_id(self, md_server: ServeServer, md_file: Path):
        c = make_comment(md_server)
        result = run_cli("resolve", str(md_file), c["id"])
        assert c["id"] in result.stdout

    def test_unknown_id_exits_nonzero(self, md_file: Path):
        result = run_cli("resolve", str(md_file), "no-such-id-abc123")
        assert result.returncode != 0

    def test_unknown_id_writes_to_stderr(self, md_server: ServeServer, md_file: Path):
        # Trigger doc-id injection first, then try resolving a non-existent comment
        md_server.get("/")
        make_comment(md_server)  # ensure store exists
        result = run_cli("resolve", str(md_file), "no-such-id-abc123")
        assert result.returncode != 0
        assert result.stderr  # something must appear on stderr

    def test_resolve_multiple_ids(self, md_server: ServeServer, md_file: Path):
        c1 = make_comment(md_server, text="First")
        c2 = make_comment(md_server, text="Second")
        result = run_cli("resolve", str(md_file), c1["id"], c2["id"])
        assert result.returncode == 0
        comments = md_server.get("/api/comments").json()["comments"]
        for c in comments:
            assert c["resolved"] is True

    def test_partial_resolve_exits_nonzero(self, md_server: ServeServer, md_file: Path):
        c = make_comment(md_server)
        result = run_cli("resolve", str(md_file), c["id"], "no-such-id")
        assert result.returncode != 0
        # The real comment should still be resolved
        comments = md_server.get("/api/comments").json()["comments"]
        resolved = next(x for x in comments if x["id"] == c["id"])
        assert resolved["resolved"] is True


class TestListSubcommand:
    def test_list_shows_running_server(self, md_server: ServeServer):
        result = run_cli("list")
        assert result.returncode == 0
        assert str(md_server.port) in result.stdout

    def test_list_json_returns_array(self, md_server: ServeServer):
        result = run_cli("list", "--json")
        assert result.returncode == 0
        data = json.loads(result.stdout)
        assert isinstance(data, list)

    def test_list_json_has_required_fields(self, md_server: ServeServer):
        result = run_cli("list", "--json")
        data = json.loads(result.stdout)
        assert len(data) >= 1
        entry = next((x for x in data if x.get("port") == md_server.port), None)
        assert entry is not None, f"Port {md_server.port} not found in list output"
        assert "pid" in entry
        assert "port" in entry
        assert "url" in entry
        assert "path" in entry
        assert "mode" in entry

    def test_list_json_url_matches_port(self, md_server: ServeServer):
        result = run_cli("list", "--json")
        data = json.loads(result.stdout)
        entry = next(x for x in data if x.get("port") == md_server.port)
        assert str(md_server.port) in (entry["url"] or "")


class TestKillSubcommand:
    def test_kill_by_port(self, md_file: Path):
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        try:
            assert httpx.get(f"{base_url}/api/comments").status_code == 200
            result = run_cli("kill", "--port", str(port))
            assert result.returncode == 0
            time.sleep(0.5)
            with pytest.raises(Exception):
                httpx.get(f"{base_url}/api/comments", timeout=1.0)
        finally:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)

    def test_kill_by_pid(self, md_file: Path):
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        try:
            assert httpx.get(f"{base_url}/api/comments").status_code == 200
            result = run_cli("kill", str(proc.pid))
            assert result.returncode == 0
            time.sleep(0.5)
            with pytest.raises(Exception):
                httpx.get(f"{base_url}/api/comments", timeout=1.0)
        finally:
            if proc.poll() is None:
                proc.terminate()
                proc.wait(timeout=5)
