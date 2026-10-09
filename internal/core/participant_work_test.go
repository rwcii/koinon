package core

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func participantCaller(t *testing.T, s *Store, k Key) MemoryCaller {
	t.Helper()
	m, err := s.ResolveWorkCaller(context.Background(), k, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The participant keeps its cursor and live generation across a holder change. Every
// old resolved caller, explicitly named consumer and keyed retry is fenced before it
// can touch a cursor, return a replay, reconcile work or mutate a claim.
func TestParticipantMemoryWorkContinuityAndFencing(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	ka, kb := Key{a.Family, a.ID}, Key{b.Family, b.ID}
	m := participantCaller(t, s, ka)
	if m.Consumer != "participant:codex-koinon" || m.Native != ka {
		t.Fatalf("holder default: %+v", m)
	}
	peerMemory, err := s.ResolveMemoryCaller(ctx, kb, nil)
	if err != nil || peerMemory.Consumer != b.Name || participantCaller(t, s, kb).Consumer != "codex:synthetic-b" {
		t.Fatalf("non-holder defaults: %+v %v", peerMemory, err)
	}
	explicit, err := s.ResolveMemoryCaller(ctx, ka, &m.Consumer)
	if err != nil || explicit.Consumer != m.Consumer {
		t.Fatalf("explicit participant: %+v %v", explicit, err)
	}
	deadline, recordKey := s.clock()+600, "synthetic-note-retry"
	r := MemoryRecordRequest{Type: "status", Body: "synthetic participant note", Key: &recordKey, Deadline: &deadline}
	recorded, err := s.MemoryRecord(ctx, m, r)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil || snapshot["more"] != false {
		t.Fatalf("snapshot: %v %v", snapshot, err)
	}
	snapshotID := snapshot["snapshot_id"].(string)
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: snapshotID}); err != nil {
		t.Fatal(err)
	}
	createFields := map[string]any{"title": "synthetic task", "criteria": "synthetic criteria", "non_goals": "synthetic non-goals",
		"key": "synthetic-create-retry", "deadline": deadline}
	created := mustWork(t, s, m, "work-create", createFields)
	id := created["work_id"].(string)
	generation := mustStart(t, s, m, id, nil)
	beforeClaim := get(t, s, m, id)["current_claim"]
	if err := s.ChooseHolder(ctx, a.Address, kb, b.Revision); err != nil {
		t.Fatal(err)
	}
	successor := participantCaller(t, s, kb)
	if successor.Consumer != m.Consumer || !reflect.DeepEqual(get(t, s, successor, id)["current_claim"], beforeClaim) {
		t.Fatal("holder change moved or restarted the claim")
	}
	delta, err := s.MemorySync(ctx, successor, MemorySyncRequest{})
	if err != nil || delta["kind"] != "delta" || delta["cursor"] != recorded.Seq {
		t.Fatalf("successor cursor: %v %v", delta, err)
	}
	through := delta["next_cursor"].(int64)
	if _, err := s.MemoryAck(ctx, successor, MemoryAckRequest{Through: &through}); err != nil {
		t.Fatal(err)
	}
	// Registration revives the native peer only; naming a participant explicitly still
	// refuses, and callers resolved before the handoff cannot bypass it.
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	if _, err := s.ResolveWorkCaller(ctx, ka, &m.Consumer); !errors.Is(err, ErrStaleHolder) {
		t.Fatalf("explicit stale consumer: %v", err)
	}
	before := rowsOf(t, s.db)
	head := count(t, s, `SELECT head FROM memory_stores WHERE repository=?`, m.Repository)
	for name, call := range map[string]func() error{
		"sync":            func() error { _, err := s.MemorySync(ctx, m, MemorySyncRequest{}); return err },
		"snapshot replay": func() error { _, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: snapshotID}); return err },
		"ignored ack": func() error {
			zero := int64(0)
			_, err := s.MemoryAck(ctx, m, MemoryAckRequest{Through: &zero})
			return err
		},
		"record replay": func() error { _, err := s.MemoryRecord(ctx, m, r); return err },
		"recall":        func() error { _, err := s.MemoryRecall(ctx, m, "synthetic", 0); return err },
		"status":        func() error { _, err := s.MemoryStatus(ctx, m, ""); return err },
	} {
		if err := call(); !errors.Is(err, ErrStaleHolder) {
			t.Fatalf("stale %s: %v", name, err)
		}
	}
	for _, op := range WorkOperations() {
		fields := map[string]any{"work_id": id}
		if op == "work-create" {
			fields = createFields // a real retained replay from before the handoff
		}
		if _, err := work(s, m, op, fields); !errors.Is(err, ErrStaleHolder) {
			t.Fatalf("stale %s: %v", op, err)
		}
	}
	if got := rowsOf(t, s.db); got != before || count(t, s, `SELECT head FROM memory_stores WHERE repository=?`, m.Repository) != head {
		t.Fatal("a refused participant call wrote state")
	}
	claim := get(t, s, successor, id)["current_claim"].(map[string]any)
	mustWork(t, s, successor, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation,
		"if_claim_revision": claim["revision"], "lease_seconds": 600})
	if _, err := update(s, successor, id, revision(t, s, successor, id), generation, nil); err != nil {
		t.Fatal(err)
	}
	var actor string
	if err := s.db.QueryRow(`SELECT actor FROM work_events WHERE work_id=? ORDER BY seq DESC LIMIT 1`, id).Scan(&actor); err != nil || actor != "codex:synthetic-b" {
		t.Fatalf("work provenance: %q %v", actor, err)
	}
	if err := s.db.QueryRow(`SELECT actor FROM memory_cursors WHERE repository=? AND consumer=?`, m.Repository, m.Consumer).Scan(&actor); err != nil || actor != "codex:synthetic-b" {
		t.Fatalf("cursor provenance: %q %v", actor, err)
	}
	mustWork(t, s, successor, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, successor, id),
		"claim_generation": generation, "checkpoint": "synthetic release"})
	second := create(t, s, successor, "synthetic finish")
	gen := mustStart(t, s, successor, second, nil)
	mustWork(t, s, successor, "work-finish", map[string]any{"work_id": second, "if_revision": revision(t, s, successor, second),
		"claim_generation": gen, "outcome": "completed", "reason": "synthetic done", "references": []string{"synthetic evidence"}})
}

func TestParticipantConsumerCannotCrossRepositoryOrFamily(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	m := participantCaller(t, s, Key{a.Family, a.ID})
	for _, peer := range []Session{
		joinAs(t, s, "claude", "synthetic-other-family", repo, ""),
		joinAs(t, s, "codex", "synthetic-other-repo", namedRepo(t, "other"), ""),
	} {
		if _, err := s.ResolveMemoryCaller(context.Background(), Key{peer.Family, peer.ID}, &m.Consumer); !errors.Is(err, ErrStaleHolder) {
			t.Fatalf("foreign participant consumer accepted: %v", err)
		}
	}
	m.Repository = "synthetic foreign repository"
	if _, err := s.MemorySync(context.Background(), m, MemorySyncRequest{}); !errors.Is(err, ErrStaleHolder) {
		t.Fatalf("cached caller with wrong repository: %v", err)
	}
}

// Checkout roles are held by the participant consumer, but requests go to the exact
// current native holder. Handback still requires the requester's explicit new start.
func TestParticipantCheckoutHandoff(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	c := joinAs(t, s, "claude", "synthetic-requester", repo, "")
	ka, kb, kc := Key{a.Family, a.ID}, Key{b.Family, b.ID}, Key{c.Family, c.ID}
	m := participantCaller(t, s, ka)
	checkout, err := CheckoutResource(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	id := create(t, s, m, "synthetic writer")
	generation := mustStart(t, s, m, id, map[string]any{"resources": [][2]string{checkout.Resource}})
	for _, holder := range []Session{a, b} {
		if holder.ID == b.ID {
			if err := s.ChooseHolder(ctx, a.Address, kb, b.Revision); err != nil {
				t.Fatal(err)
			}
		}
		status, err := s.CheckoutStatus(ctx, kc, repo)
		if err != nil || status.Writer == nil || status.Writer.Peer != holder.Name || status.Writer.Generation != generation || status.Writer.Consumer != m.Consumer {
			t.Fatalf("writer status: %+v %v", status, err)
		}
		if _, err := s.RequestCheckout(ctx, Key{holder.Family, holder.ID}, repo, ""); !errors.Is(err, ErrInvalid) && wcode(err) != "invalid_request" {
			t.Fatalf("holder self-request: %v", err)
		}
		if _, err := s.RequestCheckout(ctx, kc, repo, "synthetic request"); err != nil {
			t.Fatal(err)
		}
		if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_family=? AND recipient_id=?`, holder.Family, holder.ID); n != 1 {
			t.Fatalf("request did not reach current native holder %s: %d", holder.Name, n)
		}
	}
	successor := participantCaller(t, s, kb)
	mustWork(t, s, successor, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, successor, id),
		"claim_generation": generation, "checkpoint": "synthetic committed handback", "handoff_to": c.Name,
		"checkout_resource": checkout.Resource[1]})
	status, err := s.CheckoutStatus(ctx, kc, repo)
	if err != nil || status.State != "released" || status.Writer.LeaseValid {
		t.Fatalf("handoff transferred immediately: %+v %v", status, err)
	}
	requester := participantCaller(t, s, kc)
	newGeneration := mustStart(t, s, requester, id, map[string]any{"resources": [][2]string{checkout.Resource}})
	if newGeneration == generation {
		t.Fatal("handoff acceptance reused the old generation")
	}
	// The claim may be live while its participant has no active holder.
	current := sessionState(t, s, kc)
	if _, err := s.Mutate(ctx, Mutation{Family: kc.Family, ID: kc.ID, IfRevision: current.Revision}, true); err != nil {
		t.Fatal(err)
	}
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	status, err = s.CheckoutStatus(ctx, ka, repo)
	if err != nil || status.Writer == nil || status.Writer.Peer != "" {
		t.Fatalf("unheld participant has a guessed peer: %+v %v", status, err)
	}
	if _, err := s.RequestCheckout(ctx, ka, repo, ""); wcode(err) != "writer_unaddressable" {
		t.Fatalf("request to unheld participant: %v", err)
	}
}
