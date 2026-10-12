package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkNoticesRestartKeepsThePendingHead(t *testing.T) {
	s, clock, creator, _, _, worker := noticePair(t)
	id := createRoleWork(t, s, creator, "codex", "worker")
	mustReconcileNotices(t, s)
	root := filepath.Dir(s.storage.path)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.now = func() time.Time { return *clock }
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) != 1 {
		t.Fatal("restart duplicated pending notice")
	}
	mustWork(t, s, creator, "work-edit", map[string]any{"work_id": id, "if_revision": revision(t, s, creator, id), "criteria": "synthetic next scope"})
	mustReconcileNotices(t, s)
	if _, _, err := s.AckParticipant(context.Background(), Key{worker.Family, worker.ID}, 1); err != nil {
		t.Fatal(err)
	}
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) != 2 {
		t.Fatal("restart lost the later head")
	}
}

func TestWorkNoticeRefusalCannotInvalidateAFundedVerdict(t *testing.T) {
	s, _, creator, owner, _, _ := noticePair(t)
	id := createRoleWork(t, s, creator, "codex", "worker")
	seq := mustReport(t, s, owner, id, mustStart(t, s, owner, id, nil))
	u, err := usage(context.Background(), s.db, creator.Repository)
	if err != nil {
		t.Fatal(err)
	}
	d, err := storeDebt(context.Background(), s.db, creator.Repository)
	if err != nil {
		t.Fatal(err)
	}
	l := s.limits()
	s.memory.limits.logical = u.Logical + l.reservedBytes + d.verdict*l.verdictBytes
	if err := s.ReconcileWorkNotices(context.Background()); err == nil {
		t.Fatal("notice was admitted at the ordinary ceiling")
	}
	if count(t, s, `SELECT COUNT(*) FROM work_audiences`) != 0 || verdictCredits(t, s) != 1 || get(t, s, creator, id)["lifecycle"] != "reported" {
		t.Fatal("notice refusal changed the funded report")
	}
	if _, err := verdict(s, creator, "work-accept", id, seq, nil); err != nil {
		t.Fatalf("funded verdict: %v", err)
	}
	s.memory.limits.logical = l.logical
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) == 0 {
		t.Fatal("durable events did not recover the notice")
	}
}

func TestWorkNoticesProductionCallbackUsesTheExistingWake(t *testing.T) {
	d, err := Start(Config{StateDir: filepath.Join(t.TempDir(), "state"), Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo := namedRepo(t, "production-work-notice")
	creator := joinAs(t, d.store, "claude", "synthetic-creator", repo, "")
	worker := joinAs(t, d.store, "codex", "synthetic-worker", repo, "worker")
	received := make(chan string, 4)
	d.store.wake.mu.Lock()
	d.store.wake.send = func(_ context.Context, s Session, notice string) wakeResult {
		if s.Family != worker.Family || s.ID != worker.ID {
			t.Errorf("wrong native target: %s:%s", s.Family, s.ID)
		}
		received <- notice
		return accepted()
	}
	d.store.wake.mu.Unlock()
	createRoleWork(t, d.store, participantCaller(t, d.store, Key{creator.Family, creator.ID}), "codex", "worker")
	select {
	case notice := <-received:
		if !strings.HasPrefix(notice, "Koinon inbox") || strings.Contains(notice, "synthetic role") {
			t.Fatalf("provider notice: %s", notice)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("production callback did not wake")
	}
	inbox := participantNotices(t, d.store, Key{worker.Family, worker.ID})
	if len(inbox.Messages) != 1 || inbox.Messages[0].SenderFamily != "daemon" {
		t.Fatalf("production inbox: %+v", inbox)
	}
}

func TestWorkNoticeProviderRetryKeepsOneMessage(t *testing.T) {
	s, clock, creator, _, _, worker := noticePair(t)
	createRoleWork(t, s, creator, "codex", "worker")
	mustReconcileNotices(t, s)
	calls := 0
	s.wake.send = func(_ context.Context, target Session, _ string) wakeResult {
		calls++
		if target.ID != worker.ID {
			t.Errorf("wrong target: %s", target.ID)
		}
		if calls == 1 {
			return wakeResult{"waiting", "synthetic_failure"}
		}
		return accepted()
	}
	if err := s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(2 * time.Second)
	mustReconcileNotices(t, s)
	if err := s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) != 1 {
		t.Fatalf("retry calls=%d", calls)
	}
}

func TestWorkNoticeTargetsEveryNativeFamily(t *testing.T) {
	for _, family := range SupportedFamilies() {
		t.Run(family, func(t *testing.T) {
			s, _, _ := workStore(t)
			repo := namedRepo(t, "family-notice")
			creatorFamily := "claude"
			if family == "claude" {
				creatorFamily = "codex"
			}
			creator := joinAs(t, s, creatorFamily, "synthetic-creator", repo, "")
			var worker Session
			var err error
			if family == "deepseek" {
				worker, err = s.Register(context.Background(), Registration{Family: family, ID: "synthetic-worker", Repository: repo, Directory: repo, TTLSeconds: 60})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				worker = joinAs(t, s, family, "synthetic-worker", repo, "")
			}
			createRoleWork(t, s, participantCaller(t, s, Key{creator.Family, creator.ID}), family, "")
			mustReconcileNotices(t, s)
			calls := 0
			s.wake.send = func(_ context.Context, target Session, notice string) wakeResult {
				calls++
				if target.Family != family || target.ID != worker.ID || !strings.HasPrefix(notice, "Koinon inbox") {
					t.Errorf("native target/notice: %+v %s", target, notice)
				}
				return accepted()
			}
			if family == "agy" {
				s.wake.mu.Lock()
				notice, err := s.offerWake(context.Background(), Key{family, worker.ID})
				s.wake.mu.Unlock()
				if err != nil || !strings.HasPrefix(notice, "Koinon inbox") {
					t.Fatalf("hook offer: %s %v", notice, err)
				}
				calls++
			} else if err := s.wakeStep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("native provider calls: %d", calls)
			}
		})
	}
}

func mustReconcileNotices(t *testing.T, s *Store) {
	t.Helper()
	if err := s.ReconcileWorkNotices(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func noticePair(t *testing.T) (*Store, *time.Time, MemoryCaller, MemoryCaller, Session, Session) {
	t.Helper()
	s, clock, _ := workStore(t)
	repo := namedRepo(t, "work-notices")
	creator := joinAs(t, s, "claude", "synthetic-creator", repo, "")
	worker := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
	return s, clock, participantCaller(t, s, Key{creator.Family, creator.ID}), participantCaller(t, s, Key{worker.Family, worker.ID}), creator, worker
}

func participantNotices(t *testing.T, s *Store, k Key) Inbox {
	t.Helper()
	after := int64(0)
	inbox, err := s.ReadInboxes(context.Background(), k, 0, &after, 100)
	if err != nil {
		t.Fatal(err)
	}
	return inbox
}

func TestWorkNoticesCoalesceAndSeparateReadFromAck(t *testing.T) {
	s, _, creator, _, _, worker := noticePair(t)
	id := createRoleWork(t, s, creator, "codex", "worker")
	mustReconcileNotices(t, s)
	k := Key{worker.Family, worker.ID}
	first := participantNotices(t, s, k)
	if len(first.Messages) != 1 {
		t.Fatalf("first notice: %+v", first)
	}
	n := first.Messages[0]
	if n.SenderFamily != "daemon" || n.SenderName != "koinon-daemon" || strings.Contains(n.Body, "synthetic role") || !strings.Contains(n.Body, "head") {
		t.Fatalf("notice provenance/content: %+v", n)
	}
	// A changed scope after the inbox read still uses the one pending message.
	mustWork(t, s, creator, "work-edit", map[string]any{"work_id": id, "if_revision": revision(t, s, creator, id), "criteria": "changed synthetic criteria"})
	mustReconcileNotices(t, s)
	if got := count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`); got != 1 {
		t.Fatalf("read was treated as acknowledgement: %d", got)
	}
	if _, _, err := s.AckParticipant(context.Background(), k, n.Seq); err != nil {
		t.Fatal(err)
	}
	mustReconcileNotices(t, s)
	second := participantNotices(t, s, k)
	if len(second.Messages) != 2 || second.Messages[1].Body == n.Body {
		t.Fatalf("changed head after ack: %+v", second)
	}
	mustReconcileNotices(t, s)
	if got := count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`); got != 2 {
		t.Fatalf("duplicate reconciliation: %d", got)
	}
}

func TestWorkNoticesDoNotLoopOnWorkerProgress(t *testing.T) {
	s, _, creator, owner, coordinator, worker := noticePair(t)
	id := createRoleWork(t, s, creator, "codex", "worker")
	mustReconcileNotices(t, s)
	k := Key{worker.Family, worker.ID}
	if _, _, err := s.AckParticipant(context.Background(), k, 1); err != nil {
		t.Fatal(err)
	}
	generation := mustStart(t, s, owner, id, nil)
	mustReconcileNotices(t, s)
	for i := 0; i < 3; i++ {
		mustWork(t, s, owner, "work-update", map[string]any{"work_id": id, "if_revision": revision(t, s, owner, id),
			"claim_generation": generation, "progress": "synthetic progress", "checkpoint": "synthetic checkpoint",
			"next_artifact": "synthetic next", "progress_deadline": now(s) + 600})
		mustReconcileNotices(t, s)
	}
	mustWork(t, s, owner, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, owner, id),
		"claim_generation": generation, "checkpoint": "synthetic released"})
	mustReconcileNotices(t, s)
	if inbox := participantNotices(t, s, k); len(inbox.Messages) != 1 {
		t.Fatalf("worker claim/progress/release caused a wake loop: %+v", inbox)
	}
	// A coordinator's changed scope still reaches the worker.
	mustWork(t, s, creator, "work-edit", map[string]any{"work_id": id, "if_revision": revision(t, s, creator, id), "criteria": "synthetic external change"})
	mustReconcileNotices(t, s)
	if inbox := participantNotices(t, s, k); len(inbox.Messages) != 2 {
		t.Fatalf("coordinator change did not notify worker: %+v", inbox)
	}
	if _, _, err := s.AckParticipant(context.Background(), k, 2); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, owner, id, mustStart(t, s, owner, id, nil))
	mustReconcileNotices(t, s)
	if inbox := participantNotices(t, s, k); len(inbox.Messages) != 2 {
		t.Fatalf("worker's report notified itself: %+v", inbox)
	}
	if inbox := participantNotices(t, s, Key{coordinator.Family, coordinator.ID}); len(inbox.Messages) != 1 {
		t.Fatalf("creator missed worker's report: %+v", inbox)
	}
}

func TestWorkAudienceCleanupKeepsRowsAfterAnUnlockedHistoryChange(t *testing.T) {
	s, _, creator, _, _, _ := noticePair(t)
	create(t, s, creator, "synthetic unassigned seed")
	store, err := storeOf(context.Background(), s.db, creator.Repository)
	if err != nil {
		t.Fatal(err)
	}
	a := workAudience{Kind: "role", Family: "codex", Role: "worker"}
	if _, err := s.db.Exec(`INSERT INTO work_audiences(store,audience,data,head) VALUES(?,?,?,1)`, store, a.key(), a.encoding()); err != nil {
		t.Fatal(err)
	}
	var observedHead int64
	if err := s.db.QueryRow(`SELECT head FROM memory_stores WHERE store_id=?`, store).Scan(&observedHead); err != nil {
		t.Fatal(err)
	}
	heads, err := s.audienceHeads(context.Background(), store, now(s))
	if err != nil {
		t.Fatal(err)
	}
	// A work change commits after the unlocked scan, making its unused row relevant.
	createRoleWork(t, s, creator, "codex", "worker")
	if err := s.cleanWorkAudiences(context.Background(), creator.Repository, store, observedHead, heads); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM work_audiences`) != 1 {
		t.Fatal("cleanup removed a newly relevant audience from a stale snapshot")
	}
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) != 1 {
		t.Fatal("next pass did not deliver the newly relevant head")
	}
}

func TestWorkNoticesReportCreatorVerdictWorkerAndSuccession(t *testing.T) {
	s, _, creator, owner, coordinator, worker := noticePair(t)
	id := createRoleWork(t, s, creator, "codex", "worker")
	gen := mustStart(t, s, owner, id, nil)
	seq := mustReport(t, s, owner, id, gen)
	mustReconcileNotices(t, s)
	if inbox := participantNotices(t, s, Key{coordinator.Family, coordinator.ID}); len(inbox.Messages) != 1 {
		t.Fatalf("creator report notice: %+v", inbox)
	}
	// Clear the current assignment; the report's participant provenance is independent.
	mustWork(t, s, creator, "work-propose", map[string]any{"work_id": id, "if_revision": revision(t, s, creator, id), "proposed_role": nil})
	mustReconcileNotices(t, s)
	inbox := participantNotices(t, s, Key{worker.Family, worker.ID})
	if _, _, err := s.AckParticipant(context.Background(), Key{worker.Family, worker.ID}, inbox.Messages[len(inbox.Messages)-1].Seq); err != nil {
		t.Fatal(err)
	}
	mustReconcileNotices(t, s)
	inbox = participantNotices(t, s, Key{worker.Family, worker.ID})
	if _, _, err := s.AckParticipant(context.Background(), Key{worker.Family, worker.ID}, inbox.Messages[len(inbox.Messages)-1].Seq); err != nil {
		t.Fatal(err)
	}
	next := joinAs(t, s, "codex", "synthetic-successor", worker.Repository, "worker")
	if err := s.ChooseHolder(context.Background(), worker.Address, Key{next.Family, next.ID}, next.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := verdict(s, creator, "work-accept", id, seq, nil); err != nil {
		t.Fatal(err)
	}
	mustReconcileNotices(t, s)
	got := participantNotices(t, s, Key{next.Family, next.ID})
	if len(got.Messages) < 1 || got.Messages[len(got.Messages)-1].Acknowledged {
		t.Fatalf("successor missed verdict: %+v", got)
	}
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles WHERE active=1`) != 0 {
		t.Fatal("notification transferred ownership")
	}
}

func TestWorkNoticesUnheldCustomAndForeignAudiences(t *testing.T) {
	s, _, creator, _, _, worker := noticePair(t)
	createRoleWork(t, s, creator, "grok", "unheld")
	id := create(t, s, creator, "synthetic custom")
	mustWork(t, s, creator, "work-propose", map[string]any{"work_id": id, "if_revision": 1, "proposed_assignee": "opaque-synthetic-consumer"})
	foreignRepo := namedRepo(t, "foreign-notices")
	foreign := joinAs(t, s, "codex", "synthetic-foreign", foreignRepo, "")
	otherID := create(t, s, creator, "synthetic foreign")
	mustWork(t, s, creator, "work-propose", map[string]any{"work_id": otherID, "if_revision": 1, "proposed_assignee": "participant:" + foreign.Address})
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon'`) != 0 {
		t.Fatal("unresolved/foreign audience received a notice")
	}
	consumer := "opaque-synthetic-consumer"
	m, err := s.ResolveMemoryCaller(context.Background(), Key{worker.Family, worker.ID}, &consumer)
	if err != nil {
		t.Fatal(err)
	}
	drain(t, s, m)
	mustReconcileNotices(t, s)
	inbox, err := s.ReadInbox(context.Background(), Key{worker.Family, worker.ID}, 0, 100)
	if err != nil || len(inbox.Messages) != 1 {
		t.Fatalf("verified custom actor: %+v %v", inbox, err)
	}
}

func TestWorkNoticesAudienceSaturationAndCompleteFallback(t *testing.T) {
	s, _, creator, worker, _, _ := noticePair(t)
	store, err := storeOf(context.Background(), s.db, creator.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if store == "" {
		create(t, s, creator, "synthetic seed")
		store, _ = storeOf(context.Background(), s.db, creator.Repository)
	}
	// Unused rows have pending notices, so reconciliation cannot evict them.
	for i := 0; i < maxWorkAudiences; i++ {
		a := workAudience{Kind: "consumer", Consumer: fmt.Sprintf("synthetic-held-%d", i)}
		k := Key{Family: "codex", ID: participantKey(worker.Consumer[len(participantPrefix):])}
		if err := ensureSyntheticPendingAudience(t, s, store, a, k); err != nil {
			t.Fatal(err)
		}
	}
	id := createRoleWork(t, s, creator, "codex", "worker")
	before := count(t, s, `SELECT COUNT(*) FROM messages`)
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM work_audiences`) != maxWorkAudiences || count(t, s, `SELECT COUNT(*) FROM messages`) != before {
		t.Fatal("saturation evicted or allocated")
	}
	status, err := s.workNoticeStatus(context.Background(), creator.Repository)
	if err != nil || status["saturated"] != true || status["missing_audiences"].(int64) != 1 {
		t.Fatalf("saturation status: %v %v", status, err)
	}
	items := mustWork(t, s, worker, "work-list", map[string]any{"assigned_to_me": true})["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["work_id"] != id {
		t.Fatalf("missing-row query fallback: %v", items)
	}
	// Acknowledge one synthetic pending message, making exactly one row eligible.
	if _, err := s.db.Exec(`UPDATE sessions SET acked_through=1 WHERE family='codex' AND substr(id,1,12)='participant:'`); err != nil {
		t.Fatal(err)
	}
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM work_audiences`) != maxWorkAudiences || count(t, s, `SELECT COUNT(*) FROM messages`) != before+1 {
		t.Fatal("eligible row was not reclaimed/delivered once")
	}
	mustReconcileNotices(t, s)
	if count(t, s, `SELECT COUNT(*) FROM messages`) != before+1 {
		t.Fatal("recovered head duplicated")
	}
}

func ensureSyntheticPendingAudience(t *testing.T, s *Store, store string, a workAudience, k Key) error {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureParticipantInbox(context.Background(), tx, s.now().UnixMilli(), k.ID[len(participantPrefix):]); err != nil {
		return err
	}
	var seq int64
	if err := tx.QueryRow(`UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq`, k.Family, k.ID).Scan(&seq); err != nil {
		return err
	}
	r, err := tx.Exec(`INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,created_at,delivery_updated_at) VALUES(?,?,?,'daemon','work','koinon-daemon','synthetic pointer',?,?)`, k.Family, k.ID, seq, s.now().UnixMilli(), s.now().UnixMilli())
	if err != nil {
		return err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO work_audiences(store,audience,data,head,notice_head,message_id) VALUES(?,?,?,1,1,?)`, store, a.key(), a.encoding(), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func TestWorkCreatorQueriesPageBeyondAnAdvancedCursor(t *testing.T) {
	s, _, creator, _, _, _ := noticePair(t)
	for i := 0; i < 110; i++ {
		create(t, s, creator, fmt.Sprintf("synthetic item %d", i))
	}
	drain(t, s, creator)
	seen := 0
	after := ""
	for {
		fields := map[string]any{"created_by_me": true, "limit": 7}
		if after != "" {
			fields["after"] = after
		}
		page := mustWork(t, s, creator, "work-list", fields)
		seen += len(page["items"].([]any))
		if page["more"] == false {
			break
		}
		after = page["next_after"].(string)
	}
	if seen != 110 {
		t.Fatalf("advanced cursor hid created work: %d", seen)
	}
}
