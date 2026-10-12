package core

import (
	"context"
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
	// The migration step is added after the preceding room schema is published.
	if _, err := s.db.Exec(machineDecisionsTable); err != nil {
		t.Fatal(err)
	}
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
