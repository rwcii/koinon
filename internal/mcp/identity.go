package mcp

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/rwcii/koinon/internal/core"
)

var errIdentity = errors.New("identity_unavailable")

// openCodeField is the argument that the Koinon OpenCode plugin sets on every Koinon tool
// call from the calling session's ID (spike fact 6); OpenCode gives MCP servers no other
// per-call session source.
const openCodeField = "koinon_session"

// callerFields may never come from tool arguments: a model must not choose its identity.
var callerFields = []string{"caller", "family", "id", "as", "session", "session_id"}

func claudeProcess(command func(int) (string, []string, error), pid int) bool {
	if command == nil || pid <= 1 {
		return false
	}
	executable, args, err := command(pid)
	if err != nil {
		return false
	}
	// The native installer runs `claude` from .../claude/versions/<version>; an npm
	// installation runs node with the @anthropic-ai/claude-code entry point.
	if filepath.Base(executable) == "claude" || strings.Contains(executable, "/claude/versions/") {
		return true
	}
	if len(args) > 0 && filepath.Base(args[0]) == "claude" {
		return true
	}
	for _, arg := range args {
		if strings.Contains(arg, "@anthropic-ai/claude-code/") {
			return true
		}
	}
	return false
}

// codexSubagent reports whether a Codex call may come from a thread other than the user's
// own: its turn metadata, an object or a JSON string, names a thread_source other than
// "user" ("subagent", live-checks F2, or a value not yet known). A call without that
// metadata is the user's thread.
func codexSubagent(meta map[string]json.RawMessage) bool {
	raw := meta["x-codex-turn-metadata"]
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}
	var turn struct {
		ThreadSource string `json:"thread_source"`
	}
	return json.Unmarshal(raw, &turn) == nil && turn.ThreadSource != "" && turn.ThreadSource != "user"
}

// identity names the calling session from the family's own per-call source, or from the
// trusted Claude environment. It removes the OpenCode plugin field from the arguments.
func (s *server) identity(meta map[string]json.RawMessage, args map[string]json.RawMessage) (core.Key, error) {
	for _, field := range callerFields {
		if _, found := args[field]; found {
			return core.Key{}, errIdentity
		}
	}
	s.mu.Lock()
	client, claude := s.client, s.claude
	s.mu.Unlock()
	text := func(raw json.RawMessage) string {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return ""
		}
		return value
	}
	session, plugin := args[openCodeField]
	delete(args, openCodeField)
	var key core.Key
	switch {
	case meta["threadId"] != nil:
		if strings.HasPrefix(client, "codex") {
			key = core.Key{Family: "codex", ID: text(meta["threadId"])}
		}
	case meta["antigravity.google/conversation_id"] != nil:
		key = core.Key{Family: "agy", ID: text(meta["antigravity.google/conversation_id"])}
	case client == "claude-code":
		if claude && !plugin {
			key = core.Key{Family: "claude", ID: s.c.Getenv("CLAUDE_CODE_SESSION_ID")}
		}
	case client == "opencode":
		if plugin {
			key = core.Key{Family: "opencode", ID: text(session)}
		}
	}
	if plugin && key.Family != "opencode" {
		return core.Key{}, errIdentity
	}
	if key.ID == "" || len(key.ID) > 256 || strings.ContainsAny(key.ID, "\x00\r\n") {
		return core.Key{}, errIdentity
	}
	return key, nil
}
