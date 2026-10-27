package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/rwcii/koinon/internal/platform"
)

// The Grok status line (#515) follows the rules of the Claude and agy status lines in Grok's
// config.toml: setup owns only the `type` and `command` keys of the [ui.status_line] table.
// The edit is a text edit of those keys; setup parses the file before and after it and
// refuses an edit whose meaning differs anywhere else. The text editor supports a plain
// [ui.status_line] table only: an inline table, a dotted key or any other shape that it
// cannot prove is refused unchanged. That is a limit of the editor, not a Grok rule.

// grokOwned are the keys of [ui.status_line] that setup sets and removal restores.
var grokOwned = []string{"type", "command"}

// grokConfigFile is Grok's config.toml: in GrokHome, else $GROK_HOME, else ~/.grok.
func grokConfigFile(o Options) string {
	home := o.GrokHome
	if home == "" {
		home = os.Getenv("GROK_HOME")
	}
	if home == "" {
		user, _ := os.UserHomeDir()
		home = filepath.Join(user, ".grok")
	}
	return filepath.Join(home, "config.toml")
}

// GrokStatusLine sets up or removes the grok-status hook in Grok's config.toml. Without an
// earlier command, the hook prints nothing.
func GrokStatusLine(binary, state, config string, remove bool) (StatusLineResult, error) {
	root, err := platform.PrivateDir(state)
	if err != nil {
		return StatusLineResult{}, fmt.Errorf("state directory: %w", err)
	}
	g := grokStatusFile{statusLine{binary: binary, state: root, settings: config, label: "Grok configuration",
		hook: "grok-status", record: "grok-statusline.json", missing: "grok_config_missing"}}
	if remove {
		return g.remove()
	}
	return g.setUp()
}

type grokStatusFile struct{ statusLine }

// isWrapper reports whether an entry runs a Koinon grok-status hook, of this binary or of an
// earlier one: after an upgrade that moves the binary, an edited wrapper is still Koinon's and
// is left alone, never wrapped again.
func (g grokStatusFile) isWrapper(entry json.RawMessage) bool {
	words, err := splitWords(entryCommand(entry))
	return err == nil && len(words) >= 3 && words[1] == "hook" && words[2] == g.hookName()
}

func (g grokStatusFile) unusual() error {
	return SettingsError{"settings_unusual", "the Grok configuration does not keep ui.status_line as a plain [ui.status_line] table that Koinon can edit; not changed", g.settings}
}

// grokConfig is one read of config.toml: its bytes (nil when absent), its meaning, and the
// owned keys of [ui.status_line] as a JSON entry (nil when neither is set).
type grokConfig struct {
	raw   []byte
	value map[string]any
	entry json.RawMessage
	table bool // the [ui.status_line] table exists
}

func (g grokStatusFile) read() (grokConfig, error) {
	raw, err := g.readRaw()
	if raw == nil || err != nil {
		return grokConfig{raw: raw, value: map[string]any{}}, err
	}
	value, err := decodeTOML(raw)
	if err != nil {
		return grokConfig{}, SettingsError{"settings_invalid", "the Grok configuration is not valid TOML; not changed", g.settings}
	}
	c := grokConfig{raw: raw, value: value}
	table, present, ok := statusTable(value)
	if !ok {
		return c, g.unusual()
	}
	c.table = present
	fields := []field{}
	for _, key := range grokOwned {
		v, set := table[key]
		if !set {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return c, g.unusual()
		}
		text, _ := json.Marshal(s)
		fields = append(fields, field{key, text})
	}
	if len(fields) > 0 {
		c.entry = encodeFields(fields)
	}
	return c, nil
}

func decodeTOML(raw []byte) (map[string]any, error) {
	value := map[string]any{}
	_, err := toml.Decode(string(raw), &value)
	return value, err
}

// statusTable returns ui.status_line, whether it exists, and whether both levels are tables.
func statusTable(value map[string]any) (map[string]any, bool, bool) {
	ui, set := value["ui"]
	if !set {
		return nil, false, true
	}
	uiTable, ok := ui.(map[string]any)
	if !ok {
		return nil, false, false
	}
	status, set := uiTable["status_line"]
	if !set {
		return nil, false, true
	}
	table, ok := status.(map[string]any)
	return table, ok, ok
}

// tomlLine is one line of config.toml outside strings, arrays and inline tables: a table
// header, a key, or neither (a blank or comment line).
type tomlLine struct {
	header string // the plain header of a [table] line; "[[" for an array-of-tables header
	key    string // the plain key of a key/value line
}

// tomlLines classifies each line of raw. A line that starts inside a multiline string, an
// array or an inline table is neither a header nor a key. A file that this lexer misreads
// fails the semantic check after the edit.
func tomlLines(raw string) []tomlLine {
	lines := strings.SplitAfter(raw, "\n")
	result := make([]tomlLine, len(lines))
	const (
		none = iota
		basic
		literal
		multiBasic
		multiLiteral
		comment
	)
	state, depth := none, 0
	for n, line := range lines {
		if state == none && depth == 0 {
			result[n] = classify(line)
		}
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch state {
			case none:
				switch {
				case c == '#':
					state = comment
				case strings.HasPrefix(line[i:], `"""`):
					state, i = multiBasic, i+2
				case strings.HasPrefix(line[i:], `'''`):
					state, i = multiLiteral, i+2
				case c == '"':
					state = basic
				case c == '\'':
					state = literal
				case c == '[' || c == '{':
					depth++
				case c == ']' || c == '}':
					depth--
				}
			case basic, multiBasic:
				switch {
				case c == '\\':
					i++
				case state == basic && c == '"', state == multiBasic && strings.HasPrefix(line[i:], `"""`):
					if state == multiBasic {
						i += 2
					}
					state = none
				}
			case literal:
				if c == '\'' {
					state = none
				}
			case multiLiteral:
				if strings.HasPrefix(line[i:], `'''`) {
					state, i = none, i+2
				}
			case comment:
				if c == '\n' {
					state = none
				}
			}
		}
		if state == basic || state == literal || state == comment {
			state = none // a single-line string or a comment ends with its line
		}
	}
	return result
}

func classify(line string) tomlLine {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		return tomlLine{}
	case strings.HasPrefix(trimmed, "[["):
		return tomlLine{header: "[["}
	case strings.HasPrefix(trimmed, "["):
		header, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "["), "]")
		return tomlLine{header: plainKey(header)}
	}
	key, _, _ := strings.Cut(trimmed, "=")
	return tomlLine{key: plainKey(key)}
}

// tomlString is s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// statusLines splits text into lines and finds the plain [ui.status_line] table: the index of
// its header (-1 when absent) and of the first line after it. ok is false for a second header.
func statusLines(text string) (lines []string, kinds []tomlLine, start, end int, ok bool) {
	lines = strings.SplitAfter(text, "\n")
	kinds = tomlLines(text)
	start, end = -1, len(lines)
	for i, k := range kinds {
		if k.header == "" {
			continue
		}
		if start >= 0 && end == len(lines) {
			end = i
		}
		if k.header == "ui.status_line" {
			if start >= 0 {
				return nil, nil, 0, 0, false
			}
			start = i
		}
	}
	return lines, kinds, start, end, true
}

// valueSpan finds the value of a key/value line whose value is a single-line basic or literal
// string: line[from:to] is the string token, and what follows it (spacing, a comment) stays.
func valueSpan(line string) (from, to int, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return 0, 0, false
	}
	from = eq + 1
	for from < len(line) && (line[from] == ' ' || line[from] == '\t') {
		from++
	}
	rest := line[from:]
	if strings.HasPrefix(rest, `"""`) || strings.HasPrefix(rest, `'''`) {
		return 0, 0, false
	}
	switch {
	case strings.HasPrefix(rest, `"`):
		for i := 1; i < len(rest); i++ {
			switch rest[i] {
			case '\\':
				i++
			case '"':
				return from, from + i + 1, true
			case '\n':
				return 0, 0, false
			}
		}
	case strings.HasPrefix(rest, "'"):
		if i := strings.IndexAny(rest[1:], "'\n"); i >= 0 && rest[1+i] == '\'' {
			return from, from + i + 2, true
		}
	}
	return 0, 0, false
}

// rawValues are the value tokens of the owned keys in the plain [ui.status_line] table of c.
func rawValues(c grokConfig) map[string]string {
	lines, kinds, start, end, ok := statusLines(string(c.raw))
	if !ok || start < 0 {
		return nil
	}
	raw := map[string]string{}
	for i := start + 1; i < end; i++ {
		if key := kinds[i].key; key == "type" || key == "command" {
			if from, to, ok := valueSpan(lines[i]); ok {
				raw[key] = lines[i][from:to]
			}
		}
	}
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// token is raw, the saved spelling of v, or else v as a TOML basic string.
func token(v, raw string) string {
	if raw != "" {
		return raw
	}
	return tomlString(v)
}

// setValue sets the value of an owned key line to v, keeping the key spelling, the spacing
// and any comment. raw is the saved spelling of v; without it, a line that holds v already
// stays as it is.
func setValue(line, v, raw string) (string, bool) {
	from, to, ok := valueSpan(line)
	if !ok {
		return "", false
	}
	if raw == "" {
		if now, err := decodeTOML([]byte("v = " + line[from:to])); err == nil && now["v"] == v {
			return line, true
		}
	}
	return line[:from] + token(v, raw) + line[to:], true
}

// edit sets the owned keys of [ui.status_line] to entry's members, spelled as in raw when it
// has them, and removes those that entry lacks; with drop, it also removes the table header
// when no other line is left in the table. It returns the new text, or nil when the editor
// cannot place the change. The write checks the meaning, so a wrong raw token is refused.
func (g grokStatusFile) edit(c grokConfig, entry json.RawMessage, raw map[string]string, drop bool) []byte {
	want := map[string]string{}
	if entry != nil {
		fields, err := objectFields(entry)
		if err != nil {
			return nil
		}
		for _, f := range fields {
			var s string
			if json.Unmarshal(f.value, &s) != nil {
				return nil
			}
			want[f.key] = s
		}
	}
	text := string(c.raw)
	lines, kinds, start, end, ok := statusLines(text)
	if !ok {
		return nil
	}
	if start < 0 {
		if c.table || len(want) == 0 {
			return nil // a table without a plain header, or nothing to add
		}
		var add strings.Builder
		if text != "" && !strings.HasSuffix(text, "\n") {
			add.WriteByte('\n')
		}
		if text != "" {
			add.WriteByte('\n')
		}
		add.WriteString("[ui.status_line]\n")
		for _, key := range grokOwned {
			if v, ok := want[key]; ok {
				add.WriteString(key + " = " + token(v, raw[key]) + "\n")
			}
		}
		return []byte(text + add.String())
	}
	var out []string
	out = append(out, lines[:start+1]...)
	at := len(out) // new keys go after the table's last non-blank line
	placed := map[string]bool{}
	rest := false
	for i := start + 1; i < end; i++ {
		k := kinds[i]
		if v, owned := want[k.key]; owned && !placed[k.key] {
			line, ok := setValue(lines[i], v, raw[k.key])
			if !ok {
				return nil
			}
			out = append(out, line)
			placed[k.key], at = true, len(out)
			continue
		}
		if k.key == "type" || k.key == "command" {
			continue // an owned key that entry lacks is removed
		}
		if k.key != "" {
			rest = true
		}
		out = append(out, lines[i])
		if strings.TrimSpace(lines[i]) != "" {
			at = len(out)
		}
	}
	var keys []string
	for _, key := range grokOwned {
		if v, ok := want[key]; ok && !placed[key] {
			keys = append(keys, key+" = "+token(v, raw[key])+"\n")
		}
	}
	if len(keys) > 0 {
		if at > len(out) {
			at = len(out)
		}
		if !strings.HasSuffix(out[at-1], "\n") {
			out[at-1] += "\n"
		}
		out = append(out[:at], append(keys, out[at:]...)...)
	}
	out = append(out, lines[end:]...)
	if drop && len(want) == 0 && !rest {
		// The table that setup added goes too, with the blank line that set it apart.
		header := start
		out = append(out[:header], out[header+1:]...)
		if header > 0 && strings.TrimSpace(out[header-1]) == "" {
			out = append(out[:header-1], out[header:]...)
		}
	}
	return []byte(strings.Join(out, ""))
}

// expected is c's meaning with the owned keys set from entry, the change that an edit may
// make; with drop, an emptied status table and then an emptied ui table go too.
func expected(c grokConfig, entry json.RawMessage, drop bool) (map[string]any, error) {
	value, err := decodeTOML(c.raw)
	if err != nil {
		return nil, err
	}
	ui, _ := value["ui"].(map[string]any)
	if ui == nil {
		ui = map[string]any{}
	}
	table, _ := ui["status_line"].(map[string]any)
	if table == nil {
		table = map[string]any{}
	}
	for _, key := range grokOwned {
		delete(table, key)
	}
	if entry != nil {
		fields, err := objectFields(entry)
		if err != nil {
			return nil, err
		}
		for _, f := range fields {
			var s string
			if err := json.Unmarshal(f.value, &s); err != nil {
				return nil, err
			}
			table[f.key] = s
		}
	}
	ui["status_line"] = table
	value["ui"] = ui
	if drop && len(table) == 0 {
		delete(ui, "status_line")
	}
	return value, nil
}

// write makes the edit, checks that its meaning differs only in the owned keys, and replaces
// the file as the other status lines do.
func (g grokStatusFile) write(c grokConfig, entry json.RawMessage, raw map[string]string, drop bool) error {
	data := g.edit(c, entry, raw, drop)
	if data == nil {
		return g.unusual()
	}
	after, err := decodeTOML(data)
	if err != nil {
		return g.unusual()
	}
	want, err := expected(c, entry, drop)
	if drop {
		// Without the table that setup added, ui is empty or absent, as an explicit [ui] keeps it.
		for _, v := range []map[string]any{after, want} {
			if ui, ok := v["ui"].(map[string]any); ok && len(ui) == 0 {
				delete(v, "ui")
			}
		}
	}
	if err != nil || !reflect.DeepEqual(after, want) {
		return g.unusual()
	}
	return g.writeRaw(c.raw, data)
}

// setUp makes the hook the status-line command, saving the owned keys first, as the other
// status lines do.
func (g grokStatusFile) setUp() (StatusLineResult, error) {
	result := StatusLineResult{Action: "set_up", SettingsFile: g.settings}
	if info, err := os.Stat(filepath.Dir(g.settings)); err != nil || !info.IsDir() {
		result.Outcome, result.Reason = "skipped", g.missing
		return result, nil
	}
	record, err := g.load()
	if err != nil {
		return result, err
	}
	if record.SettingsFile != "" && record.SettingsFile != g.settings && active(record) {
		result.Outcome, result.Reason = "changed", "statusline_other_file"
		return result, nil
	}
	c, err := g.read()
	if err != nil {
		return result, err
	}
	now := c.entry
	var original json.RawMessage
	var raw map[string]string // the spelling of original's values; none for an unwrapped one
	added := !c.table
	switch {
	case active(record) && equal(now, record.Wrapper):
		original, raw, added = record.Original, record.RawValues, record.TableAdded
	case record.State == "pending" && equal(now, record.Original):
		original, raw, added = record.Original, record.RawValues, record.TableAdded
	case active(record):
		if g.isWrapper(now) {
			result.Outcome, result.Reason = "changed", "statusline_changed"
			return result, nil
		}
		original, raw = now, rawValues(c)
	case g.isWrapper(now):
		original = g.unwrap(now)
	default:
		original, raw = now, rawValues(c)
	}
	entry := g.wrapperEntry(original)
	if equal(entry, now) {
		result.Outcome = "unchanged"
		return result, g.save(statusRecord{State: "enabled", Original: original, Wrapper: now, TableAdded: added, RawValues: raw})
	}
	// Save the original before the file changes, so a crash can never lose it.
	if err := g.save(statusRecord{State: "pending", Original: original, Wrapper: entry, TableAdded: added, RawValues: raw}); err != nil {
		return result, err
	}
	if err := g.write(c, entry, nil, false); err != nil {
		return result, err
	}
	result.Outcome = "set_up"
	return result, g.save(statusRecord{State: "enabled", Original: original, Wrapper: entry, TableAdded: added, RawValues: raw})
}

// remove restores the saved keys in the recorded file while it still holds the exact wrapper,
// and removes the table only when setup added it and nothing else is left in it.
func (g grokStatusFile) remove() (StatusLineResult, error) {
	result := StatusLineResult{Action: "remove", SettingsFile: g.settings}
	record, err := g.load()
	if err != nil {
		return result, err
	}
	if record.SettingsFile != "" {
		g.settings = record.SettingsFile
		result.SettingsFile = g.settings
	}
	declined := statusRecord{State: "declined"}
	if !active(record) {
		result.Outcome = "not_set_up"
		return result, g.save(declined)
	}
	c, err := g.read()
	if err != nil {
		return result, err
	}
	switch {
	case record.State == "pending" && equal(c.entry, record.Original):
		result.Outcome = "restored"
	case !equal(c.entry, record.Wrapper):
		result.Outcome, result.Reason = "changed", "statusline_changed"
	default:
		if err := g.write(c, record.Original, record.RawValues, record.TableAdded); err != nil {
			return result, err
		}
		result.Outcome = "restored"
	}
	return result, g.save(declined)
}
