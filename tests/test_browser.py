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


class TestPageLevelComments:
    def test_launcher_is_always_available(self, md_page: Page):
        expect(md_page.locator("#comment-page-btn")).to_be_visible()

    def test_creates_page_comment_and_shows_it_in_the_panel(self, md_page: Page):
        md_page.click("#comment-page-btn")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("This page needs an intro")
        md_page.keyboard.press("Control+Enter")

        # No highlight is created — the comment belongs to the whole document.
        expect(md_page.locator("#comment-badge")).to_be_visible(timeout=4000)
        expect(md_page.locator("mark.comment-highlight")).to_have_count(0)

        # Submitting opens the panel, where the item is labelled "Whole page".
        panel = md_page.locator("#comment-panel.open")
        expect(panel).to_be_visible(timeout=2000)
        item = panel.locator(".panel-comment-item.page-level")
        expect(item).to_have_count(1)
        assert "Whole page" in item.inner_text()
        assert "This page needs an intro" in item.inner_text()

    def test_page_comment_persists_with_scope(self, md_page: Page, md_server: ServeServer):
        md_page.click("#comment-page-btn")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("Scope check")
        md_page.keyboard.press("Control+Enter")
        expect(md_page.locator("#comment-badge")).to_be_visible(timeout=4000)

        comments = md_server.get("/api/comments").json()["comments"]
        assert [c["scope"] for c in comments] == ["page"]

    def test_thread_opens_from_the_panel(self, page: Page, md_server: ServeServer):
        md_server.post("/api/comments", json={"text": "Page note", "scope": "page"})
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")

        page.click("#comment-badge")
        page.click(".panel-comment-item.page-level")
        popover = page.locator(".comment-popover.page-level, .comment-popover")
        expect(popover.first).to_be_visible(timeout=3000)
        assert "Page note" in popover.first.inner_text()
        # A thread anchored to nothing must still land inside the viewport.
        box = popover.first.bounding_box()
        width = page.evaluate("() => window.innerWidth")
        assert box["x"] >= 0 and box["x"] + box["width"] <= width + 1

    def test_page_comment_is_not_listed_as_unanchored(self, page: Page, md_server: ServeServer):
        md_server.post("/api/comments", json={"text": "Page note", "scope": "page"})
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")
        expect(page.locator("#comment-badge")).to_be_visible(timeout=4000)
        expect(page.locator(".orphaned-comments")).to_have_count(0)


class TestCommentShortcut:
    """"c" opens a comment form: the whole page when nothing is selected, the
    selection when there is one."""

    def test_c_opens_a_page_comment_form(self, md_page: Page):
        md_page.locator("body").click(position={"x": 5, "y": 5})
        md_page.keyboard.press("c")
        form = md_page.locator(".comment-form")
        expect(form).to_be_visible(timeout=2000)
        assert "Comment on the whole page" in form.inner_text()

    def test_c_creates_a_page_scoped_comment(self, md_page: Page, md_server: ServeServer):
        md_page.keyboard.press("c")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("Shortcut page note")
        md_page.keyboard.press("Control+Enter")
        expect(md_page.locator("#comment-badge")).to_be_visible(timeout=4000)

        comments = md_server.get("/api/comments").json()["comments"]
        assert [(c["text"], c["scope"]) for c in comments] == [
            ("Shortcut page note", "page")
        ]

    def test_c_comments_on_the_selection_when_there_is_one(self, md_page: Page):
        select_text_in_first_paragraph(md_page)
        md_page.keyboard.press("c")
        form = md_page.locator(".comment-form")
        expect(form).to_be_visible(timeout=2000)
        # Anchored, not page-level: no whole-page label on the form.
        assert "Comment on the whole page" not in form.inner_text()

    def test_c_inside_the_comment_box_is_typed_not_swallowed(self, md_page: Page):
        md_page.keyboard.press("c")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.click()
        md_page.keyboard.type("cccc")
        expect(ta).to_have_value("cccc")
        # Still one form, not four.
        expect(md_page.locator(".comment-form")).to_have_count(1)

    def test_c_in_vim_search_is_typed_not_swallowed(self, md_page: Page):
        md_page.keyboard.press("Escape")  # vim mode on
        md_page.keyboard.press("/")       # search bar takes focus
        md_page.keyboard.type("cc")
        expect(md_page.locator("#vim-search-bar input")).to_have_value("cc")
        expect(md_page.locator(".comment-form")).to_have_count(0)

    def test_c_in_vim_visual_mode_comments_on_the_selection(self, md_page: Page):
        md_page.keyboard.press("Escape")  # vim mode on
        md_page.keyboard.press("v")       # select the block under the cursor
        md_page.keyboard.press("c")
        form = md_page.locator(".comment-form")
        expect(form).to_be_visible(timeout=2000)
        # vim.js handled the key, so the page-level handler stayed out of it.
        assert "Comment on the whole page" not in form.inner_text()
        expect(form).to_have_count(1)


class TestHideShowComments:

    def test_toggle_hides_and_shows_highlights(self, md_page: Page):
        # Create a comment so a highlight appears.
        select_text_in_first_paragraph(md_page)
        md_page.click("#comment-btn")
        ta = md_page.locator(".comment-form textarea")
        expect(ta).to_be_visible(timeout=2000)
        ta.fill("hide/show test")
        md_page.keyboard.press("Control+Enter")
        expect(md_page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)

        # The hide/show toggle lives in the comment panel header.
        md_page.click("#comment-badge")
        toggle = md_page.locator("#comment-hide-toggle")
        expect(toggle).to_be_visible(timeout=2000)

        # Hide: the mark stays in the DOM but its highlight background is cleared.
        toggle.click()
        md_page.wait_for_timeout(100)
        assert md_page.evaluate(
            "() => document.body.classList.contains('serve-comments-hidden')"
        ) is True
        # pointer-events (no CSS transition, unlike background) is the reliable
        # signal that the hidden styling took effect.
        pe = md_page.evaluate(
            "() => getComputedStyle(document.querySelector('mark.comment-highlight')).pointerEvents"
        )
        assert pe == "none", f"highlight not cleared (pointer-events={pe!r})"

        # Show again.
        toggle.click()
        md_page.wait_for_timeout(100)
        assert md_page.evaluate(
            "() => document.body.classList.contains('serve-comments-hidden')"
        ) is False

    def test_hidden_state_persists_across_reload(self, page: Page, md_server: ServeServer):
        make_comment(md_server, anchor_text="simple markdown document", text="persist")
        page.goto(f"{md_server.base_url}/")
        page.wait_for_load_state("networkidle")
        expect(page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)

        page.click("#comment-badge")
        page.click("#comment-hide-toggle")
        page.wait_for_timeout(100)
        assert page.evaluate(
            "() => document.body.classList.contains('serve-comments-hidden')"
        ) is True

        # Reload: the hidden state (localStorage) is applied on load without
        # reopening the panel.
        page.reload()
        page.wait_for_load_state("networkidle")
        expect(page.locator("mark.comment-highlight")).to_have_count(1, timeout=4000)
        assert page.evaluate(
            "() => document.body.classList.contains('serve-comments-hidden')"
        ) is True


class TestFileActions:

    def test_base_dir_exposed(self, dir_page: Page):
        base = dir_page.evaluate("() => window.__serveBaseDir")
        assert isinstance(base, str) and base.startswith("/"), base

    def test_drag_carries_local_path_not_localhost(self, dir_page: Page):
        result = dir_page.evaluate(
            """() => {
              const a = document.querySelector('.sidebar-file');
              const dt = new DataTransfer();
              a.dispatchEvent(new DragEvent('dragstart', {dataTransfer: dt, bubbles: true, cancelable: true}));
              return {plain: dt.getData('text/plain'), uri: dt.getData('text/uri-list'), dl: dt.getData('DownloadURL')};
            }"""
        )
        # text/plain must be the local filesystem path, not a localhost URL.
        assert result["plain"].startswith("/"), result
        assert "localhost" not in result["plain"], result
        assert result["uri"].startswith("file://"), result
        # DownloadURL still points at the raw localhost URL so Finder/desktop
        # drops materialize a real file.
        assert "localhost" in result["dl"] and "dl=1" in result["dl"], result

    def test_context_menu_reveal_calls_api(self, dir_page: Page):
        # Intercept so a real Finder window never opens during the test.
        dir_page.route(
            "**/api/reveal**",
            lambda route: route.fulfill(
                status=200, content_type="application/json", body='{"ok":true}'
            ),
        )
        dir_page.locator(".sidebar-file.active").click(button="right")
        menu = dir_page.locator(".serve-context-menu")
        expect(menu).to_be_visible(timeout=2000)
        expect(menu).to_contain_text("Copy path")
        with dir_page.expect_request("**/api/reveal**") as ri:
            menu.get_by_text("Reveal in Finder").click()
        assert "path=" in ri.value.url

    def test_context_menu_copy_path(self, dir_page: Page):
        dir_page.context.grant_permissions(["clipboard-read", "clipboard-write"])
        dir_page.locator(".sidebar-file.active").click(button="right")
        menu = dir_page.locator(".serve-context-menu")
        expect(menu).to_be_visible(timeout=2000)
        menu.get_by_text("Copy path").click()
        expect(dir_page.locator(".serve-toast")).to_be_visible(timeout=2000)
        copied = dir_page.evaluate("() => navigator.clipboard.readText()")
        assert copied.startswith("/") and "localhost" not in copied, copied


class TestMovedDocumentRecovery:
    """The document being read moves on disk; the reader must land somewhere
    they can navigate from, and one click must get them to the new location."""

    def test_live_reload_lands_on_the_recovery_page(
        self, page: Page, dir_server: ServeServer, dir_tree
    ):
        page.goto(f"{dir_server.base_url}/README.md")
        page.wait_for_load_state("networkidle")

        (dir_tree / "README.md").rename(dir_tree / "sub" / "README.md")

        # The open page has content to swap, but the response no longer does, so
        # the reload script falls back to a full load of the not-found page.
        page.wait_for_selector(".serve-nf", timeout=5000)
        expect(page.locator('.serve-nf a[href="/sub/README.md"]')).to_be_visible()
        expect(page.locator("#serve-sidebar")).to_be_attached()

        page.click('.serve-nf a[href="/sub/README.md"]')
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-content")).to_be_attached()

    def test_way_up_finds_a_file_moved_out_of_the_served_folder(
        self, page: Page, dir_server: ServeServer, dir_tree
    ):
        # Moved above the served root, so nothing in the tree can point at it
        # until the server serves the parent folder.
        page.goto(f"{dir_server.base_url}/README.md")
        page.wait_for_load_state("networkidle")
        (dir_tree / "README.md").rename(dir_tree.parent / "README.md")

        page.wait_for_selector(".serve-nf", timeout=5000)
        expect(page.locator("#serve-nf-up")).to_be_visible()

        page.click("#serve-nf-up")
        page.wait_for_selector('.serve-nf a[href="/README.md"]', timeout=5000)
        page.click('.serve-nf a[href="/README.md"]')
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-content")).to_be_attached()
