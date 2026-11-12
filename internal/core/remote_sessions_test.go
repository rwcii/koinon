package core

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func durableAuth(t *testing.T) (*dashboardAuth, *Store, *time.Time) {
	t.Helper()
	s, _ := testStore(t)
	s.secretCost = 1
	if err := s.setMaintainerSecret(context.Background(), "a strong testing secret", "set"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	a, err := newDashboardAuth(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	a.store = s
	return a, s, &now
}
func remoteRequest(token string) *http.Request {
	r := httptest.NewRequest("GET", "https://host.example.ts.net/dashboard/sessions", nil)
	r = r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, "https://host.example.ts.net"))
	r.AddCookie(&http.Cookie{Name: cookieName(r), Value: token})
	return r
}
func TestDurableRemoteFixedDeadlineRestartAndRollback(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	info := a.sessionInfo(token)
	if info.ExpiresAt != now.Add(48*time.Hour).UnixMilli() {
		t.Fatal(info)
	}
	*now = now.Add(47 * time.Hour)
	if _, ok := a.session(remoteRequest(token)); !ok {
		t.Fatal("early expiry")
	}
	// New auth instance models per-start ephemeral keys and loss of every memory record.
	b, _ := newDashboardAuth(func() time.Time { return *now })
	b.store = s
	if _, ok := b.session(remoteRequest(token)); !ok {
		t.Fatal("restart lost durable session")
	}
	if b.sessionInfo(token).ExpiresAt != info.ExpiresAt {
		t.Fatal("restart extended session")
	}
	if b.validCSRF(token, a.csrf(token)) {
		t.Fatal("old CSRF survived restart")
	}
	*now = time.UnixMilli(info.ExpiresAt)
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("accepted at deadline")
	}
	*now = now.Add(-time.Hour)
	c, _ := newDashboardAuth(func() time.Time { return *now })
	c.store = s
	if _, ok := c.session(remoteRequest(token)); ok {
		t.Fatal("rollback revived expired session")
	}
	// KV rows leave the exact published schema catalog intact for previous binaries.
	if err := checkCatalog(s.db, schemaVersion); err != nil {
		t.Fatal(err)
	}
}
func TestDurableRemoteOfflinePasswordChangeAndRevocation(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setMaintainerSecret(context.Background(), "a new testing secret", "set"); err != nil {
		t.Fatal(err)
	}
	b, _ := newDashboardAuth(func() time.Time { return *now })
	b.store = s
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("offline password change accepted old session")
	}
	token, err = b.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.revokeRemote(b.remoteSessionID(token), "device_revoked"); err != nil {
		t.Fatal(err)
	}
	c, _ := newDashboardAuth(func() time.Time { return *now })
	c.store = s
	if _, ok := c.session(remoteRequest(token)); ok {
		t.Fatal("revocation lost on restart")
	}
	var found string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE name GLOB 'remote.v2.session:*' LIMIT 1`).Scan(&found); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(found, token) {
		t.Fatal("stored raw bearer")
	}
}
func TestDurableRemoteCapacityAndFailedWrites(t *testing.T) {
	a, s, _ := durableAuth(t)
	var first string
	for i := 0; i < maxRemoteSessions; i++ {
		token, err := a.start("remote:https://host.example.ts.net")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = token
		}
	}
	if _, err := a.start("remote:https://host.example.ts.net"); err != ErrRemoteCapacity {
		t.Fatal(err)
	}
	if _, ok := a.session(remoteRequest(first)); !ok {
		t.Fatal("capacity evicted first")
	}
	// Storage write faults cannot report successful issuance or revocation.
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_remote_write BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.revokeRemote(a.remoteSessionID(first), "device_revoked"); err != ErrRevocationIncomplete {
		t.Fatal(err)
	}
	if _, ok := a.session(remoteRequest(first)); ok {
		t.Fatal("failed durable revocation left access in process")
	}
}

func TestDurableRemoteStoreReopenAndCorruption(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	root := s.root
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	b, _ := newDashboardAuth(func() time.Time { return *now })
	b.store = reopened
	if _, ok := b.session(remoteRequest(token)); !ok {
		t.Fatal("reopened store lost session")
	}
	if _, err := reopened.db.Exec(`UPDATE settings SET value='{"version":2}' WHERE name GLOB 'remote.v2.session:*'`); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("corrupt session accepted")
	}
	if _, err := b.start("remote:https://host.example.ts.net"); err == nil {
		t.Fatal("issued login with corrupt session store")
	}
}

func TestDurableRemoteStorageAdmissionAndLogoutReadFailure(t *testing.T) {
	a, s, _ := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	s.storage.mu.Lock()
	s.storage.blocked = "injected"
	s.storage.mu.Unlock()
	if _, err := a.start("remote:https://host.example.ts.net"); err != ErrStorageBlocked {
		t.Fatalf("issuance bypassed admission: %v", err)
	}
	if err := a.logout(token); err != ErrRevocationIncomplete {
		t.Fatalf("logout storage failure: %v", err)
	}
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("failed logout retained access")
	}
	s.storage.mu.Lock()
	s.storage.blocked = ""
	s.storage.mu.Unlock()
	if err := a.revokeRemote("", "device_revoked"); err != nil {
		t.Fatal(err)
	}
	fresh, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.logout(fresh); err != ErrRevocationIncomplete {
		t.Fatalf("closed store logout reported success: %v", err)
	}
}

func TestDurableRemoteTrustedOriginReconciliation(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	// An untrusted crossover rejects only that request and preserves the valid login.
	copied := remoteRequest(token)
	copied = copied.WithContext(context.WithValue(copied.Context(), remoteOriginKey{}, "https://other.example.ts.net"))
	if _, ok := a.session(copied); ok {
		t.Fatal("cookie crossed origins")
	}
	if _, ok := a.session(remoteRequest(token)); !ok {
		t.Fatal("crossover revoked original")
	}
	// Restart knows no previous in-memory configuration. Reconciliation uses the
	// newly saved origin and must tombstone the old cookie before opening a listener.
	b, _ := newDashboardAuth(func() time.Time { return *now })
	b.store = s
	if err := b.reconcileRemoteOrigin("https://other.example.ts.net"); err != nil {
		t.Fatal(err)
	}
	c, _ := newDashboardAuth(func() time.Time { return *now })
	c.store = s
	if err := c.reconcileRemoteOrigin("https://host.example.ts.net"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.session(remoteRequest(token)); ok {
		t.Fatal("restored origin revived old session")
	}
}

func TestDurableRemoteRestoreDisabledConfiguration(t *testing.T) {
	d, root, _ := remoteFixture(t)
	ctx := context.Background()
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	token, err := d.auth.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := ReadRemoteConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// Model a new daemon's empty runtime configuration while retaining disk state.
	d.remote.mu.Lock()
	d.closeRemoteLocked()
	d.remote.config = nil
	d.remote.mu.Unlock()
	saved.Enabled = false
	if err := writeRemoteConfig(root, saved); err != nil {
		t.Fatal(err)
	}
	d.restoreRemote(ctx)
	if _, ok := d.auth.session(remoteRequest(token)); ok {
		t.Fatal("disabled saved configuration retained login")
	}
	// Fresh auth proves that rejecting the old credential is durable.
	next, _ := newDashboardAuth(time.Now)
	next.store = d.store
	if _, ok := next.session(remoteRequest(token)); ok {
		t.Fatal("disabled state revocation lost on restart")
	}
}

// A failed expiry write must remain visible and retry after storage recovers,
// even when the clock rolls back before that retry.
func TestDurableRemoteFailedExpiryRetriesBeforeRestart(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	info := a.sessionInfo(token)
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_expiry_write BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected expiry failure'); END`); err != nil {
		t.Fatal(err)
	}
	*now = time.UnixMilli(info.ExpiresAt)
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("accepted expired credential with failed write")
	}
	if got := a.remoteDiagnostics()["revocation_incomplete"]; got != true {
		t.Fatalf("expiry persistence failure hidden: %v", got)
	}
	if got := a.remoteRejectionReason(remoteRequest(token)); got != "session_expired" {
		t.Fatalf("lost expiry reason: %s", got)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_expiry_write`); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(-time.Hour)
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("rollback revived pending expiry")
	}
	if got := a.remoteDiagnostics()["revocation_incomplete"]; got != false {
		t.Fatalf("durable retry did not complete: %v", got)
	}
	root := s.root
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	b, err := newDashboardAuth(func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	b.store = reopened
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("restart and rollback revived retried expiry")
	}
	if got := b.remoteRejectionReason(remoteRequest(token)); got != "session_expired" {
		t.Fatalf("durable reason: %s", got)
	}
}

func TestDurableRemoteFailedExpiryDiagnosticsRetry(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	info := a.sessionInfo(token)
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_expiry_write BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected expiry failure'); END`); err != nil {
		t.Fatal(err)
	}
	*now = time.UnixMilli(info.ExpiresAt)
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("expired credential accepted")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_expiry_write`); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(-time.Hour)
	// No further phone request: host diagnostics must finish the durable denial.
	status := a.remoteDiagnostics()
	if status["revocation_incomplete"] != false || status["active_sessions"] != 0 {
		t.Fatalf("pending expiry not reconciled: %v", status)
	}
	b, err := newDashboardAuth(func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	b.store = s
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("diagnostics retry was not durable")
	}
}

func TestDurableRemoteExpiryDiagnosticsMixedRetries(t *testing.T) {
	a, s, now := durableAuth(t)
	for i := 0; i < 2; i++ {
		if _, err := a.start("remote:https://host.example.ts.net"); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		raw, err := hex.DecodeString(session.Hash)
		if err != nil {
			t.Fatal(err)
		}
		var hash [32]byte
		copy(hash[:], raw)
		a.denied[hash] = "session_expired"
		if a.pendingRevocations == nil {
			a.pendingRevocations = map[string]string{}
		}
		a.pendingRevocations[session.ID] = "session_expired"
	}
	// Fail the first row and allow the later row to succeed. The later success
	// must not hide the earlier failure in this diagnostics response.
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_first_expiry BEFORE UPDATE ON settings WHEN OLD.name = '` + remoteSessionPrefix + sessions[0].ID + `' BEGIN SELECT RAISE(FAIL,'injected expiry failure'); END`); err != nil {
		t.Fatal(err)
	}
	status := a.remoteDiagnostics()
	if status["store_health"] != "unavailable" || status["revocation_incomplete"] != true || status["active_sessions"] != 0 {
		t.Fatalf("partial persistence failure hidden: %v", status)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_first_expiry`); err != nil {
		t.Fatal(err)
	}
	status = a.remoteDiagnostics()
	if status["store_health"] != "healthy" || status["revocation_incomplete"] != false {
		t.Fatalf("repair did not complete: %v", status)
	}
	b, err := newDashboardAuth(func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	b.store = s
	rows, err := b.readRemoteLocked()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Revoked != "session_expired" {
			t.Fatal("missing durable denial")
		}
	}
}

func TestDurableRemoteSmallClockCorrectionKeepsValidSession(t *testing.T) {
	a, s, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	original := a.sessionInfo(token)
	*now = now.Add(time.Hour)
	if _, ok := a.session(remoteRequest(token)); !ok {
		t.Fatal("valid session rejected")
	}
	observed := a.sessionInfo(token).LastObserved
	*now = now.Add(-time.Second)
	if _, ok := a.session(remoteRequest(token)); !ok {
		t.Fatal("small clock correction expired valid session")
	}
	info := a.sessionInfo(token)
	if info.LastObserved != observed || info.ExpiresAt != original.ExpiresAt {
		t.Fatal("clock correction changed high-water mark or deadline", info)
	}
	b, err := newDashboardAuth(func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	b.store = s
	if _, ok := b.session(remoteRequest(token)); !ok {
		t.Fatal("restart during clock correction rejected valid session")
	}
	*now = time.UnixMilli(original.ExpiresAt)
	if _, ok := b.session(remoteRequest(token)); ok {
		t.Fatal("accepted at original deadline")
	}
}

func TestDurableRemoteRevokeAllDiagnosticsRetry(t *testing.T) {
	a, s, now := durableAuth(t)
	var tokens []string
	for i := 0; i < 2; i++ {
		token, err := a.start("remote:https://host.example.ts.net")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER fail_revoke BEFORE UPDATE ON settings BEGIN SELECT RAISE(FAIL,'injected revocation failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.revokeRemote("", "device_revoked"); err != ErrRevocationIncomplete {
		t.Fatal(err)
	}
	if a.remoteDiagnostics()["revocation_incomplete"] != true {
		t.Fatal("global revocation failure hidden")
	}
	// The original inventory was readable: its retry must not include a later
	// newly issued browser session.
	fresh, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_revoke`); err != nil {
		t.Fatal(err)
	}
	if a.remoteDiagnostics()["revocation_incomplete"] != false {
		t.Fatal("global revocation not retried")
	}
	b, err := newDashboardAuth(func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	b.store = s
	for _, token := range tokens {
		if _, ok := b.session(remoteRequest(token)); ok {
			t.Fatal("global retry did not persist revocation")
		}
	}
	if _, ok := b.session(remoteRequest(fresh)); !ok {
		t.Fatal("retry extended the original forget-all to a later sign-in")
	}
}

func TestDurableRemoteUnreadableRevocationDiagnosticsRetry(t *testing.T) {
	for _, action := range []string{"revoke", "logout"} {
		t.Run(action, func(t *testing.T) {
			a, s, now := durableAuth(t)
			token, err := a.start("remote:https://host.example.ts.net")
			if err != nil {
				t.Fatal(err)
			}
			other, err := a.start("remote:https://host.example.ts.net")
			if err != nil {
				t.Fatal(err)
			}
			id := a.remoteSessionID(token)
			root := s.root
			if err := s.db.Close(); err != nil {
				t.Fatal(err)
			}
			if action == "logout" {
				err = a.logout(token)
			} else {
				err = a.revokeRemote(id, "device_revoked")
			}
			if err != ErrRevocationIncomplete {
				t.Fatal(err)
			}
			if a.remoteDiagnostics()["revocation_incomplete"] != true {
				t.Fatal("unreadable revocation failure hidden")
			}
			reopened, err := openStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.db.Close()
			a.store = reopened
			status := a.remoteDiagnostics()
			if status["revocation_incomplete"] != false || status["active_sessions"] != 1 {
				t.Fatal("read recovery did not reconcile targeted denial", status)
			}
			if _, ok := a.session(remoteRequest(other)); !ok {
				t.Fatal("targeted retry left unrelated browser locked out")
			}
			b, err := newDashboardAuth(func() time.Time { return *now })
			if err != nil {
				t.Fatal(err)
			}
			b.store = reopened
			if _, ok := b.session(remoteRequest(token)); ok {
				t.Fatal("targeted retry did not persist")
			}
		})
	}
}

func TestDurableRemoteForgetAllPreservesExpiryReason(t *testing.T) {
	a, _, now := durableAuth(t)
	token, err := a.start("remote:https://host.example.ts.net")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(remoteSessionLifetime)
	if _, ok := a.session(remoteRequest(token)); ok {
		t.Fatal("expired credential accepted")
	}
	if err := a.revokeRemote("", "device_revoked"); err != nil {
		t.Fatal(err)
	}
	if got := a.remoteRejectionReason(remoteRequest(token)); got != "session_expired" {
		t.Fatal("forget-all rewrote original end reason", got)
	}
}
