package platform

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// BoundUnixPaths lists the filesystem addresses of the AF_UNIX sockets that the kernel
// holds, listening or only bound: a socket stays in this table until its last descriptor
// closes, so a path that is not listed has no live owner.
func BoundUnixPaths() ([]string, error) { return boundUnixPaths() }

// boundPaths reads the address column of a socket table whose header has exactly the
// columns given. The address is the text after the other columns, so a path that
// contains spaces stays whole. A table with any other header is refused, because a
// misread table would show no owners.
func boundPaths(table []byte, columns ...string) ([]string, error) {
	lines := strings.Split(string(table), "\n")
	header := -1
	for n, line := range lines {
		if strings.Join(strings.Fields(line), " ") == strings.Join(columns, " ") {
			header = n
			break
		}
	}
	if header < 0 {
		return nil, errors.New("unrecognized AF_UNIX socket table")
	}
	pattern := regexp.MustCompile(`^\s*(?:\S+\s+){` + strconv.Itoa(len(columns)-1) + `}(/.*)$`)
	var paths []string
	for _, line := range lines[header+1:] {
		if m := pattern.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			paths = append(paths, m[1])
		}
	}
	return paths, nil
}
