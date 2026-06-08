//go:build darwin

package main

import (
	"os"
	"syscall"
	"time"
)

// fileBirthTime returns the file's creation (birth) time on macOS.
func fileBirthTime(fi os.FileInfo) (time.Time, bool) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
	}
	return time.Time{}, false
}
