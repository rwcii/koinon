package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func createRoleWork(t *testing.T, s *Store, m MemoryCaller, family, role string) string {
	t.Helper()
	out := mustWork(t, s, m, "work-create", map[string]any{"title": "synthetic role task", "criteria": "synthetic criteria",
		"non_goals": "synthetic non-goals", "proposed_role": WorkRole{family, role}, "key": key(), "deadline": now(s) + 600})
	return out["work_id"].(string)
}

func TestWorkRoleConflictForeignRepositoryAndSubagent(t *testing.T) {
	s, clock, _ := workStore(t)
	repo := namedRepo(t, "role-conflicts")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "worker")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "worker")
	c := joinAs(t, s, "codex", "synthetic-c", repo, "worker")
	coordinator := joinAs(t, s, "claude", "synthetic-coordinator", repo, "")
	mc := participantCaller(t, s, Key{coordinator.Family, coordinator.ID})
	id := createRoleWork(t, s, mc, "codex", "worker")
	*clock = clock.Add(30 * time.Second)
	b, c, coordinator = renew(t, s, b), renew(t, s, c), renew(t, s, coordinator)
	*clock = clock.Add(31 * time.Second)
	b, c, coordinator = renew(t, s, b), renew(t, s, c), renew(t, s, coordinator)
	mc = participantCaller(t, s, Key{coordinator.Family, coordinator.ID})
	if get(t, s, mc, id)["role_resolution"].(map[string]any)["state"] != "conflict" {
		t.Fatal("multiple qualifiers did not expose conflict")
	}
	mb := participantCaller(t, s, Key{b.Family, b.ID})
	if _, err := start(s, mb, id, 1, nil); wcode(err) != "role_conflict" {
		t.Fatalf("conflicting role start: %v", err)
	}
	if len(mustWork(t, s, mb, "work-list", map[string]any{"assigned_to_me": true})["items"].([]any)) != 0 {
		t.Fatal("conflict assigned an arbitrary qualifier")
	}
	if err := s.ChooseHolder(context.Background(), a.Address, Key{b.Family, b.ID}, b.Revision); err != nil {
		t.Fatal(err)
	}
	sub, err := s.Register(context.Background(), withLaunch(t, s, Registration{Family: "codex", ID: "synthetic-sub",
		Repository: repo, Directory: repo, TTLSeconds: 60, Subagent: true}))
	if err != nil {
		t.Fatal(err)
	}
	ms := participantCaller(t, s, Key{sub.Family, sub.ID})
	if _, err := start(s, ms, id, 1, nil); wcode(err) != "role_not_held" {
		t.Fatalf("sub-agent held a role: %v", err)
	}
	foreign := joinAs(t, s, "codex", "synthetic-foreign", namedRepo(t, "other-repo"), "worker")
	mf := participantCaller(t, s, Key{foreign.Family, foreign.ID})
	if _, err := work(s, mf, "work-get", map[string]any{"work_id": id}); wcode(err) != "work_not_found" {
		t.Fatalf("foreign-store discovery: %v", err)
	}
	mf.Repository = mc.Repository // a stale/internal resolved caller cannot bypass scope.
	if _, err := start(s, mf, id, 1, nil); wcode(err) != "role_not_held" {
		t.Fatalf("foreign native actor accepted a role: %v", err)
	}
	mb = participantCaller(t, s, Key{b.Family, b.ID})
	mustStart(t, s, mb, id, nil) // verified parent/native holder remains eligible.
}

func TestWorkRoleMigrationPreservesLegacyState(t *testing.T) {
	root := loadFixture(t)
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.now = func() time.Time { return fixtureTime }
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles WHERE native_actor!=''`) != 0 ||
		count(t, s, `SELECT COUNT(*) FROM work_replays WHERE native_actor!=''`) != 0 ||
		count(t, s, `SELECT COUNT(*) FROM work_items WHERE proposed_role!=''`) != 0 {
		t.Fatal("fixture migration converted legacy ownership/routing")
	}
	// The committed fixture contains a live native claim, cursor and keyed outcomes;
	// existing fixture tests compare their rows and continuation across migration.
	if count(t, s, `SELECT COUNT(*) FROM claim_bundles`) == 0 || count(t, s, `SELECT COUNT(*) FROM memory_cursors`) == 0 {
		t.Fatal("fixture state was lost")
	}
	before := rowsOf(t, s.db)
	if _, err := s.db.Exec(undoSchemaTwentySeven + undoSchemaTwentySix + undoSchemaTwentyFive + undoSchemaTwentyFour + undoSchemaTwentyThree + undoSchemaTwentyTwo + undoSchemaTwentyOne + undoSchemaTwenty + undoSchemaNineteen + undoSchemaEighteen + undoSchemaSeventeen + undoSchemaSixteen + `PRAGMA user_version=15`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db, 15, 16); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, rowsOf(t, s.db)) {
		t.Fatal("schema 15 migration changed deadlines, generations, cursors or inboxes")
	}
	if err := checkCatalog(s.db, 16); err != nil {
		t.Fatal(err)
	}
}

func TestWorkRoleFundedControlsAtSaturation(t *testing.T) {
	s, clock, _ := workStore(t)
	repo := namedRepo(t, "native-controls")
	role := "worker" + strings.Repeat("x", 18)
	a := joinAs(t, s, "codex", "synthetic-native-owner", repo, role)
	if _, err := s.Mutate(context.Background(), Mutation{Family: a.Family, ID: a.ID, IfRevision: a.Revision, TTLSeconds: 3600}, false); err != nil {
		t.Fatal(err)
	}
	m := participantCaller(t, s, Key{a.Family, a.ID})
	s.memory.limits.logical, s.memory.limits.workLogical, s.memory.limits.entries = 1<<40, 1<<40, 1<<20
	ids, generations := make([]string, maxBundles), make([]int64, maxBundles)
	for i := range ids {
		out := mustWork(t, s, m, "work-create", map[string]any{"title": "synthetic saturation", "criteria": strings.Repeat("c", 4096),
			"non_goals": strings.Repeat("n", 2048), "proposed_role": WorkRole{"codex", role}, "key": key(), "deadline": now(s) + 600})
		ids[i] = out["work_id"].(string)
		generations[i] = mustStart(t, s, m, ids[i], map[string]any{"checkpoint": strings.Repeat("p", 1024),
			"next_artifact": strings.Repeat("n", 1024), "progress_deadline": now(s) + 60, "lease_seconds": 3600})
	}
	if count(t, s, `SELECT SUM(length(native_actor)) FROM claim_bundles`) != maxBundles*64 {
		t.Fatal("native bindings were not retained at their bounded size")
	}
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + mustDebt(t, s) + 64
	for {
		_, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "finding", Body: strings.Repeat("n", 8192)})
		if err != nil {
			if code(err) != "capacity" {
				t.Fatal(err)
			}
			break
		}
	}
	*clock = clock.Add(120 * time.Second)
	for i, id := range ids {
		before := count(t, s, `PRAGMA page_count`)
		if err := s.reconcile(context.Background(), m.Repository, id, now(s), new(bool)); err != nil {
			t.Fatal(err)
		}
		if growth := count(t, s, `PRAGMA page_count`) - before; growth > overdueCreditPages {
			t.Fatalf("native overdue growth: %d", growth)
		}
		before = count(t, s, `PRAGMA page_count`)
		mustWork(t, s, m, "work-finish", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id),
			"claim_generation": generations[i], "outcome": "withdrawn", "reason": strings.Repeat("r", 1024), "key": key(), "deadline": now(s) + 600})
		if growth := count(t, s, `PRAGMA page_count`) - before; growth > endCreditPages {
			t.Fatalf("native end growth: %d", growth)
		}
	}
	if mustDebt(t, s) != 0 {
		t.Fatal("native controls leaked credit")
	}
	var integrity string
	if err := s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
}

func TestWorkRoleNativeSuccessionAndProposalChanges(t *testing.T) {
	s, clock, _ := workStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "role-work")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "worker")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "worker")
	c := joinAs(t, s, "claude", "synthetic-coordinator", repo, "")
	ma, mb, mc := participantCaller(t, s, Key{a.Family, a.ID}), participantCaller(t, s, Key{b.Family, b.ID}), participantCaller(t, s, Key{c.Family, c.ID})
	id := createRoleWork(t, s, mc, "codex", "worker")
	if len(mustWork(t, s, ma, "work-list", map[string]any{"assigned_to_me": true})["items"].([]any)) != 1 ||
		len(mustWork(t, s, mb, "work-list", map[string]any{"assigned_to_me": true})["items"].([]any)) != 0 {
		t.Fatal("assignment did not select the current holder")
	}
	startKey, deadline := key(), now(s)+600
	started, err := start(s, ma, id, 1, map[string]any{"key": startKey, "deadline": deadline})
	if err != nil {
		t.Fatal(err)
	}
	gen := int64(started["claim"].(map[string]any)["generation"].(float64))
	claim := get(t, s, ma, id)["current_claim"].(map[string]any)
	if claim["consumer"] != nativeWorkConsumer(ma) || claim["ownership"] != "native" {
		t.Fatalf("new claim not native-bound: %v", claim)
	}
	// A call resolved again with the same native identity uses the same generation.
	ma = participantCaller(t, s, Key{a.Family, a.ID})
	mustWork(t, s, ma, "claim-renew", map[string]any{"work_id": id, "claim_generation": gen, "if_claim_revision": 1})
	if err := s.ChooseHolder(ctx, a.Address, Key{b.Family, b.ID}, b.Revision); err != nil {
		t.Fatal(err)
	}
	mb = participantCaller(t, s, Key{b.Family, b.ID})
	if len(mustWork(t, s, mb, "work-list", map[string]any{"assigned_to_me": true})["items"].([]any)) != 1 {
		t.Fatal("successor did not discover the outstanding assignment")
	}
	if _, err := start(s, mb, id, revision(t, s, mb, id), nil); wcode(err) != "claim_conflict" {
		t.Fatalf("successor inherited a live claim: %v", err)
	}
	// Proposal edits/clears do not transfer or erase this generation's native binding.
	mustWork(t, s, mc, "work-propose", map[string]any{"work_id": id, "if_revision": revision(t, s, mc, id), "proposed_role": nil})
	if _, err := work(s, mb, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, mb, id),
		"claim_generation": gen, "checkpoint": "synthetic wrong release"}); wcode(err) != "stale_claim" {
		t.Fatalf("cleared proposal lost its binding: %v", err)
	}
	// Holder choice retired A; re-registration revives its native session, not the
	// participant. That active original native owner can release its own generation.
	joinAs(t, s, a.Family, a.ID, repo, "worker")
	ma = participantCaller(t, s, Key{a.Family, a.ID})
	mustWork(t, s, ma, "work-release", map[string]any{"work_id": id, "if_revision": revision(t, s, mc, id),
		"claim_generation": gen, "checkpoint": "synthetic original-owner release"})
	mustWork(t, s, mc, "work-propose", map[string]any{"work_id": id, "if_revision": revision(t, s, mc, id), "proposed_role": WorkRole{"codex", "worker"}})
	newGen := mustStart(t, s, mb, id, nil)
	if newGen == gen || get(t, s, mb, id)["current_claim"].(map[string]any)["consumer"] == claim["consumer"] {
		t.Fatal("successor reused the original native consumer/generation")
	}
	// Retained retries are actor-bound even after the old bundle was reclaimed.
	replayed, err := start(s, ma, id, 1, map[string]any{"key": startKey, "deadline": deadline})
	if err != nil || replayed["duplicate"] != true {
		t.Fatalf("original native retry did not survive reclamation: %v %v", replayed, err)
	}
	copied := claim["consumer"].(string)
	wrong, err := s.ResolveWorkCaller(ctx, Key{b.Family, b.ID}, &copied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := start(s, wrong, id, 1, map[string]any{"key": startKey, "deadline": deadline}); err == nil {
		t.Fatal("another native session replayed the old start")
	}
	// Expiry is another valid path to a fresh generation; the clock never changes IDs.
	*clock = clock.Add(time.Hour)
	joinAs(t, s, b.Family, b.ID, repo, "worker")
	mb = participantCaller(t, s, Key{b.Family, b.ID})
	_, err = start(s, mb, id, revision(t, s, mb, id), nil)
	if wcode(err) != "revision_conflict" {
		t.Fatalf("expiry reconciliation: %v", err)
	}
	if next := mustStart(t, s, mb, id, nil); next == newGen {
		t.Fatal("expired native generation was reused")
	}
}

func TestWorkRoleEveryOwnerOperationAndReplay(t *testing.T) {
	for _, op := range []string{"work-edit", "work-update", "claim-renew", "work-release", "work-finish"} {
		t.Run(op, func(t *testing.T) {
			s, _ := testStore(t)
			repo := namedRepo(t, "owner-operations")
			a := joinAs(t, s, "codex", "synthetic-owner", repo, "worker")
			b := joinAs(t, s, "claude", "synthetic-other", repo, "")
			ma, mb := participantCaller(t, s, Key{a.Family, a.ID}), participantCaller(t, s, Key{b.Family, b.ID})
			id := createRoleWork(t, s, ma, "codex", "worker")
			gen := mustStart(t, s, ma, id, nil)
			fields := map[string]any{"work_id": id, "if_revision": revision(t, s, ma, id), "claim_generation": gen,
				"key": key(), "deadline": now(s) + 600}
			switch op {
			case "work-edit":
				fields["title"] = "synthetic edited title"
			case "work-update":
				fields["progress"], fields["checkpoint"], fields["next_artifact"], fields["progress_deadline"] = "progress", "checkpoint", "artifact", now(s)+600
			case "claim-renew":
				delete(fields, "if_revision")
				fields["if_claim_revision"] = 1
			case "work-release":
				fields["checkpoint"] = "synthetic release"
			case "work-finish":
				fields["outcome"], fields["reason"] = "withdrawn", "synthetic cancellation"
			}
			consumer := nativeWorkConsumer(ma)
			copied, err := s.ResolveWorkCaller(context.Background(), Key{b.Family, b.ID}, &consumer)
			if err != nil {
				t.Fatal(err)
			}
			before := rowsOf(t, s.db)
			for _, wrong := range []MemoryCaller{mb, copied} {
				if _, err := work(s, wrong, op, fields); wcode(err) != "stale_claim" {
					t.Fatalf("wrong native %s: %v", op, err)
				}
			}
			if !reflect.DeepEqual(before, rowsOf(t, s.db)) {
				t.Fatal("refused owner call changed state")
			}
			mustWork(t, s, ma, op, fields)
			for _, wrong := range []MemoryCaller{mb, copied} {
				if _, err := work(s, wrong, op, fields); wcode(err) != "stale_claim" {
					t.Fatalf("wrong native keyed replay %s: %v", op, err)
				}
			}
			if out := mustWork(t, s, ma, op, fields); out["duplicate"] != true {
				t.Fatal("same native owner failed its keyed retry")
			}
		})
	}
}

func TestWorkRoleNativeExpiryWhileWaiting(t *testing.T) {
	s, clock, _ := workStore(t)
	repo := namedRepo(t, "native-expiry")
	a := joinAs(t, s, "codex", "synthetic-owner", repo, "worker")
	m := participantCaller(t, s, Key{a.Family, a.ID})
	id := createRoleWork(t, s, m, "codex", "worker")
	gen := mustStart(t, s, m, id, nil)
	initial := *clock
	var reads atomic.Int64
	// The caller is active when normalization begins and expires before its
	// serialized write. The claim itself remains live for another 900 seconds.
	s.now = func() time.Time {
		if reads.Add(1) == 1 {
			return initial
		}
		return initial.Add(61 * time.Second)
	}
	if _, err := work(s, m, "claim-renew", map[string]any{"work_id": id, "claim_generation": gen, "if_claim_revision": 1}); err == nil {
		t.Fatal("an expired native caller renewed after waiting for the write transaction")
	}
}

func TestWorkRoleDefaultFamiliesAndValidation(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "default-roles")
	for _, family := range []string{"codex", "claude", "agy", "opencode", "grok", "deepseek"} {
		var a Session
		if family == "deepseek" {
			var err error
			a, err = s.Register(context.Background(), Registration{Family: family, ID: "synthetic-" + family, Repository: repo, Directory: repo, TTLSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			a = joinAs(t, s, family, "synthetic-"+family, repo, "")
		}
		m := participantCaller(t, s, Key{family, a.ID})
		id := createRoleWork(t, s, m, family, "")
		mustStart(t, s, m, id, nil)
		if get(t, s, m, id)["current_claim"].(map[string]any)["ownership"] != "native" {
			t.Fatalf("default %s not native-bound", family)
		}
	}
	ma := participantCaller(t, s, Key{"codex", "synthetic-codex"})
	for _, value := range []any{true, "codex", map[string]any{"family": "codex"}, WorkRole{"unknown", ""}, WorkRole{"codex", "BAD"},
		map[string]any{"family": "codex", "role": nil}, map[string]any{"family": "codex", "role": "", "native_actor": "fake"}} {
		if _, err := work(s, ma, "work-create", map[string]any{"title": "t", "criteria": "c", "non_goals": "n", "key": key(),
			"deadline": now(s) + 600, "proposed_role": value}); wcode(err) != "invalid_request" {
			t.Fatalf("invalid role %v: %v", value, err)
		}
	}
	id := createRoleWork(t, s, ma, "codex", "worker")
	if get(t, s, ma, id)["role_resolution"].(map[string]any)["state"] != "unheld" {
		t.Fatal("missing role was guessed")
	}
	if _, err := start(s, ma, id, 1, nil); wcode(err) != "role_unheld" {
		t.Fatalf("missing role start: %v", err)
	}
	if _, err := work(s, ma, "work-propose", map[string]any{"work_id": id, "if_revision": 1, "proposed_role": WorkRole{"codex", ""}, "proposed_assignee": "other"}); wcode(err) != "invalid_request" {
		t.Fatalf("two assignees: %v", err)
	}
	// Only the actual holder can accept even with a syntactically valid consumer key.
	wrongConsumer := "synthetic-other-consumer"
	explicit, err := s.ResolveWorkCaller(context.Background(), ma.Native, &wrongConsumer)
	if err != nil {
		t.Fatal(err)
	}
	id = createRoleWork(t, s, ma, "codex", "")
	if _, err := start(s, explicit, id, 1, nil); wcode(err) != "invalid_request" {
		t.Fatalf("explicit wrong native key: %v", err)
	}
}

func TestWorkAssignmentPagingAndCatchup(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "assignment-pages")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "worker")
	m := participantCaller(t, s, Key{a.Family, a.ID})
	for i := 0; i < 111; i++ {
		createRoleWork(t, s, m, "codex", "worker")
	}
	seen := map[string]bool{}
	after := ""
	for {
		fields := map[string]any{"assigned_to_me": true, "limit": 17}
		if after != "" {
			fields["after"] = after
		}
		page := mustWork(t, s, m, "work-list", fields)
		encoded, _ := json.Marshal(page)
		if len(encoded) > maxWorkList || page["more"] != page["truncated"] {
			t.Fatalf("page bound/compatibility: %d %v", len(encoded), page)
		}
		for _, raw := range page["items"].([]any) {
			id := raw.(map[string]any)["work_id"].(string)
			if seen[id] || id <= after {
				t.Fatal("paging repeated or reordered an item")
			}
			seen[id] = true
		}
		if page["more"] != true {
			if page["next_after"] != nil {
				t.Fatal("final page advertised a continuation")
			}
			break
		}
		after = page["next_after"].(string)
	}
	if len(seen) != 111 {
		t.Fatalf("incomplete inventory: %d", len(seen))
	}
	first := mustWork(t, s, m, "work-list", map[string]any{"assigned_to_me": true, "limit": 100})
	newID := createRoleWork(t, s, m, "codex", "worker")
	second := mustWork(t, s, m, "work-list", map[string]any{"assigned_to_me": true, "after": first["next_after"]})
	found := false
	for _, raw := range second["items"].([]any) {
		found = found || raw.(map[string]any)["work_id"] == newID
	}
	if !found {
		t.Fatal("item created between pages was lost")
	}
	// An assignment changed behind the page boundary is visible in catch-up and a
	// fresh current query, even after the participant cursor is already advanced.
	snap, err := s.MemorySync(context.Background(), m, MemorySyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for snap["more"] == true {
		snap, err = s.MemorySync(context.Background(), m, MemorySyncRequest{SnapshotID: snap["snapshot_id"].(string), PageToken: snap["page_token"].(int64)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.MemoryAck(context.Background(), m, MemoryAckRequest{SnapshotID: snap["snapshot_id"].(string)}); err != nil {
		t.Fatal(err)
	}
	earliest := fmt.Sprintf("%032x", 1)
	mustWork(t, s, m, "work-propose", map[string]any{"work_id": earliest, "if_revision": 1, "proposed_role": nil})
	mustWork(t, s, m, "work-propose", map[string]any{"work_id": earliest, "if_revision": 2, "proposed_role": WorkRole{"codex", "worker"}})
	delta, err := s.MemorySync(context.Background(), m, MemorySyncRequest{})
	if err != nil || delta["kind"] != "delta" || len(delta["entries"].([]json.RawMessage)) < 2 {
		t.Fatalf("assignment catch-up: %v %v", delta, err)
	}
	current := mustWork(t, s, m, "work-list", map[string]any{"assigned_to_me": true, "limit": 1})
	if current["items"].([]any)[0].(map[string]any)["work_id"] != earliest {
		t.Fatal("fresh current query missed an assignment behind the cursor")
	}
}
