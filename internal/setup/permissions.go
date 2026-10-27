package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// The recommended server-wide permission rules of docs/PERMISSIONS.md for each family: the
// koinon server's tools and the read-only commands that the guide tells agents to run.
var (
	ClaudePermissions = []string{
		"mcp__koinon__*",
		"Bash(koinon guide --agent claude)",
		"Bash(koinon context show)",
		"Bash(koinon status)",
	}
	AgyPermissions = []string{"mcp(koinon/*)", "command(koinon guide --agent agy)"}
	// CodexPermissions is the key that setup sets in the [mcp_servers.koinon] table.
	CodexPermissions = []string{`default_tools_approval_mode = "approve"`}
	// OpenCodePermissions are members of the permission object: a key, or bash.KEY.
	OpenCodePermissions = []string{`"koinon_*": "allow"`, `"bash": {"koinon guide --agent opencode": "allow"}`}
)

// GrokPermissionNote is the launch rule that Grok takes instead of a settings file.
const GrokPermissionNote = "Grok takes its rule at launch: koinon grok --allow 'MCPTool(koinon__*)'. No file was changed."

// PermissionsResult reports one family's recommended permission rules in its settings file.
type PermissionsResult struct {
	Family       string `json:"family"`
	SettingsFile string `json:"settings_file,omitempty"`
	// Outcome is present, missing, added, different, unreadable or manual.
	Outcome string   `json:"outcome"`
	Missing []string `json:"missing,omitempty"`
	Added   []string `json:"added,omitempty"`
	// Different lists rules whose key the file sets to another value; they are never changed.
	Different []string `json:"different,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	Note      string   `json:"note,omitempty"`
}

const missingNote = "Run koinon install in a terminal to add them, or merge them by hand (docs/PERMISSIONS.md)."

// Permissions reports the family's recommended rules that its settings file lacks. With add,
// it adds them and keeps every other setting; without add, it changes nothing. A file that
// is unsafe or not in the expected form is reported, and add refuses it.
func Permissions(o Options, add bool) (PermissionsResult, error) {
	result := PermissionsResult{Family: o.Family}
	var err error
	switch o.Family {
	case "claude":
		dir := o.ClaudeConfig
		if dir == "" {
			dir = claudeConfigDir(os.Getenv)
		}
		err = allowList(&result, statusLine{settings: filepath.Join(dir, "settings.json")}, ClaudePermissions, func(rule string) []string {
			if rule == "mcp__koinon__*" {
				return []string{"mcp__koinon"}
			}
			return []string{"Bash(koinon *)", "Bash(koinon:*)"}
		}, add)
	case "agy":
		err = allowList(&result, statusLine{settings: agySettingsFile(o), label: "Antigravity settings"}, AgyPermissions, func(rule string) []string {
			if strings.HasPrefix(rule, "command(") {
				return []string{"command(koinon)"}
			}
			return nil
		}, add)
	case "codex":
		var root string
		if root, err = codexHome(o); err == nil {
			err = codexApproval(&result, filepath.Join(root, "config.toml"), add)
		}
	case "opencode":
		var root string
		if root, err = openCodeRoot(o); err == nil {
			err = openCodePermission(&result, root, add)
		}
	case "grok":
		result.Outcome, result.Note = "manual", GrokPermissionNote
	case "deepseek":
		result.Outcome, result.Note = "manual", "Approve the exact commands of koinon guide --agent deepseek in the session's own approval flow."
	default:
		return result, errors.New("unknown agent family")
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
		result.Missing = nil
		return result, nil
	}
	if result.Outcome == "missing" {
		result.Note = missingNote
	}
	return result, nil
}

// conclude sets the outcome after the missing and different rules are known, or, with add
// and no different rule, writes the missing rules through write and records them as added.
func conclude(result *PermissionsResult, add bool, write func() error) error {
	switch {
	case len(result.Different) > 0:
		// A rule the person set otherwise: the file is left as it is (#449).
		result.Outcome = "different"
		return nil
	case len(result.Missing) == 0:
		result.Outcome = "present"
		return nil
	case !add:
		result.Outcome = "missing"
		return nil
	}
	if err := write(); err != nil {
		return err
	}
	result.Outcome, result.Added, result.Missing = "added", result.Missing, nil
	return nil
}

// allowList handles a JSON settings file whose permissions.allow is a list of rules, as
// Claude Code and Antigravity keep them. covers names broader rules that also cover a rule.
func allowList(result *PermissionsResult, l statusLine, rules []string, covers func(string) []string, add bool) error {
	result.SettingsFile = l.settings
	raw, fields, err := l.read()
	if err != nil {
		return err
	}
	var allow []string
	var items []json.RawMessage // the existing rules, kept as written
	var permissions []field
	if value := lookup(fields, "permissions"); value != nil {
		if permissions, err = objectFields(value); err == nil {
			if list := lookup(permissions, "allow"); list != nil {
				err = json.Unmarshal(list, &items)
			}
			// Every rule must be a string; a null would decode as "" and be rewritten.
			for _, item := range items {
				var rule string
				if err != nil || !bytes.HasPrefix(bytes.TrimSpace(item), []byte(`"`)) || json.Unmarshal(item, &rule) != nil {
					err = errors.New("not a rule")
					break
				}
				allow = append(allow, rule)
			}
		}
		if err != nil {
			return SettingsError{"settings_invalid", "permissions.allow in the " + l.name() + " file is not a list of rules; not changed", l.settings}
		}
	}
	for _, rule := range rules {
		if !slices.Contains(allow, rule) && !slices.ContainsFunc(covers(rule), func(r string) bool { return slices.Contains(allow, r) }) {
			result.Missing = append(result.Missing, rule)
		}
	}
	return conclude(result, add, func() error {
		for _, rule := range result.Missing {
			item, err := json.Marshal(rule)
			if err != nil {
				return err
			}
			items = append(items, item)
		}
		list, err := json.Marshal(items)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(l.settings), 0700); err != nil {
			return err
		}
		return l.write(raw, replace(fields, "permissions", encodeFields(replace(permissions, "allow", list))))
	})
}

// codexApproval sets default_tools_approval_mode in the [mcp_servers.koinon] table that
// codex mcp add wrote, by inserting one line at the end of that table; every other line is
// kept as written. The file is parsed before and after: the result must parse and differ only
// in that key.
func codexApproval(result *PermissionsResult, path string, add bool) error {
	l := statusLine{settings: path, label: "Codex configuration"}
	result.SettingsFile = path
	raw, err := l.readRaw()
	if err != nil {
		return err
	}
	before, err := codexServer(raw)
	if err != nil {
		return SettingsError{"settings_invalid", "the Codex configuration is not valid TOML; not changed", path}
	}
	if before == nil {
		return SettingsError{"settings_no_server", "the Codex configuration has no mcp_servers.koinon entry; run setup first", path}
	}
	switch value := before["default_tools_approval_mode"]; value {
	case "approve":
	case nil:
		result.Missing = CodexPermissions
	default:
		result.Different = []string{fmt.Sprintf("default_tools_approval_mode = %q", fmt.Sprint(value))}
	}
	return conclude(result, add, func() error {
		unusual := SettingsError{"settings_unusual", "the Codex configuration does not keep mcp_servers.koinon as a [mcp_servers.koinon] table; not changed", path}
		lines := strings.SplitAfter(string(raw), "\n")
		start, end := -1, len(lines)
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "[") {
				continue
			}
			if start >= 0 && end == len(lines) {
				end = i
			}
			header, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "["), "]")
			if !strings.HasPrefix(trimmed, "[[") && plainKey(header) == "mcp_servers.koinon" {
				start = i
			}
		}
		if start < 0 {
			return unusual
		}
		at := start + 1
		for i := start + 1; i < end; i++ {
			if trimmed := strings.TrimSpace(lines[i]); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				at = i + 1
			}
		}
		if !strings.HasSuffix(lines[at-1], "\n") {
			lines[at-1] += "\n"
		}
		lines = slices.Insert(lines, at, CodexPermissions[0]+"\n")
		data := []byte(strings.Join(lines, ""))
		after, err := codexServer(data)
		if err != nil || after == nil || after["default_tools_approval_mode"] != "approve" {
			return unusual
		}
		delete(after, "default_tools_approval_mode")
		if !reflect.DeepEqual(after, before) {
			return unusual
		}
		return l.writeRaw(raw, data)
	})
}

// codexServer parses a Codex configuration and returns its mcp_servers.koinon table, or nil.
func codexServer(raw []byte) (map[string]any, error) {
	var config map[string]any
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, err
	}
	servers, _ := config["mcp_servers"].(map[string]any)
	server, _ := servers["koinon"].(map[string]any)
	return server, nil
}

// plainKey removes the quotes and spaces of a TOML key.
func plainKey(key string) string {
	return strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "").Replace(key)
}

// openCodePermission adds "koinon_*": "allow" and the guide command's bash rule to the
// permission object of opencode.json. OpenCode applies the last matching rule, so the rules
// are appended. A permission or bash value that is a single action is kept: "allow" covers
// the rule, another action is reported as different and nothing is added.
func openCodePermission(result *PermissionsResult, root string, add bool) error {
	l := statusLine{settings: filepath.Join(root, "opencode.json"), label: "OpenCode configuration"}
	result.SettingsFile = l.settings
	if _, err := os.Lstat(filepath.Join(root, "opencode.jsonc")); err == nil {
		result.SettingsFile = filepath.Join(root, "opencode.jsonc")
		return SettingsError{"settings_jsonc", "OpenCode uses opencode.jsonc; merge the rules by hand", result.SettingsFile}
	}
	raw, fields, err := l.read()
	if err != nil {
		return err
	}
	invalid := SettingsError{"settings_invalid", "permission in the OpenCode configuration is not an object of actions; not changed", l.settings}
	action := func(value json.RawMessage) (string, bool) {
		var s string
		return s, value != nil && json.Unmarshal(value, &s) == nil
	}
	permission := lookup(fields, "permission")
	var members, bash []field
	if permission != nil {
		if s, ok := action(permission); ok {
			if s != "allow" {
				result.Different = []string{`"permission": "` + s + `"`}
			}
			return conclude(result, add, nil)
		}
		if members, err = objectFields(permission); err != nil {
			return invalid
		}
	}
	switch s, ok := action(lookup(members, "koinon_*")); {
	case !ok && lookup(members, "koinon_*") != nil:
		return invalid
	case !ok:
		result.Missing = append(result.Missing, OpenCodePermissions[0])
	case s != "allow":
		result.Different = append(result.Different, `"koinon_*": "`+s+`"`)
	}
	guide := "koinon guide --agent opencode"
	if value := lookup(members, "bash"); value != nil {
		if s, ok := action(value); ok {
			if s != "allow" {
				result.Different = append(result.Different, `"bash": "`+s+`"`)
			}
		} else if bash, err = objectFields(value); err != nil {
			return invalid
		} else if s, ok := action(lookup(bash, guide)); !ok && lookup(bash, guide) != nil {
			return invalid
		} else if !ok {
			result.Missing = append(result.Missing, OpenCodePermissions[1])
		} else if s != "allow" {
			result.Different = append(result.Different, fmt.Sprintf(`"bash": {%q: %q}`, guide, s))
		}
	} else {
		result.Missing = append(result.Missing, OpenCodePermissions[1])
		bash = []field{}
	}
	return conclude(result, add, func() error {
		for _, rule := range result.Missing {
			if rule == OpenCodePermissions[0] {
				members = replace(members, "koinon_*", json.RawMessage(`"allow"`))
			} else {
				bash = replace(bash, guide, json.RawMessage(`"allow"`))
				members = replace(members, "bash", encodeFields(bash))
			}
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		return l.write(raw, replace(fields, "permission", encodeFields(members)))
	})
}

// Inspection is one family's state on this machine, read without a change.
type Inspection struct {
	Family      string             `json:"family"`
	CLI         string             `json:"cli,omitempty"`
	Configured  bool               `json:"configured"`
	Permissions *PermissionsResult `json:"permissions,omitempty"`
}

// Inspect finds the family's CLI on PATH (or o.CLI), whether its koinon server runs exactly
// o.Binary, and its permission rules. It changes nothing.
func Inspect(ctx context.Context, o Options) Inspection {
	in := Inspection{Family: o.Family}
	cli := o.CLI
	if cli == "" {
		cli, _ = exec.LookPath(o.Family)
	}
	if cli == "" || !filepath.IsAbs(cli) {
		return in
	}
	in.CLI = cli
	if out, err := runCLI(ctx, cli, getCommand(o.Family)...); err == nil {
		in.Configured = configured(o.Family, out, o.Binary)
	}
	if result, err := Permissions(o, false); err == nil {
		in.Permissions = &result
	}
	return in
}

// Offer shows the family's missing rules on tell and adds them when the person accepts at
// ask; for another outcome it only tells why nothing was asked. permissions is Permissions,
// or a test's stand-in.
func Offer(o Options, permissions func(Options, bool) (PermissionsResult, error), ask func(string) (bool, error), tell io.Writer) (PermissionsResult, error) {
	result, err := permissions(o, false)
	if err != nil {
		return result, err
	}
	switch result.Outcome {
	case "missing":
		fmt.Fprintf(tell, "%s: these permission rules let its sessions use Koinon without a prompt for each call:\n", o.Family)
		for _, rule := range result.Missing {
			fmt.Fprintln(tell, "  "+rule)
		}
		yes, err := ask("Add them to " + result.SettingsFile + "?")
		if err != nil || !yes {
			return result, err
		}
		if result, err = permissions(o, true); err != nil {
			var refused SettingsError
			result.Outcome, result.Reason = "unreadable", err.Error()
			if errors.As(err, &refused) {
				result.Reason = refused.Code
			}
			fmt.Fprintf(tell, "%s: not changed: %v\n", o.Family, err)
		}
	case "manual":
		fmt.Fprintf(tell, "%s: %s\n", o.Family, result.Note)
	case "unreadable":
		fmt.Fprintf(tell, "%s: permission rules not read (%s): %s\n", o.Family, result.Reason, result.SettingsFile)
	case "different":
		fmt.Fprintf(tell, "%s: kept your setting %s in %s\n", o.Family, strings.Join(result.Different, ", "), result.SettingsFile)
	}
	return result, nil
}
