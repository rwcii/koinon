package platform

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// Readable reports whether fd has data to read, or its writer has closed, within timeout.
// A zero timeout only looks; reading nothing, it leaves every byte for the next reader.
func Readable(fd int, timeout time.Duration) (bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && n > 0, err
	}
}
