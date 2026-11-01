package setup

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Koinon's Grok status line runs every 30 seconds, also while the session is idle; an earlier
// integer from 1 to 30 stays, and removal restores the earlier table exactly
// (DECISIONS.md#GROK-IDLE-568).

const grokUserTable = "[ui.status_line]\ntype = \"command\"\ncommand = \"~/bin/line\"\n"

func TestGrokRefreshInterval(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       int64
	}{
		{"none", "", 30},
		{"1", "refresh_interval = 1\n", 1},
		{"30", "refresh_interval = 30 # mine\n", 30},
		{"plus sign", "refresh_interval = +5\n", 5},
		{"120", "refresh_interval = 120\n", 30},
		{"hex 120", "refresh_interval = 0x78\n", 30},
		{"0", "refresh_interval = 0\n", 30},
		{"negative", "refresh_interval = -5\n", 30},
		{"float", "refresh_interval = 1.5\n", 30},
		{"float 30", "refresh_interval = 30.0\n", 30},
		{"string", "refresh_interval = \"30\"\n", 30},
		{"boolean", "refresh_interval = true\n", 30},
		{"nan", "refresh_interval = nan\n", 30},
		{"signed nan", "refresh_interval = -nan # mine\n", 30},
		{"inf", "refresh_interval = inf\n", 30},
		{"plus inf", "refresh_interval = +inf\n", 30},
		{"minus inf", "refresh_interval = -inf\n", 30},
		{"date", "refresh_interval = 1979-05-27\n", 30},
		{"local time", "refresh_interval = 07:32:00\n", 30},
		{"date-time", "refresh_interval = 1979-05-27T07:32:00Z\n", 30},
		{"date-time with a space", "refresh_interval = 1979-05-27 07:32:00-07:00 # mine\n", 30},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, _ := grokFixture(t)
			original := grokUserTable + c.line
			os.WriteFile(g.settings, []byte(original), 0600)
			result, err := g.setUp()
			mustOutcome(t, result, err, "set_up")
			table := grokStatusOf(t, g.settings)
			if table["refresh_interval"] != c.want || table["command"] != grokWrapper+" --command '~/bin/line'" {
				t.Fatalf("table: %+v", table)
			}
			result, err = g.setUp()
			mustOutcome(t, result, err, "unchanged")
			result, err = g.remove()
			mustOutcome(t, result, err, "restored")
			if data, _ := os.ReadFile(g.settings); string(data) != original {
				t.Fatalf("restored:\n%s", data)
			}
		})
	}
	// A nan or a date elsewhere in the file stays as it is.
	g, _ := grokFixture(t)
	other := "[other]\nn = nan\nlist = [nan, 1979-05-27]\nwhen = 1979-05-27 07:32:00\n\n" + grokUserTable
	os.WriteFile(g.settings, []byte(other), 0600)
	result, err := g.setUp()
	mustOutcome(t, result, err, "set_up")
	if table := grokStatusOf(t, g.settings); table["refresh_interval"] != int64(30) {
		t.Fatalf("table: %+v", table)
	}
	result, err = g.remove()
	mustOutcome(t, result, err, "restored")
	if data, _ := os.ReadFile(g.settings); string(data) != other {
		t.Fatalf("restored:\n%s", data)
	}
	// An interval that is not a scalar is a shape that the editor cannot prove.
	g, _ = grokFixture(t)
	os.WriteFile(g.settings, []byte(grokUserTable+"refresh_interval = [30]\n"), 0600)
	if _, err := g.setUp(); err == nil || !strings.Contains(err.Error(), "settings_unusual") {
		t.Fatalf("array interval: %v", err)
	}
}

// legacyGrokRecord writes a record of a setup from before refresh_interval was owned: its original and
// its wrapper hold type and command only, and the file holds that wrapper and extra.
func legacyGrokRecord(t *testing.T, g grokStatusFile, extra string) string {
	t.Helper()
	original, _ := json.Marshal(map[string]string{"type": "command", "command": "~/bin/line"})
	wrapper, _ := json.Marshal(map[string]string{"type": "command", "command": grokWrapper + " --command '~/bin/line'"})
	if err := g.save(statusRecord{State: "enabled", Original: original, Wrapper: wrapper, RawValues: map[string]string{"command": `"~/bin/line"`}}); err != nil {
		t.Fatal(err)
	}
	text := "[ui.status_line]\ntype = \"command\"\ncommand = \"" + grokWrapper + " --command '~/bin/line'\"\n" + extra
	os.WriteFile(g.settings, []byte(text), 0600)
	return text
}

// A legacy record is checked on type and command only; the interval that the file has at the
// upgrade becomes the one that removal restores, and the interval is owned from then on.
func TestGrokRefreshLegacyUpgrade(t *testing.T) {
	for _, c := range []struct {
		name, extra string
		want        int64
	}{
		{"no interval", "", 30},
		{"valid interval", "refresh_interval = 5 # mine\n", 5},
		{"long interval", "refresh_interval = 120\n", 30},
		{"nan", "refresh_interval = nan\n", 30},
		{"minus inf", "refresh_interval = -inf\n", 30},
		{"date", "refresh_interval = 1979-05-27 # mine\n", 30},
	} {
		t.Run(c.name, func(t *testing.T) {
			g, _ := grokFixture(t)
			legacyGrokRecord(t, g, c.extra)
			result, err := g.setUp()
			if c.want == 5 {
				mustOutcome(t, result, err, "unchanged")
			} else {
				mustOutcome(t, result, err, "set_up")
			}
			if table := grokStatusOf(t, g.settings); table["refresh_interval"] != c.want {
				t.Fatalf("table: %+v", table)
			}
			record, _ := g.load()
			if grokLegacy(record) {
				t.Fatalf("still legacy: %s", record.Wrapper)
			}
			// Setup again changes nothing.
			result, err = g.setUp()
			mustOutcome(t, result, err, "unchanged")
			result, err = g.remove()
			mustOutcome(t, result, err, "restored")
			if data, _ := os.ReadFile(g.settings); string(data) != grokUserTable+c.extra {
				t.Fatalf("restored:\n%s", data)
			}
		})
	}
	// Removal right after the upgrade, without a new setup, keeps the file's interval.
	for _, line := range []string{"refresh_interval = 7\n", "refresh_interval = nan\n", "refresh_interval = 1979-05-27\n"} {
		g, _ := grokFixture(t)
		legacyGrokRecord(t, g, line)
		result, err := g.remove()
		mustOutcome(t, result, err, "restored")
		if data, _ := os.ReadFile(g.settings); string(data) != grokUserTable+line {
			t.Fatalf("removal of a legacy record:\n%s", data)
		}
	}
	// A legacy wrapper that the person edited stays, at setup and at removal.
	g, _ := grokFixture(t)
	text := legacyGrokRecord(t, g, "refresh_interval = 7\n")
	edited := strings.Replace(text, "--command '~/bin/line'", "--command 'mine'", 1)
	os.WriteFile(g.settings, []byte(edited), 0600)
	for _, step := range []func() (StatusLineResult, error){g.setUp, g.remove} {
		result, err := step()
		if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
			t.Fatalf("edited legacy wrapper: %+v %v", result, err)
		}
		if data, _ := os.ReadFile(g.settings); string(data) != edited {
			t.Fatalf("edited legacy wrapper changed:\n%s", data)
		}
	}
}

// After the upgrade, an edit of only the interval is the person's: setup and removal keep it,
// also an edit to a value that JSON cannot hold.
func TestGrokRefreshEditAfterMigration(t *testing.T) {
	for name, setUp := range map[string]func(g grokStatusFile){
		"new installation": func(g grokStatusFile) {
			os.WriteFile(g.settings, []byte(grokUserTable), 0600)
		},
		"legacy record": func(g grokStatusFile) { legacyGrokRecord(t, g, "") },
	} {
		for _, value := range []string{"10", "nan", "1979-05-27"} {
			t.Run(name+" "+value, func(t *testing.T) {
				g, _ := grokFixture(t)
				setUp(g)
				result, err := g.setUp()
				mustOutcome(t, result, err, "set_up")
				data, _ := os.ReadFile(g.settings)
				edited := strings.Replace(string(data), "refresh_interval = 30", "refresh_interval = "+value, 1)
				if edited == string(data) {
					t.Fatalf("no interval line:\n%s", data)
				}
				os.WriteFile(g.settings, []byte(edited), 0600)
				for _, step := range []func() (StatusLineResult, error){g.setUp, g.remove} {
					result, err := step()
					if err != nil || result.Outcome != "changed" || result.Reason != "statusline_changed" {
						t.Fatalf("edited interval: %+v %v", result, err)
					}
					if data, _ := os.ReadFile(g.settings); string(data) != edited {
						t.Fatalf("edited interval changed:\n%s", data)
					}
				}
			})
		}
	}
}
