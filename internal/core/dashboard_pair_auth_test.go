package core

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pairingAuth(t *testing.T) (*dashboardAuth, *time.Time, string) {
	t.Helper()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	a, err := newDashboardAuth(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := a.start("dashboard")
	if err != nil {
		t.Fatal(err)
	}
	return a, &now, issuer
}

func mustRemoteTicket(t *testing.T, a *dashboardAuth, issuer string) string {
	t.Helper()
	token, expiry, err := a.newRemoteTicket("https://host.example.ts.net", issuer)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 64 || expiry.Sub(a.now()) != remoteTicketLifetime {
		t.Fatal("invalid ticket shape or expiry")
	}
	hash, ok := tokenHash(token)
	if !ok || len(a.tickets) != 1 || a.tickets[hash].origin != "https://host.example.ts.net" {
		t.Fatal("ticket not stored by hash and exact origin")
	}
	return token
}

func TestRemoteTicketBoundOneTimeSession(t *testing.T) {
	a, _, issuer := pairingAuth(t)
	token := mustRemoteTicket(t, a, issuer)
	if _, ok, err := a.redeemRemoteTicket(token, "https://else.example.ts.net"); ok || err != nil {
		t.Fatal("ticket crossed origin", ok, err)
	}
	if _, ok, err := a.login(token); ok || err != nil {
		t.Fatal("remote ticket entered local link flow", ok, err)
	}
	id, ok, err := a.redeemRemoteTicket(token, "https://host.example.ts.net")
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	hash, _ := tokenHash(id)
	if a.sessions[hash].client != "remote:https://host.example.ts.net" {
		t.Fatal("session scope missing")
	}
	if _, ok, err := a.redeemRemoteTicket(token, "https://host.example.ts.net"); ok || err != nil {
		t.Fatal("ticket replayed", ok, err)
	}
	local, err := a.link()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.redeemRemoteTicket(local, "https://host.example.ts.net"); ok || err != nil {
		t.Fatal("local link crossed into remote", ok, err)
	}
	if _, ok, err := a.login(local); !ok || err != nil {
		t.Fatal("remote check consumed local token", ok, err)
	}
}

func TestRemoteTicketExpiryAndRevocation(t *testing.T) {
	for _, mode := range []string{"expiry", "logout", "secret-change", "remote-disable", "restart"} {
		t.Run(mode, func(t *testing.T) {
			a, now, issuer := pairingAuth(t)
			token := mustRemoteTicket(t, a, issuer)
			switch mode {
			case "expiry":
				*now = now.Add(remoteTicketLifetime)
			case "logout":
				a.logout(issuer)
			case "secret-change":
				a.endAll()
			case "remote-disable":
				a.endRemote()
			case "restart":
				var err error
				a, err = newDashboardAuth(func() time.Time { return *now })
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, ok, err := a.redeemRemoteTicket(token, "https://host.example.ts.net"); ok || err != nil {
				t.Fatal("revoked/expired ticket accepted", ok, err)
			}
		})
	}
	a, _, issuer := pairingAuth(t)
	a.logout(issuer)
	if _, _, err := a.newRemoteTicket("https://host.example.ts.net", issuer); err == nil {
		t.Fatal("logged-out browser minted ticket")
	}
	if _, _, err := a.newRemoteTicket("http://host.example.ts.net", issuer); err == nil {
		t.Fatal("untrusted origin minted ticket")
	}
}

func TestRemoteTicketConcurrentRedeemAndRevocation(t *testing.T) {
	a, _, issuer := pairingAuth(t)
	token := mustRemoteTicket(t, a, issuer)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := a.redeemRemoteTicket(token, "https://host.example.ts.net")
			if err != nil {
				t.Error(err)
			}
			if ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("concurrent scans started", successes.Load(), "sessions")
	}
	// A concurrent revocation either precedes redemption and refuses it, or follows
	// the atomic consume/insert and removes the new remote session. It cannot leave
	// a late-created login behind after the revocation lock was released.
	for range 64 {
		token, _, err := a.newRemoteTicket("https://host.example.ts.net", issuer)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func() { defer wg.Done(); a.redeemRemoteTicket(token, "https://host.example.ts.net") }()
		go func() { defer wg.Done(); a.endRemote() }()
		wg.Wait()
		a.mu.Lock()
		for _, session := range a.sessions {
			if session.client != "dashboard" {
				t.Error("revocation left remote login")
			}
		}
		a.mu.Unlock()
	}
}

func TestRemoteTicketOutstandingCap(t *testing.T) {
	a, now, issuer := pairingAuth(t)
	oldest, _, err := a.newRemoteTicket("https://host.example.ts.net", issuer)
	if err != nil {
		t.Fatal(err)
	}
	for range maxLoginLinks {
		*now = now.Add(time.Second)
		if _, _, err := a.newRemoteTicket("https://host.example.ts.net", issuer); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.tickets) != maxLoginLinks {
		t.Fatal("ticket cap", len(a.tickets))
	}
	if _, ok, err := a.redeemRemoteTicket(oldest, "https://host.example.ts.net"); ok || err != nil {
		t.Fatal("oldest excess ticket survived", ok, err)
	}
}
