package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// ClaudePermissions are the recommended Claude Code allow rules of docs/PERMISSIONS.md: the
// koinon server's tools and the read-only commands that the guide tells agents to run.
var ClaudePermissions = []string{
	"mcp__koinon__*",
	"Bash(koinon guide --agent claude)",
	"Bash(koinon context show)",
	"Bash(koinon status)",
}

// claudeCovers names the broader allow rules that also cover a recommended rule.
func claudeCovers(rule string) []string {
	if rule == "mcp__koinon__*" {
		return []string{"mcp__koinon"}
	}
	return []string{"Bash(koinon *)", "Bash(koinon:*)"}
}

// PermissionsResult reports the recommended Claude Code rules in the user settings file.
type PermissionsResult struct {
	SettingsFile string   `json:"settings_file"`
	Outcome      string   `json:"outcome"` // present, missing, added, unreadable
	Missing      []string `json:"missing,omitempty"`
	Added        []string `json:"added,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Next         string   `json:"next,omitempty"`
}

const addPermissions = "koinon setup claude --add-permissions"

// ClaudePermissionRules reads permissions.allow of <configDir>/settings.json and reports
// the recommended rules that it does not hold. With add, it appends them and keeps every
// other setting; without add, it changes nothing.
func ClaudePermissionRules(configDir string, add bool) (PermissionsResult, error) {
	l := statusLine{settings: filepath.Join(configDir, "settings.json")}
	result := PermissionsResult{SettingsFile: l.settings}
	raw, fields, err := l.read()
	var allow []string
	var permissions []field
	if err == nil {
		if value := lookup(fields, "permissions"); value != nil {
			if permissions, err = objectFields(value); err == nil {
				if list := lookup(permissions, "allow"); list != nil {
					err = json.Unmarshal(list, &allow)
				}
			}
			if err != nil {
				err = SettingsError{"settings_invalid", "permissions.allow in the Claude settings file is not a list of rules; not changed", l.settings}
			}
		}
	}
	if err != nil {
		if add {
			return result, err
		}
		var refused SettingsError
		result.Outcome, result.Reason = "unreadable", err.Error()
		if errors.As(err, &refused) {
			result.Reason = refused.Code
		}
		return result, nil
	}
	for _, rule := range ClaudePermissions {
		if !slices.Contains(allow, rule) && !slices.ContainsFunc(claudeCovers(rule), func(r string) bool { return slices.Contains(allow, r) }) {
			result.Missing = append(result.Missing, rule)
		}
	}
	switch {
	case len(result.Missing) == 0:
		result.Outcome = "present"
		return result, nil
	case !add:
		result.Outcome, result.Next = "missing", addPermissions
		return result, nil
	}
	list, err := json.Marshal(append(allow, result.Missing...))
	if err != nil {
		return result, err
	}
	value := encodeFields(replace(permissions, "allow", list))
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return result, err
	}
	if err := l.write(raw, replace(fields, "permissions", value)); err != nil {
		return result, err
	}
	result.Outcome, result.Added, result.Missing = "added", result.Missing, nil
	return result, nil
}
