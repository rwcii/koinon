package platform

import (
	"path/filepath"
	"strings"
)

// ClaudeCommand reports whether a process with this executable and these arguments is
// Claude Code. The native installer runs `claude` from .../claude/versions/<version>; an
// npm installation runs node with the @anthropic-ai/claude-code entry point.
func ClaudeCommand(executable string, args []string) bool {
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

// AgentCommand reports whether a process with this executable and these arguments runs an
// agent CLI: Claude Code, Codex, agy, OpenCode or Grok.
func AgentCommand(executable string, args []string) bool {
	if ClaudeCommand(executable, args) {
		return true
	}
	names := []string{filepath.Base(executable)}
	if len(args) > 0 {
		names = append(names, filepath.Base(args[0]))
	}
	for _, name := range names {
		switch name {
		case "codex", "agy", "opencode", "grok", "grok-native":
			return true
		}
	}
	return false
}
