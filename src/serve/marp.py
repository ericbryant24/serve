"""Marp slide rendering — shells out to marp-cli for `marp: true` documents."""

import html as _html
import re
import shutil
import subprocess
from pathlib import Path

from serve.templates import inject_reload_script


_FRONTMATTER_RE = re.compile(r"^---\s*\n(.*?\n)---\s*\n", re.DOTALL)
_SECTION_OPEN_RE = re.compile(r"<section\b([^>]*)>", re.IGNORECASE)
_TRUTHY = {"true", "yes", "on", "1"}


def _read(file_path: Path) -> str:
    return file_path.read_text(encoding="utf-8", errors="replace")


def _parse_frontmatter(content: str) -> dict[str, str] | None:
    """Return a flat dict of YAML frontmatter scalars, or None if absent.

    Only handles the `key: value` shape we need for directive detection.
    Marp's own renderer parses real YAML; we only need to know `marp: true`.
    """
    m = _FRONTMATTER_RE.match(content)
    if not m:
        return None
    fm: dict[str, str] = {}
    for line in m.group(1).splitlines():
        if ":" in line and not line.lstrip().startswith("#"):
            k, _, v = line.partition(":")
            fm[k.strip()] = v.strip().strip('"').strip("'")
    return fm


def is_marp_doc(file_path: Path) -> bool:
    """True if *file_path* is a markdown file with `marp: true` in frontmatter."""
    if file_path.suffix.lower() != ".md":
        return False
    try:
        content = _read(file_path)
    except OSError:
        return False
    fm = _parse_frontmatter(content)
    if not fm:
        return False
    return fm.get("marp", "").lower() in _TRUTHY


def _slide_line_ranges(content: str) -> list[tuple[int, int]]:
    """Compute 1-indexed (start, end) source line ranges for each slide.

    Frontmatter (leading `---...---`) is skipped. Slides are separated by
    lines matching `^---\\s*$`. Returned ranges cover only the slide's own
    content lines (separators excluded).
    """
    lines = content.splitlines()
    n = len(lines)

    body_start_idx = 0  # 0-indexed
    if lines and lines[0].strip() == "---":
        for i in range(1, n):
            if lines[i].strip() == "---":
                body_start_idx = i + 1
                break

    ranges: list[tuple[int, int]] = []
    slide_start_idx = body_start_idx
    for i in range(body_start_idx, n):
        if lines[i].strip() == "---":
            ranges.append((slide_start_idx + 1, i))  # convert to 1-indexed; end is line before separator
            slide_start_idx = i + 1
    if slide_start_idx < n:
        ranges.append((slide_start_idx + 1, n))
    elif not ranges:
        # Empty body but `marp: true` — emit one empty range so we don't crash later
        ranges.append((body_start_idx + 1, body_start_idx + 1))
    return ranges


def _resolve_marp_cmd() -> list[str] | None:
    """Find an executable that can run marp-cli, preferring a global install."""
    if shutil.which("marp"):
        return ["marp"]
    if shutil.which("npx"):
        return ["npx", "-y", "@marp-team/marp-cli"]
    return None


def _inject_section_source_lines(html: str, ranges: list[tuple[int, int]]) -> str:
    """Add `data-source-lines="N-M"` to each top-level `<section>` opening tag.

    Sections are matched in document order against *ranges*. Tags that
    already have `data-source-lines` are left alone (so re-runs are safe).
    """
    iter_ranges = iter(ranges)

    def _replace(match: re.Match) -> str:
        attrs = match.group(1) or ""
        if "data-source-lines" in attrs.lower():
            return match.group(0)
        try:
            start, end = next(iter_ranges)
        except StopIteration:
            return match.group(0)
        return f'<section{attrs} data-source-lines="{start}-{end}">'

    return _SECTION_OPEN_RE.sub(_replace, html)


def _missing_marp_page(file_path: Path, *, sidebar, favicon_path: str | None) -> str:
    body = (
        '<!doctype html><html><head>'
        '<meta charset="utf-8">'
        f'<title>{_html.escape(file_path.name)} — marp-cli required</title>'
        '<style>'
        'body{font:14px/1.5 -apple-system,Segoe UI,sans-serif;max-width:680px;'
        'margin:80px auto;padding:0 24px;color:#222}'
        'code{background:#f4f4f4;padding:2px 6px;border-radius:3px;'
        'font-family:Menlo,monospace;font-size:13px}'
        'pre{background:#f4f4f4;padding:16px;border-radius:6px;overflow-x:auto}'
        'h1{font-size:20px}.warn{color:#b54708}'
        '</style></head><body>'
        f'<h1>Marp deck — <code>{_html.escape(file_path.name)}</code></h1>'
        '<p class="warn">This document declares <code>marp: true</code>, '
        'but neither <code>marp</code> nor <code>npx</code> is on your PATH.</p>'
        '<p>Install one of:</p>'
        '<pre>npm install -g @marp-team/marp-cli</pre>'
        '<p>or install Node so the <code>npx</code> fallback works.</p>'
        '<p>The page will reload automatically once the file is saved.</p>'
        '</body></html>'
    )
    return inject_reload_script(
        body, sidebar=sidebar, favicon_path=favicon_path or "", annotate_html=False
    )


def _marp_error_page(
    file_path: Path, stderr: str, *, sidebar, favicon_path: str | None
) -> str:
    body = (
        '<!doctype html><html><head>'
        '<meta charset="utf-8">'
        f'<title>{_html.escape(file_path.name)} — marp error</title>'
        '<style>'
        'body{font:14px/1.5 -apple-system,Segoe UI,sans-serif;max-width:780px;'
        'margin:60px auto;padding:0 24px;color:#222}'
        'pre{background:#1e1e1e;color:#f4f4f4;padding:16px;border-radius:6px;'
        'overflow-x:auto;white-space:pre-wrap;font:12px Menlo,monospace}'
        'h1{font-size:20px;color:#b42318}'
        '</style></head><body>'
        f'<h1>marp-cli failed on {_html.escape(file_path.name)}</h1>'
        f'<pre>{_html.escape(stderr or "(no stderr output)")}</pre>'
        '<p>Fix the slide source and save — the page will reload.</p>'
        '</body></html>'
    )
    return inject_reload_script(
        body, sidebar=sidebar, favicon_path=favicon_path or "", annotate_html=False
    )


def render_marp(
    file_path: Path,
    *,
    sidebar: tuple[str, str] | None = None,
    favicon_path: str | None = None,
) -> str:
    """Render a Marp deck to a complete HTML page with reload + comment hooks."""
    cmd = _resolve_marp_cmd()
    if cmd is None:
        return _missing_marp_page(file_path, sidebar=sidebar, favicon_path=favicon_path)

    try:
        result = subprocess.run(
            [*cmd, "--html", str(file_path), "-o", "-"],
            capture_output=True,
            text=True,
            timeout=60,
        )
    except subprocess.TimeoutExpired:
        return _marp_error_page(
            file_path, "marp-cli timed out after 60s", sidebar=sidebar, favicon_path=favicon_path
        )
    except FileNotFoundError:
        return _missing_marp_page(file_path, sidebar=sidebar, favicon_path=favicon_path)

    if result.returncode != 0 or not result.stdout.strip():
        return _marp_error_page(
            file_path, result.stderr, sidebar=sidebar, favicon_path=favicon_path
        )

    html = result.stdout
    ranges = _slide_line_ranges(_read(file_path))
    html = _inject_section_source_lines(html, ranges)
    return inject_reload_script(
        html, sidebar=sidebar, favicon_path=favicon_path or "", annotate_html=False
    )
