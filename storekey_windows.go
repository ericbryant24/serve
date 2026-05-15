//go:build windows

package main

import (
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
)

func inodeStoreKey(path string) string {
	abs, _ := filepath.Abs(path)
	h := md5.Sum([]byte(abs))
	return hex.EncodeToString(h[:4])
}
