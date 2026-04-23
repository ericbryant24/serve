"""Generate self-contained data URLs from served pages."""

import base64
import mimetypes
import re
from pathlib import Path

from serve.renderer import render


def _inline_images(html: str, base_dir: Path) -> str:
    """Replace local image sources with inline base64 data URLs."""

    def replace_src(match: re.Match) -> str:
        prefix = match.group(1)
        src = match.group(2)
        suffix = match.group(3)

        # Skip URLs and data URIs
        if src.startswith(("http://", "https://", "data:", "//")):
            return match.group(0)

        img_path = (base_dir / src).resolve()
        if not img_path.exists() or not img_path.is_file():
            return match.group(0)

        mime_type = mimetypes.guess_type(str(img_path))[0] or "application/octet-stream"
        img_data = base64.b64encode(img_path.read_bytes()).decode("ascii")
        return f"{prefix}data:{mime_type};base64,{img_data}{suffix}"

    return re.sub(
        r"""(<img\s[^>]*?src=["'])([^"']+)(["'])""",
        replace_src,
        html,
        flags=re.IGNORECASE,
    )


def generate_data_url(file_path: Path, mode: str) -> str:
    """Generate a data URL for the given file.

    Renders the page as self-contained HTML (no reload script, local images
    inlined as base64) and returns a ``data:text/html;base64,...`` URL.
    """
    file_path = file_path.resolve()
    base_dir = file_path.parent

    if mode == "markdown":
        html = render(file_path)
    else:
        html = file_path.read_text(encoding="utf-8")

    # Strip the live-reload WebSocket script — it won't work outside the server
    html = re.sub(
        r"<script>\(function\(\)\s*\{\s*function connect\(\).*?</script>",
        "",
        html,
        flags=re.DOTALL,
    )

    html = _inline_images(html, base_dir)

    encoded = base64.b64encode(html.encode("utf-8")).decode("ascii")
    return f"data:text/html;base64,{encoded}"
