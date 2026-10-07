// Package setup adds the Koinon MCP server to each agent family's configuration through
// the agent's own command, and prints each family's guidance.
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
)

// Options select the agent family and, for tests and nonstandard installations, the
// agent CLI, this binary and the configuration roots that setup writes.
type Options struct {
	Family   string
	CLI      string // absolute agent CLI; default: the CLI on PATH, reported
	Binary   string // absolute koinon binary; default: this executable
	AgyRoot  string // agy global customization root; default ~/.gemini/config
	Opencode string // OpenCode global configuration directory; default $XDG_CONFIG_HOME/opencode
}

// Report lists what setup changed and what it found already in place.
type Report struct {
	OK        bool     `json:"ok"`
	Family    string   `json:"family"`
	CLI       string   `json:"cli,omitempty"`
	Changed   []string `json:"changed"`
	Unchanged []string `json:"unchanged"`
	Note      string   `json:"note,omitempty"`
}

const serverName = "koinon"

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'" }

// Run configures one family and returns its report.
func Run(ctx context.Context, o Options) (Report, error) {
	report := Report{OK: true, Family: o.Family, Changed: []string{}, Unchanged: []string{}}
	if o.Family == "deepseek" {
		// Spike fact 2: DeepSeek MCP support is unverified, so it keeps the command path.
		report.Note = "DeepSeek uses the documented command path: koinon send --as deepseek:$DSH_SESSION_ID NAME BODY, koinon inbox and koinon ack with the same --as. Nothing was changed."
		return report, nil
	}
	if o.Family != "claude" && o.Family != "codex" && o.Family != "agy" && o.Family != "opencode" {
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
	cli := o.CLI
	if cli == "" {
		if cli, err = exec.LookPath(o.Family); err != nil {
			return report, fmt.Errorf("%s is not installed; pass --cli with its absolute path", o.Family)
		}
	}
	if !filepath.IsAbs(cli) {
		return report, errors.New("the agent CLI path must be absolute")
	}
	report.CLI = cli
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		var out bytes.Buffer
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Stdout, cmd.Stderr = &out, &out
		cmd.Stdin = nil
		err := cmd.Run()
		return out.String(), err
	}
	// Each family's command to read the entry, and to add it. A present entry that
	// already runs this binary's `mcp` command is left alone.
	var get, add, remove []string
	switch o.Family {
	case "claude":
		get = []string{"mcp", "get", serverName}
		remove = []string{"mcp", "remove", serverName}
		add = []string{"mcp", "add", "--scope", "user", serverName, "--", o.Binary, "mcp"}
	case "codex":
		get = []string{"mcp", "get", serverName, "--json"}
		remove = []string{"mcp", "remove", serverName}
		add = []string{"mcp", "add", serverName, "--", o.Binary, "mcp"}
	case "agy":
		get = []string{"mcp", "list"}
		add = []string{"mcp", "add", serverName, "--", o.Binary, "mcp"}
	case "opencode":
		get = []string{"mcp", "list"}
		add = []string{"mcp", "add", serverName, "--", o.Binary, "mcp"}
	}
	current, getErr := run(get...)
	if getErr == nil && strings.Contains(current, o.Binary) {
		report.Unchanged = append(report.Unchanged, "mcp server "+serverName)
	} else {
		if remove != nil && getErr == nil {
			// Replace an entry that names another command; nothing else is touched.
			if out, err := run(remove...); err != nil {
				return report, fmt.Errorf("%s mcp remove failed: %s", o.Family, strings.TrimSpace(out))
			}
		}
		if out, err := run(add...); err != nil {
			return report, fmt.Errorf("%s mcp add failed: %s", o.Family, strings.TrimSpace(out))
		}
		report.Changed = append(report.Changed, "mcp server "+serverName)
	}
	switch o.Family {
	case "agy":
		err = agyHook(o, &report)
	case "opencode":
		err = openCodePlugin(o, &report)
	}
	return report, err
}

func agyHook(o Options, report *Report) error {
	root := o.AgyRoot
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		root = filepath.Join(home, ".gemini", "config")
	}
	entry, err := json.Marshal(map[string]any{"Stop": []map[string]any{{
		"type": "command", "command": quote(o.Binary) + " hook agy-stop", "timeout": 10,
	}}})
	if err != nil {
		return err
	}
	path := filepath.Join(root, "hooks.json")
	changed, err := mergeKey(path, serverName, entry)
	if err != nil {
		return fmt.Errorf("agy hooks: %w", err)
	}
	if changed {
		report.Changed = append(report.Changed, "agy Stop hook in "+path)
	} else {
		report.Unchanged = append(report.Unchanged, "agy Stop hook in "+path)
	}
	return nil
}

// openCodePluginSource adds the calling session's ID to every Koinon tool call. OpenCode
// passes the same argument object to the hook and to the MCP call (spike fact 6).
const openCodePluginSource = `// Managed by koinon setup opencode. It names the calling session on Koinon tool calls.
export const KoinonIdentity = async () => ({
  "tool.execute.before": async (input, output) => {
    if (input.tool.startsWith("koinon_")) output.args.koinon_session = input.sessionID
  },
})
`

func openCodePlugin(o Options, report *Report) error {
	root := o.Opencode
	if root == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base = filepath.Join(home, ".config")
		}
		root = filepath.Join(base, "opencode")
	}
	path := filepath.Join(root, "plugins", "koinon-identity.js")
	current, err := os.ReadFile(path)
	if err == nil && string(current) == openCodePluginSource {
		report.Unchanged = append(report.Unchanged, "OpenCode identity plugin "+path)
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := writeFile(path, []byte(openCodePluginSource), 0600); err != nil {
		return err
	}
	report.Changed = append(report.Changed, "OpenCode identity plugin "+path)
	return nil
}

// mergeKey sets one top-level key of a JSON object file and keeps every other key, its
// order and its value text. It reports whether the file changed.
func mergeKey(path, key string, value json.RawMessage) (bool, error) {
	data, err := os.ReadFile(path)
	mode := os.FileMode(0600)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte("{}")
	} else if err != nil {
		return false, err
	} else if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if token, err := dec.Token(); err != nil || token != json.Delim('{') {
		return false, errors.New("not a JSON object; left unchanged")
	}
	type member struct {
		key   string
		value json.RawMessage
	}
	var members []member
	found := false
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return false, errors.New("invalid JSON; left unchanged")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return false, errors.New("invalid JSON; left unchanged")
		}
		name := token.(string)
		if name == key {
			var a, b any
			json.Unmarshal(raw, &a)
			json.Unmarshal(value, &b)
			if fmt.Sprint(a) == fmt.Sprint(b) {
				return false, nil
			}
			raw, found = value, true
		}
		members = append(members, member{name, raw})
	}
	if _, err := dec.Token(); err != nil {
		return false, errors.New("invalid JSON; left unchanged")
	}
	if _, err := dec.Token(); err != io.EOF {
		return false, errors.New("trailing data; left unchanged")
	}
	if !found {
		members = append(members, member{key, value})
	}
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, m := range members {
		name, _ := json.Marshal(m.key)
		fmt.Fprintf(&out, "  %s: %s", name, m.value)
		if i < len(members)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("}\n")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	return true, writeFile(path, out.Bytes(), mode)
}

// writeFile replaces a file through a synced temporary file in the same directory.
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".koinon-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), mode); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
