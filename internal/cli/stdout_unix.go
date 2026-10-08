//go:build !windows

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdoutGone reports whether whatever was reading stdout has gone away, so a
// stream with nothing new to say still exits when its reader does.
func stdoutGone() bool {
	fds := []unix.PollFd{{Fd: int32(os.Stdout.Fd()), Events: unix.POLLOUT}}
	n, err := unix.Poll(fds, 0)
	if err != nil || n == 0 {
		return false
	}
	return fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0
}
