package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Codex turns a skill on or off only through skills.config entries, selected by name or by
// path (P9 in chunks/04-codex-cycle.md). codexActivation reads every configuration layer of
// the host as TOML, so quoted and escaped keys keep their meaning, and refuses an entry that
// can turn off one of the verified pickup files. Other entries, and entries that turn a skill
// on, are harmless.

// codexConfigMax bounds one configuration layer.
const codexConfigMax = 1 << 20

// codexLayer is one parsed configuration layer and the directory that its relative paths
// start from ("" for a command-line value, whose base is unknown).
type codexLayer struct {
	doc  map[string]any
	base string
}

// codexActivation checks the effective skills.config of the host's configuration, layer by
// layer in Codex's order from lowest to highest: /etc/codex/config.toml, $CODEX_HOME/config.toml,
// the .codex/config.toml of each directory from the checkout root down to the session's
// directory (dirs lists them upward), the host's own -c/--config arguments, and
// /etc/codex/managed_config.toml. Tables merge and any other value of a higher layer replaces
// the lower one, so the effective entries are those of the highest layer that sets them.
// /etc/codex/requirements.toml constrains rather than sets values: a disabling entry there
// refuses as well.
func codexActivation(t CycleTarget, codex string, dirs []string, files []string) error {
	read := func(path string) (*codexLayer, error) {
		data, err := readBounded(path, codexConfigMax)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, errors.New("pickup_entry_unreadable")
		}
		doc := map[string]any{}
		if _, err := toml.Decode(string(data), &doc); err != nil {
			return nil, errors.New("pickup_entry_unreadable")
		}
		return &codexLayer{doc, filepath.Dir(path)}, nil
	}
	paths := []string{"/etc/codex/config.toml", filepath.Join(codex, "config.toml")}
	for i := len(dirs) - 1; i >= 0; i-- {
		paths = append(paths, filepath.Join(dirs[i], ".codex", "config.toml"))
	}
	var layers []codexLayer
	for _, path := range paths {
		layer, err := read(path)
		if err != nil {
			return err
		}
		if layer != nil {
			layers = append(layers, *layer)
		}
	}
	_, args, err := t.tools.processCommand(int(t.HostPID))
	if err != nil {
		return errors.New("pickup_entry_unverified")
	}
	values, profile := codexArguments(args)
	for _, value := range values {
		doc, err := codexOverride(value)
		if err != nil {
			return errors.New("pickup_entry_unreadable")
		}
		layers = append(layers, codexLayer{doc, ""})
	}
	if layer, err := read("/etc/codex/managed_config.toml"); err != nil {
		return err
	} else if layer != nil {
		layers = append(layers, *layer)
	}
	// The active profile: the -p argument, else the effective profile key. A key that is not a
	// name leaves it unknown ("*"): then every profile counts.
	if profile == "" {
		if value, _ := codexEffective(layers, "profile"); value != nil {
			if name, ok := value.(string); ok {
				profile = name
			} else {
				profile = "*"
			}
		}
	}
	check := func(keys ...string) error {
		value, base := codexEffective(layers, keys...)
		return codexEntries(value, base, files)
	}
	if err := check("skills", "config"); err != nil {
		return err
	}
	if profile != "" {
		names := []string{profile}
		if profile == "*" {
			names = nil
			for _, layer := range layers {
				profiles, _ := layer.doc["profiles"].(map[string]any)
				for name := range profiles {
					names = append(names, name)
				}
			}
		}
		for _, name := range names {
			if err := check("profiles", name, "skills", "config"); err != nil {
				return err
			}
		}
	}
	requirements, err := read("/etc/codex/requirements.toml")
	if err != nil {
		return err
	}
	if requirements != nil {
		return codexEntries(codexPath(requirements.doc, "skills", "config"), requirements.base, files)
	}
	return nil
}

// codexEffective returns the effective value at a key path over the layers, lowest first, and
// the directory of the layer that set it: a layer that has a table on the path passes on to the
// next key, and one that sets the last key, or another value on the path, replaces the value.
func codexEffective(layers []codexLayer, keys ...string) (value any, base string) {
	for _, layer := range layers {
		var current any = layer.doc
		for i, key := range keys {
			table, ok := current.(map[string]any)
			if !ok {
				break
			}
			next, found := table[key]
			if !found {
				break
			}
			if i == len(keys)-1 {
				value, base = next, layer.base
				break
			}
			if _, isTable := next.(map[string]any); !isTable {
				// A value in place of a table on the path: its native meaning is not proven,
				// so it is the effective value, which no entry check accepts.
				value, base = next, layer.base
				break
			}
			current = next
		}
	}
	return value, base
}

// codexArguments returns the values of the -c/--config arguments and the -p/--profile name.
func codexArguments(args []string) (values []string, profile string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case arg == "-c" || arg == "--config":
			values = append(values, next())
		case strings.HasPrefix(arg, "--config="):
			values = append(values, strings.TrimPrefix(arg, "--config="))
		case strings.HasPrefix(arg, "-c") && len(arg) > 2:
			values = append(values, strings.TrimPrefix(strings.TrimPrefix(arg, "-c"), "="))
		case arg == "-p" || arg == "--profile":
			profile = next()
		case strings.HasPrefix(arg, "--profile="):
			profile = strings.TrimPrefix(arg, "--profile=")
		}
	}
	return values, profile
}

// codexOverride parses one -c KEY=VALUE as the TOML document KEY = VALUE. A value that is not
// TOML is a string, as in Codex.
func codexOverride(value string) (map[string]any, error) {
	key, raw, ok := strings.Cut(value, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return nil, errors.New("override without a key")
	}
	doc := map[string]any{}
	if _, err := toml.Decode(key+" = "+raw, &doc); err == nil {
		return doc, nil
	}
	doc = map[string]any{}
	if _, err := toml.Decode(key+" = ''", &doc); err != nil {
		return nil, err
	}
	codexSetLeaf(doc, raw)
	return doc, nil
}

// codexSetLeaf replaces the one leaf of a single-key document with a string.
func codexSetLeaf(doc map[string]any, value string) {
	for key, v := range doc {
		if inner, ok := v.(map[string]any); ok {
			codexSetLeaf(inner, value)
		} else {
			doc[key] = value
		}
	}
}

// codexPath returns the value at a key path of a document, or nil.
func codexPath(doc map[string]any, keys ...string) any {
	var value any = doc
	for _, key := range keys {
		table, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = table[key]
	}
	return value
}

// codexEntries checks one skills.config value: an array of tables, each selecting a skill by
// name or by path. An entry whose enabled is not true and whose selector can name a verified
// file refuses; Codex ignores an entry with both or neither selector.
func codexEntries(value any, base string, files []string) error {
	if value == nil {
		return nil
	}
	entries, ok := value.([]map[string]any)
	if !ok {
		list, isList := value.([]any)
		if !isList {
			return errors.New("pickup_entry_unverified")
		}
		for _, item := range list {
			entry, ok := item.(map[string]any)
			if !ok {
				return errors.New("pickup_entry_unverified")
			}
			entries = append(entries, entry)
		}
	}
	for _, entry := range entries {
		if enabled, ok := entry["enabled"].(bool); ok && enabled {
			continue
		}
		name, hasName := entry["name"]
		path, hasPath := entry["path"]
		if hasName == hasPath {
			continue
		}
		if hasName {
			text, ok := name.(string)
			if !ok {
				return errors.New("pickup_entry_unverified")
			}
			if parts := strings.Split(text, ":"); parts[len(parts)-1] == "pickup" {
				return errors.New("pickup_entry_unverified")
			}
			continue
		}
		text, ok := path.(string)
		if !ok || !codexPathOutside(text, base, files) {
			return errors.New("pickup_entry_unverified")
		}
	}
	return nil
}

// codexPathOutside reports whether a path selector resolves to a file that is neither a
// verified pickup file nor its directory. A relative path without a known base, or a path that
// does not resolve, may name one.
func codexPathOutside(path, base string, files []string) bool {
	if !filepath.IsAbs(path) {
		if base == "" {
			return false
		}
		path = filepath.Join(base, path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, file := range files {
		if real == file || real == filepath.Dir(file) {
			return false
		}
	}
	return true
}
