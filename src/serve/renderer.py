"""Markdown to HTML rendering pipeline."""

import mimetypes
import re
from pathlib import Path

from markdown_it import MarkdownIt
from markdown_it.renderer import RendererHTML
from pygments import highlight
from pygments.formatters import HtmlFormatter
from pygments.lexers import get_lexer_by_name, get_lexer_for_filename
from pygments.util import ClassNotFound

from serve.templates import wrap_markdown, wrap_code, wrap_image, wrap_pdf, wrap_plain

_formatter = HtmlFormatter(cssclass="highlight", nowrap=False)


def _fence_renderer(
    self: RendererHTML, tokens: list, idx: int, options: dict, env: dict
) -> str:
    """Custom fence renderer that handles mermaid blocks and syntax highlighting."""
    token = tokens[idx]
    info = token.info.strip() if token.info else ""
    content = token.content

    source_lines = token.attrGet("data-source-lines")
    data_attr = f' data-source-lines="{source_lines}"' if source_lines else ""

    # Mermaid blocks: output <pre class="mermaid"> for mermaid.js
    if info == "mermaid":
        return f'<pre class="mermaid"{data_attr}>{content}</pre>\n'

    # Syntax highlighting with Pygments (only when language is specified)
    if info:
        try:
            lexer = get_lexer_by_name(info, stripall=True)
            return f'<div class="highlight"{data_attr}>{highlight(content, lexer, _formatter)}</div>\n'
        except ClassNotFound:
            pass

    # Fallback: plain code block
    escaped = (
        content.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )
    if info:
        return f'<pre{data_attr}><code class="language-{info}">{escaped}</code></pre>\n'
    return f"<pre{data_attr}><code>{escaped}</code></pre>\n"


def _source_lines_rule(state) -> None:
    """Core rule that adds data-source-lines attributes to block-level tokens."""
    for token in state.tokens:
        if token.map is not None and token.nesting >= 0:
            start = token.map[0] + 1  # convert 0-indexed to 1-indexed
            end = token.map[1]
            token.attrSet("data-source-lines", f"{start}-{end}")


def _build_parser() -> MarkdownIt:
    """Create a configured markdown-it parser."""
    md = MarkdownIt("commonmark", {"html": True, "typographer": True})
    md.enable("table")
    md.enable("strikethrough")
    # Override the fence renderer
    md.add_render_rule("fence", _fence_renderer)
    # Add source line annotations for comment anchoring
    md.core.ruler.push("source_lines", _source_lines_rule)
    return md


_parser = _build_parser()


def _strip_frontmatter(source: str) -> str:
    """Strip YAML frontmatter, replacing with blank lines to preserve line numbers."""
    m = re.match(r"^---\s*\n(.*?\n)---\s*\n", source, re.DOTALL)
    if not m:
        return source
    # Replace frontmatter with the same number of blank lines
    fm_lines = m.group(0).count("\n")
    return "\n" * fm_lines + source[m.end():]


def render(
    file_path: Path,
    *,
    sidebar: tuple[str, str] | None = None,
) -> str:
    """Read a markdown file and return a complete HTML document."""
    source = file_path.read_text(encoding="utf-8")
    source = _strip_frontmatter(source)
    content = _parser.render(source)
    pygments_css = _formatter.get_style_defs(".highlight")
    title = file_path.stem
    return wrap_markdown(title, content, pygments_css, sidebar=sidebar, favicon_path=str(file_path))


def render_code_file(
    file_path: Path,
    *,
    sidebar: tuple[str, str] | None = None,
) -> str:
    """Read a text file and return syntax-highlighted HTML."""
    source = file_path.read_text(encoding="utf-8")
    try:
        lexer = get_lexer_for_filename(file_path.name, stripall=True)
    except ClassNotFound:
        return wrap_plain(file_path.name, source, sidebar=sidebar, favicon_path=str(file_path))
    highlighted = highlight(source, lexer, _formatter)
    pygments_css = _formatter.get_style_defs(".highlight")
    return wrap_code(file_path.name, highlighted, pygments_css, sidebar=sidebar, favicon_path=str(file_path))


def render_pdf(
    file_path: Path,
    url_path: str,
    *,
    sidebar: tuple[str, str] | None = None,
) -> str:
    """Return an HTML page embedding a PDF."""
    return wrap_pdf(file_path.name, url_path, sidebar=sidebar, favicon_path=str(file_path))


def render_image(
    file_path: Path,
    url_path: str,
    *,
    sidebar: tuple[str, str] | None = None,
) -> str:
    """Return an HTML page displaying an image."""
    return wrap_image(file_path.name, url_path, sidebar=sidebar, favicon_path=str(file_path))


def can_render_as_code(file_path: Path) -> bool:
    """Check if a file can be syntax-highlighted by Pygments."""
    try:
        get_lexer_for_filename(file_path.name)
        return True
    except ClassNotFound:
        return False


def is_text_file(file_path: Path) -> bool:
    """Heuristic check for whether a file is likely text."""
    mime, _ = mimetypes.guess_type(file_path.name)
    if mime and mime.startswith("text/"):
        return True
    # Common text extensions not always in mimetypes
    text_extensions = {
        ".txt", ".log", ".cfg", ".ini", ".conf", ".env",
        ".csv", ".tsv", ".json", ".yaml", ".yml", ".toml",
        ".xml", ".svg", ".md", ".rst", ".tex",
    }
    return file_path.suffix.lower() in text_extensions
