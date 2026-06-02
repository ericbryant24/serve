package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ---------------------------------------------------------------------------
// serve watch — emit JSONL events for comment-store changes
// ---------------------------------------------------------------------------

func cmdWatch(args []string) {
	file, newOnly, err := parseWatchArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	out := bufio.NewWriter(os.Stdout)
	emitter := &eventEmitter{w: out, newOnly: newOnly}

	if file != "" {
		runWatchSingle(ctx, file, emitter)
	} else {
		runWatchAll(ctx, emitter)
	}
}

func parseWatchArgs(args []string) (file string, newOnly bool, err error) {
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			fmt.Print(`Usage: serve watch [file] [--new]

Emit one JSON event per line whenever a comment changes.

Arguments:
  file        Watch only this file's comments. Omit to watch all files.

Flags:
  --new       Only emit new_comment and new_reply events.

Event types:
  initial, new_comment, new_reply, edited, resolved, unresolved, deleted
`)
			os.Exit(0)
		case a == "--new":
			newOnly = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag: %s", a)
		default:
			if file != "" {
				return "", false, fmt.Errorf("multiple file arguments")
			}
			file = a
		}
	}
	if file != "" {
		resolved, rerr := resolveFile(file)
		if rerr != nil {
			return "", false, rerr
		}
		file = resolved
	}
	return file, newOnly, nil
}

// ---------------------------------------------------------------------------
// Event emitter
// ---------------------------------------------------------------------------

type eventEmitter struct {
	w       *bufio.Writer
	newOnly bool
	mu      sync.Mutex
}

func (e *eventEmitter) emit(ev map[string]any) {
	if e.newOnly {
		t, _ := ev["event"].(string)
		if t != "new_comment" && t != "new_reply" {
			return
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := json.Marshal(ev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal error:", err)
		return
	}
	e.w.Write(data)
	e.w.WriteByte('\n')
	e.w.Flush()
}

// ---------------------------------------------------------------------------
// Single-file watcher
// ---------------------------------------------------------------------------

func runWatchSingle(ctx context.Context, filePath string, emitter *eventEmitter) {
	dir := commentStoreDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	_, initial, err := LoadCommentsByKey(storeKeyForFile(filePath), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading comments:", err)
		os.Exit(1)
	}
	snapshot := initial
	emitInitials(emitter, filePath, snapshot)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	defer watcher.Close()
	if err := watcher.Add(dir); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	onChange := func() {
		// Re-resolve the key each tick. If the source file was atomically
		// rewritten (write-temp + rename), its inode — and thus the store
		// key — has drifted. A frozen key would watch a ghost.
		_, next, err := LoadCommentsByKey(storeKeyForFile(filePath), dir)
		if err != nil {
			return
		}
		for _, ev := range diffSnapshots(snapshot, next, filePath) {
			emitter.emit(ev)
		}
		snapshot = next
	}
	debounce := newDebouncer(50*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}
			// Trigger on any .json event in the dir — the key may have
			// drifted, so we can't filter by the originally-resolved name.
			if strings.HasSuffix(event.Name, ".json") {
				debounce.Trigger()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// All-files watcher
// ---------------------------------------------------------------------------

func runWatchAll(ctx context.Context, emitter *eventEmitter) {
	dir := commentStoreDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	type snap struct {
		path     string
		comments []Comment
	}
	snapshots := map[string]snap{}

	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		key := strings.TrimSuffix(name, ".json")
		path, comments, err := LoadCommentsByKey(key, dir)
		if err != nil {
			continue
		}
		snapshots[key] = snap{path: path, comments: comments}
		emitInitials(emitter, path, comments)
	}

	rescan := func() {
		entries, _ := os.ReadDir(dir)
		seen := map[string]bool{}
		for _, ent := range entries {
			name := ent.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			key := strings.TrimSuffix(name, ".json")
			seen[key] = true
			path, next, err := LoadCommentsByKey(key, dir)
			if err != nil {
				continue
			}
			prev := snapshots[key]
			// Prefer the freshly persisted path; fall back to whatever we knew.
			effectivePath := path
			if effectivePath == "" {
				effectivePath = prev.path
			}
			for _, ev := range diffSnapshots(prev.comments, next, effectivePath) {
				emitter.emit(ev)
			}
			snapshots[key] = snap{path: effectivePath, comments: next}
		}
		for key, prev := range snapshots {
			if seen[key] {
				continue
			}
			for _, ev := range diffSnapshots(prev.comments, nil, prev.path) {
				emitter.emit(ev)
			}
			delete(snapshots, key)
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	defer watcher.Close()
	if err := watcher.Add(dir); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	debounce := newDebouncer(100*time.Millisecond, rescan)
	defer debounce.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}
			if strings.HasSuffix(event.Name, ".json") {
				debounce.Trigger()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Event computation
// ---------------------------------------------------------------------------

func emitInitials(emitter *eventEmitter, file string, comments []Comment) {
	if emitter.newOnly {
		return
	}
	for _, c := range comments {
		if c.Resolved {
			continue
		}
		emitter.emit(initialEvent(file, c))
	}
}

func initialEvent(file string, c Comment) map[string]any {
	ev := map[string]any{
		"event":             "initial",
		"file":              file,
		"comment_id":        c.ID,
		"timestamp":         time.Now().UTC().Format(time.RFC3339),
		"anchor_text":       c.AnchorText,
		"text":              c.Text,
		"source_line_start": c.SourceLineStart,
		"source_line_end":   c.SourceLineEnd,
		"parent_id":         c.ParentID,
	}
	return ev
}

// diffSnapshots computes the events that explain the transition from prev to
// next. It is pure: no I/O, no time-based behavior beyond the emitted
// timestamps. Tests can call it directly.
func diffSnapshots(prev, next []Comment, file string) []map[string]any {
	prevByID := map[string]Comment{}
	for _, c := range prev {
		prevByID[c.ID] = c
	}
	nextByID := map[string]Comment{}
	for _, c := range next {
		nextByID[c.ID] = c
	}
	ts := time.Now().UTC().Format(time.RFC3339)

	var events []map[string]any

	// Created: in next but not in prev.
	for _, c := range next {
		if _, existed := prevByID[c.ID]; existed {
			continue
		}
		eventType := "new_comment"
		if c.ParentID != nil {
			eventType = "new_reply"
		}
		events = append(events, map[string]any{
			"event":             eventType,
			"file":              file,
			"comment_id":        c.ID,
			"timestamp":         ts,
			"anchor_text":       c.AnchorText,
			"text":              c.Text,
			"source_line_start": c.SourceLineStart,
			"source_line_end":   c.SourceLineEnd,
			"parent_id":         c.ParentID,
		})
	}

	// Edited / resolved / unresolved: in both, fields differ.
	for _, n := range next {
		p, existed := prevByID[n.ID]
		if !existed {
			continue
		}
		if n.Text != p.Text {
			events = append(events, map[string]any{
				"event":      "edited",
				"file":       file,
				"comment_id": n.ID,
				"timestamp":  ts,
				"text":       n.Text,
			})
		}
		if n.Resolved != p.Resolved {
			eventType := "resolved"
			if !n.Resolved {
				eventType = "unresolved"
			}
			events = append(events, map[string]any{
				"event":      eventType,
				"file":       file,
				"comment_id": n.ID,
				"timestamp":  ts,
			})
		}
	}

	// Deleted: in prev but not in next.
	for _, c := range prev {
		if _, stillThere := nextByID[c.ID]; stillThere {
			continue
		}
		events = append(events, map[string]any{
			"event":      "deleted",
			"file":       file,
			"comment_id": c.ID,
			"timestamp":  ts,
		})
	}

	return events
}
