package core

import (
	"context"
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/remote"
)

// withIPs reports the host's Tailscale addresses in its status, as Tailscale does.
type withIPs struct {
	base *syntheticTailscale
	ips  any
}

func (w withIPs) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "status" {
		return json.Marshal(map[string]any{"BackendState": "Running", "Self": map[string]any{"Online": true, "DNSName": "host.example.ts.net.", "TailscaleIPs": w.ips}, "CertDomains": []string{"host.example.ts.net"}})
	}
	return w.base.Run(ctx, args...)
}

const policyRuleBoth = `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["100.64.0.1:443","[fd7a:115c:a1e0::1]:443"]}`

func TestRemoteStatusPolicyHintOnlyWhenServeIsReady(t *testing.T) {
	d, root, fake := remoteFixture(t)
	ctx := context.Background()
	use := func(ips any) {
		d.remote.mu.Lock()
		d.remote.manager = remote.Manager{Adapter: withIPs{fake, ips}}
		d.remote.mu.Unlock()
	}
	use([]string{"fd7a:115c:a1e0::1", "100.64.0.1"})
	if s := d.remoteStatus(ctx); s.PolicyHint != "" || s.PolicyRule != "" {
		t.Fatalf("hint before enable: %+v", s)
	}
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	s := d.remoteStatus(ctx)
	if s.Serve != "ready" || s.Phone != "unverified" || s.Error != "" || !strings.Contains(s.PolicyHint, "TCP 443") || !strings.Contains(s.PolicyHint, "every member of the tailnet") || s.PolicyRule != policyRuleBoth {
		t.Fatalf("ready status: %+v", s)
	}
	// One family gives that family only; no or bad data gives the general text and no rule.
	use([]string{"100.64.0.1"})
	if s := d.remoteStatus(ctx); s.PolicyRule != `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["100.64.0.1:443"]}` {
		t.Fatalf("IPv4 only: %+v", s)
	}
	for _, ips := range []any{nil, []string{}, "bad", []any{42, "192.0.2.1"}} {
		use(ips)
		s := d.remoteStatus(ctx)
		if s.Serve != "ready" || s.Listener != "ready" || s.Phone != "unverified" || s.Error != "" || s.PolicyHint != policyHint(443) || s.PolicyRule != "" {
			t.Fatalf("%v: %+v", ips, s)
		}
	}
	// The dashboard's connection details show the hint and the rule.
	use([]string{"fd7a:115c:a1e0::1", "100.64.0.1"})
	client := newActionClient(t, d, root)
	got := dashboardDo(t, d, "GET", "/dashboard/remote/status?fragment=1", client.cookie, nil, nil)
	body := html.UnescapeString(got.body)
	if got.status != 200 || !strings.Contains(body, policyHint(443)) || !strings.Contains(body, "<code>"+policyRuleBoth+"</code>") {
		t.Fatalf("dashboard: %d %s", got.status, got.body)
	}
	// A missing Serve mapping gives no hint: the next step is the mapping.
	fake.mu.Lock()
	fake.config = remote.ServeConfig{}
	fake.mu.Unlock()
	if s := d.remoteStatus(ctx); s.Serve != "missing" || s.PolicyHint != "" {
		t.Fatalf("missing mapping: %+v", s)
	}
}
