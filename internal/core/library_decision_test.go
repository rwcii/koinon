package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func machineDecisionFixture(t *testing.T) (*Store, Key, library.Catalog, string, library.Decision) {
	t.Helper()
	s, _ := testStore(t)
	key := Key{"codex", "synthetic-library-decision"}
	if _, err := s.Register(context.Background(), Registration{Family: key.Family, ID: key.ID, Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	c, _ := library.Embedded("dev")
	read, _ := c.Read(key.Family, "skill.pair")
	root := library.ScopeDigest("machine", "/synthetic/native-root")
	d := library.Decision{Marker: library.DecisionMarker, Version: library.DecisionVersion, ItemID: read.Item.ID, Family: key.Family, Scope: "machine", ScopeDigest: root,
		CatalogDigest: c.Digest, ItemDigest: read.Item.ContentDigest, Kind: "item", Choice: "adopted", Evidence: "synthetic exact-change evidence", Result: []library.TargetDigest{{ID: "skill", Digest: read.Item.ContentDigest}}}
	return s, key, c, root, d
}

func TestMachineDecisionCASDeclineAndLostReply(t *testing.T) {
	s, key, c, root, d := machineDecisionFixture(t)
	ctx := context.Background()
	first, err := s.LibraryDecision(ctx, key, c, root, 0, d)
	if err != nil || first.Revision != 1 || len(first.Decision.Adopted) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	s.storage.blocked = "synthetic blocked write admission"
	second, err := s.LibraryDecision(ctx, key, c, root, 0, d)
	if err != nil || !second.Unchanged || second.Revision != 1 {
		t.Fatalf("lost reply: %+v %v", second, err)
	}
	s.storage.blocked = ""
	d.Choice, d.Evidence = "declined", "synthetic update decline"
	d.Result = []library.TargetDigest{{ID: "skill", Digest: strings.Repeat("c", 64)}}
	decline, err := s.LibraryDecision(ctx, key, c, root, 1, d)
	if err != nil || decline.Revision != 2 || decline.Decision.Adopted[0].Digest != first.Decision.Adopted[0].Digest {
		t.Fatalf("decline: %+v %v", decline, err)
	}
	d.Evidence = "synthetic competing choice"
	_, err = s.LibraryDecision(ctx, key, c, root, 1, d)
	var refused WorkRefusal
	if !errors.As(err, &refused) || refused.Code != "decision_conflict" {
		t.Fatalf("stale revision: %v", err)
	}
	read, err := s.LibraryDecisionRead(ctx, key, root, d.ItemID)
	if err != nil || read.Revision != 2 || read.Decision.Choice != "declined" {
		t.Fatalf("current: %+v %v", read, err)
	}
	if count(t, s, `SELECT COUNT(*) FROM library_decisions`) != 1 {
		t.Fatal("CAS created history rows")
	}
}

func TestMachineDecisionConcurrentComparedRevision(t *testing.T) {
	s, key, c, root, d := machineDecisionFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			choice := d
			choice.Evidence = fmt.Sprintf("synthetic writer %d", i)
			_, err := s.LibraryDecision(context.Background(), key, c, root, 0, choice)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	ok, conflict := 0, 0
	for err := range errs {
		var r WorkRefusal
		if err == nil {
			ok++
		} else if errors.As(err, &r) && r.Code == "decision_conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("results: %d/%d", ok, conflict)
	}
}

func TestMachineDecisionRowAndOrdinaryPageCapacity(t *testing.T) {
	s, key, c, _, d := machineDecisionFixture(t)
	ctx := context.Background()
	for i := range maxMachineDecisions {
		root := library.ScopeDigest("machine", fmt.Sprintf("/synthetic/root-%d", i))
		d.ScopeDigest = root
		if _, err := s.LibraryDecision(ctx, key, c, root, 0, d); err != nil {
			t.Fatal(i, err)
		}
	}
	root := library.ScopeDigest("machine", "/synthetic/overflow")
	d.ScopeDigest = root
	if _, err := s.LibraryDecision(ctx, key, c, root, 0, d); code(err) != "decision_capacity" {
		t.Fatalf("row ceiling: %v", err)
	}
	if count(t, s, `SELECT COUNT(*) FROM library_decisions`) != maxMachineDecisions {
		t.Fatal("evicted a decision")
	}
	root = library.ScopeDigest("machine", "/synthetic/root-0")
	d.ScopeDigest = root
	d.Evidence = "synthetic changed outcome"
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + appendAllowance - 1
	if _, err := s.LibraryDecision(ctx, key, c, root, 1, d); !errors.Is(err, ErrCapacity) {
		t.Fatalf("ordinary ceiling: %v", err)
	}
	if err := write(ctx, s); err != nil {
		t.Fatalf("reserved control stopped: %v", err)
	}
	current, err := s.LibraryDecisionRead(ctx, key, root, d.ItemID)
	if err != nil || current.Revision != 1 {
		t.Fatalf("refusal mutated current: %+v %v", current, err)
	}
}

func TestMachineDecisionFollowsRootAcrossRepositories(t *testing.T) {
	s, key, c, root, d := machineDecisionFixture(t)
	ctx := context.Background()
	d.Choice = "declined"
	d.Result = nil
	if _, err := s.LibraryDecision(ctx, key, c, root, 0, d); err != nil {
		t.Fatal(err)
	}
	otherKey := Key{"codex", "synthetic-other-repository"}
	repo := testRepo(t)
	if _, err := s.Register(ctx, Registration{Family: otherKey.Family, ID: otherKey.ID, Directory: repo, Repository: repo}); err != nil {
		t.Fatal(err)
	}
	r, err := s.LibraryDecisionRead(ctx, otherKey, root, d.ItemID)
	if err != nil || r == nil || r.Decision.Choice != "declined" {
		t.Fatalf("machine decline became repository-local: %+v %v", r, err)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), key.ID) || strings.Contains(string(data), otherKey.ID) || strings.Contains(string(data), repo) {
		t.Fatal("private native provenance in current record")
	}
	if r, err := s.LibraryDecisionRead(ctx, otherKey, library.ScopeDigest("machine", "/synthetic/other-root"), d.ItemID); err != nil || r != nil {
		t.Fatal("different native roots shared a decision", r, err)
	}
}

func TestMachineDecisionsDoNotConsumeFundedWorkControls(t *testing.T) {
	s, caller, c, root, d := machineDecisionFixture(t)
	ctx := context.Background()
	m := MemoryCaller{Family: "codex", Repository: "/synthetic/library-work/.git", Consumer: "synthetic-library-worker", Name: "synthetic-library-worker"}
	id := create(t, s, m, "synthetic funded work")
	generation := mustStart(t, s, m, id, nil)
	debt := mustDebt(t, s)
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + debt + appendAllowance - 1
	if _, err := s.LibraryDecision(ctx, caller, c, root, 0, d); !errors.Is(err, ErrCapacity) {
		t.Fatal("machine record borrowed work debt", err)
	}
	if mustDebt(t, s) != debt {
		t.Fatal("refusal changed funded obligations")
	}
	mustWork(t, s, m, "work-finish", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "claim_generation": generation, "outcome": "withdrawn", "reason": "synthetic completed control test", "key": key(), "deadline": now(s) + 600})
	if count(t, s, `SELECT COUNT(*) FROM library_decisions`) != 0 {
		t.Fatal("ordinary capacity refusal wrote a decision")
	}
}

func TestLibrarySchemaTwentyOnePreservesPublishedSchemaTwentyState(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	room := mustRoom(t, s, "synthetic preserved room", 7)
	member := join(t, s, "codex", "synthetic-published-room-member", "")
	mustAdd(t, s, room, member.Name)
	mustHumanPost(t, s, room, "synthetic retained published-schema20 post")
	if err := s.SetRoomRefill(roomCtx("room-refill"), room, 64); err != nil {
		t.Fatal(err)
	}
	m := MemoryCaller{Family: "codex", Repository: "/synthetic/migration/.git", Consumer: "synthetic-migration", Name: "synthetic-migration"}
	note(t, s, m, MemoryRecordRequest{Type: "finding", Body: "synthetic retained schema19 note"})
	before := rowsOf(t, s.db)
	if _, err := s.db.Exec(undoSchemaTwentySeven + undoSchemaTwentySix + undoSchemaTwentyFive + undoSchemaTwentyFour + undoSchemaTwentyThree + undoSchemaTwentyTwo + undoSchemaTwentyOne + `PRAGMA user_version=20`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 20); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || count(t, upgraded, `SELECT COUNT(*) FROM library_decisions`) != 0 || rowsOf(t, upgraded.db) != before {
		t.Fatal("schema21 changed retained published state")
	}
	if count(t, upgraded, `SELECT library_floor FROM memory_stores WHERE repository=?`, m.Repository) != 0 {
		t.Fatal("migration inferred lost library history from unrelated data")
	}
	if count(t, upgraded, `SELECT create_refill FROM rooms WHERE room_id=?`, room) != 7 || count(t, upgraded, `SELECT refill FROM rooms WHERE room_id=?`, room) != 64 {
		t.Fatal("room retry inputs lost in migration")
	}
	if count(t, upgraded, `SELECT COUNT(*) FROM room_posts`) != 1 || count(t, upgraded, `SELECT COUNT(*) FROM room_deliveries`) != 1 || count(t, upgraded, `SELECT COUNT(*) FROM room_replays`) != 1 {
		t.Fatal("library migration altered published room rows")
	}
	if err := checkCatalog(upgraded.db, 20); err == nil {
		t.Fatal("older schema writer accepted the new ordinary table")
	}
	if _, err := upgraded.StorageStatus(ctx); err != nil {
		t.Fatal(err)
	}
}
