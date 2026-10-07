package core

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Work item tests: synthetic sessions, temporary state and an adjustable clock.

func workStore(t *testing.T) (*Store, *time.Time, MemoryCaller) {
	t.Helper()
	s, clock, m := memoryStore(t)
	m.Consumer = "codex:synthetic-a"
	return s, clock, m
}

func other(m MemoryCaller, consumer string) MemoryCaller {
	m.Consumer = consumer
	return m
}

func wcode(err error) string {
	var w WorkRefusal
	if errors.As(err, &w) {
		return w.Code
	}
	return code(err)
}

func work(s *Store, m MemoryCaller, op string, fields map[string]any) (map[string]any, error) {
	raw := map[string]json.RawMessage{}
	for k, v := range fields {
		raw[k], _ = json.Marshal(v)
	}
	result, err := s.Work(context.Background(), m, op, raw)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(result)
	var out map[string]any
	json.Unmarshal(data, &out)
	return out, nil
}

func mustWork(t *testing.T, s *Store, m MemoryCaller, op string, fields map[string]any) map[string]any {
	t.Helper()
	out, err := work(s, m, op, fields)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	return out
}

func now(s *Store) float64 { return s.clock() }

var keys atomic.Int64

func key() string { return fmt.Sprintf("synthetic-key-%d", keys.Add(1)) }

func create(t *testing.T, s *Store, m MemoryCaller, title string) string {
	t.Helper()
	out := mustWork(t, s, m, "work-create", map[string]any{"title": title, "criteria": "synthetic criteria",
		"non_goals": "synthetic non-goals", "key": key(), "deadline": now(s) + 600})
	return out["work_id"].(string)
}

func get(t *testing.T, s *Store, m MemoryCaller, id string) map[string]any {
	t.Helper()
	return mustWork(t, s, m, "work-get", map[string]any{"work_id": id})
}

func revision(t *testing.T, s *Store, m MemoryCaller, id string) int64 {
	t.Helper()
	return int64(get(t, s, m, id)["revision"].(float64))
}

func start(s *Store, m MemoryCaller, id string, revision int64, extra map[string]any) (map[string]any, error) {
	fields := map[string]any{"work_id": id, "if_revision": revision, "checkpoint": "synthetic start", "next_artifact": "synthetic artifact",
		"progress_deadline": now(s) + 3600, "key": key(), "deadline": now(s) + 600}
	for k, v := range extra {
		fields[k] = v
	}
	return work(s, m, "work-start", fields)
}

func mustStart(t *testing.T, s *Store, m MemoryCaller, id string, extra map[string]any) int64 {
	t.Helper()
	out, err := start(s, m, id, revision(t, s, m, id), extra)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return int64(out["claim"].(map[string]any)["generation"].(float64))
}

func update(s *Store, m MemoryCaller, id string, rev, generation int64, extra map[string]any) (map[string]any, error) {
	fields := map[string]any{"work_id": id, "if_revision": rev, "claim_generation": generation, "progress": "synthetic progress",
		"checkpoint": "synthetic checkpoint", "next_artifact": "synthetic next", "progress_deadline": now(s) + 3600}
	for k, v := range extra {
		fields[k] = v
	}
	return work(s, m, "work-update", fields)
}

func events(t *testing.T, s *Store, m MemoryCaller, id string) []string {
	t.Helper()
	store, _ := storeOf(context.Background(), s.db, m.Repository)
	rows, err := s.db.Query(`SELECT kind FROM work_events WHERE store=? AND work_id=? ORDER BY seq`, store, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		rows.Scan(&kind)
		kinds = append(kinds, kind)
	}
	return kinds
}

func count(t *testing.T, s *Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWorkCreateAndProposalAreNotAcceptance(t *testing.T) {
	s, _, m := workStore(t)
	out := mustWork(t, s, m, "work-create", map[string]any{"title": "synthetic", "criteria": "c", "non_goals": "n",
		"proposed_assignee": "claude:synthetic-b", "author": "synthetic author", "references": []string{"synthetic-ref"},
		"key": key(), "deadline": now(s) + 600})
	id := out["work_id"].(string)
	if len(id) != 32 || id != fmt.Sprintf("%032x", 1) || out["revision"] != 1.0 || out["duplicate"] != false {
		t.Fatalf("create: %v", out)
	}
	item := get(t, s, m, id)
	if item["lifecycle"] != "open" || item["current_claim"] != nil || item["proposed_assignee"] != "claude:synthetic-b" ||
		item["lease_valid"] != false || item["created_consumer"] != m.Consumer || item["type"] != "work-item" {
		t.Fatalf("created item: %v", item)
	}
	if _, err := work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": 5, "proposed_assignee": nil}); wcode(err) != "revision_conflict" {
		t.Fatalf("stale proposal: %v", err)
	} else if details := err.(WorkRefusal).Details; details["current_revision"] != int64(1) {
		t.Fatalf("conflict details: %v", details)
	}
	mustWork(t, s, m, "work-propose", map[string]any{"work_id": id, "if_revision": 1, "proposed_assignee": nil})
	item = get(t, s, m, id)
	if item["proposed_assignee"] != nil || item["current_claim"] != nil || item["lifecycle"] != "open" {
		t.Fatalf("cleared proposal: %v", item)
	}
	var author, family, consumer string
	if err := s.db.QueryRow(`SELECT author,writer_family,consumer FROM memory_entries WHERE type='work-event' ORDER BY seq LIMIT 1`).Scan(&author, &family, &consumer); err != nil ||
		author != "synthetic author" || family != "codex" || consumer != m.Consumer {
		t.Fatalf("provenance: %q %q %q %v", author, family, consumer, err)
	}
	if kinds := events(t, s, m, id); strings.Join(kinds, ",") != "created,proposed" {
		t.Fatalf("events: %v", kinds)
	}
}

func TestWorkReplayPrecedesPreconditions(t *testing.T) {
	s, clock, m := workStore(t)
	fields := map[string]any{"title": "synthetic", "criteria": "c", "non_goals": "n", "key": "create-1", "deadline": now(s) + 600}
	first := mustWork(t, s, m, "work-create", fields)
	id := first["work_id"].(string)
	again := mustWork(t, s, m, "work-create", fields)
	if again["duplicate"] != true || again["work_id"] != id || again["seq"] != first["seq"] {
		t.Fatalf("replay: %v %v", first, again)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM work_items`); n != 1 {
		t.Fatalf("duplicate work created: %d", n)
	}
	changed := map[string]any{"title": "other", "criteria": "c", "non_goals": "n", "key": "create-1", "deadline": now(s) + 600}
	if _, err := work(s, m, "work-create", changed); wcode(err) != "idempotency_conflict" {
		t.Fatalf("changed content: %v", err)
	}
	// Another consumer has its own key space.
	if out := mustWork(t, s, other(m, "codex:synthetic-b"), "work-create", changed); out["duplicate"] != false {
		t.Fatalf("other consumer: %v", out)
	}
	// A start replay returns its original result after later revisions.
	startFields := map[string]any{"work_id": id, "if_revision": 1, "checkpoint": "c", "next_artifact": "a",
		"progress_deadline": now(s) + 3600, "key": "start-1", "deadline": now(s) + 600}
	started := mustWork(t, s, m, "work-start", startFields)
	generation := int64(started["claim"].(map[string]any)["generation"].(float64))
	if _, err := update(s, m, id, 2, generation, nil); err != nil {
		t.Fatal(err)
	}
	replayed := mustWork(t, s, m, "work-start", startFields)
	if replayed["duplicate"] != true || replayed["revision"] != started["revision"] || replayed["seq"] != started["seq"] {
		t.Fatalf("start replay: %v %v", started, replayed)
	}
	if _, err := work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": 3, "proposed_assignee": "x", "key": "p"}); wcode(err) != "invalid_request" {
		t.Fatalf("key without deadline: %v", err)
	}
	if _, err := work(s, m, "work-create", map[string]any{"title": "t", "criteria": "c", "non_goals": "n", "key": "late", "deadline": now(s) - 1}); wcode(err) != "retry_deadline_expired" {
		t.Fatalf("expired deadline: %v", err)
	}
	if _, err := work(s, m, "work-create", map[string]any{"title": "t", "criteria": "c", "non_goals": "n", "key": "far", "deadline": now(s) + 90000}); wcode(err) != "invalid_request" {
		t.Fatalf("deadline beyond horizon: %v", err)
	}
	// After its deadline the key refuses rather than guessing.
	*clock = clock.Add(700 * time.Second)
	if _, err := work(s, m, "work-create", fields); wcode(err) != "retry_deadline_expired" {
		t.Fatalf("retry after deadline: %v", err)
	}
}

func TestWorkCompetingStartsAndResources(t *testing.T) {
	s, _, m := workStore(t)
	b := other(m, "claude:synthetic-b")
	first, second, third := create(t, s, m, "one"), create(t, s, m, "two"), create(t, s, m, "three")
	generation := mustStart(t, s, m, first, map[string]any{"resources": [][]string{{"path", "auth"}, {"exact", "db:schema"}}})
	_, err := start(s, b, first, revision(t, s, b, first), nil)
	if wcode(err) != "claim_conflict" {
		t.Fatalf("second writer: %v", err)
	}
	details := err.(WorkRefusal).Details
	if details["consumer"] != m.Consumer || details["generation"] != generation || details["work_id"] != first {
		t.Fatalf("holder: %v", details)
	}
	before := count(t, s, `SELECT claim_counter FROM memory_stores`)
	rows := count(t, s, `SELECT COUNT(*) FROM claim_resources`)
	for _, resources := range [][][]string{
		{{"path", "docs"}, {"path", "auth/session.py"}}, // all or nothing: the second member conflicts
		{{"path", "."}},
		{{"exact", "db:schema"}},
	} {
		if _, err := start(s, b, second, revision(t, s, b, second), map[string]any{"resources": resources}); wcode(err) != "claim_conflict" {
			t.Fatalf("overlap %v: %v", resources, err)
		}
		// The same consumer conflicts with itself on another work item.
		if _, err := start(s, m, second, revision(t, s, m, second), map[string]any{"resources": resources}); wcode(err) != "claim_conflict" {
			t.Fatalf("same consumer %v: %v", resources, err)
		}
	}
	if count(t, s, `SELECT claim_counter FROM memory_stores`) != before || count(t, s, `SELECT COUNT(*) FROM claim_resources`) != rows {
		t.Fatal("refused start left a partial bundle")
	}
	mustStart(t, s, b, second, map[string]any{"resources": [][]string{{"path", "authorization"}, {"exact", "auth"}}})
	mustStart(t, s, b, third, map[string]any{"resources": [][]string{{"path", "docs/a"}}})
}

func TestWorkResourceSpelling(t *testing.T) {
	for _, bad := range []claimResource{{"path", "/abs"}, {"path", "a/../b"}, {"path", "a//b"}, {"path", "./a"}, {"path", "a/"},
		{"path", "a\\b"}, {"path", "a\x01"}, {"path", ".."}, {"path", strings.Repeat("a", 513)}, {"path", ""}, {"exact", "x\n"},
		{"other", "x"}, {"writer", "nothex"}} {
		if err := checkResource(bad); wcode(err) != "invalid_request" {
			t.Errorf("accepted %q: %v", bad, err)
		}
	}
	for _, good := range []claimResource{{"path", "."}, {"path", "a/b.c"}, {"exact", "any key: /x/.."}} {
		if err := checkResource(good); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
	cases := []struct {
		a, b string
		want bool
	}{{"auth", "auth/session.py", true}, {"auth", "authorization", false}, {".", "x", true}, {"a/b", "a/b", true}, {"a/b", "a/bc", false}}
	for _, c := range cases {
		if overlaps(claimResource{"path", c.a}, claimResource{"path", c.b}) != c.want || overlaps(claimResource{"path", c.b}, claimResource{"path", c.a}) != c.want {
			t.Errorf("overlap %s %s", c.a, c.b)
		}
	}
	if overlaps(claimResource{"path", "x"}, claimResource{"exact", "x"}) {
		t.Error("namespaces overlap")
	}
	if _, err := claimBundle(fmt.Sprintf("%032x", 1), []claimResource{{"path", "a"}, {"path", "a"}}); wcode(err) != "invalid_request" {
		t.Error("duplicate resource accepted")
	}
	many := make([]claimResource, 9)
	for i := range many {
		many[i] = claimResource{"exact", fmt.Sprint(i)}
	}
	if _, err := claimBundle(fmt.Sprintf("%032x", 1), many); wcode(err) != "invalid_request" {
		t.Error("nine resources accepted")
	}
}

func TestWorkScopeHistoryAndOwnerEdits(t *testing.T) {
	s, _, m := workStore(t)
	b := other(m, "claude:synthetic-b")
	id := create(t, s, m, "scope")
	mustWork(t, s, b, "work-edit", map[string]any{"work_id": id, "if_revision": 1, "title": "renamed before start"})
	generation := mustStart(t, s, m, id, nil)
	if item := get(t, s, m, id); item["criteria_changed_after_start"] != false {
		t.Fatalf("flag before change: %v", item)
	}
	rev := revision(t, s, m, id)
	if _, err := work(s, b, "work-edit", map[string]any{"work_id": id, "if_revision": rev, "title": "x", "claim_generation": generation}); wcode(err) != "stale_claim" {
		t.Fatalf("non-owner edit: %v", err)
	}
	if _, err := work(s, m, "work-edit", map[string]any{"work_id": id, "if_revision": rev, "title": "x"}); wcode(err) != "invalid_request" {
		t.Fatalf("owner edit without generation: %v", err)
	}
	mustWork(t, s, m, "work-edit", map[string]any{"work_id": id, "if_revision": rev, "title": "title only", "claim_generation": generation})
	if item := get(t, s, m, id); item["criteria_changed_after_start"] != false {
		t.Fatalf("title-only edit flagged: %v", item)
	}
	rev = revision(t, s, m, id)
	mustWork(t, s, m, "work-edit", map[string]any{"work_id": id, "if_revision": rev, "criteria": "changed", "claim_generation": generation})
	mustWork(t, s, m, "work-edit", map[string]any{"work_id": id, "if_revision": rev + 1, "criteria": "synthetic criteria", "claim_generation": generation})
	item := get(t, s, m, id)
	if item["criteria_changed_after_start"] != true || item["criteria"] != "synthetic criteria" {
		t.Fatalf("reverted scope not flagged: %v", item)
	}
	scopes := item["scope_revisions"].([]any)
	if len(scopes) != 5 || scopes[0] != 1.0 {
		t.Fatalf("scope revisions: %v", scopes)
	}
	original := mustWork(t, s, m, "work-get", map[string]any{"work_id": id, "revision": 1})
	if original["title"] != "scope" || original["consumer"] != m.Consumer {
		t.Fatalf("retained scope: %v", original)
	}
	if _, err := work(s, m, "work-get", map[string]any{"work_id": id, "revision": 3}); wcode(err) != "work_not_found" {
		t.Fatalf("non-scope revision: %v", err)
	}
}

func TestWorkRenewalIsNotProgress(t *testing.T) {
	s, clock, m := workStore(t)
	id := create(t, s, m, "renew")
	generation := mustStart(t, s, m, id, map[string]any{"lease_seconds": 60})
	before := get(t, s, m, id)
	headBefore, _ := head(t, s, m.Repository)
	*clock = clock.Add(30 * time.Second)
	out := mustWork(t, s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1,
		"lease_seconds": 600, "key": "renew-1", "deadline": now(s) + 600})
	claim := out["claim"].(map[string]any)
	if out["seq"] != nil || claim["revision"] != 2.0 || claim["expires_at"] != now(s)+600 || out["revision"] != before["revision"] {
		t.Fatalf("renewal: %v", out)
	}
	after := get(t, s, m, id)
	headAfter, _ := head(t, s, m.Repository)
	if headAfter != headBefore || after["revision"] != before["revision"] || after["progress_deadline"] != before["progress_deadline"] ||
		after["last_lease_expires"] != before["last_lease_expires"] || after["observed_lease_expires"] != now(s)+600 {
		t.Fatalf("renewal changed history: %v %v", before, after)
	}
	// The deadline is part of the request content, so another deadline is another request.
	if _, err := work(s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1,
		"lease_seconds": 600, "key": "renew-1", "deadline": now(s) + 570}); wcode(err) != "idempotency_conflict" {
		t.Fatalf("renew key reuse: %v", err)
	}
}

func TestWorkRenewalRevisionAndReplay(t *testing.T) {
	s, _, m := workStore(t)
	id := create(t, s, m, "renew")
	generation := mustStart(t, s, m, id, nil)
	fields := map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1, "key": "renew-1", "deadline": now(s) + 600}
	first := mustWork(t, s, m, "claim-renew", fields)
	if again := mustWork(t, s, m, "claim-renew", fields); again["duplicate"] != true || again["seq"] != nil ||
		again["claim"].(map[string]any)["revision"] != first["claim"].(map[string]any)["revision"] {
		t.Fatalf("renew replay: %v %v", first, again)
	}
	if _, err := work(s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1}); wcode(err) != "revision_conflict" {
		t.Fatalf("stale claim revision: %v", err)
	}
	if _, err := work(s, other(m, "claude:synthetic-b"), "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 2}); wcode(err) != "stale_claim" {
		t.Fatalf("non-owner renewal: %v", err)
	}
	// An update with renew_for renews in the same transaction and uses the claim revision.
	out, err := update(s, m, id, revision(t, s, m, id), generation, map[string]any{"renew_for": 1200})
	if err != nil || out["claim"].(map[string]any)["revision"] != 3.0 || out["claim"].(map[string]any)["expires_at"] != now(s)+1200 {
		t.Fatalf("update with renewal: %v %v", out, err)
	}
}

func TestWorkOverdueExpiryAndReacquisition(t *testing.T) {
	s, clock, m := workStore(t)
	b := other(m, "claude:synthetic-b")
	id := create(t, s, m, "due")
	unrelated := create(t, s, m, "other")
	generation := mustStart(t, s, m, id, map[string]any{"progress_deadline": now(s) + 100, "lease_seconds": 900})
	*clock = clock.Add(150 * time.Second)
	item := get(t, s, m, id)
	if item["progress_overdue"] != true || item["progress_unverified"] != true || item["lease_valid"] != true {
		t.Fatalf("overdue observation: %v", item)
	}
	if list := mustWork(t, s, m, "work-list", map[string]any{"stale": true}); len(list["items"].([]any)) != 1 {
		t.Fatalf("stale filter: %v", list)
	}
	// A mutation of the target records the due transition first, once.
	rev := revision(t, s, m, id)
	if _, err := work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": rev, "proposed_assignee": "x"}); wcode(err) != "revision_conflict" {
		t.Fatalf("propose after due: %v", err)
	}
	if kinds := events(t, s, m, id); kinds[len(kinds)-1] != "progress-overdue" {
		t.Fatalf("events: %v", kinds)
	}
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kinds := events(t, s, m, id); strings.Count(strings.Join(kinds, ","), "progress-overdue") != 1 {
		t.Fatalf("overdue repeated: %v", kinds)
	}
	// A blocked report counts as progress and keeps its blocker; it rearms the obligation.
	if _, err := update(s, m, id, revision(t, s, m, id), generation, map[string]any{"lifecycle": "blocked"}); wcode(err) != "invalid_request" {
		t.Fatalf("blocked without blocker: %v", err)
	}
	if _, err := update(s, m, id, revision(t, s, m, id), generation, map[string]any{"lifecycle": "blocked", "blocker": "synthetic blocker",
		"progress_deadline": now(s) + 60}); err != nil {
		t.Fatal(err)
	}
	if item := get(t, s, m, id); item["lifecycle"] != "blocked" || item["blocker"] != "synthetic blocker" || item["progress_overdue"] != false {
		t.Fatalf("blocked: %v", item)
	}
	// Past both the lease and the new progress deadline, expiry wins: one event, no overdue.
	*clock = clock.Add(2000 * time.Second)
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	kinds := events(t, s, m, id)
	if kinds[len(kinds)-1] != "lease-expired" || strings.Count(strings.Join(kinds, ","), "progress-overdue") != 1 {
		t.Fatalf("expiry: %v", kinds)
	}
	item = get(t, s, m, id)
	if item["lease_expired"] != true || item["lifecycle"] != "blocked" || item["current_claim"] != nil ||
		item["progress_unverified_reasons"].([]any)[0] != "lease_expired" || item["checkpoint"] != "synthetic checkpoint" {
		t.Fatalf("expired item: %v", item)
	}
	// A clock step back and another sweep do not repeat the transition.
	*clock = clock.Add(-1500 * time.Second)
	s.MaintainWork(context.Background())
	*clock = clock.Add(1500 * time.Second)
	s.MaintainWork(context.Background())
	if again := events(t, s, m, id); len(again) != len(kinds) {
		t.Fatalf("transition repeated: %v", again)
	}
	// The late old owner can do nothing with its generation.
	rev = revision(t, s, m, id)
	for op, fields := range map[string]map[string]any{
		"claim-renew":  {"work_id": id, "claim_generation": generation, "if_claim_revision": 1},
		"work-release": {"work_id": id, "if_revision": rev, "claim_generation": generation, "checkpoint": "late"},
		"work-finish":  {"work_id": id, "if_revision": rev, "claim_generation": generation, "outcome": "withdrawn", "reason": "late"},
	} {
		if _, err := work(s, m, op, fields); wcode(err) != "stale_claim" {
			t.Fatalf("late %s: %v", op, err)
		}
	}
	if _, err := update(s, m, id, rev, generation, nil); wcode(err) != "stale_claim" {
		t.Fatalf("late update: %v", err)
	}
	// A new writer starts explicitly with a new generation; expiry assigned nobody.
	next := mustStart(t, s, b, id, nil)
	if next <= generation {
		t.Fatalf("generation reused: %d after %d", next, generation)
	}
	if item := get(t, s, b, id); item["lease_expired"] != false || item["last_writer"] != b.Consumer || item["lifecycle"] != "active" {
		t.Fatalf("reacquired: %v", item)
	}
	if kinds := events(t, s, m, unrelated); len(kinds) != 1 {
		t.Fatalf("unrelated item changed: %v", kinds)
	}
}

func TestWorkFinishRetentionAndReclamation(t *testing.T) {
	s, clock, m := workStore(t)
	id := create(t, s, m, "finish")
	generation := mustStart(t, s, m, id, nil)
	rev := revision(t, s, m, id)
	for _, fields := range []map[string]any{
		{"outcome": "completed"}, {"outcome": "withdrawn"}, {"outcome": "withdrawn", "reason": "  "}, {"outcome": "done"},
	} {
		fields["work_id"], fields["if_revision"], fields["claim_generation"] = id, rev, generation
		if _, err := work(s, m, "work-finish", fields); wcode(err) != "invalid_request" {
			t.Fatalf("finish %v: %v", fields, err)
		}
	}
	finished := mustWork(t, s, m, "work-finish", map[string]any{"work_id": id, "if_revision": rev, "claim_generation": generation,
		"outcome": "completed", "references": []string{"synthetic-pr"}, "key": "finish-1", "deadline": now(s) + 600})
	item := get(t, s, m, id)
	if item["lifecycle"] != "finished" || item["outcome"] != "completed" || item["expires_at"] != now(s)+workRetention ||
		item["current_claim"] != nil || item["progress_unverified"] != false {
		t.Fatalf("finished: %v", item)
	}
	if _, err := work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": item["revision"], "proposed_assignee": "x"}); wcode(err) != "invalid_transition" {
		t.Fatalf("finished is terminal: %v", err)
	}
	if _, err := start(s, m, id, int64(item["revision"].(float64)), nil); wcode(err) != "invalid_transition" {
		t.Fatalf("restart finished: %v", err)
	}
	// A consumer that froze a snapshot keeps it; replay keeps its original result.
	reader := other(m, "reader")
	page, err := s.MemorySync(context.Background(), reader, MemorySyncRequest{})
	if err != nil || page["total"] != int64(1) {
		t.Fatalf("snapshot before expiry: %v %v", page, err)
	}
	headBefore, _ := head(t, s, m.Repository)
	*clock = clock.Add((workRetention + 1) * time.Second)
	if _, err := work(s, m, "work-get", map[string]any{"work_id": id}); wcode(err) != "work_not_found" {
		t.Fatalf("expired item visible: %v", err)
	}
	if list := mustWork(t, s, m, "work-list", nil); len(list["items"].([]any)) != 0 {
		t.Fatalf("expired item listed: %v", list)
	}
	status, _ := s.MemoryStatus(context.Background(), m, "")
	// The refused restart above already reclaimed the inactive bundle: a start may do
	// that independent cleanup before its own refusal (docs/WORK-ITEMS-COMMANDS.md).
	if wm := status["work_maintenance"].(map[string]any); wm["expired_items"] != int64(1) || wm["inactive_bundles"] != int64(0) {
		t.Fatalf("maintenance status: %v", wm)
	}
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	// One sweep reclaims the inactive bundle and then the item.
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles`) != 0 || count(t, s, `SELECT COUNT(*) FROM work_items`) != 0 ||
		count(t, s, `SELECT COUNT(*) FROM work_events`) != 0 || count(t, s, `SELECT COUNT(*) FROM work_scope_revisions`) != 0 ||
		count(t, s, `SELECT COUNT(*) FROM memory_entries WHERE type='work-event'`) != 0 {
		t.Fatal("finished item not reclaimed")
	}
	headAfter, floor := head(t, s, m.Repository)
	if headAfter != headBefore || floor != headBefore || count(t, s, `SELECT work_counter FROM memory_stores`) != 1 {
		t.Fatalf("head %d->%d floor %d", headBefore, headAfter, floor)
	}
	if count(t, s, `SELECT COUNT(*) FROM memory_snapshot_items`) == 0 {
		t.Fatal("frozen snapshot payload removed")
	}
	if status, _ := s.MemoryStatus(context.Background(), m, ""); status["work_maintenance"].(map[string]any)["last_successful_sweep"] == nil {
		t.Fatal("sweep not reported")
	}
	_ = finished
}

func TestWorkReleaseKeepsCheckpointAndRefusesWithoutEffect(t *testing.T) {
	s, _, m := workStore(t)
	id := create(t, s, m, "release")
	generation := mustStart(t, s, m, id, map[string]any{"resources": [][]string{{"path", "a"}}})
	rev := revision(t, s, m, id)
	headBefore, _ := head(t, s, m.Repository)
	if _, err := work(s, m, "work-release", map[string]any{"work_id": id, "if_revision": rev, "claim_generation": generation + 1, "checkpoint": "x"}); wcode(err) != "stale_claim" {
		t.Fatalf("wrong generation: %v", err)
	}
	if after, _ := head(t, s, m.Repository); after != headBefore || revision(t, s, m, id) != rev {
		t.Fatal("refused release changed state")
	}
	mustWork(t, s, m, "work-release", map[string]any{"work_id": id, "if_revision": rev, "claim_generation": generation, "checkpoint": "final checkpoint"})
	item := get(t, s, m, id)
	if item["lifecycle"] != "open" || item["checkpoint"] != "final checkpoint" || item["current_claim"] != nil || item["progress_deadline"] != nil ||
		item["last_writer"] != m.Consumer || item["last_generation"] != float64(generation) {
		t.Fatalf("released: %v", item)
	}
	// The inactive bundle is reclaimed under ordinary admission before the next start.
	mustStart(t, s, other(m, "claude:synthetic-b"), id, map[string]any{"resources": [][]string{{"path", "a"}}})
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles`) != 1 {
		t.Fatal("inactive bundle kept")
	}
}

func TestWorkStreamSnapshotAndNoteIsolation(t *testing.T) {
	s, _, m := workStore(t)
	note(t, s, m, MemoryRecordRequest{Type: "decision", Body: "synthetic decision"})
	second := create(t, s, m, "second")
	first := create(t, s, m, "first")
	_ = first
	entries := drain(t, s, other(m, "reader"))
	if len(entries) != 3 || entries[0].Type != "decision" {
		t.Fatalf("snapshot: %v", entries)
	}
	var views []map[string]any
	page, _ := s.MemorySync(context.Background(), other(m, "fresh"), MemorySyncRequest{})
	for _, raw := range page["entries"].([]json.RawMessage) {
		var v map[string]any
		json.Unmarshal(raw, &v)
		views = append(views, v)
	}
	if views[1]["type"] != "work-item" || views[1]["work_id"] != second || views[2]["work_id"] != first || views[1]["seq"] != 2.0 {
		t.Fatalf("work views: %v", views)
	}
	generation := mustStart(t, s, m, second, nil)
	page, err := s.MemorySync(context.Background(), other(m, "reader"), MemorySyncRequest{})
	if err != nil || page["kind"] != "delta" {
		t.Fatalf("delta: %v %v", page, err)
	}
	var event MemoryEntry
	json.Unmarshal(page["entries"].([]json.RawMessage)[0], &event)
	var payload map[string]any
	json.Unmarshal(event.Payload, &payload)
	if event.Type != workEventType || event.EventKind != "started" || event.WorkID != second || payload["lifecycle"] != "active" ||
		payload["current_claim"].(map[string]any)["generation"] != float64(generation) {
		t.Fatalf("work event: %+v %v", event, payload)
	}
	// Later changes never rewrite an earlier payload.
	update(s, m, second, revision(t, s, m, second), generation, map[string]any{"progress": "later"})
	page, _ = s.MemorySync(context.Background(), other(m, "reader"), MemorySyncRequest{})
	var again MemoryEntry
	json.Unmarshal(page["entries"].([]json.RawMessage)[0], &again)
	if string(again.Payload) != string(event.Payload) {
		t.Fatal("event payload changed")
	}
	if found, _ := s.MemoryRecall(context.Background(), m, "synthetic", 0); len(found["entries"].([]json.RawMessage)) != 1 {
		t.Fatalf("recall includes work: %v", found)
	}
	if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "x", Supersedes: ptr(event.Seq)}); code(err) != "invalid_request" {
		t.Fatalf("note replaced a work event: %v", err)
	}
}

func TestWorkListFiltersBoundsAndReadsDoNotWrite(t *testing.T) {
	s, _, m := workStore(t)
	b := other(m, "claude:synthetic-b")
	ids := []string{}
	for i := 0; i < 5; i++ {
		ids = append(ids, create(t, s, m, fmt.Sprintf("item %d", i)))
	}
	mustWork(t, s, m, "work-propose", map[string]any{"work_id": ids[0], "if_revision": 1, "proposed_assignee": b.Consumer})
	mustStart(t, s, b, ids[1], nil)
	g := mustStart(t, s, m, ids[2], nil)
	update(s, m, ids[2], revision(t, s, m, ids[2]), g, map[string]any{"lifecycle": "blocked", "blocker": "x"})
	headBefore, _ := head(t, s, m.Repository)
	pagesBefore := count(t, s, `PRAGMA page_count`)
	check := func(filter map[string]any, want ...string) {
		t.Helper()
		out := mustWork(t, s, m, "work-list", filter)
		var got []string
		for _, item := range out["items"].([]any) {
			got = append(got, item.(map[string]any)["work_id"].(string))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("filter %v: %v, want %v", filter, got, want)
		}
	}
	check(nil, ids...)
	check(map[string]any{"owner": b.Consumer}, ids[1])
	check(map[string]any{"owner": nil}, ids[0], ids[3], ids[4])
	check(map[string]any{"proposed_assignee": b.Consumer}, ids[0])
	check(map[string]any{"lifecycle": "active"}, ids[1])
	check(map[string]any{"blocked": true}, ids[2])
	check(map[string]any{"lifecycle": "open", "proposed_assignee": nil}, ids[3], ids[4])
	check(map[string]any{"stale": false, "lifecycle": "blocked"}, ids[2])
	out := mustWork(t, s, m, "work-list", map[string]any{"limit": 2})
	if len(out["items"].([]any)) != 2 || out["truncated"] != true {
		t.Fatalf("limit: %v", out)
	}
	if headAfter, _ := head(t, s, m.Repository); headAfter != headBefore || count(t, s, `PRAGMA page_count`) != pagesBefore ||
		count(t, s, `SELECT COUNT(*) FROM memory_cursors`) != 0 {
		t.Fatal("a read wrote")
	}
	// Reads work while writes are blocked.
	s.storage.mu.Lock()
	s.storage.blocked = "synthetic block"
	s.storage.mu.Unlock()
	if _, err := work(s, m, "work-get", map[string]any{"work_id": ids[0]}); err != nil {
		t.Fatalf("read while blocked: %v", err)
	}
	if _, err := work(s, m, "work-create", map[string]any{"title": "t", "criteria": "c", "non_goals": "n", "key": key(), "deadline": now(s) + 60}); code(err) != "storage_blocked" {
		t.Fatalf("write while blocked: %v", err)
	}
	if status, err := s.MemoryStatus(context.Background(), m, ""); err != nil || status["work_maintenance"].(map[string]any)["pending_due"] != int64(0) {
		t.Fatalf("status while blocked: %v", err)
	}
}

func TestWorkListByteBound(t *testing.T) {
	s, _, m := workStore(t)
	title := strings.Repeat("t", 256)
	for i := 0; i < 100; i++ {
		id := create(t, s, m, title)
		if i < maxBundles {
			mustStart(t, s, other(m, fmt.Sprint("writer-", i)), id, map[string]any{"checkpoint": strings.Repeat("c", 1024)})
		}
	}
	out := mustWork(t, s, m, "work-list", nil)
	data, _ := json.Marshal(out)
	if out["truncated"] != true || len(data) > maxWorkList || len(out["items"].([]any)) >= 100 {
		t.Fatalf("list bound: %d bytes, %d items, truncated %v", len(data), len(out["items"].([]any)), out["truncated"])
	}
}

func TestWorkValidation(t *testing.T) {
	s, _, m := workStore(t)
	id := create(t, s, m, "valid")
	raw := func(op string, fields map[string]string) error {
		r := map[string]json.RawMessage{}
		for k, v := range fields {
			r[k] = json.RawMessage(v)
		}
		_, err := s.Work(context.Background(), m, op, r)
		return err
	}
	q := `"` + id + `"`
	for _, c := range []struct {
		op     string
		fields map[string]string
	}{
		{"work-get", map[string]string{"work_id": q, "unknown": "1"}},
		{"work-get", map[string]string{"work_id": `"not-an-id"`}},
		{"work-get", map[string]string{"work_id": q, "revision": "true"}},
		{"work-get", map[string]string{"work_id": q, "revision": "1.5"}},
		{"work-get", map[string]string{"work_id": q, "revision": `"1"`}},
		{"work-get", map[string]string{"work_id": q, "revision": "null"}},
		{"work-get", map[string]string{"work_id": q, "key": `"k"`}},
		{"work-list", map[string]string{"limit": "0"}},
		{"work-list", map[string]string{"limit": "101"}},
		{"work-list", map[string]string{"stale": "1"}},
		{"work-list", map[string]string{"lifecycle": `"done"`}},
		{"work-create", map[string]string{"title": `"t"`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"k"`}},
		{"work-create", map[string]string{"title": `" "`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"k"`, "deadline": "1800000060"}},
		{"work-create", map[string]string{"title": `"` + strings.Repeat("t", 257) + `"`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"k"`, "deadline": "1800000060"}},
		{"work-create", map[string]string{"title": `"t"`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"` + strings.Repeat("k", 129) + `"`, "deadline": "1800000060"}},
		{"work-create", map[string]string{"title": `"t"`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"k"`, "deadline": "1800000060",
			"references": `["1","2","3","4","5","6","7","8","9"]`}},
		{"work-create", map[string]string{"title": `"t"`, "criteria": `"c"`, "non_goals": `"n"`, "key": `"k"`, "deadline": "1800000060",
			"proposed_assignee": `"` + strings.Repeat("a", 129) + `"`}},
		{"work-start", map[string]string{"work_id": q, "if_revision": "1", "checkpoint": `"c"`, "next_artifact": `"a"`,
			"progress_deadline": "1800090000", "key": `"k"`, "deadline": "1800000060"}},
		{"work-start", map[string]string{"work_id": q, "if_revision": "1", "checkpoint": `"c"`, "next_artifact": `"a"`,
			"progress_deadline": "1800000600", "key": `"k"`, "deadline": "1800000060", "lease_seconds": "59"}},
		{"work-start", map[string]string{"work_id": q, "if_revision": "1", "checkpoint": `"c"`, "next_artifact": `"a"`,
			"progress_deadline": "1800000600", "key": `"k"`, "deadline": "1800000060", "resources": `[["writer","x"]]`}},
		{"work-update", map[string]string{"work_id": q, "if_revision": "1", "claim_generation": "1", "progress": `"p"`, "checkpoint": `"c"`,
			"next_artifact": `"a"`, "progress_deadline": "1800000600", "lifecycle": `"finished"`}},
		{"work-edit", map[string]string{"work_id": q, "if_revision": "1"}},
		{"work-propose", map[string]string{"work_id": q, "if_revision": "1"}},
		{"work-propose", map[string]string{"work_id": q, "if_revision": "0", "proposed_assignee": "null"}},
	} {
		if err := raw(c.op, c.fields); wcode(err) != "invalid_request" {
			t.Errorf("%s %v: %v", c.op, c.fields, err)
		}
	}
	err := raw("work-update", map[string]string{"work_id": q})
	if err == nil || !strings.Contains(err.Error(), "claim_generation, progress, checkpoint, next_artifact, progress_deadline") {
		t.Errorf("missing fields not named: %v", err)
	}
	if _, err := work(s, m, "work-get", map[string]any{"work_id": fmt.Sprintf("%032x", 99)}); wcode(err) != "work_not_found" {
		t.Errorf("unknown target: %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM work_items`); n != 1 {
		t.Errorf("refusals wrote: %d", n)
	}
}

func TestWorkMalformedOrUnknownRequestDoesNotReconcile(t *testing.T) {
	s, clock, m := workStore(t)
	id := create(t, s, m, "due")
	generation := mustStart(t, s, m, id, nil)
	fields := map[string]any{"work_id": id, "if_revision": 2, "claim_generation": generation, "progress": "p", "checkpoint": "c",
		"next_artifact": "a", "progress_deadline": now(s) + 60, "key": "update-1", "deadline": now(s) + 80000}
	first := mustWork(t, s, m, "work-update", fields)
	*clock = clock.Add(100 * time.Second)
	before := len(events(t, s, m, id))
	work(s, m, "work-propose", map[string]any{"work_id": id})
	work(s, m, "work-propose", map[string]any{"work_id": fmt.Sprintf("%032x", 77), "if_revision": 1, "proposed_assignee": nil})
	// A replay returns its original result and reconciles nothing.
	if again := mustWork(t, s, m, "work-update", fields); again["duplicate"] != true || again["seq"] != first["seq"] {
		t.Fatalf("replay: %v", again)
	}
	if after := len(events(t, s, m, id)); after != before {
		t.Fatalf("refused or replayed requests reconciled: %d -> %d", before, after)
	}
	work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": 3, "proposed_assignee": nil})
	if after := len(events(t, s, m, id)); after != before+1 {
		t.Fatalf("a valid request did not reconcile its target: %d -> %d", before, after)
	}
}

func TestWorkCountLimitsAndDaemonBundleCap(t *testing.T) {
	s, _, m := workStore(t)
	s.memory.limits.workItems = 2
	create(t, s, m, "one")
	create(t, s, m, "two")
	headBefore, _ := head(t, s, m.Repository)
	if _, err := work(s, m, "work-create", map[string]any{"title": "t", "criteria": "c", "non_goals": "n", "key": key(), "deadline": now(s) + 60}); code(err) != "capacity" {
		t.Fatalf("item limit: %v", err)
	}
	if after, _ := head(t, s, m.Repository); after != headBefore || count(t, s, `SELECT work_counter FROM memory_stores`) != 2 {
		t.Fatal("refused create left state")
	}
	s.memory.limits.workItems = 128
	// 16 bundles per store; 32 across the daemon.
	repos := []MemoryCaller{m, {Repository: "/synthetic/two/.git", Family: "codex", Name: "codex-two", Consumer: "c2"},
		{Repository: "/synthetic/three/.git", Family: "codex", Name: "codex-three", Consumer: "c3"}}
	for r, caller := range repos[:2] {
		for i := 0; i < maxBundles; i++ {
			id := create(t, s, caller, "bundle")
			mustStart(t, s, other(caller, fmt.Sprintf("writer-%d-%d", r, i)), id, nil)
		}
		id := create(t, s, caller, "one too many")
		if _, err := start(s, caller, id, 1, nil); wcode(err) != "claim_capacity" {
			t.Fatalf("store bundle cap: %v", err)
		}
	}
	id := create(t, s, repos[2], "third repository")
	if _, err := start(s, repos[2], id, 1, nil); wcode(err) != "claim_capacity" {
		t.Fatalf("daemon bundle cap: %v", err)
	}
	if pages := mustDebt(t, s); pages != 32*(overdueCreditPages+endCreditPages) {
		t.Fatalf("debt pages: %d", pages)
	}
}

func mustDebt(t *testing.T, s *Store) int64 {
	t.Helper()
	status, err := s.StorageStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return status.WorkDebtPages
}

// The debt of funded work controls is kept free by every write: session, message, launch
// and memory writes refuse first, and a promised control still ends its claim.
func TestWorkDebtPreservedByEveryWriter(t *testing.T) {
	s, _, m := workStore(t)
	repo := testRepo(t)
	r := registration(repo, "codex")
	if _, err := s.Register(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	id := create(t, s, m, "debt")
	generation := mustStart(t, s, m, id, nil)
	if debt := mustDebt(t, s); debt != overdueCreditPages+endCreditPages {
		t.Fatalf("debt: %d", debt)
	}
	pages := count(t, s, `PRAGMA page_count`)
	// Leave exactly no ordinary room once the debt is kept free.
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance + mustDebt(t, s) - 1
	if _, err := s.Register(context.Background(), registration(repo, "claude")); code(err) != "capacity" {
		t.Fatalf("session write took the debt: %v", err)
	}
	if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "x"}); code(err) != "capacity" {
		t.Fatalf("note took the debt: %v", err)
	}
	if _, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "codex", Directory: repo, CLI: "/synthetic/codex", HostPID: 1}); code(err) != "capacity" {
		t.Fatalf("launch took the debt: %v", err)
	}
	mustWork(t, s, m, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "claim_generation": generation, "checkpoint": "end"})
	if debt := mustDebt(t, s); debt != 0 {
		t.Fatalf("release did not spend its credit: %d", debt)
	}
	if _, err := s.Register(context.Background(), registration(repo, "claude")); err != nil {
		t.Fatalf("freed debt not available: %v", err)
	}
}

// Saturation (docs/WORK-ITEMS-GO-STORAGE.md): with the ordinary band full of notes, every
// funded control of 16 bundles commits, each within its page allowance, then note
// withdrawals, snapshot acknowledgements and bundle cleanup; integrity holds.
func TestWorkSaturationAndControlAllowances(t *testing.T) {
	for name, repository := range map[string]string{"short": "/synthetic/repo/.git", "long": "/" + strings.Repeat("r", 4000) + "/.git"} {
		t.Run(name, func(t *testing.T) { saturate(t, repository) })
	}
}

func saturate(t *testing.T, repository string) {
	s, clock, m := workStore(t)
	m.Repository = repository
	s.memory.limits.logical = 1 << 40
	s.memory.limits.workLogical = 1 << 40
	s.memory.limits.entries = 1 << 20
	reader := other(m, "reader")
	drain(t, s, reader)
	ids := make([]string, maxBundles)
	generations := make([]int64, maxBundles)
	big := strings.Repeat("x", 4000)
	for i := range ids {
		out := mustWork(t, s, m, "work-create", map[string]any{"title": "saturation", "criteria": strings.Repeat("c", 4096),
			"non_goals": strings.Repeat("n", 2048), "key": key(), "deadline": now(s) + 600})
		ids[i] = out["work_id"].(string)
		generations[i] = mustStart(t, s, other(m, fmt.Sprint("writer-", i)), ids[i], map[string]any{"checkpoint": big[:1024],
			"next_artifact": big[:1024], "progress_deadline": now(s) + 60, "lease_seconds": 3600})
	}
	pages := count(t, s, `PRAGMA page_count`)
	s.storage.maxPages = pages + reservePages + commitSlack + mustDebt(t, s) + 64
	var seqs []int64
	for {
		r, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: strings.Repeat("n", 8192)})
		if err != nil {
			if code(err) != "capacity" {
				t.Fatalf("filling: %v", err)
			}
			break
		}
		seqs = append(seqs, r.Seq)
	}
	if len(seqs) == 0 {
		t.Fatal("no ordinary room to fill")
	}
	largest := map[string]int64{}
	measure := func(allowance int64, what string, f func()) {
		t.Helper()
		before := count(t, s, `PRAGMA page_count`)
		f()
		grown := count(t, s, `PRAGMA page_count`) - before
		if grown > allowance {
			t.Fatalf("%s grew %d pages, above %d", what, grown, allowance)
		}
		largest[what] = max(largest[what], grown)
	}
	*clock = clock.Add(120 * time.Second)
	for i, id := range ids {
		measure(overdueCreditPages, "overdue", func() {
			if err := s.reconcile(context.Background(), m.Repository, id, now(s), new(bool)); err != nil {
				t.Fatalf("overdue %d: %v", i, err)
			}
		})
	}
	for i, id := range ids {
		writer := other(m, fmt.Sprint("writer-", i))
		measure(endCreditPages, "finish", func() {
			mustWork(t, s, writer, "work-finish", map[string]any{"work_id": id, "if_revision": revision(t, s, writer, id),
				"claim_generation": generations[i], "outcome": "withdrawn", "reason": big[:1024], "key": key(), "deadline": now(s) + 600})
		})
	}
	if debt := mustDebt(t, s); debt != 0 {
		t.Fatalf("debt left: %d", debt)
	}
	var version string
	s.db.QueryRow(`SELECT sqlite_version()`).Scan(&version)
	t.Logf("SQLite %s: %d notes filled the ordinary band; largest growth: overdue %d pages, finish %d pages", version, len(seqs), largest["overdue"], largest["finish"])
	for _, seq := range seqs[:4] {
		if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: "withdrawn", Revokes: ptr(seq)}); err != nil {
			t.Fatalf("withdrawal: %v", err)
		}
	}
	page, err := s.MemorySync(context.Background(), reader, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MemoryAck(context.Background(), reader, MemoryAckRequest{Through: ptr(page["next_cursor"].(int64))}); err != nil {
		t.Fatalf("acknowledgement: %v", err)
	}
	for range ids {
		if err := s.MaintainWork(context.Background()); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles`) != 0 {
		t.Fatal("bundles not reclaimed")
	}
	var integrity string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
}

func TestWorkBootstrapTwoConsumers(t *testing.T) {
	s, _, m := workStore(t)
	for i := 0; i < 5; i++ {
		note(t, s, m, MemoryRecordRequest{Type: "decision", Body: fmt.Sprint("synthetic decision ", i)})
		id := create(t, s, m, fmt.Sprint("item ", i))
		if i%2 == 0 {
			mustStart(t, s, other(m, fmt.Sprint("writer-", i)), id, nil)
		}
	}
	for _, consumer := range []string{"first", "second"} {
		if entries := drain(t, s, other(m, consumer)); len(entries) != 10 {
			t.Fatalf("%s bootstrap: %d entries", consumer, len(entries))
		}
	}
}

func TestWorkSchemaFormatHeaderAndMigration(t *testing.T) {
	s, root := testStore(t)
	m := MemoryCaller{Repository: "/synthetic/repo/.git", Family: "codex", Name: "codex-repo-01", Consumer: "c"}
	seq := note(t, s, m, MemoryRecordRequest{Type: "decision", Body: "kept across migration"})
	// Schema 4: the memory runtime without work tables or counters.
	if _, err := s.db.Exec(`DROP TABLE work_items; DROP TABLE work_scope_revisions; DROP TABLE claim_bundles; DROP TABLE claim_resources;
		DROP TABLE work_events; DROP TABLE work_replays; ALTER TABLE memory_stores DROP COLUMN work_counter;
		ALTER TABLE memory_stores DROP COLUMN claim_counter; PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := head(t, s, m.Repository); h != seq || len(live(t, s, m, "kept")) != 1 {
		t.Fatal("migration lost memory")
	}
	id := create(t, s, m, "after migration")
	if id != fmt.Sprintf("%032x", 1) {
		t.Fatalf("work id: %s", id)
	}
	generation := mustStart(t, s, m, id, nil)
	s.db.Close()
	// Restart preserves leases and generations and invents no progress.
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if item := get(t, s, m, id); item["current_claim"] == nil || item["last_generation"] != float64(generation) {
		t.Fatalf("lease after restart: %v", item)
	}
	if _, err := update(s, m, id, revision(t, s, m, id), generation, nil); err != nil {
		t.Fatalf("owner after restart: %v", err)
	}
	s.db.Close()
	// A database whose header schema format is not 4 is refused.
	path := filepath.Join(root, "state.sqlite3")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var format [4]byte
	binary.BigEndian.PutUint32(format[:], 1)
	f.WriteAt(format[:], 44)
	f.Close()
	if s, err := openStore(root); err == nil || !strings.Contains(err.Error(), "schema format") {
		if s != nil {
			s.db.Close()
		}
		t.Fatalf("format 1 accepted: %v", err)
	}
}

func TestWorkEpochMismatchIsCorruption(t *testing.T) {
	s, _, m := workStore(t)
	id := create(t, s, m, "epoch")
	generation := mustStart(t, s, m, id, nil)
	s.db.Exec(`UPDATE claim_bundles SET progress_epoch=progress_epoch+5`)
	if _, err := update(s, m, id, revision(t, s, m, id), generation, nil); wcode(err) != "incompatible_store" {
		t.Fatalf("epoch mismatch: %v", err)
	}
}

func TestWorkConcurrentStartsHaveOneWriter(t *testing.T) {
	s, _, m := workStore(t)
	id := create(t, s, m, "race")
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = start(s, other(m, fmt.Sprint("writer-", i)), id, 1, nil)
		}(i)
	}
	wg.Wait()
	won := 0
	for _, err := range results {
		switch wcode(err) {
		case "":
			won++
		case "claim_conflict", "revision_conflict":
		default:
			t.Fatalf("start: %v", err)
		}
	}
	if won != 1 || count(t, s, `SELECT COUNT(*) FROM claim_bundles`) != 1 {
		t.Fatalf("%d writers", won)
	}
}

func TestWorkChangeHook(t *testing.T) {
	s, clock, m := workStore(t)
	var mu sync.Mutex
	calls := []string{}
	s.OnMemoryChange(func(repo string) {
		mu.Lock()
		calls = append(calls, repo)
		mu.Unlock()
		// The hook runs outside every lock: it can read and write.
		s.MemoryStatus(context.Background(), m, "")
	})
	id := create(t, s, m, "hook")
	mustWork(t, s, m, "work-list", nil)
	work(s, m, "work-propose", map[string]any{"work_id": id, "if_revision": 9, "proposed_assignee": nil})
	generation := mustStart(t, s, m, id, map[string]any{"progress_deadline": now(s) + 60})
	mustWork(t, s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1})
	if len(calls) != 2 {
		t.Fatalf("hook calls after create, refused, read, start and renewal: %v", calls)
	}
	*clock = clock.Add(120 * time.Second)
	s.MaintainWork(context.Background())
	if len(calls) != 3 || calls[2] != m.Repository {
		t.Fatalf("due transition hook: %v", calls)
	}
}

func TestWorkAPIConsumerAndRefusalDetails(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	repo := testRepo(t)
	a, b := registration(repo, "codex"), registration(repo, "claude")
	for _, r := range []Registration{a, b} {
		if _, err := d.store.Register(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	callerA, callerB := Key{a.Family, a.ID}, Key{b.Family, b.ID}
	deadline := float64(time.Now().Unix() + 600)
	status, created := post(t, address, secret, "/v1/work/work-create", map[string]any{"caller": callerA, "title": "api", "criteria": "c",
		"non_goals": "n", "key": "k", "deadline": deadline})
	if status != 200 {
		t.Fatalf("create: %d %v", status, created)
	}
	id := created["result"].(map[string]any)["work_id"].(string)
	startBody := func(caller Key, revision int) map[string]any {
		return map[string]any{"caller": caller, "work_id": id, "if_revision": revision, "checkpoint": "c", "next_artifact": "a",
			"progress_deadline": deadline, "key": "s", "deadline": deadline}
	}
	if status, result := post(t, address, secret, "/v1/work/work-start", startBody(callerA, 1)); status != 200 {
		t.Fatalf("start: %d %v", status, result)
	}
	status, refused := post(t, address, secret, "/v1/work/work-start", startBody(callerB, 2))
	details, _ := refused["details"].(map[string]any)
	if status != 409 || refused["code"] != "claim_conflict" || details["consumer"] != a.Family+":"+a.ID {
		t.Fatalf("conflict: %d %v", status, refused)
	}
	if status, result := post(t, address, secret, "/v1/work/work-get", map[string]any{"caller": callerB, "work_id": id, "consumer": "stable"}); status != 200 ||
		result["result"].(map[string]any)["current_claim"].(map[string]any)["consumer"] != a.Family+":"+a.ID {
		t.Fatalf("get: %d %v", status, result)
	}
	if status, _ := post(t, address, secret, "/v1/work/work-get", map[string]any{"caller": callerB, "work_id": id, "consumer": nil}); status != 400 {
		t.Fatalf("null consumer: %d", status)
	}
	if response := request(t, d, "/v1/work/work-unknown", `{}`, secret); response.StatusCode != 404 {
		t.Fatalf("unknown operation: %d", response.StatusCode)
	}
}

// Review regressions for PR 190.

// An admitted item always leaves room for its funded due events and later observations.
func TestWorkAdmittedViewKeepsRoomForDueEvents(t *testing.T) {
	s, clock, m := workStore(t)
	out := mustWork(t, s, m, "work-create", map[string]any{"title": "size", "criteria": strings.Repeat("c", 4096),
		"non_goals": strings.Repeat("n", 2048), "key": key(), "deadline": now(s) + 600})
	id := out["work_id"].(string)
	resources := [][]string{}
	for i := 0; i < 8; i++ {
		resources = append(resources, []string{"exact", strings.Repeat(string(rune('a'+i)), 512)})
	}
	generation := mustStart(t, s, m, id, map[string]any{"resources": resources, "checkpoint": strings.Repeat("k", 1024),
		"next_artifact": strings.Repeat("a", 1024), "progress_deadline": now(s) + 60, "lease_seconds": 120})
	// The largest progress that is still admitted, escaped to its longest JSON spelling.
	lo, hi := 0, 2048
	for lo < hi {
		n := (lo + hi + 1) / 2
		_, err := update(s, m, id, revision(t, s, m, id), generation, map[string]any{"progress": strings.Repeat(`"`, n),
			"checkpoint": strings.Repeat("k", 1024), "next_artifact": strings.Repeat("a", 1024), "progress_deadline": now(s) + 60})
		switch wcode(err) {
		case "":
			lo = n
		case "record_too_large":
			hi = n - 1
		default:
			t.Fatal(err)
		}
	}
	if lo == 0 {
		t.Fatal("no progress admitted")
	}
	*clock = clock.Add(61 * time.Second)
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatalf("funded overdue event refused: %v", err)
	}
	*clock = clock.Add(120 * time.Second)
	if err := s.MaintainWork(context.Background()); err != nil {
		t.Fatalf("funded expiry event refused: %v", err)
	}
	if kinds := events(t, s, m, id); kinds[len(kinds)-2] != "progress-overdue" || kinds[len(kinds)-1] != "lease-expired" {
		t.Fatalf("events: %v", kinds)
	}
	if debt := mustDebt(t, s); debt != 0 {
		t.Fatalf("debt left: %d", debt)
	}
	get(t, s, m, id)
	drain(t, s, other(m, "reader"))
}

// A request that waits for the serialized operation is judged at its processing time.
func TestWorkQueuedRenewalCannotReviveExpiredLease(t *testing.T) {
	s, clock, m := workStore(t)
	id := create(t, s, m, "queued renewal")
	generation := mustStart(t, s, m, id, map[string]any{"lease_seconds": 60, "progress_deadline": now(s) + 3600})
	var current atomic.Int64
	current.Store(clock.Unix())
	sampled := make(chan struct{}, 1)
	s.now = func() time.Time {
		select {
		case sampled <- struct{}{}:
		default:
		}
		return time.Unix(current.Load(), 0)
	}
	s.memory.op.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := work(s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": generation, "if_claim_revision": 1})
		done <- err
	}()
	// Let the request reach the lock, then let the lease lapse while it waits.
	time.Sleep(50 * time.Millisecond)
	current.Add(120)
	s.memory.op.Unlock()
	if err := <-done; wcode(err) != "stale_claim" {
		t.Fatalf("queued renewal: %v", err)
	}
	if item := get(t, s, m, id); item["current_claim"] != nil || item["lease_expired"] != true {
		t.Fatalf("expired claim revived: %v", item)
	}
}

// Schema objects that this runtime does not create are refused before any write.
func TestWorkCatalogRefusesExtraObjects(t *testing.T) {
	for name, statement := range map[string]string{
		"index":   `CREATE INDEX unexpected_credit_index ON claim_bundles(active,overdue_credit,end_credit)`,
		"trigger": `CREATE TRIGGER unexpected_claim_trigger AFTER UPDATE OF active ON claim_bundles BEGIN SELECT RAISE(ABORT,'x'); END`,
		"view":    `CREATE VIEW unexpected_view AS SELECT 1`,
	} {
		t.Run(name, func(t *testing.T) {
			s, root := testStore(t)
			if _, err := s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			s.db.Close()
			if reopened, err := openStore(root); err == nil || !strings.Contains(err.Error(), "schema objects") {
				if reopened != nil {
					reopened.db.Close()
				}
				t.Fatalf("extra %s accepted: %v", name, err)
			}
		})
	}
	// An extra object in an older schema is refused before its migration.
	s, root := testStore(t)
	if _, err := s.db.Exec(`DROP TABLE work_items; DROP TABLE work_scope_revisions; DROP TABLE claim_bundles; DROP TABLE claim_resources;
		DROP TABLE work_events; DROP TABLE work_replays; ALTER TABLE memory_stores DROP COLUMN work_counter;
		ALTER TABLE memory_stores DROP COLUMN claim_counter; CREATE INDEX unexpected ON memory_stores(head); PRAGMA user_version=4`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	if reopened, err := openStore(root); err == nil {
		reopened.db.Close()
		t.Fatal("extra object in schema 4 accepted")
	}
}

// Equivalent JSON spellings of one request replay instead of conflicting.
func TestWorkReplayUsesDecodedContent(t *testing.T) {
	s, _, m := workStore(t)
	fields := map[string]json.RawMessage{"title": json.RawMessage(`"t"`), "criteria": json.RawMessage(`"c"`), "non_goals": json.RawMessage(`"n"`),
		"key": json.RawMessage(`"canonical-key"`), "deadline": json.RawMessage(`1800000600`), "proposed_assignee": json.RawMessage(`null`)}
	first, err := s.Work(context.Background(), m, "work-create", fields)
	if err != nil {
		t.Fatal(err)
	}
	fields["title"], fields["deadline"] = json.RawMessage(`"t"`), json.RawMessage(`1800000600.0`)
	again, err := s.Work(context.Background(), m, "work-create", fields)
	if err != nil || again.(map[string]any)["duplicate"] != true || fmt.Sprint(again.(map[string]any)["work_id"]) != first.(map[string]any)["work_id"] {
		t.Fatalf("equivalent retry: %v %v", again, err)
	}
	// An explicit null and an absent field stay distinct.
	delete(fields, "proposed_assignee")
	if _, err := s.Work(context.Background(), m, "work-create", fields); wcode(err) != "idempotency_conflict" {
		t.Fatalf("absent field replayed as null: %v", err)
	}
}
