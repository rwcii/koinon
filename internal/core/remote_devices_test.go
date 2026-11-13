package core

import (
	"context"
	"net/url"
	"testing"
	"time"
)

// #673 review L1: only a revoke-all cancels the open QR sign-in tickets.
func TestDurableRemoteSingleRevokeKeepsTickets(t *testing.T) {
	a, s, now := durableAuth(t)
	var tokens []string
	for i := 0; i < 2; i++ {
		token, err := a.start("remote:https://host.example.ts.net")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	a.tickets[[32]byte{1}] = remoteTicket{origin: "https://host.example.ts.net", expires: now.Add(time.Minute)}
	if err := a.revokeRemote(a.remoteSessionID(tokens[0]), "device_revoked"); err != nil {
		t.Fatal(err)
	}
	if len(a.tickets) != 1 {
		t.Fatal("single-device revoke cancelled the QR sign-in tickets")
	}
	// A failed single revoke is retried by every status read; the retry keeps the tickets too.
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_revoke BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected revocation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.revokeRemote(a.remoteSessionID(tokens[1]), "device_revoked"); err != ErrRevocationIncomplete {
		t.Fatal(err)
	}
	if a.remoteDiagnostics()["revocation_incomplete"] != true || len(a.tickets) != 1 {
		t.Fatal("diagnostics retry cancelled the QR sign-in tickets", len(a.tickets))
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_revoke`); err != nil {
		t.Fatal(err)
	}
	if a.remoteDiagnostics()["revocation_incomplete"] != false || len(a.tickets) != 1 {
		t.Fatal("retry did not finish, or it cancelled the tickets")
	}
	if err := a.revokeRemote("", "device_revoked"); err != nil {
		t.Fatal(err)
	}
	if len(a.tickets) != 0 {
		t.Fatal("revoke-all kept the QR sign-in tickets")
	}
}

// #673 review L2: a cleared maintainer secret ends the remote logins; it is not a storage fault.
func TestDurableRemoteClearedSecretIsNotStorageFault(t *testing.T) {
	a, s, _ := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.clearMaintainerSecret(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("remote login survived a cleared secret")
	}
	if a.authReason != "password_changed" || a.storeHealth == "unavailable" {
		t.Fatal(a.authReason, a.storeHealth)
	}
	devices, err := a.remoteDevices(token)
	if err != nil || devices == nil || len(devices) != 0 {
		t.Fatal(devices, err)
	}
	d := a.remoteDiagnostics()
	if d["secret_set"] != false || d["active_sessions"] != 0 || d["store_health"] == "unavailable" {
		t.Fatal(d)
	}
}

// #673 review L3: a wrong request is 400 and an unknown or ended device is 404.
func TestRemoteDevicesRouteStatus(t *testing.T) {
	d, root := startTestDaemon(t)
	local := newActionClient(t, d, root)
	d.store.secretCost = 1
	if err := d.store.setMaintainerSecret(context.Background(), testSecret, "set"); err != nil {
		t.Fatal(err)
	}
	token, err := d.auth.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	id := d.auth.remoteSessionID(token)
	post := func(form url.Values) dashboardResponse {
		form.Set("csrf", local.csrf)
		return dashboardDo(t, d, "POST", "/dashboard/remote/devices", local.cookie, local.sameOrigin, form)
	}
	for _, tc := range []struct {
		name string
		form url.Values
		want int
	}{
		{"unknown action", url.Values{"action": {"delete"}, "id": {id}}, 400},
		{"revoke without id", url.Values{"action": {"revoke"}}, 400},
		{"rename without label", url.Values{"action": {"rename"}, "id": {id}}, 400},
		{"rename unknown id", url.Values{"action": {"rename"}, "id": {"no-such-device"}, "label": {"Phone"}}, 404},
		{"revoke unknown id", url.Values{"action": {"revoke"}, "id": {"no-such-device"}}, 404},
		{"rename", url.Values{"action": {"rename"}, "id": {id}, "label": {"Phone"}}, 200},
		{"revoke", url.Values{"action": {"revoke"}, "id": {id}}, 200},
		{"revoke again", url.Values{"action": {"revoke"}, "id": {id}}, 404},
		{"rename revoked", url.Values{"action": {"rename"}, "id": {id}, "label": {"Phone"}}, 404},
	} {
		if got := post(tc.form); got.status != tc.want {
			t.Fatalf("%s: %d %s", tc.name, got.status, got.body)
		}
	}
}

// #673 review L9: a full device list keeps the QR code, so that it can be scanned again
// after the maintainer forgets a device.
func TestRemoteTicketKeptAtCapacity(t *testing.T) {
	a, _, _ := durableAuth(t)
	const origin = "https://host.example.ts.net"
	var first string
	for i := 0; i < maxRemoteSessions; i++ {
		token, err := a.start("remote:" + origin)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = token
		}
	}
	issuer, err := a.start("dashboard")
	if err != nil {
		t.Fatal(err)
	}
	ticket, _, err := a.newRemoteTicket(origin, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.redeemRemoteTicket(ticket, origin); ok || err != ErrRemoteCapacity {
		t.Fatal(ok, err)
	}
	if len(a.tickets) != 1 {
		t.Fatal("a full device list used up the QR code")
	}
	if err := a.revokeRemote(a.remoteSessionID(first), "device_revoked"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := a.redeemRemoteTicket(ticket, origin); !ok || err != nil || id == "" {
		t.Fatal("kept code not usable after a device was forgotten", ok, err)
	}
	if _, ok, _ := a.redeemRemoteTicket(ticket, origin); ok {
		t.Fatal("code redeemed twice")
	}
}
