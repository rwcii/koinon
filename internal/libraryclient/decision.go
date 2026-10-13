package libraryclient

import (
	"context"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

// DecisionRequest contains outcome observations. Family and root are absent: the
// existing native client derives them afresh before recording machine state.
type DecisionRequest struct {
	ItemID        string                 `json:"item_id"`
	Choice        string                 `json:"decision"`
	Evidence      string                 `json:"evidence"`
	CatalogDigest string                 `json:"catalog_digest"`
	ItemDigest    string                 `json:"item_digest"`
	Before        []library.TargetDigest `json:"pre_edit"`
	Result        []library.TargetDigest `json:"result"`
	Compared      int64                  `json:"if_revision"`
}

func (c Client) Decision(ctx context.Context, r DecisionRequest) (*core.LibraryDecisionRecord, error) {
	i, _, err := c.inspectionContext(ctx, "machine", "")
	if err != nil {
		return nil, err
	}
	if i.MachineRoot == "" {
		return nil, library.ErrUnsupportedScope
	}
	var reply struct {
		Library library.ReadResult `json:"library"`
	}
	if err := c.call(ctx, "/v1/library/read", map[string]any{"item_id": r.ItemID}, &reply); err != nil {
		return nil, err
	}
	cat := library.Catalog{Version: reply.Library.Version, Digest: reply.Library.Digest, Items: []library.Item{reply.Library.Item}}
	root := library.ScopeDigest("machine", i.MachineRoot)
	d := library.Decision{Marker: library.DecisionMarker, Version: library.DecisionVersion, ItemID: r.ItemID, Family: c.Caller.Family, Scope: "machine", ScopeDigest: root,
		CatalogDigest: r.CatalogDigest, ItemDigest: r.ItemDigest, Kind: "item", Choice: r.Choice, Evidence: r.Evidence, Before: r.Before, Result: r.Result}
	if cat.ValidateCurrent(d, c.Caller.Family, "machine", root) != nil || r.Compared < 0 {
		return nil, library.ErrInvalidDecision
	}
	var outcome struct {
		Result *core.LibraryDecisionRecord `json:"result"`
	}
	if err := c.call(ctx, "/v1/library/decision", map[string]any{"root_digest": root, "if_revision": r.Compared, "decision": d}, &outcome); err != nil {
		return nil, err
	}
	return outcome.Result, nil
}
