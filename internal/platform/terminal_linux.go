package platform

import "golang.org/x/sys/unix"

const (
	termiosRequest = unix.TCGETS
	termiosSet     = unix.TCSETS
)

// FlushInput discards the terminal input that fd holds and no reader has taken.
func FlushInput(fd int) error { return unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
