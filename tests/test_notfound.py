"""
Black-box tests for the not-found and directory-index pages.

A document that moves on disk mid-session used to leave the browser on a bare
"Not Found" body with nothing to click. These tests pin the recovery path: the
404 page keeps the sidebar, names where the file probably went, and lists the
nearest folder that still exists.
"""

import time
from contextlib import contextmanager
from pathlib import Path

import httpx

from conftest import ServeServer, _free_port, _start_server


@contextmanager
def serving(target: Path):
    """Run a server on an arbitrary target for the length of one test."""
    proc, base_url = _start_server(str(target), _free_port())
    try:
        yield base_url
    finally:
        proc.terminate()
        proc.wait(timeout=10)


class TestMissingFilePage:
    def test_missing_file_returns_404_html(self, dir_server: ServeServer):
        r = dir_server.get("/nope.md")
        assert r.status_code == 404
        assert "text/html" in r.headers.get("content-type", "")
        assert "Not found" in r.text

    def test_missing_file_page_keeps_the_sidebar(self, dir_server: ServeServer):
        r = dir_server.get("/nope.md")
        assert 'id="serve-sidebar"' in r.text
        assert "__serveFileTree" in r.text

    def test_missing_file_page_lists_the_folder(self, dir_server: ServeServer):
        r = dir_server.get("/nope.md")
        assert 'href="/README.md"' in r.text
        assert 'href="/sub"' in r.text

    def test_missing_file_in_missing_folder_falls_back_to_root(
        self, dir_server: ServeServer
    ):
        r = dir_server.get("/gone/deeper/nope.md")
        assert r.status_code == 404
        assert 'href="/README.md"' in r.text

    def test_no_serve_content_so_reload_does_a_full_load(
        self, dir_server: ServeServer
    ):
        # The reload script swaps #serve-content when it is present; the 404 page
        # omits it so a returning file is rebuilt as a full document page.
        r = dir_server.get("/nope.md")
        assert 'id="serve-content"' not in r.text

    def test_raw_asset_request_stays_a_plain_404(self, dir_server: ServeServer):
        # An <img>/<embed> fetch is not navigation; it must not get a page the
        # browser would try to decode as an image.
        r = dir_server.get("/nope.png?raw=1")
        assert r.status_code == 404
        assert "serve-sidebar" not in r.text

    def test_moved_file_is_offered_at_its_new_path(
        self, dir_server: ServeServer, dir_tree: Path
    ):
        (dir_tree / "README.md").rename(dir_tree / "sub" / "README.md")
        time.sleep(0.3)  # let the watcher rebuild the tree
        r = dir_server.get("/README.md")
        assert r.status_code == 404
        assert "Did it move?" in r.text
        assert 'href="/sub/README.md"' in r.text
        assert "same filename, different folder" in r.text

    def test_renamed_file_is_offered_as_a_similar_name(
        self, dir_server: ServeServer, dir_tree: Path
    ):
        (dir_tree / "README.md").rename(dir_tree / "README-v2.md")
        time.sleep(0.3)
        r = dir_server.get("/README.md")
        assert r.status_code == 404
        assert 'href="/README-v2.md"' in r.text


class TestDirectoryIndex:
    def test_directory_path_lists_its_entries(self, dir_server: ServeServer):
        r = dir_server.get("/sub")
        assert r.status_code == 200
        assert 'href="/sub/page.md"' in r.text

    def test_directory_path_with_trailing_slash(self, dir_server: ServeServer):
        r = dir_server.get("/sub/")
        assert r.status_code == 200
        assert 'href="/sub/page.md"' in r.text


class TestServedFileMoves:
    def test_root_recovers_when_the_served_file_moves(self, md_file: Path):
        # `serve file.md` renders that file at "/". When it moves, "/" must
        # still be navigable and point at the new location.
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        try:
            assert httpx.get(f"{base_url}/").status_code == 200

            moved_dir = md_file.parent / "moved"
            moved_dir.mkdir()
            md_file.rename(moved_dir / md_file.name)
            time.sleep(0.3)

            r = httpx.get(f"{base_url}/")
            assert r.status_code == 404
            assert f'href="/moved/{md_file.name}"' in r.text
            assert 'id="serve-sidebar"' in r.text
        finally:
            proc.terminate()
            proc.wait(timeout=10)


class TestRootWithoutADefaultFile:
    def test_root_lists_the_folder(self, tmp_path: Path):
        # No README, no index, no markdown: the root still has to be navigable.
        (tmp_path / "code.py").write_text("x = 1\n")
        (tmp_path / "notes.txt").write_text("hi\n")
        with serving(tmp_path) as base_url:
            r = httpx.get(f"{base_url}/")
            assert r.status_code == 200
            assert 'href="/code.py"' in r.text
            assert 'href="/notes.txt"' in r.text

    def test_empty_folder_says_so(self, tmp_path: Path):
        (tmp_path / "README.md").write_text("# Hi\n")
        (tmp_path / "hollow").mkdir()
        with serving(tmp_path) as base_url:
            r = httpx.get(f"{base_url}/hollow")
            assert r.status_code == 200
            assert "This folder is empty" in r.text
