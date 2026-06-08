//go:build !darwin && !windows

package main

import (
	"os"
	"time"
)

// fileBirthTime has no portable implementation on Linux/BSD (creation time is
// not exposed through the standard stat fields), so only the modified time is
// shown on those platforms.
func fileBirthTime(fi os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
