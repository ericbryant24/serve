"""The HTTP API and the checks that keep other web pages out of it."""

import os

import httpx

from conftest import leaf_selection, page_data, url_path


def test_page_carries_its_data_and_token(daemon, docs):
    d = page_data(daemon, docs / "spec.md")
    assert d["view"] == "doc" and d["doc"]["kind"] == "markdown"
    assert d["root"]["path"] == str(docs)
    assert 'data-b="' in d["doc"]["html"]
    html = httpx.get(daemon.url(docs / "spec.md"), headers={"Accept": "text/html"}).text
    assert f'name="serve-token" content="{daemon.token}"' in html


def test_raw_files_and_images(daemon, docs):
    r = httpx.get(daemon.url(docs / "notes.txt") + "?raw=1")
    assert r.status_code == 200 and r.text.startswith("first line")
    # A non-navigation request (an <img>) gets the bytes, not a page.
    r = httpx.get(daemon.url(docs / "notes.txt"), headers={"Accept": "image/*"})
    assert r.text.startswith("first line")


def test_thread_lifecycle(daemon, docs):
    spec = docs / "spec.md"
    t = daemon.ok("POST", "threads", {"path": str(spec), "scope": "text", "text": "Why?", "selection": leaf_selection(daemon, spec, "skip weekends")})
    assert t["placement"]["segments"][0]["text"] == "skip weekends"
    t = daemon.ok("POST", f"threads/{t['id']}/messages", {"text": "Because **reasons**."})
    assert "<strong>reasons</strong>" in t["messages"][1]["html"]
    mid = t["messages"][1]["id"]
    t = daemon.ok("PATCH", f"messages/{mid}", {"text": "edited"})
    assert t["messages"][1]["text"] == "edited" and t["messages"][1]["edited_at"]
    assert daemon.ok("DELETE", f"messages/{mid}")["thread_deleted"] is False
    t = daemon.ok("PATCH", f"threads/{t['id']}", {"status": "resolved"})
    assert t["status"] == "resolved" and t["resolved_by"]["kind"] == "human"
    daemon.ok("DELETE", f"threads/{t['id']}")
    assert daemon.ok("GET", "threads?path=" + url_path(spec))["threads"] == []


def test_element_and_page_comments(daemon, docs):
    spec = docs / "spec.md"
    blocks = page_data(daemon, spec)["doc"]["html"]
    import re

    key = re.search(r'<ul data-b="([^"]+)"', blocks).group(1)
    t = daemon.ok("POST", "threads", {"path": str(spec), "scope": "element", "text": "Reorder", "element": {"key": key, "element": {"tag": "ul", "label": "list"}}})
    assert t["placement"]["element"] == key and t["location"]["line_start"] == 15
    p = daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "Overall"})
    assert p["location"]["state"] == "page"


def test_stale_selection_is_a_conflict(daemon, docs):
    r = daemon.api("POST", "threads", {"path": str(docs / "spec.md"), "scope": "text", "text": "x", "selection": {"start_key": "nope", "start_offset": 0, "end_key": "nope", "end_offset": 4, "quote": "no such words"}})
    assert r.status_code == 409


def test_code_and_text_files_take_comments(daemon, docs):
    go = docs / "sub" / "main.go"
    t = daemon.ok("POST", "threads", {"path": str(go), "scope": "text", "text": "logger", "selection": leaf_selection(daemon, go, "Println")})
    assert t["location"]["line_start"] == 6 and t["location"]["current_text"] == "Println"


def test_save_detects_conflicts(daemon, docs):
    spec = docs / "spec.md"
    f = daemon.ok("GET", "file?path=" + url_path(spec))
    spec.write_text(spec.read_text() + "\nAgent was here.\n")
    r = daemon.api("PUT", "file", {"path": str(spec), "content": "mine", "base_rev": f["rev"]})
    assert r.status_code == 409 and "Agent was here." in r.json()["content"]
    r = daemon.api("PUT", "file", {"path": str(spec), "content": "mine\n", "base_rev": r.json()["rev"]})
    assert r.status_code == 200 and spec.read_text() == "mine\n"


def test_tree_search_and_folders(daemon, docs, tmp_path):
    names = [e["name"] for e in daemon.ok("GET", "tree?path=" + url_path(docs))["entries"]]
    assert names == ["sub", "notes.txt", "page.html", "spec.md"]
    res = daemon.ok("GET", "search?root=" + url_path(docs) + "&q=main")["results"]
    assert res[0]["name"] == "sub/main.go"
    other = tmp_path / "other"
    other.mkdir()
    (other / "x.md").write_text("# X\n")
    assert httpx.get(daemon.url(other / "x.md"), headers={"Accept": "text/html"}).status_code == 403
    daemon.open(other)
    assert httpx.get(daemon.url(other / "x.md"), headers={"Accept": "text/html"}).status_code == 200


def test_missing_and_moved_files(daemon, docs):
    spec = docs / "spec.md"
    daemon.ok("POST", "threads", {"path": str(spec), "scope": "page", "text": "keep me"})
    (docs / "archive").mkdir()
    os.rename(spec, docs / "archive" / "spec.md")
    r = httpx.get(daemon.url(spec), headers={"Accept": "text/html"})
    assert r.status_code == 404
    d = page_data_from(r.text)
    assert d["view"] == "notfound" and d["suggestions"][0]["path"] == str(docs / "archive" / "spec.md")
    # Comments followed the file.
    moved = daemon.ok("GET", "threads?path=" + url_path(docs / "archive" / "spec.md"))["threads"]
    assert moved[0]["messages"][0]["text"] == "keep me"


def page_data_from(html):
    import json

    return json.loads(html.split('<script id="serve-data" type="application/json">', 1)[1].split("</script>", 1)[0])


# --- security ----------------------------------------------------------------------


def test_api_needs_the_token(daemon, docs):
    assert daemon.api("GET", "home", token="").status_code == 403
    assert daemon.api("GET", "home", token="wrong").status_code == 403
    assert daemon.api("GET", "home").status_code == 200


def test_cross_site_writes_are_refused(daemon, docs):
    spec = docs / "spec.md"
    before = spec.read_text()
    # What a page on another site can send without a preflight.
    r = httpx.put(f"{daemon.base}/_serve/api/file", content='{"path":"%s","content":"pwned"}' % spec, headers={"Content-Type": "text/plain", "Origin": "https://evil.example"})
    assert r.status_code == 403
    r = daemon.api("PUT", "file", {"path": str(spec), "content": "pwned"}, headers={"Origin": "https://evil.example"})
    assert r.status_code == 403
    r = httpx.post(f"{daemon.base}/_serve/api/folders", content='{"path":"/"}', headers={"Content-Type": "text/plain", "X-Serve-Token": daemon.token})
    assert r.status_code == 415
    assert spec.read_text() == before


def test_dns_rebinding_is_refused(daemon, docs):
    r = httpx.get(daemon.url(docs / "spec.md"), headers={"Host": f"evil.example:{daemon.port}", "Accept": "text/html"})
    assert r.status_code == 403


def test_the_content_port_has_no_token_and_no_api(daemon, docs):
    content = f"http://localhost:{daemon.content_port}"
    r = httpx.get(content + url_path(docs / "page.html") + "?frame=1")
    assert r.status_code == 200 and "/_serve/embed.js" in r.text and daemon.token not in r.text
    assert httpx.get(content + "/_serve/api/home", headers={"X-Serve-Token": daemon.token}).status_code == 403
    assert httpx.get(content + url_path(docs / "spec.md")).status_code == 200  # raw bytes only
    assert "serve-token" not in httpx.get(content + url_path(docs / "spec.md"), headers={"Accept": "text/html"}).text


def test_nothing_is_written_into_served_folders(daemon, docs):
    assert sorted(p.name for p in docs.iterdir()) == ["notes.txt", "page.html", "spec.md", "sub"]


def test_only_this_machines_apps_may_frame_serve(daemon, docs):
    r = httpx.get(daemon.url(docs / "spec.md") + "?embed=1", headers={"Accept": "text/html"})
    csp = r.headers["content-security-policy"]
    assert "frame-ancestors 'self' http://localhost:*" in csp and "https:" not in csp
    assert "http://*.localhost:*" in csp
    assert '"embedded":true' in r.text
