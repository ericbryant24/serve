"""Serve's chrome must stay usable on top of someone else's HTML.

An HTML prototype brings its own CSS. A fixed app bar at z-index 1030 (Bootstrap)
or 1100 (Material UI) spans the full viewport and ignores the body offset serve
uses to make room for the sidebar, so before the z-index band was reserved it
painted straight over the sidebar header and the collapse button. The panel then
could not be collapsed, which also meant the prototype could not be seen in full.
"""

from pathlib import Path

import pytest

from conftest import _free_port, _start_server

# z-index 1100 is Material UI's app bar default; 1030 is Bootstrap's navbar.
PROTOTYPE = """<!doctype html>
<html><head><meta charset="utf-8"><title>Billing</title>
<style>
  body { margin: 0; font-family: sans-serif; }
  .appbar { position: fixed; top: 0; left: 0; right: 0; height: 56px;
            background: #5b2d8e; color: #fff; z-index: 1100; }
  .fab { position: fixed; bottom: 24px; right: 24px; z-index: 1200;
         background: #5b2d8e; color: #fff; padding: 14px 22px; }
  .content { padding: 76px 24px 24px; }
</style></head>
<body>
  <div class="appbar"><strong>Customer Billing Platform</strong></div>
  <div class="content"><h3>1400 Industrial Pkwy</h3><p>7 transactions</p></div>
  <div class="fab">Post Invoice</div>
</body></html>
"""


@pytest.fixture()
def prototype_server(tmp_path: Path):
    doc = tmp_path / "app.html"
    doc.write_text(PROTOTYPE)
    proc, base_url = _start_server(str(doc), _free_port())
    yield base_url
    proc.terminate()
    proc.wait(timeout=10)


HIT_TEST = """(sel) => {
    const t = document.querySelector(sel);
    if (!t) return 'missing';
    const r = t.getBoundingClientRect();
    const h = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    if (h === t || t.contains(h)) return 'reachable';
    return 'blocked by ' + (h ? (h.id || h.className || h.tagName) : 'nothing');
}"""


@pytest.mark.parametrize(
    "selector",
    ["#serve-sidebar-toggle", "#serve-sidebar-header", "#serve-sidebar-up"],
)
def test_chrome_is_reachable_over_a_fixed_app_bar(page, prototype_server, selector):
    page.goto(prototype_server, wait_until="networkidle")
    page.wait_for_timeout(400)
    assert page.evaluate(HIT_TEST, selector) == "reachable"


def test_sidebar_paints_above_page_content(page, prototype_server):
    page.goto(prototype_server, wait_until="networkidle")
    page.wait_for_timeout(400)
    sidebar_z = int(page.evaluate(
        "() => getComputedStyle(document.getElementById('serve-sidebar')).zIndex"
    ))
    appbar_z = int(page.evaluate(
        "() => getComputedStyle(document.querySelector('.appbar')).zIndex"
    ))
    assert sidebar_z > appbar_z, "the sidebar must outrank a prototype's app bar"


def test_collapse_restores_the_full_width(page, prototype_server):
    page.goto(prototype_server, wait_until="networkidle")
    page.wait_for_timeout(400)

    assert page.evaluate("() => getComputedStyle(document.body).marginLeft") == "260px"

    page.click("#serve-sidebar-toggle")
    page.wait_for_timeout(500)

    # A prototype that set body{margin:0} must get exactly that back; the 20px
    # readability gutter belongs to markdown, not to someone else's layout.
    assert page.evaluate("() => getComputedStyle(document.body).marginLeft") == "0px"
    assert page.evaluate(
        "() => document.getElementById('serve-sidebar').getBoundingClientRect().left"
    ) <= -260

    page.click("#serve-sidebar-toggle")
    page.wait_for_timeout(500)
    assert page.evaluate("() => getComputedStyle(document.body).marginLeft") == "260px"


def test_markdown_keeps_its_reading_gutter(page, md_server):
    """The raw-HTML override must not leak into serve's own rendering."""
    page.goto(md_server.base_url, wait_until="networkidle")
    page.wait_for_timeout(400)
    assert page.evaluate("() => getComputedStyle(document.body).marginLeft") == "280px"
