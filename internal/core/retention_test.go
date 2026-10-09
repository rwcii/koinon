package core

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Retention tests (#216): synthetic sessions in temporary state, with a test clock.

const day = 24 * time.Hour

func retentionStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	s.limits()
	return s, &clock
}

func cull(t *testing.T, s *Store) RetentionCounts {
	t.Helper()
	if err := s.Cull(context.Background()); err != nil {
		t.Fatalf("cull: %v", err)
	}
	status, err := s.RetentionStatus(context.Background())
	if err != nil || status.Last == nil || status.Fault != nil {
		t.Fatalf("retention status: %+v %v", status, err)
	}
	return *status.Last
}

func sendAll(t *testing.T, s *Store, from Key, to string, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := range n {
		out, err := s.Send(context.Background(), from, to, "synthetic message "+strconv.Itoa(i))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, out.ID)
	}
	return ids
}

func messageCount(t *testing.T, s *Store, k Key) int64 {
	t.Helper()
	return count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_family=? AND recipient_id=?`, k.Family, k.ID)
}

func sessionExists(t *testing.T, s *Store, k Key) bool {
	t.Helper()
	return count(t, s, `SELECT COUNT(*) FROM sessions WHERE family=? AND id=?`, k.Family, k.ID) == 1
}

// keepAlive renews a session so that it stays active while the clock moves.
func keepAlive(t *testing.T, s *Store, k Key) {
	t.Helper()
	current := sessionState(t, s, k)
	if current.State != "active" {
		if _, err := s.Register(context.Background(), withLaunch(t, s, Registration{Family: k.Family, ID: k.ID, Repository: current.Repository,
			Directory: current.Directory, TTLSeconds: 3600})); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := s.Mutate(context.Background(), Mutation{Family: k.Family, ID: k.ID, IfRevision: current.Revision, TTLSeconds: 3600}, false); err != nil {
		t.Fatal(err)
	}
}

func TestAcknowledgedMessagesDeletedAfterRetention(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "retention")
	a := join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	ka, kb := Key{"codex", "synthetic-a"}, Key{"claude", "synthetic-b"}
	ids := sendAll(t, s, ka, b.Name, 3)
	other := sendAll(t, s, kb, a.Name, 1)
	if _, err := s.Ack(ctx, kb, 2); err != nil {
		t.Fatal(err)
	}
	// The first sweep only marks the acknowledgement.
	if got := cull(t, s); got.MessagesByRetention != 0 || messageCount(t, s, kb) != 3 {
		t.Fatalf("first sweep deleted: %+v", got)
	}
	*clock = clock.Add(messageRetention - time.Minute)
	keepAlive(t, s, ka)
	keepAlive(t, s, kb)
	if got := cull(t, s); got.MessagesByRetention != 0 {
		t.Fatalf("deleted before the period: %+v", got)
	}
	*clock = clock.Add(2 * time.Minute)
	keepAlive(t, s, ka)
	keepAlive(t, s, kb)
	if got := cull(t, s); got.MessagesByRetention != 2 || messageCount(t, s, kb) != 1 {
		t.Fatalf("after the period: %+v, %d left", got, messageCount(t, s, kb))
	}
	// The unacknowledged message stays and the inbox still reads it.
	inbox, err := s.ReadInbox(ctx, kb, 0, 0)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].Seq != 3 || inbox.AckedThrough != 2 {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	// The sender reads a deleted message as the fixed state deleted.
	outcome, err := s.MessageOutcome(ctx, ka, ids[0])
	if err != nil || outcome.DeliveryState != "deleted" || outcome.ID != ids[0] {
		t.Fatalf("deleted outcome: %+v %v", outcome, err)
	}
	// A message that exists but another session sent, and an ID never issued, are not found.
	if _, err := s.MessageOutcome(ctx, ka, other[0]); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("other sender: %v", err)
	}
	if _, err := s.MessageOutcome(ctx, ka, other[0]+100); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unissued: %v", err)
	}
	if outcome, err := s.MessageOutcome(ctx, ka, ids[2]); err != nil || outcome.DeliveryState == "deleted" {
		t.Fatalf("kept message: %+v %v", outcome, err)
	}
}

func TestLateAcknowledgementKeepsMessageForFullPeriod(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "late")
	join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	ka, kb := Key{"codex", "synthetic-a"}, Key{"claude", "synthetic-b"}
	sendAll(t, s, ka, b.Name, 1)
	cull(t, s)
	// The message waits unacknowledged longer than the period, then is acknowledged.
	*clock = clock.Add(messageRetention + day)
	keepAlive(t, s, kb)
	keepAlive(t, s, ka)
	if _, err := s.Ack(ctx, kb, 1); err != nil {
		t.Fatal(err)
	}
	if got := cull(t, s); got.MessagesByRetention != 0 {
		t.Fatalf("deleted at acknowledgement: %+v", got)
	}
	*clock = clock.Add(messageRetention - time.Minute)
	keepAlive(t, s, kb)
	if got := cull(t, s); got.MessagesByRetention != 0 {
		t.Fatalf("deleted inside the period after acknowledgement: %+v", got)
	}
	*clock = clock.Add(2 * time.Minute)
	if got := cull(t, s); got.MessagesByRetention != 1 {
		t.Fatalf("not deleted after the period: %+v", got)
	}
}

func TestMessageDeletionIsBatched(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "batched")
	join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	ka, kb := Key{"codex", "synthetic-a"}, Key{"claude", "synthetic-b"}
	sendAll(t, s, ka, b.Name, cullBatch+20)
	if _, err := s.Ack(ctx, kb, cullBatch+20); err != nil {
		t.Fatal(err)
	}
	cull(t, s)
	*clock = clock.Add(messageRetention + time.Minute)
	keepAlive(t, s, kb)
	if got := cull(t, s); got.MessagesByRetention != cullBatch+20 || messageCount(t, s, kb) != 0 {
		t.Fatalf("batched deletion: %+v", got)
	}
	if mark := count(t, s, `SELECT ack_mark_at FROM sessions WHERE family='claude' AND id='synthetic-b'`); mark != 0 {
		t.Fatalf("mark left: %d", mark)
	}
}

func TestInactiveSessionsDeletedAfterRetention(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "inactive")
	gone := join(t, s, "codex", "synthetic-gone", repo)
	unread := join(t, s, "claude", "synthetic-unread", repo)
	claimed := join(t, s, "agy", "synthetic-claimed", repo)
	sender := Key{"opencode", "synthetic-sender"}
	kg, ku, kc := Key{"codex", "synthetic-gone"}, Key{"claude", "synthetic-unread"}, Key{"agy", "synthetic-claimed"}
	join(t, s, sender.Family, sender.ID, repo)
	sendAll(t, s, sender, unread.Name, 1)
	m := MemoryCaller{Repository: "/synthetic/inactive/.git", Family: "agy", Name: claimed.Name, Consumer: "agy:synthetic-claimed"}
	id := create(t, s, m, "synthetic claimed item")
	// The bundle stays active until the work sweep reconciles its expired lease, which
	// this test does not run.
	mustStart(t, s, m, id, nil)
	if gone.Alias == "" {
		t.Fatalf("the first codex session holds no alias: %+v", gone)
	}
	cull(t, s)
	*clock = clock.Add(sessionRetention)
	keepAlive(t, s, sender)
	// Inside the period after expiry nothing is deleted.
	if got := cull(t, s); got.SessionsByRetention != 0 || !sessionExists(t, s, kg) {
		t.Fatalf("deleted inside the period: %+v", got)
	}
	*clock = clock.Add(2 * time.Minute)
	keepAlive(t, s, sender)
	got := cull(t, s)
	if got.SessionsByRetention != 1 || sessionExists(t, s, kg) || !sessionExists(t, s, ku) || !sessionExists(t, s, kc) {
		t.Fatalf("retention deletion: %+v", got)
	}
	if got.HeldUnacknowledged != 1 || got.HeldClaims != 1 {
		t.Fatalf("held counts: %+v", got)
	}
	// The deleted session's names are released: a send to its name finds no peer, and the
	// alias waits for the next active session of its repository.
	if n := count(t, s, `SELECT COUNT(*) FROM names WHERE kind='peer' AND family='codex' AND session_id='synthetic-gone'`); n != 0 {
		t.Fatalf("peer name kept: %d", n)
	}
	if _, err := s.Send(ctx, sender, gone.Name, "late"); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("send to deleted name: %v", err)
	}
	if _, err := s.Send(ctx, sender, gone.Alias, "late"); !errors.Is(err, ErrAliasUnheld) {
		t.Fatalf("send to released alias: %v", err)
	}
	next := join(t, s, "codex", "synthetic-next", repo)
	if next.Alias != gone.Alias {
		t.Fatalf("alias not taken by the next session: %+v", next)
	}
	// The maintainer's clear acknowledges the kept inbox; the session goes once its
	// messages have passed their own retention period.
	if _, err := s.ack(ctx, ku, -1, true); err != nil {
		t.Fatal(err)
	}
	cull(t, s)
	if !sessionExists(t, s, ku) {
		t.Fatal("deleted before its acknowledged messages")
	}
	*clock = clock.Add(messageRetention + time.Minute)
	keepAlive(t, s, sender)
	cull(t, s)
	if got := cull(t, s); sessionExists(t, s, ku) || got.HeldUnacknowledged != 0 {
		t.Fatalf("cleared session kept: %+v", got)
	}
	if !sessionExists(t, s, kc) {
		t.Fatal("a session with a live claim was deleted")
	}
}

func TestImportedAndReregisteredSessions(t *testing.T) {
	s, clock := retentionStore(t)
	repo := namedRepo(t, "imported")
	join(t, s, "codex", "synthetic-imported", repo)
	back := join(t, s, "claude", "synthetic-back", repo)
	ki, kb := Key{"codex", "synthetic-imported"}, Key{"claude", "synthetic-back"}
	// An imported session can carry registration, renewal and expiry time 0.
	if _, err := s.db.Exec(`UPDATE sessions SET registered_at=0,renewed_at=0,expires_at=0 WHERE family='codex' AND id='synthetic-imported'`); err != nil {
		t.Fatal(err)
	}
	if got := cull(t, s); got.SessionsByRetention != 1 || sessionExists(t, s, ki) {
		t.Fatalf("imported session: %+v", got)
	}
	// A session past its period that registers again just before the sweep is kept.
	*clock = clock.Add(sessionRetention + time.Minute)
	if _, err := s.Register(context.Background(), withLaunch(t, s, Registration{Family: "claude", ID: "synthetic-back", Repository: repo, Directory: repo, TTLSeconds: 60})); err != nil {
		t.Fatal(err)
	}
	if got := cull(t, s); got.SessionsByRetention != 0 || !sessionExists(t, s, kb) {
		t.Fatalf("registered session deleted: %+v", got)
	}
	// The deleting transaction reads the conditions again: a registration after the
	// candidate was chosen keeps it.
	*clock = clock.Add(sessionRetention + time.Minute)
	if _, err := s.Register(context.Background(), withLaunch(t, s, Registration{Family: "claude", ID: "synthetic-back", Repository: repo, Directory: repo, TTLSeconds: 60})); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.deleteSession(context.Background(), kb, `s.purge_at=0 AND s.last_seq=s.acked_through AND `+inactiveSince,
		s.now().Add(-sessionRetention).UnixMilli())
	if err != nil || deleted || !sessionExists(t, s, kb) {
		t.Fatalf("deleted a registered session: %v %v", deleted, err)
	}
	if sessionState(t, s, kb).Name != back.Name {
		t.Fatal("name changed")
	}
}

func TestPurgeActiveSessionWithLiveClaim(t *testing.T) {
	s, _ := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "purge")
	p := join(t, s, "codex", "synthetic-purged", repo)
	join(t, s, "claude", "synthetic-sender", repo)
	kp, ks := Key{"codex", "synthetic-purged"}, Key{"claude", "synthetic-sender"}
	ids := sendAll(t, s, ks, p.Name, 3)
	m := MemoryCaller{Repository: "/synthetic/purge/.git", Family: "codex", Name: p.Name, Consumer: "codex:synthetic-purged"}
	id := create(t, s, m, "synthetic purged item")
	mustStart(t, s, m, id, nil)
	// A stale revision, and a mark on an unmarked session's removal, are refused.
	if err := s.markPurge(ctx, kp, p.Revision+1, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale mark: %v", err)
	}
	if err := s.markPurge(ctx, kp, p.Revision, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("unmark without a mark: %v", err)
	}
	if err := s.markPurge(ctx, kp, p.Revision, true); err != nil {
		t.Fatal(err)
	}
	marked := sessionState(t, s, kp)
	if marked.State != "retired" || marked.PurgeAt == 0 {
		t.Fatalf("mark did not retire: %+v", marked)
	}
	got := cull(t, s)
	if got.SessionsByPurge != 1 || got.MessagesByPurge != 3 || sessionExists(t, s, kp) || messageCount(t, s, kp) != 0 {
		t.Fatalf("purge: %+v", got)
	}
	// The claim was released first, with a work event that names the maintainer.
	item := get(t, s, m, id)
	if item["lifecycle"] != "open" || item["checkpoint"] != purgeCheckpoint {
		t.Fatalf("claim not released: %+v", item)
	}
	store, _ := storeOf(ctx, s.db, m.Repository)
	var writer string
	if err := s.db.QueryRow(`SELECT e.writer_family||':'||e.writer_name FROM work_events w JOIN memory_entries e
		ON e.repository=? AND e.seq=w.seq WHERE w.store=? AND w.work_id=? AND w.kind='released'`, m.Repository, store, id).Scan(&writer); err != nil {
		t.Fatal(err)
	}
	if writer != "maintainer:maintainer" {
		t.Fatalf("release event names %s, not the maintainer", writer)
	}
	// The sender reads the purged, never acknowledged, messages as deleted.
	if outcome, err := s.MessageOutcome(ctx, ks, ids[2]); err != nil || outcome.DeliveryState != "deleted" {
		t.Fatalf("purged outcome: %+v %v", outcome, err)
	}
	if status, _ := s.RetentionStatus(ctx); status.MarkedForPurge != 0 {
		t.Fatalf("marked count: %+v", status)
	}
}

func TestPurgeMarkRemovedBeforeSweep(t *testing.T) {
	s, _ := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "unmark")
	p := join(t, s, "codex", "synthetic-kept", repo)
	join(t, s, "claude", "synthetic-sender", repo)
	kp := Key{"codex", "synthetic-kept"}
	sendAll(t, s, Key{"claude", "synthetic-sender"}, p.Name, 2)
	if err := s.markPurge(ctx, kp, p.Revision, true); err != nil {
		t.Fatal(err)
	}
	if status, _ := s.RetentionStatus(ctx); status.MarkedForPurge != 1 {
		t.Fatalf("marked count: %+v", status)
	}
	marked := sessionState(t, s, kp)
	if err := s.markPurge(ctx, kp, marked.Revision, false); err != nil {
		t.Fatal(err)
	}
	got := cull(t, s)
	kept := sessionState(t, s, kp)
	if got.SessionsByPurge != 0 || kept.PurgeAt != 0 || kept.State != "retired" || messageCount(t, s, kp) != 2 {
		t.Fatalf("unmarked session changed: %+v %+v", got, kept)
	}
}

func TestPurgeDashboardActions(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	repo := namedRepo(t, "purgeactions")
	a := join(t, s, "codex", "synthetic-a", repo)
	ka := Key{"codex", "synthetic-a"}
	client := newActionClient(t, d, root)
	n := len(auditAll(t, s))
	form := func(revision int64, confirm bool) url.Values {
		v := url.Values{"family": {"codex"}, "id": {"synthetic-a"}, "revision": {strconv.FormatInt(revision, 10)}}
		if confirm {
			v.Set("confirm", "purge")
		}
		return v
	}
	if got := client.do("purge", form(a.Revision, false)); got != "confirmation_required" {
		t.Fatalf("unconfirmed purge: %s", got)
	}
	expectAudit(t, s, n, "purge", "refused", "confirmation_required")
	n++
	if got := client.do("purge", form(a.Revision+1, true)); got != "revision_changed" {
		t.Fatalf("stale purge: %s", got)
	}
	expectAudit(t, s, n, "purge", "refused", "revision_changed")
	n++
	if got := client.do("purge", url.Values{"family": {"maintainer"}, "id": {"maintainer"}, "revision": {"1"}, "confirm": {"purge"}}); got != "maintainer_session" {
		t.Fatalf("maintainer purge: %s", got)
	}
	expectAudit(t, s, n, "purge", "refused", "maintainer_session")
	n++
	if got := client.do("purge", form(a.Revision, true)); got != "purge_marked" {
		t.Fatalf("purge: %s", got)
	}
	expectAudit(t, s, n, "purge", "accepted", "")
	n++
	marked := sessionState(t, s, ka)
	if marked.PurgeAt == 0 || marked.State != "retired" {
		t.Fatalf("not marked: %+v", marked)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if !strings.Contains(page.body, "marked for purge") || !strings.Contains(page.body, "/dashboard/actions/unpurge") {
		t.Fatal("the sessions view does not show the mark")
	}
	if got := client.do("unpurge", form(marked.Revision, false)); got != "purge_unmarked" {
		t.Fatalf("unpurge: %s", got)
	}
	expectAudit(t, s, n, "unpurge", "accepted", "")
	if kept := sessionState(t, s, ka); kept.PurgeAt != 0 {
		t.Fatalf("mark kept: %+v", kept)
	}
	// Dashboard health shows the last sweep's counts.
	if err := s.Cull(context.Background()); err != nil {
		t.Fatal(err)
	}
	health := dashboardDo(t, d, "GET", "/dashboard/health", client.cookie, nil, nil)
	if health.status != 200 || !strings.Contains(health.body, "last sweep") || !strings.Contains(health.body, "0 marked for purge") {
		t.Fatalf("health: %d %s", health.status, health.body)
	}
	// The status reports the periods and the marked sessions.
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := GetStatus(context.Background(), d.Addresses()[0], secret)
	if err != nil || !strings.Contains(string(data), `"retention":{"message_retention_days":30,"session_retention_days":30,`) ||
		!strings.Contains(string(data), `"marked_for_purge":0`) {
		t.Fatalf("status: %s %v", data, err)
	}
}

// Review regressions on #240 (F1): a mark removed after the sweep selected the session,
// before it released the claim, releases nothing.
func TestUnpurgeDuringSweepKeepsClaim(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "cancel")
	peer := join(t, s, "codex", "synthetic-cancel", repo)
	key := Key{"codex", "synthetic-cancel"}
	owner := MemoryCaller{Repository: "/synthetic/cancel/.git", Family: "codex", Name: peer.Name, Consumer: "codex:synthetic-cancel"}
	id := create(t, s, owner, "synthetic cancel")
	mustStart(t, s, owner, id, nil)
	if err := s.markPurge(ctx, key, peer.Revision, true); err != nil {
		t.Fatal(err)
	}
	marked := sessionState(t, s, key)
	// The sweep's first clock read is the expiry check of the selected claim, after it
	// read the marked session and before any release or deletion.
	entered, resume := make(chan struct{}), make(chan struct{})
	var gated atomic.Bool
	current := *clock
	s.now = func() time.Time {
		if gated.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
		return current
	}
	done := make(chan error, 1)
	go func() { done <- s.Cull(ctx) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not reach the claim check")
	}
	err := s.markPurge(ctx, key, marked.Revision, false)
	close(resume)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !sessionExists(t, s, key) {
		t.Fatal("unmarked session deleted")
	}
	if item := get(t, s, owner, id); item["lifecycle"] != "active" {
		t.Fatalf("a cancelled purge released the live claim: %+v", item)
	}
}

// Review regressions on #240 (F2): purge and retention share one budget per sweep, and a
// partial batch never exceeds what is left of it.
func TestMixedDeletionRespectsBudget(t *testing.T) {
	s, clock := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "budget")
	purged := join(t, s, "codex", "synthetic-purge-budget", repo)
	join(t, s, "claude", "synthetic-retention-budget", repo)
	pk, rk := Key{"codex", "synthetic-purge-budget"}, Key{"claude", "synthetic-retention-budget"}
	// Synthetic inboxes in one fixture transaction, not 10,001 sends.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, fixture := range []struct {
		key Key
		n   int
	}{{pk, 1}, {rk, cullBudget}} {
		for seq := 1; seq <= fixture.n; seq++ {
			if _, err := tx.Exec(`INSERT INTO messages(sender_family,sender_id,sender_name,recipient_family,recipient_id,seq,body,created_at,delivery_updated_at)
				VALUES('maintainer','maintainer','maintainer',?,?,?,'synthetic',?,?)`, fixture.key.Family, fixture.key.ID, seq, clock.UnixMilli(), clock.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(`UPDATE sessions SET last_seq=? WHERE family=? AND id=?`, fixture.n, fixture.key.Family, fixture.key.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(ctx, rk, cullBudget); err != nil {
		t.Fatal(err)
	}
	cull(t, s)
	*clock = clock.Add(messageRetention + time.Minute)
	keepAlive(t, s, rk)
	if err := s.markPurge(ctx, pk, purged.Revision, true); err != nil {
		t.Fatal(err)
	}
	got := cull(t, s)
	if got.MessagesByPurge != 1 || got.MessagesByRetention != cullBudget-1 {
		t.Fatalf("the sweep did not stop at the budget of %d: %+v", cullBudget, got)
	}
	// The next sweep deletes the rest and clears the mark.
	if got := cull(t, s); got.MessagesByRetention != 1 || messageCount(t, s, rk) != 0 {
		t.Fatalf("the next sweep: %+v, %d left", got, messageCount(t, s, rk))
	}
	if mark := count(t, s, `SELECT ack_mark_at FROM sessions WHERE family=? AND id=?`, rk.Family, rk.ID); mark != 0 {
		t.Fatalf("mark left: %d", mark)
	}
}
