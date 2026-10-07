package platform

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// ClaudeSocketDirs are canonicalized only for allowlist comparisons. A messaging
// path itself always stays literal, including Darwin's /tmp alias, for peer keys.
func ClaudeSocketDirs() []string { return claudeSocketDirs() }
func ValidatePeerSocket(path string, allowed []string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) >= UnixPathBytes() || filepath.Ext(path) != ".sock" {
		return errors.New("invalid peer socket path")
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if err = owned(info, true); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	found := false
	for _, dir := range allowed {
		candidate, e := filepath.EvalSymlinks(dir)
		if e == nil && candidate == canonical {
			found = true
		}
	}
	if !found {
		return errors.New("unsupported peer socket directory")
	}
	if filepath.Base(path) == "control.sock" || len(filepath.Base(path)) >= 13 && filepath.Base(path)[len(filepath.Base(path))-13:] == "-control.sock" {
		return errors.New("control endpoint is not a peer socket")
	}
	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 || !sameUser(info) {
		return errors.New("unsafe peer socket")
	}
	return nil
}

// ConnectPeer validates both endpoint metadata and kernel identity. allowed is
// the published platform allowlist in production and a private receiver root in tests.
func ConnectPeer(path string, expectedPID int, allowed []string) (*net.UnixConn, error) {
	if err := ValidatePeerSocket(path, allowed); err != nil {
		return nil, err
	}
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	conn := c.(*net.UnixConn)
	pid, err := PeerPID(conn)
	if err != nil || expectedPID <= 0 || pid != expectedPID {
		conn.Close()
		return nil, errors.New("peer kernel identity mismatch")
	}
	return conn, nil
}

// ReplyListener binds only a newly created, owned private directory. It is held
// by the sender process, with a short literal path on both supported platforms.
func ReplyListener() (*net.UnixListener, string, func(), error) {
	dir, err := os.MkdirTemp("/tmp", "kwn-")
	if err != nil {
		return nil, "", nil, err
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, "", nil, err
	}
	cleanup := func() { listener.Close(); os.RemoveAll(dir) }
	return listener, path, cleanup, nil
}

// ReadCredential validates private metadata before reading any credential bytes.
func ReadCredential(path string, limit int64) ([]byte, error) {
	f, err := OpenOwned(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 || info.Size() > limit {
		return nil, errors.New("unsafe credential metadata")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, errors.New("credential too large")
	}
	return data, err
}
