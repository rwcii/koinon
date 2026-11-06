package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

func TestDashboardCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	var opened [][]string
	var openErr error
	saved := openBrowser
	openBrowser = func(_ context.Context, argv []string) error { opened = append(opened, argv); return openErr }
	defer func() { openBrowser = saved }()
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("DISPLAY", ":0")
	link := regexp.MustCompile(`^http://` + regexp.QuoteMeta(address) + `/dashboard/login\?token=[0-9a-f]{64}$`)
	run1 := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(context.Background(), append([]string{"dashboard", "--state-dir", root, "--address", address}, args...), nil, &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	out := run1()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !link.MatchString(lines[0]) || !strings.Contains(out, "within 60 seconds") {
		t.Fatalf("output: %q", out)
	}
	want := map[string]string{"linux": "xdg-open", "darwin": "/usr/bin/open"}[runtime.GOOS]
	if len(opened) != 1 || len(opened[0]) != 2 || opened[0][0] != want || opened[0][1] != lines[0] {
		t.Fatalf("opener: %q", opened)
	}
	// The printed link logs in once.
	client := http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for i, status := range []int{http.StatusSeeOther, http.StatusUnauthorized} {
		response, err := client.Get(lines[0])
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("use %d: %d", i, response.StatusCode)
		}
	}
	// --no-open prints only; a failed opener is reported, not fatal; SSH means no browser.
	if out := run1("--no-open"); len(opened) != 1 || !link.MatchString(strings.Split(out, "\n")[0]) {
		t.Fatalf("no-open: %q %q", out, opened)
	}
	openErr = errors.New("synthetic failure")
	if out := run1(); !strings.Contains(out, "did not open") {
		t.Fatalf("open failure: %q", out)
	}
	t.Setenv("SSH_CONNECTION", "10.0.0.1 1 10.0.0.2 22")
	opened = nil
	if out := run1(); len(opened) != 0 || !strings.Contains(out, "No browser is available") {
		t.Fatalf("ssh: %q %q", out, opened)
	}
	var out2 bytes.Buffer
	if err := run(context.Background(), []string{"dashboard", "--state-dir", root, "--address", address, "extra"}, nil, &out2); err == nil {
		t.Fatal("extra argument accepted")
	}
}

func TestDashboardCommandNetworkDetails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("ab", 32)
	if err := os.WriteFile(filepath.Join(root, "secret"), []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("A", 64)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/dashboard/links" || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		network := core.DashboardNetworkStatus{URL: "https://board.example.test:8443/dashboard/", Code: "running", Fingerprint: fingerprint}
		if requests.Add(1) > 1 {
			network.Code = "dashboard_address_unavailable"
			network.Error = "synthetic bind failure"
			network.Step = "Run koinon install to retry."
		}
		json.NewEncoder(w).Encode(map[string]any{"path": "/dashboard/login?token=" + strings.Repeat("cd", 32), "expires_in": 60,
			"dashboard_network": network})
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"dashboard", "--state-dir", root, "--address", strings.TrimPrefix(server.URL, "http://"), "--no-open"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{server.URL + "/dashboard/login?token=", "Network dashboard: https://board.example.test:8443/dashboard/", "Certificate SHA-256: " + fingerprint, "Sign in there with the maintainer secret."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
	out.Reset()
	if err := run(context.Background(), []string{"dashboard", "--state-dir", root, "--address", strings.TrimPrefix(server.URL, "http://"), "--no-open"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "synthetic bind failure") || !strings.Contains(out.String(), "Run koinon install to retry.") || strings.Contains(out.String(), "Sign in there") {
		t.Fatalf("inactive network instructions: %s", out.String())
	}
}
