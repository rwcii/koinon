package platform

import (
	"context"
	"os/exec"
	"time"
)

func boundUnixPaths() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	table, err := exec.CommandContext(ctx, "/usr/sbin/netstat", "-anf", "unix").Output()
	if err != nil {
		return nil, err
	}
	return boundPaths(table, "Address", "Type", "Recv-Q", "Send-Q", "Inode", "Conn", "Refs", "Nextref", "Addr")
}
