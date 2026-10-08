package platform

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"strconv"
	"syscall"
)

func UnixPathBytes() int { return 108 }
func claudeSocketDirs() []string {
	return []string{"/tmp/cc-socks", "/tmp/cc-socks-" + strconv.Itoa(os.Geteuid()), "/run/user/" + strconv.Itoa(os.Geteuid()) + "/cc-socks"}
}
func sameUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}
func PeerPID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	pid := 0
	var checked error
	err = raw.Control(func(fd uintptr) {
		u, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			checked = e
			return
		}
		if u.Uid != uint32(os.Geteuid()) || u.Pid <= 0 {
			checked = errors.New("foreign kernel peer")
			return
		}
		pid = int(u.Pid)
	})
	if err != nil {
		return 0, err
	}
	return pid, checked
}
