package core

import (
	"context"
	"errors"
	"net"
	"testing"
)

// A bare link-local IPv6 address has no interface scope for a TCP bind. The current
// setting has no zone field; refuse it before saving an address that cannot start.
func TestDashboardNetworkLinkLocalNeedsZone(t *testing.T) {
	interfaces := func() ([]net.Addr, error) {
		var result []net.Addr
		for _, text := range []string{"fe80::10/64", "169.254.4.5/16", "2001:db8::12/64"} {
			ip, prefix, _ := net.ParseCIDR(text)
			prefix.IP = ip
			result = append(result, prefix)
		}
		return result, nil
	}
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("fe80::10")}}, nil
	}
	for _, address := range []string{"[fe80::10]:8443", "scope.example.test:8443"} {
		resolved, err := resolveDashboardAddress(context.Background(), address, interfaces, lookup)
		var refusal Refusal
		if !errors.As(err, &refusal) || refusal.Code != "dashboard_address_invalid" {
			t.Errorf("accepted an address requiring an unsupported zone: %s %+v %v", address, resolved, err)
		}
	}
	for _, address := range []string{"169.254.4.5:8443", "[2001:db8::12]:8443"} {
		if _, err := resolveDashboardAddress(context.Background(), address, interfaces, lookup); err != nil {
			t.Errorf("refused an address that needs no zone: %s %v", address, err)
		}
	}
}
