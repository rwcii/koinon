package core

import (
	"context"
	"strings"
	"testing"
)

// Board actions (#537): each kind takes exactly its verdicts, each verdict reaches every
// recipient with its own notice, and each one but an fyi acknowledgement asks for the
// repository record.

func listed(t *testing.T, s *Store, caller Key, filter string) string {
	t.Helper()
	page, err := s.DecisionList(context.Background(), caller, filter, 0, 0)
	if err != nil {
		t.Fatalf("list %s: %v", filter, err)
	}
	var refs []string
	for _, item := range page.Items {
		refs = append(refs, item.Reference)
	}
	return strings.Join(refs, " ")
}

func TestDecisionEachKindAndAction(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	recipients := &[]string{f.address, f.peer.Name}
	cases := []struct {
		reference, kind, verdict, note, state string
		recorded                              bool
	}{
		{"K-DA", "decision", "approved", "", "in_progress", true},
		{"K-DD", "decision", "declined", "", "archived", true},
		{"K-DF", "decision", "feedback", "Split it in two.", "returned", true},
		{"K-TC", "task", "completed", "Done on the host.", "archived", true},
		{"K-TD", "task", "declined", "", "archived", true},
		{"K-TF", "task", "feedback", "Which host?", "returned", true},
		{"K-FA", "fyi", "acknowledged", "", "archived", false},
	}
	for _, c := range cases {
		mustCreate(t, s, f.holderKey, c.reference, DecisionFields{Title: ptr("Item " + c.reference), Kind: ptr(c.kind), Recipients: recipients})
	}
	// Tasks wait beside decision items; an fyi item is open, not waiting.
	if got := listed(t, s, f.peerKey, "waiting"); got != "K-DA K-DD K-DF K-TC K-TD K-TF" {
		t.Fatalf("waiting: %q", got)
	}
	for _, c := range cases {
		// Every verdict that the kind does not take is refused, and nothing is stored.
		for _, other := range []string{"approved", "declined", "feedback", "completed", "acknowledged"} {
			allowed := false
			for _, v := range kindVerdicts[c.kind] {
				allowed = allowed || v == other
			}
			if !allowed {
				if _, err := s.DecisionVerdict(verdictCtx(), c.reference, 1, other, "A note."); refusalCode(err) != "invalid_verdict" {
					t.Errorf("%s %s: %v", c.reference, other, err)
				}
			}
		}
		if _, err := s.DecisionVerdict(verdictCtx(), c.reference, 2, c.verdict, c.note); refusalCode(err) != "stale_revision" {
			t.Errorf("%s stale: %v", c.reference, err)
		}
		v, err := s.DecisionVerdict(verdictCtx(), c.reference, 1, c.verdict, c.note)
		if err != nil {
			t.Fatalf("%s: %v", c.reference, err)
		}
		if _, err := s.DecisionVerdict(verdictCtx(), c.reference, 1, c.verdict, c.note); refusalCode(err) != "decided" {
			t.Errorf("%s second verdict: %v", c.reference, err)
		}
		if len(v.Deliveries) != 2 || v.Deliveries[0].Outcome != "queued" || v.Deliveries[1].Outcome != "queued" {
			t.Errorf("%s deliveries: %+v", c.reference, v.Deliveries)
		}
		var body string
		if err := s.db.QueryRow(`SELECT body FROM messages WHERE recipient_id=? AND decision_verdict=?`, participantKey(f.address), v.ID).Scan(&body); err != nil ||
			!strings.HasPrefix(body, verdictNoticeBody(c.reference, c.verdict, v.ID)) || strings.Contains(body, "Entry:") != c.recorded {
			t.Errorf("%s notice: %q %v", c.reference, body, err)
		}
		item, err := s.DecisionGet(ctx, f.peerKey, c.reference)
		if err != nil || item.State != c.state || item.Verdict == nil || item.Verdict.Verdict != c.verdict || (item.Record != nil) != c.recorded {
			t.Errorf("%s item: %+v %v", c.reference, item, err)
		}
		if (item.State == "archived") != (item.ArchivedAt != 0) {
			t.Errorf("%s archived at %d", c.reference, item.ArchivedAt)
		}
	}
	if got := listed(t, s, f.peerKey, "returned"); got != "K-DF K-TF" {
		t.Errorf("returned: %q", got)
	}
	if got := listed(t, s, f.peerKey, "waiting"); got != "" {
		t.Errorf("waiting after the verdicts: %q", got)
	}
	// The pending page asks for the record while it holds a recorded verdict.
	page, err := s.DecisionPending(ctx, f.peerKey, 0, 0)
	if err != nil || len(page.Deliveries) != len(cases) || page.Record == nil {
		t.Fatalf("pending: %+v %v", page, err)
	}
	last := page.Deliveries[len(page.Deliveries)-1]
	if only, err := s.DecisionPending(ctx, f.peerKey, last.VerdictID-1, 0); err != nil || len(only.Deliveries) != 1 || only.Deliveries[0].Verdict != "acknowledged" || only.Record != nil {
		t.Fatalf("acknowledgement only: %+v %v", only, err)
	}
}

// Feedback returns an item to its author: it leaves the waiting list, the author's edit makes
// it wait again at a new revision, it can get feedback again, and a withdrawal archives it
// with every feedback kept in its history.
func TestDecisionFeedbackReturnsToAuthor(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.holderKey, "FB-1", DecisionFields{Title: ptr("Pick a store"), Kind: ptr("decision"), Recipients: &[]string{f.address}})
	if _, err := s.DecisionVerdict(verdictCtx(), "FB-1", 1, "feedback", "  \n\t"); refusalCode(err) != "feedback_required" {
		t.Fatalf("blank feedback: %v", err)
	}
	first, err := s.DecisionVerdict(verdictCtx(), "FB-1", 1, "feedback", "Compare **two** stores first.")
	if err != nil {
		t.Fatal(err)
	}
	// Only the author's edit or withdrawal acts on a returned item; the maintainer has nothing to decide.
	if _, err := s.DecisionVerdict(verdictCtx(), "FB-1", 1, "approved", ""); refusalCode(err) != "decided" {
		t.Fatalf("verdict on a returned item: %v", err)
	}
	if _, err := s.DecisionClose(ctx, f.holderKey, "FB-1", 1, "", "close", s.clock()+600); refusalCode(err) != "not_in_progress" {
		t.Fatalf("close a returned item: %v", err)
	}
	edited, err := s.DecisionEdit(ctx, f.holderKey, "FB-1", 1, DecisionFields{Context: ptr("Store A and store B compared.")})
	if err != nil || edited.State != "active" || edited.Revision != 2 || edited.Verdict != nil || edited.Verdicts != 1 {
		t.Fatalf("edit after feedback: %+v %v", edited, err)
	}
	if got := listed(t, s, f.holderKey, "waiting"); got != "FB-1" {
		t.Fatalf("waiting again: %q", got)
	}
	second := mustVerdictNote(t, s, "FB-1", 2, "feedback", "Add the cost.")
	item, err := s.DecisionWithdraw(ctx, f.holderKey, "FB-1", 2, "answered in chat", "fb-1", s.clock()+600)
	if err != nil || item.State != "archived" || !item.Withdrawn || item.Verdict != nil || item.Record != nil {
		t.Fatalf("withdraw a returned item: %+v %v", item, err)
	}
	history, err := s.DecisionHistory(ctx, f.holderKey, "FB-1", 0, 0, false)
	if err != nil || len(history.Verdicts) != 2 || history.Verdicts[0].ID != first.ID || history.Verdicts[1].ID != second.ID ||
		history.Verdicts[1].SupersededAt == 0 || history.Verdicts[1].Note != "Add the cost." {
		t.Fatalf("history: %+v %v", history, err)
	}
	restored, err := s.DecisionRestore(ctx, f.holderKey, "FB-1", 2)
	if err != nil || restored.State != "active" || restored.Verdict != nil {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	// The repository entry quotes the feedback under its own label.
	entry := DecisionRecordEntry("FB-1", "", first)
	if !strings.Contains(entry, "**Feedback**, revision 1") || !strings.Contains(entry, "Feedback:\n\n> Compare **two** stores first.") || strings.Contains(entry, "Note:") {
		t.Fatalf("entry: %s", entry)
	}
}

// The author is told of every verdict: at its participant address when no recipient names it,
// once when a recipient does, and at its peer name when it holds no participant.
func TestDecisionVerdictReachesTheAuthor(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.holderKey, "AU-1", DecisionFields{Title: ptr("Read this"), Kind: ptr("fyi"), Recipients: &[]string{f.peer.Name}})
	mustCreate(t, s, f.holderKey, "AU-2", DecisionFields{Title: ptr("Read that"), Kind: ptr("fyi"), Recipients: &[]string{f.holder.Name}})
	mustCreate(t, s, f.peerKey, "AU-3", DecisionFields{Title: ptr("Do this"), Kind: ptr("task")})
	names := func(v DecisionVerdict) string {
		var out []string
		for _, d := range v.Deliveries {
			out = append(out, d.Name+":"+d.Outcome)
		}
		return strings.Join(out, " ")
	}
	if v := mustVerdict(t, s, "AU-1", 1, "acknowledged"); names(v) != f.peer.Name+":queued "+f.address+":queued" {
		t.Errorf("author not named: %s", names(v))
	}
	if v := mustVerdict(t, s, "AU-2", 1, "acknowledged"); names(v) != f.holder.Name+":queued" {
		t.Errorf("author named by its peer name: %s", names(v))
	}
	if v := mustVerdictNote(t, s, "AU-3", 1, "feedback", "Which host?"); names(v) != f.peer.Name+":queued" {
		t.Errorf("plain peer author: %s", names(v))
	}
	// The holder of the author's address reads both deliveries, once each.
	page, err := s.DecisionPending(ctx, f.holderKey, 0, 0)
	if err != nil || len(page.Deliveries) != 2 || page.Deliveries[0].Reference != "AU-1" || page.Deliveries[1].Reference != "AU-2" || page.Record != nil {
		t.Fatalf("author's pending: %+v %v", page, err)
	}
}

func mustVerdictNote(t *testing.T, s *Store, reference string, revision int64, verdict, note string) DecisionVerdict {
	t.Helper()
	v, err := s.DecisionVerdict(verdictCtx(), reference, revision, verdict, note)
	if err != nil {
		t.Fatalf("verdict %s: %v", reference, err)
	}
	return v
}

// The board shows a returned item with the feedback and no verdict form, under its own filter.
func TestDecisionBoardReturnedCard(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-returned", "")
	mustCreate(t, d.store, Key{"claude", peer.ID}, "RT-1", DecisionFields{Title: ptr("Restart it"), Kind: ptr("task"), Recipients: &[]string{peer.Name}})
	if got := client.do("decision-feedback", map[string][]string{"reference": {"RT-1"}, "revision": {"1"}}); got != "feedback_required" {
		t.Fatalf("feedback without text: %s", got)
	}
	if got := client.do("decision-feedback", map[string][]string{"reference": {"RT-1"}, "revision": {"1"}, "note": {"Use the *staging* host."}}); got != "decision_feedback" {
		t.Fatalf("feedback: %s", got)
	}
	for _, path := range []string{"/dashboard/decisions", "/dashboard/decisions?status=returned"} {
		page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		card := cardOf(t, page.body, "RT-1")
		if !strings.Contains(card, ">returned</span>") || !strings.Contains(card, "Use the <em>staging</em> host.") || strings.Contains(card, "<form") ||
			!strings.Contains(page.body, "<b>1</b> returned") {
			t.Errorf("%s: returned card: %s", path, card)
		}
	}
	if got := client.do("decision-complete", map[string][]string{"reference": {"RT-1"}, "revision": {"1"}}); got != "decided" {
		t.Fatalf("complete a returned task: %s", got)
	}
}

// Schema 36 rebuilds the item and verdict tables of a schema 35 store: every row and ID is
// kept, the AUTOINCREMENT high-water marks are kept, and open ACTION FOR YOU fyi items become
// tasks.
func TestSchemaThirtySixAddsTasks(t *testing.T) {
	root := t.TempDir()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a := join(t, s, "codex", "synthetic-v36", namedRepo(t, "koinon"))
	key := Key{"codex", a.ID}
	mustCreate(t, s, key, "V-OPEN", decisionFields("ACTION FOR YOU: restart the host", "fyi"))
	mustCreate(t, s, key, "V-DONE", decisionFields("ACTION FOR YOU: rotate the key", "fyi"))
	mustCreate(t, s, key, "V-NOTE", decisionFields("Plain notice", "fyi"))
	mustCreate(t, s, key, "V-DEC", decisionFields("Decide", "decision"))
	mustCreate(t, s, key, "V-GONE", decisionFields("Moved away", "decision"))
	if _, err := fixtureExec(s, undoSchemaThirtySix+`PRAGMA user_version=35`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 35); err != nil {
		t.Fatal(err)
	}
	// Schema 35 values: an old dismissal and an approval, and a removed newest item and verdict
	// whose IDs must not come back.
	if _, err := fixtureExec(s, `UPDATE decision_items SET state='archived',archived_at=1 WHERE reference='V-DONE';
		INSERT INTO decision_verdicts(item_id,revision,verdict,note,created_at,snapshot) SELECT id,1,'dismissed','',1,'{}' FROM decision_items WHERE reference='V-DONE';
		INSERT INTO decision_verdicts(item_id,revision,verdict,note,created_at,snapshot) SELECT id,1,'approved','',1,'{}' FROM decision_items WHERE reference='V-GONE';
		DELETE FROM decision_verdicts WHERE item_id=(SELECT id FROM decision_items WHERE reference='V-GONE');
		DELETE FROM decision_items WHERE reference='V-GONE';`); err != nil {
		t.Fatal(err)
	}
	items, verdicts := count(t, s, `SELECT MAX(id) FROM decision_items`), count(t, s, `SELECT MAX(id) FROM decision_verdicts`)
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || count(t, upgraded, `SELECT COUNT(*) FROM decision_items`) != 4 ||
		count(t, upgraded, `SELECT COUNT(*) FROM decision_verdicts WHERE verdict='dismissed'`) != 1 {
		t.Fatal("schema 36 upgrade")
	}
	if err := checkCatalog(upgraded.db, 35); err == nil {
		t.Fatal("a schema 35 writer would accept the schema 36 store")
	}
	for reference, kind := range map[string]string{"V-OPEN": "task", "V-DONE": "fyi", "V-NOTE": "fyi", "V-DEC": "decision"} {
		var got string
		if err := upgraded.db.QueryRow(`SELECT kind FROM decision_items WHERE reference=?`, reference).Scan(&got); err != nil || got != kind {
			t.Errorf("%s: %q %v", reference, got, err)
		}
	}
	created, err := upgraded.DecisionCreate(context.Background(), key, "V-NEW", decisionFields("New", "task"), "v-new", upgraded.clock()+600)
	if err != nil || created.State != "active" {
		t.Fatalf("create after the upgrade: %+v %v", created, err)
	}
	if id := count(t, upgraded, `SELECT id FROM decision_items WHERE reference='V-NEW'`); id != items+2 {
		t.Fatalf("new item ID %d after %d and a removed item", id, items)
	}
	v, err := upgraded.DecisionVerdict(verdictCtx(), "V-OPEN", 1, "completed", "")
	if err != nil || v.ID != verdicts+2 {
		t.Fatalf("verdict after the upgrade: %+v %v (after %d)", v, err, verdicts)
	}
}
