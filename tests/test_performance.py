"""
Performance benchmarks for the serve tool.

Run against Python:
    uv run pytest tests/test_performance.py -s -v

Run against Go:
    SERVE_CMD="./serve-go/serve-go" uv run pytest tests/test_performance.py -s -v

All tests print measurements to stdout (pass -s to see them).
No assertions on absolute values — tests pass as long as the server responds.
"""

import json
import os
import statistics
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import httpx
import pytest
from websockets.sync.client import connect

from conftest import SERVE_CMD, ServeServer, make_comment

IMPL = os.path.basename(SERVE_CMD.split()[-1])


def _report(label: str, samples_ms: list[float]) -> None:
    s = sorted(samples_ms)
    n = len(s)
    qs = statistics.quantiles(s, n=100)
    mean = statistics.mean(s)
    print(
        f"\n=== {label} ({n} req) ===\n"
        f"  mean: {mean:6.1f} ms  |  "
        f"p50: {qs[49]:6.1f} ms  |  "
        f"p95: {qs[94]:6.1f} ms  |  "
        f"p99: {qs[98]:6.1f} ms"
    )


# ---------------------------------------------------------------------------
# Startup time
# ---------------------------------------------------------------------------


class TestStartupTime:
    def test_startup_single_file(self, perf_server: ServeServer) -> None:
        ms = perf_server.startup_ms  # type: ignore[attr-defined]
        print(f"\n=== [{IMPL}] Startup time (single file) ===\n  {ms:.1f} ms")

    def test_startup_large_dir(self, large_dir_server: ServeServer) -> None:
        ms = large_dir_server.startup_ms  # type: ignore[attr-defined]
        print(f"\n=== [{IMPL}] Startup time (large dir, ~50 files) ===\n  {ms:.1f} ms")


# ---------------------------------------------------------------------------
# Response latency
# ---------------------------------------------------------------------------


class TestResponseLatency:
    def test_get_single_file(self, perf_server: ServeServer) -> None:
        url = f"{perf_server.base_url}/"
        # 5-request warmup
        for _ in range(5):
            httpx.get(url, timeout=10.0)

        samples = []
        for _ in range(200):
            t0 = time.perf_counter()
            r = httpx.get(url, timeout=10.0)
            samples.append((time.perf_counter() - t0) * 1000)
            assert r.status_code == 200

        _report(f"[{IMPL}] GET / (single file)", samples)

    def test_get_dir_listing(self, large_dir_server: ServeServer) -> None:
        url = f"{large_dir_server.base_url}/api/files"
        for _ in range(5):
            httpx.get(url, timeout=10.0)

        samples = []
        for _ in range(100):
            t0 = time.perf_counter()
            r = httpx.get(url, timeout=10.0)
            samples.append((time.perf_counter() - t0) * 1000)
            assert r.status_code == 200

        _report(f"[{IMPL}] GET /api/files (dir listing, ~50 files)", samples)

    def test_get_dir_file_render(self, large_dir_server: ServeServer) -> None:
        url = f"{large_dir_server.base_url}/docs/auth.md"
        for _ in range(5):
            httpx.get(url, timeout=10.0)

        samples = []
        for _ in range(100):
            t0 = time.perf_counter()
            r = httpx.get(url, timeout=10.0)
            samples.append((time.perf_counter() - t0) * 1000)
            assert r.status_code == 200

        _report(f"[{IMPL}] GET /docs/auth.md (dir mode file render)", samples)

    def test_comment_post(self, perf_server: ServeServer) -> None:
        samples = []
        created_ids = []
        for _ in range(50):
            t0 = time.perf_counter()
            c = make_comment(perf_server)
            samples.append((time.perf_counter() - t0) * 1000)
            created_ids.append(c["id"])

        _report(f"[{IMPL}] POST /api/comments", samples)

        for cid in created_ids:
            perf_server.delete(f"/api/comments/{cid}")


# ---------------------------------------------------------------------------
# Throughput
# ---------------------------------------------------------------------------


class TestThroughput:
    def test_single_file_concurrent(self, perf_server: ServeServer) -> None:
        url = f"{perf_server.base_url}/"
        n, workers = 500, 10
        for _ in range(5):
            httpx.get(url, timeout=10.0)

        t0 = time.perf_counter()
        with ThreadPoolExecutor(max_workers=workers) as pool:
            futures = [pool.submit(httpx.get, url, timeout=10.0) for _ in range(n)]
            results = [f.result() for f in as_completed(futures)]
        elapsed = time.perf_counter() - t0
        errors = sum(1 for r in results if r.status_code != 200)
        rps = n / elapsed
        print(
            f"\n=== [{IMPL}] Throughput (single file, {workers} workers, {n} req) ===\n"
            f"  {rps:.0f} req/s  (wall: {elapsed:.2f}s, errors: {errors})"
        )

    def test_dir_mode_concurrent(self, large_dir_server: ServeServer) -> None:
        url = f"{large_dir_server.base_url}/api/files"
        n, workers = 500, 10
        for _ in range(5):
            httpx.get(url, timeout=10.0)

        t0 = time.perf_counter()
        with ThreadPoolExecutor(max_workers=workers) as pool:
            futures = [pool.submit(httpx.get, url, timeout=10.0) for _ in range(n)]
            results = [f.result() for f in as_completed(futures)]
        elapsed = time.perf_counter() - t0
        errors = sum(1 for r in results if r.status_code != 200)
        rps = n / elapsed
        print(
            f"\n=== [{IMPL}] Throughput (dir listing, {workers} workers, {n} req) ===\n"
            f"  {rps:.0f} req/s  (wall: {elapsed:.2f}s, errors: {errors})"
        )


# ---------------------------------------------------------------------------
# Memory
# ---------------------------------------------------------------------------


class TestMemory:
    def _rss_mb(self, pid: int) -> float:
        result = subprocess.run(
            ["ps", "-o", "rss=", "-p", str(pid)],
            capture_output=True,
            text=True,
        )
        return int(result.stdout.strip()) / 1024

    def test_memory_single_file(self, perf_server: ServeServer) -> None:
        for _ in range(10):
            httpx.get(f"{perf_server.base_url}/", timeout=5.0)
        mb = self._rss_mb(perf_server.pid)  # type: ignore[attr-defined]
        print(f"\n=== [{IMPL}] Memory RSS (single file) ===\n  {mb:.1f} MB")

    def test_memory_large_dir(self, large_dir_server: ServeServer) -> None:
        for _ in range(10):
            httpx.get(f"{large_dir_server.base_url}/api/files", timeout=5.0)
        mb = self._rss_mb(large_dir_server.pid)  # type: ignore[attr-defined]
        print(f"\n=== [{IMPL}] Memory RSS (large dir, ~50 files) ===\n  {mb:.1f} MB")


# ---------------------------------------------------------------------------
# File watch latency
# ---------------------------------------------------------------------------


class TestWatchLatency:
    def _measure_watch_latency(
        self, server: ServeServer, target_file: Path, n: int = 10
    ) -> list[float]:
        ws_url = f"ws://localhost:{server.port}/ws"
        latencies = []
        content = target_file.read_text()
        for i in range(n):
            new_content = content + f"\n\n<!-- perf probe {i} -->\n"
            with connect(ws_url) as ws:
                time.sleep(0.05)
                t0 = time.perf_counter()
                target_file.write_text(new_content)
                deadline = time.monotonic() + 5.0
                while time.monotonic() < deadline:
                    try:
                        msg = json.loads(ws.recv(timeout=0.5))
                        if msg.get("type") == "reload":
                            latencies.append((time.perf_counter() - t0) * 1000)
                            break
                    except TimeoutError:
                        continue
                else:
                    pytest.fail(f"No reload broadcast after 5s (iteration {i})")
            content = new_content
        return latencies

    def test_watch_single_file(self, perf_server: ServeServer) -> None:
        target = perf_server.target_file  # type: ignore[attr-defined]
        latencies = self._measure_watch_latency(perf_server, target)
        _report(f"[{IMPL}] File watch latency (single file mode)", latencies)
        print("  (includes debounce delay — ~300-500 ms floor is expected)")

    def test_watch_dir_file(self, large_dir_server: ServeServer) -> None:
        target = large_dir_server.dir_root / "docs" / "auth.md"  # type: ignore[attr-defined]
        latencies = self._measure_watch_latency(large_dir_server, target)
        _report(f"[{IMPL}] File watch latency (dir mode, nested file)", latencies)
        print("  (includes debounce delay — ~300-500 ms floor is expected)")
