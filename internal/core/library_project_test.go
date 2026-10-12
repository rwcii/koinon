package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func projectDecisionFixture(t *testing.T, m MemoryCaller) library.Decision {
	t.Helper()
	c, _ := library.Embedded("dev")
	read, _ := c.Read(m.Family, "skill.pair")
	return library.Decision{Marker: library.DecisionMarker, Version: library.DecisionVersion, ItemID: read.Item.ID, Family: m.Family, Scope: "project", ScopeDigest: library.ScopeDigest("project", m.Repository), CatalogDigest: c.Digest, ItemDigest: read.Item.ContentDigest, Kind: "item", Choice: "adopted", Evidence: "synthetic exact-change evidence", Result: []library.TargetDigest{{ID: "skill", Digest: read.Item.ContentDigest}}, Adopted: []library.TargetDigest{{ID: "skill", Digest: read.Item.ContentDigest}}}
}

func TestProjectLibraryReceiptDeclineRetryAndRevocation(t *testing.T) {
	s, clock, m := memoryStore(t)
	ctx := context.Background()
	d := projectDecisionFixture(t, m)
	body, _ := d.Encode()
	deadline := float64(clock.Unix()) + 600
	r := MemoryRecordRequest{Type: "decision", Body: string(body), Key: ptr("synthetic-adopt"), Deadline: &deadline}
	first, err := s.MemoryRecord(ctx, m, r)
	if err != nil {
		t.Fatal(err)
	}
	d.Choice, d.Evidence = "declined", "synthetic update decline"
	d.Result = nil
	body, _ = d.Encode()
	r.Body = string(body)
	r.Key = ptr("synthetic-decline")
	r.Supersedes = &first.Seq
	second, err := s.MemoryRecord(ctx, m, r)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.MemoryRecord(ctx, m, r)
	if err != nil || !repeat.Duplicate || repeat.Seq != second.Seq {
		t.Fatalf("keyed repeat: %+v %v", repeat, err)
	}
	before := libraryRows(t, s)
	recalled, err := s.MemoryRecall(ctx, m, library.DecisionMarker, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recalled["entries"].([]json.RawMessage)) != 1 {
		t.Fatal("superseded receipt remained live", recalled)
	}
	if string(before) != string(libraryRows(t, s)) {
		t.Fatal("state recall moved memory or cursors")
	}
	var entry MemoryEntry
	json.Unmarshal(recalled["entries"].([]json.RawMessage)[0], &entry)
	retained, err := library.ParseDecision([]byte(entry.Body))
	if err != nil || retained.Adopted[0].Digest != d.Adopted[0].Digest {
		t.Fatal("decline lost adopted baseline", err)
	}
	note(t, s, m, MemoryRecordRequest{Type: "decision", Body: "synthetic explicit revocation", Revokes: &second.Seq})
	recalled, err = s.MemoryRecall(ctx, m, library.DecisionMarker, 0)
	if err != nil {
		t.Fatal(err)
	}
	history := recalled["library_history"].(map[string]any)
	unavailable := history["unavailable"].([]libraryUnavailableReceipt)
	if len(recalled["entries"].([]json.RawMessage)) != 0 || len(unavailable) != 1 || unavailable[0].Reason != "decision_revoked" {
		t.Fatalf("revocation became missing decision: %+v", recalled)
	}
}

func TestProjectLibraryReceiptBindsFamilyScopeAndBaseline(t *testing.T) {
	s, _, m := memoryStore(t)
	d := projectDecisionFixture(t, m)
	body, _ := d.Encode()
	first := note(t, s, m, MemoryRecordRequest{Type: "decision", Body: string(body)})
	for _, change := range []func(*library.Decision){func(d *library.Decision) { d.Family = "claude" }, func(d *library.Decision) { d.ScopeDigest = library.ScopeDigest("project", "/synthetic/foreign") }, func(d *library.Decision) { d.Choice = "declined"; d.Adopted = nil }, func(d *library.Decision) { d.CatalogDigest = library.ScopeDigest("machine", "/synthetic/stale") }} {
		wrong := d
		change(&wrong)
		body, _ := wrong.Encode()
		if _, err := s.MemoryRecord(context.Background(), m, MemoryRecordRequest{Type: "decision", Body: string(body), Supersedes: &first, Author: ptr("synthetic trusted-looking author")}); code(err) != "invalid_library_decision" {
			t.Fatalf("unbound receipt accepted: %+v %v", wrong, err)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM memory_entries`) != 1 {
		t.Fatal("invalid receipt wrote data")
	}
}
