package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/setup"
)

// claudeCLI is the only agent CLI on PATH, a claude that keeps its MCP entries in a file, enough for setup and for
// the plan's `claude mcp get koinon`.
func claudeCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cli, state := filepath.Join(dir, "claude"), filepath.Join(dir, "entries")
	script := `#!/bin/sh
touch '` + state + `'
case "$1 $2" in
"mcp get") line=$(command grep "^$3 " '` + state + `') || { echo "No MCP server named $3"; exit 1; }
  set -- $line; printf '%s:\n  Scope: User config\n  Type: stdio\n  Command: %s\n  Args: %s\n' "$1" "$2" "$3"; exit 0 ;;
"mcp remove") for name; do :; done; command grep -v "^$name " '` + state + `' > '` + state + `.new'; mv '` + state + `.new' '` + state + `'; exit 0 ;;
"mcp add") shift 2; while [ "$1" != "--" ]; do name=$1; shift; done; shift
  command grep -v "^$name " '` + state + `' > '` + state + `.new'; mv '` + state + `.new' '` + state + `'
  printf '%s %s\n' "$name" "$*" >> '` + state + `'; exit 0 ;;
esac
exit 2
`
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Only this CLI and the system tools: the person's other agent CLIs stay out.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	return cli
}

// After `koinon setup claude` without --binary from a Homebrew keg, `koinon install --plan`,
// and so the Homebrew caveats, need no setup step: setup records the binary that the plan
// checks (#577).
func TestPlanAfterDefaultSetupFromAKeg(t *testing.T) {
	ctx := context.Background()
	e := planEnv(t, "systemd")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_DATA_HOME", base)
	if e.o.Prefix, err = legacy.GoPrefix(); err != nil {
		t.Fatal(err)
	}
	brew := filepath.Join(base, "brew")
	keg := homebrewKeg(t, brew, "0.8.0", "synthetic binary")
	opt := filepath.Join(brew, "opt", "koinon", "bin", "koinon")
	os.MkdirAll(filepath.Dir(Binary(e.o.Prefix)), 0755)
	if err := os.Symlink(opt, Binary(e.o.Prefix)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.Services.WriteDaemon(Binary(e.o.Prefix), e.o.StateDir); err != nil {
		t.Fatal(err)
	}
	e.o.Source = keg
	e.o.Inspect = setup.Inspect
	cli := claudeCLI(t)
	if _, err := setup.Run(ctx, setup.Options{Family: "claude", CLI: cli, Binary: setup.DefaultBinary(keg), ClaudeConfig: e.o.ClaudeConfig, StateDir: e.o.StateDir}); err != nil {
		t.Fatal(err)
	}
	if steps := plan(t, e); len(steps) != 0 {
		t.Fatalf("plan after default setup: %q", steps)
	}
	// The opt path, which PR 578 recorded, is not the binary that the plan checks.
	if _, err := setup.Run(ctx, setup.Options{Family: "claude", CLI: cli, Binary: opt, ClaudeConfig: e.o.ClaudeConfig, StateDir: e.o.StateDir}); err != nil {
		t.Fatal(err)
	}
	if steps := plan(t, e); len(steps) != 1 {
		t.Fatalf("plan after an opt-path setup: %q", steps)
	}
}
