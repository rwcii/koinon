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
)

// The Claude Code cycle adapter (#196, chunk 03) against captured screens and a synthetic
// terminal. Nothing here starts Claude Code or reaches a real tmux server.

// Captured Claude Code 2.1.296 screens (paths replaced): dim placeholder and suggestion text is
// an empty input line; typed text, menus and the add-directory dialog are read as they show.
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
		if s := read(name); !s.found || s.input != want || s.dialog != "" {
			t.Fatalf("%s: found %v input %q dialog %q", name, s.found, s.input, s.dialog)
		}
	}
	if s := read("add-dir-dialog"); s.dialog != "add_dir_scope" {
		t.Fatalf("add-dir dialog: %q", s.dialog)
	}
	// The captured loader entry (an earlier loader description) is listed first; the catalog
	// item's description does not match it.
	menu := menuFirst(read("pickup-menu-loader"))
	captured := PickupEntry{Description: "Resume work from this agent's newest handoff. Session-local loader for a managed context cycle; the procedure is the canonical catalog skill."}
	catalog := PickupEntry{Description: "Resume work from this agent's newest handoff in its own directory under the Git-ignored _handoff/."}
	if !describes(menu, captured) || describes(menu, catalog) {
		t.Fatalf("menu entry %q", menu)
	}
}

// claudeTerminal is a synthetic Claude Code pane: an input line, a /pickup menu, the
// add-directory dialog, copy mode, and a registry record that /clear changes.
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
	added   []string
	said    []string // transcript lines after the synthetic transcript's first line
	menu    string   // the description that /pickup lists first
	closed  bool     // Escape closed the menu since the last typed text
	keys    []string
	at      []time.Time // when each key arrived
	fences  int
	failAt  int // the fence call that fails, 0 for none
	home    string
	pid     int64
	start   int64
	args    []string
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
	if strings.HasPrefix(c.input, "/pickup") && c.menu != "" && !c.closed {
		b.WriteString("  ❯ /pickup                     " + c.menu + "\n")
	}
	b.WriteString("────────\n\x1b[39m" + claudeInputMark)
	if c.input == "" {
		b.WriteString("\x1b[2mwrite a handoff\x1b[0m")
	} else {
		b.WriteString(c.input)
	}
	b.WriteString("\n────────\n")
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
				c.added = append(c.added, strings.TrimPrefix(c.input, "/add-dir "))
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
			case strings.HasPrefix(c.input, "/add-dir ") && slices.Contains(c.added, strings.TrimPrefix(c.input, "/add-dir ")):
				dir := strings.TrimPrefix(c.input, "/add-dir ")
				c.said = append(c.said, "❯ "+c.input, "  ⎿  "+dir+" is already added as a working directory.")
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
	checkout := t.TempDir()
	return CycleTarget{CycleID: "synthetic", Family: "claude", SessionID: "synthetic-a", HostPID: c.pid, HostStart: c.start,
		Socket: "/tmp/synthetic-socket", Pane: "%3", Epoch: "synthetic-epoch-1", Directory: checkout, Checkout: checkout,
		LoaderDir: filepath.Join(t.TempDir(), "loader"),
		Pickup:    PickupEntry{Content: []byte("synthetic canonical pickup\n"), Description: "Resume work from this agent's newest handoff in its own directory."},
		Loader:    claudeLoaderEntry(),
		tools: hostTools{tmux: c.tmux,
			processStart:   func(pid int) (int64, error) { return c.start, nil },
			processParents: func() (map[int]int, error) { return map[int]int{}, nil },
			processCommand: func(pid int) (string, []string, error) { return "/synthetic/claude", c.args, nil },
			processEnvironment: func(pid int, name string) (string, error) {
				if name == "HOME" {
					return c.home, nil
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

// Probe checks only the entry that a bare /pickup runs: personal over project, project over
// the loader. Shadowed entries are not checked; an unknown order is reported.
func TestClaudeProbe(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	personal := func(tg CycleTarget) string {
		return filepath.Join(term.home, ".claude", "skills", "pickup", "SKILL.md")
	}
	project := func(tg CycleTarget) string {
		return filepath.Join(tg.Checkout, ".claude", "skills", "pickup", "SKILL.md")
	}
	loader := func(tg CycleTarget) string {
		return filepath.Join(tg.LoaderDir, ".claude", "skills", "pickup", "SKILL.md")
	}
	for _, c := range []struct {
		name  string
		setup func(tg *CycleTarget)
		want  CycleProbe
	}{
		{"interactive", func(tg *CycleTarget) { writeEntry(t, loader(*tg), tg.Loader.Content) }, CycleProbe{Bootstrap: "interactive"}},
		{"launch loader", func(tg *CycleTarget) {
			writeEntry(t, loader(*tg), tg.Loader.Content)
			term.args = []string{"claude", "--model", "synthetic", "--add-dir", tg.LoaderDir}
		}, CycleProbe{Bootstrap: "loader"}},
		{"loader changed", func(tg *CycleTarget) { writeEntry(t, loader(*tg), []byte("other\n")) }, CycleProbe{Reason: "pickup_entry_differs"}},
		{"loader missing", func(tg *CycleTarget) {}, CycleProbe{Reason: "pickup_entry_unreadable"}},
		{"project", func(tg *CycleTarget) { writeEntry(t, project(*tg), tg.Pickup.Content) }, CycleProbe{Bootstrap: "project"}},
		{"project differs", func(tg *CycleTarget) { writeEntry(t, project(*tg), []byte("older pickup\n")) }, CycleProbe{Reason: "pickup_entry_differs", Bootstrap: "project"}},
		{"personal shadows a different project copy", func(tg *CycleTarget) {
			writeEntry(t, personal(*tg), tg.Pickup.Content)
			writeEntry(t, project(*tg), []byte("older pickup\n"))
		}, CycleProbe{Bootstrap: "personal"}},
		{"legacy command", func(tg *CycleTarget) {
			writeEntry(t, filepath.Join(tg.Checkout, ".claude", "commands", "pickup.md"), []byte("legacy\n"))
		}, CycleProbe{Reason: "pickup_entry_unverified"}},
		{"personal shadows two project levels", func(tg *CycleTarget) {
			tg.Directory = filepath.Join(tg.Checkout, "nested")
			writeEntry(t, personal(*tg), tg.Pickup.Content)
			writeEntry(t, filepath.Join(tg.Directory, ".claude", "skills", "pickup", "SKILL.md"), []byte("older pickup\n"))
			writeEntry(t, project(*tg), []byte("older pickup\n"))
		}, CycleProbe{Bootstrap: "personal"}},
		{"personal command beside a personal skill", func(tg *CycleTarget) {
			writeEntry(t, personal(*tg), tg.Pickup.Content)
			writeEntry(t, filepath.Join(term.home, ".claude", "commands", "pickup.md"), []byte("legacy\n"))
		}, CycleProbe{Reason: "pickup_entry_unverified"}},
		{"two project levels", func(tg *CycleTarget) {
			tg.Directory = filepath.Join(tg.Checkout, "nested")
			writeEntry(t, filepath.Join(tg.Directory, ".claude", "skills", "pickup", "SKILL.md"), tg.Pickup.Content)
			writeEntry(t, project(*tg), tg.Pickup.Content)
		}, CycleProbe{Reason: "pickup_entry_unverified"}},
	} {
		os.RemoveAll(filepath.Join(term.home, ".claude", "skills"))
		os.RemoveAll(filepath.Join(term.home, ".claude", "commands"))
		term.args = []string{"claude"}
		tg := term.target(t)
		c.setup(&tg)
		if got := (claudeCycle{}).Probe(ctx, tg); got != c.want {
			t.Fatalf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	tg := term.target(t)
	writeEntry(t, filepath.Join(tg.LoaderDir, ".claude", "skills", "pickup", "SKILL.md"), tg.Loader.Content)
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
	if state, _ := (claudeCycle{}).Inspect(ctx, tg, term.fence); state.Prompt != "add_dir_scope" {
		t.Fatalf("dialog: %+v", state)
	}
	if (claudeCycle{}).Answerable("native_dialog") || !(claudeCycle{}).Answerable("add_dir_scope") {
		t.Fatal("answerable dialogs")
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

// Pickup submits only when the menu lists the expected entry first; otherwise the typed text
// is erased and nothing is submitted.
func TestClaudePickupEntry(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.menu = tg.Pickup.Description
	if ok, err := (claudeCycle{}).Pickup(ctx, tg, "project", term.fence); !ok || err != nil {
		t.Fatalf("project pickup: %v %v", ok, err)
	}
	other := newClaudeTerminal(t)
	tg = other.target(t)
	other.menu = tg.Loader.Description
	if ok, err := (claudeCycle{}).Pickup(ctx, tg, "project", other.fence); ok || err == nil || slices.Contains(other.keys, "Enter") ||
		other.input != "" || !strings.HasPrefix(other.keys[len(other.keys)-1], "BSpace") || !errors.Is(err, errClaudeMenuEntry) {
		t.Fatalf("wrong entry: %v %v keys %q input %q", ok, err, other.keys, other.input)
	}
	if ok, err := (claudeCycle{}).Pickup(ctx, tg, "interactive", other.fence); !ok || err != nil {
		t.Fatalf("loader pickup: %v %v", ok, err)
	}
}

// The interactive bootstrap adds the loader for this session only (option 1), proves that
// /pickup lists it, and erases that text unsubmitted.
func TestClaudeBootstrap(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.menu = tg.Loader.Description
	if ok, err := (claudeCycle{}).Bootstrap(ctx, tg, term.fence); !ok || err != nil {
		t.Fatalf("bootstrap: %v %v keys %q", ok, err, term.keys)
	}
	erase := strings.TrimSpace(strings.Repeat("BSpace ", len("/pickup")+2))
	want := []string{"-l /add-dir " + tg.LoaderDir, "Escape", "Enter", "-l 1", "-l /pickup", erase}
	// Every key, the erase included, passes the fence.
	if !slices.Equal(term.keys, want) || term.fences != len(want) || !slices.Equal(term.added, []string{tg.LoaderDir}) || term.input != "" {
		t.Fatalf("keys %q fences %d added %q input %q", term.keys, term.fences, term.added, term.input)
	}
}

// A second bootstrap in the same session (an earlier cycle added the loader, and /clear keeps
// it) gets Claude Code's "already added" reply instead of the dialog: no option is entered, and
// /pickup must still list the loader.
func TestClaudeBootstrapAlreadyAdded(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	tg := term.target(t)
	term.menu = tg.Loader.Description
	term.added = []string{tg.LoaderDir}
	if ok, err := (claudeCycle{}).Bootstrap(ctx, tg, term.fence); !ok || err != nil {
		t.Fatalf("bootstrap: %v %v keys %q", ok, err, term.keys)
	}
	erase := strings.TrimSpace(strings.Repeat("BSpace ", len("/pickup")+2))
	want := []string{"-l /add-dir " + tg.LoaderDir, "Escape", "Enter", "-l /pickup", erase}
	if !slices.Equal(term.keys, want) || term.input != "" {
		t.Fatalf("keys %q input %q", term.keys, term.input)
	}
	// Claude Code wraps a long echo or reply onto more lines (an 80-column pane).
	dir := "/home/synthetic/.local/state/koinon/cycle/claude-loader"
	for _, lines := range [][]string{
		{"❯ /add-dir " + dir, "  ⎿  " + dir + " is already added as", " a working directory.", "", "────", "❯\u00a0"},
		{"❯ /add-dir /home/synthetic/.local/state/koinon/cy", "cle/claude-loader", "  ⎿  " + dir, " is already added as a working directory."},
	} {
		if !claudeAlreadyAdded(claudeScreen{lines: lines, found: true}, dir) {
			t.Fatalf("wrapped reply refused: %q", lines)
		}
	}
	// The reply must answer this /add-dir of this directory: an older reply above a later
	// /add-dir, a reply for another directory, another command's echo, a line between the echo
	// and the reply, or a reply with text still in the input line is not one.
	for _, c := range []struct {
		lines []string
		input string
	}{
		{[]string{"❯ /add-dir " + dir, "  ⎿  " + dir + " is already added as a working directory.", "❯ /add-dir " + dir}, ""},
		{[]string{"❯ /add-dir " + dir, "  ⎿  /tmp/other is already added as a working directory."}, ""},
		{[]string{"❯ /add-dir " + dir + "-other", "  ⎿  " + dir + "-other is already added as a working directory."}, ""},
		{[]string{"❯ /add-dir " + dir, "● other output", "  ⎿  " + dir + " is already added as a working directory."}, ""},
		{[]string{"❯ /add-dir " + dir, "  ⎿  " + dir + " is already added as a working directory."}, "draft"},
	} {
		if claudeAlreadyAdded(claudeScreen{lines: c.lines, found: true, input: c.input}, dir) {
			t.Fatalf("reply accepted: %q input %q", c.lines, c.input)
		}
	}
	// Without the loader in the menu the bootstrap still fails.
	term = newClaudeTerminal(t)
	tg = term.target(t)
	term.added = []string{tg.LoaderDir}
	if ok, err := (claudeCycle{}).Bootstrap(ctx, tg, term.fence); ok || !errors.Is(err, errClaudeMenuEntry) {
		t.Fatalf("bootstrap without the menu entry: %v %v", ok, err)
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
		len(term.keys) != 0 || len(term.added) != 0 {
		t.Fatalf("changed dialog: %v %v keys %q added %q", ok, err, term.keys, term.added)
	}
}
