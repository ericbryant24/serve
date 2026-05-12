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

func storeKeyForFile(path string) string {
	abs, _ := filepath.Abs(path)
	if fi, err := os.Stat(abs); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			return fmt.Sprintf("%x-%x", uint64(st.Dev), st.Ino)
		}
	}
	h := md5.Sum([]byte(abs))
	return hex.EncodeToString(h[:4])
}
