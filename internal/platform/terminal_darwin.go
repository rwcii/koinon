package platform

import "golang.org/x/sys/unix"

const (
	termiosRequest = unix.TIOCGETA
	termiosSet     = unix.TIOCSETA
)
