"""Comments from the old JSON-per-document store come across once, intact."""

import json

from conftest import SPEC


def old_store(home, path, comments):
    d = home / ".serve" / "comments"
    d.mkdir(parents=True, exist_ok=True)
    (d / "a1b2c3d4.json").write_text(json.dumps({"path": str(path), "comments": comments}))


def test_import_flattens_threads_and_tells_authors_apart(cli, docs):
    spec = docs / "spec.md"
    old_store(cli.home, spec, [
        {"id": "r1", "text": "Why three?", "created_at": "2026-09-01T10:00:00Z", "resolved": False, "anchor_text": "up to three times",
         "block_text": "We retry failed card payments up to three times over five days.", "parent_id": None},
        {"id": "a1", "text": "Changed it.", "created_at": "2026-09-01T10:05:00Z", "resolved": True, "anchor_text": "", "block_text": "", "parent_id": "r1"},
        {"id": "h1", "text": "Thanks", "created_at": "2026-09-01T10:06:00Z", "resolved": False, "anchor_text": "up to three times", "block_text": "x", "parent_id": "a1"},
        {"id": "p1", "text": "Overall fine", "created_at": "2026-09-01T11:00:00Z", "resolved": True, "scope": "page", "anchor_text": "", "block_text": "", "parent_id": None},
    ])
    out = cli.json("comments", spec, "--all")
    threads = {t["text"]: t for t in out["threads"]}
    main = threads["Why three?"]
    assert [m["text"] for m in main["messages"]] == ["Why three?", "Changed it.", "Thanks"]
    assert [m["author"]["kind"] for m in main["messages"]] == ["human", "agent", "human"]
    assert main["source_line_start"] == 5 and main["location"]["state"] == "ok"
    assert threads["Overall fine"]["resolved"] and threads["Overall fine"]["scope"] == "page"
    # The JSON is left alone, and a second run does not import again.
    assert (cli.home / ".serve" / "comments" / "a1b2c3d4.json").exists()
    assert len(cli.json("comments", spec, "--all")["threads"]) == 2


def test_import_keeps_comments_for_files_that_are_gone(cli, docs, tmp_path):
    gone = tmp_path / "gone.md"
    old_store(cli.home, gone, [{"id": "r1", "text": "lost", "created_at": "2026-09-01T10:00:00Z", "resolved": False, "anchor_text": "x", "block_text": "y", "parent_id": None}])
    cli.json("comments", docs / "spec.md")  # any command runs the import
    assert "gone.md" in cli.run("gc").stdout
