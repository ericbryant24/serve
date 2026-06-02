# Bug: comment store orphans when an external editor atomic-writes the file

## Symptom

A running `serve` HTTP server keeps writing browser-submitted comments into one comment-store file, while every CLI tool (`serve comments`, `serve watch`) reads from a different one. The two never agree, so:

- `serve comments <file>` reports zero comments, even while the browser shows several.
- `serve watch <file>` sits silent forever despite new comments arriving.
- The CLI-reported `doc_id` doesn't match the doc_id under which comments are actually being stored.

The split persists until the server is restarted. From the user's perspective, comments stop being readable to anything except the browser session that created them.

## Repro

1. `serve -p 8000 --no-open <dir>` against a directory.
2. Open a markdown file in the browser, add a comment. Note the doc_id in the rendered page (e.g. `4cc4c4e6`).
3. From outside, edit the file with a tool that uses atomic write (write-temp + `rename(2)` over the original). VS Code, vim with `:set backupcopy=auto`, Claude Code's `Edit`/`Write` tools, most modern editors.
4. Run `serve comments <file>`. The reported `doc_id` is now different (e.g. `75fe8cac`) and `comments` is empty.
5. Browser still shows the original comment (rendered against the cached server state).
6. Add another comment via the browser. It lands in `4cc4c4e6.json`. `serve comments` still reports `75fe8cac` and empty.

## Root cause

Two pieces of code together produce the divergence.

**1. The store key is inode-derived (`storekey_unix.go:17-28`).**

```go
func inodeStoreKey(path string) string {
    abs, _ := filepath.Abs(path)
    if fi, err := os.Stat(abs); err == nil {
        if st, ok := fi.Sys().(*syscall.Stat_t); ok {
            raw := fmt.Sprintf("%x-%x", uint64(st.Dev), st.Ino)
            h := md5.Sum([]byte(raw))
            return hex.EncodeToString(h[:4])
        }
    }
    ...
}
```

The key is a hash of dev+inode. Comment: "so comments survive `mv`/`git mv`." That's true for plain renames *within* a filesystem (rename preserves inode). It is **not** true for the atomic-write idiom most editors use: write contents to a temp file, then `rename(tempfile, original)`. The rename replaces the original path with the temp's inode — a **new** inode. The store key changes on the next `os.Stat` even though the path is unchanged.

**2. The HTTP server caches `CommentStore` per file path forever (`server.go:259-268`).**

```go
func (s *Server) getStoreForFile(fp string) (*CommentStore, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    if store, ok := s.commentStores[fp]; ok {
        return store, nil
    }
    store := NewCommentStoreForFile(fp, commentStoreDir())
    s.commentStores[fp] = store
    return store, nil
}
```

The cache key is the file path. The cached value carries the *original* storeKey from when the path was first served. Nothing invalidates the cache when the file's inode changes. The cache also persists for the life of the server process — there's no TTL, no refresh on fsnotify events, no re-stat on each request.

CLI tools (`serve comments`, `serve watch`) have no such cache — they call `NewCommentStoreForFile` per invocation, which calls `os.Stat`, which sees the new inode. So they compute a *fresh* storeKey, every time.

After an external atomic write:
- Server: routes browser POST through cached store → writes to **old** storeKey's file.
- CLI: re-stats file → reads from **new** storeKey's file (which usually doesn't exist).

The comment file with all the actual data is now orphaned from the perspective of every tool except the running server process.

## Why the assumption held until it didn't

The CLAUDE.md says inode-keying was chosen "so comments survive `mv`/`git mv`." That use case is real. But the design assumed the path-to-inode mapping is stable across edits, which is only true for in-place editors (vim with `backupcopy=yes`, anything that opens the file with `O_WRONLY|O_TRUNC` and writes through the existing inode). Atomic-write is the more common modern pattern — used by VS Code, JetBrains IDEs, Sublime, Emacs (default), Claude Code's Edit/Write tools, and any tool that wraps `os.WriteFile` on a path that already exists (Go's stdlib uses `O_TRUNC` so it's actually safe; but many language-native equivalents in Python/Node use rename).

The browser-vs-CLI mismatch was invisible during testing because the test suite likely exercises the CLI and the HTTP server in separate runs, not in the wild combination of "long-running server + external editor + CLI watcher."

## Fix

Two changes, both small. Either alone helps; both together close the gap.

### 1. Don't cache the storeKey across requests on the server.

`getStoreForFile` is keyed on file path but the cached value embeds an immutable storeKey. The simplest fix is to drop the cache and always construct a fresh `CommentStore` per request. `CommentStore` is cheap to create (an MD5 of a short string plus a path concatenation) and it doesn't hold any expensive state — the actual load happens on `.List()` / `.Add()` etc., which already re-reads from disk.

```go
func (s *Server) getStoreForFile(fp string) (*CommentStore, error) {
    return NewCommentStoreForFile(fp, commentStoreDir()), nil
}
```

After this, browser writes always land in the *current* storeKey's file, matching what CLI tools see. Existing orphaned comment files would still exist, but new writes converge.

If the per-request `os.Stat` cost is a concern in profiling later, add an LRU with mtime-or-inode-based invalidation. Don't pre-optimize before measuring.

### 2. Migrate comments when the inode changes.

Even with (1), comments written *before* the editor rewrite are stranded in the old storeKey file. To recover them, lean on the `path` field that's already persisted in `commentsFile { Path, Comments }` (`comments.go:56-59`). On any read where the current storeKey has no file, scan `~/.serve/comments/*.json` for any entry whose `Path` field matches the current file's absolute path. If exactly one matches:

- Read it.
- Rename it to the current storeKey's filename (or write its contents through the new store and delete the old file).
- Continue serving.

This makes the inode-based key self-healing across atomic writes. The "comments survive `mv`" property is preserved because the `mv` case keeps the same inode (no rename triggers the migration path); the new "comments survive atomic-write rewrites of the same path" property is added on top.

A short sketch in `CommentStore.load`:

```go
func (s *CommentStore) load() ([]Comment, string, error) {
    data, err := os.ReadFile(s.path)
    if os.IsNotExist(err) && s.sourcePath != "" {
        if migrated, ok := s.tryMigrateFromPath(); ok {
            return migrated.Comments, migrated.Path, nil
        }
        return nil, "", nil
    }
    ...
}

func (s *CommentStore) tryMigrateFromPath() (commentsFile, bool) {
    abs, _ := filepath.Abs(s.sourcePath)
    matches, _ := filepath.Glob(filepath.Join(filepath.Dir(s.path), "*.json"))
    for _, m := range matches {
        if m == s.path { continue }
        data, err := os.ReadFile(m)
        if err != nil { continue }
        var wrapped commentsFile
        if err := json.Unmarshal(data, &wrapped); err != nil { continue }
        if wrapped.Path == "" { continue }
        if absMatch, _ := filepath.Abs(wrapped.Path); absMatch == abs {
            // Migrate
            if err := os.Rename(m, s.path); err == nil {
                return wrapped, true
            }
        }
    }
    return commentsFile{}, false
}
```

Edge cases worth handling:
- Multiple matches (shouldn't happen but worth being defensive): take the most-recently-modified, leave the others alone, log a warning.
- Legacy bare-array stores have no `Path` field — they can't participate in migration. That's acceptable since they predate the path-tagged format.
- Race against a concurrent `Add` from the browser side. The current `sync.Mutex` only guards one store instance; migration would need a flock-or-similar across instances. For now, accept the rare race (worst case: a comment is written to the old file just before migration, and gets lost). Document the limitation.

### 3. (Optional) Surface the divergence so it can't silently swallow events.

`serve watch [file]` could log a warning at startup if there's a comment file whose `Path` matches the requested file but whose storeKey differs from the file's current storeKey. That's a hint to the user that something rewrote the file under serve's nose, and the migration path will repair it on next access.

## What this also implies for `serve watch`

`serve watch <file>` re-computes the storeKey on each iteration (it just calls `LoadCommentsByKey` against `storeKeyForFile(file)` — see `watch.go`). After change (2), the watcher would migrate orphaned comments on first read, so the all-files watch loop in `serve watch` (no file arg) would naturally pick up post-migration files. Until then, a hot-fix workaround is to add `--migrate` or run `serve watch` once to trigger lazy migration on a known file.

## Related: why my first instinct was wrong

I initially diagnosed this as a stale browser tab — "your tab is locked to an old doc_id, just refresh." That was wrong. Refreshing made no difference because the *server* is the one holding the stale state. The browser is faithfully sending the right path to the right URL; the server keeps interpreting that path against a frozen inode snapshot.

A reload of just the browser doesn't touch the server's cache. The only way to get the server to recompute the storeKey is to restart it (which throws away the cache and forces a fresh `os.Stat`). That's a hint at the cache-invalidation problem the fix needs to solve permanently.
