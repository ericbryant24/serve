package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ---------------------------------------------------------------------------
// serve wait — block until the next comment-store change, print it, exit.
//
// Unlike `serve watch` (a long-lived stream), `wait` returns as soon as one
// qualifying event arrives. This makes it usable from a harness that only
// re-invokes an agent when a background command *exits*: park `serve wait` in
// the background, get woken on the next comment, read full state with
// `serve comments`, then re-arm.
//
// Exit codes:
//
//	0    a qualifying event was emitted
//	124  --timeout elapsed with no event (matches coreutils `timeout`)
//	130  interrupted by SIGINT/SIGTERM
//	1    usage or runtime error
//
// ---------------------------------------------------------------------------

func cmdWait(args []string) {
	file, newOnly, timeout, err := parseWaitArgs(args)
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

	if timeout > 0 {
		var tcancel context.CancelFunc
		ctx, tcancel = context.WithTimeout(ctx, timeout)
		defer tcancel()
	}

	if runWaitSingle(ctx, file, newOnly) {
		os.Exit(0)
	}
	if ctx.Err() == context.DeadlineExceeded {
		os.Exit(124)
	}
	os.Exit(130)
}

func parseWaitArgs(args []string) (file string, newOnly bool, timeout time.Duration, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Print(`Usage: serve wait <file> [--new] [--timeout N]

Block until the next comment-store change for <file>, print that one event
as JSON, and exit. Designed for agents: run it in the background and you are
woken (by its exit) the moment a comment arrives — then read the full state
with 'serve comments <file>' and re-arm another 'serve wait'.

Flags:
  --new          Only wake on new_comment / new_reply (ignore edits/resolves).
  --timeout N     Give up after N seconds of silence and exit 124.

Exit codes:
  0    an event was emitted
  124  timed out with no event
  130  interrupted (SIGINT/SIGTERM)

Event types (same shape as 'serve watch'):
  new_comment, new_reply, edited, resolved, unresolved, deleted
`)
			os.Exit(0)
		case a == "--new":
			newOnly = true
		case a == "--timeout":
			if i+1 >= len(args) {
				return "", false, 0, fmt.Errorf("--timeout requires a number of seconds")
			}
			n, e := strconv.Atoi(args[i+1])
			if e != nil || n < 0 {
				return "", false, 0, fmt.Errorf("invalid --timeout: %s", args[i+1])
			}
			timeout = time.Duration(n) * time.Second
			i++
		case strings.HasPrefix(a, "--timeout="):
			n, e := strconv.Atoi(strings.TrimPrefix(a, "--timeout="))
			if e != nil || n < 0 {
				return "", false, 0, fmt.Errorf("invalid --timeout: %s", a)
			}
			timeout = time.Duration(n) * time.Second
		case strings.HasPrefix(a, "-"):
			return "", false, 0, fmt.Errorf("unknown flag: %s", a)
		default:
			if file != "" {
				return "", false, 0, fmt.Errorf("multiple file arguments")
			}
			file = a
		}
	}
	if file == "" {
		return "", false, 0, fmt.Errorf("file argument required")
	}
	resolved, rerr := resolveFile(file)
	if rerr != nil {
		return "", false, 0, rerr
	}
	return resolved, newOnly, timeout, nil
}

// runWaitSingle blocks until the first qualifying change to filePath's comment
// store (or ctx is cancelled). It prints that single event and returns true;
// on cancellation it returns false. It never replays existing ("initial")
// comments — wait is strictly forward-looking.
func runWaitSingle(ctx context.Context, filePath string, newOnly bool) bool {
	dir := commentStoreDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	_, snapshot, err := LoadCommentsByKey(storeKeyForFile(filePath), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading comments:", err)
		os.Exit(1)
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

	out := bufio.NewWriter(os.Stdout)
	done := make(chan struct{})
	var once sync.Once

	onChange := func() {
		// Re-resolve the key each tick: an atomic rewrite of the source file
		// (write-temp + rename) drifts its inode and thus the store key.
		_, next, err := LoadCommentsByKey(storeKeyForFile(filePath), dir)
		if err != nil {
			return
		}
		events := diffSnapshots(snapshot, next, filePath)
		snapshot = next
		for _, ev := range events {
			if waitQualifies(ev, newOnly) {
				if data, err := json.Marshal(ev); err == nil {
					out.Write(data)
					out.WriteByte('\n')
					out.Flush()
				}
				once.Do(func() { close(done) })
				return
			}
		}
	}
	debounce := newDebouncer(50*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case <-done:
			return true
		case <-ctx.Done():
			return false
		case event, ok := <-watcher.Events:
			if !ok {
				return false
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}
			if strings.HasSuffix(event.Name, ".json") {
				debounce.Trigger()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return false
			}
		}
	}
}

// waitQualifies reports whether a diff event should wake `serve wait`. The
// "initial" replay is never a wake (wait is forward-looking); with --new only
// fresh comments/replies count; otherwise any real change does.
func waitQualifies(ev map[string]any, newOnly bool) bool {
	t, _ := ev["event"].(string)
	if t == "initial" {
		return false
	}
	if newOnly {
		return t == "new_comment" || t == "new_reply"
	}
	return true
}
