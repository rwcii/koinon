package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// #665: the install's tailnet answer enables remote access on its port for the given daemon,
// and asks the operator question on the install's own terminal reader, so an answer typed
// ahead for a later question stays for it.
func TestInstallRemoteEnableSharesTheTerminal(t *testing.T) {
	ctx := context.Background()
	adapter := &grantableCLIAdapter{}
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	_, slave := openPTY(t)
	tell, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer tell.Close()
	term := &terminal{ctx: ctx, file: slave, in: bufio.NewReader(strings.NewReader("y\nlater answer\n")), tell: tell}
	goos, account, runCmd := operatorGOOS, operatorAccount, operatorRun
	t.Cleanup(func() { operatorGOOS, operatorAccount, operatorRun = goos, account, runCmd })
	operatorGOOS, operatorAccount = "linux", func() string { return "synthetic-user" }
	var grants [][]string
	operatorRun = func(cmd *exec.Cmd) error {
		grants = append(grants, cmd.Args)
		if cmd.Stdin != slave || cmd.Stdout != tell {
			t.Errorf("grant terminal: %v %v", cmd.Stdin, cmd.Stdout)
		}
		adapter.mu.Lock()
		adapter.operator = true
		adapter.mu.Unlock()
		return nil
	}
	url, err := installRemoteEnable(term)(ctx, root, address, 8443)
	if err != nil || url != "https://board.example.ts.net:8443/dashboard/" || len(grants) != 1 || !slices.Equal(grants[0], grantArgs) || adapter.port != "8443" {
		t.Fatalf("enable: %q %v grants %v port %q", url, err, grants, adapter.port)
	}
	if later, err := term.line("Next: "); err != nil || later != "later answer" {
		t.Fatalf("typed-ahead answer: %q %v", later, err)
	}
	// Another state directory names another daemon's secret: no call reaches this one.
	if _, err := installRemoteEnable(term)(ctx, filepath.Join(t.TempDir(), "other"), address, 8443); err == nil {
		t.Fatal("enabled with another installation's state")
	}
}
