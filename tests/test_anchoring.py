"""
Browser tests for comment anchoring edge cases.

Exercises scenarios that can fail silently — the most dangerous class of bug,
because the user sees no error, just the wrong text highlighted (or nothing).

Fixture line numbers (anchoring.md, with pre-baked comment-id frontmatter):
  7  First occurrence: repeated phrase goes here in the first paragraph.
  9  Some unrelated middle content on this line.
  11 Second occurrence: repeated phrase goes here in the second paragraph.
  13 ## Formatting Section
  15 This paragraph has **bold content** inside it and _italic content_ too.
  17 Click [link text here](https://example.com) to go somewhere.
  21 ```python (code block, lines 21-24)
  22 def unique_function_name():
  28 | Column A | Column B |  (table, lines 28-31)
  30 | alpha    | beta     |

The comment-id is pre-baked so the server never injects frontmatter mid-test,
which would shift all source line numbers and break source-line scoping.
"""

import shutil
import socket

import httpx
import pytest
from playwright.sync_api import Page, expect

from conftest import FIXTURES_DIR, COMMENTS_DIR, ServeServer, _free_port, _start_server, _wait_ready


@pytest.fixture
def anchor_page(page: Page, anchoring_server: ServeServer):
    page.goto(f"{anchoring_server.base_url}/")
    page.wait_for_load_state("networkidle")
    return page


def post_comment(server: ServeServer, **kwargs) -> dict:
    payload = {"text": "Test comment", "anchor_text": "", "source_line_start": None, "source_line_end": None}
    payload.update(kwargs)
    r = server.post("/api/comments", json=payload)
    assert r.status_code == 200, r.text
    return r.json()


def reload(page: Page, server: ServeServer):
    page.goto(f"{server.base_url}/")
    page.wait_for_load_state("networkidle")


# ---------------------------------------------------------------------------
# Source-line scoping — the most critical guarantee
# ---------------------------------------------------------------------------

class TestSourceLineScoping:

    def test_first_occurrence_highlighted_when_scoped_to_line_7(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="repeated phrase goes here",
            source_line_start=7,
            source_line_end=7,
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        containing = mark.evaluate("el => el.closest('p, li, td, th, h1, h2, h3, h4, h5, h6').textContent")
        assert "First occurrence" in containing, (
            f"Expected mark inside first-occurrence paragraph, got: {containing!r}"
        )

    def test_second_occurrence_highlighted_when_scoped_to_line_11(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="repeated phrase goes here",
            source_line_start=11,
            source_line_end=11,
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        containing = mark.evaluate("el => el.closest('p, li, td, th, h1, h2, h3, h4, h5, h6').textContent")
        assert "Second occurrence" in containing, (
            f"Expected mark inside second-occurrence paragraph, got: {containing!r}"
        )

    def test_both_occurrences_highlighted_independently(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="repeated phrase goes here",
            source_line_start=7,
            source_line_end=7,
        )
        post_comment(
            anchoring_server,
            anchor_text="repeated phrase goes here",
            source_line_start=11,
            source_line_end=11,
        )
        reload(page, anchoring_server)
        expect(page.locator("mark.comment-highlight")).to_have_count(2, timeout=4000)


# ---------------------------------------------------------------------------
# Inline formatting — cross-element wrapping
# ---------------------------------------------------------------------------

class TestInlineFormattingAnchoring:

    def test_bold_text_anchor(self, page: Page, anchoring_server: ServeServer):
        post_comment(
            anchoring_server,
            anchor_text="bold content",
            source_line_start=15,
            source_line_end=15,
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        # The strong tag should still be present in the document (not destroyed by extraction)
        expect(page.locator("strong")).to_have_count(1)

    def test_italic_text_anchor(self, page: Page, anchoring_server: ServeServer):
        post_comment(
            anchoring_server,
            anchor_text="italic content",
            source_line_start=15,
            source_line_end=15,
        )
        reload(page, anchoring_server)
        expect(page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)
        # The em tag should still be intact
        expect(page.locator("em")).to_have_count(1)

    def test_anchor_spanning_plain_text_and_bold(
        self, page: Page, anchoring_server: ServeServer
    ):
        # "paragraph has **bold content**" crosses a text→strong boundary
        post_comment(
            anchoring_server,
            anchor_text="paragraph has bold content",
            source_line_start=15,
            source_line_end=15,
        )
        reload(page, anchoring_server)
        # Should produce at least one mark (may split across nodes)
        marks = page.locator("mark.comment-highlight")
        expect(marks).not_to_have_count(0, timeout=4000)
        # DOM must not be broken: strong should still exist somewhere
        expect(page.locator("strong")).to_have_count(1)


# ---------------------------------------------------------------------------
# Link text anchoring
# ---------------------------------------------------------------------------

class TestLinkTextAnchoring:

    def test_comment_on_link_text(self, page: Page, anchoring_server: ServeServer):
        post_comment(
            anchoring_server,
            anchor_text="link text here",
            source_line_start=17,
            source_line_end=17,
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        # The anchor tag href must still be present (link not broken)
        expect(page.locator('a[href="https://example.com"]')).to_have_count(1)


# ---------------------------------------------------------------------------
# Code block anchoring
# ---------------------------------------------------------------------------

class TestCodeBlockAnchoring:

    def test_comment_on_code_content(self, page: Page, anchoring_server: ServeServer):
        post_comment(
            anchoring_server,
            anchor_text="unique_function_name",
            source_line_start=22,
            source_line_end=22,
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        # Mark must be inside the pre/code block, not outside it
        is_in_code = mark.evaluate(
            "el => el.closest('pre, code') !== null"
        )
        assert is_in_code, "Highlight should be inside the code block"


# ---------------------------------------------------------------------------
# Table cell anchoring
# ---------------------------------------------------------------------------

class TestTableCellAnchoring:

    def test_comment_on_table_cell(self, page: Page, anchoring_server: ServeServer):
        post_comment(
            anchoring_server,
            anchor_text="alpha",
            # "alpha" only appears once, no source_line scoping needed
        )
        reload(page, anchoring_server)
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=4000)
        is_in_cell = mark.evaluate("el => el.closest('td, th') !== null")
        assert is_in_cell, "Highlight should be inside a table cell"

    def test_table_cell_highlighted_after_live_create(
        self, page: Page, anchoring_server: ServeServer
    ):
        """Comment created while the page is already open (no page reload) must highlight.

        This is the user's real workflow: page is open → create comment via browser →
        WebSocket fires comments-updated → softReload fetches fresh HTML with span
        markers → __refreshComments applies highlights.
        """
        # Load first so the page is already open
        page.goto(f"{anchoring_server.base_url}/")
        page.wait_for_load_state("networkidle")

        # Create comment via API while page is open — simulates browser UI
        # "alpha" is in the same row as "beta" (shorter); the span-marker approach
        # must route to the "alpha" cell, not the "beta" cell.
        post_comment(
            anchoring_server,
            anchor_text="alpha",
            source_line_start=30,
            source_line_end=30,
        )

        # Wait for the soft reload + highlight (no page.goto — must work via WS)
        page.wait_for_function(
            "() => document.querySelectorAll('mark.comment-highlight').length > 0",
            timeout=6000,
        )
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=2000)
        is_in_cell = mark.evaluate("el => el.closest('td, th') !== null")
        assert is_in_cell, "Highlight must be inside a table cell, not orphaned"
        cell_text = mark.evaluate("el => el.closest('td, th').textContent.trim()")
        assert "alpha" in cell_text, f"Expected highlight in 'alpha' cell, got: {cell_text!r}"

    def test_cell_boundary_prevents_cross_cell_match(
        self, page: Page, anchoring_server: ServeServer
    ):
        # "alpha beta" spans two cells; the \x00 boundary should prevent matching
        post_comment(anchoring_server, anchor_text="alpha    beta")
        reload(page, anchoring_server)
        # Should produce zero highlights (cross-cell match blocked) or fall to orphaned
        marks = page.locator("mark.comment-highlight")
        count = marks.count()
        # If it does highlight, it must be inside a single cell (not spanning two)
        if count > 0:
            is_in_single_cell = marks.first.evaluate(
                "el => el.closest('td, th') !== null"
            )
            assert is_in_single_cell, "Highlight should not span cell boundaries"


# ---------------------------------------------------------------------------
# Orphaned comments — silent failure detection
# ---------------------------------------------------------------------------

class TestCrossCellComments:
    """A comment spanning two columns must not restructure the table.

    Goldmark emits a newline between </td> and <td>, so that whitespace is a
    text node parented by the <tr>. Wrapping it in a <mark> puts phrasing
    content directly inside the row, and the browser answers by rendering an
    anonymous extra cell: the row gains a column and the real content shifts
    into it.
    """

    SELECT_ACROSS_CELLS = """() => {
        const tds = Array.from(document.querySelectorAll('td'));
        const a = tds.find(t => t.textContent.trim() === 'alpha');
        const b = tds.find(t => t.textContent.trim() === 'beta');
        const r = document.createRange();
        r.setStart(a.firstChild, 0);
        r.setEnd(b.firstChild, b.firstChild.length);
        const s = window.getSelection();
        s.removeAllRanges();
        s.addRange(r);
        document.dispatchEvent(new MouseEvent('mouseup', {bubbles: true}));
    }"""

    def _comment_across_two_cells(self, page: Page):
        page.evaluate(self.SELECT_ACROSS_CELLS)
        page.wait_for_timeout(300)
        page.click("#comment-btn")
        page.wait_for_selector(".comment-form textarea", timeout=4000)
        page.fill(".comment-form textarea", "spans two cells")
        page.keyboard.press("Control+Enter")
        page.wait_for_selector("mark.comment-highlight", timeout=4000)
        page.wait_for_timeout(400)

    def _cells_per_row(self, page: Page) -> list:
        return page.evaluate(
            "() => Array.from(document.querySelectorAll('table tr')).map(r => r.children.length)"
        )

    def test_row_keeps_its_cell_count(self, anchor_page: Page):
        before = self._cells_per_row(anchor_page)
        self._comment_across_two_cells(anchor_page)
        assert self._cells_per_row(anchor_page) == before, (
            "commenting across two columns changed the table's shape"
        )

    def test_no_mark_is_a_direct_child_of_a_row(self, anchor_page: Page):
        self._comment_across_two_cells(anchor_page)
        stray = anchor_page.evaluate(
            "() => document.querySelectorAll('tr > mark, tbody > mark, thead > mark, table > mark').length"
        )
        assert stray == 0, "a <mark> was placed where only cells are allowed"

    def test_both_cells_are_highlighted(self, anchor_page: Page):
        self._comment_across_two_cells(anchor_page)
        texts = anchor_page.evaluate(
            "() => Array.from(document.querySelectorAll('mark.comment-highlight'))"
            ".map(m => m.textContent.trim())"
        )
        assert "alpha" in texts and "beta" in texts, (
            f"both cells should carry the highlight, got {texts}"
        )
        in_cells = anchor_page.evaluate(
            "() => Array.from(document.querySelectorAll('mark.comment-highlight'))"
            ".every(m => m.closest('td, th') !== null)"
        )
        assert in_cells, "every highlight must sit inside a cell"

    def test_survives_a_reload(self, anchor_page: Page, anchoring_server: ServeServer):
        before = self._cells_per_row(anchor_page)
        self._comment_across_two_cells(anchor_page)
        reload(anchor_page, anchoring_server)
        anchor_page.wait_for_selector("mark.comment-highlight", timeout=4000)
        assert self._cells_per_row(anchor_page) == before


class TestOrphanedComments:

    def test_nonexistent_anchor_creates_no_mark(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="xyzzy_this_text_does_not_exist_anywhere",
        )
        reload(page, anchoring_server)
        # No mark should appear in the document
        expect(page.locator("mark.comment-highlight")).to_have_count(0)

    def test_nonexistent_anchor_comment_still_tracked(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="xyzzy_this_text_does_not_exist_anywhere",
            text="Orphan comment",
        )
        reload(page, anchoring_server)
        # The comment badge should still count the orphaned comment
        badge = page.locator("#comment-badge")
        expect(badge).to_be_visible(timeout=4000)

    def test_orphaned_comment_shown_in_panel(
        self, page: Page, anchoring_server: ServeServer
    ):
        post_comment(
            anchoring_server,
            anchor_text="xyzzy_this_text_does_not_exist_anywhere",
            text="Orphan comment text",
        )
        reload(page, anchoring_server)
        # Click badge to open panel and look for orphaned section
        badge = page.locator("#comment-badge")
        expect(badge).to_be_visible(timeout=4000)
        badge.click()
        page.wait_for_timeout(300)
        # Either the orphaned-comments section or the comment text should be visible
        body_text = page.locator("body").inner_text()
        assert "Orphan comment text" in body_text or "orphan" in body_text.lower()


# ---------------------------------------------------------------------------
# Directory-mode anchoring — the user's real workflow
# ---------------------------------------------------------------------------

@pytest.fixture()
def anchoring_dir_server(tmp_path):
    """Serve anchoring.md via directory mode (matching the typical user workflow)."""
    d = tmp_path / "docs"
    d.mkdir()
    shutil.copy(FIXTURES_DIR / "anchoring.md", d / "anchoring.md")
    port = _free_port()
    proc, base_url = _start_server(str(d), port)
    doc_ids: list[str] = []
    server = ServeServer(base_url, port, proc, doc_ids)
    yield server
    proc.terminate()
    proc.wait(timeout=10)
    import re
    content = (d / "anchoring.md").read_text()
    m = re.search(r"comment-id:\s*([a-f0-9]+)", content)
    if m:
        (COMMENTS_DIR / f"{m.group(1)}.json").unlink(missing_ok=True)


def post_dir_comment(server: ServeServer, file_path: str, **kwargs) -> dict:
    """Post a comment in directory mode (requires ?file= param)."""
    payload = {"text": "Test comment", "anchor_text": "", "source_line_start": None, "source_line_end": None}
    payload.update(kwargs)
    r = server.post(f"/api/comments?file={file_path}", json=payload)
    assert r.status_code == 200, r.text
    return r.json()


class TestDirectoryModeAnchoring:

    def test_table_cell_highlighted_after_live_create_dir_mode(
        self, page: Page, anchoring_dir_server: ServeServer
    ):
        """Directory mode: comment created while page is open anchors correctly.

        This is the user's exact workflow — directory mode sidebar, comment
        submitted via browser, soft reload via WebSocket, highlight appears.
        """
        page.goto(f"{anchoring_dir_server.base_url}/anchoring.md")
        page.wait_for_load_state("networkidle")

        # Create comment via API while page is open (no page.goto after)
        post_dir_comment(
            anchoring_dir_server,
            file_path="anchoring.md",
            anchor_text="alpha",
            source_line_start=30,
            source_line_end=30,
        )

        # soft reload fires via WebSocket comments-updated → highlight should appear
        page.wait_for_function(
            "() => document.querySelectorAll('mark.comment-highlight').length > 0",
            timeout=6000,
        )
        mark = page.locator("mark.comment-highlight").first
        expect(mark).to_be_visible(timeout=2000)
        is_in_cell = mark.evaluate("el => el.closest('td, th') !== null")
        assert is_in_cell, "Highlight must be inside a table cell in directory mode"
        cell_text = mark.evaluate("el => el.closest('td, th').textContent.trim()")
        assert "alpha" in cell_text, f"Expected 'alpha' cell, got: {cell_text!r}"


# ---------------------------------------------------------------------------
# Anchor survives document edits that shift line numbers
# ---------------------------------------------------------------------------

class TestAnchorSurvivesLineShift:
    def test_repeated_anchor_stays_on_its_block_after_lines_inserted(
        self, page: Page, tmp_path
    ):
        """A comment on the 2nd occurrence of a repeated phrase must stay there
        after the document is edited above it (which makes the stored source
        line stale). Without block_text disambiguation the highlight jumps to
        the first occurrence — the bug this guards against.
        """
        d = tmp_path / "docs"
        d.mkdir()
        f = d / "doc.md"
        f.write_text(
            "# Title\n\n"
            "The quick brown fox in paragraph one.\n\n"
            "The quick brown fox in paragraph two.\n"
        )
        before = set(COMMENTS_DIR.glob("*.json")) if COMMENTS_DIR.exists() else set()
        port = _free_port()
        proc, base_url = _start_server(str(f), port)
        try:
            # Comment the SECOND occurrence; block_text uniquely identifies it.
            r = httpx.post(
                f"{base_url}/api/comments?file=doc.md",
                json={
                    "text": "on two",
                    "anchor_text": "quick brown fox",
                    "block_text": "The quick brown fox in paragraph two.",
                    "source_line_start": 5,
                    "source_line_end": 5,
                },
            )
            assert r.status_code == 200

            page.goto(f"{base_url}/doc.md")
            page.wait_for_load_state("networkidle")
            page.wait_for_function(
                "() => document.querySelectorAll('mark.comment-highlight').length > 0",
                timeout=6000,
            )
            block = page.locator("mark.comment-highlight").first.evaluate(
                "el => el.closest('p').textContent"
            )
            assert "paragraph two" in block, f"initial anchor wrong: {block!r}"

            # Edit: insert paragraphs ABOVE, shifting the commented line down so
            # the stored source_line_start (5) is now stale.
            f.write_text(
                "# Title\n\n"
                "Inserted one.\n\nInserted two.\n\nInserted three.\n\n"
                "The quick brown fox in paragraph one.\n\n"
                "The quick brown fox in paragraph two.\n"
            )
            page.wait_for_timeout(2000)  # soft reload + re-anchor via WebSocket

            marks = page.locator("mark.comment-highlight")
            expect(marks).to_have_count(1, timeout=4000)
            after = marks.first.evaluate("el => el.closest('p').textContent")
            assert "paragraph two" in after, f"anchor drifted after edit: {after!r}"
        finally:
            proc.terminate()
            proc.wait(timeout=10)
            for jf in (set(COMMENTS_DIR.glob("*.json")) - before):
                jf.unlink(missing_ok=True)
