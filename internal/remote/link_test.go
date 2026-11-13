package remote

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

var testTCP = TCPMapping{Port: 47672, Target: "127.0.0.1:43211"}

func TestTCPMappingEnsureAndRemove(t *testing.T) {
	f := newFake()
	f.root(Handler{Proxy: testMapping.Target})
	f.config.TCP = map[string]TCPConfig{"443": {HTTPS: true}}
	m := Manager{Adapter: f}
	if err := m.EnsureTCP(context.Background(), testTCP, false); err != nil {
		t.Fatal(err)
	}
	if got := f.config.TCP["47672"]; got != (TCPConfig{TCPForward: testTCP.Target}) {
		t.Fatalf("mapping %+v", got)
	}
	// Idempotent for the owned mapping; an unowned identical forward is not adopted.
	if err := m.EnsureTCP(context.Background(), testTCP, true); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsureTCP(context.Background(), testTCP, false); code(err) != "tailscale_serve_conflict" {
		t.Fatalf("unowned identical: %v", err)
	}
	if n := len(f.writes()); n != 1 {
		t.Fatalf("writes %d: %v", n, f.writes())
	}
	if err := m.RemoveTCP(context.Background(), testTCP); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.config.TCP["47672"]; ok {
		t.Fatal("mapping kept")
	}
	// The dashboard's mappings are untouched.
	if f.config.TCP["443"] != (TCPConfig{HTTPS: true}) || f.config.Web[testMapping.hostPort()].Handlers["/"].Proxy != testMapping.Target {
		t.Fatalf("dashboard changed: %+v %+v", f.config.TCP, f.config.Web)
	}
	// Removing an absent mapping changes nothing.
	before := len(f.writes())
	if err := m.RemoveTCP(context.Background(), testTCP); err != nil || len(f.writes()) != before {
		t.Fatalf("absent remove: %v %d", err, len(f.writes()))
	}
}

func TestTCPMappingConflictsNeverMutate(t *testing.T) {
	cases := map[string]func(*fakeAdapter){
		"other target": func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"47672": {TCPForward: "127.0.0.1:1"}} },
		"https":        func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"47672": {HTTPS: true}} },
		"terminate": func(f *fakeAdapter) {
			f.config.TCP = map[string]TCPConfig{"47672": {TCPForward: testTCP.Target, TerminateTLS: "host.example.ts.net"}}
		},
		"web": func(f *fakeAdapter) {
			f.config.Web = map[string]WebConfig{"host.example.ts.net:47672": {Handlers: map[string]Handler{"/": {Proxy: "http://127.0.0.1:2"}}}}
		},
		"funnel": func(f *fakeAdapter) { f.config.AllowFunnel = map[string]bool{"host.example.ts.net:47672": true} },
		"foreground": func(f *fakeAdapter) {
			f.config.Foreground = map[string]*ServeConfig{"x": {TCP: map[string]TCPConfig{"47672": {TCPForward: testTCP.Target}}}}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake()
			setup(f)
			m := Manager{Adapter: f}
			if err := m.EnsureTCP(context.Background(), testTCP, true); code(err) != "tailscale_serve_conflict" {
				t.Fatalf("ensure: %v", err)
			}
			// Remove reports a foreign use of the port; with no background forward on the
			// port there is nothing of ours to remove.
			_, background := f.config.TCP["47672"]
			if err := m.RemoveTCP(context.Background(), testTCP); background && code(err) != "tailscale_serve_conflict" || !background && err != nil {
				t.Fatalf("remove: %v", err)
			}
			if w := f.writes(); len(w) != 0 {
				t.Fatalf("mutated: %v", w)
			}
		})
	}
}

func TestTCPMappingNeedsReadback(t *testing.T) {
	f := newFake()
	f.noApply = true
	if err := (Manager{Adapter: f}).EnsureTCP(context.Background(), testTCP, false); code(err) != "tailscale_serve_not_restored" {
		t.Fatal(err)
	}
	f = newFake()
	f.config.TCP = map[string]TCPConfig{"47672": {TCPForward: testTCP.Target}}
	f.noApply = true
	if err := (Manager{Adapter: f}).RemoveTCP(context.Background(), testTCP); code(err) != "tailscale_serve_remove_unverified" {
		t.Fatal(err)
	}
	f = newFake()
	f.state = "Stopped"
	if err := (Manager{Adapter: f}).EnsureTCP(context.Background(), testTCP, false); code(err) != "tailscale_disconnected" || len(f.writes()) != 0 {
		t.Fatal(err)
	}
}

func TestTCPMappingValidation(t *testing.T) {
	for _, m := range []TCPMapping{{0, "127.0.0.1:1"}, {70000, "127.0.0.1:1"}, {1, "0.0.0.0:1"}, {1, "127.0.0.1:0"}, {1, "127.0.0.1:01"}, {1, "localhost:1"}, {1, "[::1]:1"}} {
		if ValidateTCPMapping(m) == nil {
			t.Errorf("accepted %+v", m)
		}
	}
	if ValidateTCPMapping(testTCP) != nil {
		t.Fatal("refused valid")
	}
}

func TestPeerAddresses(t *testing.T) {
	f := newFake()
	f.peers = map[string]any{
		"k1": map[string]any{"DNSName": "Laptop.Example.ts.net.", "TailscaleIPs": []string{"fd7a:115c:a1e0::1", "100.64.0.2", "8.8.8.8"}},
		"k2": map[string]any{"DNSName": "dup.example.ts.net.", "TailscaleIPs": []string{"100.64.0.3"}},
		"k3": map[string]any{"DNSName": "dup.example.ts.net.", "TailscaleIPs": []string{"100.64.0.4"}},
		"k4": map[string]any{"DNSName": "v6only.example.ts.net.", "TailscaleIPs": []string{"fd7a:115c:a1e0::5"}},
		"k5": map[string]any{"DNSName": "bad", "TailscaleIPs": []string{"100.64.0.6"}},
		"k6": "malformed",
	}
	s, err := (Manager{Adapter: f}).Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PeerAddresses("laptop.example.ts.net"); !reflect.DeepEqual(got, []string{"100.64.0.2", "fd7a:115c:a1e0::1"}) {
		t.Fatalf("laptop %v", got)
	}
	if got := s.PeerAddresses("LAPTOP.example.ts.net."); len(got) != 2 {
		t.Fatalf("normalized %v", got)
	}
	for _, name := range []string{"dup.example.ts.net", "laptop", "example.ts.net", "aptop.example.ts.net", ""} {
		if got := s.PeerAddresses(name); got != nil {
			t.Fatalf("%q: %v", name, got)
		}
	}
	if got := s.PeerAddresses("v6only.example.ts.net"); !reflect.DeepEqual(got, []string{"fd7a:115c:a1e0::5"}) {
		t.Fatalf("v6 %v", got)
	}
	// A malformed peer field never makes the status unreadable.
	raw, _ := json.Marshal(map[string]any{"BackendState": "Running", "Self": map[string]any{"Online": true, "DNSName": "host.example.ts.net."}, "CertDomains": []string{"host.example.ts.net"}, "Peer": []int{1}})
	f.statusRaw = raw
	if s, err = (Manager{Adapter: f}).Inspect(context.Background()); err != nil || s.Peers != nil || s.Ready() != nil {
		t.Fatalf("malformed: %v %v", err, s.Peers)
	}
}
