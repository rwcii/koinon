package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Reported results and funded verdicts (#140 chunk 02): synthetic sessions, temporary state.

func report(s *Store, m MemoryCaller, id string, generation int64, extra map[string]any) (map[string]any, error) {
	fields := map[string]any{"work_id": id, "if_revision": revisionOf(s, m, id), "claim_generation": generation,
		"summary": "synthetic report", "references": []string{"synthetic evidence"}, "key": key(), "deadline": now(s) + 600}
	for k, v := range extra {
		fields[k] = v
	}
	return work(s, m, "work-report", fields)
}

func mustReport(t *testing.T, s *Store, m MemoryCaller, id string, generation int64) int64 {
	t.Helper()
	out, err := report(s, m, id, generation, nil)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	return int64(out["seq"].(float64))
}

func verdict(s *Store, m MemoryCaller, op, id string, seq int64, extra map[string]any) (map[string]any, error) {
	fields := map[string]any{"work_id": id, "if_revision": revisionOf(s, m, id), "report_seq": seq,
		"references": []string{"synthetic verdict evidence"}, "key": key(), "deadline": now(s) + 600}
	if op == "work-reject" {
		fields["criteria"], fields["non_goals"] = "synthetic corrected criteria", "synthetic corrected non-goals"
	}
	for k, v := range extra {
		fields[k] = v
	}
	return work(s, m, op, fields)
}

// revisionOf reads the current revision through a caller that may not own the item.
func revisionOf(s *Store, m MemoryCaller, id string) int64 {
	out, err := work(s, m, "work-get", map[string]any{"work_id": id})
	if err != nil {
		return 1
	}
	return int64(out["revision"].(float64))
}

func verdictCredits(t *testing.T, s *Store) int64 {
	t.Helper()
	return count(t, s, `SELECT COALESCE(SUM(verdict_credit),0) FROM work_items`)
}

func TestWorkReportAcceptAndRetries(t *testing.T) {
	s, clock, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic reported work")
	generation := mustStart(t, s, m, id, map[string]any{"resources": [][]string{{"path", "src"}}})
	debt := mustDebt(t, s)
	out, err := report(s, m, id, generation, map[string]any{"summary": "synthetic summary", "references": []string{"commit 1", "test log"}})
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(out["seq"].(float64))
	item := get(t, s, reviewer, id)
	if item["lifecycle"] != "reported" || item["current_claim"] != nil || item["progress_deadline"] != nil ||
		item["report_seq"] != float64(seq) || item["report_consumer"] != m.Consumer || item["verdict"] != nil {
		t.Fatalf("reported view: %v", item)
	}
	frozen := item["report"].(map[string]any)
	if frozen["summary"] != "synthetic summary" || len(frozen["references"].([]any)) != 2 || frozen["claim_generation"] != float64(generation) {
		t.Fatalf("frozen report: %v", frozen)
	}
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles WHERE active=1`) != 0 || verdictCredits(t, s) != 1 {
		t.Fatal("the report kept its claim or did not reserve its verdict")
	}
	if got := mustDebt(t, s); got != debt-overdueCreditPages-endCreditPages+verdictCreditPages {
		t.Fatalf("debt after report: %d from %d", got, debt)
	}
	// The resources are free: another item can claim the same path.
	other := create(t, s, m, "synthetic other work")
	mustStart(t, s, reviewer, other, map[string]any{"resources": [][]string{{"path", "src"}}})
	// A list shows the pointers; the reported item never expires.
	list := mustWork(t, s, reviewer, "work-list", map[string]any{"lifecycle": "reported"})["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["report_seq"] != float64(seq) {
		t.Fatalf("reported list: %v", list)
	}
	*clock = clock.Add(40 * 24 * time.Hour)
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if get(t, s, reviewer, id)["lifecycle"] != "reported" {
		t.Fatal("reported work expired")
	}
	// Stale report sequences and revisions refuse; the reporter is refused.
	if _, err := verdict(s, reviewer, "work-accept", id, seq+100, nil); wcode(err) != "stale_report" {
		t.Fatalf("stale report sequence: %v", err)
	}
	if _, err := verdict(s, reviewer, "work-accept", id, seq, map[string]any{"if_revision": 1}); wcode(err) != "revision_conflict" {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := verdict(s, m, "work-accept", id, seq, nil); wcode(err) != "own_report" {
		t.Fatalf("reporter verdict: %v", err)
	}
	fixed := key()
	first, err := verdict(s, reviewer, "work-accept", id, seq, map[string]any{"key": fixed})
	if err != nil {
		t.Fatal(err)
	}
	item = get(t, s, reviewer, id)
	if item["lifecycle"] != "finished" || item["outcome"] != "completed" || item["verdict"] != "accepted" ||
		item["verdict_seq"] != first["seq"] || item["expires_at"] != now(s)+workRetention {
		t.Fatalf("accepted view: %v", item)
	}
	if verdictCredits(t, s) != 0 {
		t.Fatal("accept did not spend its credit")
	}
	again, err := verdict(s, reviewer, "work-accept", id, seq, map[string]any{"key": fixed, "if_revision": first["revision"].(float64) - 1})
	if err != nil || again["duplicate"] != true || again["seq"] != first["seq"] {
		t.Fatalf("fixed retry: %v %v", again, err)
	}
	if _, err := verdict(s, reviewer, "work-reject", id, seq, nil); wcode(err) != "invalid_transition" {
		t.Fatalf("second verdict: %v", err)
	}
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM work_events WHERE kind='accepted'`).Scan(&payload); err != nil ||
		!strings.Contains(payload, `"verdict_evidence":{"report_seq":`+fmt.Sprint(seq)) {
		t.Fatalf("accepted payload: %s %v", payload, err)
	}
}

func TestWorkRejectReturnsOpenForAFreshStart(t *testing.T) {
	s, _, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic rework")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	out, err := verdict(s, reviewer, "work-reject", id, seq, map[string]any{"title": "synthetic corrected title"})
	if err != nil {
		t.Fatal(err)
	}
	item := get(t, s, reviewer, id)
	scopes := item["scope_revisions"].([]any)
	if item["lifecycle"] != "open" || item["verdict"] != "rejected" || item["current_claim"] != nil || item["title"] != "synthetic corrected title" ||
		item["criteria"] != "synthetic corrected criteria" || scopes[len(scopes)-1] != out["revision"] || item["report_seq"] != float64(seq) {
		t.Fatalf("rejected view: %v", item)
	}
	if verdictCredits(t, s) != 0 || count(t, s, `SELECT COUNT(*) FROM work_events WHERE kind IN ('reported','rejected')`) != 2 {
		t.Fatal("reject kept its credit or lost the report")
	}
	// The old scope stays readable; the worker starts a fresh generation and reports again.
	if old := mustWork(t, s, reviewer, "work-get", map[string]any{"work_id": id, "revision": 1}); old["criteria"] != "synthetic criteria" {
		t.Fatalf("old scope: %v", old)
	}
	second := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	if _, err := verdict(s, reviewer, "work-accept", id, seq, nil); wcode(err) != "stale_report" {
		t.Fatalf("verdict on the earlier report: %v", err)
	}
	if _, err := verdict(s, reviewer, "work-accept", id, second, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWorkReportedStateRefusesOtherWrites(t *testing.T) {
	s, _, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic reported state")
	generation := mustStart(t, s, m, id, nil)
	seq := mustReport(t, s, m, id, generation)
	rev := revisionOf(s, m, id)
	refusals := map[string]map[string]any{
		"work-start":   {"checkpoint": "synthetic", "next_artifact": "synthetic", "progress_deadline": now(s) + 60},
		"work-edit":    {"criteria": "synthetic"},
		"work-update":  {"claim_generation": generation, "progress": "synthetic", "checkpoint": "synthetic", "next_artifact": "synthetic", "progress_deadline": now(s) + 60},
		"work-release": {"claim_generation": generation, "checkpoint": "synthetic"},
		"work-finish":  {"claim_generation": generation, "outcome": "withdrawn", "reason": "synthetic"},
		"work-report":  {"claim_generation": generation, "summary": "synthetic", "references": []string{"synthetic"}},
	}
	for op, fields := range refusals {
		fields["work_id"], fields["if_revision"], fields["key"], fields["deadline"] = id, rev, key(), now(s)+600
		if _, err := work(s, m, op, fields); wcode(err) != "invalid_transition" {
			t.Fatalf("%s on reported work: %v", op, err)
		}
	}
	mustWork(t, s, m, "work-propose", map[string]any{"work_id": id, "if_revision": rev, "proposed_assignee": "codex:synthetic-reviewer",
		"key": key(), "deadline": now(s) + 600})
	item := get(t, s, reviewer, id)
	if item["lifecycle"] != "reported" || item["report_seq"] != float64(seq) || verdictCredits(t, s) != 1 {
		t.Fatalf("propose changed the report: %v", item)
	}
	if _, err := verdict(s, reviewer, "work-accept", id, seq, nil); err != nil {
		t.Fatal(err)
	}
	// Only reported work takes a verdict.
	open := create(t, s, m, "synthetic open")
	if _, err := verdict(s, reviewer, "work-accept", open, 1, nil); wcode(err) != "invalid_transition" {
		t.Fatalf("verdict on open work: %v", err)
	}
}

// The reporting native session is refused under every consumer label; a different native
// session of the same participant can record the verdict (PDR 004, Decision 3).
func TestWorkVerdictExcludesTheReportingSessionOnly(t *testing.T) {
	s, _, _ := workStore(t)
	repo := namedRepo(t, "verdict-succession")
	a := joinAs(t, s, "codex", "synthetic-worker-a", repo, "worker")
	b := joinAs(t, s, "codex", "synthetic-worker-b", repo, "worker")
	coordinator := joinAs(t, s, "claude", "synthetic-coordinator", repo, "")
	if err := s.ChooseHolder(context.Background(), a.Address, Key{a.Family, a.ID}, a.Revision); err != nil {
		t.Fatal(err)
	}
	ma := participantCaller(t, s, Key{a.Family, a.ID})
	mc := participantCaller(t, s, Key{coordinator.Family, coordinator.ID})
	id := createRoleWork(t, s, mc, "codex", "worker")
	seq := mustReport(t, s, ma, id, mustStart(t, s, ma, id, nil))
	for _, label := range []string{"synthetic-relabel", "codex:synthetic-worker-a"} {
		relabelled, err := s.ResolveWorkCaller(context.Background(), Key{a.Family, a.ID}, &label)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range []string{"work-accept", "work-reject"} {
			if _, err := verdict(s, relabelled, op, id, seq, nil); wcode(err) != "own_report" {
				t.Fatalf("reporting session as %s, %s: %v", label, op, err)
			}
		}
	}
	// A also reports a default-participant item, under the participant consumer.
	legacy := create(t, s, ma, "synthetic participant work")
	legacySeq := mustReport(t, s, ma, legacy, mustStart(t, s, ma, legacy, nil))
	// The participant moves to B, another native session. B's participant consumer is
	// the legacy report's consumer and is refused; B's own consumer succeeds, and the role
	// report's native-bound consumer is not B's, so B's participant consumer may accept it.
	b = renew(t, s, b)
	if err := s.ChooseHolder(context.Background(), a.Address, Key{b.Family, b.ID}, b.Revision); err != nil {
		t.Fatal(err)
	}
	mb := participantCaller(t, s, Key{b.Family, b.ID})
	if !isParticipantID(mb.Consumer) || mb.Consumer != ma.Consumer {
		t.Fatalf("B's default consumer: %s, A's %s", mb.Consumer, ma.Consumer)
	}
	if _, err := verdict(s, mb, "work-accept", legacy, legacySeq, nil); wcode(err) != "own_report" {
		t.Fatalf("reporting consumer from a successor: %v", err)
	}
	own := "codex:synthetic-worker-b"
	mbOwn, err := s.ResolveWorkCaller(context.Background(), Key{b.Family, b.ID}, &own)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verdict(s, mbOwn, "work-accept", legacy, legacySeq, nil); err != nil {
		t.Fatalf("successor session verdict: %v", err)
	}
	if _, err := verdict(s, mb, "work-reject", id, seq, nil); err != nil {
		t.Fatalf("successor participant verdict on the role report: %v", err)
	}
}

// A report refuses when its verdict cannot be funded, and keeps its claim; every
// acknowledged report can still accept or reject at saturation.
func TestWorkReportFundingAndVerdictsAtSaturation(t *testing.T) {
	s, clock, m := workStore(t)
	s.memory.limits.logical, s.memory.limits.workLogical, s.memory.limits.entries = 1<<40, 1<<40, 1<<20
	reviewer := other(m, "codex:synthetic-reviewer")
	ids := make([]string, maxStoreVerdicts+1)
	generations := make([]int64, len(ids))
	big := strings.Repeat("x", 4096)
	for i := range ids {
		out := mustWork(t, s, m, "work-create", map[string]any{"title": big[:256], "criteria": big, "non_goals": big[:2048],
			"key": key(), "deadline": now(s) + 600})
		ids[i] = out["work_id"].(string)
	}
	refs := []string{big[:512], big[:512], big[:512], big[:512], big[:512], big[:512], big[:512], big[:512]}
	seqs := make([]int64, maxStoreVerdicts)
	for i := range seqs {
		generations[i] = mustStart(t, s, other(m, fmt.Sprint("writer-", i)), ids[i], map[string]any{"checkpoint": big[:1024], "next_artifact": big[:1024]})
		out, err := report(s, other(m, fmt.Sprint("writer-", i)), ids[i], generations[i], map[string]any{"summary": big[:2048], "references": refs[:4]})
		if err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
		seqs[i] = int64(out["seq"].(float64))
	}
	// As after a finish, maintenance reclaims the released bundles.
	for range seqs {
		if err := s.MaintainWork(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	last := len(ids) - 1
	writer := other(m, "writer-last")
	generations[last] = mustStart(t, s, writer, ids[last], nil)
	var refusal WorkRefusal
	if _, err := report(s, writer, ids[last], generations[last], nil); !errors.As(err, &refusal) || refusal.Code != "capacity" ||
		!strings.Contains(refusal.Message, "store's pending verdict") {
		t.Fatalf("seventeenth pending verdict: %v", err)
	}
	if get(t, s, writer, ids[last])["current_claim"] == nil {
		t.Fatal("the refused report lost its claim")
	}
	if mustDebt(t, s) != maxStoreVerdicts*verdictCreditPages+overdueCreditPages+endCreditPages {
		t.Fatalf("debt: %d", mustDebt(t, s))
	}
	// Fill the ordinary band: the claim's finish and every verdict still commit.
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + mustDebt(t, s) + 64
	for {
		_, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: big + big})
		if err != nil {
			if code(err) != "capacity" {
				t.Fatal(err)
			}
			break
		}
	}
	mustWork(t, s, writer, "work-finish", map[string]any{"work_id": ids[last], "if_revision": revisionOf(s, writer, ids[last]),
		"claim_generation": generations[last], "outcome": "withdrawn", "reason": "synthetic", "key": key(), "deadline": now(s) + 600})
	*clock = clock.Add(time.Second)
	largest := map[string]int64{}
	for i, id := range ids[:maxStoreVerdicts] {
		op, extra := "work-accept", map[string]any(nil)
		if i%2 == 1 {
			op, extra = "work-reject", map[string]any{"title": big[:256], "criteria": big, "non_goals": big[:2048]}
		}
		before := count(t, s, `PRAGMA page_count`)
		if _, err := verdict(s, reviewer, op, id, seqs[i], extra); err != nil {
			t.Fatalf("%s %d at saturation: %v", op, i, err)
		}
		growth := count(t, s, `PRAGMA page_count`) - before
		if growth > verdictCreditPages {
			t.Fatalf("%s grew %d pages", op, growth)
		}
		largest[op] = max(largest[op], growth)
	}
	var version string
	s.db.QueryRow(`SELECT sqlite_version()`).Scan(&version)
	t.Logf("SQLite %s: largest growth: accept %d pages, reject %d pages", version, largest["work-accept"], largest["work-reject"])
	if verdictCredits(t, s) != 0 || mustDebt(t, s) != 0 {
		t.Fatalf("credits left: %d, debt %d", verdictCredits(t, s), mustDebt(t, s))
	}
	var integrity string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
}

// At most 32 verdicts are pending across all stores; the refused report keeps its claim.
func TestWorkDaemonPendingVerdictLimit(t *testing.T) {
	s, _, m := workStore(t)
	for store := 0; store <= maxDaemonVerdicts/maxStoreVerdicts; store++ {
		caller := m
		caller.Repository = fmt.Sprintf("/synthetic/repo-%d/.git", store)
		for i := 0; i < maxStoreVerdicts; i++ {
			id := create(t, s, caller, "synthetic daemon limit")
			generation := mustStart(t, s, caller, id, nil)
			_, err := report(s, caller, id, generation, nil)
			if store*maxStoreVerdicts+i < maxDaemonVerdicts {
				if err != nil {
					t.Fatalf("report %d in store %d: %v", i, store, err)
				}
				if err := s.MaintainWork(context.Background()); err != nil {
					t.Fatal(err)
				}
				continue
			}
			var refusal WorkRefusal
			if !errors.As(err, &refusal) || refusal.Code != "capacity" || !strings.Contains(refusal.Message, "daemon's pending verdict") {
				t.Fatalf("thirty-third report: %v", err)
			}
			if get(t, s, caller, id)["current_claim"] == nil {
				t.Fatal("the refused report lost its claim")
			}
			return
		}
	}
	t.Fatal("the daemon limit was not reached")
}

// The store ceilings keep each pending verdict's slots and bytes free for it.
func TestWorkVerdictReservesSlotsAndBytes(t *testing.T) {
	s, _, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic slots")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	l := s.limits()
	u, err := usage(context.Background(), s.db, m.Repository)
	if err != nil {
		t.Fatal(err)
	}
	d, err := storeDebt(context.Background(), s.db, m.Repository)
	if err != nil || d.verdict != 1 {
		t.Fatalf("debt: %+v %v", d, err)
	}
	c := l.caps(ordinary, d)
	free := l.caps(ordinary, workDebt{overdue: d.overdue, end: d.end})
	if free.entries-c.entries != 1 || free.workEvents-c.workEvents != 1 || free.idem-c.idem != 1 || free.workScopes-c.workScopes != 1 ||
		free.logical-c.logical != l.verdictBytes || free.workLogical-c.workLogical != l.verdictBytes {
		t.Fatalf("reservation: %+v against %+v", c, free)
	}
	// Ordinary writes stop at the reduced ceiling: fill entries to it.
	s.memory.limits.entries = u.Entries + l.reservedEntries + 1 + 1
	last, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "fills the last ordinary slot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "takes the verdict's slot"}); code(err) != "capacity" {
		t.Fatalf("ordinary write took the verdict's slot: %v", err)
	}
	// A withdrawal, a control write, uses the reserve; the verdict, also a control
	// write, still commits beyond the ordinary ceiling.
	if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "withdrawn", Revokes: ptr(last.Seq)}); err != nil {
		t.Fatalf("withdrawal: %v", err)
	}
	if _, err := verdict(s, reviewer, "work-reject", id, seq, nil); err != nil {
		t.Fatalf("funded verdict: %v", err)
	}
}

// At the scope-history limit a worker still starts and withdraws; the report that could
// not fund a rejection refuses and keeps the claim.
func TestWorkReportAtTheScopeHistoryLimit(t *testing.T) {
	s, _, m := workStore(t)
	s.memory.limits.workScopesPerItem = 2
	id := create(t, s, m, "synthetic scope limit")
	mustWork(t, s, m, "work-edit", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "criteria": "synthetic second",
		"key": key(), "deadline": now(s) + 600})
	generation := mustStart(t, s, m, id, nil)
	if _, err := report(s, m, id, generation, nil); wcode(err) != "capacity" {
		t.Fatalf("unfundable report: %v", err)
	}
	if verdictCredits(t, s) != 0 || get(t, s, m, id)["lifecycle"] != "active" {
		t.Fatal("the refused report changed the item")
	}
	mustWork(t, s, m, "work-finish", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "claim_generation": generation,
		"outcome": "withdrawn", "reason": "synthetic", "key": key(), "deadline": now(s) + 600})
}

// Credits survive a restart and a refused verdict; a retried verdict spends nothing twice.
func TestWorkVerdictCreditAcrossRestartAndRollback(t *testing.T) {
	s, root := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	m := MemoryCaller{Repository: "/synthetic/repo/.git", Family: "codex", Name: "codex-repo-01", Consumer: "codex:synthetic-a"}
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic restart")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	debt := mustDebt(t, s)
	s.db.Close()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.now = func() time.Time { return clock }
	if verdictCredits(t, s) != 1 || mustDebt(t, s) != debt {
		t.Fatal("restart changed the verdict credit")
	}
	// A verdict refused in its transaction rolls back and leaves the credit.
	if _, err := verdict(s, reviewer, "work-reject", id, seq, map[string]any{"criteria": strings.Repeat("c", 4097)}); wcode(err) != "invalid_request" {
		t.Fatalf("oversized rejection: %v", err)
	}
	s.memory.limits.workScopes = 1
	if _, err := verdict(s, reviewer, "work-reject", id, seq, nil); err != nil {
		t.Fatalf("a funded rejection at the scope ceiling: %v", err)
	}
	if verdictCredits(t, s) != 0 || mustDebt(t, s) != 0 {
		t.Fatal("the rejection did not spend exactly its credit")
	}
}

// Every valid verdict fits its bounds: maximum evidence, and for a rejection the maximum
// corrected scope, on an item at its admission bound or with a short original scope.
// Consumer keys have their maximum length.
func TestWorkVerdictAtTheBounds(t *testing.T) {
	big := strings.Repeat("x", 4096)
	refs := []string{big[:512], big[:512], big[:512], big[:512], big[:512], big[:512], big[:512], big[:512]}
	for _, op := range []string{"work-accept", "work-reject"} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprint(op, " short scope ", short), func(t *testing.T) {
				s, _, m := workStore(t)
				s.memory.limits.logical, s.memory.limits.workLogical = 1<<40, 1<<40
				m.Consumer = "w" + strings.Repeat("o", 127)
				reviewer := other(m, "r"+strings.Repeat("e", 127))
				fields := map[string]any{"title": big[:256], "criteria": big, "non_goals": big[:2048], "proposed_assignee": strings.Repeat("a", 128),
					"references": refs, "key": key(), "deadline": now(s) + 600}
				if short {
					fields["title"], fields["criteria"], fields["non_goals"] = "t", "c", "n"
				}
				id := mustWork(t, s, m, "work-create", fields)["work_id"].(string)
				generation := mustStart(t, s, m, id, map[string]any{"checkpoint": big[:1024], "next_artifact": big[:1024]})
				// The largest progress, and then the largest report, that the item admits.
				for progress := 2048; ; progress -= 16 {
					_, err := work(s, m, "work-update", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "claim_generation": generation,
						"progress": big[:progress], "checkpoint": big[:1024], "next_artifact": big[:1024], "progress_deadline": now(s) + 60,
						"references": refs, "key": key(), "deadline": now(s) + 600})
					if err == nil {
						break
					}
					if wcode(err) != "record_too_large" || progress <= 16 {
						t.Fatalf("update: %v", err)
					}
				}
				var seq int64
				for summary := 2048; ; summary -= 16 {
					out, err := report(s, m, id, generation, map[string]any{"summary": big[:summary], "references": refs})
					if err == nil {
						seq = int64(out["seq"].(float64))
						break
					}
					if wcode(err) != "record_too_large" || summary <= 16 {
						t.Fatalf("report: %v", err)
					}
				}
				extra := map[string]any{"references": refs}
				if op == "work-reject" {
					extra["title"], extra["criteria"], extra["non_goals"] = big[:256], big, big[:2048]
				}
				if _, err := verdict(s, reviewer, op, id, seq, extra); err != nil {
					t.Fatalf("%s at the bounds: %v", op, err)
				}
				item := get(t, s, reviewer, id)
				if item["progress"] != "" || item["checkpoint"] != "" || item["next_artifact"] != "" || len(item["references"].([]any)) != len(refs) ||
					item["report"].(map[string]any)["summary"] == "" {
					t.Fatalf("verdict view: %v", item)
				}
			})
		}
	}
}

// The store's logical charge counts the report and verdict columns, as the row charge does,
// so an ordinary write stops at the actual logical ceiling.
func TestWorkVerdictMetadataIsCharged(t *testing.T) {
	s, _, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic accounting")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	if _, err := verdict(s, reviewer, "work-accept", id, seq, nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store, err := storeOf(ctx, s.db, m.Repository)
	if err != nil {
		t.Fatal(err)
	}
	w, err := current(ctx, s.db, store, id, now(s))
	if err != nil {
		t.Fatal(err)
	}
	u, err := usage(ctx, s.db, m.Repository)
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(w.rowCost(store))
	for _, row := range []struct {
		table    string
		overhead int64
		fields   []string
	}{
		{"work_scope_revisions", workRowOverhead, []string{"store", "work_id", "consumer", "author", "title", "criteria", "non_goals"}},
		{"claim_bundles", workRowOverhead, []string{"store", "work_id", "consumer", "native_actor"}},
		{"claim_resources", workRowOverhead, []string{"store", "kind", "resource"}},
		{"work_events", workRowOverhead, []string{"store", "work_id", "kind", "payload", "actor"}},
		{"work_replays", workReplayOverhead, []string{"key", "consumer", "operation", "result", "native_actor"}},
	} {
		var bytes int64
		if err := s.db.QueryRow("SELECT COALESCE(SUM(?+"+textBytes(row.fields...)+"),0) FROM "+row.table+" WHERE store=?", row.overhead, store).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		expected += bytes
	}
	var entryBytes int64
	if err := s.db.QueryRow("SELECT COALESCE(SUM(?+"+textBytes("body", "path", "author", "scope_target", "consumer")+"),0) FROM memory_entries WHERE repository=? AND type='work-event'",
		entryOverhead, m.Repository).Scan(&entryBytes); err != nil {
		t.Fatal(err)
	}
	expected += entryBytes
	if u.WorkLogical != expected {
		t.Fatalf("work charge %d, exact row charges %d", u.WorkLogical, expected)
	}
	body := "synthetic next note"
	s.memory.limits.logical = u.Logical + int64(entryOverhead+len(m.Consumer)+len(body)) + s.limits().reservedBytes - 1
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: body}); code(err) != "capacity" {
		t.Fatalf("a note beyond the logical ceiling was admitted: %v", err)
	}
}

// Memory catch-up carries the report and verdict events with their payloads.
func TestWorkReportAndVerdictInMemoryDeltas(t *testing.T) {
	s, _, m := workStore(t)
	reader := other(m, "reader")
	drain(t, s, reader)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic deltas")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	if _, err := verdict(s, reviewer, "work-accept", id, seq, nil); err != nil {
		t.Fatal(err)
	}
	page, err := s.MemorySync(context.Background(), reader, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, raw := range page["entries"].([]json.RawMessage) {
		var e MemoryEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, e.EventKind)
		if e.EventKind == "reported" && !strings.Contains(string(e.Payload), `"report":{"summary":"synthetic report"`) {
			t.Fatalf("reported payload: %s", e.Payload)
		}
	}
	if strings.Join(kinds, ",") != "created,started,reported,accepted" {
		t.Fatalf("delta kinds: %v", kinds)
	}
}

// A schema 16 store (develop d95fac8) keeps its claims, scope history, replays and
// counters through the rebuild; its items then report and take verdicts.
func TestWorkUpgradeFromD95fac8(t *testing.T) {
	root := loadFixtureFile(t, "d95fac8.sql")
	before := map[string]int64{}
	queries := map[string]string{
		"items":    `SELECT COUNT(*) FROM work_items`,
		"events":   `SELECT COUNT(*) FROM work_events`,
		"scopes":   `SELECT COUNT(*) FROM work_scope_revisions`,
		"replays":  `SELECT COUNT(*) FROM work_replays`,
		"native":   `SELECT COUNT(*) FROM claim_bundles WHERE native_actor!=''`,
		"bundles":  `SELECT COUNT(*) FROM claim_bundles`,
		"counters": `SELECT SUM(work_counter)+SUM(claim_counter)+SUM(head) FROM memory_stores`,
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.now = func() time.Time { return time.UnixMilli(1800000001000) }
	for name, q := range queries {
		before[name] = count(t, s, q)
	}
	if count(t, s, `PRAGMA user_version`) != schemaVersion || before["items"] != 4 || before["native"] != 1 || before["replays"] == 0 {
		t.Fatalf("fixture after migration: %v", before)
	}
	rows := rowsOf(t, s.db)
	if _, err := s.db.Exec(undoSchemaThirtyThree + undoSchemaThirtyTwo + undoSchemaThirtyOne + undoSchemaThirty + undoSchemaTwentyNine + undoSchemaTwentyEight + undoSchemaTwentySeven + undoSchemaTwentySix + undoSchemaTwentyFive + undoSchemaTwentyFour + undoSchemaTwentyThree + undoSchemaTwentyTwo + undoSchemaTwentyOne + undoSchemaTwenty + undoSchemaNineteen + undoSchemaEighteen + undoSchemaSeventeen + `PRAGMA user_version=16`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 16); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db, 16, 17); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 17); err != nil {
		t.Fatal(err)
	}
	for name, q := range queries {
		if count(t, s, q) != before[name] {
			t.Fatalf("migration changed %s", name)
		}
	}
	if rowsOf(t, s.db) != rows {
		t.Fatal("migration changed claims, sessions or cursors")
	}
	var lifecycle string
	if err := s.db.QueryRow(`SELECT group_concat(lifecycle||verdict_credit) FROM (SELECT lifecycle,verdict_credit FROM work_items ORDER BY work_id)`).Scan(&lifecycle); err != nil ||
		lifecycle != "active0,active0,open0,finished0" {
		t.Fatalf("migrated items: %s %v", lifecycle, err)
	}
}

// A consumer that recorded a verdict and later reported the same item is refused before its
// earlier verdict's replay can answer it.
func TestWorkVerdictReplayRefusesTheLaterReporter(t *testing.T) {
	s, _, m := workStore(t)
	reviewer := other(m, "codex:synthetic-reviewer")
	id := create(t, s, m, "synthetic replay order")
	seq := mustReport(t, s, m, id, mustStart(t, s, m, id, nil))
	fixed := key()
	first, err := verdict(s, reviewer, "work-reject", id, seq, map[string]any{"key": fixed})
	if err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, reviewer, id, mustStart(t, s, reviewer, id, nil))
	if _, err := verdict(s, reviewer, "work-reject", id, seq, map[string]any{"key": fixed, "if_revision": first["revision"].(float64) - 1}); wcode(err) != "own_report" {
		t.Fatalf("the later reporter's replay: %v", err)
	}
}
