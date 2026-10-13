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

// ProcessStart reports a process's start time as an opaque value that identifies the process
// together with its ID: field 22 of /proc/<pid>/stat, in clock ticks since boot. Values are
// comparable only with other values of this host; ErrProcessGone means no such process.
func ProcessStart(pid int) (int64, error) {
	if pid <= 0 {
		return 0, ErrProcessGone
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrProcessGone
	}
	if err != nil {
		return 0, err
	}
	// Fields after the command name's closing parenthesis start with field 3 (the state).
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, errors.New("process stat unreadable")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return 0, errors.New("process stat unreadable")
	}
	return strconv.ParseInt(fields[19], 10, 64)
}

// ProcessEnvironment reports one variable of a process this user owns, from its initial
// environment. A missing variable is "" with no error.
func ProcessEnvironment(pid int, name string) (string, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return "", err
	}
	for _, entry := range strings.Split(string(data), "\x00") {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value, nil
		}
	}
	return "", nil
}

// ProcessGroups reports the process group, controlling terminal and that terminal's
// foreground process group of every process this user can see: fields 5, 7 and 8 of
// /proc/<pid>/stat. A process without a terminal has Terminal 0. A process that exits during
// the scan is left out.
func ProcessGroups() (map[int]ProcessGroup, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	groups := map[int]ProcessGroup{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			continue
		}
		// Fields after the command name start with field 3 (the state).
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 6 {
			continue
		}
		group, groupErr := strconv.Atoi(fields[2])
		terminal, terminalErr := strconv.Atoi(fields[4])
		foreground, foregroundErr := strconv.Atoi(fields[5])
		if groupErr == nil && terminalErr == nil && foregroundErr == nil {
			groups[pid] = ProcessGroup{Group: group, Terminal: terminal, Foreground: foreground}
		}
	}
	if len(groups) == 0 {
		return nil, errors.New("process table unreadable")
	}
	return groups, nil
}
