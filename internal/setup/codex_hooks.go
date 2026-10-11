package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// koinonGroup reports whether a SessionStart group is one that setup wrote, for any binary.
func koinonGroup(group json.RawMessage) bool {
	var g struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	return json.Unmarshal(group, &g) == nil && len(g.Hooks) == 1 && g.Hooks[0].Type == "command" &&
		strings.HasSuffix(g.Hooks[0].Command, codexHookCommand)
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
		for i, g := range groups {
			if sameJSON(g, want) {
				return groups, false
			}
			if koinonGroup(g) {
				// A group that runs another koinon binary is replaced.
				groups[i] = want
				return groups, true
			}
		}
		return append(groups, want), true
	})
	if err != nil {
		return fmt.Errorf("codex hooks: %w", err)
	}
	entry := "Codex SessionStart hook in " + path
	if changed {
		report.Changed = append(report.Changed, entry)
		report.Note = "Codex asks to review the new SessionStart hook at its next start; trust it, or the address moves only at the first Koinon tool call after /clear."
	} else {
		report.Unchanged = append(report.Unchanged, entry)
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
		for i, g := range groups {
			if sameJSON(g, want) {
				return append(groups[:i:i], groups[i+1:]...), true
			}
		}
		return groups, false
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
