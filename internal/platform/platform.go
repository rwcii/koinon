// Package platform holds the operating-system boundary for the Go runtime.
package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrProcessGone reports that no process has the asked process ID.
var ErrProcessGone = errors.New("no such process")

// ProcessGroup is a process's group, its controlling terminal's device number (0 for none)
// and that terminal's foreground process group: the group that receives the terminal's input.
type ProcessGroup struct {
	Group, Terminal, Foreground int
}

// DefaultStateDir is separate from every Python-era state file.
func DefaultStateDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	return filepath.Join(base, "koinon", "go"), nil
}

// PrivateDir creates a private root and refuses unsafe existing roots. Filesystem
// configuration may use macOS's /tmp alias; peer addresses never pass through this.
func PrivateDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("state directory must be absolute")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if err := owned(info, true); err != nil {
		return "", err
	}
	return canonical, nil
}

func owned(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("unsafe state ownership or permissions")
	}
	if directory {
		if !info.IsDir() {
			return errors.New("state root is not a directory")
		}
	} else if !info.Mode().IsRegular() || stat.Nlink != 1 || info.Mode().Perm() != 0600 {
		return errors.New("state file must be a regular file with one link")
	}
	return nil
}

// OpenPrivate does not follow a final symlink and verifies the opened inode.
// It never fixes permissions of existing files.
func OpenPrivate(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil {
		err = owned(info, false)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Lock holds a kernel lock for the daemon's lifetime. Death releases it even if
// the lock file remains. Never unlink it: all contenders must lock the same inode.
func Lock(path string) (*os.File, error) {
	f, err := OpenPrivate(path, syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("daemon already running or lock unavailable: %w", err)
	}
	return f, nil
}

// LockHeld reports whether err is Lock's refusal because another process holds the lock.
func LockHeld(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}

func Unlock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return errors.Join(err, f.Close())
}

func SyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// OpenOwned opens a regular file owned by this user for reading, without following a final
// symlink. Unlike OpenPrivate it accepts any permission bits, for files that another
// program of this user writes, such as an agent's own session records.
func OpenOwned(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) {
			err = errors.New("not a regular file owned by this user")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// FileIdentity returns an open file's device, inode and size, so that a reader notices a
// replaced or shortened file.
func FileIdentity(f *os.File) (device, inode uint64, size int64, err error) {
	info, err := f.Stat()
	if err != nil {
		return 0, 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, errors.New("no file identity")
	}
	return uint64(stat.Dev), uint64(stat.Ino), info.Size(), nil
}

// TmuxSocketDir is the directory of this user's tmux server sockets, as tmux itself
// chooses it on Linux and macOS: $TMUX_TMPDIR/tmux-<uid>, else /tmp/tmux-<uid>. The path is
// kept unresolved, as tmux and its clients name it.
func TmuxSocketDir(getenv func(string) string) string {
	base := getenv("TMUX_TMPDIR")
	if base == "" || !filepath.IsAbs(base) {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// TrustedExecutable resolves path, the absolute executable that an agent's own terminal runs,
// such as its tmux (#418), for this process to run instead of a name looked up on its own
// PATH. Every symbolic link is resolved, and the target must be a regular file with an
// execute bit, owned by root or this process's user, and not writable by its group or by
// others. It reads no shell configuration and guesses no installation directory.
func TrustedExecutable(path string) (string, error) {
	return trustedExecutable(path, os.Geteuid())
}

func trustedExecutable(path string, uid int) (string, error) {
	if !CleanAbsolutePath(path) {
		return "", errors.New("executable path is not absolute and clean")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	// Lstat: a link put in place of the resolved file is refused, not followed.
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !ok || !info.Mode().IsRegular():
		return "", errors.New("executable is not a regular file")
	case info.Mode().Perm()&0111 == 0:
		return "", errors.New("executable has no execute bit")
	case info.Mode().Perm()&0022 != 0:
		return "", errors.New("executable is writable by its group or others")
	case stat.Uid != 0 && stat.Uid != uint32(uid):
		return "", errors.New("executable is not owned by root or this user")
	}
	return resolved, nil
}

// CleanAbsolutePath reports whether path is absolute, clean, at most 4096 bytes and free of
// control characters.
func CleanAbsolutePath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
