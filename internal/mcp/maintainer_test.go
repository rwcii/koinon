package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The maintainer mode has its own tools; the agent tools stay as they are (PDR 009,
// Decision 3).
func TestMaintainerToolsAreSeparate(t *testing.T) {
	want := []string{"ack", "board_list", "decision_get", "decision_history", "decision_verdict", "inbox", "peer_status", "peers",
		"room_add", "room_create", "room_list", "room_post", "room_read", "room_remove", "send"}
	if got := maintainerToolNames(); !slices.Equal(got, want) {
		t.Fatalf("maintainer tools: %v", got)
	}
	for _, tool := range maintainerTools {
		schema := tool["inputSchema"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s: %v", tool["name"], schema)
		}
		if _, ok := schema["properties"].(map[string]any)["token"]; ok {
			t.Fatalf("%s takes a token argument", tool["name"])
		}
	}
	for _, tool := range toolList {
		if name := tool["name"].(string); slices.Contains([]string{"board_list", "decision_verdict", "room_create", "room_add", "room_remove"}, name) {
			t.Fatalf("the agent tools have %s", name)
		}
	}
}

// Unknown tools and arguments are refused before any daemon call.
func TestMaintainerServerRefusesUnknownCalls(t *testing.T) {
	var out bytes.Buffer
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"work_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"peers","arguments":{"caller":{"family":"codex","id":"x"}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send","arguments":{"to":"x"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
	}, "\n") + "\n"
	if err := ServeMaintainer(context.Background(), MaintainerConfig{StateDir: t.TempDir(), Address: "127.0.0.1:9", Token: "t", Client: "synthetic"},
		strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var reply struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
			Error *rpcError `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Error != nil {
			codes = append(codes, "rpc")
			continue
		}
		var value struct {
			Code string `json:"code"`
		}
		json.Unmarshal([]byte(reply.Result.Content[0].Text), &value)
		codes = append(codes, value.Code)
	}
	if strings.Join(codes, ",") != "unknown_tool,invalid_arguments,invalid_arguments,rpc" {
		t.Fatalf("codes: %v", codes)
	}
}
