package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Decision verdicts, delivery, close and deep archive (#206, chunk 02). All data is
// synthetic.

func verdictCtx() context.Context {
	ctx, _ := withAudit(context.Background(), "decision-verdict", "synthetic")
	return ctx
}

func mustVerdict(t *testing.T, s *Store, reference string, revision int64, verdict string) DecisionVerdict {
	t.Helper()
	v, err := s.DecisionVerdict(verdictCtx(), reference, revision, verdict, "")
	if err != nil {
		t.Fatalf("verdict %s: %v", reference, err)
	}
	return v
}

type verdictFixture struct {
	s                   *Store
	holder, other, peer Session
	holderKey, otherKey Key
	peerKey             Key
	clock               *time.Time
	address             string
}

func newVerdictFixture(t *testing.T) *verdictFixture {
	t.Helper()
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	repo := namedRepo(t, "koinon")
	holder := joinAs(t, s, "codex", "synthetic-verdict-holder", repo, "")
	other := joinAs(t, s, "codex", "synthetic-verdict-other", repo, "")
	peer := join(t, s, "claude", "synthetic-verdict-peer", "")
	return &verdictFixture{s: s, holder: holder, other: other, peer: peer, holderKey: Key{"codex", holder.ID},
		otherKey: Key{"codex", other.ID}, peerKey: Key{"claude", peer.ID}, clock: &clock, address: holder.Alias}
}

func TestDecisionVerdictStoresSnapshotAndDeliveries(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "V-1", DecisionFields{Title: ptr("Ship it"), Kind: ptr("decision"), Context: ptr("Facts."),
		Recommendation: ptr("Approve."), Commands: &[]string{"make ship"}, Recipients: &[]string{f.address, f.peer.Name, "synthetic-nobody", "maintainer"}})
	v := mustVerdict(t, s, "V-1", 1, "approved")
	want := []string{"queued participant", "queued peer", "skipped unknown_name", "skipped maintainer"}
	for i, d := range v.Deliveries {
		got := d.Outcome + " " + d.Kind
		if d.Outcome == "skipped" {
			got = d.Outcome + " " + d.Reason
		}
		if got != want[i] {
			t.Fatalf("delivery %d: %+v", i, d)
		}
	}
	got, err := s.DecisionGet(ctx, f.peerKey, "V-1")
	if err != nil || got.State != "in_progress" || got.Verdict == nil || got.Verdict.Snapshot.Title != "Ship it" ||
		got.Verdict.Snapshot.Commands[0] != "make ship" || got.Verdicts != 1 {
		t.Fatalf("get: %+v %v", got, err)
	}
	var body, kind string
	var typed int64
	if err := s.db.QueryRow(`SELECT body,system_kind,decision_verdict FROM messages WHERE recipient_id=?`, participantKey(f.address)).
		Scan(&body, &kind, &typed); err != nil || kind != decisionVerdictKind || typed != v.ID || body != verdictNoticeBody("V-1", v.ID) {
		t.Fatalf("participant message: %q %q %d %v", body, kind, typed, err)
	}
	if count(t, s, `SELECT COUNT(*) FROM audit WHERE action='decision-verdict' AND target=? AND result='accepted'`,
		fmt.Sprintf("decision V-1 revision 1 approved verdict %d", v.ID)) != 1 {
		t.Fatal("audit record")
	}
	for _, key := range []Key{f.holderKey, f.peerKey} {
		pending, err := s.DecisionPending(ctx, key, 0, 0)
		if err != nil || len(pending.Deliveries) != 1 || pending.Deliveries[0].VerdictID != v.ID || pending.Deliveries[0].Verdict != "approved" {
			t.Fatalf("pending %v: %+v %v", key, pending, err)
		}
	}
	if pending, _ := s.DecisionPending(ctx, f.otherKey, 0, 0); len(pending.Deliveries) != 0 {
		t.Fatalf("a non-holder sees the participant's delivery: %+v", pending)
	}
	// A lost acknowledgement reply: the repeat changes nothing.
	for i, want := range []int{1, 0} {
		acked, err := s.DecisionAck(ctx, f.holderKey, []int64{v.ID})
		if err != nil || len(acked) != want {
			t.Fatalf("ack %d: %v %v", i, acked, err)
		}
	}
	if pending, _ := s.DecisionPending(ctx, f.holderKey, 0, 0); len(pending.Deliveries) != 0 {
		t.Fatalf("acknowledged delivery still pending: %+v", pending)
	}
	if pending, _ := s.DecisionPending(ctx, f.peerKey, 0, 0); len(pending.Deliveries) != 1 {
		t.Fatal("another recipient's acknowledgement acknowledged this one")
	}
	if _, err := s.DecisionAck(ctx, f.holderKey, make([]int64, 33)); refusalCode(err) != "invalid_request" {
		t.Fatalf("33 ids: %v", err)
	}
}

func TestDecisionVerdictRefusals(t *testing.T) {
	f := newVerdictFixture(t)
	s := f.s
	mustCreate(t, s, f.peerKey, "R-1", decisionFields("Decide", "decision"))
	mustCreate(t, s, f.peerKey, "R-2", decisionFields("Notice", "fyi"))
	for name, c := range map[string]struct {
		reference string
		revision  int64
		verdict   string
		code      string
	}{
		"stale":            {"R-1", 2, "approved", "stale_revision"},
		"dismiss decision": {"R-1", 1, "dismissed", "invalid_verdict"},
		"approve fyi":      {"R-2", 1, "approved", "invalid_verdict"},
		"unknown":          {"R-9", 1, "approved", "not_found"},
	} {
		if _, err := s.DecisionVerdict(verdictCtx(), c.reference, c.revision, c.verdict, ""); refusalCode(err) != c.code {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "R-1", 1, "approved", "key "+"ghp_"+strings.Repeat("A1", 18)); refusalCode(err) != "credential_refused" {
		t.Fatalf("credential note: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE decision_items SET commands=? WHERE reference='R-1'`, `["rm​ -rf"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "R-1", 1, "approved", ""); refusalCode(err) != "hidden_command" {
		t.Fatalf("hidden command: %v", err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_verdicts`) != 0 {
		t.Fatal("a refused verdict was stored")
	}
	dismissed := mustVerdict(t, s, "R-2", 1, "dismissed")
	got, err := s.DecisionGet(context.Background(), f.peerKey, "R-2")
	if err != nil || got.State != "archived" || got.ArchivedAt == 0 || len(dismissed.Deliveries) != 0 {
		t.Fatalf("dismiss: %+v %v", got, err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "R-2", 1, "dismissed", ""); refusalCode(err) != "decided" {
		t.Fatalf("second verdict: %v", err)
	}
}

// A failure of any insert leaves no part of the verdict.
func TestDecisionVerdictIsAtomic(t *testing.T) {
	for _, table := range []string{"decision_verdicts", "decision_deliveries", "messages", "audit"} {
		t.Run(table, func(t *testing.T) {
			f := newVerdictFixture(t)
			s := f.s
			mustCreate(t, s, f.peerKey, "A-1", DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
			before := rowsOf(t, s.db)
			if _, err := s.db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DecisionVerdict(verdictCtx(), "A-1", 1, "approved", ""); err == nil {
				t.Fatal("verdict committed with a failing insert")
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_insert`); err != nil {
				t.Fatal(err)
			}
			got, _ := s.DecisionGet(context.Background(), f.peerKey, "A-1")
			if got.State != "active" || got.Verdict != nil || rowsOf(t, s.db) != before ||
				count(t, s, `SELECT COUNT(*) FROM decision_verdicts`)+count(t, s, `SELECT COUNT(*) FROM decision_deliveries`) != 0 {
				t.Fatalf("part of the verdict remains: %+v", got)
			}
		})
	}
}

// An edit of a decided item reopens it; the superseded verdict's delivery stays pending.
func TestDecisionReopenAndHistory(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "H-1", DecisionFields{Title: ptr("First text"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
	first := mustVerdict(t, s, "H-1", 1, "approved")
	reopened, err := s.DecisionEdit(ctx, f.peerKey, "H-1", 1, DecisionFields{Title: ptr("Second text")})
	if err != nil || reopened.State != "active" || reopened.Revision != 2 || reopened.Verdict != nil || reopened.Verdicts != 1 {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
	pending, err := s.DecisionPending(ctx, f.peerKey, 0, 0)
	if err != nil || len(pending.Deliveries) != 1 || pending.Deliveries[0].SupersededAt == 0 {
		t.Fatalf("superseded delivery: %+v %v", pending, err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "H-1", 1, "approved", ""); refusalCode(err) != "stale_revision" {
		t.Fatalf("verdict on the old text: %v", err)
	}
	second := mustVerdict(t, s, "H-1", 2, "declined")
	page, err := s.DecisionHistory(ctx, f.peerKey, "H-1", 0, 0)
	if err != nil || len(page.Verdicts) != 2 || page.Verdicts[0].ID != first.ID || page.Verdicts[0].Snapshot.Title != "First text" ||
		page.Verdicts[0].SupersededAt == 0 || page.Verdicts[1].ID != second.ID || page.Verdicts[1].Snapshot.Title != "Second text" {
		t.Fatalf("history: %+v %v", page, err)
	}
	// Many reopen cycles page 16 verdicts at a time.
	revision := int64(2)
	for i := 0; i < 40; i++ {
		item, err := s.DecisionEdit(ctx, f.peerKey, "H-1", revision, DecisionFields{Order: ptr(int64(i))})
		if err != nil {
			t.Fatal(err)
		}
		revision = item.Revision
		mustVerdict(t, s, "H-1", revision, "declined")
	}
	var seen int
	for after := int64(0); ; {
		page, err := s.DecisionHistory(ctx, f.peerKey, "H-1", after, 0)
		if err != nil || len(page.Verdicts) > decisionHistoryItems {
			t.Fatalf("page: %d %v", len(page.Verdicts), err)
		}
		seen += len(page.Verdicts)
		if !page.More {
			break
		}
		after = *page.NextAfter
	}
	if seen != 42 {
		t.Fatalf("history walk: %d", seen)
	}
}

// The largest verdict, every field at its limit in the character with the longest JSON
// escape, fits one history page.
func TestDecisionHistoryLargestVerdictFitsAPage(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	commands := make([]string, decisionCommandCount)
	for i := range commands {
		commands[i] = strings.Repeat("<", decisionCommandLimit)
	}
	mustCreate(t, s, f.peerKey, "B-1", DecisionFields{Title: ptr(strings.Repeat("<", decisionTitleLimit)), Kind: ptr("decision"),
		Context: ptr(strings.Repeat("<", decisionContextLimit)), Recommendation: ptr(strings.Repeat("<", decisionRecommendationLimit)), Commands: &commands})
	if _, err := s.DecisionVerdict(verdictCtx(), "B-1", 1, "approved", strings.Repeat("<", decisionNoteLimit)); err != nil {
		t.Fatal(err)
	}
	page, err := s.DecisionHistory(ctx, f.peerKey, "B-1", 0, 0)
	if err != nil || len(page.Verdicts) != 1 {
		t.Fatalf("largest verdict: %+v %v", page, err)
	}
	reply, _ := json.Marshal(map[string]any{"ok": true, "result": page})
	if len(reply) >= decisionHistoryBytes {
		t.Fatalf("reply %d bytes", len(reply))
	}
}

func TestDecisionClose(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "C-1", decisionFields("Work", "decision"))
	deadline := s.clock() + 600
	if _, err := s.DecisionClose(ctx, f.peerKey, "C-1", 1, "", "close-1", deadline); refusalCode(err) != "not_in_progress" {
		t.Fatalf("close before a verdict: %v", err)
	}
	mustVerdict(t, s, "C-1", 1, "approved")
	closed, err := s.DecisionClose(ctx, f.peerKey, "C-1", 1, "Done; see the change.", "close-2", deadline)
	if err != nil || closed.State != "archived" || closed.ClosedBy != f.peer.Name || closed.OutcomeNote != "Done; see the change." || closed.ArchivedAt == 0 {
		t.Fatalf("close: %+v %v", closed, err)
	}
	again, err := s.DecisionClose(ctx, f.peerKey, "C-1", 1, "Done; see the change.", "close-2", deadline)
	if err != nil || again.State != "archived" {
		t.Fatalf("retry: %+v %v", again, err)
	}
	if _, err := s.DecisionClose(ctx, f.peerKey, "C-1", 1, "", "close-3", deadline); refusalCode(err) != "not_in_progress" {
		t.Fatalf("second close: %v", err)
	}
}

func TestDecisionWakeNamesVerdictIDs(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	var ids []string
	for i := 1; i <= 10; i++ {
		reference := fmt.Sprintf("N-%d", i)
		mustCreate(t, s, f.peerKey, reference, DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
		ids = append(ids, fmt.Sprint(mustVerdict(t, s, reference, 1, "approved").ID))
	}
	b := wakeBatch{Session: Session{Family: "claude", ID: f.peer.ID}, First: 1, Last: 10}
	for i := 0; i < 2; i++ {
		suffix, err := s.verdictNoticeSuffix(ctx, b)
		if err != nil || suffix != " Decision verdicts "+strings.Join(ids[:8], ", ")+" +2 more." {
			t.Fatalf("suffix %d: %q %v", i, suffix, err)
		}
	}
	if !strings.HasPrefix(wakeNotice(b)+" Decision", "Koinon inbox ") {
		t.Fatal("notice text")
	}
	b.First, b.Last = 1, 1
	if suffix, _ := s.verdictNoticeSuffix(ctx, b); suffix != " Decision verdicts "+ids[0]+"." {
		t.Fatalf("one verdict: %q", suffix)
	}
	// A plain message range names no verdict.
	if _, err := s.Send(ctx, f.holderKey, f.peer.Name, "synthetic plain message"); err != nil {
		t.Fatal(err)
	}
	b.First, b.Last = 11, 11
	if suffix, _ := s.verdictNoticeSuffix(ctx, b); suffix != "" {
		t.Fatalf("plain range: %q", suffix)
	}
	// The wake path sends the suffix to the provider.
	var sent string
	s.wake.send = func(_ context.Context, _ Session, notice string) wakeResult {
		sent = notice
		return wakeResult{"notified", "accepted"}
	}
	if _, err := s.submitWake(ctx, Key{"claude", f.peer.ID}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent, "Decision verdicts "+ids[0]) {
		t.Fatalf("sent notice: %q", sent)
	}
}

// A participant's delivery follows its current holder; an inactive peer is skipped.
func TestDecisionDeliveryFollowsHolderAndSkipsInactive(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "F-1", DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{f.address}})
	v := mustVerdict(t, s, "F-1", 1, "approved")
	holderCtx, _ := withAudit(ctx, "participant-holder", f.address)
	if err := s.ChooseHolder(holderCtx, f.address, f.otherKey, f.other.Revision); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.DecisionPending(ctx, f.otherKey, 0, 0); len(p.Deliveries) != 1 || p.Deliveries[0].VerdictID != v.ID {
		t.Fatalf("new holder: %+v", p)
	}
	if p, _ := s.DecisionPending(ctx, f.holderKey, 0, 0); len(p.Deliveries) != 0 {
		t.Fatalf("former holder: %+v", p)
	}
	retired := join(t, s, "agy", "synthetic-verdict-retired", "")
	expired := join(t, s, "opencode", "synthetic-verdict-expired", "")
	mustCreate(t, s, f.peerKey, "F-2", DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{retired.Name, expired.Name}})
	if _, err := s.Mutate(ctx, Mutation{Family: "agy", ID: retired.ID, IfRevision: retired.Revision}, true); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(2 * time.Minute)
	v2 := mustVerdict(t, s, "F-2", 1, "approved")
	for i, d := range v2.Deliveries {
		if d.Outcome != "skipped" || d.Reason != "recipient_inactive" {
			t.Fatalf("delivery %d: %+v", i, d)
		}
	}
}

// Acknowledgement is an ordinary write: refused at a full ordinary band, the delivery stays
// queued, and it succeeds once there is space.
func TestDecisionAckAtAFullStore(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "S-1", DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
	v := mustVerdict(t, s, "S-1", 1, "approved")
	s.storage.mu.Lock()
	pages, _ := s.pages(ctx, s.db)
	saved := s.storage.maxPages
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance - 1
	s.storage.mu.Unlock()
	if _, err := s.DecisionAck(ctx, f.peerKey, []int64{v.ID}); code(err) != "capacity" {
		t.Fatalf("ack at a full store: %v", err)
	}
	if p, _ := s.DecisionPending(ctx, f.peerKey, 0, 0); len(p.Deliveries) != 1 {
		t.Fatal("refused ack changed the delivery")
	}
	s.storage.mu.Lock()
	s.storage.maxPages = saved
	s.storage.mu.Unlock()
	if acked, err := s.DecisionAck(ctx, f.peerKey, []int64{v.ID}); err != nil || len(acked) != 1 {
		t.Fatalf("ack after space: %v %v", acked, err)
	}
}

func TestDecisionPendingSurvivesRestart(t *testing.T) {
	s, root := testStore(t)
	peer := join(t, s, "claude", "synthetic-restart-peer", "")
	key := Key{"claude", peer.ID}
	mustCreate(t, s, key, "T-1", DecisionFields{Title: ptr("t"), Kind: ptr("decision"), Recipients: &[]string{peer.Name}})
	v := mustVerdict(t, s, "T-1", 1, "approved")
	s.db.Close()
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	p, err := reopened.DecisionPending(context.Background(), key, 0, 0)
	if err != nil || len(p.Deliveries) != 1 || p.Deliveries[0].VerdictID != v.ID {
		t.Fatalf("after restart: %+v %v", p, err)
	}
}

// deepArchiveFixture archives one item past 30 days.
func deepArchiveFixture(t *testing.T) (*verdictFixture, DecisionVerdict) {
	t.Helper()
	f := newVerdictFixture(t)
	mustCreate(t, f.s, f.peerKey, "D-1", DecisionFields{Title: ptr("Archive me"), Kind: ptr("decision"), Context: ptr("Old facts."),
		Recipients: &[]string{f.peer.Name, "synthetic-nobody"}})
	v := mustVerdict(t, f.s, "D-1", 1, "declined")
	// The synthetic sessions outlive the 31 days without renewals.
	if _, err := f.s.db.Exec(`UPDATE sessions SET expires_at=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(31 * 24 * time.Hour)
	return f, v
}

func logLines(t *testing.T, s *Store) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(s.root, "decisions", "*.jsonl"))
	var lines []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func TestDecisionDeepArchiveAndRestore(t *testing.T) {
	f, v := deepArchiveFixture(t)
	s, ctx := f.s, context.Background()
	// A queued delivery keeps the item in the archive.
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items`) != 1 || len(logLines(t, s)) != 0 {
		t.Fatal("an item with a queued delivery moved")
	}
	if _, err := s.DecisionAck(ctx, f.peerKey, []int64{v.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items`)+count(t, s, `SELECT COUNT(*) FROM decision_verdicts`)+
		count(t, s, `SELECT COUNT(*) FROM decision_deliveries`)+count(t, s, `SELECT COUNT(*) FROM decision_recipients`) != 0 {
		t.Fatal("rows remain after the move")
	}
	lines := logLines(t, s)
	if len(lines) != 1 || !strings.Contains(lines[0], `"reference":"D-1"`) || !strings.Contains(lines[0], "Old facts.") {
		t.Fatalf("log: %v", lines)
	}
	files, _ := filepath.Glob(filepath.Join(s.root, "decisions", "*.jsonl"))
	if info, err := os.Stat(files[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode: %v %v", info.Mode(), err)
	}
	if _, err := s.DecisionGet(ctx, f.peerKey, "D-1"); refusalCode(err) != "deep_archived" {
		t.Fatalf("get moved: %v", err)
	}
	if _, err := s.DecisionCreate(ctx, f.peerKey, "D-1", decisionFields("t", "fyi"), "reuse", s.clock()+60); refusalCode(err) != "reference_in_use" {
		t.Fatalf("reference reuse: %v", err)
	}
	restored, err := s.DecisionRestore(ctx, f.peerKey, "D-1")
	if err != nil || restored.State != "archived" || restored.Context != "Old facts." || restored.Verdict == nil || restored.Verdict.ID != v.ID ||
		restored.ArchivedAt != s.now().UnixMilli() || len(restored.Recipients) != 2 {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_deep_archive`) != 0 || len(logLines(t, s)) != 1 {
		t.Fatal("restore changed the log or kept the locator")
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "D-1"); refusalCode(err) != "not_found" {
		t.Fatalf("second restore: %v", err)
	}
	// The restored item starts a new 30-day period.
	if err := s.archiveDecisions(ctx); err != nil || count(t, s, `SELECT COUNT(*) FROM decision_items`) != 1 {
		t.Fatalf("restored item moved at once: %v", err)
	}
}

// A crash after the mark, after the append, or in a partial line, and an edit between the
// steps, leave either the rows or one complete line for the current revision.
func TestDecisionDeepArchiveCrashAndEdit(t *testing.T) {
	ack := func(t *testing.T, f *verdictFixture, v DecisionVerdict) {
		if _, err := f.s.DecisionAck(context.Background(), f.peerKey, []int64{v.ID}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("crash after mark", func(t *testing.T) {
		f, v := deepArchiveFixture(t)
		ack(t, f, v)
		f.s.decisions.afterMark = func(int64) { panic("synthetic crash") }
		func() {
			defer func() { recover() }()
			f.s.archiveDecisions(context.Background())
		}()
		f.s.decisions.afterMark = nil
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items WHERE mark_revision>0`) != 1 || len(logLines(t, f.s)) != 0 {
			t.Fatal("mark state")
		}
		if err := f.s.archiveDecisions(context.Background()); err != nil {
			t.Fatal(err)
		}
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items`) != 0 || len(logLines(t, f.s)) != 1 || count(t, f.s, `SELECT COUNT(*) FROM decision_deep_archive`) != 1 {
			t.Fatal("finish after mark")
		}
	})
	t.Run("crash after append", func(t *testing.T) {
		f, v := deepArchiveFixture(t)
		ack(t, f, v)
		f.s.decisions.afterAppend = func(int64) { panic("synthetic crash") }
		func() {
			defer func() { recover() }()
			f.s.archiveDecisions(context.Background())
		}()
		f.s.decisions.afterAppend = nil
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items`) != 1 || len(logLines(t, f.s)) != 1 {
			t.Fatal("append state")
		}
		if err := f.s.archiveDecisions(context.Background()); err != nil {
			t.Fatal(err)
		}
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items`) != 0 || len(logLines(t, f.s)) != 1 {
			t.Fatalf("finish after append wrote %d lines", len(logLines(t, f.s)))
		}
		if _, err := f.s.DecisionRestore(context.Background(), f.peerKey, "D-1"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("partial line", func(t *testing.T) {
		f, v := deepArchiveFixture(t)
		ack(t, f, v)
		name := "decisions/" + f.s.now().UTC().Format("2006-01") + ".jsonl"
		file, _, err := f.s.openDecisionLog(name)
		if err != nil {
			t.Fatal(err)
		}
		file.Write([]byte(`{"record":"koinon.decision.v1","id":`))
		file.Close()
		if _, err := f.s.db.Exec(`UPDATE decision_items SET mark_revision=revision,mark_file=?,mark_offset=0`, name); err != nil {
			t.Fatal(err)
		}
		if err := f.s.archiveDecisions(context.Background()); err != nil {
			t.Fatal(err)
		}
		lines := logLines(t, f.s)
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items`) != 0 || len(lines) != 2 || !strings.HasPrefix(lines[0], `{"record":"koinon.decision.v1","id":`) {
			t.Fatalf("partial line: %v", lines)
		}
		restored, err := f.s.DecisionRestore(context.Background(), f.peerKey, "D-1")
		if err != nil || restored.Title != "Archive me" {
			t.Fatalf("restore after a partial line: %+v %v", restored, err)
		}
	})
	t.Run("edit between append and delete", func(t *testing.T) {
		f, v := deepArchiveFixture(t)
		ack(t, f, v)
		f.s.decisions.afterAppend = func(int64) {
			if _, err := f.s.DecisionEdit(context.Background(), f.peerKey, "D-1", 1, DecisionFields{Title: ptr("New facts")}); err != nil {
				t.Errorf("edit: %v", err)
			}
		}
		if err := f.s.archiveDecisions(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, err := f.s.DecisionGet(context.Background(), f.peerKey, "D-1")
		if err != nil || got.State != "active" || got.Title != "New facts" || count(t, f.s, `SELECT COUNT(*) FROM decision_deep_archive`) != 0 {
			t.Fatalf("edited item: %+v %v", got, err)
		}
		if count(t, f.s, `SELECT COUNT(*) FROM decision_items WHERE mark_revision>0`) != 0 {
			t.Fatal("mark kept after the edit")
		}
	})
}

// Approve, Decline and Dismiss are dashboard actions behind the login, Host, Origin and CSRF
// checks, on the revision the page showed.
func TestDecisionVerdictDashboardActions(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-dashboard-peer", "")
	key := Key{"claude", peer.ID}
	mustCreate(t, d.store, key, "W-1", DecisionFields{Title: ptr("Approve this"), Kind: ptr("decision"), Recipients: &[]string{peer.Name}})
	mustCreate(t, d.store, key, "W-2", decisionFields("Notice", "fyi"))
	page := dashboardDo(t, d, "GET", "/dashboard/decisions?ref=W-1", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "/dashboard/actions/decision-approve") || !strings.Contains(page.body, `name="revision" value="1"`) {
		t.Fatalf("item page: %d %s", page.status, page.body)
	}
	approve := url.Values{"reference": {"W-1"}, "revision": {"1"}, "note": {"Go ahead."}}
	for name, c := range map[string]struct {
		cookie string
		form   url.Values
		status int
	}{
		"no session": {"", approve, 401},
		"no token":   {client.cookie, approve, 403},
	} {
		if r := dashboardDo(t, d, "POST", "/dashboard/actions/decision-approve", c.cookie, client.sameOrigin, c.form); r.status != c.status {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	if r := dashboardDo(t, d, "POST", "/dashboard/actions/decision-approve", client.cookie,
		func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, url.Values{"csrf": {client.csrf}}); r.status != 403 {
		t.Fatalf("foreign origin: %d", r.status)
	}
	if count(t, d.store, `SELECT COUNT(*) FROM decision_verdicts`) != 0 {
		t.Fatal("a refused request stored a verdict")
	}
	if got := client.do("decision-approve", url.Values{"reference": {"W-1"}, "revision": {"2"}}); got != "stale_revision" {
		t.Fatalf("stale: %s", got)
	}
	if got := client.do("decision-approve", approve); got != "decision_approved" {
		t.Fatalf("approve: %s", got)
	}
	if got := client.do("decision-approve", approve); got != "decided" {
		t.Fatalf("second approve: %s", got)
	}
	if got := client.do("decision-dismiss", url.Values{"reference": {"W-2"}, "revision": {"1"}}); got != "decision_dismissed" {
		t.Fatalf("dismiss: %s", got)
	}
	got, err := d.store.DecisionGet(context.Background(), key, "W-1")
	if err != nil || got.Verdict == nil || got.Verdict.Note != "Go ahead." || got.State != "in_progress" {
		t.Fatalf("stored verdict: %+v %v", got, err)
	}
}
