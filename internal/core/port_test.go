package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The state directory records the port that koinon install --port set (#405). Without a
// record, the port and every address stay at 47671.
func TestRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	noEnv := func(string) string { return "" }
	if port, err := RecordedPort(root); err != nil || port != DefaultPort || DefaultPort != 47671 {
		t.Fatalf("no record: %d %v", port, err)
	}
	if address, err := DaemonAddress(root, noEnv); err != nil || address != "127.0.0.1:47671" {
		t.Fatalf("default address %q %v", address, err)
	}
	if got := Listeners(DefaultPort); len(got) != 2 || got[0] != "127.0.0.1:47671" || got[1] != "[::1]:47671" {
		t.Fatalf("default listeners %v", got)
	}
	if !InstalledAddress(root, "127.0.0.1:47671") || !InstalledAddress(root, "[::1]:47671") || InstalledAddress(root, "127.0.0.1:47670") {
		t.Fatal("installed address without a record")
	}

	if err := RecordPort(root, 47670); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "port")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("port file %v %v", info, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "47670\n" {
		t.Fatalf("port file holds %q", data)
	}
	if port, err := RecordedPort(root); err != nil || port != 47670 {
		t.Fatalf("recorded %d %v", port, err)
	}
	if address, err := DaemonAddress(root, noEnv); err != nil || address != "127.0.0.1:47670" {
		t.Fatalf("recorded address %q %v", address, err)
	}
	if got := Listeners(47670); got[0] != "127.0.0.1:47670" || got[1] != "[::1]:47670" {
		t.Fatalf("listeners %v", got)
	}
	if !InstalledAddress(root, "127.0.0.1:47670") || !InstalledAddress(root, "[::1]:47670") || InstalledAddress(root, "127.0.0.1:47671") {
		t.Fatal("installed address with a record")
	}
	// KOINON_DAEMON_ADDRESS, which a launcher passes to its agent, comes first.
	env := func(name string) string {
		return map[string]string{"KOINON_DAEMON_ADDRESS": "127.0.0.1:5000"}[name]
	}
	if address, err := DaemonAddress(root, env); err != nil || address != "127.0.0.1:5000" {
		t.Fatalf("environment address %q %v", address, err)
	}
	// The default port is recorded like any other.
	if err := RecordPort(root, 47671); err != nil {
		t.Fatal(err)
	}
	if port, err := RecordedPort(root); err != nil || port != 47671 {
		t.Fatalf("back to the default: %d %v", port, err)
	}

	for _, port := range []int{0, -1, 65536} {
		if err := RecordPort(root, port); err == nil {
			t.Fatalf("port %d recorded", port)
		}
	}
	// A damaged record is refused, never read as the default.
	for _, content := range []string{"", "abc\n", "0\n", "65536\n", "47670 47671\n", strings.Repeat("1", 20)} {
		os.WriteFile(path, []byte(content), 0600)
		if _, err := RecordedPort(root); err == nil || !strings.HasPrefix(err.Error(), "port_invalid: ") {
			t.Fatalf("%q: %v", content, err)
		}
		if _, err := DaemonAddress(root, noEnv); err == nil {
			t.Fatalf("%q gave an address", content)
		}
	}
	os.WriteFile(path, []byte("47670\n"), 0644)
	os.Chmod(path, 0644)
	if _, err := RecordedPort(root); err == nil {
		t.Fatal("a port file readable by others was accepted")
	}
}

// The start hint names koinon install only at the installed service's port (#405).
func TestStartDaemonUsesRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if hint := StartDaemon(root, "127.0.0.1:47671"); !strings.Contains(hint, "koinon install --state-dir") {
		t.Fatalf("default: %s", hint)
	}
	if err := RecordPort(root, 47670); err != nil {
		t.Fatal(err)
	}
	if hint := StartDaemon(root, "127.0.0.1:47670"); !strings.Contains(hint, "koinon install --state-dir") {
		t.Fatalf("recorded: %s", hint)
	}
	if hint := StartDaemon(root, "127.0.0.1:47671"); strings.Contains(hint, "koinon install") ||
		!strings.Contains(hint, "--listen 127.0.0.1:47671 --listen-v6 '[::1]:47671'") {
		t.Fatalf("other port: %s", hint)
	}
}
