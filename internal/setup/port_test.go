package setup

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// A hook without KOINON_DAEMON_ADDRESS reaches the daemon at the state directory's
// recorded port (#405); the variable, which a launcher sets, comes first.
func TestHookUsesRecordedPort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	_, port, _ := net.SplitHostPort(d.Addresses()[0])
	number, _ := net.LookupPort("tcp", port)
	if err := core.RecordPort(root, number); err != nil {
		t.Fatal(err)
	}
	env := func(values map[string]string) HookEnv {
		return HookEnv{Getenv: func(k string) string { return values[k] }, Now: time.Now}
	}
	if _, err := env(map[string]string{"KOINON_STATE_DIR": root}).call("/v1/status", nil); err != nil {
		t.Fatalf("recorded port: %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	free := listener.Addr().String()
	listener.Close()
	if _, err := env(map[string]string{"KOINON_STATE_DIR": root, "KOINON_DAEMON_ADDRESS": free}).call("/v1/status", nil); err == nil {
		t.Fatal("the hook ignored KOINON_DAEMON_ADDRESS")
	}
}
