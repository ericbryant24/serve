"""
Tests for the directory-only serving model:
  - Serving a single file roots at its directory and renders it at "/".
  - The sidebar "go up a directory" control re-roots the running server.
"""

import time

import pytest
from playwright.sync_api import Page, expect

from conftest import ServeServer, _free_port, _start_server


@pytest.fixture
def nested_file_server(tmp_path):
    """Serve project/docs/guide.md — a file nested one level below the project."""
    proj = tmp_path / "project"
    (proj / "docs").mkdir(parents=True)
    (proj / "docs" / "guide.md").write_text("# Guide\n\nThe guide body text.\n")
    (proj / "docs" / "intro.md").write_text("# Intro\n\nIntro body.\n")
    (proj / "README.md").write_text("# Project Readme\n")
    port = _free_port()
    proc, base_url = _start_server(str(proj / "docs" / "guide.md"), port)
    server = ServeServer(base_url, port, proc, [])
    yield server
    proc.terminate()
    proc.wait(timeout=10)


class TestServeFileOpensDirectory:
    def test_root_renders_the_served_file(self, nested_file_server: ServeServer):
        r = nested_file_server.get("/")
        assert r.status_code == 200
        assert "The guide body text" in r.text

    def test_sidebar_present_and_lists_siblings(self, nested_file_server: ServeServer):
        names = [n["name"] for n in nested_file_server.get("/api/files").json()["files"]]
        assert "guide.md" in names
        assert "intro.md" in names  # sibling is visible in the tree

    def test_sibling_file_is_servable(self, nested_file_server: ServeServer):
        r = nested_file_server.get("/intro.md")
        assert r.status_code == 200
        assert "Intro body" in r.text


class TestReroot:
    def test_reroot_moves_root_up_one_level(self, nested_file_server: ServeServer):
        d = nested_file_server.post("/api/reroot").json()
        assert d["ok"] is True
        assert d["prefix"] == "docs"

        # The parent's contents are now visible in the tree.
        names = [n["name"] for n in nested_file_server.get("/api/files").json()["files"]]
        assert "README.md" in names
        assert "docs" in names

        # The originally-served file now lives under docs/ and "/" still shows it.
        under_docs = nested_file_server.get("/docs/guide.md")
        assert under_docs.status_code == 200
        assert "The guide body text" in under_docs.text
        assert "The guide body text" in nested_file_server.get("/").text

    def test_reroot_get_not_allowed(self, nested_file_server: ServeServer):
        assert nested_file_server.get("/api/reroot").status_code == 405


class TestUpButton:
    def test_up_button_reroots_and_keeps_current_file(
        self, page: Page, nested_file_server: ServeServer
    ):
        page.goto(f"{nested_file_server.base_url}/")
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-content")).to_contain_text("The guide body text")

        page.click("#serve-sidebar-up")
        # The tab navigates to the same file under the new (parent) root.
        page.wait_for_url("**/docs/guide.md", timeout=5000)
        expect(page.locator("#serve-content")).to_contain_text("The guide body text")

        # The sidebar now shows the parent directory's contents.
        names = page.eval_on_selector_all(
            "#serve-sidebar-tree .sidebar-file", "els => els.map(e => e.textContent)"
        )
        assert any("README" in (n or "") for n in names)


class TestReconnectResync:
    def test_sidebar_resyncs_after_server_restart(self, page: Page, tmp_path):
        """A tab self-heals after the server restarts (e.g. after a crash): on
        WebSocket reconnect it re-fetches the tree, so a change made while the
        server was down appears without a manual refresh.
        """
        d = tmp_path / "site"
        d.mkdir()
        (d / "README.md").write_text("# Root\n")
        (d / "a.md").write_text("# A\n")
        port = _free_port()
        proc, base_url = _start_server(str(d), port)
        try:
            page.goto(f"{base_url}/README.md")
            page.wait_for_load_state("networkidle")
            page.wait_for_selector("#serve-sidebar-tree .sidebar-file")

            # Simulate a crash: stop the server, change the tree while it's down.
            proc.terminate()
            proc.wait(timeout=10)
            time.sleep(0.5)  # let the port free up for reuse
            (d / "added_while_down.md").write_text("# Added\n")

            # Restart on the same port; the tab's WebSocket auto-reconnects.
            proc, base_url = _start_server(str(d), port)

            # Without a manual reload, the reconnect resync must surface the new file.
            page.wait_for_function(
                "() => Array.from(document.querySelectorAll('#serve-sidebar-tree .sidebar-file'))"
                ".some(a => (a.getAttribute('href') || '').endsWith('/added_while_down.md'))",
                timeout=10000,
            )
        finally:
            proc.terminate()
            proc.wait(timeout=10)
