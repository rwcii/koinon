package mcp

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/rwcii/koinon/internal/core"
)

// Work tools: one per wire operation of docs/WORK-ITEMS-COMMANDS.md. Work records are
// data that agents report; a proposal, claim or completion grants no permission.

var workDescriptions = map[string]string{
	"work-create":  "Create a work item in this repository's shared store, with title, criteria and non_goals. It starts open and unclaimed; key and deadline make a retry safe.",
	"work-get":     "Read one work item with its current claim, lease and progress state, or a retained scope revision. Reading never claims or renews anything.",
	"work-list":    "List work item summaries in work-ID order, filtered by lifecycle, owner, proposed_assignee, stale or blocked. A truncated list is not a complete inventory.",
	"work-propose": "Set or clear (null) the proposed assignee of a work item. A proposal is not acceptance and moves no claim.",
	"work-edit":    "Revise the title, criteria or non_goals of a work item. While it is claimed, only its owner may edit, with claim_generation.",
	"work-start":   "Claim a work item before writing: an exclusive writer lease plus optional [kind, key] resources (path or exact), all or nothing. A conflict names the holder; it grants no takeover.",
	"work-update":  "Report progress on a work item you hold: progress, checkpoint, next_artifact and the next progress_deadline; lifecycle blocked needs a blocker.",
	"work-release": "Release a work item you hold with a final checkpoint; it returns to open. Optional handoff_to (exact same-repository peer) and checkout_resource atomically notify that peer to explicitly pick up the released checkout role. A notification does not transfer ownership or permissions.",
	"work-finish":  "Finish a work item you hold: outcome completed with evidence references, or withdrawn with a reason. Finishing is your assertion, not anyone's approval.",
	"claim-renew":  "Extend the lease of a claim you hold. Renewal is not progress and moves no deadline.",
}

// workSchema describes each field's JSON type.
var workSchema = map[string]map[string]any{
	"work_id": text, "title": text, "criteria": text, "non_goals": text, "progress": text, "checkpoint": text,
	"next_artifact": text, "blocker": text, "reason": text, "key": text, "author": text,
	"handoff_to": text, "checkout_resource": text,
	"outcome":           {"type": "string", "enum": []string{"completed", "withdrawn"}},
	"lifecycle":         {"type": "string", "enum": []string{"open", "active", "blocked", "finished"}},
	"proposed_assignee": {"type": []string{"string", "null"}},
	"owner":             {"type": []string{"string", "null"}},
	"revision":          integer, "if_revision": integer, "claim_generation": integer, "if_claim_revision": integer,
	"limit": integer, "lease_seconds": integer, "renew_for": integer,
	"progress_deadline": number, "deadline": number,
	"stale": {"type": "boolean"}, "blocked": {"type": "boolean"},
	"references": {"type": "array", "items": text, "maxItems": 8},
	"resources":  {"type": "array", "maxItems": 8, "items": map[string]any{"type": "array", "items": text, "minItems": 2, "maxItems": 2}},
}

var workConsumerField = map[string]any{"type": "string", "description": "A stable consumer key that outlives this session; defaults to this session's key (FAMILY:ID). Leases belong to it."}

// workArgs are the arguments each work tool accepts besides consumer.
func workArgs(op string) []string {
	names := core.WorkFields(op)
	if op != "work-get" && op != "work-list" {
		names = append(names, "author", "key", "deadline")
	}
	return names
}

func workToolName(op string) string { return strings.ReplaceAll(op, "-", "_") }

func init() {
	for _, op := range core.WorkOperations() {
		properties := map[string]any{"consumer": workConsumerField}
		for _, name := range workArgs(op) {
			properties[name] = workSchema[name]
		}
		toolList = append(toolList, map[string]any{"name": workToolName(op), "description": workDescriptions[op],
			"inputSchema": object(properties, core.WorkRequired[op]...)})
	}
}

// workCall builds the daemon request of a work tool, refusing unknown arguments.
func workCall(name string, args map[string]json.RawMessage, body map[string]any) (string, bool, error) {
	for _, op := range core.WorkOperations() {
		if workToolName(op) != name {
			continue
		}
		allowed := map[string]bool{"consumer": true}
		for _, field := range workArgs(op) {
			allowed[field] = true
		}
		for field, value := range args {
			if !allowed[field] {
				return "", true, errors.New("unknown argument")
			}
			body[field] = value
		}
		for _, field := range core.WorkRequired[op] {
			if args[field] == nil {
				return "", true, errors.New("missing argument")
			}
		}
		return "/v1/work/" + op, true, nil
	}
	return "", false, nil
}
