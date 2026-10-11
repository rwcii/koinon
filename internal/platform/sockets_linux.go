package platform

import (
	"os"
	"strconv"
)

// boundUnixPaths reads the table of every network namespace that a process of this user
// is in: /proc/net/unix shows only the reader's own namespace, and a sandboxed caller
// would otherwise miss the sockets of the user's session.
func boundUnixPaths() ([]string, error) {
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return nil, err
	}
	tables := map[string]string{self: "/proc/self/net/unix"}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		// Another user's process, or one that ended, has no readable namespace link.
		if ns, err := os.Readlink("/proc/" + e.Name() + "/ns/net"); err == nil && tables[ns] == "" {
			tables[ns] = "/proc/" + e.Name() + "/net/unix"
		}
	}
	var paths []string
	for ns, file := range tables {
		table, err := os.ReadFile(file)
		if err != nil && ns != self {
			continue // its only process ended; the namespace went with it
		}
		if err != nil {
			return nil, err
		}
		found, err := boundPaths(table, "Num", "RefCount", "Protocol", "Flags", "Type", "St", "Inode", "Path")
		if err != nil {
			return nil, err
		}
		paths = append(paths, found...)
	}
	return paths, nil
}
