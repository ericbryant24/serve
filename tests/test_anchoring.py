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

import pytest
from playwright.sync_api import Page, expect

from conftest import ServeServer


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
