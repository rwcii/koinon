package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// claimFixture is a cycle fixture whose session holds work claims in its repository's store
// under each consumer form, beside a claim of another session.
type claimFixture struct {
	*cycleFixture
	m                                        MemoryCaller
	participant, peer, native, family, other claimOf
}

type claimOf struct {
	m          MemoryCaller
	id         string
	generation int64
}

func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()
	f := &claimFixture{cycleFixture: newCycleFixture(t)}
	m, err := f.s.ResolveWorkCaller(context.Background(), f.k, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Consumer != participantKey(f.session.Address) {
		t.Fatalf("consumer %q", m.Consumer)
	}
	f.m = m
	take := func(m MemoryCaller, title string) claimOf {
		id := create(t, f.s, m, title)
		return claimOf{m, id, mustStart(t, f.s, m, id, map[string]any{"lease_seconds": 60})}
	}
	stranger := other(m, "synthetic-stranger")
	stranger.Native = Key{"claude", "synthetic-stranger"}
	f.participant = take(m, "participant")
	f.peer = take(other(m, f.session.Name), "peer")
	f.family = take(other(m, "claude:synthetic-a"), "family")
	f.native = take(other(m, "synthetic-copied-consumer"), "native")
	f.other = take(stranger, "other")
	// A role-bound claim records the native session's work actor (work_role.go); it is set
	// here directly.
	actor := nativeWorkActorForKey(f.k)
	if _, err := fixtureExec(f.s, `UPDATE claim_bundles SET consumer=?,native_actor=? WHERE work_id=?`, "native-work:"+actor, actor, f.native.id); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *claimFixture) expires(t *testing.T, c claimOf) float64 {
	t.Helper()
	var expires float64
	if err := f.s.db.QueryRow(`SELECT expires_at FROM claim_bundles WHERE work_id=?`, c.id).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	return expires
}

func (f *claimFixture) renew(t *testing.T, c claimOf, revision, seconds int64) float64 {
	t.Helper()
	out := mustWork(t, f.s, c.m, "claim-renew", map[string]any{"work_id": c.id, "claim_generation": c.generation,
		"if_claim_revision": revision, "lease_seconds": seconds, "key": key(), "deadline": now(f.s) + 600})
	return out["claim"].(map[string]any)["expires_at"].(float64)
}

// Admission extends the session's live claims, under each consumer form, to the protection
// floor without changing their revision; another session's claim keeps its lease (#489).
func TestContextCycleAdmissionProtectsClaims(t *testing.T) {
	f := newClaimFixture(t)
	before := f.expires(t, f.other)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	floor := float64(v.CycleDeadline)/1000 + cycleClaimGrace
	for name, c := range map[string]claimOf{"participant": f.participant, "peer": f.peer, "family": f.family, "native": f.native} {
		if got := f.expires(t, c); got != floor {
			t.Fatalf("%s: expires %v, want %v", name, got, floor)
		}
	}
	if got := f.expires(t, f.other); got != before {
		t.Fatalf("another session's claim moved: %v", got)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM claim_bundles WHERE revision!=1`); n != 0 {
		t.Fatalf("a claim revision changed: %d", n)
	}
	// Past the ordinary lease, a renew with the unchanged claim revision keeps the floor, and
	// the owner still updates its work.
	f.clock = f.clock.Add(5 * time.Minute)
	if got := f.renew(t, f.participant, 1, 60); got != floor {
		t.Fatalf("renew shortened the protected claim: %v", got)
	}
	if _, err := update(f.s, f.m, f.participant.id, revision(t, f.s, f.m, f.participant.id), f.participant.generation, nil); err != nil {
		t.Fatalf("owner lost the claim: %v", err)
	}
	// A longer renew stays longer.
	if got := f.renew(t, f.peer, 1, 3600); got != now(f.s)+3600 {
		t.Fatalf("longer renew: %v", got)
	}
	// The sweep ends the unprotected claim and keeps the protected ones.
	f.step(t)
	if err := f.s.MaintainWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM claim_bundles WHERE active=1`); n != 4 {
		t.Fatalf("active claims %d, want 4", n)
	}
}

// An expired claim is not revived, an unheld participant address protects nothing, and a
// renew without a cycle may shorten as before (#489).
func TestContextCycleClaimsBoundaries(t *testing.T) {
	f := newClaimFixture(t)
	// A renew without a cycle shortens.
	long := f.renew(t, f.family, 1, 3600)
	if got := f.renew(t, f.family, 2, 60); got >= long || got != now(f.s)+60 {
		t.Fatalf("renew without a cycle: %v after %v", got, long)
	}
	// The peer claim expires before the request.
	f.clock = f.clock.Add(30 * time.Second)
	fixtureExec(f.s, `UPDATE claim_bundles SET expires_at=? WHERE work_id=?`, now(f.s)-1, f.peer.id)
	// Another session holds the participant address at admission.
	fixtureExec(f.s, `UPDATE names SET holder_id='synthetic-holder' WHERE name=?`, f.session.Address)
	participant := f.expires(t, f.participant)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	if v == nil {
		t.Fatal("no cycle")
	}
	floor := float64(v.CycleDeadline)/1000 + cycleClaimGrace
	if got := f.expires(t, f.peer); got >= now(f.s) {
		t.Fatalf("an expired claim was revived: %v", got)
	}
	if got := f.expires(t, f.participant); got != participant {
		t.Fatalf("an unheld address extended its claim: %v", got)
	}
	if got := f.expires(t, f.native); got != floor {
		t.Fatalf("native claim: %v", got)
	}
}

// After the cycle ends, the extended expiry stays; a later renew follows the ordinary lease
// (#489).
func TestContextCycleClaimsAfterTheCycle(t *testing.T) {
	f := newClaimFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	floor := float64(v.CycleDeadline)/1000 + cycleClaimGrace
	e, _ := f.s.contextEnables(context.Background())
	if _, err := f.s.SetContextEnable(roomCtx("context-enable"), f.session.Name, f.reload(t, f.k).Revision, e[f.k].Revision, false); err != nil {
		t.Fatal(err)
	}
	if got := f.status(t, f.k); got.State != cycleCancelled {
		t.Fatalf("cycle: %+v", got)
	}
	if got := f.expires(t, f.native); got != floor {
		t.Fatalf("the end shortened the claim: %v", got)
	}
	if got := f.renew(t, f.family, 1, 60); got != now(f.s)+60 {
		t.Fatalf("renew after the cycle: %v", got)
	}
}

// The managed successor that holds the participant renews the protected claim while the
// cycle waits for its pickup, and keeps the floor (#489).
func TestContextCycleClaimsSuccessorRenew(t *testing.T) {
	f := newClaimFixture(t)
	v, name := f.readyCycle(t)
	floor := float64(v.CycleDeadline)/1000 + cycleClaimGrace
	f.step(t)
	f.step(t)
	f.step(t)
	if v = *f.status(t, f.k); v.State != cycleWaitingPickup {
		t.Fatalf("waiting: %+v", v)
	}
	successor := Key{"claude", "synthetic-epoch-2"}
	join(t, f.s, "claude", successor.ID, f.repo)
	fixtureExec(f.s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, successor.ID)
	fixtureExec(f.s, `UPDATE names SET holder_id=? WHERE name=?`, successor.ID, f.session.Address)
	f.status(t, successor)
	ms, err := f.s.ResolveWorkCaller(context.Background(), successor, nil)
	if err != nil || ms.Consumer != f.m.Consumer {
		t.Fatalf("successor caller: %v %q", err, ms.Consumer)
	}
	// Past the ordinary lease, before the pickup stage's deadline.
	f.clock = f.clock.Add(90 * time.Second)
	if got := f.renew(t, claimOf{ms, f.participant.id, f.participant.generation}, 1, 60); got != floor {
		t.Fatalf("successor renew: %v, want %v", got, floor)
	}
	if done, err := f.s.ContextCyclePickup(context.Background(), successor, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work"); err != nil || done.State != cycleComplete {
		t.Fatalf("pickup: %v %+v", err, done)
	}
}

// A failed admission leaves the claims and the store unchanged; a committed one keeps the
// extension across a reopen (#489).
func TestContextCycleClaimsCommitWithAdmission(t *testing.T) {
	f := newClaimFixture(t)
	before := f.expires(t, f.participant)
	if _, err := fixtureExec(f.s, `CREATE TRIGGER synthetic_fail BEFORE INSERT ON audit WHEN NEW.action='context-cycle'
		BEGIN SELECT RAISE(ABORT,'synthetic admission failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Millisecond)
	f.s.Observe(context.Background(), Observation{Caller: f.k, Context: &ObservedValue{Source: "claude_statusline",
		At: f.s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(900), Epoch: "synthetic-epoch-1"}})
	if n := count(t, f.s, `SELECT COUNT(*) FROM context_cycles`); n != 0 || f.expires(t, f.participant) != before {
		t.Fatalf("a failed admission left %d cycles, claim expiry %v", n, f.expires(t, f.participant))
	}
	if _, err := fixtureExec(f.s, `DROP TRIGGER synthetic_fail`); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 950, "synthetic-epoch-1")
	v := f.status(t, f.k)
	if v == nil {
		t.Fatal("no cycle")
	}
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = f.s.now
	f.s = reopened
	if got := f.expires(t, f.participant); got != float64(v.CycleDeadline)/1000+cycleClaimGrace {
		t.Fatalf("after reopen: %v", got)
	}
}

// A real role-bound start records the native work actor; admission protects that claim and
// a renew past the ordinary lease keeps the floor (#489; codex-koinon's review fixture).
func TestContextCycleClaimsRealNativeRoleClaim(t *testing.T) {
	f := newCycleFixture(t)
	m := participantCaller(t, f.s, f.k)
	id := createRoleWork(t, f.s, m, "claude", "")
	gen := mustStart(t, f.s, m, id, map[string]any{"lease_seconds": 60})
	var actor, consumer string
	if err := f.s.db.QueryRow(`SELECT native_actor,consumer FROM claim_bundles WHERE work_id=?`, id).Scan(&actor, &consumer); err != nil {
		t.Fatal(err)
	}
	if actor != nativeWorkActorForKey(f.k) || consumer != "native-work:"+actor {
		t.Fatalf("native start binding: %q %q", actor, consumer)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	floor := float64(v.CycleDeadline)/1000 + cycleClaimGrace
	f.clock = f.clock.Add(90 * time.Second)
	f.status(t, f.k)
	out := mustWork(t, f.s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": gen,
		"if_claim_revision": 1, "lease_seconds": 60, "key": key(), "deadline": now(f.s) + 600})
	if got := out["claim"].(map[string]any)["expires_at"].(float64); got != floor {
		t.Fatalf("real native renewal shortened floor: %v != %v", got, floor)
	}
}

// An admission that cannot fund its credit stores no cycle and moves no claim; a funded
// retry adds exactly the cycle credit to the debt (#489; codex-koinon's review fixture).
func TestContextCycleClaimsAdmissionFunding(t *testing.T) {
	f := newClaimFixture(t)
	debt := f.debt(t)
	before := f.expires(t, f.participant)
	// Allow the ordinary transaction to begin, but not to fund its new cycle credit.
	f.s.storage.maxPages = count(t, f.s, `PRAGMA page_count`) + reservePages + commitSlack + debt + appendAllowance
	if tx, err := f.s.begin(context.Background(), ordinary); err != nil {
		t.Fatal(err)
	} else {
		tx.Rollback()
	}
	f.clock = f.clock.Add(time.Millisecond)
	err := f.s.Observe(context.Background(), Observation{Caller: f.k, Context: &ObservedValue{Source: "claude_statusline",
		At: f.clock.UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(900), Epoch: "synthetic-epoch-1"}})
	if err != nil && !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if got := count(t, f.s, `SELECT COUNT(*) FROM context_cycles`); got != 0 || f.expires(t, f.participant) != before || f.debt(t) != debt {
		t.Fatalf("unfunded admission changed cycle/claim/debt: %d %v %d", got, f.expires(t, f.participant), f.debt(t))
	}
	f.s.storage.maxPages = defaultMaxPages
	f.observe(t, f.k, 950, "synthetic-epoch-1")
	if f.status(t, f.k) == nil || f.debt(t) != debt+cycleCreditPages {
		t.Fatalf("funded admission debt: %d != %d", f.debt(t), debt+cycleCreditPages)
	}
}
