package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// The agy cycle adapter (#512) against captured screens and a synthetic terminal. Nothing
// here starts agy or reaches a real tmux server.

func agyScreen(t *testing.T, name string) codexScreen {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "agy-screens", "cycle-"+name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return readCodexScreen(string(data))
}

// Captured Antigravity CLI 1.4.7 screens (account data and paths replaced).
func TestAgyCycleScreenFixtures(t *testing.T) {
	for name, want := range map[string]struct {
		text           string
		idle, cleared  bool
		menu, notReady string
	}{
		"start":        {idle: true, cleared: true},
		"after-clear":  {idle: true, cleared: true},
		"after-turn":   {idle: true},
		"after-dialog": {idle: true},
		"draft":        {text: "some draft", notReady: "input_not_empty"},
		"working":      {notReady: "turn_running"},
		"clear-menu":   {text: "/cl", menu: "/clear", notReady: "input_not_empty"},
		"pickup-menu":  {text: "/koinon-pickup", menu: "/koinon-pickup", notReady: "input_not_empty"},
		"pickup-fuzzy": {text: "/koinon-pi", menu: "/koinon-pickup", notReady: "input_not_empty"},
		"after-pickup": {notReady: "turn_running"},
	} {
		s := agyScreen(t, name)
		l, ok := agyComposer(s)
		if !ok || l.text != want.text || agyIdle(s) != want.idle || agyCleared(s) != want.cleared || agyDialog(s).dialog.Digest != "" {
			t.Fatalf("%s: %+v %v idle %v cleared %v", name, l, ok, agyIdle(s), agyCleared(s))
		}
		if want.menu != "" && (len(l.menu) == 0 || !l.menu[0].selected || l.menu[0].name != want.menu) {
			t.Fatalf("%s menu: %+v", name, l.menu)
		}
		if got := agyNotReady(CycleTarget{}, s); got != want.notReady {
			t.Fatalf("%s: not ready %q", name, got)
		}
	}
	// The full command selects its one row; a partial one does not count, nor a shadow's row.
	row := pickupRow(pickupSkillEntry())
	if !agyMenuReady(agyScreen(t, "pickup-menu"), agyPickupCommand, row) || agyMenuReady(agyScreen(t, "pickup-fuzzy"), agyPickupCommand, row) {
		t.Fatal("pickup menu rows")
	}
	// The shadow capture's one row has the workspace skill's description (its composer was
	// captured while typing).
	if l, _ := agyComposer(agyScreen(t, "pickup-shadow")); len(l.menu) != 1 || l.menu[0].name != agyPickupCommand || row(l.menu[0]) {
		t.Fatalf("shadow row: %+v", l.menu)
	}
	if agyPrompts(agyScreen(t, "after-pickup"), agyPickupCommand) != 1 || !agySubmitted(agyScreen(t, "start"), agyScreen(t, "after-pickup"), agyPickupCommand) {
		t.Fatal("pickup receipt")
	}
	// The approval dialog: its visible options, the selection, and a digest of its text.
	first, moved, wrapped := agyDialog(agyScreen(t, "dialog-command")), agyDialog(agyScreen(t, "dialog-moved")), agyDialog(agyScreen(t, "dialog-wrapped"))
	if !slices.Equal(first.dialog.Options, []string{"1", "2", "3", "4"}) || first.selected != "1" ||
		!slices.Equal(moved.dialog.Options, []string{"1", "2", "3", "4", "5", "6"}) || moved.selected != "4" || moved.head != first.head ||
		moved.dialog.Digest == first.dialog.Digest || wrapped.selected != "1" || len(wrapped.dialog.Options) != 4 {
		t.Fatalf("dialogs: %+v %+v %+v", first, moved, wrapped)
	}
	// The list grows after the digit: the chosen option keeps its text, which Choose compares.
	if first.option("4") != "4. No, cancel" || moved.option("4") != first.option("4") || moved.option("7") != "" {
		t.Fatalf("option text: %q %q", first.option("4"), moved.option("4"))
	}
	if !strings.Contains(wrapped.dialog.Lines[len(wrapped.dialog.Lines)-3], "--path- format=absolute") {
		t.Fatalf("wrapped option: %q", wrapped.dialog.Lines)
	}
	if got := agyNotReady(CycleTarget{}, agyScreen(t, "dialog-command")); got != "native_dialog" {
		t.Fatalf("dialog: %q", got)
	}
	// After the clear, only the cleared screen admits the pickup.
	if agyNotReady(CycleTarget{SuccessorEpoch: "b"}, agyScreen(t, "after-turn")) != "successor_not_cleared" ||
		agyNotReady(CycleTarget{SuccessorEpoch: "b"}, agyScreen(t, "after-clear")) != "" {
		t.Fatal("successor screen")
	}
}

// agyTerminal is a synthetic agy pane: a banner, a transcript, a composer with the slash-command
// menu, an approval dialog, and the conversations that /clear starts.
type agyTerminal struct {
	mu        sync.Mutex
	said      []string
	input     string
	running   bool
	dialog    bool
	selected  string
	chosen    []string
	keys      []string
	fences    int
	failAt    int
	pickupRow string // the menu row that /koinon-pickup shows
	clears    int
	noClear   bool // /clear does not take effect
	home, dir string
	state     string
	pid       int64
	start     int64
	groups    map[int]platform.ProcessGroup
	parents   map[int]int
	commands  map[int]string
}

func newAgyTerminal(t *testing.T) *agyTerminal {
	a := &agyTerminal{home: t.TempDir(), dir: t.TempDir(), state: t.TempDir(), pid: 5151, start: 99,
		pickupRow: "> /koinon-pickup  " + pickupSkillDescription[:95] + "...",
		groups:    map[int]platform.ProcessGroup{5151: {Group: 5151, Terminal: 7, Foreground: 5151}, 5152: {Group: 5151, Terminal: 7, Foreground: 5151}},
		parents:   map[int]int{5152: 5151},
		commands:  map[int]string{5151: "/synthetic/agy", 5152: "/synthetic/koinon"}}
	skill := AgyPickupSkillPath(a.home)
	os.MkdirAll(filepath.Dir(skill), 0o700)
	os.WriteFile(skill, pickupSkillEntry().Content, 0o600)
	a.statusLine(t, true)
	previous, wait := agyEvidencedPlatforms, agyKeyWait
	agyEvidencedPlatforms, agyKeyWait = map[string]bool{runtime.GOOS: true}, 300*time.Millisecond
	t.Cleanup(func() { agyEvidencedPlatforms, agyKeyWait = previous, wait })
	return a
}

// statusLine writes the Antigravity CLI settings with Koinon's statusLine entry and setup's
// record of it; enabled false writes a declined record.
func (a *agyTerminal) statusLine(t *testing.T, enabled bool) {
	settings := filepath.Join(a.home, ".gemini", "antigravity-cli", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	wrapper := `{"type":"command","command":"'/opt/koinon' hook agy-status","stack_with_default":true}`
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":[]},"statusLine":`+wrapper+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state := map[bool]string{true: "enabled", false: "declined"}[enabled]
	record, _ := json.Marshal(map[string]any{"state": state, "settings_file": settings, "wrapper": json.RawMessage(wrapper)})
	if err := os.WriteFile(filepath.Join(a.state, "agy-statusline.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (a *agyTerminal) screen() string {
	rule := strings.Repeat("─", 60)
	footer := func(left string) string { return left + "          Synthetic Model · high" }
	lines := []string{"", "      ▄▀▀▄        Antigravity CLI 1.4.7", "     ▀▀▀▀▀▀       person@example.invalid (Synthetic)",
		"    ▀▀▀▀▀▀▀▀      Synthetic Model", "   ▄▀▀    ▀▀▄     /tmp/synthetic/work", "  ▄▀▀      ▀▀▄", ""}
	for _, said := range a.said {
		lines = append(lines, rule, "> "+said, "", "  ok", "")
	}
	if a.dialog {
		lines = append(lines, "Command", rule, "", "Requesting permission for:", "   uname -s", "", "Run this command?")
		for i, option := range []string{"Yes, run command", "Yes, and always allow in this conversation", "Yes, and always allow (Persist to settings.json)", "No, cancel"} {
			mark := "  "
			if strconv.Itoa(i+1) == a.selected {
				mark = "> "
			}
			lines = append(lines, mark+strconv.Itoa(i+1)+". "+option)
		}
		lines = append(lines, "", "  "+agyDialogHint, footer("esc to cancel"))
		return strings.Join(lines, "\n") + "\n"
	}
	if a.running {
		lines = append(lines, "⣷  Generating...")
	}
	lines = append(lines, rule, strings.TrimSpace("> "+a.input), rule)
	switch {
	case a.input == "/clear":
		lines = append(lines, "> /clear  Clear conversation and start a new one", "  "+agyMenuHint, footer("esc to cancel"))
	case a.input == agyPickupCommand:
		lines = append(lines, a.pickupRow, "  "+agyMenuHint, footer("esc to cancel"))
	case a.running:
		lines = append(lines, footer("esc to cancel"))
	case a.input != "":
		lines = append(lines, footer(""))
	default:
		lines = append(lines, footer(agyIdleFooter))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (a *agyTerminal) tmux(_ context.Context, socket string, args ...string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case args[0] == "display-message" && args[len(args)-1] == "#{pane_in_mode}":
		return "0\n", nil
	case args[0] == "display-message":
		return "%6 " + strconv.FormatInt(a.pid, 10) + "\n", nil
	case args[0] == "capture-pane":
		return a.screen(), nil
	case args[0] == "send-keys":
		key := strings.Join(args[3:], " ")
		a.keys = append(a.keys, key)
		switch {
		case a.dialog && strings.HasPrefix(key, "-l "):
			a.selected = strings.TrimPrefix(key, "-l ")
		case a.dialog && key == "Enter":
			a.dialog, a.chosen = false, append(a.chosen, a.selected)
		case strings.HasPrefix(key, "-l "):
			a.input += strings.TrimPrefix(key, "-l ")
		case key == "C-u":
			a.input = ""
		case key == "Enter":
			switch a.input {
			case "/clear":
				if !a.noClear {
					a.said, a.clears = nil, a.clears+1
				}
			case "":
			default:
				a.said = append(a.said, a.input)
			}
			a.input = ""
		}
		return "", nil
	}
	return "", errors.New("unexpected tmux call")
}

func (a *agyTerminal) target() CycleTarget {
	return CycleTarget{CycleID: "synthetic", Family: "agy", SessionID: "conv-a", HostPID: a.pid, HostStart: a.start,
		Socket: "/tmp/synthetic-socket", Pane: "%6", Epoch: "conv-a", Holds: true, Pickup: pickupSkillEntry(), State: cycleReady,
		Directory: a.dir, observedEpoch: "conv-a", stateRoot: a.state,
		tools: hostTools{tmux: a.tmux,
			processStart:   func(pid int) (int64, error) { return a.start, nil },
			processParents: func() (map[int]int, error) { return a.parents, nil },
			processCommand: func(pid int) (string, []string, error) {
				if c, ok := a.commands[pid]; ok {
					return c, nil, nil
				}
				return "", nil, errors.New("no process")
			},
			processEnvironment: func(pid int, name string) (string, error) {
				if name == "HOME" {
					return a.home, nil
				}
				return "", nil
			},
			processGroups: func() (map[int]platform.ProcessGroup, error) { return a.groups, nil }}}
}

func (a *agyTerminal) fence(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fences++
	if a.fences == a.failAt {
		return ErrCycleChanged
	}
	return nil
}

func (a *agyTerminal) sent() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.keys)
}

// Probe needs the platform, the pane with only non-agent descendants in the host's foreground
// group, the participant address, the session's own conversation as the latest epoch, Koinon's
// recorded status line and the effective pickup entry.
func TestAgyProbe(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "" || probe.Entry != PickupSkillName {
		t.Fatalf("probe: %+v", probe)
	}
	for name, change := range map[string]func(*CycleTarget){
		"participant_unheld": func(t *CycleTarget) { t.Holds = false },
		"epoch_unverified":   func(t *CycleTarget) { t.observedEpoch = "conv-other" },
		"pane_unproven": func(t *CycleTarget) {
			commands := map[int]string{5151: "/synthetic/agy", 5152: "/synthetic/agy"}
			t.tools.processCommand = func(pid int) (string, []string, error) { return commands[pid], nil, nil }
		},
		"pickup_entry_unverified": func(t *CycleTarget) { t.Directory = "" },
		"statusline_unverified":   func(t *CycleTarget) { t.stateRoot = "" },
	} {
		target := a.target()
		change(&target)
		if probe := (agyCycle{}).Probe(ctx, target); probe.Reason != name {
			t.Fatalf("%s: %+v", name, probe)
		}
	}
	// A process of the foreground group that is not the host's descendant is refused.
	a.groups[6000] = platform.ProcessGroup{Group: 5151, Terminal: 7, Foreground: 5151}
	a.commands[6000] = "/usr/bin/cat"
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "pane_unproven" {
		t.Fatalf("foreign group member: %+v", probe)
	}
	delete(a.groups, 6000)
	// After the reset the requester's old epoch is no longer checked.
	target := a.target()
	target.observedEpoch, target.SuccessorEpoch = "conv-a", "conv-b"
	if probe := (agyCycle{}).Probe(ctx, target); probe.Reason != "" {
		t.Fatalf("after the reset: %+v", probe)
	}
	// The status line: a declined record, or an entry that differs from the record.
	a.statusLine(t, false)
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "statusline_unverified" || probe.Step == "" {
		t.Fatalf("declined: %+v", probe)
	}
	a.statusLine(t, true)
	settings := filepath.Join(a.home, ".gemini", "antigravity-cli", "settings.json")
	os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/usr/local/bin/line.sh"}}`), 0o600)
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "statusline_unverified" {
		t.Fatalf("other entry: %+v", probe)
	}
	a.statusLine(t, true)
	// The personal skill must be the binary's; a workspace SKILL.md that would replace it must be
	// too, and an empty workspace skill directory is no shadow.
	skill := AgyPickupSkillPath(a.home)
	os.WriteFile(skill, []byte("other"), 0o600)
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "pickup_entry_differs" || !strings.Contains(probe.Step, skill) {
		t.Fatalf("differs: %+v", probe)
	}
	os.WriteFile(skill, pickupSkillEntry().Content, 0o600)
	for _, root := range agyShadowRoots {
		dir := filepath.Join(a.dir, root, "skills", PickupSkillName)
		os.MkdirAll(dir, 0o700)
		if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "" {
			t.Fatalf("%s empty directory: %+v", root, probe)
		}
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: koinon-pickup\ndescription: project\n---\n"), 0o600)
		if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "pickup_entry_shadowed" {
			t.Fatalf("%s shadow: %+v", root, probe)
		}
		os.WriteFile(filepath.Join(dir, "SKILL.md"), pickupSkillEntry().Content, 0o600)
		if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "" {
			t.Fatalf("%s equal copy: %+v", root, probe)
		}
	}
	previous := agyEvidencedPlatforms
	agyEvidencedPlatforms = map[string]bool{}
	if probe := (agyCycle{}).Probe(ctx, a.target()); probe.Reason != "platform_unverified" {
		t.Fatalf("platform: %+v", probe)
	}
	agyEvidencedPlatforms = previous
}

// Inspect keeps a draft, and reports a running turn, an open dialog and a pane in copy mode
// after leaving it.
func TestAgyInspect(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	if pane, err := (agyCycle{}).Inspect(ctx, a.target(), a.fence); err != nil || !pane.Idle {
		t.Fatalf("idle: %+v %v", pane, err)
	}
	a.input = "a draft of the maintainer"
	if pane, err := (agyCycle{}).Inspect(ctx, a.target(), a.fence); err != nil || pane.Idle || pane.Seen != "input_not_empty" || len(a.sent()) != 0 {
		t.Fatalf("draft: %+v %v %v", pane, err, a.sent())
	}
	a.input, a.running = "", true
	if pane, _ := (agyCycle{}).Inspect(ctx, a.target(), a.fence); pane.Seen != "turn_running" {
		t.Fatalf("running: %+v", pane)
	}
	a.running, a.dialog, a.selected = false, true, "1"
	if pane, _ := (agyCycle{}).Inspect(ctx, a.target(), a.fence); pane.Prompt != "native_dialog" {
		t.Fatalf("dialog: %+v", pane)
	}
}

// Clear types /clear only into an idle composer, submits it while its one menu row is selected,
// and reads back the cleared screen. A draft is never touched; a clear without effect is not
// received.
func TestAgyClear(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	a.said = []string{"an earlier prompt"}
	if received, err := (agyCycle{}).Clear(ctx, a.target(), a.fence); !received || err != nil || a.clears != 1 {
		t.Fatalf("clear: %v %v %v", received, err, a.sent())
	}
	if got := a.sent(); !slices.Equal(got, []string{"-l /clear", "Enter"}) {
		t.Fatalf("keys: %q", got)
	}
	a = newAgyTerminal(t)
	a.input = "a draft"
	if received, err := (agyCycle{}).Clear(ctx, a.target(), a.fence); received || !errors.Is(err, errAgyNotReady) || len(a.sent()) != 0 || a.input != "a draft" {
		t.Fatalf("draft: %v %v %q", received, err, a.sent())
	}
	a = newAgyTerminal(t)
	a.said, a.noClear = []string{"an earlier prompt"}, true
	if received, err := (agyCycle{}).Clear(ctx, a.target(), a.fence); received || !errors.Is(err, errAgySubmit) {
		t.Fatalf("no effect: %v %v", received, err)
	}
	// A fence that fails before the Enter erases the typed command.
	a = newAgyTerminal(t)
	a.said, a.failAt = []string{"an earlier prompt"}, 2
	if received, err := (agyCycle{}).Clear(ctx, a.target(), a.fence); received || !errors.Is(err, ErrCycleChanged) || a.input != "" || a.clears != 0 {
		t.Fatalf("fenced: %v %v %q %q", received, err, a.sent(), a.input)
	}
}

// NewEpoch reads the witnesses: nothing before the clear's receipt is recorded, exactly one new
// conversation until the reset is verified, the newest after it. The old conversation's stale
// report and an overflow never count.
func TestAgyNewEpoch(t *testing.T) {
	ctx := context.Background()
	base := CycleTarget{Epoch: "conv-a", State: cycleClearReceived}
	epoch := func(state string, overflow bool, list ...agyWitness) string {
		target := base
		target.State, target.witnesses = state, agyWitnesses{List: list, Overflow: overflow}
		got, err := (agyCycle{}).NewEpoch(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	old, next, later := agyWitness{ID: "conv-a", First: 148}, agyWitness{ID: "conv-b", First: 461}, agyWitness{ID: "conv-c", First: 900}
	for _, c := range []struct {
		state    string
		overflow bool
		list     []agyWitness
		want     string
	}{
		{cycleClearIntent, false, []agyWitness{old, next}, ""},
		{cycleClearReceived, false, nil, ""},
		{cycleClearReceived, false, []agyWitness{old}, ""},
		{cycleClearReceived, false, []agyWitness{old, next}, "conv-b"},
		{cycleClearReceived, false, []agyWitness{next, old}, "conv-b"},
		{cycleClearReceived, false, []agyWitness{old, next, later}, ""},
		{cycleClearReceived, true, []agyWitness{old, next}, ""},
		{cycleResetVerified, false, []agyWitness{old, next}, "conv-b"},
		{cycleResetVerified, false, []agyWitness{old, next, later}, "conv-c"},
	} {
		if got := epoch(c.state, c.overflow, c.list...); got != c.want {
			t.Fatalf("%s %v %+v: %q", c.state, c.overflow, c.list, got)
		}
	}
}

// Pickup enters /koinon-pickup only for the menu row with the canonical description, and its
// receipt is the submitted prompt. A shadow's row is refused and its typed text erased.
func TestAgyPickup(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	target := a.target()
	target.SuccessorEpoch, target.State = "conv-b", cycleResetVerified
	if received, err := (agyCycle{}).Pickup(ctx, target, a.fence); !received || err != nil || !slices.Equal(a.said, []string{agyPickupCommand}) {
		t.Fatalf("pickup: %v %v %q", received, err, a.sent())
	}
	a = newAgyTerminal(t)
	a.pickupRow = "> /koinon-pickup  SHADOW .agent/skills project pickup."
	target = a.target()
	target.SuccessorEpoch, target.State = "conv-b", cycleResetVerified
	if received, err := (agyCycle{}).Pickup(ctx, target, a.fence); received || !errors.Is(err, errAgyMenuEntry) || a.input != "" || len(a.said) != 0 {
		t.Fatalf("shadow row: %v %v %q", received, err, a.sent())
	}
	// A shadow file is refused before any key.
	a = newAgyTerminal(t)
	shadow := filepath.Join(a.dir, ".agent", "skills", PickupSkillName, "SKILL.md")
	os.MkdirAll(filepath.Dir(shadow), 0o700)
	os.WriteFile(shadow, []byte("project"), 0o600)
	target = a.target()
	target.SuccessorEpoch = "conv-b"
	if received, err := (agyCycle{}).Pickup(ctx, target, a.fence); received || err == nil || err.Error() != "pickup_entry_shadowed" || len(a.sent()) != 0 {
		t.Fatalf("shadow file: %v %v %q", received, err, a.sent())
	}
}

// Dialog and Choose: the digit moves the selection, which is read back, then one Enter confirms
// it; a changed dialog or an option that is not shown is refused.
func TestAgyDialogChoice(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	a.dialog, a.selected = true, "1"
	dialog, err := (agyCycle{}).Dialog(ctx, a.target())
	if err != nil || !slices.Equal(dialog.Options, []string{"1", "2", "3", "4"}) {
		t.Fatalf("dialog: %+v %v", dialog, err)
	}
	if ok, err := (agyCycle{}).Choose(ctx, a.target(), "other-digest", "4", a.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("changed: %v %v", ok, err)
	}
	if ok, err := (agyCycle{}).Choose(ctx, a.target(), dialog.Digest, "7", a.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("hidden option: %v %v", ok, err)
	}
	if ok, err := (agyCycle{}).Choose(ctx, a.target(), dialog.Digest, "4", a.fence); !ok || err != nil || !slices.Equal(a.chosen, []string{"4"}) {
		t.Fatalf("choose: %v %v %v", ok, err, a.sent())
	}
	if got := a.sent(); !slices.Equal(got, []string{"-l 4", "Enter"}) {
		t.Fatalf("keys: %q", got)
	}
	if _, err := (agyCycle{}).Dialog(ctx, a.target()); err == nil {
		t.Fatal("dialog after the choice")
	}
}

// The digit selects an option whose text then changes under the same request and number, as
// another dialog could show it: no Enter is sent (#521 review F2).
func TestAgyDialogChoiceKeepsItsMeaning(t *testing.T) {
	ctx := context.Background()
	a := newAgyTerminal(t)
	a.dialog, a.selected = true, "1"
	target := a.target()
	base := target.tools.tmux
	typed := false
	target.tools.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		out, err := base(ctx, socket, args...)
		if args[0] == "send-keys" && strings.Join(args[3:], " ") == "-l 4" {
			typed = true
		}
		if args[0] == "capture-pane" && typed {
			out = strings.ReplaceAll(out, "4. No, cancel", "4. Yes, run command and remember")
		}
		return out, err
	}
	dialog, err := (agyCycle{}).Dialog(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := (agyCycle{}).Choose(ctx, target, dialog.Digest, "4", a.fence); ok || err == nil || len(a.chosen) != 0 {
		t.Fatalf("changed option text: %v %v keys %q", ok, err, a.sent())
	}
	if got := a.sent(); !slices.Equal(got, []string{"-l 4"}) {
		t.Fatalf("keys: %q", got)
	}
}
