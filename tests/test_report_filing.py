"""The GitHub filing flow, driven end to end against a stub.

serve's GitHub endpoints are overridable, so the whole device-flow dance —
show a code, wait for approval, create the issue — runs here without touching
github.com. That is what makes the auto-polling behaviour testable: the screen
has to advance on its own once approval lands, with no second button press.
"""

import json
import os
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import httpx
import pytest

from conftest import _free_port, _serve_cmd_parts, _wait_ready, child_env

ISSUE_URL = "https://github.com/ericbryant24/serve/issues/999"


class GitHubStub:
    """Minimal stand-in for the three GitHub endpoints serve talks to."""

    def __init__(self):
        self.port = _free_port()
        self.approved = False
        self.issue_body = None
        stub = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def _json(self, code, payload):
                body = json.dumps(payload).encode()
                self.send_response(code)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                n = int(self.headers.get("Content-Length", 0))
                raw = self.rfile.read(n).decode()
                if self.path == "/device/code":
                    self._json(200, {
                        "device_code": "dc-1",
                        "user_code": "TEST-CODE",
                        "verification_uri": "https://github.com/login/device",
                        "expires_in": 900,
                        "interval": 1,
                    })
                elif self.path == "/oauth/token":
                    if stub.approved:
                        self._json(200, {"access_token": "gho_stub"})
                    else:
                        self._json(200, {"error": "authorization_pending"})
                else:  # issue creation
                    stub.issue_body = json.loads(raw)
                    self._json(201, {"html_url": ISSUE_URL})

        self.httpd = HTTPServer(("127.0.0.1", self.port), Handler)
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def start(self):
        self.thread.start()

    def stop(self):
        self.httpd.shutdown()
        self.httpd.server_close()

    @property
    def env(self) -> dict:
        base = f"http://127.0.0.1:{self.port}"
        return {
            "SERVE_GITHUB_DEVICE_URL": f"{base}/device/code",
            "SERVE_GITHUB_TOKEN_URL": f"{base}/oauth/token",
            "SERVE_GITHUB_API": base,
            "SERVE_GITHUB_CLIENT_ID": "stub-client-id",
        }


@pytest.fixture()
def stub_server(tmp_path: Path):
    stub = GitHubStub()
    stub.start()

    doc = tmp_path / "doc.md"
    doc.write_text("# Doc\n\nSome content.\n")
    home = tmp_path / "home"
    home.mkdir()

    port = _free_port()
    proc = subprocess.Popen(
        _serve_cmd_parts() + ["--no-open", "-p", str(port), str(doc)],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=Path(__file__).parent.parent,
        env=child_env(HOME=str(home), **stub.env),
    )
    base_url = f"http://localhost:{port}"
    try:
        _wait_ready(base_url)
    except RuntimeError:
        proc.terminate()
        proc.wait(timeout=5)
        stub.stop()
        raise

    yield base_url, stub, home

    proc.terminate()
    proc.wait(timeout=10)
    stub.stop()


def _to_review(page, base_url, title="Filing flow", body="body text"):
    page.goto(base_url, wait_until="networkidle")
    page.click("#serve-report-btn")
    page.wait_for_selector("#serve-report-modal:not([hidden])")
    page.wait_for_timeout(1200)
    page.fill("#srp-title", title)
    page.fill("#srp-body", body)
    page.click("[data-act=review]")
    page.wait_for_selector(".srp-payload", timeout=10000)


def test_review_warns_that_authorization_comes_first(page, stub_server):
    base_url, _, _ = stub_server
    _to_review(page, base_url)
    assert "authorize" in page.inner_text(".srp-dest").lower(), (
        "a first-time filer should be told an authorization step is coming"
    )


def test_device_screen_shows_a_copyable_code(page, stub_server):
    base_url, _, _ = stub_server
    _to_review(page, base_url)
    page.click("[data-act=file]")
    page.wait_for_selector(".srp-code", timeout=10000)

    assert page.inner_text("#srp-code").strip() == "TEST-CODE"
    assert page.is_visible("[data-act=copy-code]")
    assert page.is_visible(".srp-link-btn"), "no way to open GitHub from the dialog"


def test_polling_starts_without_a_second_click(page, stub_server):
    base_url, _, _ = stub_server
    _to_review(page, base_url)
    page.click("[data-act=file]")
    page.wait_for_selector(".srp-code", timeout=10000)
    page.wait_for_timeout(600)

    assert page.is_visible(".srp-waiting"), (
        "the dialog should already be waiting for approval, not sitting idle"
    )
    # The old flow required pressing "I have entered the code" to make progress.
    assert page.query_selector("[data-act=await]") is None


def test_approval_advances_the_screen_on_its_own(page, stub_server):
    base_url, stub, _ = stub_server
    _to_review(page, base_url, title="Auto-poll works")
    page.click("[data-act=file]")
    page.wait_for_selector(".srp-code", timeout=10000)
    page.wait_for_timeout(400)

    # Approve out-of-band, exactly as the reporter would on github.com. No
    # further interaction with the dialog.
    stub.approved = True

    page.wait_for_selector(".srp-msg.ok", timeout=15000)
    msg = page.inner_text(".srp-msg.ok")
    assert "Filed" in msg
    assert page.query_selector(f'.srp-msg.ok a[href="{ISSUE_URL}"]') is not None

    assert stub.issue_body is not None, "no issue was created"
    assert stub.issue_body["title"] == "Auto-poll works"
    assert stub.issue_body["labels"] == ["bug"]


def test_filed_report_records_the_issue_url(page, stub_server):
    base_url, stub, home = stub_server
    _to_review(page, base_url)
    page.click("[data-act=file]")
    page.wait_for_selector(".srp-code", timeout=10000)
    stub.approved = True
    page.wait_for_selector(".srp-msg.ok", timeout=15000)

    reports = httpx.get(f"{base_url}/api/report").json()["reports"]
    assert len(reports) == 1
    assert reports[0]["upload"]["status"] == "filed"
    assert reports[0]["upload"]["issue_url"] == ISSUE_URL

    # And the token was cached, so the next report skips authorization.
    assert (home / ".serve" / "github.json").exists()
