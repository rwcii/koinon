package platform

import (
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
