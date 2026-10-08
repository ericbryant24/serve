"""The command line: what an agent uses to read, answer and resolve comments."""

import json
import subprocess
import time

import httpx
import pytest

from conftest import leaf_selection


def comment(d, path, phrase, text="Why?"):
    r = d.api("POST", "threads", {"path": str(path), "scope": "text", "text": text, "selection": leaf_selection(d, path, phrase)})
    assert r.status_code == 201, r.text
    return r.json()


def test_help_and_version(cli):
    out = cli.run("help").stdout
    for c in ("comments", "reply", "resolve", "wait", "watch", "inbox", "status", "stop"):
        assert c in out
    assert cli.run("--version").stdout.startswith("serve ")
    assert "--since" in cli.run("help", "wait").stdout


def test_unknown_flags_and_commands_are_errors(cli, docs):
    p = cli.run("comments", docs / "spec.md", "--bogus", check=False)
    assert p.returncode == 2 and "not defined" in p.stderr
    p = cli.run("frobnicate", check=False)
    assert p.returncode == 2 and "unknown command" in p.stderr


def test_comments_on_a_file_without_any(cli, docs):
    out = cli.json("comments", docs / "spec.md")
    assert out["threads"] == [] and "cursor" in out


def test_reply_resolve_and_current_lines(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "up to three times", "Why three?")
    out = cli.json("comments", spec)
    th = out["threads"][0]
    assert th["id"] == t["id"] and th["awaiting"] == "agent"
    assert th["anchor_text"] == "up to three times"
    assert th["source_line_start"] == 5 and th["location"]["state"] == "ok"

    # The agent edits above the comment and inside it.
    text = spec.read_text().replace("## Goals", "## Summary\n\nWhy retries matter.\n\n## Goals")
    text = text.replace("up to three times", "up to four times")
    spec.write_text(text)
    th = cli.json("comments", spec)["threads"][0]
    assert th["source_line_start"] == 9
    assert th["location"]["state"] == "changed"
    assert th["location"]["current_text"] == "up to four times"

    # A reply by prefix, signed as the agent.
    msg = cli.json("reply", spec, t["id"][:4], "Changed to four.", SERVE_AUTHOR="Claude")
    assert msg["author"] == {"kind": "agent", "name": "Claude"}
    th = cli.json("comments", spec)["threads"][0]
    assert th["awaiting"] == "human" and len(th["messages"]) == 2

    out = cli.run("resolve", spec, t["id"], "--note", "Done.").stdout
    assert "Resolved" in out
    assert cli.json("comments", spec)["threads"] == []
    th = cli.json("comments", spec, "--all")["threads"][0]
    assert th["resolved"] and th["messages"][-1]["text"] == "Done."

    cli.run("reopen", spec, t["id"])
    assert len(cli.json("comments", spec)["threads"]) == 1


def test_reply_to_a_reply_lands_in_the_same_thread(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "skip weekends")
    first = cli.json("reply", spec, t["id"], "one")
    cli.json("reply", spec, first["id"], "two")
    msgs = cli.json("comments", spec)["threads"][0]["messages"]
    assert [m["text"] for m in msgs] == ["Why?", "one", "two"]


def test_reply_text_from_stdin_and_as_human(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "ACH payments")
    m = cli.json("reply", spec, t["id"], "-", input="line one\n\n- a list\n", **{})
    assert m["text"] == "line one\n\n- a list"
    m = cli.json("reply", spec, t["id"], "from a person", "--as", "human:Dana")
    assert m["author"] == {"kind": "human", "name": "Dana"}


def test_edit_and_delete(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "dunning emails")
    r = cli.json("reply", spec, t["id"], "typo here")
    cli.run("edit", spec, r["id"], "fixed")
    assert cli.json("comments", spec)["threads"][0]["messages"][1]["text"] == "fixed"
    cli.run("delete", spec, r["id"])
    assert len(cli.json("comments", spec)["threads"][0]["messages"]) == 1
    cli.run("delete", spec, t["id"])
    assert cli.json("comments", spec)["threads"] == []
    assert cli.run("delete", spec, "zzzzzzzz", check=False).returncode == 1


def test_text_format(cli, daemon, docs):
    spec = docs / "spec.md"
    comment(daemon, spec, "skip weekends", "Yes, skip them.")
    out = cli.run("comments", spec, "--format", "text").stdout
    assert "awaiting agent" in out and "line 15" in out and "Yes, skip them." in out


def test_wait_since_a_cursor_misses_nothing(cli, daemon, docs):
    spec = docs / "spec.md"
    cursor = cli.json("comments", spec)["cursor"]
    # A comment arrives after the listing but before wait starts.
    comment(daemon, spec, "skip weekends", "Between listing and waiting")
    start = time.monotonic()
    p = cli.run("wait", spec, "--since", cursor, "--timeout", "5")
    assert time.monotonic() - start < 3
    ev = json.loads(p.stdout)
    assert ev["event"] == "new_comment" and ev["text"] == "Between listing and waiting"
    assert ev["source_line_start"] == 15 and ev["cursor"] > cursor


def test_wait_from_human_ignores_the_agents_own_replies(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "skip weekends")
    cursor = cli.json("comments", spec)["cursor"]
    cli.run("reply", spec, t["id"], "agent says hi")
    p = cli.run("wait", spec, "--since", cursor, "--from", "human", "--timeout", "1", check=False)
    assert p.returncode == 124
    cli.run("reply", spec, t["id"], "person answers", "--as", "human")
    ev = json.loads(cli.run("wait", spec, "--since", cursor, "--from", "human", "--timeout", "5").stdout)
    assert ev["event"] == "new_reply" and ev["text"] == "person answers"


def test_wait_times_out(cli, docs):
    p = cli.run("wait", docs / "spec.md", "--timeout", "1", check=False)
    assert p.returncode == 124


def test_watch_streams_and_exits_with_its_reader(cli, daemon, docs):
    spec = docs / "spec.md"
    comment(daemon, spec, "ACH payments", "existing")
    proc = cli.popen("watch", spec)
    first = json.loads(proc.stdout.readline())
    assert first["event"] == "initial" and first["text"] == "existing"
    comment(daemon, spec, "skip weekends", "fresh")
    ev = json.loads(proc.stdout.readline())
    assert ev["event"] == "new_comment" and ev["text"] == "fresh"
    proc.stdout.close()
    proc.wait(timeout=5)

    start = time.monotonic()
    sh = subprocess.run(f"{' '.join(cli_cmd())} watch {spec} | head -n1", shell=True, capture_output=True, text=True, env=cli.env(), timeout=10)
    assert time.monotonic() - start < 5 and sh.stdout.startswith("{")


def cli_cmd():
    from conftest import SERVE_CMD

    return SERVE_CMD


def test_inbox_lists_threads_waiting_for_the_agent(cli, daemon, docs):
    spec = docs / "spec.md"
    t = comment(daemon, spec, "ACH payments", "What about SEPA?")
    out = cli.json("inbox", "--json")
    assert out["documents"][0]["threads"][0]["id"] == t["id"]
    cli.run("reply", spec, t["id"], "Good question")
    assert cli.json("inbox", "--json")["documents"] == []
    assert cli.json("inbox", "--for", "human", "--json")["documents"][0]["threads"][0]["id"] == t["id"]


def test_export_json_and_html(cli, daemon, docs, tmp_path):
    spec = docs / "spec.md"
    comment(daemon, spec, "skip weekends")
    out = cli.json("export", spec)
    assert out["threads"][0]["anchor"]["quote"] == "skip weekends"
    html = tmp_path / "out.html"
    cli.run("export", spec, "--html", "-o", html)
    text = html.read_text()
    assert "<h1" in text and "Payment retry spec" in text and "/_serve/" not in text


def test_gc_lists_and_prunes_comments_on_missing_files(cli, daemon, docs):
    gone = docs / "gone.md"
    gone.write_text("# Gone\n\nSoon deleted.\n")
    daemon.ok("POST", "threads", {"path": str(gone), "scope": "page", "text": "x"})
    gone.unlink()
    assert "gone.md" in cli.run("gc").stdout
    cli.run("gc", "--prune")
    assert "still exists" in cli.run("gc").stdout


def test_open_starts_and_reuses_the_background_server(cli, docs):
    from conftest import free_port

    port = free_port()
    try:
        out = cli.run("open", docs / "spec.md", "--port", port, "--no-open").stdout
        assert f"http://localhost:{port}/" in out and "spec.md" in out
        info = json.loads((cli.home / ".serve" / "daemon.json").read_text())
        r = httpx.get(f"http://localhost:{port}/_serve/health")
        assert r.json()["pid"] == info["pid"]
        # A second open reuses it.
        cli.run(docs / "notes.txt", "--port", port, "--no-open")
        assert json.loads((cli.home / ".serve" / "daemon.json").read_text())["pid"] == info["pid"]
        st = cli.json("status", "--json")
        assert st["running"] and st["port"] == port
        assert any(f["path"] == str(docs) for f in st["folders"])
    finally:
        cli.run("stop")
    assert not cli.json("status", "--json")["running"]


def test_agent_init_writes_the_skill_and_claude_md(cli, tmp_path):
    proj = tmp_path / "proj"
    proj.mkdir()
    (proj / "CLAUDE.md").write_text("# Mine\n\nKeep this.\n\n# Inline Document Comments\n\nold text\n\n# After\n\nKeep this too.\n")
    p = subprocess.run(cli_cmd() + ["agent-init", "--project", "--yes"], cwd=proj, env=cli.env(), capture_output=True, text=True)
    assert p.returncode == 0, p.stderr
    skill = (proj / ".claude" / "skills" / "serve" / "SKILL.md").read_text()
    assert "serve wait" in skill and "--since" in skill
    md = (proj / "CLAUDE.md").read_text()
    assert "Keep this." in md and "Keep this too." in md and "old text" not in md
    assert md.count("# Inline Document Comments") == 1


def test_report_commands(cli):
    assert "No reports" in cli.run("report").stdout
    assert cli.run("report", "show", "abcdef123456", check=False).returncode == 1


def test_a_different_port_reuses_the_running_server(cli, docs):
    from conftest import free_port

    p1, p2 = free_port(), free_port()
    try:
        first = cli.json("open", docs / "spec.md", "--port", p1, "--json")
        assert first["port"] == p1 and first["embed_url"].endswith("?embed=1")
        pid = json.loads((cli.home / ".serve" / "daemon.json").read_text())["pid"]
        p = cli.run("open", docs / "notes.txt", "--port", p2, "--no-open")
        assert "already running on port" in p.stderr and f"localhost:{p1}" in p.stdout
        assert json.loads((cli.home / ".serve" / "daemon.json").read_text())["pid"] == pid
    finally:
        cli.run("stop")


def test_inbox_under_a_folder(cli, daemon, docs, tmp_path):
    other = tmp_path / "other"
    other.mkdir()
    (other / "x.md").write_text("# X\n\nSome text.\n")
    daemon.open(other)
    comment(daemon, docs / "spec.md", "skip weekends", "in docs")
    daemon.ok("POST", "threads", {"path": str(other / "x.md"), "scope": "page", "text": "in other"})
    out = cli.json("inbox", "--under", docs, "--json")
    assert [d["file"] for d in out["documents"]] == [str(docs / "spec.md")]
