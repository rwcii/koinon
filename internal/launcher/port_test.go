package launcher

import (
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// A launch without --address uses KOINON_DAEMON_ADDRESS or the state directory's recorded
// port, and passes that address to the agent and its MCP server (#405).
func TestParseUsesRecordedPort(t *testing.T) {
	t.Setenv("KOINON_DAEMON_ADDRESS", "")
	root := filepath.Join(t.TempDir(), "state")
	parse := func(args ...string) Options {
		t.Helper()
		o, err := Parse("codex", append([]string{"--state-dir", root}, args...))
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	if o := parse(); o.Address != "127.0.0.1:47671" {
		t.Fatalf("no record: %s", o.Address)
	}
	if err := core.RecordPort(root, 47670); err != nil {
		t.Fatal(err)
	}
	if o := parse("--", "--address", "literal"); o.Address != "127.0.0.1:47670" || len(o.Args) != 2 {
		t.Fatalf("recorded: %+v", o)
	}
	if o := parse("--address", "[::1]:6000"); o.Address != "[::1]:6000" {
		t.Fatalf("flag: %s", o.Address)
	}
	t.Setenv("KOINON_DAEMON_ADDRESS", "127.0.0.1:5000")
	t.Setenv("KOINON_STATE_DIR", root)
	if o := parse(); o.Address != "127.0.0.1:5000" {
		t.Fatalf("environment: %s", o.Address)
	}
}

// A launched agent gets the launch address as KOINON_DAEMON_ADDRESS only when it is not where
// the state directory's installed service listens; without it, the agent's clients follow a
// later koinon install --port (#549).
func TestPinsAddress(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	pins := func(address string) bool { return Options{StateDir: root, Address: address}.pinsAddress() }
	if pins("127.0.0.1:47671") || pins("[::1]:47671") || !pins("127.0.0.1:5000") {
		t.Fatal("no record")
	}
	if err := core.RecordPort(root, 47681); err != nil {
		t.Fatal(err)
	}
	if pins("127.0.0.1:47681") || pins("[::1]:47681") || !pins("127.0.0.1:47671") || !pins("192.0.2.1:47681") {
		t.Fatal("recorded port")
	}
	// An address that the launcher itself inherited never reaches the agent.
	for _, family := range []string{"claude", "codex"} {
		if env := cleanEnvironment([]string{"KOINON_DAEMON_ADDRESS=127.0.0.1:47671", "PATH=/synthetic"}, family); len(env) != 1 || env[0] != "PATH=/synthetic" {
			t.Fatalf("%s: inherited address kept: %q", family, env)
		}
	}
}
