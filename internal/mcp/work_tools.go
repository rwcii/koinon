package mcp

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/rwcii/koinon/internal/core"
)

// Work tools: one per wire operation of docs/WORK-ITEMS-COMMANDS.md. Work records are what
// agents report about their shared work.

var workDescriptions = map[string]string{
	"work-create":  "Create a work item in this repository's shared store, with title, criteria and non_goals. It starts open and unclaimed; key and deadline make a retry safe.",
	"work-get":     "Read one work item with its current claim, lease and progress state, or a retained scope revision. Reading never claims or renews anything.",
	"work-list":    "Page work item summaries in work-ID order with after, more and next_after; filter by lifecycle, owner, proposed_assignee, assigned_to_me, stale or blocked. assigned_to_me includes proposals to this native holder's family/role. Page to the end for a complete current inventory, then use memory catch-up for changes.",
	"work-propose": "Set or clear (null) a proposed_assignee consumer or proposed_role {family, role}; empty role names the default participant. Supply one of the mutually exclusive fields. A proposal moves no claim.",
	"work-edit":    "Revise the title, criteria or non_goals of a work item. While it is claimed, its owner edits it with claim_generation.",
	"work-start":   "Claim a work item before writing: an exclusive writer lease plus optional [kind, key] resources (path or exact), all or nothing. A role proposal requires its current native holder and derives a native-work consumer bound to that session for the full claim generation. Another session can start it after its release or expiry. Existing participant claims continue unchanged.",
	"work-update":  "Report progress on a work item you hold: progress, checkpoint, next_artifact and the next progress_deadline; lifecycle blocked needs a blocker.",
	"work-release": "Release a work item you hold with a final checkpoint; it returns to open. Optional handoff_to (exact same-repository peer) and checkout_resource atomically notify that peer to explicitly pick up the released checkout role.",
	"work-finish":  "Finish a work item you hold: outcome completed with evidence references, or withdrawn with a reason.",
	"claim-renew":  "Extend the lease of a claim you hold. Renewal moves no progress deadline.",
	"work-report":  "Report the result of a work item you hold, with a summary and evidence references. The claim is released and the item stays reported until another worker accepts or rejects it; the store reserves that verdict first, or refuses and keeps your claim.",
	"work-accept":  "Accept the current report of a reported item, naming its report_seq and evidence references. The item finishes as completed. A worker other than the reporter records it.",
	"work-reject":  "Reject the current report of a reported item, naming its report_seq, evidence references and corrected criteria and non_goals (optional title). A new scope revision is written and the item returns open, unclaimed. A worker other than the reporter records it.",
}

// workSchema describes each field's JSON type.
var workSchema = map[string]map[string]any{
	"work_id": text, "title": text, "criteria": text, "non_goals": text, "progress": text, "checkpoint": text,
	"next_artifact": text, "blocker": text, "reason": text, "key": text, "author": text, "summary": text,
	"handoff_to": text, "checkout_resource": text,
	"outcome":           {"type": "string", "enum": []string{"completed", "withdrawn"}},
	"lifecycle":         {"type": "string", "enum": []string{"open", "active", "blocked", "reported", "finished"}},
	"proposed_assignee": {"type": []string{"string", "null"}},
	"proposed_role": {"type": []string{"object", "null"}, "properties": map[string]any{
		"family": map[string]any{"type": "string", "enum": []string{"claude", "codex", "deepseek", "agy", "opencode", "grok"}},
		"role":   map[string]any{"type": "string", "maxLength": 24},
	}, "required": []string{"family", "role"}, "additionalProperties": false},
	"after":    text,
	"owner":    {"type": []string{"string", "null"}},
	"revision": integer, "if_revision": integer, "claim_generation": integer, "if_claim_revision": integer, "report_seq": integer,
	"limit":         {"type": "integer", "minimum": 1, "maximum": 100},
	"lease_seconds": lease, "renew_for": lease,
	"progress_deadline": number, "deadline": number,
	"stale": {"type": "boolean"}, "blocked": {"type": "boolean"}, "assigned_to_me": {"type": "boolean"}, "created_by_me": {"type": "boolean"},
	"references": {"type": "array", "items": text, "maxItems": 8},
	"resources":  {"type": "array", "maxItems": 8, "items": map[string]any{"type": "array", "items": text, "minItems": 2, "maxItems": 2}},
}

// lease is a lease duration in seconds; the daemon accepts 60 to 3600.
var lease = map[string]any{"type": "integer", "minimum": 60, "maximum": 3600}

var workConsumerField = map[string]any{"type": "string", "description": "Legacy claims default to participant:<address> for its holder, otherwise FAMILY:ID. New role claims derive a native-work key bound to this native session; an explicit consumer must match that derived key. Participant keys require the current holder."}

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
				return "", true, errors.New("unknown argument " + field)
			}
			body[field] = value
		}
		for _, field := range core.WorkRequired[op] {
			if args[field] == nil {
				return "", true, errors.New("missing argument " + field)
			}
		}
		return "/v1/work/" + op, true, nil
	}
	return "", false, nil
}
