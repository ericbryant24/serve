"""
Shared fixtures for serve's integration tests.

They run the real binary (SERVE_CMD, default ./serve from the repository
root). Every serve process gets its own throwaway HOME, so nothing a test
does reaches the real ~/.serve, and each test that needs a server gets a
fresh background server on a free port.
"""

import json
import os
import shlex
import shutil
import socket
import subprocess
import time
from pathlib import Path

import httpx
import pytest

ROOT = Path(__file__).resolve().parent.parent
SERVE_CMD = shlex.split(os.environ.get("SERVE_CMD", str(ROOT / "serve")))

SPEC = """# Payment retry spec

## Goals

We retry failed card payments up to three times over five days.

Retries stop when the customer updates their card.

## Non-goals

We do not retry ACH payments.

## Open questions

- Should retries skip weekends?
- Who owns the dunning emails?
"""


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class CLI:
    """Runs serve commands as a given user (HOME)."""

    def __init__(self, home: Path):
        self.home = home

    def env(self, **extra) -> dict:
        e = dict(os.environ, HOME=str(self.home))
        e.pop("SERVE_AUTHOR", None)
        e.pop("SERVE_PORT", None)
        e.update(extra)
        return e

    def run(self, *args, input=None, check=True, timeout=30, **env) -> subprocess.CompletedProcess:
        p = subprocess.run(
            SERVE_CMD + [str(a) for a in args],
            input=input,
            capture_output=True,
            text=True,
            timeout=timeout,
            env=self.env(**env),
            cwd=ROOT,
        )
        if check and p.returncode != 0:
            raise AssertionError(f"serve {' '.join(map(str, args))} exited {p.returncode}\n{p.stdout}\n{p.stderr}")
        return p

    def json(self, *args, **kw):
        return json.loads(self.run(*args, **kw).stdout)

    def popen(self, *args, **env) -> subprocess.Popen:
        return subprocess.Popen(
            SERVE_CMD + [str(a) for a in args],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            env=self.env(**env),
            cwd=ROOT,
        )


class Daemon:
    """A background server started for one test."""

    def __init__(self, cli: CLI, port: int):
        self.cli = cli
        self.port = port
        self.proc = cli.popen("daemon", "--port", port)
        info = self.home_file()
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError("serve daemon exited: " + self.proc.stderr.read())
            if info.exists():
                try:
                    d = json.loads(info.read_text())
                    if d.get("pid") == self.proc.pid:
                        r = httpx.get(f"http://localhost:{port}/_serve/health", timeout=1)
                        if r.status_code == 200:
                            self.info = d
                            break
                except Exception:
                    pass
            time.sleep(0.05)
        else:
            raise RuntimeError("serve daemon did not start")
        self.token = self.info["token"]
        self.content_port = self.info["content_port"]
        self.base = f"http://localhost:{port}"

    def home_file(self) -> Path:
        return self.cli.home / ".serve" / "daemon.json"

    def url(self, path: Path | str) -> str:
        return self.base + url_path(path)

    def api(self, method: str, path: str, body=None, token=None, headers=None, **kw) -> httpx.Response:
        h = {"X-Serve-Token": self.token if token is None else token}
        if headers:
            h.update(headers)
        if body is not None:
            kw["json"] = body
        return httpx.request(method, f"{self.base}/_serve/api/{path}", headers=h, timeout=10, **kw)

    def ok(self, method: str, path: str, body=None):
        r = self.api(method, path, body)
        assert r.status_code < 300, f"{method} {path}: {r.status_code} {r.text}"
        return r.json() if r.text else None

    def open(self, folder: Path):
        self.ok("POST", "folders", {"path": str(folder)})

    def stop(self):
        if self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()


def url_path(p: Path | str) -> str:
    """The URL path serve shows a file at (absolute path, as in the tests' HOME)."""
    from urllib.parse import quote

    p = os.path.realpath(str(p))
    return quote(p)


@pytest.fixture()
def home(tmp_path: Path) -> Path:
    h = tmp_path / "home"
    h.mkdir()
    return h


@pytest.fixture()
def cli(home: Path) -> CLI:
    return CLI(home)


@pytest.fixture()
def docs(tmp_path: Path) -> Path:
    d = Path(os.path.realpath(tmp_path / "docs"))
    d.mkdir()
    (d / "spec.md").write_text(SPEC)
    (d / "notes.txt").write_text("first line\nsecond line\n")
    (d / "sub").mkdir()
    (d / "sub" / "main.go").write_text('package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello")\n}\n')
    (d / "page.html").write_text(
        "<!doctype html><html><head><title>Widget</title></head><body>"
        "<h1>Pricing widget</h1><p>Plans start at <b>$10</b> a month.</p>"
        "<button id='buy' onclick=\"document.getElementById('o').textContent='clicked'\">Buy</button><p id='o'></p>"
        "</body></html>"
    )
    return d


@pytest.fixture()
def daemon(cli: CLI, docs: Path):
    d = Daemon(cli, free_port())
    d.open(docs)
    yield d
    d.stop()


def leaf_selection(d: Daemon, path: Path, phrase: str) -> dict:
    """A browser-style selection of phrase, found through the page's data."""
    r = httpx.get(d.url(path), headers={"Accept": "text/html"})
    html = r.text
    data = json.loads(html.split('<script id="serve-data" type="application/json">', 1)[1].split("</script>", 1)[0])
    from html.parser import HTMLParser

    class Leaves(HTMLParser):
        def __init__(self):
            super().__init__()
            self.stack = []
            self.texts = {}

        def handle_starttag(self, tag, attrs):
            a = dict(attrs)
            key = a.get("data-b") if "data-t" in a else None
            if tag in ("br", "img", "input", "hr"):
                return
            self.stack.append(key)
            if key:
                self.texts.setdefault(key, "")

        def handle_endtag(self, tag):
            if tag in ("br", "img", "input", "hr"):
                return
            if self.stack:
                self.stack.pop()

        def handle_data(self, data):
            for k in self.stack:
                if k:
                    self.texts[k] += data

    p = Leaves()
    p.feed(data["doc"]["html"])
    for k, t in p.texts.items():
        i = t.find(phrase)
        if i >= 0:
            # Offsets count UTF-16 units, as the browser does.
            pre = len(t[:i].encode("utf-16-le")) // 2
            n = len(phrase.encode("utf-16-le")) // 2
            return {"start_key": k, "start_offset": pre, "end_key": k, "end_offset": pre + n, "quote": phrase}
    raise AssertionError(f"{phrase!r} not on the page")


def page_data(d: Daemon, path: Path) -> dict:
    html = httpx.get(d.url(path), headers={"Accept": "text/html"}).text
    return json.loads(html.split('<script id="serve-data" type="application/json">', 1)[1].split("</script>", 1)[0])
