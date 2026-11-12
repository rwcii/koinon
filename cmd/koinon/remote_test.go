package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

type remoteCLIAdapter struct {
	mu       sync.Mutex
	target   string
	commands int
}

func (a *remoteCLIAdapter) Run(_ context.Context, args ...string) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(args) == 2 && args[0] == "status" {
		return []byte(`{"BackendState":"Running","Self":{"Online":true,"DNSName":"board.example.ts.net."},"CertDomains":["board.example.ts.net"]}`), nil
	}
	if len(args) == 3 && args[1] == "status" {
		handlers := map[string]any{}
		if a.target != "" {
			handlers["/"] = map[string]string{"Proxy": a.target}
		}
		return json.Marshal(map[string]any{"TCP": map[string]any{"443": map[string]bool{"HTTPS": true}}, "Web": map[string]any{"board.example.ts.net:443": map[string]any{"Handlers": handlers}}})
	}
	if len(args) == 5 && args[3] == "--set-path=/" {
		a.commands++
		a.target = args[4]
		if a.target == "off" {
			a.target = ""
		}
		return nil, nil
	}
	return nil, errors.New("unexpected command")
}

func TestRemoteCommand(t *testing.T) {
	ctx := context.Background()
	adapter := &remoteCLIAdapter{}
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	var out bytes.Buffer
	args := func(op string) []string { return []string{"remote", op, "--state-dir", root, "--address", address} }
	// Missing maintainer secret is actionable; it never appears in arguments or QR.
	if err := run(ctx, args("enable"), strings.NewReader(""), &out); err == nil || !strings.Contains(err.Error(), "maintainer secret") {
		t.Fatal(out.String(), err)
	}
	var secretOut bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &secretOut); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"enable", "enable", "status"} {
		out.Reset()
		if err := run(ctx, args(op), strings.NewReader(""), &out); err != nil {
			t.Fatal(op, err)
		}
		text := out.String()
		if !strings.Contains(text, "https://board.example.ts.net/dashboard/") || !strings.Contains(text, "unverified") || strings.Contains(text, "synthetic-long-secret") {
			t.Fatal(op, text)
		}
	}
	out.Reset()
	if err := run(ctx, append(args("status"), "--json"), strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	var status core.RemoteStatus
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || !status.Configured || status.Listener != "ready" {
		t.Fatal(out.String(), err)
	}
	for range 2 {
		out.Reset()
		if err := run(ctx, args("disable"), strings.NewReader(""), &out); err != nil {
			t.Fatal(err)
		}
	}
	adapter.mu.Lock()
	n := adapter.commands
	adapter.mu.Unlock()
	if n != 2 {
		t.Fatal("non-idempotent mutations", n)
	}
	// A dashboard browser cannot administer the transport via the bearer-only route.
	r, _ := http.NewRequest("POST", "http://"+address+"/v1/remote/enable", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://"+address)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
}

func TestRemoteQRURLOnly(t *testing.T) {
	if qr, err := remoteQR("https://board.example.ts.net/dashboard/"); err != nil || !strings.Contains(qr, "\n") {
		t.Fatal(qr, err)
	}
	for _, link := range []string{"https://user:secret@board.example.ts.net/dashboard/", "https://board.example.ts.net/dashboard/?token=secret", "http://board.example.ts.net/dashboard/", "https://board.example.ts.net/dashboard/#secret", "https://evil.example/dashboard/"} {
		if _, err := remoteQR(link); err == nil {
			t.Errorf("encoded unsafe %q", link)
		}
	}
}

func TestRemoteStatusUnavailableDaemon(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "state")
	adapter := &remoteCLIAdapter{}
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	var out bytes.Buffer
	if err := run(ctx, []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := run(ctx, []string{"remote", "enable", "--state-dir", root, "--address", address}, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	status, err := remoteOfflineStatus(ctx, root, adapter)
	if err != nil || !status.Configured || status.URL != "https://board.example.ts.net/dashboard/" || status.Listener != "unknown" || !strings.Contains(status.Error, "Daemon unavailable") {
		t.Fatal(status, err)
	}
	if err := core.CleanupRemote(ctx, root, adapter); err != nil {
		t.Fatal(err)
	}
	off, err := remoteOfflineStatus(ctx, root, adapter)
	if err != nil || off.Configured || off.Serve != "off" {
		t.Fatal("disabled offline status", off, err)
	}
	adapter.mu.Lock()
	n := adapter.commands
	adapter.mu.Unlock()
	if n != 2 {
		t.Fatal("status changed mapping after graceful shutdown cleanup", n)
	}
}

// A stopped daemon makes enable and disable daemon_unavailable, not daemon_error, also when
// its state directory still holds the secret.
func TestRemoteStoppedDaemonIsUnavailable(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: &remoteCLIAdapter{}, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"enable", "disable"} {
		err := run(ctx, []string{"remote", action, "--state-dir", root, "--address", address}, strings.NewReader(""), &bytes.Buffer{})
		if code := errorReport(err)["code"]; code != "daemon_unavailable" {
			t.Fatalf("%s: code %v, error %v", action, code, err)
		}
	}
	refused := remoteCallError("enable", core.RefusedError{Code: "remote_secret_required"})
	if code := errorReport(refused)["code"]; code != "remote_secret_required" {
		t.Fatal("refusal code lost", code)
	}
}
