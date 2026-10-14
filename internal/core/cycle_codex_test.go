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

// The Codex cycle adapter (#196, chunk 04) against captured screens and a synthetic terminal.
// Nothing here starts Codex or reaches a real tmux server.

func cycleScreen(t *testing.T, name string) codexScreen {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex-screens", "cycle-"+name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return readCodexScreen(string(data))
}

// Captured Codex CLI 0.162.0 screens (account data, paths and thread IDs replaced).
func TestCodexCycleScreenFixtures(t *testing.T) {
	for name, want := range map[string]string{"start": "", "draft": "Reply with the single word ok.", "after-erase": "",
		"pickup-menu": "$pickup", "pickup-closed": "$pickup", "clear-typed": "/clear", "status-typed": "/status", "after-clear": "",
		"after-status": "", "after-turn": ""} {
		s := cycleScreen(t, name)
		if s.composer() < 0 || codexText(s) != want || codexDialog(s).Digest != "" {
			t.Fatalf("%s: composer %d text %q", name, s.composer(), codexText(s))
		}
	}
	if rows, open, complete := codexMenu(cycleScreen(t, "pickup-menu")); !open || !complete || pickupRows(rows) != 1 {
		t.Fatalf("pickup menu: %q %v %v", rows, open, complete)
	}
	if _, open, _ := codexMenu(cycleScreen(t, "pickup-closed")); open {
		t.Fatal("closed menu read as open")
	}
	// The /status card is no turn: that screen still counts as cleared, a turn does not.
	if !codexCleared(cycleScreen(t, "after-clear")) || !codexCleared(cycleScreen(t, "after-status")) || codexCleared(cycleScreen(t, "after-turn")) {
		t.Fatal("cleared screen")
	}
	if thread, untouched := codexStatusThread(cycleScreen(t, "after-status")); thread != "01a13800-0000-7000-8000-000000000001" || !untouched {
		t.Fatalf("status card: %q %v", thread, untouched)
	}
	// A turn of the same thread after the card keeps the thread and is not untouched.
	if thread, untouched := codexStatusThread(cycleScreen(t, "after-turn")); thread != "01a13800-0000-7000-8000-000000000001" || untouched {
		t.Fatalf("after a turn: %q %v", thread, untouched)
	}
	for _, name := range []string{"start", "after-clear", "status-typed", "dialog-exec", "after-dialog"} {
		if thread, _ := codexStatusThread(cycleScreen(t, name)); thread != "" {
			t.Fatalf("%s names thread %q", name, thread)
		}
	}
	working := cycleScreen(t, "working")
	if codexIdle(strings.Join(working.raw, "\n")) || !codexIdle(strings.Join(cycleScreen(t, "after-turn").raw, "\n")) {
		t.Fatal("working screen")
	}
	for name, want := range map[string][]string{"dialog-mcp": {"1", "2", "3", "4"}, "dialog-exec": {"1", "2", "3"}} {
		d := codexDialog(cycleScreen(t, name))
		if d.Digest == "" || !slices.Equal(d.Options, want) {
			t.Fatalf("%s: %+v", name, d)
		}
	}
	if d := codexDialog(cycleScreen(t, "after-dialog")); d.Digest != "" {
		t.Fatalf("after the dialog: %+v", d)
	}
}

// codexTerminal is a synthetic Codex pane: a banner, a transcript, a composer with the
// command popup and the skill menu, a dialog, and the threads that /clear starts.
type codexTerminal struct {
	mu         sync.Mutex
	t          *testing.T
	thread     string
	threads    int
	input      string
	closed     bool // Escape closed the menu since the last typed text
	said       []string
	menu       []string // rows that the $ menu lists
	scroll     bool     // the menu shows a scroll marker
	dialog     []string
	options    []string
	keys       []string
	fences     int
	failAt     int
	noStatus   bool   // /status shows no card
	onClear    func() // runs as /clear takes effect, with the terminal held
	codex      string
	home       string
	pid, start int64
	args       []string
}

func newCodexTerminal(t *testing.T) *codexTerminal {
	home := t.TempDir()
	term := &codexTerminal{t: t, thread: "thread-a", threads: 1, home: home, codex: filepath.Join(home, ".codex"), pid: 4343, start: 88,
		args: []string{"--no-daemon"}}
	term.rollout("thread-a", "0.162.0", "task_started", "task_complete")
	previous, wait, settle := codexEvidencedPlatforms, codexKeyWait, codexEscapeSettle
	codexEvidencedPlatforms, codexKeyWait, codexEscapeSettle = map[string]bool{runtime.GOOS: true}, 300*time.Millisecond, 0
	t.Cleanup(func() { codexEvidencedPlatforms, codexKeyWait, codexEscapeSettle = previous, wait, settle })
	return term
}

// rollout writes a thread's rollout: its session_meta and one event per given type, all of
// one turn.
func (c *codexTerminal) rollout(thread, version string, events ...string) {
	dir := filepath.Join(c.codex, "sessions", "2026", "10", "14")
	os.MkdirAll(dir, 0o700)
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": thread, "originator": "codex-tui", "cli_version": version}})
	lines := []string{string(meta)}
	for _, event := range events {
		line, _ := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": event, "turn_id": "turn-1"}})
		lines = append(lines, string(line))
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-10-14T00-00-00-"+thread+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *codexTerminal) screen() string {
	var b strings.Builder
	b.WriteString("\n  >_ OpenAI Codex (v0.162.0)\n     /tmp/synthetic/repo\n\n")
	for _, line := range c.said {
		b.WriteString(line + "\n")
	}
	if c.dialog != nil {
		for _, line := range c.dialog {
			b.WriteString(line + "\n")
		}
		return b.String()
	}
	switch {
	case strings.HasPrefix(c.input, "$") && !c.closed:
		for i, row := range c.menu {
			mark := "  "
			if i == 0 {
				mark = "› "
			}
			b.WriteString(mark + row + "\n")
		}
		if c.scroll {
			b.WriteString("↓\n")
		}
		b.WriteString("\n  enter insert · esc close\n\n")
	case strings.HasPrefix(c.input, "/"):
		b.WriteString("› " + c.input + "  synthetic command\n")
	}
	b.WriteString("\x1b[1m›\x1b[0m ")
	if c.input == "" {
		b.WriteString("\x1b[2mAsk Codex to do anything\x1b[0m")
	} else {
		b.WriteString(c.input)
	}
	b.WriteString("\n\n  synthetic-model · /tmp/synthetic/repo\n")
	if c.input == "" {
		b.WriteString("  ? for shortcuts\n")
	}
	return b.String()
}

func (c *codexTerminal) tmux(_ context.Context, socket string, args ...string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case args[0] == "display-message" && args[len(args)-1] == "#{pane_in_mode}":
		return "0\n", nil
	case args[0] == "display-message":
		return "%4 " + strconv.FormatInt(c.pid, 10) + "\n", nil
	case args[0] == "capture-pane":
		return c.screen(), nil
	case args[0] == "send-keys":
		key := strings.Join(args[3:], " ")
		c.keys = append(c.keys, key)
		switch {
		case strings.HasPrefix(key, "-l "):
			text := strings.TrimPrefix(key, "-l ")
			if c.dialog != nil {
				if slices.Contains(c.options, text) {
					c.dialog, c.options = nil, nil
				}
				return "", nil
			}
			c.input += text
			c.closed = false
		case key == "Escape":
			c.closed = true
		case strings.HasPrefix(key, "BSpace"):
			for range strings.Fields(key) {
				if r := []rune(c.input); len(r) > 0 {
					c.input = string(r[:len(r)-1])
				}
			}
		case key == "Enter":
			switch c.input {
			case "/clear":
				c.threads++
				c.thread = "thread-" + string(rune('a'+c.threads-1))
				c.said = nil
				if c.onClear != nil {
					c.input = ""
					c.onClear()
					return "", nil
				}
			case "/status":
				if !c.noStatus {
					c.said = append(c.said, "/status", "", "  >_ OpenAI Codex (v0.162.0)", "", "  Session:             "+c.thread, "")
				}
			case "":
			default:
				c.said = append(c.said, "› "+c.input, "")
			}
			c.input = ""
		}
		return "", nil
	}
	return "", errors.New("unexpected tmux call")
}

func (c *codexTerminal) target(t *testing.T) CycleTarget {
	checkout := t.TempDir()
	return CycleTarget{CycleID: "synthetic", Family: "codex", SessionID: "thread-a", HostPID: c.pid, HostStart: c.start,
		Socket: "/tmp/synthetic-socket", Pane: "%4", Epoch: "thread-a", Directory: checkout, Checkout: checkout, Holds: true,
		Pickup: PickupEntry{Content: []byte("---\nname: pickup\ndescription: synthetic\n---\n\nsynthetic canonical pickup\n"), Description: "synthetic"},
		tools: hostTools{tmux: c.tmux,
			processStart:   func(pid int) (int64, error) { return c.start, nil },
			processParents: func() (map[int]int, error) { return map[int]int{}, nil },
			processCommand: func(pid int) (string, []string, error) { return "/synthetic/codex", c.args, nil },
			processEnvironment: func(pid int, name string) (string, error) {
				if name == "HOME" {
					return c.home, nil
				}
				return "", nil
			},
			processGroups: func() (map[int]platform.ProcessGroup, error) {
				return map[int]platform.ProcessGroup{int(c.pid): {Group: int(c.pid), Terminal: 5, Foreground: int(c.pid)}}, nil
			}}}
}

func (c *codexTerminal) fence(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fences++
	if c.fences == c.failAt {
		return ErrCycleChanged
	}
	return nil
}

func (c *codexTerminal) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.keys)
}

// Probe needs the platform, the requester's own rollout of an evidenced version, the pane, and
// pickup skills that are all the catalog item; their scope is shown.
func TestCodexProbe(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	skill := func(root, dir string, content []byte) string {
		path := filepath.Join(root, dir, "SKILL.md")
		writeEntry(t, path, content)
		return path
	}
	if got := (codexCycle{}).Probe(ctx, target); got.Reason != "pickup_entry_unverified" {
		t.Fatalf("no entry: %+v", got)
	}
	project := skill(filepath.Join(target.Directory, ".agents", "skills"), "pickup", target.Pickup.Content)
	if got := (codexCycle{}).Probe(ctx, target); got != (CycleProbe{Bootstrap: "project"}) {
		t.Fatalf("project entry: %+v", got)
	}
	// A byte-equal duplicate under the other root, and another directory name, still pass.
	skill(filepath.Join(target.Directory, ".codex", "skills"), "renamed", target.Pickup.Content)
	if got := (codexCycle{}).Probe(ctx, target); got != (CycleProbe{Bootstrap: "project"}) {
		t.Fatalf("byte-equal duplicate: %+v", got)
	}
	personal := skill(filepath.Join(term.codex, "skills"), "pickup", target.Pickup.Content)
	if got := (codexCycle{}).Probe(ctx, target); got != (CycleProbe{Bootstrap: "personal"}) {
		t.Fatalf("personal entry: %+v", got)
	}
	writeEntry(t, personal, []byte("---\nname: pickup\n---\nanother procedure\n"))
	if got := (codexCycle{}).Probe(ctx, target); got.Reason != "pickup_entry_differs" {
		t.Fatalf("differing entry: %+v", got)
	}
	os.Remove(personal)
	// Another skill's file with other bytes is not a pickup entry.
	skill(filepath.Join(term.home, ".agents", "skills"), "other", []byte("---\nname: other\n---\nother\n"))
	if got := (codexCycle{}).Probe(ctx, target); got != (CycleProbe{Bootstrap: "project"}) {
		t.Fatalf("other skill: %+v", got)
	}
	term.rollout("thread-a", "0.163.0", "task_started", "task_complete")
	if got := (codexCycle{}).Probe(ctx, target); got.Reason != "native_version_unverified" {
		t.Fatalf("version: %+v", got)
	}
	term.rollout("thread-a", "0.162.0")
	// sessionProbe gives no epoch: the session's own thread is read.
	noEpoch := target
	noEpoch.Epoch = ""
	if got := (codexCycle{}).Probe(ctx, noEpoch); got != (CycleProbe{Bootstrap: "project"}) {
		t.Fatalf("without an epoch: %+v", got)
	}
	other := target
	other.SessionID = "thread-without-rollout"
	if got := (codexCycle{}).Probe(ctx, other); got.Reason != "native_registry_unavailable" {
		t.Fatalf("no rollout: %+v", got)
	}
	unheld := target
	unheld.Holds = false
	if got := (codexCycle{}).Probe(ctx, unheld); got.Reason != "participant_unheld" {
		t.Fatalf("no participant held: %+v", got)
	}
	unproven := target
	unproven.tools.processGroups = func() (map[int]platform.ProcessGroup, error) { return nil, errors.New("synthetic") }
	if got := (codexCycle{}).Probe(ctx, unproven); got.Reason != "pane_unproven" {
		t.Fatalf("pane: %+v", got)
	}
	codexEvidencedPlatforms = map[string]bool{}
	if got := (codexCycle{}).Probe(ctx, target); got.Reason != "platform_unverified" {
		t.Fatalf("platform: %+v", got)
	}
	_ = project
}

// A one-file entry reached through two symlinked roots is one file, as Codex lists it (P8).
func TestCodexPickupEntrySymlinkedRoots(t *testing.T) {
	term := newCodexTerminal(t)
	target := term.target(t)
	shared := filepath.Join(target.Directory, "agents", "skills")
	writeEntry(t, filepath.Join(shared, "pickup", "SKILL.md"), target.Pickup.Content)
	for _, root := range []string{".agents", ".codex"} {
		os.MkdirAll(filepath.Join(target.Directory, root), 0o700)
		if err := os.Symlink(shared, filepath.Join(target.Directory, root, "skills")); err != nil {
			t.Fatal(err)
		}
	}
	entry, err := codexPickupEntry(target)
	if err != nil || len(entry.files) != 1 {
		t.Fatalf("%+v %v", entry, err)
	}
}

// codexActivation refuses only a skills.config entry that can turn off a verified pickup file,
// in every layer and -c argument, with TOML's meaning of quoted and escaped keys.
func TestCodexActivation(t *testing.T) {
	term := newCodexTerminal(t)
	target := term.target(t)
	file := filepath.Join(target.Directory, ".agents", "skills", "pickup", "SKILL.md")
	writeEntry(t, file, target.Pickup.Content)
	real, _ := filepath.EvalSymlinks(file)
	elsewhere := filepath.Join(target.Directory, ".agents", "skills", "other", "SKILL.md")
	writeEntry(t, elsewhere, []byte("---\nname: other\n---\n"))
	config := filepath.Join(term.codex, "config.toml")
	project := filepath.Join(target.Directory, ".codex", "config.toml")
	quote := strconv.Quote
	for _, c := range []struct {
		name, user, project string
		args                []string
		refused             string
	}{
		{name: "none"},
		{name: "unrelated skills setting", user: "[skills]\ninclude_instructions = true\n"},
		{name: "enabled canonical", user: "[[skills.config]]\npath = " + quote(file) + "\nenabled = true\n"},
		{name: "disable another name", user: "[[skills.config]]\nname = \"other\"\nenabled = false\n"},
		{name: "disable another path", user: "[[skills.config]]\npath = " + quote(elsewhere) + "\nenabled = false\n"},
		{name: "disable by name", user: "[[skills.config]]\nname = \"pickup\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "disable by qualified name", user: "[[skills.config]]\nname = \"plugin:pickup\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "disable by path", user: "[[skills.config]]\npath = " + quote(file) + "\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "disable by directory", user: "[[skills.config]]\npath = " + quote(filepath.Dir(file)) + "\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "disable by relative path", project: "[[skills.config]]\npath = \"../.agents/skills/pickup/SKILL.md\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "disable an unresolvable path", user: "[[skills.config]]\npath = \"/nonexistent/SKILL.md\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "escaped key", user: "[[\"\\u0073kills\".\"config\"]]\nname = \"pickup\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "missing enabled", user: "[[skills.config]]\nname = \"pickup\"\n", refused: "pickup_entry_unverified"},
		{name: "both selectors are ignored", user: "[[skills.config]]\nname = \"pickup\"\npath = " + quote(file) + "\nenabled = false\n"},
		{name: "not an array of tables", user: "[skills]\nconfig = \"pickup\"\n", refused: "pickup_entry_unverified"},
		{name: "does not parse", user: "[[skills.config]\n", refused: "pickup_entry_unreadable"},
		{name: "inline argument", args: []string{"-c", `skills.config=[{name="pickup",enabled=false}]`}, refused: "pickup_entry_unverified"},
		{name: "inline argument enabled", args: []string{"-c", `skills.config=[{path=` + quote(file) + `,enabled=true}]`}},
		{name: "inline argument relative path", args: []string{"--config", `skills.config=[{path="SKILL.md",enabled=false}]`}, refused: "pickup_entry_unverified"},
		{name: "plain string argument", args: []string{"-c", "skills.config=pickup"}, refused: "pickup_entry_unverified"},
		{name: "unrelated argument", args: []string{"-c", `shell_environment_policy.set.KOINON_CODEX_HOST="4343"`}},
		{name: "active profile", user: "profile = \"p\"\n[profiles.p.skills]\nconfig = [{name = \"pickup\", enabled = false}]\n", refused: "pickup_entry_unverified"},
		{name: "profile argument", user: "[profiles.p.skills]\nconfig = [{name = \"pickup\", enabled = false}]\n", args: []string{"-p", "p"}, refused: "pickup_entry_unverified"},
		{name: "inactive profile", user: "profile = \"q\"\n[profiles.p.skills]\nconfig = [{name = \"pickup\", enabled = false}]\n"},
		{name: "no active profile", user: "[profiles.p.skills]\nconfig = [{name = \"pickup\", enabled = false}]\n"},
		{name: "a higher -c array replaces a disabling user entry", user: "[[skills.config]]\npath = " + quote(file) + "\nenabled = false\n",
			args: []string{"-c", `skills.config=[{path=` + quote(file) + `,enabled=true}]`}},
		{name: "a project entry over an enabling user entry", user: "[[skills.config]]\nname = \"pickup\"\nenabled = true\n",
			project: "[[skills.config]]\nname = \"pickup\"\nenabled = false\n", refused: "pickup_entry_unverified"},
		{name: "a -c array over a disabling project entry", project: "[[skills.config]]\nname = \"pickup\"\nenabled = false\n",
			args: []string{"-c", `skills.config=[{name="other",enabled=false}]`}},
		{name: "a project value in place of the skills table", user: "[[skills.config]]\nname = \"pickup\"\nenabled = false\n",
			project: "skills = \"off\"\n", refused: "pickup_entry_unverified"},
		{name: "unknown profile", user: "profile = \"q\"\n[profiles.p.skills]\nconfig = [{name = \"pickup\", enabled = false}]\n", project: "profile = \"p\"\n", refused: "pickup_entry_unverified"},
	} {
		os.Remove(config)
		os.Remove(project)
		if c.user != "" {
			writeEntry(t, config, []byte(c.user))
		}
		if c.project != "" {
			writeEntry(t, project, []byte(c.project))
		}
		term.args = append([]string{"--no-daemon"}, c.args...)
		err := codexActivation(target, term.codex, []string{target.Directory}, []string{real})
		if got := ""; err != nil && err.Error() != c.refused || err == nil && c.refused != "" {
			if err != nil {
				got = err.Error()
			}
			t.Errorf("%s: %q, want %q", c.name, got, c.refused)
		}
	}
}

// The rollout tail proves a thread's activity: an ended last turn, or a tail that holds only an
// ending; a later start, or no turn event, is not idle.
func TestCodexTurnEnded(t *testing.T) {
	term := newCodexTerminal(t)
	path := func() string { p, _ := codexRolloutPath(term.codex, "thread-a"); return p }
	for _, c := range []struct {
		events []string
		want   bool
	}{
		{[]string{"task_started", "task_complete"}, true},
		{[]string{"task_started", "turn_aborted"}, true},
		{[]string{"task_started"}, false},
		{[]string{"task_complete", "task_started"}, false},
		{nil, false},
		{[]string{"task_complete"}, true},
	} {
		term.rollout("thread-a", "0.162.0", c.events...)
		if got := codexTurnEnded(path()); got != c.want {
			t.Errorf("%v: %v", c.events, got)
		}
	}
}

// Clear enters /clear and /status; NewEpoch reads the new thread from the card and sends no key.
func TestCodexClearAndEpoch(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	if epoch, err := (codexCycle{}).NewEpoch(ctx, target); err != nil || epoch != "" {
		t.Fatalf("before the clear: %q %v", epoch, err)
	}
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); !ok || err != nil {
		t.Fatalf("clear: %v %v", ok, err)
	}
	if want := []string{"-l /clear", "Enter", "-l /status", "Enter"}; !slices.Equal(term.sent(), want) {
		t.Fatalf("keys %q", term.sent())
	}
	before := len(term.sent())
	if epoch, err := (codexCycle{}).NewEpoch(ctx, target); err != nil || epoch != "thread-b" {
		t.Fatalf("epoch %q %v", epoch, err)
	}
	if len(term.sent()) != before {
		t.Fatal("NewEpoch sent a key")
	}
	// A /status that shows no card leaves the clear received and the reset unproven.
	term = newCodexTerminal(t)
	term.noStatus = true
	target = term.target(t)
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); !ok || err != nil {
		t.Fatalf("clear without card: %v %v", ok, err)
	}
	if epoch, _ := (codexCycle{}).NewEpoch(ctx, target); epoch != "" {
		t.Fatalf("epoch without card: %q", epoch)
	}
	// A rejected fence before the first key enters nothing.
	term = newCodexTerminal(t)
	term.failAt = 1
	target = term.target(t)
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); ok || !errors.Is(err, ErrCycleChanged) || len(term.sent()) != 0 {
		t.Fatalf("fenced clear: %v %v %q", ok, err, term.sent())
	}
}

// Inspect proves the current thread: before the clear the requester's ended turn, after it the
// successor that the card names, untouched or with its own ended turn.
func TestCodexInspect(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	idle := func(target CycleTarget) bool {
		t.Helper()
		pane, err := (codexCycle{}).Inspect(ctx, target, term.fence)
		if err != nil || pane.Prompt != "" {
			t.Fatalf("inspect: %+v %v", pane, err)
		}
		return pane.Idle
	}
	if !idle(target) {
		t.Fatal("requester ended its turn")
	}
	term.rollout("thread-a", "0.162.0", "task_started")
	if idle(target) {
		t.Fatal("requester busy")
	}
	term.rollout("thread-a", "0.162.0", "task_started", "task_complete")
	term.input = "draft"
	if idle(target) {
		t.Fatal("a draft")
	}
	term.input = ""
	if ok, _ := (codexCycle{}).Clear(ctx, target, term.fence); !ok {
		t.Fatal("clear")
	}
	after := target
	after.SuccessorEpoch = "thread-b"
	if !idle(after) {
		t.Fatal("untouched successor")
	}
	// The old thread's idle never admits input: a prompt after the card without its own
	// rollout waits, as does a busy successor.
	term.said = append(term.said, "› typed by someone", "")
	if idle(after) {
		t.Fatal("prompt after the card")
	}
	term.rollout("thread-b", "0.162.0", "task_started")
	if idle(after) {
		t.Fatal("busy successor")
	}
	term.rollout("thread-b", "0.162.0", "task_started", "task_complete")
	if !idle(after) {
		t.Fatal("successor ended its turn")
	}
	other := after
	other.SuccessorEpoch = "thread-z"
	if idle(other) {
		t.Fatal("another thread")
	}
	term.dialog, term.options = []string{"  Would you like to run the following command?", "› 1. Yes, proceed (y)", "  2. No (esc)", "  Press enter to confirm or esc to cancel"}, []string{"1", "2"}
	if pane, err := (codexCycle{}).Inspect(ctx, after, term.fence); err != nil || pane.Prompt != "native_dialog" {
		t.Fatalf("dialog: %+v %v", pane, err)
	}
}

// Pickup submits $pickup only when the menu lists one row for each verified file, so no other
// entry can run; any other count, or a scroll marker, erases it before Enter.
func TestCodexPickupRows(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		files  int
		rows   int
		scroll bool
		ok     bool
	}{
		{"one file, one row", 1, 1, false, true},
		{"two byte-equal files, two rows", 2, 2, false, true},
		{"one file, a second entry outside the roots", 1, 2, false, false},
		{"no row", 1, 0, false, false},
		{"a scroll marker", 1, 1, true, false},
	} {
		term := newCodexTerminal(t)
		target := term.target(t)
		writeEntry(t, filepath.Join(target.Directory, ".agents", "skills", "pickup", "SKILL.md"), target.Pickup.Content)
		if c.files == 2 {
			writeEntry(t, filepath.Join(target.Directory, ".codex", "skills", "pickup", "SKILL.md"), target.Pickup.Content)
		}
		for range c.rows {
			term.menu = append(term.menu, "pickup  [Skill] synthetic")
		}
		term.menu = append(term.menu, "picker  [Skill] another skill")
		term.scroll = c.scroll
		ok, err := (codexCycle{}).Pickup(ctx, target, "project", term.fence)
		entered := slices.Contains(term.sent(), "Enter")
		if ok != c.ok || entered != c.ok || !c.ok && (err == nil || err.Error() != "pickup_entry_unverified") {
			t.Fatalf("%s: %v %v keys %q", c.name, ok, err, term.sent())
		}
		if !c.ok && term.input != "" {
			t.Fatalf("%s: left %q", c.name, term.input)
		}
		if c.ok && !slices.Equal(term.sent(), []string{"-l $pickup", "Escape", "Enter"}) {
			t.Fatalf("%s: keys %q", c.name, term.sent())
		}
	}
}

// Choose enters the digit only while the same dialog shows; Codex takes it without Enter.
func TestCodexDialogChoice(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	term.dialog, term.options = []string{"  Allow the koinon MCP server to run tool \"peers\"?", "  › 1. Allow", "    2. Cancel", "  enter to submit | esc to cancel"}, []string{"1", "2"}
	dialog, err := (codexCycle{}).Dialog(ctx, target)
	if err != nil || !slices.Equal(dialog.Options, []string{"1", "2"}) {
		t.Fatalf("dialog %+v %v", dialog, err)
	}
	if _, err := (codexCycle{}).Choose(ctx, target, "another", "1", term.fence); !errors.Is(err, ErrCycleDialogChanged) || len(term.sent()) != 0 {
		t.Fatalf("changed dialog: %v %q", err, term.sent())
	}
	if ok, err := (codexCycle{}).Choose(ctx, target, dialog.Digest, "1", term.fence); !ok || err != nil || !slices.Equal(term.sent(), []string{"-l 1"}) {
		t.Fatalf("choose: %v %v %q", ok, err, term.sent())
	}
}

// Each input step reads the pane again right before its first key: a draft that someone
// started after the idle inspection, or a busy thread, gets no key, and the draft stays.
func TestCodexInputRechecksReadiness(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	if pane, err := (codexCycle{}).Inspect(ctx, target, term.fence); err != nil || !pane.Idle {
		t.Fatalf("idle first: %+v %v", pane, err)
	}
	term.input = "maintainer draft"
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); ok || err == nil || err.Error() != "pane_not_ready" || len(term.sent()) != 0 || term.input != "maintainer draft" {
		t.Fatalf("clear over a draft: %v %v %q %q", ok, err, term.sent(), term.input)
	}
	term.input = ""
	term.rollout("thread-a", "0.162.0", "task_started")
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); ok || err == nil || len(term.sent()) != 0 {
		t.Fatalf("clear of a busy thread: %v %v %q", ok, err, term.sent())
	}
	// After the clear, the pickup needs the proven successor, untouched.
	term.rollout("thread-a", "0.162.0", "task_started", "task_complete")
	if ok, _ := (codexCycle{}).Clear(ctx, target, term.fence); !ok {
		t.Fatal("clear")
	}
	writeEntry(t, filepath.Join(target.Directory, ".agents", "skills", "pickup", "SKILL.md"), target.Pickup.Content)
	term.menu = []string{"pickup  [Skill] synthetic"}
	after := target
	after.SuccessorEpoch = "thread-b"
	sent := len(term.sent())
	term.input = "maintainer draft"
	if ok, err := (codexCycle{}).Pickup(ctx, after, "project", term.fence); ok || err == nil || err.Error() != "pane_not_ready" || len(term.sent()) != sent || term.input != "maintainer draft" {
		t.Fatalf("pickup over a draft: %v %v %q %q", ok, err, term.sent()[sent:], term.input)
	}
	term.input = ""
	if ok, err := (codexCycle{}).Pickup(ctx, after, "project", term.fence); !ok || err != nil {
		t.Fatalf("pickup: %v %v", ok, err)
	}
}

// A draft typed right after the clear gets no /status, and the clear is not read back (its
// receipt is the empty cleared screen), so the cycle ends uncertain; the draft stays.
func TestCodexStatusAfterDraft(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	term.onClear = func() { term.input = "maintainer draft" }
	target := term.target(t)
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); ok || err == nil || err.Error() != "submit_not_read_back" {
		t.Fatalf("clear: %v %v", ok, err)
	}
	if want := []string{"-l /clear", "Enter"}; !slices.Equal(term.sent(), want) || term.input != "maintainer draft" {
		t.Fatalf("keys %q, input %q", term.sent(), term.input)
	}
	if epoch, _ := (codexCycle{}).NewEpoch(ctx, target); epoch != "" {
		t.Fatalf("epoch %q", epoch)
	}
}
