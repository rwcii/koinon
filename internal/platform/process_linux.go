package platform

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// ProcessCommand reports a process's executable path and arguments.
func ProcessCommand(pid int) (string, []string, error) {
	base := "/proc/" + strconv.Itoa(pid)
	executable, err := os.Readlink(base + "/exe")
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(base + "/cmdline")
	if err != nil {
		return "", nil, err
	}
	return executable, strings.Split(strings.TrimRight(string(data), "\x00"), "\x00"), nil
}

// ProcessParents reports the parent of every process this user can see. A process that
// exits during the scan is left out.
func ProcessParents() (map[int]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	parents := map[int]int{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		// The command name is in parentheses and can hold any character; the state and the
		// parent follow the last closing parenthesis.
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			continue
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 2 {
			continue
		}
		if parent, err := strconv.Atoi(fields[1]); err == nil {
			parents[pid] = parent
		}
	}
	if len(parents) == 0 {
		return nil, errors.New("process table unreadable")
	}
	return parents, nil
}
