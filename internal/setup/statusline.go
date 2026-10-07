package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	"github.com/rwcii/koinon/internal/platform"
)

// The Claude Code status line (sprint chunk 08), with the rules of claude_statusline.py:
// only the `statusLine` entry of <claude config dir>/settings.json changes, the earlier entry
// is saved once in the Go state directory before the settings change, a removal restores it
// only while the entry is still the one Koinon set up, and a settings file that changes while
// Koinon edits it is a conflict that keeps the other change. Claude Code takes no lock on the
// file, so a change between the last comparison and the replacement cannot be detected.

const (
	statusLineRecord = "claude-statusline.json"
	settingsMax      = 1 << 20
)

// SettingsError is a refusal to edit the Claude settings file.
type SettingsError struct {
	Code    string
	Message string
	Path    string
}

func (e SettingsError) Error() string { return e.Code + ": " + e.Message + " (" + e.Path + ")" }

type statusRecord struct {
	State        string          `json:"state"` // enabled, pending or declined
	SettingsFile string          `json:"settings_file"`
	Original     json.RawMessage `json:"original"`
	Wrapper      json.RawMessage `json:"wrapper"`
}

// StatusLineResult reports one set-up or removal.
type StatusLineResult struct {
	Action       string `json:"action"`
	Outcome      string `json:"outcome"` // set_up, unchanged, changed, skipped, restored, not_set_up
	SettingsFile string `json:"settings_file"`
	Reason       string `json:"reason,omitempty"`
}

type statusLine struct {
	binary, state, settings string
}

func claudeConfigDir(getenv func(string) string) string {
	if v := getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// field is one member of a JSON object, kept in its original order and spelling.
type field struct {
	key   string
	value json.RawMessage
}

func objectFields(data []byte) ([]field, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not an object")
	}
	var fields []field
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, field{key, value})
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, errors.New("not an object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return fields, nil
}

func encodeFields(fields []field) []byte {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		out.Write(key)
		out.WriteByte(':')
		out.Write(f.value)
	}
	out.WriteByte('}')
	return out.Bytes()
}

// lookup returns the last value of key (JSON keeps the last duplicate), or nil.
func lookup(fields []field, key string) json.RawMessage {
	var found json.RawMessage
	for _, f := range fields {
		if f.key == key {
			found = f.value
		}
	}
	if string(bytes.TrimSpace(found)) == "null" {
		return nil
	}
	return found
}

// replace sets key at its first position, removing other duplicates, or removes it when
// value is nil.
func replace(fields []field, key string, value json.RawMessage) []field {
	result, placed := []field{}, false
	for _, f := range fields {
		if f.key != key {
			result = append(result, f)
		} else if value != nil && !placed {
			result, placed = append(result, field{key, value}), true
		}
	}
	if value != nil && !placed {
		result = append(result, field{key, value})
	}
	return result
}

// equal compares two JSON values as values, as Python compares decoded objects.
func equal(a, b json.RawMessage) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func entryCommand(entry json.RawMessage) string {
	var e struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if entry == nil || json.Unmarshal(entry, &e) != nil || e.Type != "command" {
		return ""
	}
	return e.Command
}

// splitWords splits a command line as a POSIX shell does for quoting: spaces, single
// quotes, double quotes and backslashes. It refuses an unterminated quote.
func splitWords(s string) ([]string, error) {
	var words []string
	var word strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words, inWord = append(words, word.String()), false
				word.Reset()
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated quote")
			}
			word.WriteString(s[i+1 : i+1+end])
			i, inWord = i+1+end, true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0 {
					i++
				}
				word.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated quote")
			}
			inWord = true
		case c == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}

// isWrapper reports whether an entry runs this binary's status-line hook.
func (l statusLine) isWrapper(entry json.RawMessage) bool {
	words, err := splitWords(entryCommand(entry))
	return err == nil && len(words) >= 3 && words[0] == l.binary && words[1] == "hook" && words[2] == "claude-status"
}

// wrapperEntry is the entry that runs the hook around the original command, keeping the
// original's other fields.
func (l statusLine) wrapperEntry(original json.RawMessage) json.RawMessage {
	command := quote(l.binary) + " hook claude-status"
	if user := entryCommand(original); user != "" {
		command += " --command " + quote(user)
	}
	var fields []field
	if original != nil {
		if existing, err := objectFields(original); err == nil {
			for _, f := range existing {
				if f.key != "type" && f.key != "command" {
					fields = append(fields, f)
				}
			}
		}
	}
	typ, _ := json.Marshal("command")
	cmd, _ := json.Marshal(command)
	return encodeFields(append(fields, field{"type", typ}, field{"command", cmd}))
}

// unwrap rebuilds the entry that a wrapper entry runs, or nil when it runs none.
func (l statusLine) unwrap(entry json.RawMessage) json.RawMessage {
	words, err := splitWords(entryCommand(entry))
	if err != nil || len(words) != 5 || words[3] != "--command" {
		return nil
	}
	fields, err := objectFields(entry)
	if err != nil {
		return nil
	}
	cmd, _ := json.Marshal(words[4])
	return encodeFields(replace(fields, "command", cmd))
}

// read returns the raw settings (nil when absent) and their members; it refuses anything
// that is unsafe to replace.
func (l statusLine) read() ([]byte, []field, error) {
	info, err := os.Lstat(l.settings)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, SettingsError{"settings_symlink", "Claude settings file is a symbolic link; not changed", l.settings}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || stat.Uid != uint32(os.Geteuid()) || info.Size() > settingsMax {
		return nil, nil, SettingsError{"settings_unsafe", "Claude settings file is not a regular file owned by this user", l.settings}
	}
	raw, err := os.ReadFile(l.settings)
	if err != nil {
		return nil, nil, err
	}
	fields, err := objectFields(raw)
	if err != nil {
		return nil, nil, SettingsError{"settings_invalid", "Claude settings file is not a valid JSON object; not changed", l.settings}
	}
	return raw, fields, nil
}

func current(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// write replaces the settings atomically unless they changed since they were read, and
// reports a change made right after the replacement.
func (l statusLine) write(expected []byte, fields []field) error {
	var data bytes.Buffer
	if err := json.Indent(&data, encodeFields(fields), "", "  "); err != nil {
		return err
	}
	data.WriteByte('\n')
	mode := os.FileMode(0600)
	if expected != nil {
		info, err := os.Stat(l.settings)
		if err != nil {
			return err
		}
		mode = info.Mode().Perm()
	}
	temp := filepath.Join(filepath.Dir(l.settings), ".settings.json.koinon")
	os.Remove(temp)
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data.Bytes())
	}
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	conflict := SettingsError{"settings_conflict", "Claude settings changed while Koinon was editing them; the other change was kept", l.settings}
	if !bytes.Equal(current(l.settings), expected) {
		return conflict
	}
	if err := os.Rename(temp, l.settings); err != nil {
		return err
	}
	if err := platform.SyncDir(filepath.Dir(l.settings)); err != nil {
		return err
	}
	if !bytes.Equal(current(l.settings), data.Bytes()) {
		conflict.Message = "Claude settings changed right after Koinon wrote them; the other change was kept"
		return conflict
	}
	return nil
}

func (l statusLine) load() (statusRecord, error) {
	var r statusRecord
	f, err := platform.OpenPrivate(filepath.Join(l.state, statusLineRecord), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4*settingsMax))
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	if string(r.Original) == "null" {
		r.Original = nil
	}
	if string(r.Wrapper) == "null" {
		r.Wrapper = nil
	}
	return r, err
}

func (l statusLine) save(r statusRecord) error {
	r.SettingsFile = l.settings
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(l.state, ".claude-statusline-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(l.state, statusLineRecord)); err != nil {
		return err
	}
	return platform.SyncDir(l.state)
}

func active(r statusRecord) bool { return r.State == "enabled" || r.State == "pending" }

// setUp makes the hook the statusLine command at the user's request, saving the previous
// entry once. An entry the user changed after set-up is wrapped again, except an edited
// wrapper, which is left alone and reported.
func (l statusLine) setUp() (StatusLineResult, error) {
	result := StatusLineResult{Action: "set_up", SettingsFile: l.settings}
	if info, err := os.Stat(filepath.Dir(l.settings)); err != nil || !info.IsDir() {
		result.Outcome, result.Reason = "skipped", "claude_config_missing"
		return result, nil
	}
	record, err := l.load()
	if err != nil {
		return result, err
	}
	raw, fields, err := l.read()
	if err != nil {
		return result, err
	}
	now := lookup(fields, "statusLine")
	var original json.RawMessage
	switch {
	case active(record) && equal(now, record.Wrapper):
		original = record.Original
	case record.State == "pending" && equal(now, record.Original):
		original = record.Original
	case active(record):
		if l.isWrapper(now) {
			result.Outcome, result.Reason = "changed", "statusline_changed"
			return result, nil
		}
		original = now
	case l.isWrapper(now):
		original = l.unwrap(now)
	default:
		original = now
	}
	entry := l.wrapperEntry(original)
	if equal(entry, now) {
		result.Outcome = "unchanged"
		return result, l.save(statusRecord{State: "enabled", Original: original, Wrapper: now})
	}
	// Save the original before the settings change, so a crash can never lose it.
	if err := l.save(statusRecord{State: "pending", Original: original, Wrapper: entry}); err != nil {
		return result, err
	}
	if err := l.write(raw, replace(fields, "statusLine", entry)); err != nil {
		return result, err
	}
	result.Outcome = "set_up"
	return result, l.save(statusRecord{State: "enabled", Original: original, Wrapper: entry})
}

// remove restores the saved entry while the hook entry is still in place, then records a
// decline; an entry the user changed is kept and reported.
func (l statusLine) remove() (StatusLineResult, error) {
	result := StatusLineResult{Action: "remove", SettingsFile: l.settings}
	record, err := l.load()
	if err != nil {
		return result, err
	}
	if record.SettingsFile != "" {
		l.settings = record.SettingsFile
		result.SettingsFile = l.settings
	}
	declined := statusRecord{State: "declined"}
	if !active(record) {
		result.Outcome = "not_set_up"
		return result, l.save(declined)
	}
	raw, fields, err := l.read()
	if err != nil {
		return result, err
	}
	now := lookup(fields, "statusLine")
	switch {
	case record.State == "pending" && equal(now, record.Original):
		result.Outcome = "restored"
	case !equal(now, record.Wrapper):
		result.Outcome, result.Reason = "changed", "statusline_changed"
	default:
		if err := l.write(raw, replace(fields, "statusLine", record.Original)); err != nil {
			return result, err
		}
		result.Outcome = "restored"
	}
	return result, l.save(declined)
}

// ClaudeStatusLine sets up the status-line hook, or removes it when remove is set.
func ClaudeStatusLine(binary, state, configDir string, remove bool) (StatusLineResult, error) {
	root, err := platform.PrivateDir(state)
	if err != nil {
		return StatusLineResult{}, fmt.Errorf("state directory: %w", err)
	}
	l := statusLine{binary: binary, state: root, settings: filepath.Join(configDir, "settings.json")}
	if remove {
		return l.remove()
	}
	return l.setUp()
}
