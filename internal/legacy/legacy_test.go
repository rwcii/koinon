package legacy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

func writeInstall(t *testing.T, config string) string {
	t.Helper()
	prefix := t.TempDir()
	if err := os.WriteFile(filepath.Join(prefix, "install.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return prefix
}

// TestMarkerMatchesTheBaselineContract: the marker is exactly what the main release's
// runtime_names.install_config refuses on: installation_state "upgrading" and an upgrade
// object with exactly version 1, an absolute operation and a 64-hex plan.
func TestMarkerMatchesTheBaselineContract(t *testing.T) {
	original := `{"state_root": "/s", "unit_dir": "/u", "memory_services": {"version": 1, "repositories": {}}, "big": 9007199254740993}` + "\n"
	prefix := writeInstall(t, original)
	plan := strings.Repeat("ab", 32)
	i, _ := ReadInstall(prefix)
	expected, _ := i.Digest()
	// A configuration other than the one inspected is refused.
	if err := PublishMarker(prefix, "/state/upgrade/1", plan, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("stale configuration: %v", err)
	}
	if err := PublishMarker(prefix, "/state/upgrade/1", plan, expected); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(prefix, "install.json"))
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	var marker map[string]any
	json.Unmarshal(config["upgrade"], &marker)
	if string(config["installation_state"]) != `"upgrading"` || len(marker) != 3 || marker["version"] != float64(1) ||
		marker["operation"] != "/state/upgrade/1" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(marker["plan"].(string)) {
		t.Fatalf("marker %s", data)
	}
	if string(config["unit_dir"]) != `"/u"` || string(config["memory_services"]) == "" || string(config["big"]) != "9007199254740993" {
		t.Fatalf("other fields lost: %s", data)
	}
	// The same marker again is accepted; another operation's is refused.
	if err := PublishMarker(prefix, "/state/upgrade/1", plan, expected); err != nil {
		t.Fatal(err)
	}
	if err := PublishMarker(prefix, "/state/upgrade/2", plan, expected); err == nil {
		t.Fatal("a second operation took the marker")
	}
	if err := ReplaceWithRemoving(prefix, "/state/upgrade/2"); err == nil {
		t.Fatal("another operation replaced the marker")
	}
	if err := RestoreRaw(prefix, "/state/upgrade/1", []byte(original)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(prefix, "install.json")); string(data) != original {
		t.Fatalf("restore is not byte-identical: %s", data)
	}
	PublishMarker(prefix, "/state/upgrade/1", plan, expected)
	if err := ReplaceWithRemoving(prefix, "/state/upgrade/1"); err != nil {
		t.Fatal(err)
	}
	i, err := ReadInstall(prefix)
	if err != nil || i.State() != "removing" || i.Config["upgrade"] != nil {
		t.Fatalf("removing %+v %v", i, err)
	}
	if err := RestoreRaw(prefix, "/state/upgrade/1", []byte(original)); err == nil {
		t.Fatal("restored over the removing state")
	}
}

func TestReadInstallChecks(t *testing.T) {
	prefix := writeInstall(t, `{}`)
	os.Chmod(filepath.Join(prefix, "install.json"), 0666)
	if _, err := ReadInstall(prefix); err == nil {
		t.Fatal("accepted an install.json writable by others")
	}
	if i, err := ReadInstall(t.TempDir()); i != nil || err != nil {
		t.Fatalf("absent: %v %v", i, err)
	}
}

func TestHoldWritersRefusesAHeldLock(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "sessions", "k")
	os.MkdirAll(home, 0700)
	os.MkdirAll(filepath.Join(root, "memory", "0123456789abcdef"), 0700)
	components, err := Components(root)
	if err != nil || len(components) != 2 {
		t.Fatalf("components %+v %v", components, err)
	}
	f, _ := os.OpenFile(filepath.Join(home, "supervisor.lock"), os.O_RDWR|os.O_CREATE, 0600)
	syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	if _, err := HoldWriters(components); err == nil || !strings.Contains(err.Error(), "python_running") {
		t.Fatalf("held lock: %v", err)
	}
	f.Close()
	g, err := HoldWriters(components)
	if err != nil {
		t.Fatal(err)
	}
	// While held, a Python writer that starts cannot take its lock.
	other, _ := os.OpenFile(filepath.Join(home, "notifier.lock"), os.O_RDWR, 0600)
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("a writer took a held lock")
	}
	other.Close()
	g.Release()
}
