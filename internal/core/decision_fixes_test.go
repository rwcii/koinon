package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// Regressions from the chunk 02 review (#206, PR 358). All data is synthetic.

// TestDecisionCloseRetryReturnsTheFirstResult: a lost close reply, an edit that reopens the
// item, then the retry with the same key, which returns the first result.
func TestDecisionCloseRetryReturnsTheFirstResult(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "R-1", DecisionFields{Title: ptr("Original"), Kind: ptr("decision")})
	mustVerdict(t, s, "R-1", 1, "approved")
	deadline := s.clock() + 600
	first, err := s.DecisionClose(ctx, f.peerKey, "R-1", 1, "Done.", "close-r1", deadline)
	if err != nil || first.State != "archived" || first.Revision != 1 {
		t.Fatalf("close: %+v %v", first, err)
	}
	if _, err := s.DecisionEdit(ctx, f.peerKey, "R-1", 1, DecisionFields{Title: ptr("Changed facts")}); err != nil {
		t.Fatal(err)
	}
	again, err := s.DecisionClose(ctx, f.peerKey, "R-1", 1, "Done.", "close-r1", deadline)
	if err != nil || again.State != "archived" || again.Revision != 1 || again.Title != "Original" || again.OutcomeNote != "Done." {
		t.Fatalf("retry after an edit: %+v %v", again, err)
	}
	if current, _ := s.decisionRead(ctx, "R-1"); current.State != "active" || current.Revision != 2 {
		t.Fatalf("the retry changed the item: %+v", current)
	}
	if _, err := s.DecisionClose(ctx, f.peerKey, "R-1", 1, "Other note.", "close-r1", deadline); refusalCode(err) != "idempotency_conflict" {
		t.Fatalf("same key, other request: %v", err)
	}
}

// TestDecisionCreateReceiptSurvivesTheUpgrade: a create receipt stored by schema 25 still
// answers its retry after the migration to schema 26.
func TestDecisionCreateReceiptSurvivesTheUpgrade(t *testing.T) {
	root, err := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA auto_vacuum=INCREMENTAL"); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db, 0, 25); err != nil {
		t.Fatal(err)
	}
	fields := decisionFields("Upgrade", "decision")
	deadline := float64(time.Now().Unix() + 600)
	receipt, _ := json.Marshal(DecisionCreated{Reference: "U-1", Revision: 1, State: "active", Recipients: []DecisionRecipient{}})
	if _, err := db.Exec(`INSERT INTO decision_items(reference,item_order,title,location,agent_text,context,recommendation,kind,state,
		commands,revision,author_family,author_name,created_at,updated_at) VALUES ('U-1',0,'Upgrade','','','','','decision','active','[]',1,'codex','synthetic',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO decision_replays(family,caller_id,key,fingerprint,deadline,result) VALUES ('codex','synthetic-upgrade','create-u1',?,?,?)`,
		decisionFingerprint("U-1", fields, deadline), deadline, string(receipt)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "state.sqlite3"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	session := join(t, s, "codex", "synthetic-upgrade", "")
	got, err := s.DecisionCreate(context.Background(), Key{"codex", session.ID}, "U-1", fields, "create-u1", deadline)
	if err != nil || !got.Duplicate || got.Reference != "U-1" || got.Revision != 1 {
		t.Fatalf("retry after the upgrade: %+v %v", got, err)
	}
}

// TestDecisionHiddenCommandBlocksOnlyApprove: a hidden command keeps Approve away, and
// Decline and Dismiss still archive the item.
func TestDecisionHiddenCommandBlocksOnlyApprove(t *testing.T) {
	f := newVerdictFixture(t)
	s := f.s
	mustCreate(t, s, f.peerKey, "H-1", decisionFields("Decide", "decision"))
	mustCreate(t, s, f.peerKey, "H-2", decisionFields("Notice", "fyi"))
	if _, err := s.db.Exec(`UPDATE decision_items SET commands=? WHERE reference IN ('H-1','H-2')`, `["echo\tbad"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "H-1", 1, "approved", ""); refusalCode(err) != "hidden_command" {
		t.Fatalf("approve: %v", err)
	}
	mustVerdict(t, s, "H-1", 1, "declined")
	mustVerdict(t, s, "H-2", 1, "dismissed")
	for _, ref := range []string{"H-1", "H-2"} {
		if item, _ := s.decisionRead(context.Background(), ref); item.State != "archived" {
			t.Fatalf("%s: %s", ref, item.State)
		}
	}
}

// TestDecisionGetIsBoundedByHistory: decision_get of an item with a long superseded history
// does about the same work as an item with one verdict, and still reports the count.
func TestDecisionGetIsBoundedByHistory(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "B-1", decisionFields("Short", "decision"))
	mustCreate(t, s, f.peerKey, "B-2", decisionFields("Long", "decision"))
	mustVerdict(t, s, "B-1", 1, "approved")
	if _, err := s.DecisionEdit(ctx, f.peerKey, "B-1", 1, DecisionFields{Context: ptr("Reopened")}); err != nil {
		t.Fatal(err)
	}
	const history = 20000
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM decision_items WHERE reference='B-2'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < history; i++ {
		if _, err := tx.Exec(`INSERT INTO decision_verdicts(item_id,revision,verdict,note,created_at,snapshot,superseded_at) VALUES (?,1,'approved','',1,'{}',2)`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`UPDATE decision_items SET verdict_count=? WHERE id=?`, history, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read := func(ref string) time.Duration {
		start := time.Now()
		for i := 0; i < 20; i++ {
			item, err := s.DecisionGet(ctx, f.peerKey, ref)
			if err != nil || item.Verdict != nil {
				t.Fatalf("%s: %+v %v", ref, item.Verdict, err)
			}
		}
		return time.Since(start)
	}
	if item, _ := s.DecisionGet(ctx, f.peerKey, "B-2"); item.Verdicts != history {
		t.Fatalf("count %d", item.Verdicts)
	}
	if item, _ := s.DecisionGet(ctx, f.peerKey, "B-1"); item.Verdicts != 1 {
		t.Fatalf("short count %d", item.Verdicts)
	}
	read("B-1")
	short, long := read("B-1"), read("B-2")
	// A scan of the history takes about thirty times longer; reads of equal work stay close.
	if long > 5*short+50*time.Millisecond {
		t.Fatalf("a long history slows decision_get: %v against %v", long, short)
	}
}
