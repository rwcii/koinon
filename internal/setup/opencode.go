package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpenCode keeps its MCP servers in its global configuration directory, and the two major
// versions differ (#692). OpenCode 1 (checked with 1.18.35) writes `mcp.NAME` there with
// `opencode mcp add NAME -- COMMAND`. OpenCode 2 (checked with 2.3.0) writes
// `mcp.servers.NAME`, and only with `--global`; without it, it writes a project file in the
// working directory. OpenCode 2's `mcp list` prints no command, so setup reads the files.

// openCodeFiles are the global configuration files that each OpenCode version loads, in its
// order. OpenCode 1 merges an entry's fields from config.json, opencode.json and
// opencode.jsonc; OpenCode 2 loads opencode.json and opencode.jsonc, and a later server object
// replaces an earlier one whole (2.3.0 packages/core/src/config/discovery.ts).
func openCodeFiles(global bool) []string {
	if global {
		return []string{"opencode.json", "opencode.jsonc"}
	}
	return []string{"config.json", "opencode.json", "opencode.jsonc"}
}

// openCodeGlobal reports whether this OpenCode CLI's `mcp add` takes --global (OpenCode 2).
func openCodeGlobal(ctx context.Context, cli string) (bool, error) {
	out, err := readCLI(ctx, 250*time.Millisecond, cli, "mcp", "add", "--help")
	if err != nil {
		return false, fmt.Errorf("%s mcp add --help: %v: %s", cli, err, strings.TrimSpace(out))
	}
	return strings.Contains(out, "--global"), nil
}

// openCodeAdd is the command that writes the koinon server into the global configuration.
func openCodeAdd(global bool, binary string) []string {
	add := []string{"mcp", "add"}
	if global {
		add = append(add, "--global")
	}
	return append(add, serverName, "--", binary, "mcp")
}

// openCodeConfigured reports whether the global configuration in root runs the server named
// koinon as exactly `binary mcp`, enabled, in the shape that this OpenCode version reads. With
// enabled false, a disabled entry also counts.
func openCodeConfigured(root string, global bool, binary string, enabled bool) (bool, error) {
	entry := map[string]any{}
	for _, name := range openCodeFiles(global) {
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		var config struct {
			MCP map[string]json.RawMessage `json:"mcp"`
		}
		if err := json.Unmarshal(plainJSON(data), &config); err != nil {
			return false, fmt.Errorf("%s: %v", filepath.Join(root, name), err)
		}
		servers := config.MCP
		if global {
			servers = nil
			if raw, ok := config.MCP["servers"]; ok {
				if err := json.Unmarshal(raw, &servers); err != nil {
					return false, fmt.Errorf("%s: mcp.servers: %v", filepath.Join(root, name), err)
				}
			}
		}
		raw, ok := servers[serverName]
		if !ok {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			return false, fmt.Errorf("%s: mcp %s: %v", filepath.Join(root, name), serverName, err)
		}
		if global {
			entry = map[string]any{}
		}
		for key, value := range fields {
			entry[key] = value
		}
	}
	if len(entry) == 0 {
		return false, nil
	}
	command, _ := entry["command"].([]any)
	if entry["type"] != "local" || len(command) != 2 || command[0] != binary || command[1] != "mcp" {
		return false, nil
	}
	off := entry["enabled"] == false
	if global {
		off = entry["disabled"] == true
	}
	return !off || !enabled, nil
}

// openCodeServer adds the koinon server to OpenCode's global configuration unless it is there.
// The CLI runs in the configuration directory, so no project file is written elsewhere, and
// the entry is read back after the add.
func openCodeServer(ctx context.Context, o Options, cli string, report *Report) error {
	root, err := openCodeRoot(o)
	if err != nil {
		return err
	}
	global, err := openCodeGlobal(ctx, cli)
	if err != nil {
		return err
	}
	if ok, err := openCodeConfigured(root, global, o.Binary, true); err != nil {
		return err
	} else if ok {
		report.Unchanged = append(report.Unchanged, "mcp server "+serverName)
		return nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	// The CLI writes the chosen root: OpenCode 2 takes it as OPENCODE_CONFIG_DIR (2.3.0
	// packages/util/src/global.ts); OpenCode 1 uses $XDG_CONFIG_HOME/opencode.
	env := append(os.Environ(), "OPENCODE_CONFIG_DIR="+root)
	if !global {
		if filepath.Base(root) != "opencode" {
			return fmt.Errorf("OpenCode 1 reads its global configuration from a directory named opencode; %s is not one", root)
		}
		env = append(os.Environ(), "XDG_CONFIG_HOME="+filepath.Dir(root))
	}
	if out, err := runCLIEnv(ctx, root, env, cli, openCodeAdd(global, o.Binary)...); err != nil {
		return fmt.Errorf("opencode mcp add failed: %s", strings.TrimSpace(out))
	}
	if ok, err := openCodeConfigured(root, global, o.Binary, true); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("opencode mcp add finished, but %s has no enabled koinon server running %s mcp; "+
			"an entry in a later file can override it", root, o.Binary)
	}
	report.Changed = append(report.Changed, "mcp server "+serverName)
	return nil
}

// plainJSON removes the comments and trailing commas that OpenCode accepts in its JSONC
// files, outside strings, so that encoding/json can read them.
func plainJSON(data []byte) []byte {
	return scanJSON(scanJSON(data, true), false)
}

// scanJSON copies data outside strings without comments (comments true), or without the
// commas that only whitespace separates from a closing bracket (comments false).
func scanJSON(data []byte, comments bool) []byte {
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case c == '"':
			j := i + 1
			for ; j < len(data) && data[j] != '"'; j++ {
				if data[j] == '\\' {
					j++
				}
			}
			j = min(j, len(data)-1)
			out = append(out, data[i:j+1]...)
			i = j
		case comments && c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i+1 < len(data) && data[i+1] != '\n' {
				i++
			}
		case comments && c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			i += end + 3
		case !comments && c == ',':
			j := i + 1
			for j < len(data) && bytes.IndexByte([]byte(" \t\r\n"), data[j]) >= 0 {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}
