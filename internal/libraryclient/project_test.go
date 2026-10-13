package libraryclient

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

func TestProjectDecisionPagesAllReceiptsAndReportsConflicts(t *testing.T) {
	cat, _ := library.Embedded("dev")
	item, _ := cat.Read("codex", "skill.pair")
	scope := library.ScopeDigest("project", "/synthetic/common")
	d := library.Decision{Marker: library.DecisionMarker, Version: library.DecisionVersion, ItemID: item.Item.ID, Family: "codex", Scope: "project", ScopeDigest: scope, CatalogDigest: cat.Digest, ItemDigest: item.Item.ContentDigest, Kind: "item", Choice: "declined", Evidence: "synthetic evidence"}
	body, _ := d.Encode()
	entry := core.MemoryEntry{Seq: 3, Type: "decision", Scope: "repo", Body: string(body), WriterFamily: "codex"}
	calls := 0
	c := Client{Caller: core.Key{Family: "codex", ID: "synthetic-receipt-client"}, Call: func(ctx context.Context, path string, request any) (json.RawMessage, error) {
		calls++
		args := request.(map[string]any)
		if path != "/v1/memory/recall" || args["query"] != library.DecisionMarker {
			t.Fatal("changed memory interface", path, args)
		}
		entries := []core.MemoryEntry{{Seq: 9, Type: "finding", Body: "synthetic unrelated library marker mention"}}
		more := true
		next := int64(9)
		if calls == 2 {
			if args["before"] != int64(9) {
				t.Fatal("not paged")
			}
			entries = []core.MemoryEntry{entry}
			more = false
			next = 0
		}
		data, _ := json.Marshal(map[string]any{"result": map[string]any{"entries": entries, "more": more, "next_before": next}})
		return data, nil
	}}
	p := c.projectDecisions(context.Background(), "/synthetic/common")
	selected, seq, reason := p.selectDecision(item.Item.ID, "item", "codex", scope)
	if calls != 2 || selected == nil || seq != 3 || reason != "" {
		t.Fatalf("incomplete selection: %d/%d/%q", calls, seq, reason)
	}
	p.entries = append(p.entries, entry)
	if _, _, reason := p.selectDecision(item.Item.ID, "item", "codex", scope); reason != "decision_conflict" {
		t.Fatal("competing receipt inferred acceptance", reason)
	}
	p.entries = []core.MemoryEntry{entry}
	p.entries[0].WriterFamily = "claude"
	if _, _, reason := p.selectDecision(item.Item.ID, "item", "codex", scope); reason != "decision_unavailable" {
		t.Fatal("body supplied trusted family", reason)
	}
	p.entries = nil
	p.floor = 10
	if _, _, reason := p.selectDecision(item.Item.ID, "item", "codex", scope); reason != "retained_history_incomplete" {
		t.Fatal("retention became missing", reason)
	}
	p.floor = 0
	p.entries = []core.MemoryEntry{{Seq: 1, Type: "decision", Scope: "repo", WriterFamily: "codex", Body: `{"library":"koinon-library-decision",`}}
	if _, _, reason := p.selectDecision(item.Item.ID, "item", "codex", scope); reason != "decision_unavailable" {
		t.Fatal("malformed retained receipt became absent", reason)
	}
}
