"""
Browser automation tests using Playwright.
These test JavaScript behavior that HTTP response tests cannot catch:
  - Link clicks are not intercepted by the comment UI
  - Comment button appears on text selection
  - Submitting a comment creates a highlight in the document
  - Clicking a highlight opens the thread popover
  - Resolving a comment via the popover marks the highlight resolved

Run:
  uv run pytest tests/test_browser.py -v

For a visible browser (useful when debugging):
  uv run pytest tests/test_browser.py -v --headed --slowmo 300
"""

import re
import pytest
from playwright.sync_api import Page, expect

from conftest import ServeServer, make_comment


# ---------------------------------------------------------------------------
# Browser-level fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def md_page(page: Page, md_server: ServeServer):
    """Navigate to the markdown server and wait for the page to settle."""
    page.goto(f"{md_server.base_url}/")
    page.wait_for_load_state("networkidle")
    return page


@pytest.fixture
def dir_page(page: Page, dir_server: ServeServer):
    """Navigate to the directory server root and wait for the page to settle."""
    page.goto(f"{dir_server.base_url}/")
    page.wait_for_load_state("networkidle")
    return page


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def select_text_in_first_paragraph(page: Page) -> None:
    """Programmatically select the first ~15 chars of the first annotated paragraph."""
    page.evaluate("""
        () => {
            // Find the first paragraph with source-line annotation
            var p = document.querySelector('p[data-source-lines]') || document.querySelector('p');
            if (!p) return;
            // Walk to first text node
            var node = p.firstChild;
            while (node && node.nodeType !== 3) node = node.nextSibling;
            if (!node || !node.textContent.trim()) return;
            var range = document.createRange();
            range.setStart(node, 0);
            range.setEnd(node, Math.min(15, node.length));
            var sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(range);
            // mousedown first (resets the comment button state), then mouseup to show it
            document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
            document.dispatchEvent(new MouseEvent('mouseup',  { bubbles: true }));
        }
    """)
    page.wait_for_timeout(200)  # comment button appears after a 10 ms setTimeout


# ---------------------------------------------------------------------------
# Link behaviour
# ---------------------------------------------------------------------------

class TestLinkBehavior:

    def test_external_link_is_in_dom(self, md_page: Page):
        expect(md_page.locator('a[href="https://example.com"]')).to_have_count(1)

    def test_external_link_click_not_prevented_by_js(self, md_page: Page):
        """Verify that no JS listener calls preventDefault() on the link click."""
        prevented = md_page.evaluate("""
            () => {
                var link = document.querySelector('a[href="https://example.com"]');
                if (!link) return 'no link';
                var prevented = null;
                // Capture phase: fires before any bubble-phase handlers
                link.addEventListener('click', function(e) {
                    prevented = e.defaultPrevented;
                    e.preventDefault();   // stop us from actually navigating away
                }, { capture: true, once: true });
                link.click();
                return prevented;
            }
        """)
        assert prevented is False, (
            f"External link click was prevented by JavaScript (defaultPrevented={prevented!r})"
        )

    def test_anchor_link_updates_url_fragment(self, md_page: Page):
        md_page.click('a[href="#section-two"]')
        md_page.wait_for_timeout(200)
        assert "#section-two" in md_page.url

    def test_relative_link_navigates_in_directory_mode(self, page: Page, dir_server: ServeServer):
        page.goto(f"{dir_server.base_url}/README.md")
        page.wait_for_load_state("networkidle")
        page.click('a[href="sub/page.md"]')
        page.wait_for_url("**/sub/page.md", timeout=5000)
        expect(page.locator("body")).to_contain_text("Nested Page")


# ---------------------------------------------------------------------------
# Comment UI — basic state
# ---------------------------------------------------------------------------

class TestCommentUIBasic:

    def test_comment_button_hidden_on_load(self, md_page: Page):
        expect(md_page.locator("#comment-btn")).not_to_be_visible()

    def test_comment_badge_present_in_dom(self, md_page: Page):
        expect(md_page.locator("#comment-badge")).to_have_count(1)

    def test_comment_button_appears_after_text_selection(self, md_page: Page):
        select_text_in_first_paragraph(md_page)
        expect(md_page.locator("#comment-btn")).to_be_visible(timeout=2000)

    def test_comment_button_hides_when_clicking_elsewhere(self, md_page: Page):
        select_text_in_first_paragraph(md_page)
        expect(md_page.locator("#comment-btn")).to_be_visible(timeout=2000)
        # Click on a blank part of the page (top-left corner is reliably outside the UI)
        md_page.mouse.click(5, 5)
        md_page.wait_for_timeout(150)
        expect(md_page.locator("#comment-btn")).not_to_be_visible()


# ---------------------------------------------------------------------------
# Comment workflow
# ---------------------------------------------------------------------------

class TestCommentWorkflow:

    def test_submit_comment_creates_highlight(self, md_page: Page):
        select_text_in_first_paragraph(md_page)
        md_page.click("#comment-btn")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("A test comment from Playwright")
        md_page.keyboard.press("Control+Enter")
        expect(md_page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)

    def test_comment_created_via_api_appears_as_highlight(
        self, page: Page, md_server: ServeServer
    ):
        make_comment(md_server, anchor_text="simple markdown document", text="API comment")
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")
        expect(page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)

    def test_highlight_click_opens_popover(self, page: Page, md_server: ServeServer):
        make_comment(md_server, anchor_text="simple markdown document", text="Popover text")
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")
        highlight = page.locator("mark.comment-highlight").first
        expect(highlight).to_be_visible(timeout=4000)
        highlight.click()
        popover = page.locator(".comment-popover")
        expect(popover).to_be_visible(timeout=2000)
        expect(popover).to_contain_text("Popover text")

    def test_resolve_button_marks_highlight_resolved(self, page: Page, md_server: ServeServer):
        make_comment(md_server, anchor_text="simple markdown document", text="Resolve me")
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")
        highlight = page.locator("mark.comment-highlight").first
        expect(highlight).to_be_visible(timeout=4000)
        highlight.click()
        popover = page.locator(".comment-popover")
        expect(popover).to_be_visible(timeout=2000)
        resolve_btn = popover.locator("[data-action='resolve']")
        resolve_btn.click()
        page.wait_for_timeout(500)
        expect(page.locator("mark.comment-highlight.resolved")).to_have_count(1, timeout=3000)

    def test_badge_count_updates_after_comment_created(self, md_page: Page):
        # Initially badge is hidden (no comments)
        badge = md_page.locator("#comment-badge")
        initial_style = badge.get_attribute("style") or ""
        assert "none" in initial_style or not badge.is_visible()

        select_text_in_first_paragraph(md_page)
        md_page.click("#comment-btn")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("Badge test comment")
        md_page.keyboard.press("Control+Enter")
        # Badge should become visible with a count
        expect(badge).to_be_visible(timeout=4000)
