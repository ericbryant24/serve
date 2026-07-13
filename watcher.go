package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// noisyDirs are directory names that generate high-frequency writes not worth
// watching — build artifacts, package caches, compiled bytecode.
var noisyDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	"vendor":       true,
	"target":       true,
	".next":        true,
	".nuxt":        true,
	".output":      true,
}

// isIgnoredPath returns true if any path component is hidden or a noisy dir.
// The watcher is intentionally stricter than the sidebar: dotdirs are skipped
// here to avoid recursing into tooling caches (.venv, .idea, .pytest_cache,
// .cursor, .claude, ...) that would overflow fsnotify limits and trigger
// spurious reloads. The sidebar still lists dotfiles so users can view/edit
// .serveignore and similar.
func isIgnoredPath(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") || noisyDirs[part] {
			return true
		}
	}
	return false
}

// watchDirectory watches an entire directory tree for changes, calling
// onChange whenever any non-hidden, non-noisy file changes. It returns when
// stop is closed, so the server can restart it on a different root ("go up").
func watchDirectory(dir string, stop <-chan struct{}, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	absRoot, _ := filepath.Abs(dir)
	_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "watcher: walk error at %s: %v\n", path, err)
			return nil
		}
		if info.IsDir() {
			if isIgnoredPath(path, absRoot) {
				return filepath.SkipDir
			}
			if err := watcher.Add(path); err != nil {
				fmt.Fprintf(os.Stderr, "watcher: failed to watch %s: %v\n", path, err)
			}
		}
		return nil
	})

	debounce := newDebouncer(50*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case <-stop:
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if isIgnoredPath(event.Name, absRoot) {
				continue
			}
			// Watch newly created subdirectories (unless ignored)
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = watcher.Add(event.Name)
				}
			}
			debounce.Trigger()
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "watcher error: %v\n", err)
		}
	}
}

// watchComments watches ~/.serve/comments/ for changes.
func watchComments(onChange func()) error {
	commentsDir := commentStoreDir()
	if err := os.MkdirAll(commentsDir, 0755); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(commentsDir); err != nil {
		return err
	}

	debounce := newDebouncer(300*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) == 0 {
				continue
			}
			if strings.HasSuffix(event.Name, ".json") {
				debounce.Trigger()
			}
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Debouncer
// ---------------------------------------------------------------------------

type debouncer struct {
	delay  time.Duration
	fn     func()
	trigCh chan struct{}
	stopCh chan struct{}
}

func newDebouncer(delay time.Duration, fn func()) *debouncer {
	d := &debouncer{
		delay:  delay,
		fn:     fn,
		trigCh: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *debouncer) Trigger() {
	select {
	case d.trigCh <- struct{}{}:
	default:
	}
}

func (d *debouncer) Stop() {
	close(d.stopCh)
}

func (d *debouncer) run() {
	var timer *time.Timer
	for {
		select {
		case <-d.stopCh:
			if timer != nil {
				timer.Stop()
			}
			return
		case <-d.trigCh:
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(d.delay, d.fn)
		}
	}
}
