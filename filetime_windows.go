//go:build windows

package main

import (
	"os"
	"syscall"
	"time"
)

// fileBirthTime returns the file's creation time on Windows.
func fileBirthTime(fi os.FileInfo) (time.Time, bool) {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, d.CreationTime.Nanoseconds()), true
	}
	return time.Time{}, false
}
