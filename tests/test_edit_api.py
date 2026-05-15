"""
Integration tests for the inline editing API.
Tests /api/file (GET), /api/edit (POST), /api/preview (POST),
and verifies the edit button appears in rendered pages.
"""

import shutil
import time
from pathlib import Path

import httpx
import pytest

from conftest import FIXTURES_DIR, ServeServer, _free_port, _start_server


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def editable_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "simple.md"
    dst = tmp_path / "simple.md"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def txt_dir(tmp_path: Path) -> Path:
    """Directory containing a notes.txt file — txt requires directory mode."""
    d = tmp_path / "txtdir"
    d.mkdir()
    (d / "notes.txt").write_text("Hello plain text\nSecond line\n")
    return d


@pytest.fixture()
def edit_server(editable_file: Path):
    port = _free_port()
    proc, base_url = _start_server(str(editable_file), port)
    server = ServeServer(base_url, port, proc, [])
    yield server
    proc.terminate()
    proc.wait(timeout=10)


@pytest.fixture()
def txt_server(txt_dir: Path):
    """Serves a directory containing notes.txt (txt needs directory mode)."""
    port = _free_port()
    proc, base_url = _start_server(str(txt_dir), port)
    server = ServeServer(base_url, port, proc, [])
    server._txt_dir = txt_dir  # type: ignore[attr-defined]
    yield server
    proc.terminate()
    proc.wait(timeout=10)


# ---------------------------------------------------------------------------
# /api/file — GET raw content
# ---------------------------------------------------------------------------


class TestGetFile:
    def test_returns_200(self, edit_server: ServeServer):
        r = edit_server.get("/api/file")
        assert r.status_code == 200

    def test_content_type_json(self, edit_server: ServeServer):
        r = edit_server.get("/api/file")
        assert "application/json" in r.headers.get("content-type", "")

    def test_returns_content_field(self, edit_server: ServeServer):
        r = edit_server.get("/api/file")
        data = r.json()
        assert "content" in data

    def test_content_matches_source(self, edit_server: ServeServer, editable_file: Path):
        r = edit_server.get("/api/file")
        assert r.json()["content"] == editable_file.read_text()

    def test_txt_file_returns_content(self, txt_server: ServeServer):
        r = txt_server.get("/api/file?file=notes.txt")
        assert r.status_code == 200
        assert r.json()["content"] == "Hello plain text\nSecond line\n"

    def test_method_not_allowed(self, edit_server: ServeServer):
        r = edit_server.post("/api/file", json={})
        assert r.status_code == 405


# ---------------------------------------------------------------------------
# /api/edit — POST updated content
# ---------------------------------------------------------------------------


class TestEditFile:
    def test_returns_ok_true(self, edit_server: ServeServer):
        r = edit_server.post("/api/edit", json={"content": "# Updated\n"})
        assert r.status_code == 200
        assert r.json() == {"ok": True}

    def test_writes_content_to_disk(self, edit_server: ServeServer, editable_file: Path):
        new_content = "# New Title\n\nEdited content.\n"
        edit_server.post("/api/edit", json={"content": new_content})
        # Allow a moment for write to flush
        time.sleep(0.05)
        assert editable_file.read_text() == new_content

    def test_roundtrip_get_then_edit_then_get(
        self, edit_server: ServeServer, editable_file: Path
    ):
        original = edit_server.get("/api/file").json()["content"]
        new_content = original + "\n\n## Appended section\n"
        edit_server.post("/api/edit", json={"content": new_content})
        time.sleep(0.05)
        updated = edit_server.get("/api/file").json()["content"]
        assert updated == new_content

    def test_bad_json_returns_400(self, edit_server: ServeServer):
        r = httpx.post(
            f"{edit_server.base_url}/api/edit",
            content=b"not json",
            headers={"Content-Type": "application/json"},
        )
        assert r.status_code == 400

    def test_method_not_allowed(self, edit_server: ServeServer):
        r = edit_server.get("/api/edit")
        assert r.status_code == 405

    def test_txt_file_editable(self, txt_server: ServeServer):
        r = txt_server.post("/api/edit?file=notes.txt", json={"content": "updated plain\n"})
        assert r.status_code == 200
        time.sleep(0.05)
        assert (txt_server._txt_dir / "notes.txt").read_text() == "updated plain\n"  # type: ignore[attr-defined]

    def test_empty_content_writes_empty_file(
        self, edit_server: ServeServer, editable_file: Path
    ):
        edit_server.post("/api/edit", json={"content": ""})
        time.sleep(0.05)
        assert editable_file.read_text() == ""


# ---------------------------------------------------------------------------
# /api/preview — POST markdown, get back HTML fragment
# ---------------------------------------------------------------------------


class TestPreview:
    def test_returns_200(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "# Hello"})
        assert r.status_code == 200

    def test_returns_html_field(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "# Hello"})
        data = r.json()
        assert "html" in data

    def test_renders_heading(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "# My Heading"})
        html = r.json()["html"]
        assert "<h1" in html
        assert "My Heading" in html

    def test_renders_paragraph(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "A paragraph here."})
        html = r.json()["html"]
        assert "<p" in html  # may have data-source-lines attribute
        assert "A paragraph here." in html

    def test_renders_bold(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "**bold text**"})
        html = r.json()["html"]
        assert "<strong>" in html

    def test_renders_code_block(self, edit_server: ServeServer):
        r = edit_server.post(
            "/api/preview", json={"content": "```python\nprint('hi')\n```"}
        )
        html = r.json()["html"]
        assert "print" in html

    def test_empty_content_returns_empty_html(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": ""})
        assert r.status_code == 200
        html = r.json()["html"]
        assert isinstance(html, str)

    def test_bad_json_returns_400(self, edit_server: ServeServer):
        r = httpx.post(
            f"{edit_server.base_url}/api/preview",
            content=b"not json",
            headers={"Content-Type": "application/json"},
        )
        assert r.status_code == 400

    def test_method_not_allowed(self, edit_server: ServeServer):
        r = edit_server.get("/api/preview")
        assert r.status_code == 405

    def test_strips_frontmatter(self, edit_server: ServeServer):
        src = "---\ntitle: Test\n---\n\n# Body"
        r = edit_server.post("/api/preview", json={"content": src})
        html = r.json()["html"]
        assert "title: Test" not in html
        assert "Body" in html

    def test_not_a_full_html_page(self, edit_server: ServeServer):
        r = edit_server.post("/api/preview", json={"content": "# Hello"})
        html = r.json()["html"]
        # Should be a fragment, not a full page
        assert "<!DOCTYPE" not in html
        assert "<html" not in html


# ---------------------------------------------------------------------------
# Edit button in rendered pages
# ---------------------------------------------------------------------------


class TestEditButtonInPage:
    def test_edit_button_present_for_markdown(self, edit_server: ServeServer):
        r = edit_server.get("/")
        assert "serve-edit-btn" in r.text

    def test_edit_button_present_for_txt(self, txt_server: ServeServer):
        r = txt_server.get("/notes.txt")
        assert r.status_code == 200
        assert "serve-edit-btn" in r.text

    def test_editor_elements_present_for_markdown(self, edit_server: ServeServer):
        r = edit_server.get("/")
        assert "serve-editor" in r.text
        assert "serve-editor-textarea" in r.text
        assert "serve-editor-split-toggle" in r.text

    def test_edit_api_js_present(self, edit_server: ServeServer):
        r = edit_server.get("/")
        assert "/api/edit" in r.text or "api/edit" in r.text


# ---------------------------------------------------------------------------
# Directory mode
# ---------------------------------------------------------------------------


class TestEditInDirectoryMode:
    def test_get_file_in_dir_mode(self, dir_server: ServeServer, dir_tree: Path):
        # Find a .md file in the directory
        md_files = list(dir_tree.glob("*.md"))
        assert md_files, "No .md files in dir fixture"
        rel = md_files[0].name
        r = dir_server.get(f"/api/file?file={rel}")
        assert r.status_code == 200
        assert "content" in r.json()

    def test_edit_file_in_dir_mode(self, dir_server: ServeServer, dir_tree: Path):
        md_files = list(dir_tree.glob("*.md"))
        assert md_files
        rel = md_files[0].name
        original = md_files[0].read_text()

        new_content = original + "\n\n<!-- edited -->\n"
        r = dir_server.post(f"/api/edit?file={rel}", json={"content": new_content})
        assert r.status_code == 200

        time.sleep(0.05)
        assert md_files[0].read_text() == new_content

        # Restore original
        md_files[0].write_text(original)

    def test_path_traversal_rejected(self, dir_server: ServeServer):
        r = dir_server.post(
            "/api/edit?file=../../etc/passwd",
            json={"content": "pwned"},
        )
        assert r.status_code == 404

    def test_dir_mode_page_has_edit_button_for_md(
        self, dir_server: ServeServer, dir_tree: Path
    ):
        md_files = list(dir_tree.glob("*.md"))
        assert md_files
        rel = md_files[0].name
        r = dir_server.get(f"/{rel}")
        assert r.status_code == 200
        assert "serve-edit-btn" in r.text
