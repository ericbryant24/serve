"""Comment data model, document ID management, and JSON persistence."""

import json
import re
import uuid
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path


@dataclass
class Comment:
    id: str
    text: str
    created_at: str
    resolved: bool = False
    anchor_text: str = ""
    block_text: str = ""
    source_line_start: int | None = None
    source_line_end: int | None = None
    parent_id: str | None = None


# ---------------------------------------------------------------------------
# Document ID management
# ---------------------------------------------------------------------------

def get_document_id(file_path: Path) -> str | None:
    """Read the comment-id from a document's frontmatter (md) or meta tag (html)."""
    content = file_path.read_text(encoding="utf-8")
    suffix = file_path.suffix.lower()

    if suffix == ".md":
        # Look for YAML frontmatter: ---\n...\n---
        m = re.match(r"^---\s*\n(.*?\n)---\s*\n", content, re.DOTALL)
        if m:
            for line in m.group(1).splitlines():
                if line.startswith("comment-id:"):
                    return line.split(":", 1)[1].strip()
        return None

    # HTML mode
    m = re.search(
        r'<meta\s+name=["\']comment-id["\']\s+content=["\']([^"\']+)["\']',
        content,
        re.IGNORECASE,
    )
    return m.group(1) if m else None


def set_document_id(file_path: Path, doc_id: str) -> None:
    """Write a comment-id into a document (frontmatter for md, meta tag for html)."""
    content = file_path.read_text(encoding="utf-8")
    suffix = file_path.suffix.lower()

    if suffix == ".md":
        fm_match = re.match(r"^---\s*\n(.*?\n)---\s*\n", content, re.DOTALL)
        if fm_match:
            # Append field to existing frontmatter
            fm_body = fm_match.group(1)
            new_fm = f"---\n{fm_body}comment-id: {doc_id}\n---\n"
            content = new_fm + content[fm_match.end():]
        else:
            # Create new frontmatter block
            content = f"---\ncomment-id: {doc_id}\n---\n\n{content}"
    else:
        # HTML: inject meta tag
        tag = f'<meta name="comment-id" content="{doc_id}">'
        if "<head>" in content:
            content = content.replace("<head>", f"<head>\n  {tag}", 1)
        elif "<HEAD>" in content:
            content = content.replace("<HEAD>", f"<HEAD>\n  {tag}", 1)
        elif "<!DOCTYPE" in content or "<!doctype" in content:
            # Insert after doctype line
            content = re.sub(
                r"(<!DOCTYPE[^>]*>)",
                rf"\1\n{tag}",
                content,
                count=1,
                flags=re.IGNORECASE,
            )
        else:
            content = f"{tag}\n{content}"

    file_path.write_text(content, encoding="utf-8")


def ensure_document_id(file_path: Path) -> str:
    """Return existing document ID or generate and write a new one."""
    doc_id = get_document_id(file_path)
    if doc_id:
        return doc_id
    doc_id = uuid.uuid4().hex[:8]
    set_document_id(file_path, doc_id)
    return doc_id


# ---------------------------------------------------------------------------
# Comment storage
# ---------------------------------------------------------------------------

_STORE_DIR = Path.home() / ".serve" / "comments"


class CommentStore:
    def __init__(self, doc_id: str) -> None:
        self.doc_id = doc_id
        self.dir = _STORE_DIR
        self.path = self.dir / f"{doc_id}.json"
        self._ensure_dir()

    def _ensure_dir(self) -> None:
        self.dir.mkdir(parents=True, exist_ok=True)

    def _load(self) -> list[dict]:
        if not self.path.exists():
            return []
        return json.loads(self.path.read_text())

    def _save(self, comments: list[dict]) -> None:
        self.path.write_text(json.dumps(comments, indent=2))

    def list_comments(self) -> list[Comment]:
        return [Comment(**c) for c in self._load()]

    def add_comment(
        self,
        text: str,
        anchor_text: str = "",
        block_text: str = "",
        source_line_start: int | None = None,
        source_line_end: int | None = None,
        parent_id: str | None = None,
    ) -> Comment:
        comment = Comment(
            id=str(uuid.uuid4()),
            text=text,
            created_at=datetime.now(timezone.utc).isoformat(),
            anchor_text=anchor_text,
            block_text=block_text,
            source_line_start=source_line_start,
            source_line_end=source_line_end,
            parent_id=parent_id,
        )
        comments = self._load()
        comments.append(asdict(comment))
        self._save(comments)
        return comment

    def update_comment(
        self, comment_id: str, text: str | None = None, resolved: bool | None = None
    ) -> Comment | None:
        comments = self._load()
        for c in comments:
            if c["id"] == comment_id:
                if text is not None:
                    c["text"] = text
                if resolved is not None:
                    c["resolved"] = resolved
                self._save(comments)
                return Comment(**c)
        return None

    def delete_comment(self, comment_id: str) -> bool:
        comments = self._load()
        filtered = [c for c in comments if c["id"] != comment_id and c.get("parent_id") != comment_id]
        if len(filtered) == len(comments):
            return False
        self._save(filtered)
        return True
