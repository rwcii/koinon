package libraryclient

import (
	"context"
	"encoding/json"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

type projectReceipts struct {
	entries     []core.MemoryEntry
	floor       int64
	incomplete  bool
	unavailable []struct {
		ItemID      string `json:"item_id"`
		Family      string `json:"family"`
		ScopeDigest string `json:"scope_digest"`
		Kind        string `json:"kind"`
		Reason      string `json:"reason"`
	}
	error bool
}

func (c Client) projectDecisions(ctx context.Context, repository string) projectReceipts {
	p := projectReceipts{}
	before := int64(0)
	for {
		var reply struct {
			Result struct {
				Entries    []core.MemoryEntry `json:"entries"`
				More       bool               `json:"more"`
				NextBefore int64              `json:"next_before"`
				History    struct {
					Floor       int64           `json:"floor"`
					Incomplete  bool            `json:"incomplete"`
					Unavailable json.RawMessage `json:"unavailable"`
				} `json:"library_history"`
			} `json:"result"`
		}
		if err := c.call(ctx, "/v1/memory/recall", map[string]any{"query": library.DecisionMarker, "before": before}, &reply); err != nil {
			p.error = true
			return p
		}
		p.entries = append(p.entries, reply.Result.Entries...)
		p.floor, p.incomplete = reply.Result.History.Floor, reply.Result.History.Incomplete
		if len(reply.Result.History.Unavailable) > 0 {
			if json.Unmarshal(reply.Result.History.Unavailable, &p.unavailable) != nil {
				p.error = true
				return p
			}
		}
		if !reply.Result.More {
			return p
		}
		if reply.Result.NextBefore <= 0 || before > 0 && reply.Result.NextBefore >= before {
			p.error = true
			return p
		}
		before = reply.Result.NextBefore
	}
}

func (p projectReceipts) selectDecision(item, kind, family, scope string) (*library.Decision, int64, string) {
	if p.error {
		return nil, 0, "decision_unavailable"
	}
	var selected *library.Decision
	seq := int64(0)
	for _, e := range p.entries {
		var header struct {
			Library     string `json:"library"`
			ItemID      string `json:"item_id"`
			Kind        string `json:"kind"`
			Family      string `json:"family"`
			ScopeDigest string `json:"scope_digest"`
		}
		if json.Unmarshal([]byte(e.Body), &header) != nil || header.Library != library.DecisionMarker || header.ItemID != item || header.Kind != kind || header.Family != family || header.ScopeDigest != scope {
			continue
		}
		d, err := library.ParseDecision([]byte(e.Body))
		if err != nil || e.Type != "decision" || e.Scope != "repo" || e.WriterFamily != family || d.Scope != "project" || e.SupersededBy != nil || e.RevokedBy != nil || e.Revokes != nil {
			return nil, 0, "decision_unavailable"
		}
		if selected != nil || e.ConflictsWith != nil {
			return nil, 0, "decision_conflict"
		}
		selected, seq = &d, e.Seq
	}
	if selected != nil {
		return selected, seq, ""
	}
	for _, absent := range p.unavailable {
		if absent.ItemID == item && absent.Kind == kind && absent.Family == family && absent.ScopeDigest == scope {
			return nil, 0, absent.Reason
		}
	}
	if p.floor > 0 || p.incomplete {
		return nil, 0, "retained_history_incomplete"
	}
	return nil, 0, ""
}
