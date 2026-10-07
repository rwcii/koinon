package platform

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

// ProcessCommand reports a process's executable path and arguments. macOS has no /proc;
// ps reports the executable path as comm and the arguments split on spaces.
func ProcessCommand(pid int) (string, []string, error) {
	out, err := exec.Command("/bin/ps", "-ww", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", nil, err
	}
	executable := strings.TrimSpace(string(out))
	out, err = exec.Command("/bin/ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || executable == "" {
		return "", nil, errors.Join(err, errors.New("process not found"))
	}
	return executable, strings.Fields(string(out)), nil
}

// ProcessParents reports the parent of every process, from one ps listing.
func ProcessParents() (map[int]int, error) {
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	parents := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil && pid > 0 {
			parents[pid] = parent
		}
	}
	if len(parents) == 0 {
		return nil, errors.New("process table unreadable")
	}
	return parents, nil
}
