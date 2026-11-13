package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// Remote disable must cancel computer invitations even when durable browser
// revocation fails; a retry must finish the browser revocation too.
func TestRemoteDisableRevocationFailureStillClearsLinkInvitations(t *testing.T) {
	host := newLinkHost(t, &linkNet{}, "alpha", "100.64.0.1")
	d := host.d
	ctx := context.Background()
	if _, err := d.linkInvite(ctx); err != nil {
		t.Fatal(err)
	}
	d.remote.mu.Lock()
	origin := d.remote.config.Mapping.Origin
	d.remote.mu.Unlock()
	token, err := d.auth.start("remote:" + origin)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, origin+"/dashboard/sessions", nil)
		r = r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, origin))
		r.AddCookie(&http.Cookie{Name: cookieName(r), Value: token})
		return r
	}
	if _, err := d.store.db.Exec(`CREATE TEMP TRIGGER fail_disable_revocation BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected revocation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.remoteDisable(ctx); !errors.Is(err, ErrRevocationIncomplete) {
		t.Fatalf("disable must report incomplete browser revocation: %v", err)
	}
	state, err := link.ReadState(host.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Invites) != 0 {
		t.Fatal("failed browser revocation left durable link invitations")
	}
	// The saved record is off and the mappings are gone, so a restart keeps Remote closed.
	if c, err := ReadRemoteConfig(host.root); err != nil || c == nil || c.Enabled || c.Owned || c.LinkOwned {
		t.Fatalf("failed browser revocation left the saved configuration on: %+v %v", c, err)
	}
	d.remote.mu.Lock()
	remoteClosed := d.remote.server == nil && d.remote.listener == nil
	d.remote.mu.Unlock()
	d.link.mu.Lock()
	linkClosed := d.link.listener == nil && len(d.link.invites) == 0
	d.link.mu.Unlock()
	if !remoteClosed || !linkClosed {
		t.Fatal("failed disable left a remote surface open")
	}
	if _, ok := d.auth.session(request()); ok {
		t.Fatal("failed disable retained browser access in memory")
	}
	if _, err := d.store.db.Exec(`DROP TRIGGER fail_disable_revocation`); err != nil {
		t.Fatal(err)
	}
	if err := d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := newDashboardAuth(time.Now)
	if err != nil {
		t.Fatal(err)
	}
	next.store = d.store
	if _, ok := next.session(request()); ok {
		t.Fatal("disable retry did not persist browser revocation")
	}
}
