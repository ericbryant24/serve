"""Discover and signal running serve instances by querying the OS.

No persistent state — `ps`/`lsof` are the source of truth.
"""

from __future__ import annotations

import os
import re
import signal
import subprocess
from dataclasses import dataclass, asdict
from pathlib import Path
from typing import Iterator


@dataclass
class Instance:
    pid: int
    port: int | None
    path: str
    mode: str
    started: str
    cmdline: str

    @property
    def url(self) -> str | None:
        return f"http://localhost:{self.port}" if self.port else None

    def to_dict(self) -> dict:
        d = asdict(self)
        d["url"] = self.url
        return d


_FLAGS_WITH_VALUE = {"-p", "--port", "--host"}
_BARE_FLAGS = {"--no-open", "--data-url"}


def _parse_serve_argv(argv: list[str]) -> str | None:
    """Given the full argv of a `serve` invocation (python + script + args),
    recover the file/dir argument the user passed (the first positional)."""
    # Skip the python interpreter and entry-point script (or `-m serve`).
    rest: list[str]
    if len(argv) >= 3 and argv[1] == "-m" and argv[2] == "serve":
        rest = argv[3:]
    elif len(argv) >= 2 and Path(argv[1]).name == "serve":
        rest = argv[2:]
    else:
        return None

    i = 0
    while i < len(rest):
        a = rest[i]
        if a in _FLAGS_WITH_VALUE:
            i += 2
            continue
        if a in _BARE_FLAGS or a.startswith("-"):
            i += 1
            continue
        return a
    return None


def _is_serve_invocation(argv: list[str]) -> bool:
    if len(argv) < 2:
        return False
    if Path(argv[1]).name == "serve":
        return True
    if argv[1] == "-m" and len(argv) >= 3 and argv[2] == "serve":
        return True
    return False


def _ps_processes() -> Iterator[tuple[int, str]]:
    """Yield (pid, cmdline) for every running process. Putting `command=` last
    in the format string lets `ps` extend the field without truncation."""
    result = subprocess.run(
        ["ps", "-axo", "pid=,command="],
        capture_output=True,
        text=True,
        check=True,
    )
    for line in result.stdout.splitlines():
        line = line.lstrip()
        if not line:
            continue
        pid_str, _, cmdline = line.partition(" ")
        try:
            pid = int(pid_str)
        except ValueError:
            continue
        yield pid, cmdline.strip()


_LSOF_PORT_RE = re.compile(r":(\d+)\s*\(LISTEN\)")


def _listening_port(pid: int) -> int | None:
    """Return the lowest TCP port `pid` is listening on, or None."""
    try:
        result = subprocess.run(
            ["lsof", "-a", "-nP", "-iTCP", "-sTCP:LISTEN", "-p", str(pid)],
            capture_output=True,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        return None
    ports: list[int] = []
    for line in result.stdout.splitlines()[1:]:
        m = _LSOF_PORT_RE.search(line)
        if m:
            ports.append(int(m.group(1)))
    return min(ports) if ports else None


def _start_time(pid: int) -> str:
    """Return the human-readable start time of `pid`, or empty string."""
    try:
        result = subprocess.run(
            ["ps", "-o", "lstart=", "-p", str(pid)],
            capture_output=True,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        return ""
    return result.stdout.strip()


def _process_cwd(pid: int) -> Path | None:
    """Return the working directory of `pid` via lsof, or None."""
    try:
        result = subprocess.run(
            ["lsof", "-a", "-d", "cwd", "-Fn", "-p", str(pid)],
            capture_output=True,
            text=True,
            check=False,
        )
    except FileNotFoundError:
        return None
    for line in result.stdout.splitlines():
        if line.startswith("n"):
            return Path(line[1:])
    return None


def _resolve_served_path(positional: str | None, cwd: Path | None) -> tuple[str, str]:
    """Replicate cli._resolve_path against the *target* process's cwd to
    recover the absolute path it's actually serving."""
    if positional is None:
        # serve with no arg: index.html in cwd if present, else cwd itself
        if cwd is None:
            return ("(default)", "?")
        index = cwd / "index.html"
        if index.is_file():
            return (str(index), "html")
        return (str(cwd), "directory")

    p = Path(positional)
    if not p.is_absolute() and cwd is not None:
        p = (cwd / p).resolve()
    elif p.is_absolute():
        p = p.resolve()

    if p.is_dir():
        return (str(p), "directory")
    suffix = p.suffix.lower()
    if suffix == ".md":
        return (str(p), "markdown")
    if suffix in {".html", ".htm"}:
        return (str(p), "html")
    return (str(p), "?")


def list_instances() -> list[Instance]:
    """Discover running serve instances. Excludes the current process."""
    self_pid = os.getpid()
    instances: list[Instance] = []
    for pid, cmdline in _ps_processes():
        if pid == self_pid:
            continue
        argv = cmdline.split()
        if not _is_serve_invocation(argv):
            continue
        # Skip subcommand invocations (serve list, serve kill, serve comments…).
        # Only the long-running document server has a listening port, but we
        # filter explicitly so transient subcommands don't briefly appear.
        rest_start = 3 if argv[1] == "-m" else 2
        rest = argv[rest_start:]
        if rest and rest[0] in {"list", "kill", "comments", "resolve", "agent-init", "ls"}:
            continue
        positional = _parse_serve_argv(argv)
        path, mode = _resolve_served_path(positional, _process_cwd(pid))
        port = _listening_port(pid)
        instances.append(
            Instance(
                pid=pid,
                port=port,
                path=path,
                mode=mode,
                started=_start_time(pid),
                cmdline=cmdline,
            )
        )
    instances.sort(key=lambda i: (i.port is None, i.port or 0, i.pid))
    return instances


def kill_instance(pid: int, force: bool = False) -> None:
    """Send SIGTERM (or SIGKILL with force=True) to `pid`.

    Raises ProcessLookupError if the process is gone, PermissionError if not
    permitted. Callers should translate these into user-facing errors.
    """
    sig = signal.SIGKILL if force else signal.SIGTERM
    os.kill(pid, sig)
