"""
Shared fixtures for the serve integration test suite.

Set SERVE_CMD env var to override the binary:
  SERVE_CMD="./serve"   # default
"""

import os
import shlex
import shutil
import socket
import subprocess
import time
from pathlib import Path

import httpx
import pytest

FIXTURES_DIR = Path(__file__).parent / "fixtures"
SERVE_CMD = os.environ.get("SERVE_CMD", "./serve")
COMMENTS_DIR = Path.home() / ".serve" / "comments"


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("", 0))
        return s.getsockname()[1]


def _wait_ready(base_url: str, timeout: float = 8.0) -> None:
    deadline = time.monotonic() + timeout
    last_exc = None
    while time.monotonic() < deadline:
        try:
            r = httpx.get(f"{base_url}/api/comments", timeout=1.0)
            if r.status_code in (200, 404):
                return
        except Exception as exc:
            last_exc = exc
        time.sleep(0.15)
    raise RuntimeError(
        f"Server at {base_url} not ready after {timeout}s. Last error: {last_exc}"
    )


def _serve_cmd_parts() -> list[str]:
    return shlex.split(SERVE_CMD)


class ServeServer:
    def __init__(self, base_url: str, port: int, proc: subprocess.Popen, doc_ids: list[str]):
        self.base_url = base_url
        self.port = port
        self.proc = proc
        self._doc_ids = doc_ids

    def get(self, path: str, **kwargs) -> httpx.Response:
        return httpx.get(f"{self.base_url}{path}", **kwargs)

    def post(self, path: str, **kwargs) -> httpx.Response:
        return httpx.post(f"{self.base_url}{path}", **kwargs)

    def patch(self, path: str, **kwargs) -> httpx.Response:
        return httpx.patch(f"{self.base_url}{path}", **kwargs)

    def delete(self, path: str, **kwargs) -> httpx.Response:
        return httpx.delete(f"{self.base_url}{path}", **kwargs)

    def register_doc_id(self, doc_id: str) -> None:
        self._doc_ids.append(doc_id)


def _start_server(target: str, port: int) -> tuple[subprocess.Popen, str]:
    cmd = _serve_cmd_parts() + ["--no-open", "-p", str(port), target]
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=Path(__file__).parent.parent,
    )
    base_url = f"http://localhost:{port}"
    try:
        _wait_ready(base_url)
    except RuntimeError:
        proc.terminate()
        proc.wait(timeout=5)
        raise
    return proc, base_url


@pytest.fixture()
def md_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "simple.md"
    dst = tmp_path / "simple.md"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def anchoring_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "anchoring.md"
    dst = tmp_path / "anchoring.md"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def anchoring_server(anchoring_file: Path):
    doc_ids: list[str] = []
    port = _free_port()
    proc, base_url = _start_server(str(anchoring_file), port)
    server = ServeServer(base_url, port, proc, doc_ids)
    yield server
    proc.terminate()
    proc.wait(timeout=10)
    import re
    content = anchoring_file.read_text()
    m = re.search(r"comment-id:\s*([a-f0-9]+)", content)
    if m:
        (COMMENTS_DIR / f"{m.group(1)}.json").unlink(missing_ok=True)


@pytest.fixture()
def frontmatter_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "with_frontmatter.md"
    dst = tmp_path / "with_frontmatter.md"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def html_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "simple.html"
    dst = tmp_path / "simple.html"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def dir_tree(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "dir"
    dst = tmp_path / "dir"
    shutil.copytree(src, dst)
    shutil.copy(FIXTURES_DIR / "code.py", dst / "code.py")
    return dst


@pytest.fixture()
def md_server(md_file: Path):
    doc_ids: list[str] = []
    port = _free_port()
    proc, base_url = _start_server(str(md_file), port)
    server = ServeServer(base_url, port, proc, doc_ids)
    yield server
    proc.terminate()
    proc.wait(timeout=10)
    for doc_id in doc_ids:
        comment_file = COMMENTS_DIR / f"{doc_id}.json"
        comment_file.unlink(missing_ok=True)


@pytest.fixture()
def html_server(html_file: Path):
    doc_ids: list[str] = []
    port = _free_port()
    proc, base_url = _start_server(str(html_file), port)
    server = ServeServer(base_url, port, proc, doc_ids)
    yield server
    proc.terminate()
    proc.wait(timeout=10)
    for doc_id in doc_ids:
        comment_file = COMMENTS_DIR / f"{doc_id}.json"
        comment_file.unlink(missing_ok=True)


@pytest.fixture()
def dir_server(dir_tree: Path):
    doc_ids: list[str] = []
    port = _free_port()
    proc, base_url = _start_server(str(dir_tree), port)
    server = ServeServer(base_url, port, proc, doc_ids)
    yield server
    proc.terminate()
    proc.wait(timeout=10)
    for doc_id in doc_ids:
        comment_file = COMMENTS_DIR / f"{doc_id}.json"
        comment_file.unlink(missing_ok=True)


def _generate_large_dir(root: Path) -> None:
    """Generate a ~50-file, 3-level directory tree for perf testing."""
    topics = [
        ("Authentication", "auth"),
        ("Authorization", "authz"),
        ("Rate Limiting", "rate-limiting"),
        ("Caching", "caching"),
        ("Logging", "logging"),
        ("Metrics", "metrics"),
        ("Tracing", "tracing"),
        ("Health Checks", "health"),
        ("Circuit Breakers", "circuit-breaker"),
        ("Load Balancing", "load-balancing"),
    ]
    subdirs = ["docs", "api", "guides"]
    code_snippets = {
        "client.py": '''\
"""HTTP client with retry logic."""
import time
import httpx


class RetryClient:
    def __init__(self, base_url: str, retries: int = 3):
        self.base_url = base_url
        self.retries = retries
        self._client = httpx.Client(timeout=10.0)

    def get(self, path: str) -> httpx.Response:
        for attempt in range(self.retries):
            try:
                return self._client.get(f"{self.base_url}{path}")
            except httpx.TransportError:
                if attempt == self.retries - 1:
                    raise
                time.sleep(0.1 * (2 ** attempt))

    def close(self) -> None:
        self._client.close()
''',
        "config.ts": '''\
interface ServerConfig {
  host: string;
  port: number;
  tls: boolean;
  maxConnections: number;
  timeoutMs: number;
}

const defaultConfig: ServerConfig = {
  host: "0.0.0.0",
  port: 8080,
  tls: false,
  maxConnections: 1000,
  timeoutMs: 30000,
};

export function loadConfig(overrides: Partial<ServerConfig> = {}): ServerConfig {
  return { ...defaultConfig, ...overrides };
}
''',
        "middleware.go": '''\
package server

import (
\t"log/slog"
\t"net/http"
\t"time"
)

func LoggingMiddleware(next http.Handler) http.Handler {
\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
\t\tstart := time.Now()
\t\twrapped := &responseWriter{ResponseWriter: w, status: 200}
\t\tnext.ServeHTTP(wrapped, r)
\t\tslog.Info("request",
\t\t\t"method", r.Method,
\t\t\t"path", r.URL.Path,
\t\t\t"status", wrapped.status,
\t\t\t"duration", time.Since(start),
\t\t)
\t})
}
''',
    }

    def _make_md(title: str, section: str, idx: int) -> str:
        lines = [
            f"# {title}\n\n",
            f"This document covers **{title.lower()}** in the context of {section} services.\n\n",
            "## Overview\n\n",
            f"The {title.lower()} subsystem handles incoming requests and applies policy before\n",
            "forwarding them to upstream services. Understanding this component is essential\n",
            "for operating the platform reliably.\n\n",
            "## Configuration\n\n",
            "| Parameter | Default | Description |\n",
            "|-----------|---------|-------------|\n",
            f"| `enabled` | `true` | Enable {title.lower()} |\n",
            f"| `timeout_ms` | `{100 + idx * 50}` | Request timeout in milliseconds |\n",
            f"| `max_retries` | `{idx % 5 + 1}` | Maximum retry attempts |\n",
            f"| `cache_ttl` | `{idx * 10}s` | Cache entry TTL |\n\n",
            "## Usage\n\n",
            "```python\n",
            f"from platform import {section.replace('-', '_')}\n\n",
            f"client = {section.replace('-', '_').title().replace('_', '')}Client(\n",
            f"    timeout_ms={100 + idx * 50},\n",
            f"    max_retries={idx % 5 + 1},\n",
            ")\n",
            f"result = client.apply(request)\n",
            "```\n\n",
            "## Error Handling\n\n",
            f"When {title.lower()} fails, the system returns a structured error:\n\n",
            "```json\n",
            "{\n",
            f'  "error": "{section}_failure",\n',
            f'  "code": {4000 + idx},\n',
            '  "retryable": true,\n',
            f'  "message": "The {title.lower()} check failed for this request"\n',
            "}\n",
            "```\n\n",
            "## Metrics\n\n",
            f"The following Prometheus metrics are emitted by the {title.lower()} component:\n\n",
            f"- `platform_{section.replace('-', '_')}_requests_total` — total requests processed\n",
            f"- `platform_{section.replace('-', '_')}_errors_total` — total errors\n",
            f"- `platform_{section.replace('-', '_')}_duration_seconds` — latency histogram\n\n",
            "## See Also\n\n",
            "- [Architecture Overview](../README.md)\n",
            "- [Runbook](../guides/runbook.md)\n",
            "- [API Reference](../api/README.md)\n",
        ]
        return "".join(lines)

    def _readme(section: str, files: list[str]) -> str:
        links = "\n".join(f"- [{f.replace('.md', '').replace('-', ' ').title()}]({f})" for f in files)
        return f"# {section.title()} Documentation\n\nThis section covers {section} topics.\n\n## Contents\n\n{links}\n"

    root.mkdir(parents=True, exist_ok=True)
    top_files = []
    for i, (title, slug) in enumerate(topics):
        fname = f"{slug}.md"
        (root / fname).write_text(_make_md(title, slug, i))
        top_files.append(fname)
    (root / "README.md").write_text(_readme("platform", top_files))

    for code_name, code_content in code_snippets.items():
        (root / code_name).write_text(code_content)

    for sub_idx, sub in enumerate(subdirs):
        sub_path = root / sub
        sub_path.mkdir()
        sub_files = []
        for i, (title, slug) in enumerate(topics):
            fname = f"{slug}.md"
            (sub_path / fname).write_text(_make_md(f"{sub.title()}: {title}", slug, sub_idx * 10 + i))
            sub_files.append(fname)
        (sub_path / "README.md").write_text(_readme(sub, sub_files))


@pytest.fixture()
def large_md_file(tmp_path: Path) -> Path:
    src = FIXTURES_DIR / "large.md"
    dst = tmp_path / "large.md"
    shutil.copy(src, dst)
    return dst


@pytest.fixture()
def large_dir_tree(tmp_path: Path) -> Path:
    root = tmp_path / "large_dir"
    _generate_large_dir(root)
    return root


@pytest.fixture()
def perf_server(large_md_file: Path):
    port = _free_port()
    t0 = time.perf_counter()
    proc, base_url = _start_server(str(large_md_file), port)
    startup_ms = (time.perf_counter() - t0) * 1000
    server = ServeServer(base_url, port, proc, [])
    server.startup_ms = startup_ms  # type: ignore[attr-defined]
    server.pid = proc.pid  # type: ignore[attr-defined]
    server.target_file = large_md_file  # type: ignore[attr-defined]
    yield server
    proc.terminate()
    proc.wait(timeout=10)


@pytest.fixture()
def large_dir_server(large_dir_tree: Path):
    port = _free_port()
    t0 = time.perf_counter()
    proc, base_url = _start_server(str(large_dir_tree), port)
    startup_ms = (time.perf_counter() - t0) * 1000
    server = ServeServer(base_url, port, proc, [])
    server.startup_ms = startup_ms  # type: ignore[attr-defined]
    server.pid = proc.pid  # type: ignore[attr-defined]
    server.dir_root = large_dir_tree  # type: ignore[attr-defined]
    yield server
    proc.terminate()
    proc.wait(timeout=10)


def make_comment(server: ServeServer, **kwargs) -> dict:
    payload = {
        "text": "Test comment",
        "anchor_text": "simple markdown document",
        "block_text": "This is a simple markdown document for testing.",
        "source_line_start": 3,
        "source_line_end": 3,
    }
    payload.update(kwargs)
    r = server.post("/api/comments", json=payload)
    assert r.status_code == 200, f"POST /api/comments failed: {r.text}"
    return r.json()
