//go:build !windows

package paths

import (
	"os"
	"syscall"
)

// FileID returns the device and inode of a file, which survive a rename.
func FileID(path string) (dev, ino uint64, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), true
}
