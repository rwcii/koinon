package platform

import "golang.org/x/sys/unix"

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, termiosRequest)
	return err == nil
}
