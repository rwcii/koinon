package platform

import "golang.org/x/sys/unix"

const (
	termiosRequest = unix.TIOCGETA
	termiosSet     = unix.TIOCSETA
)

// FlushInput discards the terminal input that fd holds and no reader has taken.
func FlushInput(fd int) error { return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH) }
