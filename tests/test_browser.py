"""The browser app, driven with Playwright against a real server."""

import re
import struct
import time
import zlib

import pytest
from playwright.sync_api import Page, expect

from conftest import leaf_selection

pytestmark = pytest.mark.usefixtures("daemon")


@pytest.fixture()
def wide(page: Page) -> Page:
    page.set_viewport_size({"width": 1440, "height": 900})
    return page


def select(page: Page, phrase: str, root=".markdown-body"):
    page.evaluate(
        """([root, phrase]) => {
          const el = document.querySelector(root);
          const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
          let n; const nodes = []; let text = '';
          while ((n = w.nextNode())) { nodes.push([n, text.length]); text += n.data; }
          const i = text.indexOf(phrase); if (i < 0) throw new Error('not found: ' + phrase);
          const at = (off) => { for (const [n, s] of nodes) if (off <= s + n.data.length) return [n, off - s]; };
          const r = document.createRange(); r.setStart(...at(i)); r.setEnd(...at(i + phrase.length));
          getSelection().removeAllRanges(); getSelection().addRange(r);
        }""",
        [root, phrase],
    )


def post(page: Page, text: str):
    page.keyboard.type(text)
    page.keyboard.press("ControlOrMeta+Enter")


def open_doc(page: Page, daemon, path):
    page.goto(daemon.url(path))
    page.wait_for_selector(".markdown-body h1, .code-view .cl, iframe.frame")


def test_renders_markdown_with_margin(wide, daemon, docs):
    open_doc(wide, daemon, docs / "spec.md")
    expect(wide.locator(".markdown-body h1")).to_have_text("Payment retry spec")
    expect(wide.locator(".margin-empty")).to_contain_text("No open comments")
    expect(wide.locator(".tree-row.current")).to_have_text(re.compile("spec.md"))


def test_select_and_comment_then_agent_reply_arrives_live(wide, daemon, docs, cli):
    spec = docs / "spec.md"
    open_doc(wide, daemon, spec)
    select(wide, "up to three times")
    wide.keyboard.press("c")
    expect(wide.locator(".thread.draft")).to_be_visible()
    post(wide, "Why three?")
    expect(wide.locator("mark.cm")).to_have_text("up to three times")
    card = wide.locator(".margin .thread").first
    expect(card).to_contain_text("Why three?")
    # The card sits level with its highlight.
    wide.wait_for_timeout(400)
    mark_top = wide.locator("mark.cm").bounding_box()["y"]
    card_top = card.bounding_box()["y"]
    assert abs(card_top - mark_top) < 30
    tid = cli.json("comments", spec)["threads"][0]["id"]
    wide.locator("mark.cm").click()
    cli.run("reply", spec, tid, "Changed to four.", SERVE_AUTHOR="Claude")
    expect(card).to_contain_text("Changed to four.")
    expect(card).to_contain_text("Claude")
    expect(card.locator(".badge")).to_have_text("Your turn")


def test_reply_box_stays_open_and_threads_are_flat(wide, daemon, docs, cli):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "first", "selection": leaf_selection(daemon, spec, "ACH payments")})
    open_doc(wide, daemon, spec)
    wide.locator("mark.cm").click()
    box = wide.locator(".margin .thread textarea")
    box.click()
    post(wide, "second")
    expect(wide.locator(".margin .thread .message")).to_have_count(2)
    box.click()
    post(wide, "third")
    expect(wide.locator(".margin .thread .message")).to_have_count(3)
    msgs = cli.json("comments", spec)["threads"][0]["messages"]
    assert [m["text"] for m in msgs] == ["first", "second", "third"]


def test_file_edit_updates_the_page_in_place(wide, daemon, docs):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "Why three?", "selection": leaf_selection(daemon, spec, "up to three times")})
    open_doc(wide, daemon, spec)
    wide.evaluate("() => { window.__marker = 1; document.querySelector('.markdown-body h1').dataset.kept = 'yes'; }")
    spec.write_text(spec.read_text().replace("three", "four"))
    expect(wide.locator("mark.cm")).to_have_text("up to four times")
    expect(wide.locator(".margin .quote")).to_contain_text("up to three times")
    assert wide.evaluate("() => window.__marker === 1 && document.querySelector('.markdown-body h1').dataset.kept === 'yes'")


def png(w: int, h: int) -> bytes:
    """A blank PNG of the given size."""

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    rows = b"".join(b"\x00" + b"\x00\x00\x00" * w for _ in range(h))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b"")


def test_changed_image_updates_the_page(wide, daemon, docs):
    (docs / "img").mkdir()
    chart = docs / "img" / "chart.png"
    chart.write_bytes(png(10, 10))
    doc = docs / "report.md"
    doc.write_text("# Report\n\n![chart](img/chart.png)\n\nThe chart above.\n")
    open_doc(wide, daemon, doc)
    img = wide.locator(".markdown-body img")
    expect(img).to_have_js_property("naturalWidth", 10)
    chart.write_bytes(png(30, 10))
    expect(img).to_have_js_property("naturalWidth", 30)
    # A block replaced later still shows the new image, not the cached one.
    doc.write_text("# Report\n\n![chart, updated](img/chart.png)\n\nThe chart above.\n")
    expect(wide.locator(".markdown-body img")).to_have_attribute("alt", "chart, updated")
    expect(wide.locator(".markdown-body img")).to_have_js_property("naturalWidth", 30)


def test_changed_image_file_updates_its_view(wide, daemon, docs):
    pic = docs / "pic.png"
    pic.write_bytes(png(10, 10))
    wide.goto(daemon.url(pic))
    img = wide.locator(".image-view img")
    expect(img).to_have_js_property("naturalWidth", 10)
    pic.write_bytes(png(40, 10))
    expect(img).to_have_js_property("naturalWidth", 40)


def test_changed_stylesheet_reloads_an_html_page(wide, daemon, docs):
    (docs / "style.css").write_text("h1 { color: rgb(255, 0, 0); }")
    page = docs / "styled.html"
    page.write_text('<!doctype html><html><head><link rel="stylesheet" href="style.css"></head><body><h1>Styled</h1></body></html>')
    wide.goto(daemon.url(page))
    h1 = wide.frame_locator("iframe.frame").locator("h1")
    expect(h1).to_have_css("color", "rgb(255, 0, 0)")
    (docs / "style.css").write_text("h1 { color: rgb(0, 0, 255); }")
    expect(h1).to_have_css("color", "rgb(0, 0, 255)")


def tree_view(page: Page) -> dict:
    """The tree's scroll position, and whether the current file's row is in view."""
    return page.evaluate(
        """() => {
          const tree = document.querySelector('.sidebar .tree');
          const row = tree.querySelector('.tree-row.current');
          const r = row.getBoundingClientRect(), t = tree.getBoundingClientRect();
          return { scroll: tree.scrollTop, visible: r.top >= t.top && r.bottom <= t.bottom };
        }"""
    )


def test_tree_keeps_the_current_file_in_view(wide, daemon, docs):
    deep = docs / "zz" / "deep"
    deep.mkdir(parents=True)
    for i in range(60):
        (deep / f"f{i:02}.md").write_text(f"# File {i}\n")
    open_doc(wide, daemon, deep / "f59.md")
    expect(wide.locator(".tree-row.current")).to_have_text(re.compile("f59.md"))
    first = tree_view(wide)
    assert first["visible"] and first["scroll"] > 0
    # Following a row in the tree opens a new page with the tree where it was.
    wide.locator(".tree-row", has_text="f50.md").click()
    expect(wide.locator(".tree-row.current")).to_have_text(re.compile("f50.md"))
    second = tree_view(wide)
    assert second["visible"] and abs(second["scroll"] - first["scroll"]) <= 1


def test_comment_mode_and_page_comments(wide, daemon, docs, cli):
    spec = docs / "spec.md"
    open_doc(wide, daemon, spec)
    wide.keyboard.press("c")
    expect(wide.locator(".mode-bar")).to_be_visible()
    li = wide.locator(".markdown-body li", has_text="Who owns the dunning emails?")
    li.hover()
    expect(wide.locator(".pick-box")).to_be_visible()
    li.click()
    post(wide, "Finance owns these.")
    expect(wide.locator(".cm-el")).to_have_count(1)
    wide.locator(".mode-bar button", has_text="Whole page").click()
    post(wide, "Needs a rollout section.")
    expect(wide.locator(".margin .quote-page")).to_be_visible()
    wide.keyboard.press("Escape")
    wide.keyboard.press("Escape")
    expect(wide.locator(".mode-bar")).to_have_count(0)
    scopes = sorted(t["scope"] for t in cli.json("comments", spec)["threads"])
    assert scopes == ["element", "page"]


def test_resolve_with_undo(wide, daemon, docs, cli):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "Overall fine"})
    open_doc(wide, daemon, spec)
    wide.locator(".margin .thread .resolve").click()
    expect(wide.locator(".margin .thread")).to_have_count(0)
    wide.locator(".toast-action", has_text="Undo").click()
    expect(wide.locator(".margin .thread")).to_have_count(1)
    time.sleep(4)
    assert cli.json("comments", spec)["threads"][0]["status"] == "open"
    wide.locator(".margin .thread .resolve").click()
    time.sleep(4.2)
    assert cli.json("comments", spec)["threads"] == []


def test_links_to_threads(wide, daemon, docs):
    spec = docs / "spec.md"
    t = daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "linked", "selection": leaf_selection(daemon, spec, "skip weekends")})
    wide.goto(daemon.url(spec) + "#thread-" + t["id"])
    expect(wide.locator(".margin .thread.active")).to_contain_text("linked")


def test_narrow_window_uses_the_panel(page: Page, daemon, docs):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "Overall"})
    page.set_viewport_size({"width": 1000, "height": 800})
    open_doc(page, daemon, spec)
    expect(page.locator(".margin")).to_have_count(0)
    page.locator(".threads-btn").click()
    expect(page.locator(".panel .thread")).to_contain_text("Overall")


def test_resolved_threads_go_below_open_ones(page: Page, daemon, docs):
    spec = docs / "spec.md"
    done = daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "Why three?", "selection": leaf_selection(daemon, spec, "up to three times")})
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "Ask finance", "selection": leaf_selection(daemon, spec, "skip weekends")})
    daemon.ok("PATCH", f"threads/{done['id']}", {"status": "resolved"})
    page.add_init_script("localStorage.setItem('serve-prefs', JSON.stringify({showResolved: true}))")

    # In the margin, the resolved thread sits below the open one, although its
    # text comes first, and it is faded until picked.
    page.set_viewport_size({"width": 1440, "height": 900})
    open_doc(page, daemon, spec)
    resolved, still_open = page.locator(".margin .thread.resolved"), page.locator(".margin .thread:not(.resolved)")
    expect(resolved).to_contain_text("Why three?")
    page.wait_for_timeout(300)
    assert resolved.bounding_box()["y"] > still_open.bounding_box()["y"]
    assert float(resolved.evaluate("e => getComputedStyle(e).opacity")) < 0.7
    page.locator("mark.cm-resolved").click()
    expect(page.locator(".margin .thread.resolved.active")).to_be_visible()

    # In the panel, open threads come first, then the resolved ones under a divider.
    page.set_viewport_size({"width": 1000, "height": 800})
    open_doc(page, daemon, spec)
    page.locator(".threads-btn").click()
    expect(page.locator(".panel .thread").first).to_contain_text("Ask finance")
    expect(page.locator(".panel .thread").last).to_contain_text("Why three?")
    expect(page.locator(".panel-divider")).to_have_text("Resolved · 1")


def test_code_file_comment(wide, daemon, docs):
    go = docs / "sub" / "main.go"
    open_doc(wide, daemon, go)
    select(wide, "Println", ".code-view")
    wide.keyboard.press("c")
    post(wide, "Use a logger.")
    expect(wide.locator("mark.cm")).to_have_text("Println")


def test_html_page_runs_in_an_isolated_frame(wide, daemon, docs, cli):
    page_html = docs / "page.html"
    open_doc(wide, daemon, page_html)
    frame = wide.frame_locator("iframe.frame")
    frame.locator("#buy").click()
    expect(frame.locator("#o")).to_have_text("clicked")
    child = [f for f in wide.frames if f != wide.main_frame][0]
    assert child.evaluate("async () => { try { await fetch('%s/_serve/api/home'); return 'reached'; } catch { return 'blocked'; } }" % daemon.base) == "blocked"
    child.evaluate("() => { const b = document.querySelector('p b'); const r = document.createRange(); r.selectNodeContents(b); getSelection().removeAllRanges(); getSelection().addRange(r); }")
    frame.locator("body").press("c")
    expect(wide.locator(".panel .thread.draft")).to_be_visible()
    post(wide, "Monthly or yearly?")
    expect(frame.locator("mark.serve-cm")).to_have_text("$10")
    assert cli.json("comments", page_html)["threads"][0]["anchor_text"] == "$10"


def test_editor_saves_and_merges_a_change_on_disk(wide, daemon, docs):
    spec = docs / "spec.md"
    open_doc(wide, daemon, spec)
    wide.locator("button", has_text="Edit").click()
    wide.wait_for_selector(".cm-content")
    wide.locator(".cm-content").click()
    wide.keyboard.press("ControlOrMeta+End")
    wide.keyboard.type("\nFrom the browser.\n")
    spec.write_text(spec.read_text().replace("# Payment retry spec", "# Payment retry spec (v2)"))
    time.sleep(0.5)
    wide.keyboard.press("ControlOrMeta+s")
    expect(wide.locator(".modal h2")).to_have_text("This file changed on disk")
    wide.locator(".modal button", has_text="Merge both").click()
    expect(wide.locator(".toast", has_text="Merged")).to_be_visible()
    wide.keyboard.press("ControlOrMeta+s")
    expect(wide.locator(".toast", has_text="Saved")).to_be_visible()
    text = spec.read_text()
    assert "(v2)" in text and "From the browser." in text
    # Escape never throws the edit away.
    wide.keyboard.type("unsaved")
    wide.keyboard.press("Escape")
    expect(wide.locator(".cm-editor")).to_be_visible()


def test_file_tree_filter_and_counts(wide, daemon, docs):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "x"})
    open_doc(wide, daemon, docs / "notes.txt")
    expect(wide.locator(".tree-row", has_text="spec.md").locator(".count")).to_have_text("1")
    wide.keyboard.press("/")
    wide.keyboard.type("main")
    expect(wide.locator(".results .tree-row")).to_have_text(re.compile("sub/main.go"))
    wide.keyboard.press("Enter")
    wide.wait_for_url(re.compile("main.go$"))


def test_file_tree_can_start_at_a_folder_inside_the_opened_one(wide, daemon, docs):
    open_doc(wide, daemon, docs / "sub" / "main.go")
    head = wide.locator(".sidebar-root")
    expect(head).to_have_text("docs")
    wide.locator(".tree-row.dir", has_text="sub").click(button="right")
    wide.locator(".menu.context button", has_text="Start the file tree here").click()
    expect(head).to_have_text("sub")
    expect(wide.locator(".tree > ul > li > .tree-row")).to_have_text([re.compile("main.go")])
    # The choice holds across pages in the folder, but not outside it.
    wide.reload()
    expect(head).to_have_text("sub")
    open_doc(wide, daemon, docs / "spec.md")
    expect(head).to_have_text("docs")
    # ↑ moves the tree back up without opening anything new.
    open_doc(wide, daemon, docs / "sub" / "main.go")
    wide.locator("button[aria-label='Show the folder above']").click()
    expect(head).to_have_text("docs")
    expect(wide.locator("button[aria-label='Open the folder above']")).to_be_visible()
    wide.reload()
    expect(head).to_have_text("docs")
    assert [f["path"] for f in daemon.ok("GET", "home")["folders"]] == [str(docs)]


def test_dark_theme_and_help(wide, daemon, docs):
    open_doc(wide, daemon, docs / "spec.md")
    wide.locator("button[aria-label='View options']").click()
    wide.locator(".view-menu button", has_text="Dark").click()
    assert wide.evaluate("() => document.documentElement.dataset.theme") == "dark"
    bg = wide.evaluate("() => getComputedStyle(document.body).backgroundColor")
    assert bg == "rgb(13, 17, 23)"
    wide.keyboard.press("Escape")
    wide.keyboard.press("?")
    expect(wide.locator(".modal.help")).to_contain_text("Next comment")


def test_start_page_lists_threads_waiting_for_you(wide, daemon, docs, cli):
    spec = docs / "spec.md"
    t = daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "Overall?"})
    cli.run("reply", spec, t["id"], "Looks fine to me.")
    wide.goto(daemon.base + "/")
    expect(wide.locator(".inbox")).to_contain_text("Looks fine to me.")
    expect(wide.locator(".folders")).to_contain_text("docs")


def test_clicking_line_numbers_comments_on_lines(wide, daemon, docs, cli):
    go = docs / "sub" / "main.go"
    open_doc(wide, daemon, go)
    rows = wide.locator(".code-view .cl")
    rows.nth(4).click(position={"x": 10, "y": 8})
    rows.nth(5).click(position={"x": 10, "y": 8}, modifiers=["Shift"])
    expect(wide.locator(".thread.draft")).to_be_visible()
    post(wide, "These two lines")
    th = cli.json("comments", go)["threads"][0]
    assert (th["source_line_start"], th["source_line_end"]) == (5, 6)


def test_embedded_in_another_apps_frame(wide, daemon, docs, cli, tmp_path):
    import http.server, threading, functools
    from conftest import free_port

    host_dir = tmp_path / "host"
    host_dir.mkdir()
    (host_dir / "index.html").write_text(f'<html><body><iframe id="f" style="width:560px;height:760px" src="{daemon.url(docs / "spec.md")}?embed=1"></iframe></body></html>')
    port = free_port()
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", port), functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(host_dir)))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    try:
        wide.goto(f"http://localhost:{port}/index.html")
        frame = wide.frame_locator("#f")
        expect(frame.locator(".markdown-body h1")).to_have_text("Payment retry spec")
        expect(frame.locator(".topbar")).to_have_count(0)
        expect(frame.locator(".sidebar")).to_have_count(0)
        expect(frame.locator(".embed-bar")).to_be_visible()
        child = [f for f in wide.frames if f != wide.main_frame][0]
        frame.locator(".markdown-body h1").click()  # focus the frame, as a person would
        child.evaluate("""() => { const p = [...document.querySelectorAll('.markdown-body p')].find(p => p.textContent.includes('ACH')); const t = p.firstChild; const i = t.data.indexOf('ACH payments'); const r = document.createRange(); r.setStart(t, i); r.setEnd(t, i + 12); getSelection().removeAllRanges(); getSelection().addRange(r); }""")
        wide.keyboard.press("c")
        expect(frame.locator(".thread.draft textarea")).to_be_focused()
        frame.locator(".thread.draft textarea").type("From the threads desk")
        frame.locator(".thread.draft textarea").press("ControlOrMeta+Enter")
        expect(frame.locator("mark.cm")).to_have_text("ACH payments")
        assert cli.json("comments", docs / "spec.md")["threads"][0]["text"] == "From the threads desk"
    finally:
        srv.shutdown()
