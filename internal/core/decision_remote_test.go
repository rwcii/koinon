package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// rawStore opens a state database without the store's migration, to read its version.
func rawStore(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// Decision cards for agents on another computer, chunk 01: capture at the origin (#746).
// All names, computers and pair IDs are synthetic.

const (
	pairAlpha = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	pairBeta  = "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2"
)

// withPairs makes the store see the paired computers of pairs, as the daemon's fence does.
func withPairs(s *Store, pairs ...linkPairRef) *[]linkPairRef {
	current := append([]linkPairRef(nil), pairs...)
	var mu sync.Mutex
	s.linkFence = func(context.Context) (boardPairs, func()) {
		mu.Lock()
		return boardPairs{pairs: append([]linkPairRef(nil), current...), known: true}, mu.Unlock
	}
	return &current
}

type noticeRow struct {
	PairID, Recipient, Address, Event, Verdict, State, Reason string
	Revision, VerdictID                                       int64
}

func notices(t *testing.T, s *Store, reference string) []noticeRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT pair_id,recipient,address,event,verdict,state,reason,revision,verdict_id FROM decision_notices
		WHERE reference=? ORDER BY id`, reference)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []noticeRow
	for rows.Next() {
		var n noticeRow
		if err := rows.Scan(&n.PairID, &n.Recipient, &n.Address, &n.Event, &n.Verdict, &n.State, &n.Reason, &n.Revision, &n.VerdictID); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func noticeEvents(ns []noticeRow) string {
	var out []string
	for _, n := range ns {
		out = append(out, fmt.Sprintf("%s:%s:%s:%d", n.Recipient, n.Event, n.State, n.Revision))
	}
	return strings.Join(out, " ")
}

// A recipient NAME@COMPUTER of a paired computer resolves as remote with its pair; another
// computer's name is unresolved with link_computer_unknown; a local name is unchanged.
func TestBoardRemoteRecipientsResolve(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	created := mustCreate(t, s, f.peerKey, "R-1", DecisionFields{Title: ptr("Remote"), Kind: ptr("decision"),
		Recipients: &[]string{"agent-one@alpha", "agent-two@gamma", f.peer.Name}})
	r := created.Recipients
	if len(r) != 3 || r[0].Kind != "remote" || !r[0].Resolved || r[0].Pair != pairAlpha ||
		r[1].Resolved || r[1].Reason != "link_computer_unknown" || r[2].Kind != "peer" {
		t.Fatalf("created recipients: %+v", r)
	}
	got, err := s.DecisionGet(ctx, f.peerKey, "R-1")
	if err != nil || got.Recipients[0].Kind != "remote" || got.Recipients[0].Notice != "queued" ||
		got.Recipients[1].Reason != "link_computer_unknown" {
		t.Fatalf("get: %+v %v", got.Recipients, err)
	}
	if n := notices(t, s, "R-1"); noticeEvents(n) != "agent-one:named:queued:1" || n[0].PairID != pairAlpha || n[0].Address != "agent-one@alpha" {
		t.Fatalf("notices: %+v", n)
	}
	// The local name list of mine is unchanged by a remote recipient.
	if mine, err := s.DecisionList(ctx, f.peerKey, "mine", 0, 0); err != nil || len(mine.Items) != 1 {
		t.Fatalf("mine: %+v %v", mine, err)
	}
	// Without a daemon fence there is no pair, so the address is unresolved.
	s.linkFence = nil
	if c := mustCreate(t, s, f.peerKey, "R-2", DecisionFields{Title: ptr("No pairs"), Kind: ptr("fyi"),
		Recipients: &[]string{"agent-one@alpha"}}); c.Recipients[0].Reason != "link_computer_unknown" || len(notices(t, s, "R-2")) != 0 {
		t.Fatalf("no pairs: %+v", c.Recipients)
	}
}

// Each event queues one notice per remote recipient, in its own transaction: create, edit
// (changed, named, removed), verdict, withdraw, the reopen of a withdrawal, and close.
func TestBoardNoticesForEachEvent(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"}, linkPairRef{ID: pairBeta, Effective: "beta"})
	mustCreate(t, s, f.peerKey, "E-1", DecisionFields{Title: ptr("Events"), Kind: ptr("decision"),
		Recipients: &[]string{"agent-one@alpha", "agent-two@alpha", f.address}})
	if _, err := s.DecisionEdit(ctx, f.peerKey, "E-1", 1, DecisionFields{Recipients: &[]string{"agent-one@alpha", "agent-three@beta", f.address}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionEdit(ctx, f.peerKey, "E-1", 2, DecisionFields{Context: ptr("More facts.")}); err != nil {
		t.Fatal(err)
	}
	v := mustVerdict(t, s, "E-1", 3, "approved")
	want := "agent-one:named:queued:1 agent-two:named:queued:1 agent-one:changed:queued:2 agent-three:named:queued:2 " +
		"agent-two:removed:queued:2 agent-one:changed:queued:3 agent-three:changed:queued:3 agent-one:verdict:queued:3 agent-three:verdict:queued:3"
	if got := noticeEvents(notices(t, s, "E-1")); got != want {
		t.Fatalf("notices:\n got %s\nwant %s", got, want)
	}
	// The remote deliveries wait for the agent's acknowledgement; the local one has its
	// inbox message as before.
	var kinds []string
	for _, d := range v.Deliveries {
		kinds = append(kinds, d.Kind+"/"+d.Outcome+"/"+d.Notice)
	}
	// The author gets the verdict too (#537).
	if strings.Join(kinds, " ") != "remote/queued/queued remote/queued/queued participant/queued/ peer/queued/" {
		t.Fatalf("deliveries: %v", kinds)
	}
	ns := notices(t, s, "E-1")
	if last := ns[len(ns)-1]; last.VerdictID != v.ID || last.Verdict != "approved" || last.PairID != pairBeta {
		t.Fatalf("verdict notice: %+v", last)
	}
	// A local acknowledgement never acknowledges a remote delivery, also for the same verdict.
	if acked, err := s.DecisionAck(ctx, f.holderKey, []int64{v.ID}); err != nil || len(acked) != 1 {
		t.Fatalf("local ack: %v %v", acked, err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_deliveries WHERE verdict_id=? AND kind='remote' AND outcome='queued'`, v.ID) != 2 {
		t.Fatal("the local acknowledgement changed a remote delivery")
	}
	// A delivery's transport state is its own verdict notice's, not a later notice's.
	if _, err := fixtureExec(s, `UPDATE decision_notices SET state='received' WHERE verdict_id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionClose(ctx, f.peerKey, "E-1", 3, "Done.", "close-e1", s.clock()+600); err != nil {
		t.Fatal(err)
	}
	got, err := s.DecisionGet(ctx, f.peerKey, "E-1")
	if err != nil || got.Verdict.Deliveries[0].Notice != "received" || got.Recipients[0].Notice != "queued" {
		t.Fatalf("states after close: %+v %+v %v", got.Verdict.Deliveries, got.Recipients, err)
	}
	if ns := notices(t, s, "E-1"); noticeEvents(ns[len(ns)-2:]) != "agent-one:closed:queued:3 agent-three:closed:queued:3" {
		t.Fatalf("close notices: %s", noticeEvents(ns))
	}
	// Withdraw and reopen.
	mustCreate(t, s, f.peerKey, "E-2", DecisionFields{Title: ptr("Withdrawn"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha"}})
	if _, err := s.DecisionWithdraw(ctx, f.peerKey, "E-2", 1, "Not needed.", "withdraw-e2", s.clock()+600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "E-2", 1); err != nil {
		t.Fatal(err)
	}
	if got := noticeEvents(notices(t, s, "E-2")); got != "agent-one:named:queued:1 agent-one:withdrawn:queued:1 agent-one:restored:queued:2" {
		t.Fatalf("withdraw notices: %s", got)
	}
}

// A kept remote recipient keeps its pair through a rename of the computer and an unrelated
// edit; the verdict goes to the stored pair. A computer that is no longer paired gets an
// unpaired notice and a skipped delivery.
func TestBoardRemoteIdentityIsStable(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	pairs := withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	mustCreate(t, s, f.peerKey, "S-1", DecisionFields{Title: ptr("Stable"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha"}})
	// The maintainer renames the computer, and a new pair takes the old name.
	*pairs = []linkPairRef{{ID: pairAlpha, Effective: "alpha-renamed"}, {ID: pairBeta, Effective: "alpha"}}
	withPairs(s, *pairs...)
	if _, err := s.DecisionEdit(ctx, f.peerKey, "S-1", 1, DecisionFields{Recipients: &[]string{"agent-one@alpha"}}); err != nil {
		t.Fatal(err)
	}
	v := mustVerdict(t, s, "S-1", 2, "approved")
	for _, n := range notices(t, s, "S-1") {
		if n.PairID != pairAlpha {
			t.Fatalf("a notice moved to another pairing: %+v", n)
		}
	}
	if v.Deliveries[0].Pair != pairAlpha {
		t.Fatalf("delivery pair: %+v", v.Deliveries[0])
	}
	// Unpaired now: the next event's notice is unpaired and its delivery skipped.
	withPairs(s, linkPairRef{ID: pairBeta, Effective: "alpha"})
	mustCreate(t, s, f.peerKey, "S-2", DecisionFields{Title: ptr("Gone"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha"}})
	if _, err := fixtureExec(s, `UPDATE decision_recipients SET pair_id=? WHERE item_id=(SELECT id FROM decision_items WHERE reference='S-2')`, pairAlpha); err != nil {
		t.Fatal(err)
	}
	v = mustVerdict(t, s, "S-2", 1, "approved")
	if d := v.Deliveries[0]; d.Outcome != "skipped" || d.Reason != "unpaired" || d.Notice != "unpaired" {
		t.Fatalf("unpaired delivery: %+v", d)
	}
}

// A failed notice insert rolls back the whole event; a repeated create key queues nothing
// more.
func TestBoardNoticeCaptureIsAtomic(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	fields := DecisionFields{Title: ptr("Atomic"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha"}}
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER notice_fails BEFORE INSERT ON decision_notices BEGIN SELECT RAISE(ABORT,'synthetic notice failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionCreate(ctx, f.peerKey, "A-1", fields, "key-a1", s.clock()+600); err == nil {
		t.Fatal("create passed a failed notice")
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items WHERE reference='A-1'`) != 0 {
		t.Fatal("the card stayed after its notice failed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER temp.notice_fails`); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, f.peerKey, "A-1", fields)
	if _, err := s.db.Exec(`CREATE TEMP TRIGGER notice_fails BEFORE INSERT ON decision_notices BEGIN SELECT RAISE(ABORT,'synthetic notice failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionVerdict(verdictCtx(), "A-1", 1, "approved", ""); err == nil {
		t.Fatal("verdict passed a failed notice")
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_verdicts`) != 0 || count(t, s, `SELECT COUNT(*) FROM decision_deliveries`) != 0 {
		t.Fatal("the verdict or its delivery stayed after its notice failed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER temp.notice_fails`); err != nil {
		t.Fatal(err)
	}
	// The retry of the first create returns its result and queues nothing.
	again, err := s.DecisionCreate(ctx, f.peerKey, "A-1", fields, "key-A-1", s.clock()+600)
	if err != nil || !again.Duplicate || len(notices(t, s, "A-1")) != 1 {
		t.Fatalf("retry: %+v %v %d", again, err, len(notices(t, s, "A-1")))
	}
}

// Unpair retires the pair's live notices; the reconciliation sees a pair with notices only.
func TestBoardUnpairRetiresNotices(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"}, linkPairRef{ID: pairBeta, Effective: "beta"})
	mustCreate(t, s, f.peerKey, "U-1", DecisionFields{Title: ptr("Unpair"), Kind: ptr("decision"),
		Recipients: &[]string{"agent-one@alpha", "agent-two@beta"}})
	for _, state := range []string{"sending", "uncertain", "unsupported"} {
		mustCreate(t, s, f.peerKey, "U-"+state, DecisionFields{Title: ptr(state), Kind: ptr("fyi"), Recipients: &[]string{"agent-one@alpha"}})
		if _, err := fixtureExec(s, `UPDATE decision_notices SET state=? WHERE reference=?`, state, "U-"+state); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.linkPairIDs(ctx)
	if err != nil || strings.Join(ids, ",") != pairAlpha+","+pairBeta {
		t.Fatalf("pair IDs with notices only: %v %v", ids, err)
	}
	if err := s.linkRetire(ctx, pairAlpha); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_notices WHERE pair_id=? AND state<>'unpaired'`, pairAlpha); n != 0 {
		t.Fatalf("%d live notices after the unpair", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_notices WHERE pair_id=? AND state='queued'`, pairBeta); n != 1 {
		t.Fatal("the unpair changed another pair's notice")
	}
}

// The message stream and the notice stream of one pair and direction are separate rows:
// a notice stream row never moves the message stream's sequence or high water.
func TestBoardNoticeStreamIsSeparate(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := fixtureExec(s, `INSERT INTO link_streams(pair_id,stream,direction,next_seq,high_water,last_result,last_digest)
		VALUES (?,'notice','out',41,0,'',''),(?,'notice','in',1,40,'received','x')`, pairAlpha, pairAlpha); err != nil {
		t.Fatal(err)
	}
	r := join(t, s, "codex", "synthetic-stream-sender", "")
	out, err := s.linkEnqueue(ctx, Key{"codex", r.ID}, pairAlpha, "alpha", "agent-one", "synthetic body")
	if err != nil {
		t.Fatal(err)
	}
	head, found, err := s.linkNextHead(ctx, pairAlpha)
	if err != nil || !found || head.Seq != 1 {
		t.Fatalf("message head: %+v %v %v (outcome %+v)", head, found, err, out)
	}
	res, err := s.linkReceive(ctx, linkDelivery{Pair: pairAlpha, Seq: 1, XID: strings.Repeat("c", 32), Recipient: r.Name,
		Sender: "agent-one", Family: "claude", Body: "synthetic body", Digest: "d1", Computer: "alpha"})
	if err != nil || res.Result != "received" {
		t.Fatalf("message receive: %+v %v", res, err)
	}
	var next, high int64
	if err := s.db.QueryRow(`SELECT next_seq,high_water FROM link_streams WHERE pair_id=? AND stream='notice' AND direction='out'`, pairAlpha).Scan(&next, &high); err != nil || next != 41 {
		t.Fatalf("notice out stream: %d %d %v", next, high, err)
	}
	if err := s.db.QueryRow(`SELECT high_water FROM link_streams WHERE pair_id=? AND stream='notice' AND direction='in'`, pairAlpha).Scan(&high); err != nil || high != 40 {
		t.Fatalf("notice in stream: %d %v", high, err)
	}
	if err := s.db.QueryRow(`SELECT high_water FROM link_streams WHERE pair_id=? AND stream='message' AND direction='in'`, pairAlpha).Scan(&high); err != nil || high != 1 {
		t.Fatalf("message in stream: %d %v", high, err)
	}
}

// The deep archive keeps the remote columns and waits for live notices; restore brings them
// back and notifies.
func TestBoardDeepArchiveKeepsRemoteRows(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	withPairs(s, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	mustCreate(t, s, f.peerKey, "D-1", DecisionFields{Title: ptr("Archive"), Kind: ptr("fyi"), Recipients: &[]string{"agent-one@alpha"}})
	v := mustVerdict(t, s, "D-1", 1, "acknowledged")
	*f.clock = f.clock.Add(31 * 24 * time.Hour)
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items WHERE reference='D-1'`) != 1 {
		t.Fatal("moved an item with a queued remote delivery and live notices")
	}
	if _, err := fixtureExec(s, `UPDATE decision_deliveries SET outcome='acknowledged' WHERE verdict_id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items WHERE reference='D-1'`) != 1 {
		t.Fatal("moved an item with live notices")
	}
	if _, err := fixtureExec(s, `UPDATE decision_notices SET state='received' WHERE reference='D-1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveDecisions(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM decision_items WHERE reference='D-1'`) != 0 || count(t, s, `SELECT COUNT(*) FROM decision_notices WHERE reference='D-1'`) != 0 {
		t.Fatal("the item or its finished notices stayed after the move")
	}
	later := join(t, s, "claude", "synthetic-restorer", "")
	if _, err := s.DecisionRestore(ctx, Key{"claude", later.ID}, "D-1", 0); err != nil {
		t.Fatal(err)
	}
	var pair, remote string
	if err := s.db.QueryRow(`SELECT pair_id,remote_name FROM decision_recipients`).Scan(&pair, &remote); err != nil || pair != pairAlpha || remote != "agent-one" {
		t.Fatalf("restored recipient: %q %q %v", pair, remote, err)
	}
	if err := s.db.QueryRow(`SELECT pair_id,remote_name FROM decision_deliveries`).Scan(&pair, &remote); err != nil || pair != pairAlpha || remote != "agent-one" {
		t.Fatalf("restored delivery: %q %q %v", pair, remote, err)
	}
	if got := noticeEvents(notices(t, s, "D-1")); got != "agent-one:restored:queued:1" {
		t.Fatalf("restore notice: %s", got)
	}
}

// boardV39Store builds a schema 39 store with cards that name NAME@COMPUTER, as v0.13.1 left
// them: unknown_name recipients and skipped deliveries.
func boardV39Store(t *testing.T) (*Store, string, *verdictFixture) {
	t.Helper()
	f := newVerdictFixture(t)
	s := f.s
	mustCreate(t, s, f.peerKey, "M-active", DecisionFields{Title: ptr("Active"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha", f.peer.Name}})
	mustCreate(t, s, f.peerKey, "M-verdict", DecisionFields{Title: ptr("Verdict"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha", "agent-two@gamma"}})
	mustVerdict(t, s, "M-verdict", 1, "approved")
	mustCreate(t, s, f.peerKey, "M-archived", DecisionFields{Title: ptr("Archived"), Kind: ptr("fyi"), Recipients: &[]string{"agent-one@alpha"}})
	mustVerdict(t, s, "M-archived", 1, "acknowledged")
	if _, err := fixtureExec(s, undoSchemaForty+`PRAGMA user_version=39`); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_deliveries WHERE name='agent-one@alpha' AND outcome='skipped' AND reason='unknown_name'`); n != 2 {
		t.Fatalf("fixture: %d skipped deliveries", n)
	}
	root := s.root
	s.db.Close()
	return s, root, f
}

func writePairs(t *testing.T, root string, pairs ...linkPairRef) {
	t.Helper()
	st := link.State{Version: 1, Name: "origin", Port: link.DefaultPort}
	for i, p := range pairs {
		st.Pairs = append(st.Pairs, link.Pair{ID: p.ID, Advertised: p.Effective, Effective: p.Effective, Pin: fmt.Sprintf("%064x", i+1),
			DNSName: p.Effective + ".example-tailnet.ts.net", IPs: []string{"100.64.0.1"}, Port: link.DefaultPort, State: link.Paired, Role: link.Inviter, Created: 1})
	}
	if err := link.WriteState(root, st); err != nil {
		t.Fatal(err)
	}
}

// The 39 to 40 migration reconciles open cards that already name a paired computer's agent,
// once; archived cards stay as they are.
func TestBoardMigrationReconcilesTaggedCards(t *testing.T) {
	_, root, _ := boardV39Store(t)
	writePairs(t, root, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	if got := noticeEvents(notices(t, s, "M-active")); got != "agent-one:named:queued:1" {
		t.Fatalf("active card: %s", got)
	}
	if got := noticeEvents(notices(t, s, "M-verdict")); got != "agent-one:named:queued:1 agent-one:verdict:queued:1" {
		t.Fatalf("card with a verdict: %s", got)
	}
	if got := notices(t, s, "M-archived"); len(got) != 0 {
		t.Fatalf("archived card: %+v", got)
	}
	var kind, outcome, pair string
	if err := s.db.QueryRow(`SELECT d.kind,d.outcome,d.pair_id FROM decision_deliveries d JOIN decision_verdicts v ON v.id=d.verdict_id
		JOIN decision_items i ON i.id=v.item_id WHERE i.reference='M-verdict' AND d.name='agent-one@alpha'`).Scan(&kind, &outcome, &pair); err != nil ||
		kind != "remote" || outcome != "queued" || pair != pairAlpha {
		t.Fatalf("reconciled delivery: %s %s %s %v", kind, outcome, pair, err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_deliveries WHERE name='agent-two@gamma' AND outcome='skipped' AND reason='unknown_name'`); n != 1 {
		t.Fatal("an unpaired computer's delivery changed")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_deliveries WHERE name='agent-one@alpha' AND outcome='skipped'`); n != 1 {
		t.Fatal("the archived card's delivery changed")
	}
	// A reopen at schema 40 reconciles nothing again.
	before := count(t, s, `SELECT COUNT(*) FROM decision_notices`)
	s.db.Close()
	if s, err = openStore(root); err != nil {
		t.Fatal(err)
	}
	if after := count(t, s, `SELECT COUNT(*) FROM decision_notices`); after != before {
		t.Fatalf("reopen added notices: %d to %d", before, after)
	}
}

// An unreadable link file stops the open before the migration: the store stays at schema 39
// with its data, and a later open with a good file migrates and reconciles. A missing file is
// no pair.
func TestBoardMigrationNeedsReadableLinkState(t *testing.T) {
	_, root, _ := boardV39Store(t)
	if err := os.WriteFile(filepath.Join(root, link.StateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(root); err == nil || !strings.Contains(err.Error(), "link_state_invalid") {
		t.Fatalf("open with a malformed link file: %v", err)
	}
	raw := rawStore(t, root)
	var version int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 39 {
		t.Fatalf("version after a failed open: %d %v", version, err)
	}
	raw.Close()
	writePairs(t, root, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(notices(t, s, "M-verdict")); got != 2 {
		t.Fatalf("retry reconciled %d notices", got)
	}
	s.db.Close()
	// A store with no link file migrates with no pair and reconciles nothing.
	_, other, _ := boardV39Store(t)
	s, err = openStore(other)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if n := count(t, s, `SELECT COUNT(*) FROM decision_notices`); n != 0 {
		t.Fatalf("no link file reconciled %d notices", n)
	}
}

// The daemon's fence orders a card's capture with an unpair: after both finish, no notice
// of the unpaired pair is live; an inbound message delivery and card creations do not
// deadlock.
func TestBoardFenceOrdersCaptureWithUnpair(t *testing.T) {
	s, root := testStore(t)
	d := &Daemon{store: s, root: root}
	s.linkFence = d.boardFence
	writePairs(t, d.root, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	peer := join(t, d.store, "claude", "synthetic-fence-peer", "")
	caller := Key{"claude", peer.ID}
	ctx := context.Background()
	create := func(i int) error {
		_, err := d.store.DecisionCreate(ctx, caller, fmt.Sprintf("F-%d", i), DecisionFields{Title: ptr("Fence"), Kind: ptr("fyi"),
			Recipients: &[]string{"agent-one@alpha"}}, fmt.Sprintf("fence-%d", i), d.store.clock()+600)
		return err
	}
	// One card is captured before the unpair, so its notice must be retired.
	if err := create(0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 1; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := create(i); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		d.link.mu.Lock()
		err := link.WriteState(d.root, link.State{Version: 1, Name: "origin", Port: link.DefaultPort})
		d.link.mu.Unlock()
		if err != nil {
			t.Error(err)
			return
		}
		if err := d.linkRetirePair(pairAlpha); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	// Each card either has its notice, retired, or was created after the unpair with an
	// unresolved recipient.
	if n := count(t, d.store, `SELECT COUNT(*) FROM decision_notices`) + count(t, d.store,
		`SELECT COUNT(*) FROM decision_recipients WHERE kind='' AND reason='link_computer_unknown'`); n != 8 {
		t.Fatalf("%d cards accounted for, want 8", n)
	}
	if n := count(t, d.store, `SELECT COUNT(*) FROM decision_notices WHERE pair_id=? AND `+noticeLive, pairAlpha); n != 0 {
		t.Fatalf("%d live notices of the unpaired pair", n)
	}
}

// Card writes, an inbound message delivery (which holds the link lock into the store, as
// answerDeliver does) and a forwarded signed verdict (which runs under the link lock, as
// answerMaintainer does) finish together: the lock order is one.
func TestBoardFenceHasOneLockOrder(t *testing.T) {
	s, root := testStore(t)
	d := &Daemon{store: s, root: root}
	s.linkFence = d.boardFence
	writePairs(t, root, linkPairRef{ID: pairAlpha, Effective: "alpha"})
	peer := join(t, s, "claude", "synthetic-order-peer", "")
	receiver := join(t, s, "codex", "synthetic-order-receiver", "")
	caller := Key{"claude", peer.ID}
	ctx := context.Background()
	mustCreate(t, s, caller, "O-0", DecisionFields{Title: ptr("Forwarded"), Kind: ptr("decision"), Recipients: &[]string{"agent-one@alpha"}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 1; i <= 6; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if _, err := s.DecisionCreate(ctx, caller, fmt.Sprintf("O-%d", i), DecisionFields{Title: ptr("Order"), Kind: ptr("fyi"),
					Recipients: &[]string{"agent-one@alpha"}}, fmt.Sprintf("order-%d", i), s.clock()+600); err != nil {
					t.Error(err)
				}
			}(i)
		}
		// One stream delivers in order, as the sender's worker does.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 6; i++ {
				d.link.mu.Lock()
				_, err := s.linkReceive(ctx, linkDelivery{Pair: pairAlpha, Seq: int64(i), XID: fmt.Sprintf("%032x", i), Recipient: receiver.Name,
					Sender: "agent-one", Family: "claude", Body: "synthetic body", Digest: fmt.Sprintf("d%d", i), Computer: "alpha"})
				d.link.mu.Unlock()
				if err != nil {
					t.Error(err)
				}
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.link.mu.Lock()
			defer d.link.mu.Unlock()
			if _, err := s.DecisionVerdict(context.WithValue(verdictCtx(), forwardedKey{}, "synthetic-pin"), "O-0", 1, "approved", ""); err != nil {
				t.Error(err)
			}
		}()
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("card writes, deliveries and a forwarded verdict did not finish: a lock order inversion")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_notices WHERE event='verdict' AND state='queued'`); n != 1 {
		t.Fatalf("forwarded verdict notices: %d", n)
	}
}
