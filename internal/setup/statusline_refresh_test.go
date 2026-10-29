package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The Claude status line runs at least every 30 seconds, also while the session is idle
// (DECISIONS.md#CLAUDE-IDLE-566): setup sets refreshInterval in its entry, keeps an earlier
// valid interval of 1 to 30 seconds, and leaves the saved original and an edited entry alone.

func claudeLine(t *testing.T) statusLine {
	t.Helper()
	l, _ := statusLineFixture(t, "/opt/koinon")
	l.refresh = claudeRefresh
	return l
}

func writeSettings(t *testing.T, l statusLine, statusLine string) {
	t.Helper()
	data := `{"model":"opus"}`
	if statusLine != "" {
		data = `{"model":"opus","statusLine":` + statusLine + `}`
	}
	if err := os.WriteFile(l.settings, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

// interval returns the refreshInterval of the settings' statusLine entry, or nil.
func interval(t *testing.T, l statusLine) json.RawMessage {
	t.Helper()
	entry, _ := settingsEntry(t, l)
	fields, err := objectFields(entry)
	if err != nil {
		t.Fatalf("entry %s: %v", entry, err)
	}
	return lookup(fields, "refreshInterval")
}

// editEntry sets one member of the settings' statusLine entry, as a person would.
func editEntry(t *testing.T, l statusLine, key, value string) {
	t.Helper()
	_, fields, err := l.read()
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := objectFields(lookup(fields, "statusLine"))
	entry = replace(entry, key, json.RawMessage(value))
	if err := os.WriteFile(l.settings, encodeFields(replace(fields, "statusLine", encodeFields(entry))), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeStatusLineRefreshInterval(t *testing.T) {
	// Without an earlier entry, and around an earlier command without an interval.
	for _, earlier := range []string{"", `{"type":"command","command":"line.sh","padding":1}`} {
		l := claudeLine(t)
		writeSettings(t, l, earlier)
		result, err := l.setUp()
		mustOutcome(t, result, err, "set_up")
		if got := interval(t, l); !equal(got, json.RawMessage("30")) {
			t.Fatalf("earlier %q: interval %s", earlier, got)
		}
		result, err = l.setUp()
		mustOutcome(t, result, err, "unchanged")
	}
	// An earlier valid interval of 1 to 30 seconds stays; any other value becomes 30. Removal
	// restores the earlier entry as it was.
	for value, want := range map[string]string{
		"1": "1", "30": "30", "30.0": "30.0", "1.5": "1.5", "120": "30", "30.5": "30", "0": "30", "-5": "30", "0.5": "30", "0.75": "30",
		`"30"`: "30", "true": "30", "null": "30", "{}": "30", "[]": "30", "[10]": "30",
	} {
		l := claudeLine(t)
		earlier := `{"type":"command","command":"line.sh","refreshInterval":` + value + `,"padding":1}`
		writeSettings(t, l, earlier)
		result, err := l.setUp()
		mustOutcome(t, result, err, "set_up")
		// The kept value keeps its spelling.
		if got := interval(t, l); string(got) != want {
			t.Fatalf("earlier interval %s: got %s, want %s", value, got, want)
		}
		result, err = l.setUp()
		mustOutcome(t, result, err, "unchanged")
		result, err = l.remove()
		mustOutcome(t, result, err, "restored")
		if restored, _ := settingsEntry(t, l); !equal(restored, json.RawMessage(earlier)) {
			t.Fatalf("earlier interval %s: restored %s", value, restored)
		}
	}
}

// An entry that an earlier Koinon set up gets the interval at the next setup, and removal still
// restores the original. An entry whose interval the person changed is theirs: setup and
// removal keep it (statusline_changed).
func TestClaudeStatusLineRefreshUpgradeAndEdits(t *testing.T) {
	l := claudeLine(t)
	original := `{"type":"command","command":"line.sh"}`
	writeSettings(t, l, original)
	earlier := l
	earlier.refresh = 0
	result, err := earlier.setUp()
	mustOutcome(t, result, err, "set_up")
	if got := interval(t, l); got != nil {
		t.Fatalf("the earlier setup set an interval: %s", got)
	}
	result, err = l.setUp()
	mustOutcome(t, result, err, "set_up")
	if got := interval(t, l); !equal(got, json.RawMessage("30")) {
		t.Fatalf("upgrade: %s", got)
	}
	result, err = l.setUp()
	mustOutcome(t, result, err, "unchanged")
	result, err = l.remove()
	mustOutcome(t, result, err, "restored")
	if restored, _ := settingsEntry(t, l); !equal(restored, json.RawMessage(original)) {
		t.Fatalf("restored after the upgrade: %s", restored)
	}

	l = claudeLine(t)
	writeSettings(t, l, original)
	result, err = l.setUp()
	mustOutcome(t, result, err, "set_up")
	editEntry(t, l, "refreshInterval", "120")
	result, err = l.setUp()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
		t.Fatalf("setup over an edited interval: %+v %v", result, err)
	}
	if got := interval(t, l); !equal(got, json.RawMessage("120")) {
		t.Fatalf("edited interval at setup: %s", got)
	}
	result, err = l.remove()
	if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
		t.Fatalf("removal of an edited interval: %+v %v", result, err)
	}
	if got := interval(t, l); !equal(got, json.RawMessage("120")) {
		t.Fatalf("edited interval at removal: %s", got)
	}
}

// Only the Claude status line gets the interval; the agy entry gets none.
func TestOnlyClaudeStatusLineRefreshes(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	config := t.TempDir()
	result, err := ClaudeStatusLine("/opt/koinon", state, config, false)
	mustOutcome(t, result, err, "set_up")
	claude := statusLine{settings: filepath.Join(config, "settings.json")}
	if got := interval(t, claude); !equal(got, json.RawMessage("30")) {
		t.Fatalf("Claude: %s", got)
	}
	agy := filepath.Join(t.TempDir(), "agy", "settings.json")
	os.MkdirAll(filepath.Dir(agy), 0700)
	result, err = AgyStatusLine("/opt/koinon", state, agy, false)
	mustOutcome(t, result, err, "set_up")
	if got := interval(t, statusLine{settings: agy, label: "Antigravity settings"}); got != nil {
		t.Fatalf("agy: %s", got)
	}
}
