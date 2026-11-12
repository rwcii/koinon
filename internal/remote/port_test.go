package remote

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

var portMapping = Mapping{Origin: "https://host.example.ts.net:8443", Target: "http://127.0.0.1:43210"}

// #665: an origin names its HTTPS port unless it is 443, in one canonical form.
func TestOriginPorts(t *testing.T) {
	for _, origin := range []string{"https://host.example.ts.net", "https://host.example.ts.net:8443", "https://host.example.ts.net:1", "https://host.example.ts.net:10000", "https://host.example.ts.net:65535"} {
		if !ValidOrigin(origin) || ValidateMapping(Mapping{Origin: origin, Target: "http://127.0.0.1:1"}) != nil {
			t.Fatalf("refused %q", origin)
		}
	}
	for _, origin := range []string{"https://host.example.ts.net:443", "https://host.example.ts.net:0", "https://host.example.ts.net:65536", "https://host.example.ts.net:08443", "https://host.example.ts.net:+8443", "https://host.example.ts.net:", "https://host.example.ts.net:8443/", "https://HOST.example.ts.net:8443", "https://host.example.ts.net:8443:1", "https://host.ts.net:8443"} {
		if ValidOrigin(origin) {
			t.Fatalf("accepted %q", origin)
		}
	}
	if portMapping.Port() != 8443 || testMapping.Port() != 443 || portMapping.HostOrigin() != "https://host.example.ts.net" || testMapping.HostOrigin() != testMapping.Origin {
		t.Fatalf("port %d %d, host %q %q", portMapping.Port(), testMapping.Port(), portMapping.HostOrigin(), testMapping.HostOrigin())
	}
	if OriginAt("https://host.example.ts.net", 443) != "https://host.example.ts.net" || OriginAt("https://host.example.ts.net", 8443) != portMapping.Origin {
		t.Fatal("OriginAt")
	}
	for _, n := range []int{0, -1, 65536} {
		if ValidHTTPSPort(n) {
			t.Fatalf("port %d", n)
		}
	}
}

// A mapping on 8443 writes and removes only HTTPS port 8443; the host's mappings on 443,
// including its own root, stay. The host's Tailscale identity is its name without the port.
func TestEnsureAndRemoveOnAnotherPort(t *testing.T) {
	f := newFake()
	f.config = ServeConfig{
		TCP: map[string]TCPConfig{"443": {HTTPS: true}},
		Web: map[string]WebConfig{"host.example.ts.net:443": {Handlers: map[string]Handler{"/": {Proxy: "http://127.0.0.1:8080"}, "/app": {Proxy: "http://127.0.0.1:9000"}}}},
		// A foreground mount and Funnel on 443 do not block 8443.
		Foreground:  map[string]*ServeConfig{"synthetic": {TCP: map[string]TCPConfig{"443": {HTTPS: true}}}},
		AllowFunnel: map[string]bool{"host.example.ts.net:443": true},
	}
	before := f.config.Web["host.example.ts.net:443"]
	m := Manager{Adapter: f}
	if err := m.Ensure(context.Background(), portMapping, false); err != nil {
		t.Fatal(err)
	}
	if w := f.writes(); len(w) != 1 || !reflect.DeepEqual(w[0], []string{"serve", "--bg", "--https=8443", "--set-path=/", portMapping.Target}) {
		t.Fatalf("writes %v", w)
	}
	s, err := m.Inspect(context.Background())
	if err != nil || !s.MappingPresent(portMapping) || s.MappingPresent(testMapping) || s.Origin != "https://host.example.ts.net" {
		t.Fatalf("after ensure: %+v %v", s, err)
	}
	if err := m.Remove(context.Background(), portMapping); err != nil {
		t.Fatal(err)
	}
	if w := f.writes(); len(w) != 2 || !reflect.DeepEqual(w[1], []string{"serve", "--bg", "--https=8443", "--set-path=/", "off"}) {
		t.Fatalf("writes %v", w)
	}
	if !reflect.DeepEqual(f.config.Web["host.example.ts.net:443"], before) || !f.config.TCP["443"].HTTPS || !f.config.AllowFunnel["host.example.ts.net:443"] {
		t.Fatalf("443 changed: %+v", f.config)
	}
	if _, ok := f.config.Web["host.example.ts.net:8443"].Handlers["/"]; ok {
		t.Fatal("8443 root stayed")
	}
}

// Another use of the chosen port refuses, and the refusal names that port.
func TestConflictOnTheChosenPort(t *testing.T) {
	for name, change := range map[string]func(*fakeAdapter){
		"tcp":  func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"8443": {TCPForward: "127.0.0.1:23000"}} },
		"http": func(f *fakeAdapter) { f.config.TCP = map[string]TCPConfig{"8443": {HTTP: true}} },
		"root": func(f *fakeAdapter) {
			f.config.Web = map[string]WebConfig{"host.example.ts.net:8443": {Handlers: map[string]Handler{"/": {Proxy: "http://127.0.0.1:9000"}}}}
		},
		"dashboard": func(f *fakeAdapter) {
			f.config.Web = map[string]WebConfig{"host.example.ts.net:8443": {Handlers: map[string]Handler{"/dashboard/": {Proxy: "http://127.0.0.1:9000"}}}}
		},
		"foreground": func(f *fakeAdapter) {
			f.config.Foreground = map[string]*ServeConfig{"s": {TCP: map[string]TCPConfig{"8443": {HTTPS: true}}}}
		},
		"funnel": func(f *fakeAdapter) { f.config.AllowFunnel = map[string]bool{"host.example.ts.net:8443": true} },
	} {
		f := newFake()
		change(f)
		err := Manager{Adapter: f}.Ensure(context.Background(), portMapping, false)
		if code(err) != "tailscale_serve_conflict" || !strings.Contains(err.Error(), "HTTPS port 8443") || len(f.writes()) != 0 {
			t.Fatalf("%s: %v %v", name, err, f.writes())
		}
	}
	// Another host name is a changed identity, also on another port.
	f := newFake()
	f.dns = "other.example.ts.net."
	if err := (Manager{Adapter: f}).Ensure(context.Background(), portMapping, false); code(err) != "tailscale_identity_changed" {
		t.Fatalf("identity: %v", err)
	}
}

func TestPolicyRuleNamesThePort(t *testing.T) {
	want := `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["100.64.0.1:8443","[fd7a:115c:a1e0::1]:8443"]}`
	if got := PolicyRule([]string{"100.64.0.1", "fd7a:115c:a1e0::1"}, 8443); got != want {
		t.Fatalf("rule %s", got)
	}
}
