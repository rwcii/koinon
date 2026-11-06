package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"

	"github.com/rwcii/koinon/internal/platform"
)

// dashboardLocalFile records the answer "only on this computer" to the install question, so a
// later install asks nothing (DASHBOARD-LAN-624, part 2, in ADR 001 Decision 2). The network
// setting stays in dashboard-address.json; the daemon never reads this file.
const dashboardLocalFile = "dashboard-local.json"

type dashboardLocal struct {
	Choice string `json:"choice"`
}

// ReadDashboardLocal reports whether the local-only choice is saved. It reads only an
// owner-checked private file; an unsafe or malformed file is reported.
func ReadDashboardLocal(root string) (bool, error) {
	path := filepath.Join(root, dashboardLocalFile)
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, dashboardLocalError(path, err)
	}
	defer f.Close()
	var c dashboardLocal
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Choice != "local" {
		return false, dashboardLocalError(path, errors.New("invalid JSON or choice"))
	}
	return true, nil
}

func dashboardLocalError(path string, err error) error {
	command := "koinon install --state-dir " + ShellWord(filepath.Dir(path))
	return fmt.Errorf("dashboard_configuration_invalid: %s: %w. Correct the file's ownership, mode 0600 and regular-file type, or remove it; then run %s, or give --dashboard-address HOST:PORT or none", path, err, command)
}

// WriteDashboardLocal saves the local-only choice in the private state directory.
func WriteDashboardLocal(root string) error {
	root, err := platform.PrivateDir(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, dashboardLocalFile)
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return dashboardLocalError(path, err)
	}
	data, err := json.Marshal(dashboardLocal{Choice: "local"})
	if err != nil {
		return err
	}
	return platform.WriteAtomic(path, append(data, '\n'), 0600)
}

// DashboardHostAddresses lists this host's addresses that the network dashboard can bind:
// no loopback, multicast or unspecified address, and no IPv6 link-local address, which needs
// a zone that the setting does not carry. IPv4 comes first, each group in sorted order.
func DashboardHostAddresses() ([]string, error) {
	return dashboardHostAddresses(net.InterfaceAddrs)
}

func dashboardHostAddresses(interfaces func() ([]net.Addr, error)) ([]string, error) {
	local, err := interfaces()
	if err != nil {
		return nil, err
	}
	var v4, v6 []string
	for _, address := range local {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !dashboardIP(ip) {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	slices.Sort(v4)
	slices.Sort(v6)
	return slices.Compact(append(v4, v6...)), nil
}
