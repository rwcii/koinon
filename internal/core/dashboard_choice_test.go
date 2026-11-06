package core

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The install question lists only addresses that the network dashboard can bind (#624 c).
func TestDashboardHostAddresses(t *testing.T) {
	var local []net.Addr
	for _, cidr := range []string{"127.0.0.1/8", "::1/128", "198.51.100.7/24", "192.0.2.10/24", "fe80::10/64",
		"169.254.0.5/16", "2001:db8::10/64", "224.0.0.251/32", "192.0.2.10/24"} {
		ip, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		local = append(local, n)
	}
	got, err := dashboardHostAddresses(func() ([]net.Addr, error) { return local, nil })
	want := []string{"169.254.0.5", "192.0.2.10", "198.51.100.7", "2001:db8::10"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %q %v", got, err)
	}
}

// The local-only answer is a private file; an unsafe or malformed one is refused.
func TestDashboardLocalChoice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if local, err := ReadDashboardLocal(root); err != nil || local {
		t.Fatalf("absent %v %v", local, err)
	}
	if err := WriteDashboardLocal(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "dashboard-local.json")
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, err)
	}
	if local, err := ReadDashboardLocal(root); err != nil || !local {
		t.Fatalf("saved %v %v", local, err)
	}
	for _, content := range []string{`{"choice":"network"}`, `{"choice":"local","x":1}`, `{"choice":"local"}{}`} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadDashboardLocal(root); err == nil {
			t.Fatalf("%s accepted", content)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDashboardLocal(root); err == nil {
		t.Fatal("mode 0644 accepted")
	}
	if err := WriteDashboardLocal(root); err == nil {
		t.Fatal("an unsafe file was replaced")
	}
}
