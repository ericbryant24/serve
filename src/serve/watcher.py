"""File watching with change notification."""

from collections.abc import Awaitable, Callable
from pathlib import Path

from watchfiles import awatch, Change

# Asset extensions to watch in markdown mode
_ASSET_EXTENSIONS = {".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".css", ".js"}


async def watch(
    file_path: Path,
    on_change: Callable[[], Awaitable[None]],
    *,
    markdown_mode: bool = False,
) -> None:
    """Watch a file (and related assets) for changes, calling on_change when detected."""
    watch_dir = file_path.parent
    target = file_path.resolve()

    def _filter(change: Change, path: str) -> bool:
        p = Path(path).resolve()
        if p == target:
            return True
        if markdown_mode and p.suffix.lower() in _ASSET_EXTENSIONS:
            return True
        return False

    async for _changes in awatch(watch_dir, watch_filter=_filter, debounce=500):
        await on_change()


async def watch_directory(
    directory: Path,
    on_change: Callable[[], Awaitable[None]],
) -> None:
    """Watch an entire directory tree for changes, calling on_change when detected."""
    root = directory.resolve()

    def _filter(change: Change, path: str) -> bool:
        # Skip hidden files/directories (.git, .DS_Store, etc.)
        parts = Path(path).relative_to(root).parts
        return not any(p.startswith(".") for p in parts)

    async for _changes in awatch(root, watch_filter=_filter, debounce=500, recursive=True):
        await on_change()
