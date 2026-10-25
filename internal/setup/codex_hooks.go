package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rwcii/koinon/internal/core"
)

// The Codex SessionStart hook (#275) lets the daemon move a participant to the thread that a
// Codex host shows after /clear, at that thread's first turn. Setup keeps one hook group in
// $CODEX_HOME/hooks.json (default ~/.codex/hooks.json) and leaves every other hook alone.

const codexHookCommand = " hook codex-session-start"

func codexHome(o Options) (string, error) {
	if o.CodexHome != "" {
		return o.CodexHome, nil
	}
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// codexHookGroup is the SessionStart group that setup writes for binary.
func codexHookGroup(binary string) json.RawMessage {
	return mustJSON(map[string]any{"hooks": []map[string]any{{
		"type": "command", "command": quote(binary) + codexHookCommand, "timeout": 10,
	}}})
}

// koinonGroup reports whether a SessionStart group has exactly the form that setup writes,
// for any absolute koinon binary: no matcher or other member, and one hook whose command is
// a quoted absolute path followed by the hook command. A user's group never matches.
func koinonGroup(group json.RawMessage) bool {
	members, ok := decodeMembers(group)
	if !ok || len(members) != 1 || members[0].key != "hooks" {
		return false
	}
	var hooks []json.RawMessage
	if json.Unmarshal(members[0].value, &hooks) != nil || len(hooks) != 1 {
		return false
	}
	var hook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	fields, ok := decodeMembers(hooks[0])
	if !ok || len(fields) != 3 || json.Unmarshal(hooks[0], &hook) != nil || hook.Type != "command" {
		return false
	}
	binary, found := strings.CutSuffix(hook.Command, codexHookCommand)
	if !found || len(binary) < 2 || binary[0] != '\'' || binary[len(binary)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(binary[1:len(binary)-1], `'"'"'`, "'")
	return filepath.IsAbs(path) && sameJSON(group, codexHookGroup(path))
}

// editCodexHooks rewrites the SessionStart groups of a Codex hooks file with edit, and keeps
// every other member in order. An absent file counts as empty when create is set; a file
// that is not plain JSON in Codex's hook format is refused, never rewritten.
func editCodexHooks(path string, create bool, edit func([]json.RawMessage) ([]json.RawMessage, bool)) (bool, error) {
	data, err := os.ReadFile(path)
	mode := os.FileMode(0600)
	switch {
	case errors.Is(err, os.ErrNotExist) && create:
		data = []byte("{}")
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	default:
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	top, ok := decodeMembers(data)
	if !ok {
		return false, errors.New("not a plain JSON object; left unchanged")
	}
	hooksAt, events := -1, []member{}
	for i, m := range top {
		if m.key == "hooks" {
			if events, ok = decodeMembers(m.value); !ok {
				return false, errors.New(`"hooks" is not an object; left unchanged`)
			}
			hooksAt = i
		}
	}
	startAt, groups := -1, []json.RawMessage{}
	for i, m := range events {
		if m.key == "SessionStart" {
			if json.Unmarshal(m.value, &groups) != nil {
				return false, errors.New(`"SessionStart" is not a list; left unchanged`)
			}
			startAt = i
		}
	}
	groups, changed := edit(groups)
	if !changed {
		return false, nil
	}
	switch {
	case len(groups) == 0 && startAt >= 0:
		events = append(events[:startAt:startAt], events[startAt+1:]...)
	case startAt >= 0:
		events[startAt].value = mustJSON(groups)
	default:
		events = append(events, member{"SessionStart", mustJSON(groups)})
	}
	switch {
	case len(events) == 0 && hooksAt >= 0:
		top = append(top[:hooksAt:hooksAt], top[hooksAt+1:]...)
	case hooksAt >= 0:
		top[hooksAt].value = encodeMembers(events, "  ")
	default:
		top = append(top, member{"hooks", encodeMembers(events, "  ")})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	return true, writeFile(path, append(encodeMembers(top, ""), '\n'), mode)
}

func codexHook(o Options, report *Report) error {
	root, err := codexHome(o)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "hooks.json")
	want := codexHookGroup(o.Binary)
	changed, err := editCodexHooks(path, true, func(groups []json.RawMessage) ([]json.RawMessage, bool) {
		// The result has exactly one Koinon group, for this binary, at the place of the first
		// Koinon group; groups for other binaries and copies are dropped.
		result, at, current := []json.RawMessage{}, -1, 0
		for _, g := range groups {
			if !koinonGroup(g) {
				result = append(result, g)
				continue
			}
			if sameJSON(g, want) {
				current++
			}
			if at < 0 {
				at = len(result)
				result = append(result, want)
			}
		}
		if at < 0 {
			return append(result, want), true
		}
		return result, len(result) != len(groups) || current != 1
	})
	if err != nil {
		return fmt.Errorf("codex hooks: %w", err)
	}
	entry := "Codex SessionStart hook in " + path
	if changed {
		report.Changed = append(report.Changed, entry)
		report.Note = "Codex asks to review the new SessionStart hook at its next start: trust it, so that the address moves at the first turn after /clear."
	} else {
		report.Unchanged = append(report.Unchanged, entry)
	}
	// The retired Python runtime's instructions leave the Codex AGENTS.md (#487).
	instructions := filepath.Join(root, "AGENTS.md")
	result, err := core.RemoveRetiredCodexBlocks(instructions)
	if err != nil {
		return fmt.Errorf("codex instructions: %w", err)
	}
	if result == "removed" {
		report.Changed = append(report.Changed, "retired Koinon instructions removed from "+instructions)
	}
	return nil
}

func removeCodexHook(o Options, report *Report) error {
	root, err := codexHome(o)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "hooks.json")
	want := codexHookGroup(o.Binary)
	changed, err := editCodexHooks(path, false, func(groups []json.RawMessage) ([]json.RawMessage, bool) {
		// Every copy of this binary's group goes; other groups stay.
		result := []json.RawMessage{}
		for _, g := range groups {
			if !sameJSON(g, want) {
				result = append(result, g)
			}
		}
		return result, len(result) != len(groups)
	})
	if err != nil {
		return fmt.Errorf("codex hooks: %w", err)
	}
	if changed {
		report.Changed = append(report.Changed, "Codex SessionStart hook in "+path)
	} else {
		report.Unchanged = append(report.Unchanged, "Codex SessionStart hook in "+path+" (absent, or not the one setup wrote)")
	}
	return nil
}
