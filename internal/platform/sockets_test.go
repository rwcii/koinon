package platform

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestBoundPathsParsesBothTables(t *testing.T) {
	linux := "Num       RefCount Protocol Flags    Type St Inode Path\n" +
		"0000000000000000: 00000002 00000000 00010000 0001 01 79111 /run/user/1000/bus\n" +
		"0000000000000000: 00000003 00000000 00000000 0001 03 79112\n" +
		"0000000000000000: 00000002 00000000 00010000 0001 01 79113 @abstract\n" +
		"0000000000000000: 00000002 00000000 00000000 0001 07 79114 /tmp/python prefix/control.sock\n"
	paths, err := boundPaths([]byte(linux), "Num", "RefCount", "Protocol", "Flags", "Type", "St", "Inode", "Path")
	if err != nil || !slices.Equal(paths, []string{"/run/user/1000/bus", "/tmp/python prefix/control.sock"}) {
		t.Fatalf("linux %q %v", paths, err)
	}
	darwin := "Active LOCAL (UNIX) domain sockets\n" +
		"Address          Type   Recv-Q Send-Q            Inode             Conn             Refs          Nextref Addr\n" +
		"3c9b5b3a8c3f1b2d stream      0      0                0 3c9b5b3a8c3f1a65                0                0\n" +
		"3c9b5b3a8c3f1e4d stream      0      0 3c9b5b3a8f1a3c1d                0                0                0 /tmp/cc-socks/0123456789abcdef-control.sock\n"
	paths, err = boundPaths([]byte(darwin), "Address", "Type", "Recv-Q", "Send-Q", "Inode", "Conn", "Refs", "Nextref", "Addr")
	if err != nil || !slices.Equal(paths, []string{"/tmp/cc-socks/0123456789abcdef-control.sock"}) {
		t.Fatalf("darwin %q %v", paths, err)
	}
	// Another layout would read as a table without owners; it is refused instead.
	if _, err := boundPaths([]byte("Num RefCount Protocol Flags Type St Path\n"), "Num", "RefCount", "Protocol", "Flags", "Type", "St", "Inode", "Path"); err == nil {
		t.Fatal("an unknown header was read")
	}
}

// TestBoundUnixPathsListsABoundSocket: a socket that is only bound, not listening, is in
// the kernel's table until its descriptor closes.
func TestBoundUnixPathsListsABoundSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "kbs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		syscall.Close(fd)
		t.Fatal(err)
	}
	paths, err := BoundUnixPaths()
	if err != nil || !slices.Contains(paths, path) {
		syscall.Close(fd)
		t.Fatalf("bound socket %s missing from %d paths: %v", path, len(paths), err)
	}
	syscall.Close(fd)
	if paths, err := BoundUnixPaths(); err != nil || slices.Contains(paths, path) {
		t.Fatalf("closed socket %s still listed: %v", path, err)
	}
}
