package library

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// ReadRoot is bounded, read-only filesystem access. Tests can inject observations and
// assert that an escaping target or a directory conflict performs no foreign read.
type ReadRoot interface {
	Lstat(string) (fs.FileInfo, error)
	Stat(string) (fs.FileInfo, error)
	Readlink(string) (string, error)
	ReadRegular(string, int) ([]byte, error)
	Close() error
}

type diskRoot struct{ *os.Root }

// OpenReadRoot uses Go's rooted filesystem operations, which keep symlink resolution
// inside the selected tree on Linux/macOS, including concurrent parent changes.
func OpenReadRoot(name string) (ReadRoot, error) {
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return diskRoot{root}, nil
}

var errNotRegular = errors.New("inspection target is not a regular file")

func (r diskRoot) ReadRegular(name string, limit int) ([]byte, error) {
	if limit < 0 || limit > MaxItemBytes {
		return nil, ErrInvalidQuery
	}
	// No-follow prevents a swapped leaf from becoming a symlink. Nonblocking lets
	// fstat reject a swapped FIFO/device without waiting for input from it.
	f, err := r.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errNotRegular
	}
	if info.Size() > int64(limit) {
		return nil, ErrInvalidQuery
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, ErrInvalidQuery
	}
	return data, nil
}
