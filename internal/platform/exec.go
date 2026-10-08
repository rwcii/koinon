package platform

import "syscall"

// Exec keeps the process identity and terminal; the launcher adds no supervisor.
func Exec(executable string, args, env []string) error {
	return syscall.Exec(executable, args, env)
}
