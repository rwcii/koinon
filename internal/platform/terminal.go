package platform

import "golang.org/x/sys/unix"

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, termiosRequest)
	return err == nil
}

// NoEcho turns off echo on the terminal fd, keeping line input and signals, and returns the
// function that restores the earlier settings.
func NoEcho(fd int) (func() error, error) {
	old, err := unix.IoctlGetTermios(fd, termiosRequest)
	if err != nil {
		return nil, err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	quiet.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, termiosSet, &quiet); err != nil {
		return nil, err
	}
	return func() error { return unix.IoctlSetTermios(fd, termiosSet, old) }, nil
}

// Echoes reports whether the terminal fd echoes input.
func Echoes(fd int) (bool, error) {
	t, err := unix.IoctlGetTermios(fd, termiosRequest)
	if err != nil {
		return false, err
	}
	return t.Lflag&unix.ECHO != 0, nil
}
