package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/rwcii/koinon/internal/core"
)

// decision_record (#536) records an item's verdicts in a checkout of this session's
// repository, as the checkout's Decision record fact says (#589): none writes nothing, short
// and full append to DECISIONS.md at its root. This server writes the file: the daemon gives
// the verdicts, and the agent commits the file with its work.

func init() {
	toolList = append(toolList, map[string]any{"name": "decision_record",
		"description": "Record a decision item's verdicts in this checkout, or in the named directory in a checkout of the same repository, as its Decision record fact in .koinon/skills/ship.md says. none (the default) writes nothing, and the result says where the rule goes. short and full append to DECISIONS.md at the checkout root the entry of each verdict that the file does not hold yet, in verdict order: short without the card text, full with the card text that the maintainer saw. A dismissal is not recorded, and a repeated call appends nothing. Follow the result's instruction, and name the decision by its reference, never by verdict ID. A recorded entry is public repository text.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema, "directory": text}, "reference")})
}

func (s *server) decisionRecordCall(ctx context.Context, caller core.Key, args map[string]json.RawMessage, meta map[string]json.RawMessage) map[string]any {
	var a struct {
		Reference *string `json:"reference"`
		Directory *string `json:"directory"`
	}
	if decodeArgs(args, &a) != nil || a.Reference == nil || *a.Reference == "" {
		return failure("invalid_arguments")
	}
	if _, present := args["directory"]; present && (a.Directory == nil || *a.Directory == "") {
		return failure("invalid_arguments")
	}
	base, err := core.CheckoutResource(ctx, s.c.Directory)
	if err != nil {
		return failure("repo_unresolved")
	}
	checkout := base
	if a.Directory != nil {
		path := *a.Directory
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.c.Directory, path)
		}
		checkout, err = core.CheckoutResource(ctx, path)
		if err != nil || checkout.Repository != base.Repository {
			return failure("repo_unresolved")
		}
	}
	mode, err := core.DecisionRecordMode(checkout.Directory)
	var refused core.Refusal
	switch {
	case errors.As(err, &refused):
		return result(map[string]any{"ok": false, "code": refused.Code, "error": refused.Message}, true)
	case err != nil:
		return result(map[string]any{"ok": false, "code": "decision_record_failed", "error": refusalText(err.Error())}, true)
	}
	if err := s.ensure(ctx, caller); err != nil {
		return refusal(caller, err)
	}
	s.observeCall(caller, meta)
	call := func(ctx context.Context, path string, body map[string]any) ([]byte, error) {
		body["caller"] = caller
		data, err := s.daemon(ctx, path, body)
		if code(err) == "caller_inactive" {
			if err = s.register(ctx, caller); err == nil {
				data, err = s.daemon(ctx, path, body)
			}
		}
		return data, err
	}
	item, verdicts, err := core.CollectDecisionRecord(ctx, call, *a.Reference)
	if err != nil {
		return refusal(caller, err)
	}
	err = core.CheckRecordRepository(item, checkout.Repository)
	var recorded core.DecisionRecordResult
	if err == nil {
		recorded, err = core.RecordDecision(checkout.Directory, mode, *a.Reference, item.Where, verdicts)
	}
	switch {
	case errors.As(err, &refused):
		return result(map[string]any{"ok": false, "code": refused.Code, "error": refused.Message}, true)
	case err != nil:
		return result(map[string]any{"ok": false, "code": "decision_record_failed", "error": refusalText(err.Error())}, true)
	}
	return result(map[string]any{"ok": true, "result": recorded}, false)
}
