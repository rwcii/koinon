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
	{"name": "decision_create", "description": "Create a decision item on the maintainer's decision board: reference, title, kind (decision or fyi) and the other fields. Write card prose in ASD-STE100 Simplified Technical English. Do not put credentials, keys, personal data, private session IDs or peer message bodies in an item; known key formats are refused. key and deadline make a retry return the first result. Only the maintainer gives a verdict, in the dashboard.",
		"inputSchema": decisionSchema(map[string]any{"reference": decisionReferenceSchema, "key": text, "deadline": number}, "reference", "title", "kind", "key", "deadline")},
	{"name": "decision_edit", "description": "Change fields of a decision item at the revision you read (if_revision). A changed item is stale_revision; read it again. A list field replaces the stored list.",
		"inputSchema": decisionSchema(map[string]any{"reference": decisionReferenceSchema, "if_revision": integer}, "reference", "if_revision")},
	{"name": "decision_list", "description": "List decision items in bounded pages of summaries (reference, title, kind, state, revision, order), in item ID order: waiting (active decision items), active, in_progress, archived, or mine (items that name this session or the participant it holds). Page with next_after. Reading moves no inbox or memory cursor.",
		"inputSchema": object(map[string]any{"filter": map[string]any{"type": "string", "enum": []string{"waiting", "active", "in_progress", "archived", "mine"}},
			"after": integer, "limit": map[string]any{"type": "integer", "minimum": 0, "maximum": 100}}, "filter")},
	{"name": "decision_get", "description": "Read one decision item by reference: every field, its state and revision, its commands and its recipients with their current resolution. Reading moves no inbox or memory cursor.",
		"inputSchema": object(map[string]any{"reference": decisionReferenceSchema}, "reference")},
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
