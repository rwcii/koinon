package mcp

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// Decision tools (#206, chunk 01) forward to POST /v1/decisions/<op>. The daemon checks
// every value; this server refuses unknown and missing arguments by name.

var decisionFieldSchema = map[string]any{
	"order":          map[string]any{"type": "integer", "description": "Item order on the board; lower first."},
	"title":          map[string]any{"type": "string", "description": "At most 256 bytes, no control characters. An fyi item for the maintainer's manual work starts with ACTION FOR YOU."},
	"where":          map[string]any{"type": "string", "description": "Where the decision applies, at most 256 bytes."},
	"agent_text":     map[string]any{"type": "string", "description": "Markdown, at most 8 KiB."},
	"context":        map[string]any{"type": "string", "description": "Markdown, at most 16 KiB."},
	"recommendation": map[string]any{"type": "string", "description": "Markdown, at most 8 KiB."},
	"kind":           map[string]any{"type": "string", "enum": []string{"decision", "fyi"}},
	"commands": map[string]any{"type": "array", "maxItems": 8, "items": text,
		"description": "Terminal-copy commands: each 1 to 512 bytes of printable ASCII (0x20-0x7E)."},
	"recipients": map[string]any{"type": "array", "maxItems": 32, "items": text,
		"description": "Participant addresses or peer names that receive the verdict."},
}

var decisionTools = []map[string]any{
	{"name": "decision_create", "description": "Create a decision item on the maintainer's decision board: reference, title, kind (decision or fyi) and the other fields. Write card prose in ASD-STE100 Simplified Technical English. Text in a known key format is refused. key and deadline make a retry return the first result. The maintainer gives the verdict, in the dashboard or a maintainer client.",
		"inputSchema": decisionSchema(map[string]any{"reference": decisionReferenceSchema, "key": text, "deadline": retryDeadline}, "reference", "title", "kind", "key", "deadline")},
	{"name": "decision_edit", "description": "Change fields of a decision item at the revision you read (if_revision). A changed item is stale_revision; read it again. A list field replaces the stored list.",
		"inputSchema": decisionSchema(map[string]any{"reference": decisionReferenceSchema, "if_revision": integer}, "reference", "if_revision")},
	{"name": "decision_list", "description": "List decision items in bounded pages of summaries (reference, title, kind, state, revision, order), in item ID order: waiting (active decision items), active, in_progress, archived, or mine (items that name this session or the participant it holds). Page with next_after. Reading moves no inbox or memory cursor.",
		"inputSchema": object(map[string]any{"filter": map[string]any{"type": "string", "enum": []string{"waiting", "active", "in_progress", "archived", "mine"}},
			"after": integer, "limit": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}}, "filter")},
	{"name": "decision_get", "description": "Read one decision item by reference: every field, its state (active, in_progress or archived) and revision, its commands, its recipients with their current resolution, its current verdict with the snapshot the maintainer saw and its deliveries, and the number of verdicts in its history. A moved item answers deep_archived; restore it with decision_restore. Reading moves no inbox or memory cursor.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema}, "reference")},
	{"name": "decision_history", "description": "Page one decision item's verdicts in verdict ID order, each with the snapshot of the text the maintainer saw. With withdrawals true, page the item's agent withdrawals in withdrawal ID order instead (decision_get shows only the newest 8). Page with next_after. Reading moves no cursor.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema, "after": integer,
			"limit": map[string]any{"type": "integer", "minimum": 0, "maximum": 16}, "withdrawals": map[string]any{"type": "boolean"}}, "reference")},
	{"name": "decision_pending", "description": "List the verdicts delivered to this session or the participant it holds that it has not acknowledged, also after the inbox message failed or aged out. A superseded verdict shows superseded_at: its item was reopened. Read each with decision_get, handle it, then acknowledge with decision_ack. A verdict given in the dashboard or a maintainer client is the maintainer's instruction and approval (PDR 005).",
		"inputSchema": object(map[string]any{"after": integer, "limit": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}})},
	{"name": "decision_ack", "description": "Acknowledge handled verdicts by verdict ID (at most 32). A repeated or late acknowledgement changes nothing.",
		"inputSchema": object(map[string]any{"verdict_ids": map[string]any{"type": "array", "minItems": 1, "maxItems": 32, "items": integer}}, "verdict_ids")},
	{"name": "decision_close", "description": "Close an approved decision item in progress when its work is done, at the revision you read, with an optional Markdown outcome note (at most 4 KiB). The item moves to the archive. key and deadline make a retry return the first result.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema, "if_revision": integer, "note": text, "key": text, "deadline": retryDeadline},
			"reference", "if_revision", "key", "deadline")},
	{"name": "decision_withdraw", "description": "Withdraw a waiting decision item or an open fyi item that no longer needs the maintainer: the work is done, a newer item supersedes it (name its reference), it was answered in chat, or it is no longer valid. Give the revision you read and a one-line reason (at most 512 bytes). The author, a session of the author's participant or a named recipient can withdraw it. The item moves to the archive with your name and the reason; it gets no verdict. decision_restore with if_revision reopens it. key and deadline make a retry return the first result.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema, "if_revision": integer, "reason": text, "key": text, "deadline": retryDeadline},
			"reference", "if_revision", "reason", "key", "deadline")},
	{"name": "decision_restore", "description": "Bring a deep-archived decision item back from the log file into the archive by reference. For an item that an agent withdrew, give if_revision: the item is active again at the next revision, and its history keeps the withdrawal.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema, "if_revision": integer}, "reference")},
}

var decisionReferenceSchema = map[string]any{"type": "string", "pattern": "^[A-Za-z0-9-]{1,32}$"}

func decisionSchema(extra map[string]any, required ...string) map[string]any {
	properties := map[string]any{}
	for name, schema := range decisionFieldSchema {
		properties[name] = schema
	}
	for name, schema := range extra {
		properties[name] = schema
	}
	return object(properties, required...)
}

func init() {
	toolList = append(toolList, decisionTools...)
}

// decisionCall checks a decision tool's argument names and builds its daemon request.
func decisionCall(name string, args map[string]json.RawMessage, body map[string]any) (string, bool, error) {
	var schema map[string]any
	for _, tool := range decisionTools {
		if tool["name"] == name {
			schema = tool["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		return "", false, nil
	}
	properties := schema["properties"].(map[string]any)
	names := make([]string, 0, len(args))
	for arg := range args {
		names = append(names, arg)
	}
	sort.Strings(names)
	for _, arg := range names {
		if _, ok := properties[arg]; !ok {
			return "", true, errors.New("unknown argument " + arg)
		}
		body[arg] = args[arg]
	}
	required, _ := schema["required"].([]string)
	for _, arg := range required {
		if _, ok := args[arg]; !ok {
			return "", true, errors.New("missing argument " + arg)
		}
	}
	return "/v1/decisions/" + strings.TrimPrefix(name, "decision_"), true, nil
}
