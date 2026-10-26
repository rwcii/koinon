package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Agent withdrawal of an active item (#504). All data is synthetic.

// TestDecisionWithdraw withdraws a waiting decision item and an open fyi item with a reason:
// each moves to the archive with the withdrawing peer, the reason and no verdict, a retry
// returns the first result, and a changed retry conflicts.
func TestDecisionWithdraw(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	deadline := s.clock() + 600
	for _, kind := range []string{"decision", "fyi"} {
		reference := "W-" + kind
		mustCreate(t, s, f.peerKey, reference, decisionFields("Withdraw me", kind))
		got, err := s.DecisionWithdraw(ctx, f.peerKey, reference, 1, "superseded by W-2", "w-"+kind, deadline)
		if err != nil || got.State != "archived" || !got.Withdrawn || got.Verdict != nil || got.ArchivedAt == 0 || got.Revision != 1 ||
			got.WithdrawalCount != 1 || len(got.Withdrawals) != 1 {
			t.Fatalf("%s: %+v %v", kind, got, err)
		}
		if w := got.Withdrawals[0]; w.By != f.peer.Name || w.Family != "claude" || w.Reason != "superseded by W-2" || w.Revision != 1 || w.ReopenedAt != 0 {
			t.Fatalf("%s withdrawal: %+v", kind, w)
		}
		again, err := s.DecisionWithdraw(ctx, f.peerKey, reference, 1, "superseded by W-2", "w-"+kind, deadline)
		if err != nil || again.State != "archived" || again.Withdrawals[0].At != got.Withdrawals[0].At {
			t.Fatalf("%s retry: %+v %v", kind, again, err)
		}
		if _, err := s.DecisionWithdraw(ctx, f.peerKey, reference, 1, "done", "w-"+kind, deadline); refusalCode(err) != "idempotency_conflict" {
			t.Fatalf("%s changed retry: %v", kind, err)
		}
		if _, err := s.DecisionWithdraw(ctx, f.peerKey, reference, 1, "done", "w2-"+kind, deadline); refusalCode(err) != "not_active" {
			t.Fatalf("%s second withdrawal: %v", kind, err)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_verdicts`)+count(t, s, `SELECT COUNT(*) FROM decision_deliveries`) != 0 {
		t.Fatal("a withdrawal wrote a verdict or a delivery")
	}
	if count(t, s, `SELECT COUNT(*) FROM audit WHERE action LIKE 'decision%'`) != 0 {
		t.Fatal("a withdrawal wrote a maintainer audit record")
	}
	page, err := s.DecisionList(ctx, f.peerKey, "archived", 0, 0)
	if err != nil || len(page.Items) != 2 || !page.Items[0].Withdrawn || page.Items[0].Verdict != "" {
		t.Fatalf("archived list: %+v %v", page, err)
	}
}

// TestDecisionWithdrawRefusals refuses a stale revision, an item with a verdict, a blank or
// multi-line reason, a credential in the reason, a caller that is not a party of the item,
// and an expired retry deadline.
func TestDecisionWithdrawRefusals(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	deadline := s.clock() + 600
	mustCreate(t, s, f.peerKey, "R-1", decisionFields("Refusals", "decision"))
	for name, c := range map[string]struct {
		revision int64
		reason   string
		deadline float64
		want     string
	}{
		"stale":      {2, "done", deadline, "stale_revision"},
		"blank":      {1, "   ", deadline, "invalid_request"},
		"empty":      {1, "", deadline, "invalid_request"},
		"lines":      {1, "done\nreally", deadline, "invalid_request"},
		"credential": {1, "ghp_" + strings.Repeat("a", 36), deadline, "credential_refused"},
		"long":       {1, strings.Repeat("a", decisionWithdrawReasonLimit+1), deadline, "invalid_request"},
		"expired":    {1, "done", s.clock() - 1, "retry_deadline_expired"},
	} {
		if _, err := s.DecisionWithdraw(ctx, f.peerKey, "R-1", c.revision, c.reason, "r-"+name, c.deadline); refusalCode(err) != c.want {
			t.Errorf("%s: %v, want %s", name, err, c.want)
		}
	}
	// The codex sessions are neither the author nor named.
	if _, err := s.DecisionWithdraw(ctx, f.otherKey, "R-1", 1, "done", "r-other", deadline); refusalCode(err) != "not_item_party" {
		t.Fatalf("unrelated caller: %v", err)
	}
	// An item with a verdict is not active.
	mustVerdict(t, s, "R-1", 1, "approved")
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "R-1", 1, "done", "r-verdict", deadline); refusalCode(err) != "not_active" {
		t.Fatalf("approved item: %v", err)
	}
	if got, err := s.DecisionGet(ctx, f.peerKey, "R-1"); err != nil || got.State != "in_progress" || got.WithdrawalCount != 0 {
		t.Fatalf("after refusals: %+v %v", got, err)
	}
}

// TestDecisionWithdrawParties lets a named recipient (by held address), the author and a
// session of the author's participant withdraw an item; the participant's other session
// qualifies as the author's successor, not as a recipient.
func TestDecisionWithdrawParties(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	deadline := s.clock() + 600
	// The claude peer files an item for the codex participant: its holder withdraws it, the
	// participant's non-holder session cannot.
	mustCreate(t, s, f.peerKey, "P-1", DecisionFields{Title: ptr("For codex"), Kind: ptr("fyi"), Recipients: &[]string{f.address}})
	if _, err := s.DecisionWithdraw(ctx, f.otherKey, "P-1", 1, "done", "p-other", deadline); refusalCode(err) != "not_item_party" {
		t.Fatalf("non-holder recipient: %v", err)
	}
	if got, err := s.DecisionWithdraw(ctx, f.holderKey, "P-1", 1, "answered in chat", "p-holder", deadline); err != nil || got.Withdrawals[0].By != f.holder.Name {
		t.Fatalf("holder: %+v %v", got, err)
	}
	// The holder files an item: the participant's other session withdraws it as the author's
	// successor, and the claude peer cannot.
	mustCreate(t, s, f.holderKey, "P-2", decisionFields("By codex", "decision"))
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "P-2", 1, "done", "p-peer", deadline); refusalCode(err) != "not_item_party" {
		t.Fatalf("unrelated peer: %v", err)
	}
	if got, err := s.DecisionWithdraw(ctx, f.otherKey, "P-2", 1, "no longer valid", "p-successor", deadline); err != nil || got.Withdrawals[0].By != f.other.Name {
		t.Fatalf("successor: %+v %v", got, err)
	}
}

// TestDecisionWithdrawRaces runs a withdrawal against a verdict on the same revision: the
// first write wins and the second is refused, so a verdict never lands on a withdrawn item
// and a withdrawal never archives a decided one.
func TestDecisionWithdrawRaces(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	deadline := s.clock() + 600
	mustCreate(t, s, f.peerKey, "X-1", decisionFields("Verdict first", "decision"))
	mustVerdict(t, s, "X-1", 1, "declined")
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "X-1", 1, "done", "x-1", deadline); refusalCode(err) != "not_active" {
		t.Fatalf("withdraw after the verdict: %v", err)
	}
	mustCreate(t, s, f.peerKey, "X-2", decisionFields("Withdrawal first", "decision"))
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "X-2", 1, "done", "x-2", deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "X-2", 1, "approved", ""); refusalCode(err) == "" {
		t.Fatal("a verdict landed on a withdrawn item")
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_verdicts v JOIN decision_items i ON i.id=v.item_id WHERE i.reference='X-2'`) != 0 {
		t.Fatal("verdict row on the withdrawn item")
	}
}

// TestDecisionWithdrawReopen reopens a withdrawn item with decision_restore at its revision:
// it is active at the next revision, takes a verdict there, and its history keeps the
// withdrawal with the reopen. Restore without if_revision, at a stale revision or by an
// unrelated caller is refused.
func TestDecisionWithdrawReopen(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	deadline := s.clock() + 600
	mustCreate(t, s, f.peerKey, "O-1", decisionFields("Reopen me", "decision"))
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "O-1", 1, "done", "o-1", deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "O-1", 0); refusalCode(err) != "invalid_request" {
		t.Fatalf("restore without if_revision: %v", err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "O-1", 2); refusalCode(err) != "stale_revision" {
		t.Fatalf("stale reopen: %v", err)
	}
	if _, err := s.DecisionRestore(ctx, f.otherKey, "O-1", 1); refusalCode(err) != "not_item_party" {
		t.Fatalf("unrelated reopen: %v", err)
	}
	if _, err := fixtureExec(s, `UPDATE sessions SET expires_at=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Minute)
	got, err := s.DecisionRestore(ctx, f.peerKey, "O-1", 1)
	if err != nil || got.State != "active" || got.Revision != 2 || got.Withdrawn || got.ArchivedAt != 0 || got.WithdrawalCount != 1 ||
		got.Withdrawals[0].ReopenedAt != s.now().UnixMilli() || got.Withdrawals[0].ReopenedBy != f.peer.Name || got.Withdrawals[0].Reason != "done" {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "O-1", 2); refusalCode(err) != "not_found" {
		t.Fatalf("reopen of an active item: %v", err)
	}
	// A withdrawal and a reopen again, then the verdict on the current revision.
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "O-1", 2, "superseded by O-2", "o-2", deadline); err != nil {
		t.Fatal(err)
	}
	if got, err = s.DecisionRestore(ctx, f.peerKey, "O-1", 2); err != nil || got.Revision != 3 || got.WithdrawalCount != 2 ||
		got.Withdrawals[0].Reason != "superseded by O-2" || got.Withdrawals[1].Reason != "done" {
		t.Fatalf("second reopen: %+v %v", got, err)
	}
	if v := mustVerdict(t, s, "O-1", 3, "approved"); v.Revision != 3 {
		t.Fatalf("verdict: %+v", v)
	}
	// An archived item that no agent withdrew keeps the deep-archive meaning of restore.
	mustCreate(t, s, f.peerKey, "O-2", decisionFields("Declined", "decision"))
	mustVerdict(t, s, "O-2", 1, "declined")
	if _, err := s.DecisionRestore(ctx, f.peerKey, "O-2", 1); refusalCode(err) != "not_found" {
		t.Fatalf("restore of a declined item: %v", err)
	}
}

// TestDecisionWithdrawEditReopen reopens a withdrawn item with an edit: the withdrawal records
// the reopen, so a later decline archives the item as declined, not withdrawn, and restore
// does not reopen it.
func TestDecisionWithdrawEditReopen(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "E-1", decisionFields("Edit me", "decision"))
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "E-1", 1, "no longer valid", "e-1", s.clock()+600); err != nil {
		t.Fatal(err)
	}
	got, err := s.DecisionEdit(ctx, f.peerKey, "E-1", 1, DecisionFields{Context: ptr("New facts.")})
	if err != nil || got.State != "active" || got.Withdrawn || got.Withdrawals[0].ReopenedAt == 0 || got.Withdrawals[0].ReopenedBy != f.peer.Name {
		t.Fatalf("edit reopen: %+v %v", got, err)
	}
	mustVerdict(t, s, "E-1", 2, "declined")
	if got, err = s.DecisionGet(ctx, f.peerKey, "E-1"); err != nil || got.Withdrawn || got.State != "archived" {
		t.Fatalf("declined: %+v %v", got, err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "E-1", 2); refusalCode(err) != "not_found" {
		t.Fatalf("restore of the declined item: %v", err)
	}
	page, err := s.DecisionList(ctx, f.peerKey, "archived", 0, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Withdrawn || page.Items[0].Verdict != "declined" {
		t.Fatalf("archived list: %+v %v", page, err)
	}
}

// TestDecisionWithdrawalDeepArchive moves a withdrawn item to the deep archive and back: the
// line holds the withdrawal, the restore brings it back as archived and withdrawn, and a
// second restore reopens it.
func TestDecisionWithdrawalDeepArchive(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "Q-1", decisionFields("Old", "fyi"))
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "Q-1", 1, "answered in chat", "q-1", s.clock()+600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(s, `UPDATE sessions SET expires_at=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(31 * 24 * time.Hour)
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items`)+count(t, s, `SELECT COUNT(*) FROM decision_withdrawals`) != 0 {
		t.Fatal("rows remain after the move")
	}
	if lines := logLines(t, s); len(lines) != 1 || !strings.Contains(lines[0], `"withdrawals":[{`) || !strings.Contains(lines[0], "answered in chat") {
		t.Fatalf("log: %v", lines)
	}
	restored, err := s.DecisionRestore(ctx, f.peerKey, "Q-1", 0)
	if err != nil || restored.State != "archived" || !restored.Withdrawn || restored.WithdrawalCount != 1 || restored.Withdrawals[0].Reason != "answered in chat" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if reopened, err := s.DecisionRestore(ctx, f.peerKey, "Q-1", 1); err != nil || reopened.State != "active" || reopened.Revision != 2 {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
}

// TestDecisionWithdrawalOnTheBoard shows a withdrawn item in the archive as withdrawn, its
// lookup with the withdrawal and its reason, and the reopened card with the reopen.
func TestDecisionWithdrawalOnTheBoard(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-withdraw-board", "")
	key := Key{"claude", peer.ID}
	ctx := context.Background()
	mustCreate(t, d.store, key, "B-1", decisionFields("Stale card", "fyi"))
	if _, err := d.store.DecisionWithdraw(ctx, key, "B-1", 1, "answered in chat", "b-1", d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		t.Helper()
		page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		if page.status != 200 {
			t.Fatalf("%s: %d %s", path, page.status, page.body)
		}
		return page.body
	}
	if body := get("/dashboard/decisions?status=archived"); !strings.Contains(body, `<td>Stale card</td><td>fyi</td><td>withdrawn</td>`) {
		t.Errorf("archive row: %s", body)
	}
	section := lookupSection(t, get("/dashboard/decisions?ref=B-1"))
	for _, want := range []string{"withdrawn by an agent", "<h4>Withdrawals (1, newest first)</h4>", "Withdrawn by " + peer.Name + " (claude) on revision 1",
		": answered in chat</li>", "Read only."} {
		if !strings.Contains(section, want) {
			t.Errorf("lookup: no %q in %s", want, section)
		}
	}
	if _, err := d.store.DecisionRestore(ctx, key, "B-1", 1); err != nil {
		t.Fatal(err)
	}
	card := cardOf(t, get("/dashboard/decisions"), "B-1")
	if !strings.Contains(card, ": answered in chat. Reopened by "+peer.Name) || !strings.Contains(card, "decision-dismiss") {
		t.Errorf("reopened card: %s", card)
	}
}

// Schema 33 adds the withdrawal table to a schema 32 store and keeps every retained row.
func TestSchemaThirtyThreeAddsWithdrawals(t *testing.T) {
	root := t.TempDir()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a := join(t, s, "codex", "synthetic-a", "")
	mustCreate(t, s, Key{"codex", a.ID}, "M-1", decisionFields("Kept", "fyi"))
	if _, err := fixtureExec(s, undoSchemaThirtyThree+`PRAGMA user_version=32`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 32); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, s.db)
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != rows ||
		count(t, upgraded, `SELECT COUNT(*) FROM decision_withdrawals`) != 0 {
		t.Fatal("schema 33 upgrade")
	}
	if err := checkCatalog(upgraded.db, 32); err == nil {
		t.Fatal("a schema 32 writer would accept the schema 33 store")
	}
	if got, err := upgraded.DecisionWithdraw(context.Background(), Key{"codex", a.ID}, "M-1", 1, "done", "m-1", upgraded.clock()+600); err != nil || !got.Withdrawn {
		t.Fatalf("withdraw after the upgrade: %+v %v", got, err)
	}
}
