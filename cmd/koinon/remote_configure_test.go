package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/install"
)

// stateSnapshot is every file of a state directory with its content, mode and time, so that a
// test can show that a command changed nothing.
func stateSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return filepath.SkipDir
		}
		if err != nil {
			return err
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String() + " " + info.ModTime().String()
		// The daemon's own store changes while it runs; the dashboard choice is in the files.
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "state.sqlite3") {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry += " " + string(data)
		}
		if !strings.HasPrefix(e.Name(), "state.sqlite3") {
			files[path] = entry
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Each option that does not apply is refused before anything is read or changed (#685, C3).
func TestRemoteConfigureRefusesOptionsWithoutEffects(t *testing.T) {
	ctx := context.Background()
	adapter := &remoteCLIAdapter{}
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	before := stateSnapshot(t, root)
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"--choice", "lan"}, "invalid_request"},
		{[]string{"--choice", ""}, "invalid_request"},
		{[]string{"--https-port", "443"}, "invalid_request"},
		{[]string{"--choice", "network", "--https-port", "443"}, "invalid_request"},
		{[]string{"--choice", "local", "--https-port", "443"}, "invalid_request"},
		{[]string{"--choice", "tailnet", "--https-port", "0"}, "invalid_request"},
		{[]string{"--choice", "tailnet", "--https-port", "65536"}, "invalid_request"},
		{[]string{"--dashboard-address", "192.0.2.10:8443"}, "invalid_request"},
		{[]string{"--choice", "tailnet", "--dashboard-address", "192.0.2.10:8443"}, "invalid_request"},
		{[]string{"--choice", "network", "--dashboard-address", "none"}, "dashboard_address_invalid"},
		{[]string{"--choice", "network", "--dashboard-address", "192.0.2.10"}, "dashboard_address_invalid"},
		{[]string{"--choice", "network", "--dashboard-address", ":8443"}, "dashboard_address_invalid"},
		{[]string{"--choice", "network", "--dashboard-address", "192.0.2.10:0"}, "dashboard_address_invalid"},
		{[]string{"--choice", "network", "--dashboard-address", "192.0.2.10:08443"}, "dashboard_address_invalid"},
		{[]string{"--choice", "network", "--dashboard-address", ""}, "dashboard_address_invalid"},
		{[]string{"extra"}, "invalid_request"},
	} {
		args := append([]string{"remote", "configure", "--state-dir", root, "--address", address}, tc.args...)
		var out bytes.Buffer
		err := run(ctx, args, strings.NewReader(""), &out)
		var usage usageError
		if !errors.As(err, &usage) || usage.code != tc.code || out.Len() != 0 {
			t.Errorf("%v: %v %q", tc.args, err, out.String())
		}
	}
	if after := stateSnapshot(t, root); !equalSnapshots(before, after) {
		t.Fatalf("a refused option changed the state:\n%v\n%v", before, after)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.commands != 0 {
		t.Fatal("a refused option changed Serve", adapter.commands)
	}
}

// Without an answer and without a terminal, configure shows the saved choice in the words of
// the install question and, on separate lines, what the daemon runs now (#685, C1).
func TestRemoteConfigureShowsSavedAndRunningChoice(t *testing.T) {
	ctx := context.Background()
	adapter := &remoteCLIAdapter{}
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	show := func(want ...string) {
		t.Helper()
		before := stateSnapshot(t, root)
		var out bytes.Buffer
		if err := run(ctx, []string{"remote", "configure", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		want = append(want, "To change it, run koinon remote configure at a terminal, or give --choice local, tailnet or network.")
		if strings.Join(lines, "\n") != strings.Join(want, "\n") {
			t.Fatalf("got:\n%s\nwant:\n%s", out.String(), strings.Join(want, "\n"))
		}
		if after := stateSnapshot(t, root); !equalSnapshots(before, after) {
			t.Fatal("showing the choice changed the state")
		}
	}
	show("Saved choice: 1. Only on this computer (no answer saved yet)",
		"Running now, network address: none",
		"Running now, tailnet: off")
	if err := core.WriteDashboardLocal(root); err != nil {
		t.Fatal(err)
	}
	show("Saved choice: 1. Only on this computer",
		"Running now, network address: none",
		"Running now, tailnet: off")

	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"remote", "enable", "--https-port", "8444", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	show("Saved choice: 2. On this computer and your tailnet, through Tailscale Serve (HTTPS port 8444)",
		"Running now, network address: none",
		"Running now, tailnet: open at https://board.example.ts.net:8444/dashboard/")

	// A network address saved for the next start does not run yet: the two lines differ.
	network := &core.DashboardAddress{Host: "192.0.2.10", IP: "192.0.2.10", Port: 8443}
	if err := core.WriteDashboardAddress(root, network); err != nil {
		t.Fatal(err)
	}
	show("Saved choice: a network address at 192.0.2.10:8443 and your tailnet through Tailscale Serve (HTTPS port 8444), which is not one of the three answers",
		"Running now, network address: none",
		"Running now, tailnet: open at https://board.example.ts.net:8444/dashboard/")
	if err := run(ctx, []string{"remote", "disable", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	show("Saved choice: 3. Also on a network address, over HTTPS, at 192.0.2.10:8443",
		"Running now, network address: none",
		"Running now, tailnet: off")

	out.Reset()
	if err := run(ctx, []string{"remote", "configure", "--json", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		OK      bool                     `json:"ok"`
		Current install.DashboardCurrent `json:"current"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil || !reply.OK || reply.Current.Choice != install.DashboardNetwork ||
		reply.Current.Network == nil || *reply.Current.Network != *network || reply.Current.HTTPSPort != 8444 ||
		reply.Current.Runtime == nil || !reply.Current.Runtime.SecretSet || reply.Current.Runtime.Remote == nil || reply.Current.Runtime.Remote.Configured {
		t.Fatal(out.String(), err)
	}

	// A daemon that does not answer leaves the runtime unknown; the saved choice still shows.
	d.Close()
	out.Reset()
	if err := run(ctx, []string{"remote", "configure", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if lines[0] != "Saved choice: 3. Also on a network address, over HTTPS, at 192.0.2.10:8443" || !strings.HasPrefix(lines[1], "Running now: unknown (daemon_unavailable: ") {
		t.Fatal(out.String())
	}
}

// A saved file that cannot be read is an error, not a guessed choice.
func TestRemoteConfigureRefusesUnreadableChoice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dashboard-local.json"), []byte("{\"choice\":\"lan\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run(context.Background(), []string{"remote", "configure", "--state-dir", root, "--address", "127.0.0.1:1"}, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "dashboard_configuration_invalid") || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
}

// With --choice, configure applies the answer, reports each step with its effect on sign-ins
// and then the choice; the same answer again changes nothing (#685, C3 to C5).
func TestRemoteConfigureAppliesChoice(t *testing.T) {
	ctx := context.Background()
	adapter := &remoteCLIAdapter{}
	dir := t.TempDir()
	root := filepath.Join(dir, "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	prefix := filepath.Join(dir, "prefix")
	if err := os.MkdirAll(filepath.Dir(install.Binary(prefix)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install.Binary(prefix), []byte("synthetic binary"), 0700); err != nil {
		t.Fatal(err)
	}
	configure := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, append([]string{"remote", "configure", "--prefix", prefix, "--state-dir", root, "--address", address}, args...), strings.NewReader(""), &out)
		return out.String(), err
	}
	// The tailnet answer needs the maintainer secret; without one and without a terminal,
	// nothing changes.
	if out, err := configure("--choice", "tailnet"); err == nil || !strings.Contains(err.Error(), "remote_secret_required") || out != "" {
		t.Fatal(out, err)
	}
	var secret bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &secret); err != nil {
		t.Fatal(err)
	}
	out, err := configure("--choice", "tailnet", "--https-port", "8444")
	if err != nil || out != "Done: save_choice\nDone: remote_enable\n"+
		"Saved choice: 2. On this computer and your tailnet, through Tailscale Serve (HTTPS port 8444)\n"+
		"Running now, network address: none\n"+
		"Running now, tailnet: open at https://board.example.ts.net:8444/dashboard/\n" {
		t.Fatalf("%q %v", out, err)
	}
	if out, err := configure("--choice", "tailnet"); err != nil || !strings.HasPrefix(out, "No change: the dashboard already opens this way.\nSaved choice: 2.") {
		t.Fatalf("%q %v", out, err)
	}
	out, err = configure("--choice", "local", "--json")
	var reply struct {
		OK     bool                    `json:"ok"`
		Change install.DashboardChange `json:"change"`
	}
	if err != nil || json.Unmarshal([]byte(out), &reply) != nil || !reply.OK || !reply.Change.Changed || len(reply.Change.Steps) != 1 ||
		reply.Change.Steps[0] != (install.DashboardStep{Step: "remote_disable", Done: true, Effect: "Remote sign-ins on your tailnet end."}) ||
		reply.Change.After == nil || reply.Change.After.Choice != install.DashboardLocal || reply.Change.After.Runtime.Remote.Configured {
		t.Fatalf("%s %v", out, err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.commands != 2 {
		t.Fatal("Serve writes", adapter.commands)
	}
}
