// Package libraryclient combines the daemon's catalog/decisions with bounded reads
// in the existing native client's environment. No daemon filesystem executor exists.
package libraryclient

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

type Client struct {
	Caller    core.Key
	Call      func(context.Context, string, any) (json.RawMessage, error)
	Getenv    func(string) string
	Inspector library.Inspector
}

type StateRequest struct {
	ItemIDs   []string `json:"item_ids,omitempty"`
	Directory string   `json:"directory,omitempty"`
	Scope     string   `json:"scope,omitempty"`
}

type StateResult struct {
	Version       int                 `json:"version"`
	CatalogDigest string              `json:"catalog_digest"`
	Scope         string              `json:"scope"`
	Items         []library.ItemState `json:"items"`
	Limitations   []string            `json:"limitations,omitempty"`
}

func (c Client) call(ctx context.Context, path string, body map[string]any, result any) error {
	body["caller"] = c.Caller
	data, err := c.Call(ctx, path, body)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, result) != nil {
		return errors.New("invalid library response")
	}
	return nil
}

func (c Client) catalog(ctx context.Context) (library.Catalog, error) {
	cat := library.Catalog{Items: []library.Item{}}
	after := ""
	for {
		var reply struct {
			Library library.Page `json:"library"`
		}
		if err := c.call(ctx, "/v1/library/list", map[string]any{"after": after, "limit": library.MaxItems}, &reply); err != nil {
			return cat, err
		}
		if cat.Version != 0 && (cat.Version != reply.Library.Version || cat.Digest != reply.Library.Digest) {
			return cat, errors.New("catalog changed while paging")
		}
		cat.Version, cat.Digest = reply.Library.Version, reply.Library.Digest
		if cat.Version != 1 || !library.ValidDigest(cat.Digest) || len(cat.Items)+len(reply.Library.Items) > library.MaxItems {
			return cat, library.ErrInvalidQuery
		}
		cat.Items = append(cat.Items, reply.Library.Items...)
		if !reply.Library.More {
			return cat, nil
		}
		if reply.Library.NextAfter == "" || reply.Library.NextAfter <= after {
			return cat, library.ErrInvalidQuery
		}
		after = reply.Library.NextAfter
	}
}

func (c Client) inspectionContext(ctx context.Context, scope, directory string) (library.InspectionContext, []string, error) {
	i := library.InspectionContext{Family: c.Caller.Family, Scope: scope}
	var reply struct {
		Directory  string `json:"directory"`
		Repository string `json:"repository"`
	}
	if err := c.call(ctx, "/v1/library/context", map[string]any{}, &reply); err != nil {
		return i, nil, err
	}
	limitations := []string{}
	selected, err := core.LibraryCheckout(ctx, reply.Directory, directory, reply.Repository)
	if err != nil || reply.Repository == "" {
		if scope == "project" || directory != "" {
			return i, nil, core.ErrInvalid
		}
		limitations = append(limitations, "project_context_unavailable")
	} else {
		i.Repository, i.ProjectRoot = selected.Repository, selected.Directory
	}
	root, reason := library.NativeRoot(c.Caller.Family, c.Getenv)
	i.MachineRoot = root
	if reason != "" {
		limitations = append(limitations, reason)
	}
	return i, limitations, nil
}

func (c Client) State(ctx context.Context, r StateRequest) (StateResult, error) {
	if r.Scope == "" {
		r.Scope = "machine"
	}
	if r.Scope != "machine" && r.Scope != "project" || len(r.ItemIDs) > library.MaxItems {
		return StateResult{}, library.ErrInvalidQuery
	}
	cat, err := c.catalog(ctx)
	if err != nil {
		return StateResult{}, err
	}
	i, limitations, err := c.inspectionContext(ctx, r.Scope, r.Directory)
	if err != nil {
		return StateResult{}, err
	}
	result := StateResult{Version: cat.Version, CatalogDigest: cat.Digest, Scope: r.Scope, Items: []library.ItemState{}, Limitations: limitations}
	ids := slices.Clone(r.ItemIDs)
	if len(ids) == 0 {
		for _, item := range cat.Items {
			if r.Scope == "project" || item.Kind == "skill" {
				ids = append(ids, item.ID)
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return StateResult{}, library.ErrInvalidQuery
		}
		seen[id] = true
		if _, err := cat.Read(c.Caller.Family, id); err != nil {
			return StateResult{}, err
		}
	}
	project := projectReceipts{}
	if i.Repository != "" {
		project = c.projectDecisions(ctx, i.Repository)
	}
	for _, id := range ids {
		var reply struct {
			Library library.ReadResult `json:"library"`
		}
		if err := c.call(ctx, "/v1/library/read", map[string]any{"item_id": id}, &reply); err != nil {
			return StateResult{}, err
		}
		if reply.Library.Digest != cat.Digest || reply.Library.Item.ID != id {
			return StateResult{}, errors.New("catalog changed during inspection")
		}
		// Targets derive from the trusted response, not a locally built catalog that
		// could belong to a different binary than the running daemon.
		selected := library.Catalog{Version: cat.Version, Digest: cat.Digest, Items: []library.Item{reply.Library.Item}}
		var decision *library.Decision
		note := ""
		revision, seq := int64(0), int64(0)
		if r.Scope == "project" {
			decision, seq, note = project.selectDecision(id, "item", c.Caller.Family, library.ScopeDigest("project", i.Repository))
		} else if i.MachineRoot != "" {
			var current struct {
				Result *core.LibraryDecisionRecord `json:"result"`
			}
			if err := c.call(ctx, "/v1/library/decision/read", map[string]any{"item_id": id, "root_digest": library.ScopeDigest("machine", i.MachineRoot)}, &current); err != nil {
				note = "decision_unavailable"
			} else if current.Result != nil {
				decision = &current.Result.Decision
				revision = current.Result.Revision
			}
		} else {
			note = "decision_scope_unavailable"
		}
		state := c.Inspector.InspectItem(selected, i, id, decision)
		if note != "" {
			state.DecisionNote = note
			state.Recommended = false
		}
		state.DecisionRevision, state.DecisionSeq = revision, seq
		if state.Facts != nil {
			facts, factsSeq, factsNote := project.selectDecision(id, "repository-facts", c.Caller.Family, library.ScopeDigest("project", i.Repository))
			if i.Repository == "" {
				factsNote = "decision_scope_unavailable"
			}
			state.Facts.Decision, state.Facts.DecisionSeq, state.Facts.DecisionNote = facts, factsSeq, factsNote
		}
		result.Items = append(result.Items, state)
	}
	return result, nil
}
