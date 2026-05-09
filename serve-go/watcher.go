package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watch watches a single file (and related assets in markdown mode) for
// changes, calling onChange whenever a relevant change is detected.
func watch(filePath string, onChange func(), markdownMode bool) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	dir := filepath.Dir(filePath)
	absTarget, _ := filepath.Abs(filePath)

	if err := watcher.Add(dir); err != nil {
		return err
	}

	assetExts := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".svg": true, ".webp": true, ".css": true, ".js": true,
	}

	debounce := newDebouncer(300*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			absPath, _ := filepath.Abs(event.Name)
			if absPath == absTarget {
				debounce.Trigger()
				continue
			}
			if markdownMode {
				ext := strings.ToLower(filepath.Ext(event.Name))
				if assetExts[ext] {
					debounce.Trigger()
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			_ = err
		}
	}
}

// watchDirectory watches an entire directory tree for changes, calling
// onChange whenever any non-hidden file changes.
func watchDirectory(dir string, onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Add root and all subdirectories
	absRoot, _ := filepath.Abs(dir)
	_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") {
				return filepath.SkipDir
			}
			_ = watcher.Add(path)
		}
		return nil
	})

	debounce := newDebouncer(300*time.Millisecond, onChange)
	defer debounce.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			// Skip hidden files
			name := filepath.Base(event.Name)
			if strings.HasPrefix(name, ".") {
				continue
			}
			// If a new directory was created, watch it too
			if event.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					_ = watcher.Add(event.Name)
				}
			}
			debounce.Trigger()
		case _, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// watchComments watches ~/.serve/comments/ for changes.
func watchComments(onChange func()) error {
	commentsDir := filepath.Join(homeDir(), ".serve", "comments")
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

	debounce := newDebouncer(200*time.Millisecond, onChange)
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
	delay   time.Duration
	fn      func()
	timer   *time.Timer
	trigCh  chan struct{}
	stopCh  chan struct{}
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
