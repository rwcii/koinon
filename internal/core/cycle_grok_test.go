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

// The Grok cycle adapter (#515) against captured screens and a synthetic terminal. Nothing
// here starts Grok or reaches a real tmux server.

func grokCycleScreen(t *testing.T, name string) codexScreen {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "grok-screens", "cycle-"+name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return readCodexScreen(string(data))
}

// Captured Grok Build 1.0.50 screens (session IDs replaced): the composer, a draft, a running
// turn, the /new and /koinon-pickup menus, the screens after /new and after the pickup, and
// three approval dialogs.
func TestGrokCycleScreenFixtures(t *testing.T) {
	for name, want := range map[string]struct {
		text, session, notReady string
		pickups                 int
		menu                    []string
	}{
		"start":      {},
		"after-turn": {},
		"draft":      {text: "hello draft", notReady: "input_not_empty"},
		"working":    {notReady: "turn_running"},
		"new-menu":   {text: "/new", notReady: "input_not_empty", menu: []string{"/new", "/context-window", "/plan-view", "/koinon-peer-review", "/koinon-refusal-review"}},
		// With a long transcript Grok draws a scrollbar thumb in the last column (native run 1).
		"new-menu-scrolled": {text: "/new", notReady: "input_not_empty", menu: []string{"/new", "/context-window", "/plan-view", "/koinon-peer-review", "/koinon-refusal-review"}},
		"after-new":         {session: "01a17e66-0000-7000-8000-000000000001"},
		"after-new-2":       {session: "01a17e66-0000-7000-8000-000000000002"},
		"pickup-menu":       {text: "/koinon-pickup", session: "01a17e66-0000-7000-8000-000000000002", notReady: "input_not_empty", menu: []string{"/koinon-pickup"}},
		"after-pickup":      {notReady: "turn_running", pickups: 1},
		"after-dialog-1":    {notReady: "turn_running", pickups: 1},
	} {
		s := grokCycleScreen(t, name)
		text, _, found := grokComposer(grokText(s))
		var menu []string
		for _, row := range grokMenu(s) {
			menu = append(menu, row.name)
		}
		if !found || text != want.text || grokSession(s) != want.session || grokPrompts(s, grokPickupCommand) != want.pickups ||
			!slices.Equal(menu, want.menu) || grokDialog(s).dialog.Digest != "" {
			t.Fatalf("%s: %q %v session %q pickups %d menu %q", name, text, found, grokSession(s), grokPrompts(s, grokPickupCommand), menu)
		}
		if got := grokNotReady(CycleTarget{}, s); got != want.notReady {
			t.Fatalf("%s: not ready %q", name, got)
		}
	}
	// The /new row is selected with its description; the pickup row with the canonical one.
	if !grokMenuReady(grokCycleScreen(t, "new-menu"), "/new", func(row grokRow) bool { return row.description == "Start a new session" }) ||
		!grokMenuReady(grokCycleScreen(t, "new-menu-scrolled"), "/new", func(row grokRow) bool { return row.description == "Start a new session" }) ||
		!grokMenuReady(grokCycleScreen(t, "pickup-menu"), grokPickupCommand, grokPickupRow(pickupSkillEntry())) {
		t.Fatal("menu rows")
	}
	// The dialogs: 3 or 4 options, each with its own digest; the selection mark is not part of it.
	one, two, three := grokDialog(grokCycleScreen(t, "dialog-1")), grokDialog(grokCycleScreen(t, "dialog-2")), grokDialog(grokCycleScreen(t, "dialog-3"))
	if !slices.Equal(one.dialog.Options, []string{"1", "2", "3"}) || !slices.Equal(three.dialog.Options, []string{"1", "2", "3", "4"}) ||
		one.dialog.Digest == two.dialog.Digest || one.dialog.Lines[len(one.dialog.Lines)-2] != "2. Yes, proceed" {
		t.Fatalf("dialogs: %+v %+v", one.dialog, three.dialog)
	}
	if got := grokNotReady(CycleTarget{}, grokCycleScreen(t, "dialog-1")); got != "native_dialog" {
		t.Fatalf("dialog: %q", got)
	}
	// After the reset, only the successor's /new notice admits the pickup.
	if grokNotReady(CycleTarget{SuccessorEpoch: "01a17e66-0000-7000-8000-000000000002"}, grokCycleScreen(t, "after-new")) != "successor_not_cleared" ||
		grokNotReady(CycleTarget{SuccessorEpoch: "01a17e66-0000-7000-8000-000000000002"}, grokCycleScreen(t, "after-new-2")) != "" ||
		grokNotReady(CycleTarget{SuccessorEpoch: "01a17e66-0000-7000-8000-000000000002"}, grokCycleScreen(t, "after-turn")) != "successor_not_cleared" {
		t.Fatal("successor screen")
	}
}

// grokTerminal is a synthetic Grok pane: a transcript, the input box with the slash-command
// menu, an approval dialog, and the sessions that /new starts.
type grokTerminal struct {
	mu        sync.Mutex
	said      []string
	input     string
	running   bool
	dialog    bool
	chosen    []string
	keys      []string
	fences    int
	failAt    int
	pickupRow string // the menu row that /koinon-pickup shows
	session   string // the latest /new notice, "" before one
	news      int
	noNew     bool // /new does not take effect
	home, dir string
	state     string
	pid       int64
	start     int64
	env       map[string]string
	groups    map[int]platform.ProcessGroup
	parents   map[int]int
	commands  map[int]string
	args      map[int][]string
}

const grokWrapperCommand = "'/opt/koinon' hook grok-status"

func newGrokTerminal(t *testing.T) *grokTerminal {
	g := &grokTerminal{home: t.TempDir(), dir: t.TempDir(), state: t.TempDir(), pid: 6161, start: 99,
		pickupRow: "    ❯ /koinon-pickup " + pickupSkillDescription,
		env:       map[string]string{},
		groups:    map[int]platform.ProcessGroup{6161: {Group: 6161, Terminal: 7, Foreground: 6161}, 6162: {Group: 6162, Terminal: 7, Foreground: 6161}},
		parents:   map[int]int{6162: 6161},
		commands:  map[int]string{6161: "/usr/local/bin/grok", 6162: "/synthetic/koinon"}}
	g.env["HOME"] = g.home
	g.skill(t, filepath.Join(g.home, ".grok"), pickupSkillEntry().Content)
	g.statusLine(t, true)
	previous, wait := grokEvidencedPlatforms, grokKeyWait
	grokEvidencedPlatforms, grokKeyWait = map[string]bool{runtime.GOOS: true}, 300*time.Millisecond
	t.Cleanup(func() { grokEvidencedPlatforms, grokKeyWait = previous, wait })
	return g
}

// skill writes a koinon-pickup skill under root.
func (g *grokTerminal) skill(t *testing.T, root string, content []byte) string {
	path := filepath.Join(root, "skills", PickupSkillName, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// statusLine writes Grok's config.toml with Koinon's status-line command and setup's record of
// it; enabled false writes a declined record. extra is appended to the config.
func (g *grokTerminal) statusLine(t *testing.T, enabled bool, extra ...string) {
	config := filepath.Join(g.home, ".grok", "config.toml")
	os.MkdirAll(filepath.Dir(config), 0o700)
	text := "[ui.status_line]\ntype = \"command\"\ncommand = \"" + strings.ReplaceAll(grokWrapperCommand, `"`, `\"`) + "\"\n" + strings.Join(extra, "")
	if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	wrapper, _ := json.Marshal(map[string]string{"type": "command", "command": grokWrapperCommand})
	state := map[bool]string{true: "enabled", false: "declined"}[enabled]
	record, _ := json.Marshal(map[string]any{"state": state, "settings_file": config, "wrapper": json.RawMessage(wrapper)})
	if err := os.WriteFile(filepath.Join(g.state, "grok-statusline.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (g *grokTerminal) screen() string {
	rule := "  " + strings.Repeat("─", 60)
	lines := []string{"", "  ~/w2                                        1.8K / 256K"}
	if g.session != "" {
		lines = append(lines, "", "     Session "+g.session+", use /dashboard to switch between sessions")
	}
	for _, said := range g.said {
		lines = append(lines, "", "     ❯ "+said+"                                        5:27 PM", "", "     ok")
	}
	if g.dialog {
		lines = append(lines, "", "  ┃", "  ┃  Run uname", "  ┃  uname -s", "  ┃",
			"  ┃  1 (●) Yes, and don't ask again for anything (always-approve mode)", "  ┃  2 (○) Yes, proceed",
			"  ┃  3 (○) No, reject (type to add feedback)", "  ┃", "", "  1/3:select  │  Tab:next option  │  Ctrl+c:cancel")
		return strings.Join(lines, "\n") + "\n"
	}
	if g.running {
		lines = append(lines, "   ⠙ Thinking… 1.1s")
	}
	switch {
	case g.input == "/new":
		lines = append(lines, rule+"5─", "    ❯ /new                   Start a new session", "      /context-window        Set the context window", rule)
	case g.input == grokPickupCommand:
		lines = append(lines, rule+"1─", g.pickupRow, rule)
	}
	lines = append(lines, "  ╭"+strings.Repeat("─", 58)+"╮", "  │ ❯ "+g.input, "  ╰"+strings.Repeat("─", 58)+"╯", "")
	switch {
	case g.running:
		lines = append(lines, "  Shift+Tab:mode  │  Ctrl+c:cancel  │  Ctrl+x:shortcuts")
	case g.input != "":
		lines = append(lines, "  Enter:send  │  Alt+Enter:newline  │  Shift+Tab:mode  │  Ctrl+x:shortcuts")
	default:
		lines = append(lines, "  Shift+Tab:mode  │  Ctrl+x:shortcuts")
	}
	return strings.Join(lines, "\n") + "\n"
}

func (g *grokTerminal) tmux(_ context.Context, socket string, args ...string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case args[0] == "display-message":
		return "%6 " + strconv.FormatInt(g.pid, 10) + "\n", nil
	case args[0] == "capture-pane":
		return g.screen(), nil
	case args[0] == "send-keys":
		key := strings.Join(args[3:], " ")
		g.keys = append(g.keys, key)
		switch {
		case g.dialog && strings.HasPrefix(key, "-l "):
			// The digit answers the dialog at once.
			g.dialog, g.chosen = false, append(g.chosen, strings.TrimPrefix(key, "-l "))
		case strings.HasPrefix(key, "-l "):
			g.input += strings.TrimPrefix(key, "-l ")
		case key == "C-u":
			g.input = ""
		case key == "Enter":
			switch g.input {
			case "/new":
				if !g.noNew {
					g.news++
					g.said, g.session = nil, "01a17e66-0000-7000-8000-00000000000"+strconv.Itoa(g.news)
				}
			case "":
			default:
				g.said, g.session = append(g.said, g.input), ""
			}
			g.input = ""
		}
		return "", nil
	}
	return "", errors.New("unexpected tmux call")
}

func (g *grokTerminal) target() CycleTarget {
	return CycleTarget{CycleID: "synthetic", Family: "grok", SessionID: "grok-a", HostPID: g.pid, HostStart: g.start,
		Socket: "/tmp/synthetic-socket", Pane: "%6", Epoch: "grok-a", Holds: true, Pickup: pickupSkillEntry(), State: cycleReady,
		Directory: g.dir, observedEpoch: "grok-a", stateRoot: g.state,
		tools: hostTools{tmux: g.tmux,
			processStart:   func(pid int) (int64, error) { return g.start, nil },
			processParents: func() (map[int]int, error) { return g.parents, nil },
			processCommand: func(pid int) (string, []string, error) {
				if c, ok := g.commands[pid]; ok {
					return c, g.args[pid], nil
				}
				return "", nil, errors.New("no process")
			},
			processEnvironment: func(pid int, name string) (string, error) { return g.env[name], nil },
			processGroups:      func() (map[int]platform.ProcessGroup, error) { return g.groups, nil }}}
}

func (g *grokTerminal) fence(context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fences++
	if g.fences == g.failAt {
		return ErrCycleChanged
	}
	return nil
}

func (g *grokTerminal) sent() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.keys)
}

// Probe needs the platform, the host leading the pane's foreground group, the participant
// address, the session itself as the latest epoch, Koinon's recorded status line and the
// effective pickup entry.
func TestGrokProbe(t *testing.T) {
	ctx := context.Background()
	g := newGrokTerminal(t)
	if probe := (grokCycle{}).Probe(ctx, g.target()); probe.Reason != "" || probe.Entry != PickupSkillName {
		t.Fatalf("probe: %+v", probe)
	}
	for name, change := range map[string]func(*CycleTarget){
		"participant_unheld":      func(t *CycleTarget) { t.Holds = false },
		"epoch_unverified":        func(t *CycleTarget) { t.observedEpoch = "grok-other" },
		"pickup_entry_unverified": func(t *CycleTarget) { t.Directory = "" },
		"statusline_unverified":   func(t *CycleTarget) { t.stateRoot = "" },
		"pane_unproven": func(t *CycleTarget) {
			// The pane's shell leads the foreground group instead of the host.
			groups := map[int]platform.ProcessGroup{6161: {Group: 6161, Terminal: 7, Foreground: 6160}}
			t.tools.processGroups = func() (map[int]platform.ProcessGroup, error) { return groups, nil }
		},
		"native_registry_unavailable": func(t *CycleTarget) {
			// A failed read of the host's GROK_HOME is not an unset variable.
			env := t.tools.processEnvironment
			t.tools.processEnvironment = func(pid int, name string) (string, error) {
				if name == "GROK_HOME" {
					return "", errors.New("environment unreadable")
				}
				return env(pid, name)
			}
		},
	} {
		target := g.target()
		change(&target)
		if probe := (grokCycle{}).Probe(ctx, target); probe.Reason != name {
			t.Fatalf("%s: %+v", name, probe)
		}
	}
	// The pinned host must be a Grok terminal session: grok-native or the grok link, never a
	// shell that the same PID and start now run, Grok's leader, or an unreadable command.
	for _, host := range []struct {
		executable string
		args       []string
		ok         bool
	}{
		{"/usr/local/bin/grok", []string{"grok", "--session-id", "grok-a"}, true},
		{"/home/agent/.grok/bin/grok-native", []string{"grok-native"}, true},
		{"/bin/bash", []string{"bash"}, false},
		{"/home/agent/.grok/bin/grok-native", []string{"grok-native", "agent", "leader"}, false},
		{"", nil, false},
	} {
		h := newGrokTerminal(t)
		if host.executable == "" {
			delete(h.commands, 6161)
		} else {
			h.commands[6161], h.args = host.executable, map[int][]string{6161: host.args}
		}
		want := map[bool]string{true: "", false: "pane_unproven"}[host.ok]
		if probe := (grokCycle{}).Probe(ctx, h.target()); probe.Reason != want {
			t.Fatalf("host %q %q: %+v", host.executable, host.args, probe)
		}
	}
	// After the reset the requester's old epoch is no longer checked.
	target := g.target()
	target.SuccessorEpoch = "grok-b"
	if probe := (grokCycle{}).Probe(ctx, target); probe.Reason != "" {
		t.Fatalf("after the reset: %+v", probe)
	}
	// The status line: a declined record, or a command that differs from the record.
	g.statusLine(t, false)
	if probe := (grokCycle{}).Probe(ctx, g.target()); probe.Reason != "statusline_unverified" || probe.Step == "" {
		t.Fatalf("declined: %+v", probe)
	}
	g.statusLine(t, true)
	os.WriteFile(filepath.Join(g.home, ".grok", "config.toml"), []byte("[ui.status_line]\ntype = \"command\"\ncommand = \"line.sh\"\n"), 0o600)
	if probe := (grokCycle{}).Probe(ctx, g.target()); probe.Reason != "statusline_unverified" {
		t.Fatalf("other command: %+v", probe)
	}
	// A GROK_HOME of the host is where the status line and the entry are read.
	g.statusLine(t, true)
	custom := t.TempDir()
	g.env["GROK_HOME"] = custom
	if probe := (grokCycle{}).Probe(ctx, g.target()); probe.Reason != "statusline_unverified" {
		t.Fatalf("custom home without its config: %+v", probe)
	}
	delete(g.env, "GROK_HOME")
	previous := grokEvidencedPlatforms
	grokEvidencedPlatforms = map[string]bool{}
	if probe := (grokCycle{}).Probe(ctx, g.target()); probe.Reason != "platform_unverified" {
		t.Fatalf("platform: %+v", probe)
	}
	grokEvidencedPlatforms = previous
}

// The effective koinon-pickup entry (N1): ~/.grok wins over ~/.agents and ~/.claude, the
// working directory's .agents copy over every user copy, and copies whose order is not verified
// must be the installed skill. [skills] disabled hides it; an ignored copy is no candidate.
func TestGrokPickupEntry(t *testing.T) {
	g := newGrokTerminal(t)
	entry := func() string { return grokPickupEntry(g.target()).Reason }
	other := []byte("---\nname: koinon-pickup\ndescription: other\n---\n")
	installed := filepath.Join(g.home, ".grok", "skills", PickupSkillName, "SKILL.md")
	// A lower user copy that differs is replaced by ~/.grok and is not checked.
	g.skill(t, filepath.Join(g.home, ".agents"), other)
	g.skill(t, filepath.Join(g.home, ".claude"), other)
	if got := entry(); got != "" {
		t.Fatalf("lower copies: %q", got)
	}
	// Without the ~/.grok copy, ~/.agents is the effective one.
	os.Remove(installed)
	if got := entry(); got != "pickup_entry_shadowed" {
		t.Fatalf("agents copy differs: %q", got)
	}
	g.skill(t, filepath.Join(g.home, ".agents"), pickupSkillEntry().Content)
	if got := entry(); got != "" {
		t.Fatalf("agents copy equal: %q", got)
	}
	os.Remove(filepath.Join(g.home, ".agents", "skills", PickupSkillName, "SKILL.md"))
	os.Remove(filepath.Join(g.home, ".claude", "skills", PickupSkillName, "SKILL.md"))
	if got := entry(); got != "pickup_entry_missing" {
		t.Fatalf("no copy: %q", got)
	}
	g.skill(t, filepath.Join(g.home, ".grok"), other)
	if probe := grokPickupEntry(g.target()); probe.Reason != "pickup_entry_differs" || !strings.Contains(probe.Step, installed) {
		t.Fatalf("installed copy differs: %+v", probe)
	}
	g.skill(t, filepath.Join(g.home, ".grok"), pickupSkillEntry().Content)
	// Project copies, up to the repository root, and a commands file must equal the skill.
	repo := filepath.Join(g.dir, "repo")
	work := filepath.Join(repo, "sub")
	os.MkdirAll(filepath.Join(repo, ".git"), 0o700)
	os.MkdirAll(work, 0o700)
	target := g.target()
	target.Directory = work
	for _, path := range []string{
		filepath.Join(work, ".agents", "skills", PickupSkillName, "SKILL.md"),
		filepath.Join(repo, ".claude", "skills", PickupSkillName, "SKILL.md"),
		filepath.Join(work, ".grok", "commands", PickupSkillName+".md"),
		filepath.Join(g.home, ".cursor", "skills", PickupSkillName, "SKILL.md"),
	} {
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, other, 0o600)
		if got := grokPickupEntry(target).Reason; got != "pickup_entry_shadowed" {
			t.Fatalf("%s: %q", path, got)
		}
		os.WriteFile(path, pickupSkillEntry().Content, 0o600)
		if got := grokPickupEntry(target).Reason; got != "" {
			t.Fatalf("%s equal: %q", path, got)
		}
		os.Remove(path)
	}
	// The working directory's .agents copy wins over every user copy: it needs no user copy, and
	// a differing user copy loses to it. A differing .agents copy is the effective entry.
	winner := filepath.Join(work, ".agents", "skills", PickupSkillName, "SKILL.md")
	os.MkdirAll(filepath.Dir(winner), 0o700)
	os.WriteFile(winner, pickupSkillEntry().Content, 0o600)
	os.Remove(installed)
	if got := grokPickupEntry(target).Reason; got != "" {
		t.Fatalf("project copy only: %q", got)
	}
	g.skill(t, filepath.Join(g.home, ".grok"), other)
	if got := grokPickupEntry(target).Reason; got != "" {
		t.Fatalf("project copy over a differing user copy: %q", got)
	}
	os.WriteFile(winner, other, 0o600)
	if probe := grokPickupEntry(target); probe.Reason != "pickup_entry_shadowed" || !strings.Contains(probe.Step, winner) {
		t.Fatalf("differing project copy: %+v", probe)
	}
	os.Remove(winner)
	// A copy whose order is not verified is the entry without a user copy, but does not
	// outrank a differing user copy.
	unordered := filepath.Join(repo, ".claude", "skills", PickupSkillName, "SKILL.md")
	os.WriteFile(unordered, pickupSkillEntry().Content, 0o600)
	if probe := grokPickupEntry(target); probe.Reason != "pickup_entry_differs" {
		t.Fatalf("unordered copy over a differing user copy: %+v", probe)
	}
	os.Remove(installed)
	if got := grokPickupEntry(target).Reason; got != "" {
		t.Fatalf("unordered copy only: %q", got)
	}
	os.Remove(unordered)
	g.skill(t, filepath.Join(g.home, ".grok"), pickupSkillEntry().Content)
	// A copy above the repository root is not Grok's.
	above := filepath.Join(g.dir, ".agents", "skills", PickupSkillName, "SKILL.md")
	os.MkdirAll(filepath.Dir(above), 0o700)
	os.WriteFile(above, other, 0o600)
	if got := grokPickupEntry(target).Reason; got != "" {
		t.Fatalf("above the repository: %q", got)
	}
	os.Remove(above)
	// [skills]: disabled refuses; an ignored differing copy is no candidate; a copy in paths
	// must equal the skill; a config that is not TOML proves nothing.
	g.statusLine(t, true, "[skills]\ndisabled = [\"koinon-pickup\"]\n")
	if got := entry(); got != "pickup_entry_disabled" {
		t.Fatalf("disabled: %q", got)
	}
	g.skill(t, filepath.Join(g.home, ".grok"), other)
	g.skill(t, filepath.Join(g.home, ".agents"), pickupSkillEntry().Content)
	g.statusLine(t, true, "[skills]\nignore = [\"~/.grok/skills/koinon-pickup\"]\n")
	if got := entry(); got != "" {
		t.Fatalf("ignored copy: %q", got)
	}
	g.skill(t, filepath.Join(g.home, ".grok"), pickupSkillEntry().Content)
	extra := filepath.Join(g.home, "team-skills")
	os.MkdirAll(filepath.Join(extra, PickupSkillName), 0o700)
	os.WriteFile(filepath.Join(extra, PickupSkillName, "SKILL.md"), other, 0o600)
	g.statusLine(t, true, "[skills]\npaths = [\"~/team-skills\"]\n")
	if got := entry(); got != "pickup_entry_shadowed" {
		t.Fatalf("paths copy: %q", got)
	}
	g.statusLine(t, true, "[skills\n")
	if got := entry(); got != "pickup_entry_unverified" {
		t.Fatalf("invalid config: %q", got)
	}
}

// Inspect keeps a draft, and reports a running turn and an open dialog.
func TestGrokInspect(t *testing.T) {
	ctx := context.Background()
	g := newGrokTerminal(t)
	if pane, err := (grokCycle{}).Inspect(ctx, g.target(), g.fence); err != nil || !pane.Idle {
		t.Fatalf("idle: %+v %v", pane, err)
	}
	g.input = "a draft of the maintainer"
	if pane, err := (grokCycle{}).Inspect(ctx, g.target(), g.fence); err != nil || pane.Seen != "input_not_empty" || len(g.sent()) != 0 {
		t.Fatalf("draft: %+v %v %v", pane, err, g.sent())
	}
	g.input, g.running = "", true
	if pane, _ := (grokCycle{}).Inspect(ctx, g.target(), g.fence); pane.Seen != "turn_running" {
		t.Fatalf("running: %+v", pane)
	}
	g.running, g.dialog = false, true
	if pane, _ := (grokCycle{}).Inspect(ctx, g.target(), g.fence); pane.Prompt != "native_dialog" {
		t.Fatalf("dialog: %+v", pane)
	}
}

// Clear types /new only into an idle composer, submits it while its row is selected, and reads
// back the notice of a new session. A draft is never touched; a /new without effect, or a
// notice that was there before, is not received.
func TestGrokClear(t *testing.T) {
	ctx := context.Background()
	g := newGrokTerminal(t)
	g.said = []string{"an earlier prompt"}
	if received, err := (grokCycle{}).Clear(ctx, g.target(), g.fence); !received || err != nil || g.news != 1 {
		t.Fatalf("clear: %v %v %v", received, err, g.sent())
	}
	if got := g.sent(); !slices.Equal(got, []string{"-l /new", "Enter"}) {
		t.Fatalf("keys: %q", got)
	}
	g = newGrokTerminal(t)
	g.input = "a draft"
	if received, err := (grokCycle{}).Clear(ctx, g.target(), g.fence); received || !errors.Is(err, errAgyNotReady) || len(g.sent()) != 0 || g.input != "a draft" {
		t.Fatalf("draft: %v %v %q", received, err, g.sent())
	}
	// An earlier session's notice still on the screen is no receipt.
	g = newGrokTerminal(t)
	g.session, g.noNew = "01a17e66-0000-7000-8000-000000000009", true
	if received, err := (grokCycle{}).Clear(ctx, g.target(), g.fence); received || !errors.Is(err, errAgySubmit) {
		t.Fatalf("stale notice: %v %v", received, err)
	}
	// Nor is the notice of the epoch itself.
	g = newGrokTerminal(t)
	target := g.target()
	target.Epoch = "01a17e66-0000-7000-8000-000000000001"
	if received, err := (grokCycle{}).Clear(ctx, target, g.fence); received || !errors.Is(err, errAgySubmit) {
		t.Fatalf("epoch notice: %v %v", received, err)
	}
	// A host that execs another program after the typed command gets no Enter.
	g = newGrokTerminal(t)
	exec := func(context.Context) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.fences++
		if g.fences == 1 {
			g.commands[6161] = "/bin/bash"
		}
		return nil
	}
	if received, err := (grokCycle{}).Clear(ctx, g.target(), exec); received || !errors.Is(err, errAgyUnproven) || !slices.Equal(g.sent(), []string{"-l /new"}) {
		t.Fatalf("host changed: %v %v %q", received, err, g.sent())
	}
	// A fence that fails before the Enter erases the typed command.
	g = newGrokTerminal(t)
	g.failAt = 2
	if received, err := (grokCycle{}).Clear(ctx, g.target(), g.fence); received || !errors.Is(err, ErrCycleChanged) || g.input != "" || g.news != 0 {
		t.Fatalf("fenced: %v %v %q %q", received, err, g.sent(), g.input)
	}
}

// Pickup enters /koinon-pickup only on the successor's screen and for the menu row with the
// canonical description, and its receipt is one more submitted pickup line.
func TestGrokPickup(t *testing.T) {
	ctx := context.Background()
	g := newGrokTerminal(t)
	g.session = "grok-b"
	target := g.target()
	target.SuccessorEpoch, target.State = "grok-b", cycleResetVerified
	if received, err := (grokCycle{}).Pickup(ctx, target, g.fence); !received || err != nil || !slices.Equal(g.said, []string{grokPickupCommand}) {
		t.Fatalf("pickup: %v %v %q", received, err, g.sent())
	}
	// Another session's screen gets no key.
	g = newGrokTerminal(t)
	g.session = "grok-c"
	target = g.target()
	target.SuccessorEpoch = "grok-b"
	if received, err := (grokCycle{}).Pickup(ctx, target, g.fence); received || !errors.Is(err, errAgyNotReady) || len(g.sent()) != 0 {
		t.Fatalf("other session: %v %v %q", received, err, g.sent())
	}
	// A shadow's row is refused and the typed text erased.
	g = newGrokTerminal(t)
	g.session, g.pickupRow = "grok-b", "    ❯ /koinon-pickup SHADOW project pickup."
	target = g.target()
	target.SuccessorEpoch = "grok-b"
	if received, err := (grokCycle{}).Pickup(ctx, target, g.fence); received || !errors.Is(err, errAgyMenuEntry) || g.input != "" || len(g.said) != 0 {
		t.Fatalf("shadow row: %v %v %q", received, err, g.sent())
	}
	// A disabled entry is refused before any key.
	g = newGrokTerminal(t)
	g.session = "grok-b"
	g.statusLine(t, true, "[skills]\ndisabled = [\"koinon-pickup\"]\n")
	target = g.target()
	target.SuccessorEpoch = "grok-b"
	if received, err := (grokCycle{}).Pickup(ctx, target, g.fence); received || err == nil || err.Error() != "pickup_entry_disabled" || len(g.sent()) != 0 {
		t.Fatalf("disabled: %v %v %q", received, err, g.sent())
	}
}

// Dialog and Choose: one digit answers the dialog at once, after a fresh read shows the same
// dialog; no Enter follows. A changed dialog or an option that is not shown is refused.
func TestGrokDialogChoice(t *testing.T) {
	ctx := context.Background()
	g := newGrokTerminal(t)
	g.dialog = true
	dialog, err := (grokCycle{}).Dialog(ctx, g.target())
	if err != nil || !slices.Equal(dialog.Options, []string{"1", "2", "3"}) {
		t.Fatalf("dialog: %+v %v", dialog, err)
	}
	if ok, err := (grokCycle{}).Choose(ctx, g.target(), "other-digest", "2", g.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("changed: %v %v", ok, err)
	}
	if ok, err := (grokCycle{}).Choose(ctx, g.target(), dialog.Digest, "7", g.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("hidden option: %v %v", ok, err)
	}
	if ok, err := (grokCycle{}).Choose(ctx, g.target(), dialog.Digest, "2", g.fence); !ok || err != nil || !slices.Equal(g.chosen, []string{"2"}) {
		t.Fatalf("choose: %v %v %v", ok, err, g.sent())
	}
	if got := g.sent(); !slices.Equal(got, []string{"-l 2"}) {
		t.Fatalf("keys: %q", got)
	}
	if _, err := (grokCycle{}).Dialog(ctx, g.target()); err == nil {
		t.Fatal("dialog after the choice")
	}
	if (grokCycle{}).Answerable("anything") {
		t.Fatal("answerable")
	}
}
