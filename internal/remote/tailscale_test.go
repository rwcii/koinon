package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var testMapping = Mapping{Origin: "https://host.example.ts.net", Target: "http://127.0.0.1:43210"}

// This fake changes only the addressed mount, like the installed CLI. All data
// is synthetic. No test executes Tailscale or reads the host's runtime state.
type fakeAdapter struct {
	state          string
	online         bool
	dns            string
	cert           bool
	config         ServeConfig
	calls          [][]string
	fail           string
	applyOnFailure bool
	noApply        bool
	statusRaw      []byte
	serveRaw       []byte
	peers          map[string]any
}

func newFake() *fakeAdapter {
	return &fakeAdapter{state: "Running", online: true, dns: "host.example.ts.net.", cert: true}
}
func (f *fakeAdapter) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if reflect.DeepEqual(args, []string{"status", "--json"}) {
		if f.statusRaw != nil {
			return f.statusRaw, nil
		}
		var domains []string
		if f.cert {
			domains = []string{strings.TrimSuffix(f.dns, ".")}
		}
		status := map[string]any{"BackendState": f.state, "Self": map[string]any{"Online": f.online, "DNSName": f.dns}, "CertDomains": domains}
		if f.peers != nil {
			status["Peer"] = f.peers
		}
		return json.Marshal(status)
	}
	if reflect.DeepEqual(args, []string{"serve", "status", "--json"}) {
		if f.serveRaw != nil {
			return f.serveRaw, nil
		}
		return json.Marshal(f.config)
	}
	if len(args) == 4 && args[0] == "serve" && args[1] == "--bg" && strings.HasPrefix(args[2], "--tcp=") && strings.HasPrefix(args[3], "tcp://") {
		if f.fail != "" && !f.applyOnFailure {
			return nil, errors.New(f.fail)
		}
		if !f.noApply {
			if f.config.TCP == nil {
				f.config.TCP = map[string]TCPConfig{}
			}
			f.config.TCP[strings.TrimPrefix(args[2], "--tcp=")] = TCPConfig{TCPForward: strings.TrimPrefix(args[3], "tcp://")}
		}
		if f.fail != "" {
			return nil, errors.New(f.fail)
		}
		return nil, nil
	}
	if len(args) == 3 && args[0] == "serve" && strings.HasPrefix(args[1], "--tcp=") && args[2] == "off" {
		if f.fail != "" {
			return nil, errors.New(f.fail)
		}
		if !f.noApply {
			delete(f.config.TCP, strings.TrimPrefix(args[1], "--tcp="))
		}
		return nil, nil
	}
	if len(args) != 5 || args[0] != "serve" || args[1] != "--bg" || !strings.HasPrefix(args[2], "--https=") || args[3] != "--set-path=/" {
		return nil, errors.New("unexpected command")
	}
	port := strings.TrimPrefix(args[2], "--https=")
	if f.fail != "" && !f.applyOnFailure {
		return nil, errors.New(f.fail)
	}
	if !f.noApply {
		hp := strings.TrimSuffix(f.dns, ".") + ":" + port
		if args[4] == "off" {
			w := f.config.Web[hp]
			delete(w.Handlers, "/")
			f.config.Web[hp] = w
		} else {
			if f.config.TCP == nil {
				f.config.TCP = map[string]TCPConfig{}
			}
			f.config.TCP[port] = TCPConfig{HTTPS: true}
			if f.config.Web == nil {
				f.config.Web = map[string]WebConfig{}
			}
			w := f.config.Web[hp]
			if w.Handlers == nil {
				w.Handlers = map[string]Handler{}
			}
			w.Handlers["/"] = Handler{Proxy: args[4]}
			f.config.Web[hp] = w
		}
	}
	if f.fail != "" {
		return nil, errors.New(f.fail)
	}
	return nil, nil
}
func (f *fakeAdapter) writes() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 1 && (c[1] == "--bg" || strings.HasPrefix(c[1], "--tcp=")) {
			out = append(out, c)
		}
	}
	return out
}
func (f *fakeAdapter) root(h Handler) {
	f.config.Web = map[string]WebConfig{testMapping.hostPort(): {Handlers: map[string]Handler{"/": h}}}
}
func code(err error) string {
	var d *Diagnostic
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func TestEnsureAndRemovePreserveUnrelatedMappings(t *testing.T) {
	f := newFake()
	f.config = ServeConfig{
		TCP: map[string]TCPConfig{"443": {HTTPS: true}, "8443": {HTTPS: true}},
		Web: map[string]WebConfig{
			testMapping.hostPort():     {Handlers: map[string]Handler{"/other/": {Proxy: "http://127.0.0.1:21000"}}},
			"host.example.ts.net:8443": {Handlers: map[string]Handler{"/": {Proxy: "http://127.0.0.1:22000"}}},
		},
		AllowFunnel: map[string]bool{"host.example.ts.net:8443": true},
	}
	m := Manager{Adapter: f}
	if err := m.Check(context.Background(), testMapping, false); err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != 0 {
		t.Fatal("preflight mutated Serve")
	}
	if err := m.Ensure(context.Background(), testMapping, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background(), testMapping, true); err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != 1 {
		t.Fatalf("repeat enable performed writes: %v", f.writes())
	}
	if err := m.Remove(context.Background(), testMapping); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(context.Background(), testMapping); err != nil {
		t.Fatal(err)
	}
	if got := f.writes(); len(got) != 2 || got[1][4] != "off" || got[1][3] != "--set-path=/" {
		t.Fatalf("unsafe/repeated remove: %v", got)
	}
	if got := f.config.Web[testMapping.hostPort()].Handlers["/other/"].Proxy; got != "http://127.0.0.1:21000" {
		t.Fatal("unrelated path changed")
	}
	if got := f.config.Web["host.example.ts.net:8443"].Handlers["/"].Proxy; got != "http://127.0.0.1:22000" || !f.config.AllowFunnel["host.example.ts.net:8443"] {
		t.Fatal("unrelated port changed")
	}
}

func TestConflictsNeverMutate(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeAdapter)
		owned bool
	}{
		{"unowned exact root", func(f *fakeAdapter) { f.root(Handler{Proxy: testMapping.Target}) }, false},
		{"other root", func(f *fakeAdapter) { f.root(Handler{Proxy: "http://127.0.0.1:22000"}) }, true},
		{"root text", func(f *fakeAdapter) { f.root(Handler{Text: "other"}) }, true},
		{"root directory", func(f *fakeAdapter) { f.root(Handler{Path: "/synthetic/files"}) }, true},
		{"ambiguous root", func(f *fakeAdapter) { f.root(Handler{Proxy: testMapping.Target, Text: "also"}) }, true},
		{"funnel", func(f *fakeAdapter) { f.config.AllowFunnel = map[string]bool{testMapping.hostPort(): true} }, true},
		{"tcp", func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"443": {TCPForward: "127.0.0.1:23000"}} }, true},
		{"http", func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"443": {HTTP: true}} }, true},
		{"foreground tcp", func(f *fakeAdapter) {
			f.config.Foreground = map[string]*ServeConfig{"synthetic": {TCP: map[string]TCPConfig{"443": {HTTPS: true}}}}
		}, true},
		{"foreground web", func(f *fakeAdapter) {
			f.config.Foreground = map[string]*ServeConfig{"synthetic": {Web: map[string]WebConfig{testMapping.hostPort(): {}}}}
		}, true},
		{"dashboard route", func(f *fakeAdapter) {
			f.config.Web = map[string]WebConfig{testMapping.hostPort(): {Handlers: map[string]Handler{"/dashboard/": {Proxy: "http://127.0.0.1:23000"}}}}
		}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			m := Manager{Adapter: f}
			before, _ := json.Marshal(f.config)
			if err := m.Check(context.Background(), testMapping, tt.owned); code(err) != "tailscale_serve_conflict" {
				t.Fatalf("check: %v", err)
			}
			if err := m.Ensure(context.Background(), testMapping, tt.owned); code(err) != "tailscale_serve_conflict" {
				t.Fatalf("ensure: %v", err)
			}
			after, _ := json.Marshal(f.config)
			if len(f.writes()) > 0 || !bytes.Equal(before, after) {
				t.Fatal("conflict changed configuration")
			}
		})
	}
}

func TestInterruptedEnableCanResumeOwnedIntent(t *testing.T) {
	f := newFake()
	f.fail = "synthetic account-private-details"
	f.applyOnFailure = true
	m := Manager{Adapter: f}
	err := m.Ensure(context.Background(), testMapping, false)
	if code(err) != "tailscale_serve_setup_failed" || strings.Contains(err.Error(), "account-private-details") {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
	f.fail = ""
	if err = m.Ensure(context.Background(), testMapping, true); err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != 1 {
		t.Fatal("resume replayed applied configuration")
	}
}
func TestFailedEnableRetriesMissingMapping(t *testing.T) {
	f := newFake()
	f.fail = "synthetic failure"
	m := Manager{Adapter: f}
	if err := m.Ensure(context.Background(), testMapping, false); err == nil {
		t.Fatal("expected failure")
	}
	f.fail = ""
	if err := m.Ensure(context.Background(), testMapping, true); err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != 2 {
		t.Fatal("did not restore missing mapping")
	}
}
func TestSuccessfulCommandNeedsReadback(t *testing.T) {
	f := newFake()
	f.noApply = true
	if err := (Manager{Adapter: f}).Ensure(context.Background(), testMapping, true); code(err) != "tailscale_serve_not_restored" {
		t.Fatalf("%v", err)
	}
	f.root(Handler{Proxy: testMapping.Target})
	if err := (Manager{Adapter: f}).Remove(context.Background(), testMapping); code(err) != "tailscale_serve_remove_unverified" {
		t.Fatalf("%v", err)
	}
}
func TestRemoveWorksStoppedAndPreservesChangedRoot(t *testing.T) {
	f := newFake()
	f.state = "Stopped"
	f.online = false
	f.cert = false
	f.root(Handler{Proxy: testMapping.Target})
	if err := (Manager{Adapter: f}).Remove(context.Background(), testMapping); err != nil {
		t.Fatal(err)
	}
	f.root(Handler{Proxy: "http://127.0.0.1:24000"})
	f.calls = nil
	if err := (Manager{Adapter: f}).Remove(context.Background(), testMapping); code(err) != "tailscale_serve_conflict" {
		t.Fatalf("%v", err)
	}
	if len(f.writes()) != 0 {
		t.Fatal("removed someone else's root")
	}
}
func TestRemoveFailureRetainsRecoverableMapping(t *testing.T) {
	f := newFake()
	f.root(Handler{Proxy: testMapping.Target})
	f.fail = "synthetic secret"
	m := Manager{Adapter: f}
	if err := m.Remove(context.Background(), testMapping); code(err) != "tailscale_serve_remove_failed" || strings.Contains(err.Error(), "synthetic secret") {
		t.Fatalf("%v", err)
	}
	f.fail = ""
	if err := m.Remove(context.Background(), testMapping); err != nil {
		t.Fatal(err)
	}
}
func TestPrerequisitesAndIdentity(t *testing.T) {
	cases := []struct {
		name, code string
		setup      func(*fakeAdapter)
	}{
		{"stopped", "tailscale_disconnected", func(f *fakeAdapter) { f.state = "Stopped"; f.online = false }},
		{"signed out", "tailscale_disconnected", func(f *fakeAdapter) { f.state = "NeedsLogin"; f.online = false }},
		{"offline", "tailscale_disconnected", func(f *fakeAdapter) { f.online = false }},
		{"HTTPS", "tailscale_https_required", func(f *fakeAdapter) { f.cert = false }},
		{"invalid name", "tailscale_name_unavailable", func(f *fakeAdapter) { f.dns = "localhost" }},
		{"new identity", "tailscale_identity_changed", func(f *fakeAdapter) { f.dns = "new.example.ts.net." }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			if err := (Manager{Adapter: f}).Ensure(context.Background(), testMapping, true); code(err) != tt.code {
				t.Fatalf("%v", err)
			}
			if len(f.writes()) != 0 {
				t.Fatal("prerequisite failure mutated Serve")
			}
		})
	}
}
func TestMalformedCommandOutputAndUnknownHandlerFields(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "{} {}", "not json", strings.Repeat("x", maxOutput+1)} {
		f := newFake()
		f.serveRaw = []byte(raw)
		if _, err := (Manager{Adapter: f}).Inspect(context.Background()); code(err) != "tailscale_serve_invalid" {
			t.Fatalf("input length %d: %v", len(raw), err)
		}
	}
	f := newFake()
	f.statusRaw = []byte("not json")
	if _, err := (Manager{Adapter: f}).Inspect(context.Background()); code(err) != "tailscale_status_invalid" {
		t.Fatal(err)
	}
	f = newFake()
	f.serveRaw = []byte(`{"Web":{"host.example.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:43210","AcceptAppCaps":["synthetic"]}}}}}`)
	if err := (Manager{Adapter: f}).Ensure(context.Background(), testMapping, true); code(err) != "tailscale_serve_conflict" {
		t.Fatal(err)
	}
}
func TestMappingValidation(t *testing.T) {
	if err := ValidateMapping(testMapping); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"http://host.example.ts.net", "https://host.example.ts.net/", "https://host.example.ts.net:443", "https://HOST.example.ts.net", "https://host.example.ts.net.evil", "https://user@host.example.ts.net", "https://host.example.ts.net?", "https://host.example.ts.net/#secret", "https://host.ts.net", "https://-host.example.ts.net"} {
		m := testMapping
		m.Origin = origin
		if ValidateMapping(m) == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
	for _, target := range []string{"http://localhost:43210", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:043210", "http://127.0.0.1:43210/", "http://127.0.0.1:43210?", "http://user@127.0.0.1:43210", "http://0.0.0.0:43210", "https://127.0.0.1:43210"} {
		m := testMapping
		m.Target = target
		if ValidateMapping(m) == nil {
			t.Fatalf("accepted %q", target)
		}
	}
}
func TestBoundedOutput(t *testing.T) {
	var b limitedBuffer
	p := bytes.Repeat([]byte("x"), maxOutput+1)
	if n, err := b.Write(p); n != len(p) || err != nil {
		t.Fatal(n, err)
	}
	if b.Len() != maxOutput || !b.overflow {
		t.Fatal("output limit not enforced")
	}
	if n, err := b.Write([]byte("again")); n != 5 || err != nil {
		t.Fatal(n, err)
	}
}

func TestExecUsesInstalledCommandWithoutLeakingStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tailscale")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '{\"BackendState\":\"Running\"}'\nprintf '%s' 'synthetic private account warning' >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	data, err := (Exec{}).Run(context.Background(), "status", "--json")
	if err != nil || string(data) != `{"BackendState":"Running"}` {
		t.Fatal(string(data), err)
	}
	if err = os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' 'synthetic private account failure' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = (Exec{}).Run(context.Background(), "serve", "status", "--json")
	if code(err) != "tailscale_command_failed" || strings.Contains(err.Error(), "private account failure") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (Exec{}).Run(ctx, "status", "--json")
	if code(err) != "tailscale_timeout" {
		t.Fatal(err)
	}
}

func TestRemoveNeverAddressesChangedIdentity(t *testing.T) {
	f := newFake()
	f.dns = "new.example.ts.net."
	f.root(Handler{Proxy: testMapping.Target})
	f.config.Web["new.example.ts.net:443"] = WebConfig{Handlers: map[string]Handler{"/": {Proxy: "http://127.0.0.1:25000"}}}
	m := Manager{Adapter: f}
	if err := m.Remove(context.Background(), testMapping); code(err) != "tailscale_identity_changed" {
		t.Fatal(err)
	}
	if len(f.writes()) != 0 {
		t.Fatal("addressed current identity during old-name cleanup")
	}
	delete(f.config.Web, testMapping.hostPort())
	if err := m.Remove(context.Background(), testMapping); err != nil {
		t.Fatal(err)
	}
	if len(f.writes()) != 0 {
		t.Fatal("absent mapping required a write")
	}
}
