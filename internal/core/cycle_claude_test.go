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
	status  string
	epoch   string
	cleared int
	added   []string
	menu    string // the description that /pickup lists first
	keys    []string
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
	previous, wait := claudeEvidencedPlatforms, claudeKeyWait
	claudeEvidencedPlatforms, claudeKeyWait = map[string]bool{runtime.GOOS: true}, 300*time.Millisecond
	t.Cleanup(func() { claudeEvidencedPlatforms, claudeKeyWait = previous, wait })
	return term
}

// write stores the registry record of the synthetic host.
func (c *claudeTerminal) write() {
	dir := filepath.Join(c.home, ".claude", "sessions")
	os.MkdirAll(dir, 0o700)
	data, _ := json.Marshal(map[string]any{"pid": c.pid, "sessionId": c.epoch, "procStart": strconv.FormatInt(c.start, 10),
		"version": "2.1.296", "status": c.status, "entrypoint": "cli"})
	if err := os.WriteFile(filepath.Join(dir, strconv.FormatInt(c.pid, 10)+".json"), data, 0o600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *claudeTerminal) screen() string {
	if c.dialog {
		return "   Add directory to workspace\n   /tmp/synthetic/loader\n   ❯ 1. Yes, for this session\n     2. Yes, and remember this directory\n     3. No\n   Enter to confirm · Esc to cancel\n"
	}
	var b strings.Builder
	b.WriteString("● synthetic transcript\n")
	if strings.HasPrefix(c.input, "/pickup") && c.menu != "" {
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
		switch {
		case key == "-X cancel":
			c.mode = false
		case strings.HasPrefix(key, "-l "):
			text := strings.TrimPrefix(key, "-l ")
			if c.dialog && text == "1" {
				c.dialog = false
				c.added = append(c.added, strings.TrimPrefix(c.input, "/add-dir "))
				c.input = ""
			} else {
				c.input += text
			}
		case key == "C-u":
			c.input = ""
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
		{"two project levels", func(tg *CycleTarget) {
			tg.Directory = filepath.Join(tg.Checkout, "nested")
			writeEntry(t, filepath.Join(tg.Directory, ".claude", "skills", "pickup", "SKILL.md"), tg.Pickup.Content)
			writeEntry(t, project(*tg), tg.Pickup.Content)
		}, CycleProbe{Reason: "pickup_entry_unverified"}},
	} {
		os.RemoveAll(filepath.Join(term.home, ".claude", "skills"))
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
		{"unevidenced version", func() {
			term.write()
			data, _ := os.ReadFile(filepath.Join(term.home, ".claude", "sessions", "4242.json"))
			os.WriteFile(filepath.Join(term.home, ".claude", "sessions", "4242.json"), []byte(strings.Replace(string(data), "2.1.296", "2.1.295", 1)), 0o600)
		}, "native_version_unverified"},
		{"reused PID", func() { term.write(); term.start = 78 }, "native_registry_unavailable"},
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
		slices.Contains(again.keys, "Enter") || again.cleared != 0 || again.input != "" || again.keys[len(again.keys)-1] != "C-u" {
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
		other.input != "" || other.keys[len(other.keys)-1] != "C-u" {
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
	want := []string{"-l /add-dir " + tg.LoaderDir, "Escape", "Enter", "-l 1", "-l /pickup", "C-u"}
	if !slices.Equal(term.keys, want) || term.fences != len(want) || !slices.Equal(term.added, []string{tg.LoaderDir}) || term.input != "" {
		t.Fatalf("keys %q fences %d added %q input %q", term.keys, term.fences, term.added, term.input)
	}
}
