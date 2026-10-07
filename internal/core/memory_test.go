package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memoryStore is a store with an adjustable clock and limits, and one synthetic caller.
func memoryStore(t *testing.T) (*Store, *time.Time, MemoryCaller) {
	t.Helper()
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	s.limits()
	return s, &clock, MemoryCaller{Repository: "/synthetic/repo/.git", Family: "codex", Name: "codex-repo-01", Consumer: "consumer-a"}
}

func code(err error) string {
	var r Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func note(t *testing.T, s *Store, m MemoryCaller, r MemoryRecordRequest) int64 {
	t.Helper()
	if r.Type == "" {
		r.Type = "finding"
	}
	if r.Body == "" {
		r.Body = "synthetic entry"
	}
	result, err := s.MemoryRecord(context.Background(), m, r)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return result.Seq
}

func ptr[T any](v T) *T { return &v }

// drain pages a snapshot fully and acknowledges it.
func drain(t *testing.T, s *Store, m MemoryCaller) []MemoryEntry {
	t.Helper()
	ctx := context.Background()
	page, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if page["kind"] != "snapshot" {
		t.Fatalf("expected a snapshot: %v", page)
	}
	id := page["snapshot_id"].(string)
	var entries []MemoryEntry
	for {
		for _, raw := range page["entries"].([]json.RawMessage) {
			var e MemoryEntry
			json.Unmarshal(raw, &e)
			entries = append(entries, e)
		}
		if page["more"] != true {
			break
		}
		if page, err = s.MemorySync(ctx, m, MemorySyncRequest{SnapshotID: id, PageToken: page["page_token"].(int64)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: id}); err != nil {
		t.Fatal(err)
	}
	return entries
}

func live(t *testing.T, s *Store, m MemoryCaller, query string) []int64 {
	t.Helper()
	r, err := s.MemoryRecall(context.Background(), m, query, 0)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, raw := range r["entries"].([]json.RawMessage) {
		var e MemoryEntry
		json.Unmarshal(raw, &e)
		seqs = append(seqs, e.Seq)
	}
	return seqs
}

func head(t *testing.T, s *Store, repo string) (int64, int64) {
	var h, f int64
	s.db.QueryRow(`SELECT head,floor FROM memory_stores WHERE repository=?`, repo).Scan(&h, &f)
	return h, f
}

func TestMemoryStorePerRepositoryAndWorktree(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo, other := testRepo(t), testRepo(t)
	for _, args := range [][]string{{"-C", repo, "-c", "user.name=Synthetic", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "fixture"}, {"-C", repo, "worktree", "add", "--detach", repo + "-tree"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	register := func(id, repository, directory string) MemoryCaller {
		if _, err := s.Register(ctx, Registration{Family: "codex", ID: id, Repository: repository, Directory: directory, TTLSeconds: 600}); err != nil {
			t.Fatal(err)
		}
		m, err := s.ResolveMemoryCaller(ctx, Key{"codex", id}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b, c := register("synthetic-a", repo, repo), register("synthetic-b", repo+"-tree", repo+"-tree"), register("synthetic-c", other, other)
	if a.Repository != b.Repository || a.Repository == c.Repository || a.Consumer != a.Name {
		t.Fatalf("stores: %+v %+v %+v", a, b, c)
	}
	note(t, s, a, MemoryRecordRequest{Body: "shared"})
	if got := drain(t, s, b); len(got) != 1 || got[0].WriterName != a.Name || got[0].WriterFamily != "codex" {
		t.Fatalf("worktree store: %+v", got)
	}
	if got := drain(t, s, c); len(got) != 0 {
		t.Fatalf("repositories collide: %+v", got)
	}
	if _, err := s.Register(ctx, Registration{Family: "claude", ID: "synthetic-plain", Directory: t.TempDir(), TTLSeconds: 600}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveMemoryCaller(ctx, Key{"claude", "synthetic-plain"}, nil); code(err) != "repo_unresolved" {
		t.Fatalf("plain session: %v", err)
	}
	for _, consumer := range []string{"", "   ", strings.Repeat("c", 129)} {
		if _, err := s.ResolveMemoryCaller(ctx, Key{"codex", "synthetic-a"}, &consumer); code(err) != "invalid_request" {
			t.Fatalf("consumer %q: %v", consumer, err)
		}
	}
	if m, err := s.ResolveMemoryCaller(ctx, Key{"codex", "synthetic-a"}, ptr(" stable ")); err != nil || m.Consumer != "stable" {
		t.Fatalf("named consumer: %+v %v", m, err)
	}
}

func TestMemoryWrites(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	now := float64(clock.Unix())
	// The head is durable and never moves backwards when entries are reclaimed.
	first := note(t, s, m, MemoryRecordRequest{Expires: ptr(now + 10)})
	*clock = clock.Add(20 * time.Second)
	if err := s.expireMemory(ctx, m.Repository, false); err != nil {
		t.Fatal(err)
	}
	if h, f := head(t, s, m.Repository); h != first || f != first {
		t.Fatalf("head %d floor %d", h, f)
	}
	if next := note(t, s, m, MemoryRecordRequest{}); next != first+1 {
		t.Fatalf("next seq %d", next)
	}
	// Competing revisions are retained and the conflict is reported.
	target := note(t, s, m, MemoryRecordRequest{Type: "decision", Body: "original"})
	winner, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "decision", Body: "first", Supersedes: &target})
	if err != nil || winner.ConflictsWith != nil {
		t.Fatalf("first replacement: %+v %v", winner, err)
	}
	rival, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "decision", Body: "second", Revokes: &target})
	if err != nil || rival.ConflictsWith == nil || *rival.ConflictsWith != winner.Seq {
		t.Fatalf("rival: %+v %v", rival, err)
	}
	if a, b := live(t, s, m, "first"), live(t, s, m, "second"); len(a) != 1 || len(b) != 1 {
		t.Fatalf("both revisions stay live: %v %v", a, b)
	}
	for _, seq := range live(t, s, m, "original") {
		t.Fatalf("replaced entry still live: %d", seq)
	}
	// A reclaimed replacement does not revive what it replaced.
	base := note(t, s, m, MemoryRecordRequest{Body: "base entry"})
	note(t, s, m, MemoryRecordRequest{Body: "short replacement", Supersedes: &base, Expires: ptr(float64(clock.Unix()) + 5)})
	*clock = clock.Add(10 * time.Second)
	s.expireMemory(ctx, m.Repository, false)
	if got := live(t, s, m, "base entry"); len(got) != 0 {
		t.Fatalf("resurrected: %v", got)
	}
	// A failed write rolls back whole.
	before, _ := head(t, s, m.Repository)
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "x", Supersedes: ptr(int64(99999))}); code(err) != "no_such_entry" {
		t.Fatalf("missing target: %v", err)
	}
	if after, _ := head(t, s, m.Repository); after != before {
		t.Fatal("failed write moved the head")
	}
	for _, c := range []struct {
		r    MemoryRecordRequest
		want string
	}{
		{MemoryRecordRequest{Type: "unknown", Body: "x"}, "invalid_request"},
		{MemoryRecordRequest{Type: "finding", Body: "x", Scope: "task"}, "invalid_request"},
		{MemoryRecordRequest{Type: "finding", Body: "x", Scope: "world"}, "invalid_request"},
		{MemoryRecordRequest{Type: "finding", Body: "  "}, "invalid_request"},
		{MemoryRecordRequest{Type: "finding", Body: strings.Repeat("é", 4097)}, "entry_too_large"},
		{MemoryRecordRequest{Type: "finding", Body: "x", Supersedes: &base, Revokes: &base}, "invalid_request"},
	} {
		if _, err := s.MemoryRecord(ctx, m, c.r); code(err) != c.want {
			t.Fatalf("%+v: %v", c.r, err)
		}
	}
	if seq := note(t, s, m, MemoryRecordRequest{Scope: "task", ScopeTarget: ptr("task-1")}); seq == 0 {
		t.Fatal("task scope")
	}
}

func TestMemoryIdempotency(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	deadline := float64(clock.Unix()) + 600
	r := MemoryRecordRequest{Type: "finding", Body: "keyed", Key: ptr("k1"), Deadline: &deadline, Author: ptr("synthetic author")}
	first, err := s.MemoryRecord(ctx, m, r)
	if err != nil || first.Duplicate || *first.Deadline != deadline || *first.IdempotencyHorizon != 86400 {
		t.Fatalf("first: %+v %v", first, err)
	}
	again, err := s.MemoryRecord(ctx, m, r)
	if err != nil || !again.Duplicate || again.Seq != first.Seq || again.IdempotencyHorizon != nil {
		t.Fatalf("duplicate: %+v %v", again, err)
	}
	changed := r
	changed.Body = "other"
	if _, err := s.MemoryRecord(ctx, m, changed); code(err) != "idempotency_conflict" {
		t.Fatalf("changed content: %v", err)
	}
	changed = r
	changed.Author = ptr("another author")
	if _, err := s.MemoryRecord(ctx, m, changed); code(err) != "idempotency_conflict" {
		t.Fatalf("author is content: %v", err)
	}
	changed = r
	changed.Deadline = ptr(deadline + 1)
	if _, err := s.MemoryRecord(ctx, m, changed); code(err) != "idempotency_conflict" {
		t.Fatalf("changed deadline: %v", err)
	}
	other := m
	other.Consumer = "consumer-b"
	if result, err := s.MemoryRecord(ctx, other, r); err != nil || result.Duplicate {
		t.Fatalf("key is scoped to the consumer: %+v %v", result, err)
	}
	for _, d := range []*float64{nil, ptr(math.NaN()), ptr(math.Inf(1)), ptr(float64(clock.Unix()) + 86401)} {
		bad := r
		bad.Key, bad.Deadline = ptr("k2"), d
		if _, err := s.MemoryRecord(ctx, m, bad); code(err) != "invalid_request" {
			t.Fatalf("deadline %v: %v", d, err)
		}
	}
	// After its deadline a retry is refused rather than appended, and the row is removed.
	*clock = clock.Add(601 * time.Second)
	if _, err := s.MemoryRecord(ctx, m, r); code(err) != "retry_deadline_expired" {
		t.Fatalf("late retry: %v", err)
	}
	s.expireMemory(ctx, m.Repository, false)
	var rows int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory_idem`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("idempotency rows kept past their deadline: %d", rows)
	}
	// A row of another scheme (an imported one) is never matched by this scheme.
	s.db.Exec(`INSERT INTO memory_idem(repository,consumer,key,scheme,fingerprint,seq,ts,deadline) VALUES (?,?,?,?,?,?,?,?)`,
		m.Repository, m.Consumer, "imported", "python1", "0", 1, 0, float64(clock.Unix())+100)
	imported := r
	imported.Key, imported.Deadline = ptr("imported"), ptr(float64(clock.Unix())+100)
	if _, err := s.MemoryRecord(ctx, m, imported); code(err) != "idempotency_conflict" {
		t.Fatalf("foreign scheme: %v", err)
	}
	// An in-window key is never evicted to make room.
	s.memory.limits.idem = 2 // the imported row and one more
	d := float64(clock.Unix()) + 600
	for i, want := range []string{"", "idem_capacity"} {
		k := MemoryRecordRequest{Type: "finding", Body: fmt.Sprint("window ", i), Key: ptr(fmt.Sprint("w", i)), Deadline: &d}
		if _, err := s.MemoryRecord(ctx, m, k); code(err) != want {
			t.Fatalf("window %d: %v", i, err)
		}
	}
	if dup, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "window 0", Key: ptr("w0"), Deadline: &d}); err != nil || !dup.Duplicate {
		t.Fatalf("kept key: %+v %v", dup, err)
	}
}

func TestMemoryCapacityAndReserve(t *testing.T) {
	s, _, m := memoryStore(t)
	ctx := context.Background()
	s.memory.limits.entries, s.memory.limits.reservedEntries = 8, 2
	var seqs []int64
	for i := 0; i < 6; i++ {
		seqs = append(seqs, note(t, s, m, MemoryRecordRequest{Body: fmt.Sprint("entry ", i)}))
	}
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "one more"}); code(err) != "capacity" || !strings.Contains(err.Error(), "intact") {
		t.Fatalf("ordinary write at capacity: %v", err)
	}
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "withdrawn", Revokes: &seqs[0]}); err != nil {
		t.Fatalf("withdrawal uses the reserve: %v", err)
	}
	if got := live(t, s, m, "entry"); len(got) != 5 {
		t.Fatalf("records lost: %v", got)
	}
	// Frozen snapshots and registrations are charged.
	s.memory.limits.entries = 5000
	u0, _ := usage(ctx, s.db, m.Repository)
	if _, err := s.MemorySync(ctx, m, MemorySyncRequest{}); err != nil {
		t.Fatal(err)
	}
	u1, _ := usage(ctx, s.db, m.Repository)
	if u1.Logical <= u0.Logical || u1.Snapshots != 1 || u1.Consumers != 1 {
		t.Fatalf("snapshot and cursor not charged: %+v %+v", u0, u1)
	}
	s.memory.limits.logical = u1.Logical + s.memory.limits.reservedBytes + 100
	other := m
	other.Consumer = "consumer-b"
	if _, err := s.MemorySync(ctx, other, MemorySyncRequest{}); code(err) != "capacity" {
		t.Fatalf("snapshot at the logical limit: %v", err)
	}
}

func TestMemoryConsumersAndRetirement(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	s.memory.limits.consumers, s.memory.limits.retired = 2, 1
	a, b, c, d := m, m, m, m
	a.Consumer, b.Consumer, c.Consumer, d.Consumer = "r1", "r2", "r3", "r4"
	drain(t, s, a)
	*clock = clock.Add(31 * 24 * time.Hour)
	drain(t, s, b) // runs expiry: idle r1 becomes a tombstone
	if _, err := s.MemorySync(ctx, a, MemorySyncRequest{}); code(err) != "consumer_retired" || !strings.Contains(err.Error(), "new consumer key") {
		t.Fatalf("retired consumer: %v", err)
	}
	drain(t, s, c)
	if _, err := s.MemorySync(ctx, d, MemorySyncRequest{}); code(err) != "capacity" {
		t.Fatalf("live consumers bound registration: %v", err)
	}
	*clock = clock.Add(91 * 24 * time.Hour)
	s.memory.lastExpiry = nil
	if err := s.expireMemory(ctx, m.Repository, false); err != nil {
		t.Fatal(err)
	}
	var tombstones int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory_retired`).Scan(&tombstones)
	if tombstones != 2 { // r1's tombstone aged out; idle r2 and r3 are retired now
		t.Fatalf("tombstones: %d", tombstones)
	}
}

func TestMemorySnapshotsAndDeltas(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	directive := note(t, s, m, MemoryRecordRequest{Type: "directive", Body: "lead"})
	for i := 0; i < 30; i++ {
		note(t, s, m, MemoryRecordRequest{Body: fmt.Sprint("finding ", i)})
	}
	for i := 0; i < 12; i++ {
		note(t, s, m, MemoryRecordRequest{Type: "status", Body: fmt.Sprint("status ", i)})
	}
	note(t, s, m, MemoryRecordRequest{Type: "decision", Body: "decided"})
	// Numeric acknowledgement cannot bootstrap or bypass a snapshot.
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{Through: ptr(int64(1))}); code(err) != "not_bootstrapped" {
		t.Fatalf("bootstrap: %v", err)
	}
	s.memory.limits.frameBudget = 1200
	page, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	id := page["snapshot_id"].(string)
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{Through: ptr(int64(1))}); code(err) != "snapshot_open" {
		t.Fatalf("open snapshot: %v", err)
	}
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: id}); code(err) != "snapshot_incomplete" {
		t.Fatalf("completion is tracked by the server: %v", err)
	}
	for _, r := range []MemorySyncRequest{{PageToken: 1}, {SnapshotID: "unknown", PageToken: 1}, {SnapshotID: id, PageToken: 99}} {
		if _, err := s.MemorySync(ctx, m, r); code(err) != "stale_page_token" {
			t.Fatalf("%+v: %v", r, err)
		}
	}
	other := m
	other.Consumer = "consumer-b"
	if _, err := s.MemoryAck(ctx, other, MemoryAckRequest{SnapshotID: id}); code(err) != "foreign_snapshot" {
		t.Fatalf("foreign ack: %v", err)
	}
	// A frozen snapshot keeps its pages while members are revoked and reclaimed.
	note(t, s, m, MemoryRecordRequest{Body: "withdraw", Revokes: &directive})
	var seen []MemoryEntry
	for {
		for _, raw := range page["entries"].([]json.RawMessage) {
			var e MemoryEntry
			json.Unmarshal(raw, &e)
			seen = append(seen, e)
		}
		if page["more"] != true {
			break
		}
		if page, err = s.MemorySync(ctx, m, MemorySyncRequest{SnapshotID: id, PageToken: page["page_token"].(int64)}); err != nil {
			t.Fatal(err)
		}
	}
	// Directives lead; 25 findings and 10 status entries, newest first, and the decision.
	if len(seen) != 1+1+25+10 || seen[0].Seq != directive || seen[1].Type != "decision" || seen[2].Body != "finding 29" || seen[27].Body != "status 11" {
		t.Fatalf("snapshot order: %d %+v", len(seen), seen[:3])
	}
	result, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: id})
	if err != nil || result["replayed"] != false {
		t.Fatalf("ack: %v %v", result, err)
	}
	replay, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: id})
	if err != nil || replay["replayed"] != true || replay["cursor"] != result["cursor"] {
		t.Fatalf("replay: %v %v", replay, err)
	}
	// The revocation after the frozen head arrives as a delta.
	s.memory.limits.frameBudget = defaultMemoryLimits.frameBudget
	delta, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil || delta["kind"] != "delta" || len(delta["entries"].([]json.RawMessage)) != 1 {
		t.Fatalf("delta: %v %v", delta, err)
	}
	var revocation MemoryEntry
	json.Unmarshal(delta["entries"].([]json.RawMessage)[0], &revocation)
	if revocation.Revokes == nil || *revocation.Revokes != directive {
		t.Fatalf("revocation: %+v", revocation)
	}
	next := delta["next_cursor"].(int64)
	for _, c := range []struct {
		through int64
		want    string
	}{{999, "not_issued"}, {next, ""}, {1, ""}} {
		r, err := s.MemoryAck(ctx, m, MemoryAckRequest{Through: &c.through})
		if code(err) != c.want {
			t.Fatalf("ack %d: %v %v", c.through, r, err)
		}
		if c.through == 1 && r["ignored"] != "not monotonic" {
			t.Fatalf("monotonic: %v", r)
		}
	}
	// Every sync refreshes activity; an acknowledged empty delta stays a delta.
	*clock = clock.Add(time.Minute)
	if d, err := s.MemorySync(ctx, m, MemorySyncRequest{}); err != nil || d["kind"] != "delta" {
		t.Fatalf("idle delta: %v %v", d, err)
	}
	var updated float64
	s.db.QueryRow(`SELECT updated FROM memory_cursors WHERE consumer=?`, m.Consumer).Scan(&updated)
	if updated != float64(clock.Unix()) {
		t.Fatalf("activity: %v", updated)
	}
	// A cursor below the floor returns to a snapshot.
	s.db.Exec(`UPDATE memory_stores SET floor=head+5`)
	if d, err := s.MemorySync(ctx, m, MemorySyncRequest{}); err != nil || d["kind"] != "snapshot" {
		t.Fatalf("below floor: %v %v", d, err)
	}
}

func TestMemorySnapshotExpiryAndObligation(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	note(t, s, m, MemoryRecordRequest{})
	page, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first := page["snapshot_id"].(string)
	*clock = clock.Add(2 * time.Hour)
	again, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil || again["snapshot_id"] == first {
		t.Fatalf("expired snapshot: %v %v", again, err)
	}
	if _, err := s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: first}); code(err) != "snapshot_expired" {
		t.Fatalf("expired ack: %v", err)
	}
	var cursor int64
	s.db.QueryRow(`SELECT seq FROM memory_cursors WHERE consumer=?`, m.Consumer).Scan(&cursor)
	if cursor != 0 {
		t.Fatalf("progress advanced without an acknowledgement: %d", cursor)
	}
	// A cleared snapshot keeps its obligation.
	s.db.Exec(`UPDATE memory_cursors SET snapshot='bogus'`)
	if d, err := s.MemorySync(ctx, m, MemorySyncRequest{}); err != nil || d["kind"] != "snapshot" {
		t.Fatalf("obligation: %v %v", d, err)
	}
	// Four retained snapshots per consumer.
	s.db.Exec(`UPDATE memory_cursors SET snapshot=NULL,resnapshot=1`)
	for i := 0; i < 4; i++ {
		s.db.Exec(`UPDATE memory_cursors SET snapshot=NULL,resnapshot=1`)
		if _, err := s.MemorySync(ctx, m, MemorySyncRequest{}); err != nil && code(err) != "snapshot_capacity" {
			t.Fatal(err)
		}
	}
	if _, err := s.MemorySync(ctx, m, MemorySyncRequest{}); code(err) != "snapshot_capacity" {
		t.Fatalf("snapshot capacity: %v", err)
	}
}

func TestMemoryFramingRecallAndStatus(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		note(t, s, m, MemoryRecordRequest{Type: "decision", Body: strings.Repeat(fmt.Sprint(i%10), 8192)})
	}
	pages := 0
	if got := len(drainCounting(t, s, m, &pages)); got != 40 || pages < 2 {
		t.Fatalf("pages %d entries %d", pages, got)
	}
	// An entry that could never be delivered is refused at admission.
	s.memory.limits.frameBudget = 64
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "too wide for one page"}); code(err) != "entry_too_large" {
		t.Fatalf("undeliverable: %v", err)
	}
	s.memory.limits.frameBudget = defaultMemoryLimits.frameBudget
	// Recall is paged with next_before, applies the liveness rule and matches literally.
	s.memory.limits.rowWindow = 3
	var keep []int64
	for i := 0; i < 7; i++ {
		keep = append(keep, note(t, s, m, MemoryRecordRequest{Body: fmt.Sprint("needle ", i)}))
	}
	dead := note(t, s, m, MemoryRecordRequest{Body: "needle revoked"})
	note(t, s, m, MemoryRecordRequest{Body: "drop", Revokes: &dead})
	note(t, s, m, MemoryRecordRequest{Body: "needle expired", Expires: ptr(float64(clock.Unix()) - 1)})
	note(t, s, m, MemoryRecordRequest{Body: "100% literal_x"})
	seen := map[int64]bool{}
	before := int64(0)
	for {
		r, err := s.MemoryRecall(ctx, m, "needle", before)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range r["entries"].([]json.RawMessage) {
			var e MemoryEntry
			json.Unmarshal(raw, &e)
			seen[e.Seq] = true
		}
		if r["more"] != true {
			break
		}
		before = r["next_before"].(int64)
	}
	if len(seen) != 7 || seen[dead] {
		t.Fatalf("recall: %v", seen)
	}
	if got := live(t, s, m, "%"); len(got) != 1 {
		t.Fatalf("wildcard not literal: %v", got)
	}
	if got := live(t, s, m, "l_t"); len(got) != 0 {
		t.Fatalf("underscore not literal: %v", got)
	}
	// Status pages its consumer list with next_after.
	s.memory.limits.rowWindow = 2
	for i := 1; i <= 5; i++ {
		c := m
		c.Consumer = fmt.Sprint("c", i)
		if _, err := s.register(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	after := ""
	for {
		st, err := s.MemoryStatus(ctx, m, after)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range st["consumers"].([]map[string]any) {
			names = append(names, c["consumer"].(string))
		}
		if st["more"] != true {
			break
		}
		after = st["next_after"].(string)
	}
	if strings.Join(names, ",") != "c1,c2,c3,c4,c5,consumer-a" {
		t.Fatalf("status pages: %v", names)
	}
}

func drainCounting(t *testing.T, s *Store, m MemoryCaller, pages *int) []MemoryEntry {
	ctx := context.Background()
	page, err := s.MemorySync(ctx, m, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	id := page["snapshot_id"].(string)
	var entries []MemoryEntry
	for {
		*pages++
		data, _ := json.Marshal(page["entries"])
		if int64(len(data)) > defaultMemoryLimits.frameBudget+int64(len(page["entries"].([]json.RawMessage))) {
			t.Fatalf("page of %d bytes", len(data))
		}
		for _, raw := range page["entries"].([]json.RawMessage) {
			var e MemoryEntry
			json.Unmarshal(raw, &e)
			entries = append(entries, e)
		}
		if page["more"] != true {
			break
		}
		if page, err = s.MemorySync(ctx, m, MemorySyncRequest{SnapshotID: id, PageToken: page["page_token"].(int64)}); err != nil {
			t.Fatal(err)
		}
	}
	s.MemoryAck(ctx, m, MemoryAckRequest{SnapshotID: id})
	return entries
}

func TestMemoryChangeHookAfterCommitOnly(t *testing.T) {
	s, _, m := memoryStore(t)
	ctx := context.Background()
	var changes []string
	s.OnMemoryChange(func(repo string) {
		// The commit is visible before the hook runs.
		h, _ := head(t, s, repo)
		changes = append(changes, fmt.Sprint(repo, "@", h))
	})
	note(t, s, m, MemoryRecordRequest{})
	drain(t, s, m)
	s.MemorySync(ctx, m, MemorySyncRequest{})
	s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "x", Supersedes: ptr(int64(99))})
	if strings.Join(changes, ";") != m.Repository+"@1" {
		t.Fatalf("hooks: %v", changes)
	}
}

func TestStorageBound(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	path := filepath.Join(root, "state.sqlite3")
	if _, err := os.Stat(path + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared-memory file: %v", err)
	}
	if err := configure(s.db); err != nil {
		t.Fatalf("settings: %v", err)
	}
	m := MemoryCaller{Repository: "/synthetic/.git", Family: "codex", Name: "codex-x-01", Consumer: "c"}
	s.now = time.Now
	note(t, s, m, MemoryRecordRequest{})
	// A write begins only with an empty log.
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		t.Fatalf("log holds %d bytes at the start of a write", info.Size())
	}
	tx.Rollback()
	// The ceiling refuses ordinary writes while the reserve serves withdrawal and progress.
	pages, _ := s.pages(ctx, s.db)
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance - 1
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "ordinary"}); code(err) != "capacity" {
		t.Fatalf("ordinary at the ceiling: %v", err)
	}
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "withdrawal", Revokes: ptr(int64(1))}); err != nil {
		t.Fatalf("withdrawal at the ceiling: %v", err)
	}
	if _, err := s.MemorySync(ctx, m, MemorySyncRequest{}); code(err) != "capacity" {
		t.Fatalf("a fresh consumer at the ceiling: %v", err)
	}
	s.storage.maxPages = defaultMaxPages
	// The engine ceiling rolls a write back whole and keeps integrity.
	pages, _ = s.pages(ctx, s.db)
	s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	big := strings.Repeat("x", 8000)
	for i := 0; i < 3; i++ {
		if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: big + fmt.Sprint(i)}); err != nil && code(err) != "capacity" {
			t.Fatalf("engine full: %v", err)
		}
	}
	var integrity string
	s.db.QueryRow("PRAGMA integrity_check").Scan(&integrity)
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", defaultMaxPages))
	// A failed log proof blocks writes; reads and status stay; recovery clears it.
	real := s.storage.path
	fake := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(fake+"-wal", []byte("unflushed"), 0600)
	s.storage.path = fake
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "blocked"}); code(err) != "storage_blocked" {
		t.Fatalf("failed proof: %v", err)
	}
	if _, err := s.MemoryRecord(ctx, m, MemoryRecordRequest{Type: "finding", Body: "withdrawal", Revokes: ptr(int64(1))}); code(err) != "storage_blocked" {
		t.Fatalf("blocked control write: %v", err)
	}
	if st, err := s.StorageStatus(ctx); err != nil || st.Blocked == nil {
		t.Fatalf("status while blocked: %+v %v", st, err)
	}
	if _, err := s.MemoryRecall(ctx, m, "x", 0); err != nil {
		t.Fatalf("reads while blocked: %v", err)
	}
	if _, err := s.Recover(ctx); code(err) != "storage_blocked" {
		t.Fatalf("recovery without a proof: %v", err)
	}
	s.storage.path = real
	if st, err := s.Recover(ctx); err != nil || st.Blocked != nil {
		t.Fatalf("recovery: %+v %v", st, err)
	}
	note(t, s, m, MemoryRecordRequest{Body: "after recovery"})
}

func TestMemoryAPIAndMigration(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	repo := namedRepo(t, "koinon")
	join(t, d.store, "codex", "synthetic-a", repo)
	caller := Key{"codex", "synthetic-a"}
	status, result := post(t, address, secret, "/v1/memory/record", map[string]any{"caller": caller, "type": "gotcha", "body": "api entry"})
	if status != 200 || result["result"].(map[string]any)["seq"] != float64(1) {
		t.Fatalf("record: %d %v", status, result)
	}
	if status, result := post(t, address, secret, "/v1/memory/record", map[string]any{"caller": caller, "type": "gotcha", "body": "x", "record_format": 2}); status != 400 {
		t.Fatalf("unknown field: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/memory/sync", map[string]any{"caller": caller, "consumer": "stable"})
	page := result["result"].(map[string]any)
	if status != 200 || page["kind"] != "snapshot" {
		t.Fatalf("sync: %d %v", status, result)
	}
	if status, result := post(t, address, secret, "/v1/memory/ack", map[string]any{"caller": caller, "consumer": "stable", "through": 1}); status != 409 || result["code"] != "snapshot_open" {
		t.Fatalf("typed refusal: %d %v", status, result)
	}
	if status, result := post(t, address, secret, "/v1/memory/recall", map[string]any{"caller": caller, "query": "api"}); status != 200 || len(result["result"].(map[string]any)["entries"].([]any)) != 1 {
		t.Fatalf("recall: %d %v", status, result)
	}
	if status, result := post(t, address, secret, "/v1/memory/status", map[string]any{"caller": caller}); status != 200 || result["result"].(map[string]any)["head"] != float64(1) {
		t.Fatalf("status: %d %v", status, result)
	}
	if status, result := post(t, address, secret, "/v1/storage/recover", map[string]any{}); status != 200 || result["storage"].(map[string]any)["blocked"] != nil {
		t.Fatalf("recover: %d %v", status, result)
	}
	data, err := GetStatus(context.Background(), address, secret)
	if err != nil || !strings.Contains(string(data), `"schema":4`) || !strings.Contains(string(data), `"max_pages"`) {
		t.Fatalf("daemon status: %s %v", data, err)
	}
}
