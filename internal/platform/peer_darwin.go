package platform

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"strconv"
	"syscall"
)

func UnixPathBytes() int { return 104 }
func claudeSocketDirs() []string {
	return []string{"/tmp/cc-socks", "/tmp/cc-socks-" + strconv.Itoa(os.Geteuid())}
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
		u, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			checked = e
			return
		}
		if u.Version != 0 || u.Uid != uint32(os.Geteuid()) {
			checked = errors.New("foreign kernel peer")
			return
		}
		pid, e = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if e != nil {
			checked = e
		} else if pid <= 0 {
			checked = errors.New("kernel peer PID unavailable")
		}
	})
	if err != nil {
		return 0, err
	}
	return pid, checked
}
