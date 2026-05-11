"""
Black-box tests for page serving behavior.
Covers markdown rendering, HTML injection, directory mode, and data URL generation.
"""

from conftest import ServeServer


class TestMarkdownServing:
    def test_get_root_returns_200(self, md_server: ServeServer):
        r = md_server.get("/")
        assert r.status_code == 200

    def test_content_type_is_html(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "text/html" in r.headers.get("content-type", "")

    def test_markdown_content_rendered(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "Hello World" in r.text

    def test_data_source_lines_attributes_present(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "data-source-lines" in r.text

    def test_websocket_reload_script_present(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "/ws" in r.text

    def test_frontmatter_not_in_body_text(self, frontmatter_file):
        # frontmatter_file fixture already places the file in its own tmp_path
        from conftest import _free_port, _start_server
        import httpx
        port = _free_port()
        proc, base_url = _start_server(str(frontmatter_file), port)
        try:
            r = httpx.get(f"{base_url}/")
            assert "title: Document With Frontmatter" not in r.text
            assert "author: Test" not in r.text
            assert "Frontmatter Document" in r.text
        finally:
            proc.terminate()
            proc.wait(timeout=10)

    def test_code_block_rendered(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "greet" in r.text

    def test_mermaid_block_rendered_as_pre(self, md_server: ServeServer):
        r = md_server.get("/")
        assert 'class="mermaid"' in r.text

    def test_table_rendered(self, md_server: ServeServer):
        r = md_server.get("/")
        assert "<table" in r.text.lower()

    def test_table_has_source_line_annotations(self, md_server: ServeServer):
        r = md_server.get("/")
        # The <table> element must carry data-source-lines so comment anchoring works
        assert '<table data-source-lines=' in r.text

    def test_external_link_rendered(self, md_server: ServeServer):
        r = md_server.get("/")
        assert '<a href="https://example.com">' in r.text

    def test_anchor_link_rendered(self, md_server: ServeServer):
        r = md_server.get("/")
        assert '<a href="#section-two">' in r.text

    def test_heading_ids_present(self, md_server: ServeServer):
        """Anchor links only work if target headings have id attributes."""
        r = md_server.get("/")
        assert 'id="section-one"' in r.text
        assert 'id="section-two"' in r.text

    def test_heading_id_matches_anchor_link(self, md_server: ServeServer):
        """The id on the heading must match the href used in the anchor link."""
        r = md_server.get("/")
        # simple.md has [the section below](#section-two) → ## Section Two
        assert '<a href="#section-two">' in r.text
        assert 'id="section-two"' in r.text

    # NOTE: whether links are *clickable* in the browser depends on JavaScript
    # event handlers and cannot be verified by HTTP response tests alone.
    # Use browser automation (e.g. Playwright) to test click behavior.


class TestHtmlServing:
    def test_get_root_returns_200(self, html_server: ServeServer):
        r = html_server.get("/")
        assert r.status_code == 200

    def test_original_content_present(self, html_server: ServeServer):
        r = html_server.get("/")
        assert "Hello from HTML" in r.text
        assert "unique-marker" in r.text

    def test_reload_script_injected(self, html_server: ServeServer):
        r = html_server.get("/")
        assert "/ws" in r.text

    def test_content_type_is_html(self, html_server: ServeServer):
        r = html_server.get("/")
        assert "text/html" in r.headers.get("content-type", "")


class TestDataUrl:
    def test_data_url_endpoint_returns_200(self, md_server: ServeServer):
        r = md_server.get("/__data_url")
        assert r.status_code == 200

    def test_data_url_starts_with_scheme(self, md_server: ServeServer):
        r = md_server.get("/__data_url")
        body = r.text.strip()
        assert body.startswith("data:text/html;base64,")

    def test_data_url_is_valid_base64(self, md_server: ServeServer):
        import base64
        r = md_server.get("/__data_url")
        body = r.text.strip()
        _, b64 = body.split(",", 1)
        decoded = base64.b64decode(b64).decode("utf-8")
        assert "Hello World" in decoded


class TestDirectoryModeServing:
    def test_root_redirects_to_readme(self, dir_server: ServeServer):
        r = dir_server.get("/", follow_redirects=False)
        assert r.status_code in (301, 302)
        assert "README" in r.headers.get("location", "").upper() or "readme" in r.headers.get("location", "").lower()

    def test_readme_renders(self, dir_server: ServeServer):
        r = dir_server.get("/", follow_redirects=True)
        assert r.status_code == 200
        assert "Directory README" in r.text

    def test_nested_file_renders(self, dir_server: ServeServer):
        r = dir_server.get("/sub/page.md")
        assert r.status_code == 200
        assert "Nested Page" in r.text

    def test_code_file_renders(self, dir_server: ServeServer):
        r = dir_server.get("/code.py")
        assert r.status_code == 200
        assert "hello" in r.text.lower()

    def test_path_traversal_rejected(self, dir_server: ServeServer):
        r = dir_server.get("/../etc/passwd")
        assert r.status_code in (403, 404)

    def test_api_files_returns_tree(self, dir_server: ServeServer):
        r = dir_server.get("/api/files")
        assert r.status_code == 200
        data = r.json()
        assert "files" in data
        files = data["files"]
        assert isinstance(files, list)
        assert len(files) > 0

    def test_api_files_has_expected_shape(self, dir_server: ServeServer):
        r = dir_server.get("/api/files")
        files = r.json()["files"]

        def check_node(node):
            assert "name" in node
            assert "path" in node
            assert "type" in node
            assert node["type"] in ("file", "dir")
            for child in node.get("children", []):
                check_node(child)

        for node in files:
            check_node(node)

    def test_api_files_excludes_hidden_files(self, dir_server: ServeServer, dir_tree):
        hidden = dir_tree / ".hidden_file.md"
        hidden.write_text("hidden")
        r = dir_server.get("/api/files")
        all_names = []

        def collect(nodes):
            for n in nodes:
                all_names.append(n["name"])
                collect(n.get("children", []))

        collect(r.json()["files"])
        assert ".hidden_file.md" not in all_names

    def test_relative_links_preserved(self, dir_server: ServeServer):
        r = dir_server.get("/README.md")
        assert 'href="sub/page.md"' in r.text

    def test_external_links_in_dir_mode(self, dir_server: ServeServer):
        r = dir_server.get("/README.md")
        assert 'href="https://example.com"' in r.text

    def test_directory_mode_has_sidebar(self, dir_server: ServeServer):
        r = dir_server.get("/README.md")
        assert r.status_code == 200
        # sidebar is injected as a nav/aside or script that builds the tree
        body = r.text.lower()
        assert "sidebar" in body or "serve-sidebar" in body or "api/files" in body
