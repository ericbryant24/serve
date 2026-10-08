"""Comments stay on their text while the file changes under them."""

import pytest

from conftest import SPEC, leaf_selection


def comment(d, path, phrase):
    r = d.api("POST", "threads", {"path": str(path), "scope": "text", "text": "c", "selection": leaf_selection(d, path, phrase)})
    assert r.status_code == 201, r.text
    return r.json()["id"]


CASES = [
    # (edit, phrase, expected state, expected current text, expected line)
    (lambda s: s.replace("## Goals", "## Summary\n\nNew intro.\n\n## Goals"), "up to three times", "ok", "up to three times", 9),
    (lambda s: s.replace("three", "four"), "up to three times", "changed", "up to four times", 5),
    (lambda s: s.replace("We do not retry ACH payments.", "ACH and SEPA are out of scope."), "ACH payments", "changed", "ACH and SEPA are out of scope", 11),
    (lambda s: s.replace("Retries stop when the customer updates their card.\n\n", ""), "customer updates", "deleted", "", 7),
    (lambda s: s.replace("skip weekends?", "skip weekends and holidays?"), "skip weekends", "ok", "skip weekends", 15),
    (lambda s: s.replace("We retry failed card payments up to three times over five days.\n\n", "") + "\n## Moved\n\nWe retry failed card payments up to three times over five days.\n", "up to three times", "ok", "up to three times", 18),
    (lambda s: s.replace("up to three times", "up to\nthree times"), "up to three times", "ok", "up to\nthree times", 5),
]


@pytest.mark.parametrize("edit,phrase,state,current,line", CASES)
def test_edits(cli, daemon, docs, edit, phrase, state, current, line):
    spec = docs / "spec.md"
    tid = comment(daemon, spec, phrase)
    spec.write_text(edit(spec.read_text()))
    th = next(t for t in cli.json("comments", spec)["threads"] if t["id"] == tid)
    assert th["location"]["state"] == state
    assert th["location"].get("current_text", "") == current
    assert th["source_line_start"] == line
    assert th["anchor_text"] == phrase


def test_a_series_of_edits(cli, daemon, docs):
    spec = docs / "spec.md"
    tid = comment(daemon, spec, "Who owns the dunning emails?")
    text = spec.read_text()
    for i in range(5):
        text = text.replace("## Goals", f"## Goals\n\nInserted paragraph {i}.")
        spec.write_text(text)
        cli.json("comments", spec)
    th = cli.json("comments", spec)["threads"][0]
    assert th["location"]["state"] == "ok" and th["location"]["current_text"] == "Who owns the dunning emails?"
    assert th["source_line_start"] == 26


def test_highlight_follows_in_the_page(daemon, docs):
    spec = docs / "spec.md"
    comment(daemon, spec, "ACH payments")
    spec.write_text(spec.read_text().replace("# Payment retry spec", "# Payment retry spec\n\nPreface."))
    t = daemon.ok("GET", "threads?path=" + str(spec))["threads"][0]
    seg = t["placement"]["segments"][0]
    assert seg["text"] == "ACH payments"


def test_repeated_text_keeps_its_own_copy(cli, daemon, docs):
    spec = docs / "spec.md"
    spec.write_text("# T\n\nSee the docs.\n\nMiddle.\n\nSee the docs.\n")
    sel = leaf_selection(daemon, spec, "Middle.")
    # Select the second "See the docs." by taking the leaf after "Middle."
    from conftest import page_data
    import re

    html = page_data(daemon, spec)["doc"]["html"]
    keys = re.findall(r'<p data-b="([^"]+)" data-t="">See the docs.</p>', html)
    r = daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "this one", "selection": {"start_key": keys[1], "start_offset": 0, "end_key": keys[1], "end_offset": 13, "quote": "See the docs."}})
    spec.write_text("# T\n\nNew first.\n\nSee the docs.\n\nMiddle.\n\nSee the docs.\n")
    th = cli.json("comments", spec)["threads"][0]
    assert th["source_line_start"] == 9 and sel
