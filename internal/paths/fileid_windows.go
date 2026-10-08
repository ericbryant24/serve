//go:build windows

package paths

// FileID is not available on Windows; documents are matched by path alone.
func FileID(path string) (dev, ino uint64, ok bool) { return 0, 0, false }
