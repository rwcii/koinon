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
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// Remove takes out what Run added for one family, for `koinon uninstall` (sprint chunk
// 11): the MCP entry only while it still runs this binary's `mcp` command, the agy Stop
// hook and the OpenCode identity plugin only while they are the ones setup wrote, and
// the Claude status line, whose saved original is restored. Anything else is reported
// and left in place. An agent CLI that is not installed is skipped.
func Remove(ctx context.Context, o Options) (Report, error) {
	report := Report{OK: true, Family: o.Family, Changed: []string{}, Unchanged: []string{}}
	switch o.Family {
	case "deepseek":
		report.Note = "DeepSeek setup changes nothing, so nothing was removed."
		return report, nil
	case "claude", "codex", "agy", "opencode":
	default:
		return report, errors.New("unknown agent family")
	}
	var err error
	if o.Binary == "" {
		if o.Binary, err = os.Executable(); err != nil {
			return report, err
		}
	}
	if !filepath.IsAbs(o.Binary) {
		return report, errors.New("the koinon binary path must be absolute")
	}
	if o.Family == "opencode" {
		err = removeOpenCode(o, &report)
		return report, err
	}
	cli := o.CLI
	if cli == "" {
		cli, _ = exec.LookPath(o.Family)
	}
	if cli == "" || !filepath.IsAbs(cli) {
		report.Unchanged = append(report.Unchanged, "mcp server "+serverName+" (the "+o.Family+" CLI is not installed)")
	} else {
		report.CLI = cli
		run := func(args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			var out bytes.Buffer
			cmd := exec.CommandContext(ctx, cli, args...)
			cmd.Stdout, cmd.Stderr = &out, &out
			err := cmd.Run()
			return out.String(), err
		}
		get := map[string][]string{"claude": {"mcp", "get", serverName}, "codex": {"mcp", "get", serverName, "--json"},
			"agy": {"mcp", "list"}}[o.Family]
		remove := map[string][]string{"claude": {"mcp", "remove", "--scope", "user", serverName},
			"codex": {"mcp", "remove", serverName}, "agy": {"mcp", "remove", serverName}}[o.Family]
		current, getErr := run(get...)
		if getErr == nil && configured(o.Family, current, o.Binary) {
			if out, err := run(remove...); err != nil {
				return report, fmt.Errorf("%s mcp remove failed: %s", o.Family, strings.TrimSpace(out))
			}
			report.Changed = append(report.Changed, "mcp server "+serverName)
		} else {
			report.Unchanged = append(report.Unchanged, "mcp server "+serverName+" (absent, or it runs another command)")
		}
	}
	switch o.Family {
	case "claude":
		if o.ClaudeConfig == "" {
			o.ClaudeConfig = claudeConfigDir(os.Getenv)
		}
		if o.StateDir == "" {
			if o.StateDir, err = platform.DefaultStateDir(); err != nil {
				return report, err
			}
		}
		result, err := ClaudeStatusLine(o.Binary, o.StateDir, o.ClaudeConfig, true)
		report.StatusLine = &result
		return report, err
	case "agy":
		return report, removeAgyHook(o, &report)
	}
	return report, nil
}

func removeAgyHook(o Options, report *Report) error {
	root := o.AgyRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		root = filepath.Join(home, ".gemini", "config")
	}
	want, err := json.Marshal(map[string]any{"Stop": []map[string]any{{
		"type": "command", "command": quote(o.Binary) + " hook agy-stop", "timeout": 10,
	}}})
	if err != nil {
		return err
	}
	path := filepath.Join(root, "hooks.json")
	changed, err := editObject(path, func(members []member) ([]member, bool) {
		for i, m := range members {
			if m.key == serverName && sameJSON(m.value, want) {
				return append(members[:i:i], members[i+1:]...), true
			}
		}
		return members, false
	})
	if err != nil {
		return fmt.Errorf("agy hooks: %w", err)
	}
	if changed {
		report.Changed = append(report.Changed, "agy Stop hook in "+path)
	} else {
		report.Unchanged = append(report.Unchanged, "agy Stop hook in "+path+" (absent, or not the one setup wrote)")
	}
	return nil
}

func openCodeRoot(o Options) (string, error) {
	if o.Opencode != "" {
		return o.Opencode, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opencode"), nil
}

// removeOpenCode removes the mcp.koinon entry that `opencode mcp add` wrote, which has
// no remove command, from the global configuration file, and the identity plugin.
func removeOpenCode(o Options, report *Report) error {
	root, err := openCodeRoot(o)
	if err != nil {
		return err
	}
	want, _ := json.Marshal(map[string]any{"type": "local", "command": []string{o.Binary, "mcp"}})
	removed := false
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		path := filepath.Join(root, name)
		changed, err := editObject(path, func(members []member) ([]member, bool) {
			for i, m := range members {
				if m.key != "mcp" {
					continue
				}
				servers, ok := decodeMembers(m.value)
				if !ok {
					return members, false
				}
				for j, s := range servers {
					var entry map[string]any
					json.Unmarshal(s.value, &entry)
					delete(entry, "enabled")
					if s.key == serverName && sameJSON(mustJSON(entry), want) {
						servers = append(servers[:j:j], servers[j+1:]...)
						if len(servers) == 0 {
							return append(members[:i:i], members[i+1:]...), true
						}
						members[i].value = encodeMembers(servers, "  ")
						return members, true
					}
				}
			}
			return members, false
		})
		if err != nil {
			report.Unchanged = append(report.Unchanged, "OpenCode mcp entry in "+path+" ("+err.Error()+"; remove it by hand)")
			continue
		}
		if changed {
			removed = true
			report.Changed = append(report.Changed, "mcp server "+serverName+" in "+path)
		}
	}
	if !removed {
		report.Unchanged = append(report.Unchanged, "mcp server "+serverName+" (absent, or it runs another command)")
	}
	path := filepath.Join(root, "plugins", "koinon-identity.js")
	current, err := os.ReadFile(path)
	switch {
	case err == nil && string(current) == openCodePluginSource:
		if err := os.Remove(path); err != nil {
			return err
		}
		report.Changed = append(report.Changed, "OpenCode identity plugin "+path)
	case err == nil:
		report.Unchanged = append(report.Unchanged, "OpenCode identity plugin "+path+" (edited; left in place)")
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	return nil
}

func mustJSON(v any) []byte { data, _ := json.Marshal(v); return data }

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return bytes.Equal(mustJSON(x), mustJSON(y))
}

type member struct {
	key   string
	value json.RawMessage
}

// decodeMembers reads a JSON object's members in order.
func decodeMembers(data []byte) ([]member, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	var members []member
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false
		}
		members = append(members, member{token.(string), raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return members, true
}

func encodeMembers(members []member, indent string) json.RawMessage {
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, m := range members {
		name, _ := json.Marshal(m.key)
		var value bytes.Buffer
		if json.Indent(&value, m.value, indent+"  ", "  ") != nil {
			value.Write(m.value)
		}
		fmt.Fprintf(&out, "%s  %s: %s", indent, name, value.Bytes())
		if i < len(members)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString(indent + "}")
	return out.Bytes()
}

// editObject rewrites a JSON object file when edit changes its members, keeping every
// other member and the file mode. An absent file is unchanged; a file that is not plain
// JSON is refused, never rewritten.
func editObject(path string, edit func([]member) ([]member, bool)) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	members, ok := decodeMembers(data)
	if !ok {
		return false, errors.New("not a plain JSON object")
	}
	members, changed := edit(members)
	if !changed {
		return false, nil
	}
	mode := os.FileMode(0600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return true, writeFile(path, append(encodeMembers(members, ""), '\n'), mode)
}
