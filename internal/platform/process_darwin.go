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
