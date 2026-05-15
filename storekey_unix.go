//go:build !windows

package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// inodeStoreKey returns a stable hex key derived from the file's inode+device.
// Uses MD5 of the "dev-ino" string so the result is a clean 8-char hex string
// with no dashes — matching the frontmatter comment-id format.
func inodeStoreKey(path string) string {
	abs, _ := filepath.Abs(path)
	if fi, err := os.Stat(abs); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			raw := fmt.Sprintf("%x-%x", uint64(st.Dev), st.Ino)
			h := md5.Sum([]byte(raw))
			return hex.EncodeToString(h[:4])
		}
	}
	h := md5.Sum([]byte(abs))
	return hex.EncodeToString(h[:4])
}
