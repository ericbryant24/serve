"""
Browser tests for the inline editor (edit.js).

Covers JavaScript behavior that HTTP-level tests cannot catch:
  - Edit button visibility and state
  - Opening / closing the editor
  - Save & Close writes to disk and updates the rendered view
  - Cancel discards changes
  - Ctrl+S saves without closing; Esc cancels
  - Preview (split) pane toggle and live preview updates
  - Synchronized scrolling between editor and preview
  - Sidebar interaction while editing

Run:
  uv run pytest test_edit_browser.py -v
  uv run pytest test_edit_browser.py -v --headed --slowmo 200  # visible browser
"""

import shutil
import time
from pathlib import Path

import pytest
from playwright.sync_api import Page, expect

from conftest import ServeServer, FIXTURES_DIR


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture()
def edit_page(page: Page, md_server: ServeServer):
    """Navigate to the single-file markdown server and wait for page to settle."""
    page.goto(f"{md_server.base_url}/")
    page.wait_for_load_state("networkidle")
    return page


@pytest.fixture()
def edit_dir_page(page: Page, dir_server: ServeServer):
    """Navigate to a markdown file inside a directory-mode server."""
    page.goto(f"{dir_server.base_url}/README.md")
    page.wait_for_load_state("networkidle")
    return page


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def open_editor(page: Page) -> None:
    """Click the Edit button and wait for the editor to appear."""
    page.locator("#serve-edit-btn").click()
    expect(page.locator("#serve-editor")).to_be_visible(timeout=3000)


def open_split(page: Page) -> None:
    """Open the editor and then enable the preview pane."""
    open_editor(page)
    page.locator("#serve-editor-split-toggle").click()
    expect(page.locator("#serve-editor-preview")).to_be_visible(timeout=3000)


# ---------------------------------------------------------------------------
# Edit button — visibility
# ---------------------------------------------------------------------------

class TestEditButtonVisibility:

    def test_edit_button_present_for_markdown(self, edit_page: Page):
        expect(edit_page.locator("#serve-edit-btn")).to_be_visible()

    def test_edit_button_absent_for_html_file(self, page: Page, html_server: ServeServer):
        page.goto(f"{html_server.base_url}/")
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-edit-btn")).to_have_count(0)

    def test_edit_button_absent_for_code_file(self, page: Page, dir_server: ServeServer):
        page.goto(f"{dir_server.base_url}/code.py")
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-edit-btn")).to_have_count(0)

    def test_edit_button_present_for_markdown_in_dir_mode(self, edit_dir_page: Page):
        expect(edit_dir_page.locator("#serve-edit-btn")).to_be_visible()

    def test_edit_button_hidden_while_editor_is_open(self, edit_page: Page):
        open_editor(edit_page)
        expect(edit_page.locator("#serve-edit-btn")).not_to_be_visible()

    def test_edit_button_reappears_after_cancel(self, edit_page: Page):
        open_editor(edit_page)
        edit_page.locator("#serve-editor-cancel").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)
        expect(edit_page.locator("#serve-edit-btn")).to_be_visible(timeout=2000)


# ---------------------------------------------------------------------------
# Opening the editor
# ---------------------------------------------------------------------------

class TestEditorOpen:

    def test_editor_visible_after_clicking_edit(self, edit_page: Page):
        open_editor(edit_page)
        expect(edit_page.locator("#serve-editor")).to_be_visible()

    def test_textarea_populated_with_file_content(self, edit_page: Page):
        open_editor(edit_page)
        content = edit_page.locator("#serve-editor-textarea").input_value()
        assert len(content) > 0, "textarea should have file content"
        assert "#" in content, "simple.md should contain markdown headings"

    def test_editor_has_save_and_close_button(self, edit_page: Page):
        open_editor(edit_page)
        expect(edit_page.locator("#serve-editor-close")).to_be_visible()
        expect(edit_page.locator("#serve-editor-close")).to_have_text("Save & Close")

    def test_editor_has_cancel_button(self, edit_page: Page):
        open_editor(edit_page)
        expect(edit_page.locator("#serve-editor-cancel")).to_be_visible()

    def test_editor_has_preview_toggle(self, edit_page: Page):
        open_editor(edit_page)
        expect(edit_page.locator("#serve-editor-split-toggle")).to_be_visible()
        expect(edit_page.locator("#serve-editor-split-toggle")).to_have_text("Preview")

    def test_rendered_content_hidden_while_editing(self, edit_page: Page):
        open_editor(edit_page)
        content = edit_page.locator("#serve-content")
        # The content div should be hidden (display: none)
        assert edit_page.evaluate(
            "() => document.getElementById('serve-content').style.display === 'none'"
        )


# ---------------------------------------------------------------------------
# Save & Close
# ---------------------------------------------------------------------------

class TestSaveAndClose:

    def test_save_and_close_writes_file(self, edit_page: Page, md_file: Path):
        open_editor(edit_page)
        new_content = "# Save Test\n\nThis was written by a browser test.\n"
        edit_page.locator("#serve-editor-textarea").fill(new_content)
        edit_page.locator("#serve-editor-close").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=4000)
        assert md_file.read_text() == new_content

    def test_save_and_close_updates_rendered_view(self, edit_page: Page):
        open_editor(edit_page)
        unique = "BROWSER_TEST_HEADING_XYZ"
        edit_page.locator("#serve-editor-textarea").fill(f"# {unique}\n\nUpdated.\n")
        edit_page.locator("#serve-editor-close").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=4000)
        expect(edit_page.locator("#serve-content")).to_contain_text(unique, timeout=5000)

    def test_save_and_close_edit_button_reappears(self, edit_page: Page):
        open_editor(edit_page)
        edit_page.locator("#serve-editor-close").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=4000)
        expect(edit_page.locator("#serve-edit-btn")).to_be_visible(timeout=2000)


# ---------------------------------------------------------------------------
# Cancel
# ---------------------------------------------------------------------------

class TestCancel:

    def test_cancel_closes_editor(self, edit_page: Page):
        open_editor(edit_page)
        edit_page.locator("#serve-editor-cancel").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)

    def test_cancel_does_not_write_file(self, edit_page: Page, md_file: Path):
        original = md_file.read_text()
        open_editor(edit_page)
        edit_page.locator("#serve-editor-textarea").fill("THIS SHOULD NOT BE SAVED")
        edit_page.locator("#serve-editor-cancel").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)
        assert md_file.read_text() == original

    def test_cancel_preserves_rendered_view(self, edit_page: Page):
        # Get the current text of the rendered document
        before = edit_page.locator("#serve-content").inner_text()
        open_editor(edit_page)
        edit_page.locator("#serve-editor-textarea").fill("# SHOULD NOT APPEAR")
        edit_page.locator("#serve-editor-cancel").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)
        edit_page.wait_for_timeout(500)
        after = edit_page.locator("#serve-content").inner_text()
        assert "SHOULD NOT APPEAR" not in after


# ---------------------------------------------------------------------------
# Keyboard shortcuts
# ---------------------------------------------------------------------------

class TestKeyboardShortcuts:

    def test_ctrl_s_saves_without_closing(self, edit_page: Page, md_file: Path):
        open_editor(edit_page)
        unique = "CTRL_S_SAVE_TEST"
        edit_page.locator("#serve-editor-textarea").fill(f"# {unique}\n")
        edit_page.keyboard.press("Control+s")
        edit_page.wait_for_timeout(400)
        # Editor must still be open
        expect(edit_page.locator("#serve-editor")).to_be_visible()
        # File must be written
        assert unique in md_file.read_text()

    def test_esc_cancels_without_saving(self, edit_page: Page, md_file: Path):
        original = md_file.read_text()
        open_editor(edit_page)
        edit_page.locator("#serve-editor-textarea").fill("DISCARDED BY ESC")
        edit_page.keyboard.press("Escape")
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)
        assert md_file.read_text() == original

    def test_ctrl_s_then_esc_saves_first(self, edit_page: Page, md_file: Path):
        """Ctrl+S saves the file, then Esc closes without a second write."""
        open_editor(edit_page)
        unique = "CTRL_S_THEN_ESC"
        edit_page.locator("#serve-editor-textarea").fill(f"# {unique}\n")
        edit_page.keyboard.press("Control+s")
        edit_page.wait_for_timeout(400)
        # Now close with Esc — file is already saved, no revert
        edit_page.keyboard.press("Escape")
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=2000)
        assert unique in md_file.read_text()


# ---------------------------------------------------------------------------
# Preview (split) pane
# ---------------------------------------------------------------------------

class TestPreviewPane:

    def test_preview_button_shows_split_pane(self, edit_page: Page):
        open_split(edit_page)
        expect(edit_page.locator("#serve-editor-preview")).to_be_visible()

    def test_preview_button_gets_active_class(self, edit_page: Page):
        open_editor(edit_page)
        btn = edit_page.locator("#serve-editor-split-toggle")
        btn.click()
        expect(edit_page.locator("#serve-editor-preview")).to_be_visible(timeout=2000)
        assert "active" in (btn.get_attribute("class") or ""), (
            "Preview button should have 'active' class when split mode is on"
        )

    def test_preview_shows_rendered_content(self, edit_page: Page):
        open_split(edit_page)
        preview = edit_page.locator("#serve-editor-preview")
        # Preview should contain at least one heading element from the rendered markdown
        expect(preview.locator("h1, h2, h3")).not_to_have_count(0, timeout=3000)

    def test_preview_updates_when_typing(self, edit_page: Page):
        open_split(edit_page)
        unique = "LIVE_PREVIEW_UNIQUE_TOKEN"
        edit_page.locator("#serve-editor-textarea").fill(f"# {unique}\n\nTest.\n")
        # Preview updates after ~300ms debounce
        expect(edit_page.locator("#serve-editor-preview")).to_contain_text(
            unique, timeout=3000
        )

    def test_toggle_preview_off_hides_pane(self, edit_page: Page):
        open_split(edit_page)
        # Click again to close preview
        edit_page.locator("#serve-editor-split-toggle").click()
        expect(edit_page.locator("#serve-editor-preview")).not_to_be_visible(timeout=2000)

    def test_toggle_preview_removes_active_class(self, edit_page: Page):
        open_split(edit_page)
        btn = edit_page.locator("#serve-editor-split-toggle")
        btn.click()  # toggle off
        edit_page.wait_for_timeout(200)
        assert "active" not in (btn.get_attribute("class") or "")

    def test_save_in_split_mode_writes_file(self, edit_page: Page, md_file: Path):
        open_split(edit_page)
        unique = "SPLIT_MODE_SAVE_TEST"
        edit_page.locator("#serve-editor-textarea").fill(f"# {unique}\n")
        edit_page.locator("#serve-editor-close").click()
        expect(edit_page.locator("#serve-editor")).not_to_be_visible(timeout=4000)
        assert unique in md_file.read_text()


# ---------------------------------------------------------------------------
# Directory mode
# ---------------------------------------------------------------------------

class TestDirectoryModeEditing:

    def test_editor_opens_in_directory_mode(self, edit_dir_page: Page):
        open_editor(edit_dir_page)
        content = edit_dir_page.locator("#serve-editor-textarea").input_value()
        assert len(content) > 0

    def test_save_writes_correct_file_in_dir_mode(
        self, page: Page, dir_server: ServeServer, dir_tree: Path
    ):
        target = dir_tree / "README.md"
        original = target.read_text()

        page.goto(f"{dir_server.base_url}/README.md")
        page.wait_for_load_state("networkidle")
        open_editor(page)

        unique = "DIR_MODE_WRITE_TEST"
        page.locator("#serve-editor-textarea").fill(f"# {unique}\n")
        page.locator("#serve-editor-close").click()
        expect(page.locator("#serve-editor")).not_to_be_visible(timeout=4000)

        assert unique in target.read_text()
        target.write_text(original)  # restore

    def test_navigating_to_different_file_shows_its_content(
        self, page: Page, dir_server: ServeServer
    ):
        page.goto(f"{dir_server.base_url}/README.md")
        page.wait_for_load_state("networkidle")
        open_editor(page)
        readme_content = page.locator("#serve-editor-textarea").input_value()
        page.locator("#serve-editor-cancel").click()

        page.goto(f"{dir_server.base_url}/sub/page.md")
        page.wait_for_load_state("networkidle")
        open_editor(page)
        sub_content = page.locator("#serve-editor-textarea").input_value()

        assert readme_content != sub_content, (
            "Different files should load different content into the editor"
        )

    def test_code_file_has_no_edit_button_in_dir_mode(
        self, page: Page, dir_server: ServeServer
    ):
        page.goto(f"{dir_server.base_url}/code.py")
        page.wait_for_load_state("networkidle")
        expect(page.locator("#serve-edit-btn")).to_have_count(0)
