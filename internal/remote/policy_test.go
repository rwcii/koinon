package remote

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestTailnetIPsKeepsOnlyTailscaleAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"both families, IPv4 first", `["fd7a:115c:a1e0::1","100.64.0.1"]`, []string{"100.64.0.1", "fd7a:115c:a1e0::1"}},
		{"IPv4 only", `["100.100.1.2"]`, []string{"100.100.1.2"}},
		{"IPv6 only", `["fd7a:115c:a1e0:ab12::5"]`, []string{"fd7a:115c:a1e0:ab12::5"}},
		{"foreign addresses", `["192.0.2.1","2001:db8::1","100.128.0.1","fd7a:115c:a1e1::1","::ffff:100.64.0.1"]`, nil},
		{"malformed entries", `["not-an-ip",42,null,{"a":1},"100.64.0.1%eth0","100.64.0.2"]`, []string{"100.64.0.2"}},
		{"duplicates", `["100.64.0.3","100.64.0.3"]`, []string{"100.64.0.3"}},
		{"not a list", `"100.64.0.1"`, nil},
		{"missing", ``, nil},
		{"null", `null`, nil},
		{"empty", `[]`, nil},
	} {
		if got := tailnetIPs(json.RawMessage(tc.raw)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPolicyRuleNamesOnlyThePresentAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []string
		want string
	}{
		{"both", []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["100.64.0.1:443","[fd7a:115c:a1e0::1]:443"]}`},
		{"IPv4 only", []string{"100.64.0.1"}, `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["100.64.0.1:443"]}`},
		{"IPv6 only", []string{"fd7a:115c:a1e0::1"}, `{"action":"accept","src":["autogroup:member"],"proto":"tcp","dst":["[fd7a:115c:a1e0::1]:443"]}`},
		{"none", nil, ""},
	} {
		if got := PolicyRule(tc.ips); got != tc.want {
			t.Errorf("%s: %s", tc.name, got)
		}
	}
}

// Malformed address data never makes an otherwise readable status unreadable.
func TestInspectToleratesBadTailscaleIPs(t *testing.T) {
	for _, ips := range []any{"not a list", 42, []any{nil, true}, nil} {
		raw, _ := json.Marshal(map[string]any{"BackendState": "Running", "Self": map[string]any{"Online": true, "DNSName": "host.example.ts.net.", "TailscaleIPs": ips}, "CertDomains": []string{"host.example.ts.net"}})
		f := &fakeAdapter{statusRaw: raw}
		s, err := (Manager{Adapter: f}).Inspect(context.Background())
		if err != nil || s.Ready() != nil || len(s.TailscaleIPs) != 0 {
			t.Fatalf("%v: %+v %v", ips, s, err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"BackendState": "Running", "Self": map[string]any{"Online": true, "DNSName": "host.example.ts.net.", "TailscaleIPs": []string{"100.64.0.1", "fd7a:115c:a1e0::1"}}, "CertDomains": []string{"host.example.ts.net"}})
	s, err := (Manager{Adapter: &fakeAdapter{statusRaw: raw}}).Inspect(context.Background())
	if err != nil || !reflect.DeepEqual(s.TailscaleIPs, []string{"100.64.0.1", "fd7a:115c:a1e0::1"}) {
		t.Fatalf("%+v %v", s, err)
	}
}
