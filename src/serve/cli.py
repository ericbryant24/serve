"""CLI entry point for the serve tool."""

import argparse
import asyncio
import json
import sys
from dataclasses import asdict
from pathlib import Path


def _resolve_file(path: str | None) -> Path:
    """Resolve a file path argument, defaulting to index.html."""
    if path:
        file_path = Path(path).resolve()
    else:
        file_path = Path.cwd() / "index.html"
    if not file_path.exists():
        print(f"Error: {file_path} not found", file=sys.stderr)
        sys.exit(1)
    return file_path


def _resolve_path(path: str | None) -> Path:
    """Resolve a file or directory path, defaulting to current directory if none given."""
    if path:
        p = Path(path).resolve()
    else:
        # No arg: try index.html in cwd, then fall back to cwd as directory
        index = Path.cwd() / "index.html"
        if index.is_file():
            return index
        return Path.cwd()
    if not p.exists():
        print(f"Error: {p} not found", file=sys.stderr)
        sys.exit(1)
    return p


def _cmd_comments(argv: list[str]) -> None:
    """List comments for a document as JSON."""
    from serve.comments import CommentStore, get_document_id

    parser = argparse.ArgumentParser(
        prog="serve comments",
        description="List inline comments for a document as JSON",
    )
    parser.add_argument("file", help="Document file (.html, .htm, or .md)")
    args = parser.parse_args(argv)

    file_path = _resolve_file(args.file)
    doc_id = get_document_id(file_path)
    if not doc_id:
        print(json.dumps({"comments": []}, indent=2))
        return

    store = CommentStore(doc_id)
    comments = store.list_comments()
    data = {"file": str(file_path), "doc_id": doc_id, "comments": [asdict(c) for c in comments]}
    print(json.dumps(data, indent=2))


def _cmd_resolve(argv: list[str]) -> None:
    """Resolve one or more comments by ID."""
    from serve.comments import CommentStore, get_document_id

    parser = argparse.ArgumentParser(
        prog="serve resolve",
        description="Mark inline comments as resolved",
    )
    parser.add_argument("file", help="Document file (.html, .htm, or .md)")
    parser.add_argument("comment_ids", nargs="+", help="Comment ID(s) to resolve")
    args = parser.parse_args(argv)

    file_path = _resolve_file(args.file)
    doc_id = get_document_id(file_path)
    if not doc_id:
        print("Error: no comments found for this document", file=sys.stderr)
        sys.exit(1)

    store = CommentStore(doc_id)
    for comment_id in args.comment_ids:
        comment = store.update_comment(comment_id, resolved=True)
        if comment:
            print(f"Resolved: {comment_id}")
        else:
            print(f"Not found: {comment_id}", file=sys.stderr)


def _cmd_agent_init(argv: list[str]) -> None:
    """Bootstrap agent integration."""
    from serve.agent_init import cmd_agent_init

    cmd_agent_init(argv)


def _cmd_serve(argv: list[str]) -> None:
    """Start the document server (default command)."""
    from serve.server import Server

    parser = argparse.ArgumentParser(
        prog="serve",
        description="Serve HTML/Markdown files with live reload and inline comments",
        epilog="subcommands:\n"
               "  serve agent-init                   Set up agent integration\n"
               "  serve comments <file>              List inline comments (JSON)\n"
               "  serve resolve <file> <id> [id...]  Mark comments as resolved\n",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "file",
        nargs="?",
        help="File or directory to serve. Files: .html, .htm, .md. "
             "Directories: serve all files with a sidebar. "
             "Defaults to index.html if present, otherwise current directory.",
    )
    parser.add_argument(
        "-p", "--port",
        type=int,
        default=8000,
        help="Port to serve on (default: 8000)",
    )
    parser.add_argument(
        "--host",
        default="localhost",
        help="Host to bind to (default: localhost)",
    )
    parser.add_argument(
        "--no-open",
        action="store_true",
        help="Don't open the browser automatically",
    )
    parser.add_argument(
        "--data-url",
        action="store_true",
        help="Generate a data URL and copy to clipboard (no server started)",
    )

    args = parser.parse_args(argv)
    file_path = _resolve_path(args.file)

    # Determine mode
    if file_path.is_dir():
        mode = "directory"
    else:
        suffix = file_path.suffix.lower()
        if suffix == ".md":
            mode = "markdown"
        elif suffix in {".html", ".htm"}:
            mode = "html"
        else:
            print(f"Error: unsupported file type '{suffix}' (use .html, .htm, or .md)", file=sys.stderr)
            sys.exit(1)

    if args.data_url:
        if mode == "directory":
            print("Error: --data-url is not supported for directories", file=sys.stderr)
            sys.exit(1)
        import subprocess
        from serve.dataurl import generate_data_url

        url = generate_data_url(file_path, mode)
        try:
            subprocess.run(["pbcopy"], input=url.encode(), check=True)
            print(f"Data URL copied to clipboard ({len(url):,} bytes)")
        except (FileNotFoundError, subprocess.CalledProcessError):
            print(url)
        return

    server = Server(
        file_path,
        mode=mode,
        host=args.host,
        port=args.port,
        open_browser=not args.no_open,
    )

    try:
        asyncio.run(server.start())
    except KeyboardInterrupt:
        print("\nStopped")


_SUBCOMMANDS = {
    "agent-init": _cmd_agent_init,
    "comments": _cmd_comments,
    "resolve": _cmd_resolve,
}


def main() -> None:
    # Check if the first argument is a known subcommand
    if len(sys.argv) > 1 and sys.argv[1] in _SUBCOMMANDS:
        _SUBCOMMANDS[sys.argv[1]](sys.argv[2:])
    else:
        _cmd_serve(sys.argv[1:])
