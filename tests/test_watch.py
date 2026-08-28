"""
Black-box tests for `serve watch` — the JSONL event stream over comment changes.
"""

import json
import queue
import shutil
import subprocess
import threading
import time
from pathlib import Path

import pytest

from conftest import (
    COMMENTS_DIR,
    child_env,
    FIXTURES_DIR,
    ServeServer,
    _free_port,
    _serve_cmd_parts,
    _start_server,
    make_comment,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


class WatchProcess:
    """Wraps a `serve watch` subprocess with a background stdout reader."""

    def __init__(self, args: list[str]):
        cmd = _serve_cmd_parts() + ["watch"] + args
        self.proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=Path(__file__).parent.parent,
            text=True,
            bufsize=1,
            env=child_env(),
        )
        self.events: queue.Queue[dict] = queue.Queue()
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

    def _read_loop(self) -> None:
        assert self.proc.stdout is not None
        for line in self.proc.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                self.events.put(json.loads(line))
            except json.JSONDecodeError:
                # Surface as raw text for debugging
                self.events.put({"_raw": line})

    def next_event(self, timeout: float = 3.0) -> dict:
        return self.events.get(timeout=timeout)

    def drain_initial(self, timeout: float = 2.0) -> list[dict]:
        """Collect every `initial` event still pending, return them in order."""
        out: list[dict] = []
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                ev = self.events.get(timeout=0.2)
            except queue.Empty:
                # No more events arriving — done.
                break
            if ev.get("event") == "initial":
                out.append(ev)
                continue
            # First non-initial event: put it back and stop
            self.events.put(ev)
            break
        return out

    def assert_no_more(self, duration: float = 0.5) -> None:
        try:
            ev = self.events.get(timeout=duration)
        except queue.Empty:
            return
        raise AssertionError(f"unexpected extra event: {ev}")

    def close(self) -> None:
        self.proc.terminate()
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=5)


@pytest.fixture()
def watch_proc():
    procs: list[WatchProcess] = []

    def _make(args: list[str]) -> WatchProcess:
        wp = WatchProcess(args)
        procs.append(wp)
        return wp

    yield _make
    for wp in procs:
        wp.close()


def _wait_for_event(wp: WatchProcess, predicate, timeout: float = 3.0) -> dict:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            ev = wp.next_event(timeout=max(0.0, deadline - time.monotonic()))
        except queue.Empty:
            break
        if predicate(ev):
            return ev
    raise AssertionError(f"timeout waiting for predicate; events so far drained")


def _doc_id_for(path: Path) -> str:
    """Run `serve comments <file>` to recover the inode-based store key."""
    result = subprocess.run(
        _serve_cmd_parts() + ["comments", str(path)],
        cwd=Path(__file__).parent.parent,
        capture_output=True,
        text=True,
        check=True,
        env=child_env(),
    )
    return json.loads(result.stdout)["doc_id"]


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------


class TestInitialReplay:
    def test_emits_initial_for_unresolved_comments(self, md_server: ServeServer, md_file: Path, watch_proc):
        # Pre-populate: two unresolved, one resolved
        c1 = make_comment(md_server, text="first")
        c2 = make_comment(md_server, text="second")
        c3 = make_comment(md_server, text="third")
        md_server.patch(f"/api/comments/{c3['id']}", json={"resolved": True})
        md_server.register_doc_id(_doc_id_for(md_file))

        wp = watch_proc([str(md_file)])
        initials = wp.drain_initial(timeout=2.0)
        ids = {e["comment_id"] for e in initials}
        assert c1["id"] in ids
        assert c2["id"] in ids
        assert c3["id"] not in ids, "resolved comments must be skipped in initial replay"

    def test_no_initials_with_new_flag(self, md_server: ServeServer, md_file: Path, watch_proc):
        c1 = make_comment(md_server, text="existing")
        md_server.register_doc_id(_doc_id_for(md_file))

        wp = watch_proc([str(md_file), "--new"])
        wp.assert_no_more(duration=0.8)


class TestLiveEvents:
    def test_new_comment_via_api_emits_event(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        created = make_comment(md_server, text="brand new")
        ev = _wait_for_event(wp, lambda e: e.get("event") == "new_comment")
        assert ev["comment_id"] == created["id"]
        assert ev["text"] == "brand new"
        assert ev["file"] == str(md_file)

    def test_reply_emits_new_reply(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        parent = make_comment(md_server, text="parent")
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        reply = make_comment(md_server, text="reply", parent_id=parent["id"])
        ev = _wait_for_event(wp, lambda e: e.get("event") == "new_reply")
        assert ev["comment_id"] == reply["id"]
        assert ev["parent_id"] == parent["id"]

    def test_edit_emits_edited(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        c = make_comment(md_server, text="original")
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        md_server.patch(f"/api/comments/{c['id']}", json={"text": "updated"})
        ev = _wait_for_event(wp, lambda e: e.get("event") == "edited")
        assert ev["comment_id"] == c["id"]
        assert ev["text"] == "updated"

    def test_resolve_emits_resolved(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        c = make_comment(md_server)
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        ev = _wait_for_event(wp, lambda e: e.get("event") == "resolved")
        assert ev["comment_id"] == c["id"]

    def test_unresolve_emits_unresolved(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        c = make_comment(md_server)
        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": True})
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        md_server.patch(f"/api/comments/{c['id']}", json={"resolved": False})
        ev = _wait_for_event(wp, lambda e: e.get("event") == "unresolved")
        assert ev["comment_id"] == c["id"]

    def test_delete_emits_deleted(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        c = make_comment(md_server)
        wp = watch_proc([str(md_file)])
        wp.drain_initial(timeout=0.5)

        md_server.delete(f"/api/comments/{c['id']}")
        ev = _wait_for_event(wp, lambda e: e.get("event") == "deleted")
        assert ev["comment_id"] == c["id"]


class TestNewFlag:
    def test_filters_to_creations_only(self, md_server: ServeServer, md_file: Path, watch_proc):
        md_server.register_doc_id(_doc_id_for(md_file))
        existing = make_comment(md_server, text="will be edited")
        wp = watch_proc([str(md_file), "--new"])
        # No initials should arrive — confirm by waiting a bit.
        time.sleep(0.3)

        # An edit must NOT produce an event.
        md_server.patch(f"/api/comments/{existing['id']}", json={"text": "edited"})
        time.sleep(0.5)

        # A resolve must NOT produce an event.
        md_server.patch(f"/api/comments/{existing['id']}", json={"resolved": True})
        time.sleep(0.5)

        # A delete must NOT produce an event.
        md_server.delete(f"/api/comments/{existing['id']}")
        time.sleep(0.5)

        # A new comment SHOULD produce a single new_comment event.
        created = make_comment(md_server, text="finally new")
        ev = _wait_for_event(wp, lambda e: True, timeout=3.0)
        assert ev["event"] == "new_comment"
        assert ev["comment_id"] == created["id"]


class TestAllFilesMode:
    def test_events_for_multiple_files(self, md_file: Path, tmp_path: Path, watch_proc):
        # Two distinct files served separately, one watcher with no path.
        second = tmp_path / "second.md"
        shutil.copy(FIXTURES_DIR / "simple.md", second)

        port_a = _free_port()
        port_b = _free_port()
        proc_a, url_a = _start_server(str(md_file), port_a)
        proc_b, url_b = _start_server(str(second), port_b)
        try:
            doc_id_a = _doc_id_for(md_file)
            doc_id_b = _doc_id_for(second)

            wp = watch_proc([])  # all-files mode
            wp.drain_initial(timeout=0.5)

            srv_a = ServeServer(url_a, port_a, proc_a, [doc_id_a])
            srv_b = ServeServer(url_b, port_b, proc_b, [doc_id_b])
            c_a = make_comment(srv_a, text="from A")
            c_b = make_comment(srv_b, text="from B")

            events = []
            deadline = time.monotonic() + 5.0
            while time.monotonic() < deadline and len(events) < 2:
                try:
                    events.append(wp.next_event(timeout=max(0.0, deadline - time.monotonic())))
                except queue.Empty:
                    break

            by_id = {e["comment_id"]: e for e in events if e.get("event") == "new_comment"}
            assert c_a["id"] in by_id, f"missing event for file A; got {events}"
            assert c_b["id"] in by_id, f"missing event for file B; got {events}"
            assert by_id[c_a["id"]]["file"] == str(md_file)
            assert by_id[c_b["id"]]["file"] == str(second)
        finally:
            for p in (proc_a, proc_b):
                p.terminate()
                p.wait(timeout=5)
            for doc_id in (_doc_id_for(md_file) if md_file.exists() else None,):
                if doc_id:
                    (COMMENTS_DIR / f"{doc_id}.json").unlink(missing_ok=True)


class TestSurvivesAtomicWrite:
    """An external editor rewriting the file via write-temp + rename used to
    orphan the comment store: the server kept writing to the old inode's key
    while CLI tools read from the new one. After the fix, both converge."""

    def _atomic_rewrite(self, path: Path, content: str) -> None:
        tmp = path.with_suffix(path.suffix + ".atomic-tmp")
        tmp.write_text(content)
        tmp.replace(path)

    def test_browser_post_after_atomic_rewrite_is_visible_to_cli(self, md_file: Path):
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        try:
            srv = ServeServer(base_url, port, proc, [])
            # First comment lands under the original inode's storeKey.
            c1 = make_comment(srv, text="before rewrite")
            old_doc_id = _doc_id_for(md_file)

            # External editor rewrites the file atomically — inode flips.
            self._atomic_rewrite(md_file, md_file.read_text() + "\nappended\n")
            new_doc_id = _doc_id_for(md_file)
            if old_doc_id == new_doc_id:
                pytest.skip("atomic rewrite did not change inode on this fs")

            # New POST from the browser must land where CLI can find it.
            c2 = make_comment(srv, text="after rewrite")

            # CLI reads via the *new* storeKey and must see BOTH comments.
            result = subprocess.run(
                _serve_cmd_parts() + ["comments", str(md_file)],
                cwd=Path(__file__).parent.parent,
                capture_output=True,
                text=True,
                check=True,
                env=child_env(),
            )
            data = json.loads(result.stdout)
            ids = {c["id"] for c in data["comments"]}
            assert c1["id"] in ids, "pre-rewrite comment should be adopted"
            assert c2["id"] in ids, "post-rewrite comment should be in same file"
            assert data["doc_id"] == new_doc_id
        finally:
            proc.terminate()
            proc.wait(timeout=5)
            for doc_id in (old_doc_id, new_doc_id if 'new_doc_id' in locals() else None):
                if doc_id:
                    (COMMENTS_DIR / f"{doc_id}.json").unlink(missing_ok=True)

    def test_watch_keeps_emitting_after_atomic_rewrite(self, md_file: Path, watch_proc):
        # serve watch <file> used to go silent after the source file's inode
        # changed: the watcher kept filtering events by the originally-resolved
        # storeKey while the server wrote comments to a new key. The watcher
        # must follow inode drift.
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        old_doc_id = _doc_id_for(md_file)
        new_doc_id = None
        try:
            srv = ServeServer(base_url, port, proc, [old_doc_id])

            # First comment establishes activity on the original storeKey.
            c1 = make_comment(srv, text="before rewrite")

            wp = watch_proc([str(md_file)])
            wp.drain_initial(timeout=1.0)

            # Atomic rewrite under the watcher's nose.
            self._atomic_rewrite(md_file, md_file.read_text() + "\nappended\n")
            new_doc_id = _doc_id_for(md_file)
            if old_doc_id == new_doc_id:
                pytest.skip("atomic rewrite did not change inode on this fs")
            srv.register_doc_id(new_doc_id)

            # Add several comments after the rewrite — every one must emit.
            c2 = make_comment(srv, text="after rewrite 1")
            c3 = make_comment(srv, text="after rewrite 2")
            c4 = make_comment(srv, text="after rewrite 3")

            seen: set[str] = set()
            deadline = time.monotonic() + 5.0
            wanted = {c2["id"], c3["id"], c4["id"]}
            while seen != wanted and time.monotonic() < deadline:
                try:
                    ev = wp.next_event(timeout=max(0.0, deadline - time.monotonic()))
                except queue.Empty:
                    break
                if ev.get("event") == "new_comment":
                    seen.add(ev["comment_id"])

            missing = wanted - seen
            assert not missing, f"watcher went silent for post-rewrite comments: {missing}"
        finally:
            proc.terminate()
            proc.wait(timeout=5)
            for doc_id in (old_doc_id, new_doc_id):
                if doc_id:
                    (COMMENTS_DIR / f"{doc_id}.json").unlink(missing_ok=True)


class TestSurvivesServerRestart:
    def test_watcher_keeps_emitting_after_server_restart(self, md_file: Path, watch_proc):
        port = _free_port()
        proc, base_url = _start_server(str(md_file), port)
        doc_id = _doc_id_for(md_file)
        try:
            srv = ServeServer(base_url, port, proc, [doc_id])
            wp = watch_proc([str(md_file)])
            wp.drain_initial(timeout=0.5)

            # Restart the file server.
            proc.terminate()
            proc.wait(timeout=5)
            proc2, base_url2 = _start_server(str(md_file), port)
            srv2 = ServeServer(base_url2, port, proc2, [doc_id])
            try:
                created = make_comment(srv2, text="after restart")
                ev = _wait_for_event(wp, lambda e: e.get("event") == "new_comment", timeout=3.0)
                assert ev["comment_id"] == created["id"]
            finally:
                proc2.terminate()
                proc2.wait(timeout=5)
        finally:
            (COMMENTS_DIR / f"{doc_id}.json").unlink(missing_ok=True)
