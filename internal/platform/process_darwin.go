package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
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

// ProcessStart reports a process's start time as an opaque value that identifies the process
// together with its ID: kern.proc.pid's p_starttime in microseconds since the epoch, read
// through sysctl without cgo. Values are comparable only with other values of this host;
// ErrProcessGone means no such process.
func ProcessStart(pid int) (int64, error) {
	if pid <= 0 {
		return 0, ErrProcessGone
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	// A process that does not exist returns no record, which the wrapper reports as EIO.
	if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) || err == nil && int(info.Proc.P_pid) != pid {
		return 0, ErrProcessGone
	}
	if err != nil {
		return 0, err
	}
	start := info.Proc.P_starttime
	return int64(start.Sec)*1_000_000 + int64(start.Usec), nil
}

// ProcessEnvironment reports one variable of a process this user owns, from its initial
// environment: the kern.procargs2 sysctl, read without cgo, holds the argument count, the
// executable path and its padding, the arguments and then the environment, each ended by a
// NUL; an empty string ends the environment. A missing variable is "" with no error.
func ProcessEnvironment(pid int, name string) (string, error) {
	if pid <= 0 {
		return "", ErrProcessGone
	}
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", err
	}
	return procargsEnvironment(data, name)
}

// procargsEnvironment reads one variable from a kern.procargs2 buffer.
func procargsEnvironment(data []byte, name string) (string, error) {
	unreadable := errors.New("process arguments unreadable")
	if len(data) < 4 {
		return "", unreadable
	}
	argc := int(binary.NativeEndian.Uint32(data))
	rest := data[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return "", unreadable
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for ; argc > 0; argc-- {
		if end = bytes.IndexByte(rest, 0); end < 0 {
			return "", unreadable
		}
		rest = rest[end+1:]
	}
	for len(rest) > 0 {
		entry := rest
		if end = bytes.IndexByte(rest, 0); end >= 0 {
			entry, rest = rest[:end], rest[end+1:]
		} else {
			rest = nil
		}
		if len(entry) == 0 {
			break
		}
		if value, ok := strings.CutPrefix(string(entry), name+"="); ok {
			return value, nil
		}
	}
	return "", nil
}

// ProcessGroups is not read on macOS: no native evidence for a pane wake there yet (#275).
func ProcessGroups() (map[int]ProcessGroup, error) {
	return nil, errors.New("process groups are not read on macOS")
}
