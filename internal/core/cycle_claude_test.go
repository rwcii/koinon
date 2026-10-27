package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The Claude Code cycle adapter (#196, chunk 03) against captured screens and a synthetic
// terminal. Nothing here starts Claude Code or reaches a real tmux server.

// Captured Claude Code 2.1.296 screens (paths replaced): dim placeholder and suggestion text is
// an empty input line; typed text, menus and a dialog are read as they show.
func TestClaudeScreenFixtures(t *testing.T) {
	read := func(name string) claudeScreen {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "claude-screens", name+".ansi"))
		if err != nil {
			t.Fatal(err)
		}
		return parseClaudeScreen(string(data))
	}
	for name, want := range map[string]string{"start-placeholder": "", "draft": "Reply with the single word ok.", "after-turn2": "",
		"clear-menu": "/clear", "clear-typed": "/clear", "after-erase": "", "after-add-dir": "", "pickup-menu-loader": "/pickup"} {
		if s := read(name); !s.found || s.input != want || s.dialog != "" || s.agents || s.turn {
			t.Fatalf("%s: found %v input %q dialog %q agents %v turn %v", name, s.found, s.input, s.dialog, s.agents, s.turn)
		}
	}
	// Claude Code 2.1.311 and 2.1.312 with a background subagent running (#533): the turn ended
	// and its wait for the subagent shows, the screen after /clear there, and a turn in progress.
	for name, turn := range map[string]bool{"background-wait": false, "background-after-clear": false, "background-turn": true} {
		if s := read(name); !s.found || s.input != "" || s.dialog != "" || !s.agents || s.turn != turn {
			t.Fatalf("%s: found %v input %q dialog %q agents %v turn %v", name, s.found, s.input, s.dialog, s.agents, s.turn)
		}
	}
	// The add-directory dialog is a native dialog like any other: the adapter answers none.
	if s := read("add-dir-dialog"); s.dialog != "native_dialog" {
		t.Fatalf("add-dir dialog: %q", s.dialog)
	}
	// The menu row of a typed skill command: the captured entry (an earlier loader skill named
	// pickup) is listed first, the selected row; another description does not match it, and
	// neither does another command.
	menu := menuFirst(read("pickup-menu-loader"), "/pickup")
	captured := PickupEntry{Description: "Resume work from this agent's newest handoff. Session-local loader for a managed context cycle; the procedure is the canonical catalog skill."}
	catalog := PickupEntry{Description: "Resume work from this agent's newest handoff in its own directory under the Git-ignored _handoff/."}
	if !describes(menu, captured) || describes(menu, catalog) || menuFirst(read("pickup-menu-loader"), "/pick") != "" {
		t.Fatalf("menu entry %q", menu)
	}
	// The clear menu closes with Escape: its row is gone from the typed screen.
	if menuFirst(read("clear-menu"), "/clear") == "" || menuFirst(read("clear-typed"), "/clear") != "" {
		t.Fatal("clear menu")
	}
	// A scope after the description, and the ellipsis of a long one, are not part of it.
	for _, row := range []string{"Koinon's managed-cycle entry for the canonical pickup… (user)", "Koinon's managed-cycle entry (project)",
		pickupSkillDescription} {
		if !describes(row, pickupSkillEntry()) {
			t.Fatalf("row %q", row)
		}
	}
	if describes("Resume work (user)", pickupSkillEntry()) || describes("(user)", pickupSkillEntry()) {
		t.Fatal("another description matched")
	}
}

// claudeTerminal is a synthetic Claude Code pane: an input line, a /koinon-pickup menu, a
// dialog, copy mode, and a registry record that /clear changes.
type claudeTerminal struct {
	mu      sync.Mutex
	t       *testing.T
	input   string
	dialog  bool
	mode    bool
	asks    bool // a native permission dialog
	status  string
	epoch   string
	cleared int
	said    []string // transcript lines after the synthetic transcript's first line
	menu    string   // the description that /koinon-pickup lists first
	closed  bool     // Escape closed the menu since the last typed text
	keys    []string
	at      []time.Time // when each key arrived
	fences  int
	failAt  int // the fence call that fails, 0 for none
	home    string
	config  string // the host's CLAUDE_CONFIG_DIR, or ""
	pid     int64
	start   int64
	args    []string
	footer  []string // lines below the input box, such as the agent panel
}

func newClaudeTerminal(t *testing.T) *claudeTerminal {
	term := &claudeTerminal{t: t, status: "idle", epoch: "synthetic-epoch-1", home: t.TempDir(), pid: 4242, start: 77,
		args: []string{"claude"}}
	term.write()
	previous, wait, settle := claudeEvidencedPlatforms, claudeKeyWait, claudeEscapeSettle
	claudeEvidencedPlatforms, claudeKeyWait, claudeEscapeSettle = map[string]bool{runtime.GOOS: true}, 300*time.Millisecond, 0
	t.Cleanup(func() { claudeEvidencedPlatforms, claudeKeyWait, claudeEscapeSettle = previous, wait, settle })
	return term
}

// claudeStartText is the procStart that Claude Code writes for a process start: the stat
// field on Linux, `ps -o lstart=` in UTC on macOS.
func claudeStartText(goos string, start int64) string {
	if goos == "darwin" {
		return time.UnixMicro(start).UTC().Format("Mon Jan _2 15:04:05 2006")
	}
	return strconv.FormatInt(start, 10)
}

// claudeSameStart compares each platform's own form; macOS has whole seconds.
func TestClaudeSameStart(t *testing.T) {
	for _, c := range []struct {
		goos, recorded string
		start          int64
		want           bool
	}{
		{"linux", "43460610", 43460610, true},
		{"linux", "43460611", 43460610, false},
		{"linux", "Sat Oct  3 22:13:05 2026", 43460610, false},
		{"darwin", "Sat Oct  3 22:13:05 2026", 1791065585_999999, true},
		{"darwin", "Sat Oct 3 22:13:05 2026", 1791065585_000000, true},
		{"darwin", "Sat Oct  3 22:13:05 2026", 1791065586_000000, false},
		{"darwin", "1791065585", 1791065585_000000, false},
		{"darwin", claudeStartText("darwin", 1791929754_123456), 1791929754_123456, true},
	} {
		if got := claudeSameStart(c.goos, c.recorded, c.start); got != c.want {
			t.Errorf("%s %q %d: %v", c.goos, c.recorded, c.start, got)
		}
	}
}

// write stores the registry record of the synthetic host.
func (c *claudeTerminal) write() {
	dir := filepath.Join(c.home, ".claude", "sessions")
	os.MkdirAll(dir, 0o700)
	data, _ := json.Marshal(map[string]any{"pid": c.pid, "sessionId": c.epoch, "procStart": claudeStartText(runtime.GOOS, c.start),
		"version": "2.1.296", "status": c.status, "entrypoint": "cli"})
	if err := os.WriteFile(filepath.Join(dir, strconv.FormatInt(c.pid, 10)+".json"), data, 0o600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *claudeTerminal) screen() string {
	if c.asks {
		return "● synthetic transcript\n────────\n Bash command\n   synthetic command\n Do you want to proceed?\n ❯ 1. Yes\n   2. No, and tell Claude what to do differently\n"
	}
	if c.dialog {
		return "   Add directory to workspace\n   /tmp/synthetic/loader\n   ❯ 1. Yes, for this session\n     2. Yes, and remember this directory\n     3. No\n   Enter to confirm · Esc to cancel\n"
	}
	var b strings.Builder
	b.WriteString("● synthetic transcript\n")
	for _, line := range c.said {
		b.WriteString(line + "\n")
	}
	if strings.HasPrefix(c.input, "/koinon-pickup") && c.menu != "" && !c.closed {
		b.WriteString("  ❯ /koinon-pickup              " + c.menu + "\n")
	}
	b.WriteString("────────\n\x1b[39m" + claudeInputMark)
	if c.input == "" {
		b.WriteString("\x1b[2mwrite a handoff\x1b[0m")
	} else {
		b.WriteString(c.input)
	}
	b.WriteString("\n────────\n")
	for _, line := range c.footer {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (c *claudeTerminal) tmux(_ context.Context, socket string, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case args[0] == "display-message" && args[len(args)-1] == "#{pane_in_mode}":
		if c.mode {
			return "1\n", nil
		}
		return "0\n", nil
	case args[0] == "display-message":
		return "%3 " + strconv.FormatInt(c.pid, 10) + "\n", nil
	case args[0] == "capture-pane":
		return c.screen(), nil
	case args[0] == "send-keys":
		key := strings.Join(args[3:], " ")
		c.keys = append(c.keys, key)
		c.at = append(c.at, time.Now())
		switch {
		case key == "-X cancel":
			c.mode = false
		case strings.HasPrefix(key, "-l "):
			text := strings.TrimPrefix(key, "-l ")
			if c.asks && (text == "1" || text == "2") {
				c.asks = false
			} else if c.dialog && text == "1" {
				c.dialog = false
				c.input = ""
			} else {
				c.input += text
				c.closed = false
			}
		case key == "Escape":
			c.closed = true
		case strings.HasPrefix(key, "BSpace"):
			for range strings.Fields(key) {
				if c.input != "" {
					c.input = c.input[:len(c.input)-1]
				}
			}
		case key == "Enter":
			switch {
			case c.input == "/clear":
				c.cleared++
				c.epoch = "synthetic-epoch-" + strconv.Itoa(c.cleared+1)
				c.write()
				c.input = ""
			case strings.HasPrefix(c.input, "/add-dir "):
				c.dialog = true
			default:
				c.input = ""
			}
		}
		return "", nil
	}
	return "", errors.New("unexpected tmux call")
}

func (c *claudeTerminal) target(t *testing.T) CycleTarget {
	return CycleTarget{CycleID: "synthetic", Family: "claude", SessionID: "synthetic-a", HostPID: c.pid, HostStart: c.start,
		Socket: "/tmp/synthetic-socket", Pane: "%3", Epoch: "synthetic-epoch-1", Pickup: pickupSkillEntry(),
		tools: hostTools{tmux: c.tmux,
			processStart:   func(pid int) (int64, error) { return c.start, nil },
			processParents: func() (map[int]int, error) { return map[int]int{}, nil },
			processCommand: func(pid int) (string, []string, error) { return "/synthetic/claude", c.args, nil },
			processEnvironment: func(pid int, name string) (string, error) {
				switch name {
				case "HOME":
					return c.home, nil
				case "CLAUDE_CONFIG_DIR":
					return c.config, nil
				}
				return "", nil
			}}}
}

// fence counts the calls and fails the configured one.
func (c *claudeTerminal) fence(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fences++
	if c.fences == c.failAt {
		return ErrCycleChanged
	}
	return nil
}

func writeEntry(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Probe checks only the koinon-pickup skill under the host's configuration root, byte for byte
// against the binary's skill; a missing or different file names the install command. Project
// and personal pickup skills are not read (#406).
func TestClaudeProbe(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	skill := func() string { return filepath.Join(term.home, ".claude", "skills", "koinon-pickup", "SKILL.md") }
	step := func(path string) string { return "run koinon install to install the koinon-pickup skill at " + path }
	for _, c := range []struct {
		name  string
		setup func()
		want  func() CycleProbe
	}{
		{"missing", func() {}, func() CycleProbe { return CycleProbe{Reason: "pickup_entry_missing", Step: step(skill())} }},
		{"installed", func() { writeEntry(t, skill(), PickupSkill()) }, func() CycleProbe { return CycleProbe{Entry: "koinon-pickup"} }},
		{"different", func() { writeEntry(t, skill(), []byte("an older entry\n")) },
			func() CycleProbe { return CycleProbe{Reason: "pickup_entry_differs", Step: step(skill())} }},
		{"not a file", func() { os.MkdirAll(skill(), 0o700) },
			func() CycleProbe { return CycleProbe{Reason: "pickup_entry_unreadable", Step: step(skill())} }},
		{"a project and a personal pickup skill do not matter", func() {
			writeEntry(t, skill(), PickupSkill())
			writeEntry(t, filepath.Join(term.home, ".claude", "skills", "pickup", "SKILL.md"), []byte("a personal pickup\n"))
			writeEntry(t, filepath.Join(term.home, ".claude", "commands", "pickup.md"), []byte("a legacy command\n"))
		}, func() CycleProbe { return CycleProbe{Entry: "koinon-pickup"} }},
		{"the host's CLAUDE_CONFIG_DIR", func() {
			writeEntry(t, skill(), PickupSkill())
			term.config = filepath.Join(term.home, "other-config")
		}, func() CycleProbe {
			return CycleProbe{Reason: "pickup_entry_missing", Step: step(filepath.Join(term.home, "other-config", "skills", "koinon-pickup", "SKILL.md"))}
		}},
	} {
		os.RemoveAll(filepath.Join(term.home, ".claude", "skills"))
		os.RemoveAll(filepath.Join(term.home, ".claude", "commands"))
		term.config = ""
		c.setup()
		if term.config != "" {
			// The registry record moves with the configuration root.
			os.MkdirAll(filepath.Join(term.config, "sessions"), 0o700)
			os.Rename(filepath.Join(term.home, ".claude", "sessions", strconv.FormatInt(term.pid, 10)+".json"),
				filepath.Join(term.config, "sessions", strconv.FormatInt(term.pid, 10)+".json"))
		}
		got := (claudeCycle{}).Probe(ctx, term.target(t))
		if term.config != "" {
			os.Rename(filepath.Join(term.config, "sessions", strconv.FormatInt(term.pid, 10)+".json"),
				filepath.Join(term.home, ".claude", "sessions", strconv.FormatInt(term.pid, 10)+".json"))
			term.config = ""
		}
		if want := c.want(); got != want {
			t.Fatalf("%s: %+v, want %+v", c.name, got, want)
		}
	}
	writeEntry(t, skill(), PickupSkill())
	// A tmux that cannot run is not an unproven pane (#418), for the probe and the inspection.
	noTmux := term.target(t)
	noTmux.tools.tmux = func(context.Context, string, ...string) (string, error) {
		return "", tmuxUnavailableError{errors.New("synthetic start failure")}
	}
	if got := (claudeCycle{}).Probe(ctx, noTmux); got.Reason != "tmux_unavailable" {
		t.Fatalf("tmux unavailable: %+v", got)
	}
	if _, err := (claudeCycle{}).Inspect(ctx, noTmux, term.fence); inspectSeen(err) != "tmux_unavailable" {
		t.Fatalf("inspection without tmux: %v", err)
	}
	for _, c := range []struct {
		name   string
		change func()
		want   string
	}{
		{"reused PID", func() { term.write(); term.start = 77 + 1_000_000 }, "native_registry_unavailable"},
		{"unevidenced platform", func() { term.start = 77; term.write(); claudeEvidencedPlatforms = map[string]bool{} }, "platform_unverified"},
	} {
		c.change()
		if got := (claudeCycle{}).Probe(ctx, term.target(t)); got.Reason != c.want {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}

// Inspect leaves copy mode behind the fence, and is ready only for an idle host with an empty
// input line; a dim suggestion is empty, a draft is not, and the dialog is reported.
func TestClaudeInspect(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.mode = true
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle || term.mode || term.fences != 1 {
		t.Fatalf("copy mode: %+v %v mode %v fences %d", state, err, term.mode, term.fences)
	}
	term.input = "a synthetic draft"
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Idle {
		t.Fatal("a draft is ready")
	}
	term.input, term.status = "", "busy"
	term.write()
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Idle {
		t.Fatal("a busy host is ready")
	}
	term.dialog = true
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Prompt != "native_dialog" {
		t.Fatalf("dialog: %+v", state)
	}
	// The adapter answers no dialog itself, the add-directory dialog included.
	if (claudeCycle{}).Answerable("native_dialog") || (claudeCycle{}).Answerable("add_dir_scope") {
		t.Fatal("answerable dialogs")
	}
	sent := len(term.keys)
	if ok, err := (claudeCycle{}).Answer(ctx, tg, "add_dir_scope", term.fence); ok || err != nil || len(term.keys) != sent {
		t.Fatalf("answer: %v %v keys %q", ok, err, term.keys)
	}
}

// Each key is preceded by the fence: the clear types the command, closes the menu and submits
// it with one Enter; the new registry epoch proves the reset.
func TestClaudeClearAndEpoch(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	if epoch, _ := (claudeCycle{}).NewEpoch(ctx, tg); epoch != "" {
		t.Fatalf("epoch before the clear: %q", epoch)
	}
	if ok, err := (claudeCycle{}).Clear(ctx, tg, term.fence); !ok || err != nil {
		t.Fatalf("clear: %v %v", ok, err)
	}
	if !slices.Equal(term.keys, []string{"-l /clear", "Escape", "Enter"}) || term.fences != 3 || term.cleared != 1 {
		t.Fatalf("keys %q fences %d", term.keys, term.fences)
	}
	if epoch, _ := (claudeCycle{}).NewEpoch(ctx, tg); epoch != "synthetic-epoch-2" {
		t.Fatalf("epoch after the clear: %q", epoch)
	}
	// A fence that fails before Enter stops the input: the clear is never submitted.
	again := newClaudeTerminal(t)
	again.failAt = 3
	if ok, err := (claudeCycle{}).Clear(ctx, again.target(t), again.fence); ok || !errors.Is(err, ErrCycleChanged) ||
		slices.Contains(again.keys, "Enter") || again.cleared != 0 || again.input != "" || !strings.HasPrefix(again.keys[len(again.keys)-1], "BSpace") {
		t.Fatalf("fenced clear: %v %v keys %q input %q", ok, err, again.keys, again.input)
	}
}

// Pickup types /koinon-pickup and submits it only when the menu lists the installed skill's
// description first; otherwise the typed text is erased and nothing is submitted.
func TestClaudePickupEntry(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.menu = tg.Pickup.Description + " (user)"
	if ok, err := (claudeCycle{}).Pickup(ctx, tg, term.fence); !ok || err != nil {
		t.Fatalf("pickup: %v %v keys %q", ok, err, term.keys)
	}
	if !slices.Equal(term.keys, []string{"-l /koinon-pickup", "Escape", "Enter"}) || term.fences != 3 || term.input != "" {
		t.Fatalf("keys %q fences %d input %q", term.keys, term.fences, term.input)
	}
	for _, menu := range []string{"Resume work from this agent's newest handoff in its own directory.", ""} {
		other := newClaudeTerminal(t)
		tg = other.target(t)
		other.menu = menu
		if ok, err := (claudeCycle{}).Pickup(ctx, tg, other.fence); ok || slices.Contains(other.keys, "Enter") ||
			other.input != "" || !strings.HasPrefix(other.keys[len(other.keys)-1], "BSpace") || !errors.Is(err, errClaudeMenuEntry) {
			t.Fatalf("menu %q: %v %v keys %q input %q", menu, ok, err, other.keys, other.input)
		}
	}
}

// A permission dialog that the adapter does not answer is read without input; the maintainer's
// option is entered behind the fence only while the same dialog shows. Each attempt passes the
// fence before the dialog is read again; refused attempts send no key.
func TestClaudeDialogChoice(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.asks = true
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Prompt != "native_dialog" {
		t.Fatalf("inspect: %+v", state)
	}
	dialog, err := (claudeCycle{}).Dialog(ctx, tg)
	if err != nil || !slices.Equal(dialog.Options, []string{"1", "2"}) || len(dialog.Digest) != 64 ||
		!slices.Contains(dialog.Lines, "Do you want to proceed?") || len(term.keys) != 0 {
		t.Fatalf("dialog: %+v %v keys %q", dialog, err, term.keys)
	}
	if ok, err := (claudeCycle{}).Choose(ctx, tg, strings.Repeat("0", 64), "2", term.fence); ok || !errors.Is(err, ErrCycleDialogChanged) || len(term.keys) != 0 {
		t.Fatalf("other digest: %v %v keys %q", ok, err, term.keys)
	}
	if ok, err := (claudeCycle{}).Choose(ctx, tg, dialog.Digest, "3", term.fence); ok || !errors.Is(err, ErrCycleDialogChanged) || len(term.keys) != 0 {
		t.Fatalf("absent option: %v %v", ok, err)
	}
	if ok, err := (claudeCycle{}).Choose(ctx, tg, dialog.Digest, "2", term.fence); !ok || err != nil ||
		!slices.Equal(term.keys, []string{"-l 2"}) || term.fences != 3 || term.asks {
		t.Fatalf("choose: %v %v keys %q fences %d", ok, err, term.keys, term.fences)
	}
}

// An Escape followed at once by Enter reaches Claude Code as Alt+Enter, which does not submit
// (native evidence): the adapter waits out the settle time after every Escape.
func TestClaudeEscapeSettles(t *testing.T) {
	term := newClaudeTerminal(t)
	claudeEscapeSettle = 80 * time.Millisecond
	if ok, err := (claudeCycle{}).Clear(context.Background(), term.target(t), term.fence); !ok || err != nil {
		t.Fatalf("clear: %v %v", ok, err)
	}
	escape := slices.Index(term.keys, "Escape")
	if escape < 0 || escape+1 >= len(term.keys) || term.at[escape+1].Sub(term.at[escape]) < claudeEscapeSettle {
		t.Fatalf("keys %q at %v", term.keys, term.at)
	}
}

// A fence that rejects the input also stops the erase: a draft that replaced the typed text
// stays as it is, and nothing is submitted.
func TestClaudeRejectedFenceKeepsTheInputLine(t *testing.T) {
	term := newClaudeTerminal(t)
	calls := 0
	fence := func(context.Context) error {
		calls++
		if calls >= 3 {
			term.mu.Lock()
			term.input = "a synthetic draft"
			term.mu.Unlock()
			return ErrCycleChanged
		}
		return nil
	}
	ok, err := (claudeCycle{}).Clear(context.Background(), term.target(t), fence)
	if ok || !errors.Is(err, ErrCycleChanged) || slices.Contains(term.keys, "Enter") || term.input != "a synthetic draft" ||
		!slices.Equal(term.keys, []string{"-l /clear", "Escape"}) {
		t.Fatalf("rejected fence: %v %v keys %q input %q", ok, err, term.keys, term.input)
	}
	// An erase finds another draft in place of its own text and leaves it.
	other := newClaudeTerminal(t)
	other.input = "a synthetic draft"
	if err := (claudeCycle{}).erase(context.Background(), other.target(t), other.fence, "/clear"); !errors.Is(err, errClaudeErase) ||
		len(other.keys) != 0 || other.input != "a synthetic draft" {
		t.Fatalf("erase over another draft: %v keys %q input %q", err, other.keys, other.input)
	}
}

// The option goes only to the dialog that was observed: a dialog that changes during the pane
// proof and fence receives no key.
func TestClaudeChoiceRecheckedAfterFence(t *testing.T) {
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.asks = true
	dialog, err := (claudeCycle{}).Dialog(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	fence := func(context.Context) error {
		term.mu.Lock()
		defer term.mu.Unlock()
		term.asks, term.dialog, term.input = false, true, "/add-dir /tmp/synthetic/unrelated"
		return nil
	}
	if ok, err := (claudeCycle{}).Choose(context.Background(), tg, dialog.Digest, "1", fence); ok || !errors.Is(err, ErrCycleDialogChanged) ||
		len(term.keys) != 0 || !term.dialog {
		t.Fatalf("changed dialog: %v %v keys %q dialog %v", ok, err, term.keys, term.dialog)
	}
}

// The host is at its prompt when its registry says idle, or shell (a background shell task
// running), with an empty input line; each other case names what was seen (#417).
func TestClaudePaneStateAcceptsBackgroundShell(t *testing.T) {
	empty := claudeScreen{found: true}
	for _, test := range []struct {
		status string
		screen claudeScreen
		want   PaneState
	}{
		{"idle", empty, PaneState{Idle: true}},
		{"shell", empty, PaneState{Idle: true}},
		{"busy", empty, PaneState{Seen: "status_busy"}},
		// A background subagent's row with no turn in progress: at the prompt (#533).
		{"busy", claudeScreen{found: true, agents: true}, PaneState{Idle: true}},
		{"busy", claudeScreen{found: true, agents: true, turn: true}, PaneState{Seen: "status_busy"}},
		{"busy", claudeScreen{found: true, agents: true, input: "draft"}, PaneState{Seen: "input_not_empty"}},
		{"waiting", claudeScreen{found: true, agents: true}, PaneState{Seen: "status_waiting"}},
		{"waiting", empty, PaneState{Seen: "status_waiting"}},
		{"synthetic", empty, PaneState{Seen: "status_unknown"}},
		{"shell", claudeScreen{}, PaneState{Seen: "input_line_not_found"}},
		{"shell", claudeScreen{found: true, input: "draft"}, PaneState{Seen: "input_not_empty"}},
	} {
		if got := claudePaneState(test.status, test.screen); got != test.want {
			t.Errorf("%s %+v: %+v", test.status, test.screen, got)
		}
	}
}

// A background subagent that wrote its transcript after the ready report keeps a ready cycle
// from its clear: its results came after the handoff (#456). Only a ready cycle checks, and
// only the session's own subagent transcripts count.
// The count of running background subagents: a meta file without "finished": true whose
// transcript changed after the request. A finished one, an older one and another session's do
// not count, and an entry that cannot be read is an error, never a count (#467).
func TestClaudeRunningSubagents(t *testing.T) {
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	tg := term.target(t)
	requested := time.Now().Add(-time.Minute)
	dir := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", term.epoch, "subagents")
	other := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", "00000000-0000-4000-8000-000000000000", "subagents")
	agent := func(dir, name, meta string, at time.Time) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		transcript := filepath.Join(dir, name+".jsonl")
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(transcript, at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := claudeRunningSubagents(tg, requested.UnixMilli()); err != nil || n != 0 {
		t.Fatalf("none: %d %v", n, err)
	}
	after, before := requested.Add(time.Second), requested.Add(-time.Second)
	agent(dir, "agent-running", `{"agentType":"general-purpose","requestShape":"background"}`, after)
	agent(dir, "agent-finished", `{"agentType":"general-purpose","finished":true}`, after)
	agent(dir, "agent-old", `{"agentType":"general-purpose"}`, before)
	agent(other, "agent-other", `{"agentType":"general-purpose"}`, after)
	if n, err := claudeRunningSubagents(tg, requested.UnixMilli()); err != nil || n != 1 {
		t.Fatalf("one running: %d %v", n, err)
	}
	// An unreadable meta file and a directory past the bound are errors.
	agent(dir, "agent-broken", `{"finished":`, after)
	if _, err := claudeRunningSubagents(tg, requested.UnixMilli()); err == nil {
		t.Fatal("an unreadable meta file was counted")
	}
	os.Remove(filepath.Join(dir, "agent-broken.meta.json"))
	defer func(n int) { maxSubagentEntries = n }(maxSubagentEntries)
	maxSubagentEntries = 2
	if _, err := claudeRunningSubagents(tg, requested.UnixMilli()); err == nil {
		t.Fatal("a directory past the bound was counted")
	}
}

func TestClaudeInspectSubagentAfterReady(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	tg := term.target(t)
	ready := time.Now().Add(-time.Minute)
	tg.ReadyAt = ready.UnixMilli()
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("no subagents: %+v %v", state, err)
	}
	dir := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", term.epoch, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "agent-synthetic.jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", "00000000-0000-4000-8000-000000000000", "subagents")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "agent-other.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Written before the report: in the handoff. Another session's newer subagent does not count.
	before := ready.Add(-time.Second)
	if err := os.Chtimes(transcript, before, before); err != nil {
		t.Fatal(err)
	}
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("subagent before the report: %+v %v", state, err)
	}
	after := ready.Add(time.Second)
	if err := os.Chtimes(transcript, after, after); err != nil {
		t.Fatal(err)
	}
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state != (PaneState{Seen: subagentAfterReady}) {
		t.Fatalf("subagent after the report: %+v %v", state, err)
	}
	// Another stage has no ready report to compare with.
	tg.ReadyAt = 0
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("not ready: %+v %v", state, err)
	}
	// A busy host stays busy; a session ID that is not a UUID selects no directory.
	tg.ReadyAt = ready.UnixMilli()
	term.status = "busy"
	term.write()
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Seen != "status_busy" {
		t.Fatalf("busy: %+v", state)
	}
	if since, err := claudeSubagentsSince(filepath.Join(term.home, ".claude"), "*", ready.UnixMilli()); since || err != nil {
		t.Fatal("a pattern session ID matched", err)
	}
}

// A busy host at its prompt with a background subagent running is ready for the clear when
// the subagent's records show it running, and for the pickup after the clear; a subagent that
// still runs after the ready report gives its result to the new conversation, while one that
// finished after it asks again (#533, #456).
func TestClaudeInspectBackgroundSubagent(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.status = "busy"
	term.footer = []string{"  ⏵⏵ auto mode on · ← 2 agents · ↓ to manage", "  ● main", "  ◯ general-purpose  Capturing timestamp 21 of 40"}
	term.write()
	tg := term.target(t)
	ready := time.Now().Add(-time.Minute)
	tg.ReadyAt = ready.UnixMilli()
	dir := filepath.Join(term.home, ".claude", "projects", "-synthetic-repo", term.epoch, "subagents")
	agent := func(name, meta string, at time.Time) {
		t.Helper()
		subagentTranscript(t, filepath.Join(term.home, ".claude"), term.epoch, name+".jsonl", at)
		if err := os.WriteFile(filepath.Join(dir, name+".meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// No subagent in the records: the row alone does not make a busy host idle.
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state.Seen != "status_busy" {
		t.Fatalf("no running subagent: %+v %v", state, err)
	}
	agent("agent-running", `{"agentType":"general-purpose","requestShape":"background"}`, ready.Add(time.Second))
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("running subagent after the report: %+v %v", state, err)
	}
	// After the clear: no ready report to compare with, and the subagent still runs.
	tg.ReadyAt = 0
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("after the clear: %+v %v", state, err)
	}
	tg.ReadyAt = ready.UnixMilli()
	// A turn in progress stays busy.
	term.footer[0] = "  ⏵⏵ auto mode on · esc to interrupt · ← 2 agents · ↓ to manage"
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state.Seen != "status_busy" {
		t.Fatalf("turn in progress: %+v %v", state, err)
	}
	term.footer[0] = "  ⏵⏵ auto mode on · ← 2 agents · ↓ to manage"
	// A subagent that finished after the report gave its result to this conversation.
	agent("agent-finished", `{"agentType":"general-purpose","finished":true}`, ready.Add(2*time.Second))
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state.Seen != subagentAfterReady {
		t.Fatalf("finished after the report: %+v %v", state, err)
	}
	os.Remove(filepath.Join(dir, "agent-finished.meta.json"))
	os.Remove(filepath.Join(dir, "agent-finished.jsonl"))
	// An unreadable meta file proves nothing.
	if err := os.WriteFile(filepath.Join(dir, "agent-running.meta.json"), []byte(`{"finished":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state.Seen != subagentsUnread {
		t.Fatalf("unreadable meta: %+v %v", state, err)
	}
}

// subagentTranscript writes a subagent transcript of session under root with time at.
func subagentTranscript(t *testing.T, root, session, name string, at time.Time) {
	t.Helper()
	dir := filepath.Join(root, "projects", "-synthetic-repo", session, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// A CLAUDE_CONFIG_DIR with glob characters is a literal path (PR 457 review).
func TestClaudeSubagentsConfigRootIsLiteral(t *testing.T) {
	term := newClaudeTerminal(t)
	term.config = filepath.Join(term.home, "claude[profile]")
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	if err := os.Rename(filepath.Join(term.home, ".claude"), term.config); err != nil {
		t.Fatal(err)
	}
	tg := term.target(t)
	ready := time.Now().Add(-time.Minute)
	tg.ReadyAt = ready.UnixMilli()
	subagentTranscript(t, term.config, term.epoch, "agent-synthetic.jsonl", ready.Add(time.Second))
	if state, err := (claudeCycle{}).Inspect(context.Background(), tg, term.fence); err != nil || state.Seen != subagentAfterReady {
		t.Fatalf("literal root: %+v %v", state, err)
	}
}

// Old transcripts before a newer one are all read, and a directory past the bound is not a
// clean result: the pane is not ready for the clear (PR 457 review).
func TestClaudeSubagentsScanIsCompleteOrRefused(t *testing.T) {
	previous := maxSubagentEntries
	maxSubagentEntries = 8
	t.Cleanup(func() { maxSubagentEntries = previous })
	root := t.TempDir()
	session := "316b9458-6b5a-47c3-b863-af1745fb54a4"
	ready := time.Now().Add(-time.Minute)
	for i := 0; i < maxSubagentEntries-1; i++ {
		subagentTranscript(t, root, session, fmt.Sprintf("agent-a%04d.jsonl", i), ready.Add(-time.Second))
	}
	if since, err := claudeSubagentsSince(root, session, ready.UnixMilli()); since || err != nil {
		t.Fatalf("old transcripts: %v %v", since, err)
	}
	subagentTranscript(t, root, session, "agent-znew.jsonl", ready.Add(time.Second))
	if since, err := claudeSubagentsSince(root, session, ready.UnixMilli()); !since || err != nil {
		t.Fatalf("newer transcript at the bound: %v %v", since, err)
	}
	subagentTranscript(t, root, session, "agent-zover.jsonl", ready.Add(-time.Second))
	if since, err := claudeSubagentsSince(root, session, ready.UnixMilli()); since || !errors.Is(err, errSubagentScan) {
		t.Fatalf("past the bound: %v %v", since, err)
	}
	term := newClaudeTerminal(t)
	term.config, term.epoch = root, session
	term.write()
	if err := os.Rename(filepath.Join(term.home, ".claude", "sessions"), filepath.Join(root, "sessions")); err != nil {
		t.Fatal(err)
	}
	tg := term.target(t)
	tg.ReadyAt = ready.UnixMilli()
	if state, err := (claudeCycle{}).Inspect(context.Background(), tg, term.fence); err != nil || state.Seen != subagentsUnread {
		t.Fatalf("inspect past the bound: %+v %v", state, err)
	}
}
