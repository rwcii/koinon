package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/remote"
)

type syntheticTailscale struct {
	mu        sync.Mutex
	config    remote.ServeConfig
	mutations int
	calls     int
	fail      bool
	dns       string
	noHTTPS   bool
	noSelf    bool
}

func (s *syntheticTailscale) Run(_ context.Context, args ...string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(args) == 2 && args[0] == "status" {
		if s.noSelf {
			return []byte(`{"BackendState":"NeedsLogin","Self":null}`), nil
		}
		dns := s.dns
		if dns == "" {
			dns = "host.example.ts.net."
		}
		certs := []string{}
		if !s.noHTTPS {
			certs = append(certs, strings.TrimSuffix(dns, "."))
		}
		return json.Marshal(map[string]any{"BackendState": "Running", "Self": map[string]any{"Online": true, "DNSName": dns}, "CertDomains": certs})
	}
	if len(args) == 3 && args[0] == "serve" && args[1] == "status" {
		return json.Marshal(s.config)
	}
	s.mutations++
	if s.fail {
		return nil, errors.New("synthetic failure")
	}
	if len(args) != 5 || args[0] != "serve" || args[1] != "--bg" || args[2] != "--https=443" || args[3] != "--set-path=/" {
		return nil, errors.New("unexpected command")
	}
	if s.config.Web == nil {
		s.config.Web = map[string]remote.WebConfig{}
	}
	dns := strings.TrimSuffix(s.dns, ".")
	if dns == "" {
		dns = "host.example.ts.net"
	}
	key := dns + ":443"
	web := s.config.Web[key]
	if web.Handlers == nil {
		web.Handlers = map[string]remote.Handler{}
	}
	if args[4] == "off" {
		delete(web.Handlers, "/")
	} else {
		web.Handlers["/"] = remote.Handler{Proxy: args[4]}
	}
	s.config.Web[key] = web
	if s.config.TCP == nil {
		s.config.TCP = map[string]remote.TCPConfig{}
	}
	s.config.TCP["443"] = remote.TCPConfig{HTTPS: true}
	return nil, nil
}

func remoteFixture(t *testing.T) (*Daemon, string, *syntheticTailscale) {
	t.Helper()
	d, root := startTestDaemon(t)
	fake := &syntheticTailscale{}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: fake}
	d.remote.mu.Unlock()
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	return d, root, fake
}

func TestRemoteLifecycle(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := ReadRemoteConfig(root)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	if !first.Enabled || !first.Owned {
		t.Fatal(first)
	}
	fi, err := os.Stat(filepath.Join(root, "remote-dashboard.json"))
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatal(fi, err)
	}
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := ReadRemoteConfig(root)
	if *first != *again {
		t.Fatal("repeated enable changed identity", first, again)
	}
	fake.mu.Lock()
	mutations := fake.mutations
	fake.mu.Unlock()
	if mutations != 1 {
		t.Fatal("enable changed working mapping", mutations)
	}
	status := d.remoteStatus(ctx)
	if status.Listener != "ready" || !status.Configured || status.Phone != "unverified" || status.Error != "" {
		t.Fatalf("%+v", status)
	}
	// The actual listener is separate and never exposes agent API routes.
	r, _ := http.NewRequest("GET", first.Mapping.Target+"/v1/status", nil)
	r.Host = "host.example.ts.net"
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal(response.StatusCode)
	}
	id, _ := d.auth.start("remote:" + first.Mapping.Origin)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	_, stillForwarding := fake.config.Web["host.example.ts.net:443"].Handlers["/"]
	fake.mu.Unlock()
	if stillForwarding {
		t.Fatal("graceful shutdown retained forwarding to a released port")
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: fake, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.restoreRemote(ctx)
	restored, _ := ReadRemoteConfig(root)
	if *restored != *first {
		t.Fatal("restart identity changed")
	}
	if status := restarted.remoteStatus(ctx); status.Listener != "ready" {
		t.Fatal(status)
	}
	hash, _ := tokenHash(id)
	if _, ok := restarted.auth.sessions[hash]; ok {
		t.Fatal("restart retained session")
	}
	if err := restarted.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	off, _ := ReadRemoteConfig(root)
	if off.Enabled || off.Owned {
		t.Fatal("disable retained ownership", off)
	}
	if response := dashboardDo(t, restarted, "GET", "/dashboard/login", "", nil, nil); response.status != 200 {
		t.Fatal("local unavailable", response.status)
	}
	if err := restarted.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	reenabled, _ := ReadRemoteConfig(root)
	if reenabled.Mapping != first.Mapping {
		t.Fatal("reenable identity changed")
	}
}

func TestRemoteInterruptedSetupAndDisable(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	fake.fail = true
	if err := d.remoteEnable(ctx); err == nil {
		t.Fatal("expected setup failure")
	}
	c, _ := ReadRemoteConfig(root)
	if c == nil || !c.Enabled || !c.Owned {
		t.Fatal("lost ownership intent")
	}
	fake.fail = false
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := d.auth.start("remote:" + c.Mapping.Origin)
	fake.fail = true
	if err := d.remoteDisable(ctx); err == nil {
		t.Fatal("expected cleanup failure")
	}
	off, _ := ReadRemoteConfig(root)
	if off.Enabled || !off.Owned {
		t.Fatal("cleanup intent lost", off)
	}
	d.remote.mu.Lock()
	active := d.remote.server != nil
	d.remote.mu.Unlock()
	if active {
		t.Fatal("failed cleanup left listener open")
	}
	hash, _ := tokenHash(id)
	d.auth.mu.Lock()
	_, live := d.auth.sessions[hash]
	d.auth.mu.Unlock()
	if live {
		t.Fatal("disable retained session")
	}
	fake.fail = false
	d.restoreRemote(ctx)
	off, _ = ReadRemoteConfig(root)
	if off.Owned {
		t.Fatal("restart cleanup did not finish")
	}
}

func TestRemotePortConflictAndUnsafeConfig(t *testing.T) {
	d, root, _ := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ := ReadRemoteConfig(root)
	d.remote.mu.Lock()
	d.closeRemoteLocked()
	occupied, err := net.Listen("tcp4", strings.TrimPrefix(c.Mapping.Target, "http://"))
	d.remote.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d.restoreRemote(ctx)
	if s := d.remoteStatus(ctx); s.Listener != "unavailable" || !strings.Contains(s.Error, "remote_listener_unavailable") {
		t.Fatal(s)
	}
	fake := d.remote.manager.Adapter.(*syntheticTailscale)
	fake.mu.Lock()
	_, forwarding := fake.config.Web["host.example.ts.net:443"].Handlers["/"]
	fake.mu.Unlock()
	if forwarding {
		t.Fatal("port conflict retained forwarding to foreign process")
	}
	occupied.Close()
	d.restoreRemote(ctx)
	if s := d.remoteStatus(ctx); s.Listener != "ready" {
		t.Fatal(s)
	}
	path := filepath.Join(root, "remote-dashboard.json")
	os.Chmod(path, 0644)
	if _, err := ReadRemoteConfig(root); err == nil {
		t.Fatal("public config read")
	}
	if err := d.remoteEnable(ctx); err == nil {
		t.Fatal("unsafe config overwritten")
	}
	os.Chmod(path, 0600)
}

func TestRemoteUninstallCleanup(t *testing.T) {
	d, root, fake := remoteFixture(t)
	if err := d.remoteEnable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CleanupRemote(context.Background(), root, fake); err != nil {
		t.Fatal(err)
	}
	if err := CleanupRemote(context.Background(), root, fake); err != nil {
		t.Fatal(err)
	}
	c, _ := ReadRemoteConfig(root)
	if c.Enabled || c.Owned {
		t.Fatal(c)
	}
}

// Optional dependencies must not be invoked at all in an ordinary installation.
func TestRemoteDisabledHasNoDependencyCalls(t *testing.T) {
	d, root := startTestDaemon(t)
	fake := &syntheticTailscale{}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: fake}
	d.remote.mu.Unlock()
	d.restoreRemote(context.Background())
	if c, err := ReadRemoteConfig(root); c != nil || err != nil {
		t.Fatal(c, err)
	}
	if err := d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 0 {
		t.Fatal("disabled installation invoked dependency", calls)
	}
}

func TestRemoteUnsafeExposureEndsSessions(t *testing.T) {
	for _, kind := range []string{"identity", "funnel"} {
		t.Run(kind, func(t *testing.T) {
			d, _, fake := remoteFixture(t)
			ctx := context.Background()
			if err := d.remoteEnable(ctx); err != nil {
				t.Fatal(err)
			}
			id, _ := d.auth.start("remote:https://host.example.ts.net")
			fake.mu.Lock()
			if kind == "identity" {
				fake.dns = "renamed.example.ts.net."
				fake.noHTTPS = true
			} else {
				fake.config.AllowFunnel = map[string]bool{"host.example.ts.net:443": true}
			}
			fake.mu.Unlock()
			d.restoreRemote(ctx)
			d.remote.mu.Lock()
			active := d.remote.server != nil
			d.remote.mu.Unlock()
			if active {
				t.Fatal("unsafe surface remained active")
			}
			hash, _ := tokenHash(id)
			d.auth.mu.Lock()
			_, live := d.auth.sessions[hash]
			d.auth.mu.Unlock()
			if live {
				t.Fatal("unsafe surface retained sessions")
			}
			fake.mu.Lock()
			fake.dns = ""
			fake.noHTTPS = false
			fake.config.AllowFunnel = nil
			fake.mu.Unlock()
			if err := d.remoteDisable(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type missingRemoteAdapter struct{}

func (missingRemoteAdapter) Run(context.Context, ...string) ([]byte, error) {
	return nil, &remote.Diagnostic{Code: "tailscale_missing", Next: "install Tailscale from https://tailscale.com/download, then repeat uninstall"}
}

func TestRemoteUninstallMissingDependencyRecovers(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	err := CleanupRemote(ctx, root, missingRemoteAdapter{})
	if err == nil || !strings.Contains(err.Error(), "install Tailscale") {
		t.Fatal("missing recovery diagnostic", err)
	}
	pending, _ := ReadRemoteConfig(root)
	if pending.Enabled || !pending.Owned {
		t.Fatal("cleanup retry state lost", pending)
	}
	// Restoring the missing dependency and repeating cleanup needs no state-file editing.
	if err := CleanupRemote(ctx, root, fake); err != nil {
		t.Fatal(err)
	}
	done, _ := ReadRemoteConfig(root)
	if done.Enabled || done.Owned {
		t.Fatal("cleanup did not recover", done)
	}
}

func TestRemoteExplicitReenableCanRecoverOccupiedReleasedPort(t *testing.T) {
	d, root, _ := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	first, _ := ReadRemoteConfig(root)
	if err := d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp4", strings.TrimPrefix(first.Mapping.Target, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	next, _ := ReadRemoteConfig(root)
	if next.Mapping.Target == first.Mapping.Target || next.Mapping.Origin != first.Mapping.Origin {
		t.Fatal("explicit re-enable did not recover while preserving URL", first, next)
	}
}

func TestRemoteRenamedIdentityRecoveryWithoutJournalEditing(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.dns = "renamed.example.ts.net."
	fake.mu.Unlock()
	err := d.remoteDisable(ctx)
	if err == nil || !strings.Contains(err.Error(), "tailscale set --hostname=host") {
		t.Fatal("no exact safe name recovery step", err)
	}
	if err := d.remoteEnable(ctx); err == nil || !strings.Contains(err.Error(), "restore") {
		t.Fatal("name conflict not actionable", err)
	}
	// Simulate the documented external account action; no live Tailscale is changed.
	fake.mu.Lock()
	fake.dns = "host.example.ts.net."
	fake.mu.Unlock()
	if err := d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.dns = "renamed.example.ts.net."
	fake.mu.Unlock()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	c, _ := ReadRemoteConfig(root)
	if c.Mapping.Origin != "https://renamed.example.ts.net" {
		t.Fatal(c)
	}
}

func TestRemoteSignedOutIsNotIdentityChange(t *testing.T) {
	d, _, fake := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := d.auth.start("remote:https://host.example.ts.net")
	fake.mu.Lock()
	fake.noSelf = true
	fake.mu.Unlock()
	err := d.remoteEnable(ctx)
	if err == nil || !strings.Contains(err.Error(), "tailscale_disconnected") {
		t.Fatal(err)
	}
	hash, _ := tokenHash(id)
	d.auth.mu.Lock()
	_, live := d.auth.sessions[hash]
	d.auth.mu.Unlock()
	if !live {
		t.Fatal("signed-out inspection invalidated login as identity change")
	}
	fake.mu.Lock()
	fake.noSelf = false
	fake.mu.Unlock()
	if err := d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.noSelf = true
	fake.mu.Unlock()
	if err := d.remoteEnable(ctx); err == nil || !strings.Contains(err.Error(), "tailscale_disconnected") {
		t.Fatal("signed out after disable", err)
	}
	fake.mu.Lock()
	fake.noSelf = false
	fake.mu.Unlock()
}

func TestRemoteStatusUnavailableSecretIsUnknown(t *testing.T) {
	d, _, _ := remoteFixture(t)
	if err := d.remoteEnable(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	status := d.remoteStatus(ctx)
	if status.Listener != "unknown" || !strings.Contains(status.Error, "state unavailable") {
		t.Fatal("unavailable observation reported as missing secret", status)
	}
}
