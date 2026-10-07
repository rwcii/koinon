package platform

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPeerSocketKernelIdentityAndPathFences(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kp-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	os.Chmod(socket, 0600)
	// Kernel credentials come from this process, never from the path or a registry claim.
	conn, err := ConnectPeer(socket, os.Getpid(), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if conn, err = ConnectPeer(socket, os.Getpid()+1, []string{dir}); err == nil {
		conn.Close()
		t.Fatal("accepted wrong kernel PID")
	}
	if err = ValidatePeerSocket(socket, []string{dir + "-other"}); err == nil {
		t.Fatal("accepted unlisted directory")
	}
	os.Chmod(socket, 0660)
	if err = ValidatePeerSocket(socket, []string{dir}); err == nil {
		t.Fatal("accepted public socket")
	}
	os.Chmod(socket, 0600)
	os.Chmod(dir, 0755)
	if err = ValidatePeerSocket(socket, []string{dir}); err == nil {
		t.Fatal("accepted public directory")
	}
	os.Chmod(dir, 0700)
	link := filepath.Join(dir, "link.sock")
	os.Symlink(socket, link)
	if err = ValidatePeerSocket(link, []string{dir}); err == nil {
		t.Fatal("accepted symlink endpoint")
	}
	for _, name := range []string{"control.sock", "peer-control.sock"} {
		if err = ValidatePeerSocket(filepath.Join(dir, name), []string{dir}); err == nil {
			t.Fatal("accepted control endpoint")
		}
	}
	atLimit := "/" + strings.Repeat("a", UnixPathBytes()-6) + ".sock"
	if len(atLimit) != UnixPathBytes() {
		t.Fatal("bad fixture")
	}
	if err = ValidatePeerSocket(atLimit, []string{dir}); err == nil {
		t.Fatal("accepted AF_UNIX length limit")
	}
	// The largest supported literal path can be connected without resolving it.
	longest := filepath.Join(dir, strings.Repeat("a", UnixPathBytes()-len(dir)-7)+".sock")
	if len(longest) != UnixPathBytes()-1 {
		t.Fatal("bad longest fixture")
	}
	boundary, err := net.ListenUnix("unix", &net.UnixAddr{Name: longest, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer boundary.Close()
	os.Chmod(longest, 0600)
	conn, err = ConnectPeer(longest, os.Getpid(), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestReplyListenerIsPrivateAndCleansOnlyItself(t *testing.T) {
	listener, path, cleanup, err := ReplyListener()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = ValidatePeerSocket(path, []string{filepath.Dir(path)}); err != nil {
		t.Fatal(err)
	}
	conn, err := ConnectPeer(path, os.Getpid(), []string{filepath.Dir(path)})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	listener.Close()
	cleanup()
	if _, err = os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("reply directory retained")
	}
}
