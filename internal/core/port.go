package core

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/platform"
)

// DefaultPort is the daemon's loopback port when the state directory records none.
const DefaultPort = 47671

// portFile is the state directory's file that koinon install --port writes (#405).
func portFile(root string) string { return filepath.Join(root, "port") }

// RecordedPort returns the port that the state directory records, or DefaultPort when it
// records none. A port file that is not a private file of this user holding one port is
// refused, never read as the default.
func RecordedPort(root string) (int, error) {
	path := portFile(root)
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPort, nil
	}
	invalid := fmt.Errorf("port_invalid: %s must be a private file holding one port from 1 to 65535", path)
	if err != nil {
		return 0, invalid
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 17))
	if err != nil || len(data) > 16 {
		return 0, invalid
	}
	port, err := strconv.Atoi(strings.TrimSuffix(string(data), "\n"))
	if err != nil || !ValidPort(port) {
		return 0, invalid
	}
	return port, nil
}

// ValidPort reports whether port is a TCP port the daemon can listen on.
func ValidPort(port int) bool { return port >= 1 && port <= 65535 }

// RecordPort writes port to the state directory, creating the private directory if needed.
// The service runs koinon serve --state-dir without listen options, so the daemon and every
// client of that state directory read it.
func RecordPort(root string, port int) error {
	if !ValidPort(port) {
		return errors.New("invalid_request: the port must be from 1 to 65535")
	}
	root, err := platform.PrivateDir(root)
	if err != nil {
		return err
	}
	return platform.WriteAtomic(portFile(root), []byte(strconv.Itoa(port)+"\n"), 0600)
}

// Listeners are the daemon's IPv4 and IPv6 loopback listeners on port.
func Listeners(port int) []string {
	p := strconv.Itoa(port)
	return []string{net.JoinHostPort("127.0.0.1", p), net.JoinHostPort("::1", p)}
}

// DaemonAddress is a client's daemon address when its command line names none:
// KOINON_DAEMON_ADDRESS, which a launcher passes to its agent, or else 127.0.0.1 at the
// state directory's recorded port.
func DaemonAddress(root string, getenv func(string) string) (string, error) {
	if address := getenv("KOINON_DAEMON_ADDRESS"); address != "" {
		return address, nil
	}
	port, err := RecordedPort(root)
	if err != nil {
		return "", err
	}
	return Listeners(port)[0], nil
}

// InstalledAddress reports whether address is where the installed service of root listens:
// 127.0.0.1 or ::1 at the recorded port.
func InstalledAddress(root, address string) bool {
	host, port, _ := net.SplitHostPort(address)
	recorded, err := RecordedPort(root)
	return err == nil && port == strconv.Itoa(recorded) && (host == "127.0.0.1" || host == "::1")
}
