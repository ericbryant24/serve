"""Browser tests for in-app bug reporting.

These run the server with an isolated HOME so captured reports land in a
temporary directory instead of the developer's real ~/.serve/reports.
"""

import base64
import json
import os
import subprocess
import time
from pathlib import Path

import httpx
import pytest

from conftest import _free_port, _serve_cmd_parts, _wait_ready, child_env

DOC = """# Q3 Renewal Risk

Three accounts are flagged for non-renewal, representing $2.4M in annual
contract value. Legal has asked that the Ardmore terms stay out of the deck.

| Account | ACV | Owner |
|---|---|---|
| Northwind Utilities | $1.10M | a.reyes |
| Ardmore Gas | $0.82M | k.olsen |

```python
def risk_score(account):
    return account.tickets / max(account.seats, 1)
```
"""


@pytest.fixture()
def report_server(tmp_path: Path):
    """serve, with HOME pointed at a scratch directory."""
    doc = tmp_path / "renewal.md"
    doc.write_text(DOC)
    home = tmp_path / "home"
    home.mkdir()

    port = _free_port()
    # A per-test home, not the shared one: these tests assert on how many
    # reports exist, so they must not see another test's captures.
    env = child_env(HOME=str(home))
    proc = subprocess.Popen(
        _serve_cmd_parts() + ["--no-open", "-p", str(port), str(doc)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=Path(__file__).parent.parent,
        env=env,
    )
    base_url = f"http://localhost:{port}"
    try:
        _wait_ready(base_url)
    except RuntimeError:
        proc.terminate()
        proc.wait(timeout=5)
        raise

    yield base_url, home

    proc.terminate()
    proc.wait(timeout=10)


def _open_report(page, base_url):
    page.goto(base_url, wait_until="networkidle")
    page.click("#serve-report-btn")
    page.wait_for_selector("#serve-report-modal:not([hidden])", timeout=5000)
    for _ in range(60):
        if "Capturing the page" not in page.inner_text(".srp-body"):
            break
        time.sleep(0.25)


def _to_review(page, title="Table overflows", body="It runs past the edge."):
    page.fill("#srp-title", title)
    page.fill("#srp-body", body)
    page.click("[data-act=review]")
    page.wait_for_selector(".srp-payload", timeout=10000)


# ---------------------------------------------------------------------------
# Capture
# ---------------------------------------------------------------------------


def test_capture_succeeds_and_stores_a_png(page, report_server):
    base_url, home = report_server
    _open_report(page, base_url)
    assert "could not be captured" not in page.inner_text(".srp-body")
    _to_review(page)

    reports = list((home / ".serve" / "reports").iterdir())
    assert len(reports) == 1
    shots = list(reports[0].glob("screenshot-*.png"))
    assert len(shots) == 1
    data = shots[0].read_bytes()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "attachment is not a real PNG"
    assert len(data) > 1000


def test_structural_capture_removes_readable_text(page, report_server):
    """The capture keeps layout and drops content.

    Rendered text is heavily antialiased, so a row of pixels crossing a line of
    text contains many distinct colours. A solid redaction bar contains two or
    three. Comparing serve's capture against a plain screenshot of the same
    page makes the difference decisive without needing OCR.
    """
    base_url, home = report_server
    _open_report(page, base_url)
    _to_review(page)

    att = page.eval_on_selector_all(
        ".srp-att input[type=checkbox]", "els => els.map(e => e.dataset.att)"
    )
    report_id = page.evaluate(
        "() => document.querySelector('.srp-foot code').textContent.split('/').filter(Boolean).pop()"
    )
    shot_url = None
    for a in att:
        el = page.query_selector(f'.srp-att img[src*="{a}"]')
        if el:
            shot_url = el.get_attribute("src")
    assert shot_url, "no screenshot preview rendered"

    control = base64.b64encode(page.screenshot()).decode()

    analyse = """
    async ([capturedURL, controlB64]) => {
      function colourfulness(img) {
        const c = document.createElement('canvas');
        c.width = img.naturalWidth; c.height = img.naturalHeight;
        const ctx = c.getContext('2d');
        ctx.drawImage(img, 0, 0);
        const d = ctx.getImageData(0, 0, c.width, c.height).data;
        let worst = 0;
        for (let y = 0; y < c.height; y += 3) {
          const seen = new Set();
          for (let x = 0; x < c.width; x++) {
            const i = (y * c.width + x) * 4;
            seen.add((d[i] << 16) | (d[i+1] << 8) | d[i+2]);
          }
          if (seen.size > worst) worst = seen.size;
        }
        return worst;
      }
      function load(src) {
        return new Promise((res, rej) => {
          const im = new Image();
          im.onload = () => res(im); im.onerror = rej; im.src = src;
        });
      }
      const a = await load(capturedURL);
      const b = await load('data:image/png;base64,' + controlB64);
      return { captured: colourfulness(a), control: colourfulness(b) };
    }
    """
    res = page.evaluate(analyse, [shot_url, control])

    assert res["control"] > 40, (
        f"control screenshot looks too flat ({res['control']} colours); "
        "the comparison would not prove anything"
    )
    assert res["captured"] < res["control"] * 0.5, (
        f"structural capture has {res['captured']} distinct colours per row vs "
        f"{res['control']} in the control: text may not have been redacted"
    )


def test_capture_does_not_mutate_the_live_page(page, report_server):
    base_url, _ = report_server
    before = page.evaluate("() => document.body.innerHTML.length") if page.url != "about:blank" else None
    _open_report(page, base_url)
    leftovers = page.evaluate(
        "() => document.querySelectorAll('#serve-content span[style*=\"color:transparent\"]').length"
    )
    assert leftovers == 0, "redaction spans leaked into the live document"
    text = page.inner_text("#serve-content")
    assert "Northwind Utilities" in text, "the live page lost its own text"


# ---------------------------------------------------------------------------
# The review gate
# ---------------------------------------------------------------------------


def test_attachments_are_off_by_default(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    _to_review(page)
    checked = page.eval_on_selector_all(
        ".srp-att input[type=checkbox]", "els => els.map(e => e.checked)"
    )
    assert checked, "no attachments offered"
    assert not any(checked), "an attachment was included without being asked for"


def test_including_an_attachment_updates_the_payload(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    _to_review(page)

    before = page.inner_text(".srp-payload")
    assert "screenshot-" not in before

    page.check(".srp-att input[type=checkbox] >> nth=0")
    page.wait_for_function(
        "() => document.querySelector('.srp-payload').textContent.includes('**Attached**')",
        timeout=5000,
    )
    after = page.inner_text(".srp-payload")
    assert "Attached" in after


def test_credentials_block_filing_until_acknowledged(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    _to_review(
        page,
        title="auth fails",
        body="I ran it with ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa and it broke",
    )

    assert page.is_visible(".srp-secrets"), "a github token in the body was not flagged"
    file_btn = page.query_selector("[data-act=file]")
    assert file_btn is not None
    assert file_btn.is_disabled(), "filing was allowed with an unacknowledged credential"

    page.check("#srp-ack")
    assert not page.query_selector("[data-act=file]").is_disabled()


def test_destination_names_the_repo_and_says_public(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    _to_review(page)
    dest = page.inner_text(".srp-dest")
    assert "github.com/ericbryant24/serve" in dest
    assert "public issue" in dest


def test_escape_closes_the_modal(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    page.keyboard.press("Escape")
    page.wait_for_selector("#serve-report-modal", state="hidden", timeout=3000)


# ---------------------------------------------------------------------------
# API
# ---------------------------------------------------------------------------


def test_feature_requests_carry_no_screenshot(page, report_server):
    base_url, home = report_server
    _open_report(page, base_url)
    page.click('.srp-kind[data-kind=feature]')
    _to_review(page, title="Add a dark theme", body="It is bright at night.")

    kinds = page.eval_on_selector_all(".srp-att-name", "els => els.map(e => e.textContent)")
    assert not any("Screenshot" in k for k in kinds), (
        "a feature request captured a screenshot"
    )


def test_report_api_lists_what_the_browser_created(page, report_server):
    base_url, _ = report_server
    _open_report(page, base_url)
    _to_review(page, title="Sidebar clips")

    res = httpx.get(f"{base_url}/api/report")
    assert res.status_code == 200
    reports = res.json()["reports"]
    assert len(reports) == 1
    assert reports[0]["title"] == "Sidebar clips"
    assert reports[0]["env"]["view_kind"] == "markdown"
