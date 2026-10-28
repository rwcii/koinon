package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The OpenCode cycle adapter (#513) against captured screens, a synthetic OpenCode server and
// a synthetic terminal. Nothing here starts OpenCode or reaches a real tmux server.

const (
	ocRequester = "ses_synthREQUESTER000000000001"
	ocSuccessor = "ses_synthSUCCESSOR000000000001"
)

func openCodeScreen(t *testing.T, name string) openCodeView {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "opencode-screens", "cycle-"+name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := readOpenCodeView(string(data))
	if !ok {
		t.Fatalf("%s: no session view", name)
	}
	return v
}

// Captured opencode 1.18.35 screens (paths and session IDs replaced): a finished turn with a
// Koinon tool call at 160 columns with the sidebar, the created session after the select, a
// wrapped tool call at 80 columns, a running turn, a draft, and a tool call that failed.
func TestOpenCodeScreenFixtures(t *testing.T) {
	for _, c := range []struct {
		name, text string
		empty      bool
		sessions   []string
	}{
		{"tool", "", false, []string{ocRequester}},
		{"empty", "", true, nil},
		{"wrapped-80", "", false, []string{ocSuccessor}},
		{"busy", "", false, nil},
		{"draft", "a draft that someone started", false, []string{ocSuccessor}},
		{"tool-error", "", false, []string{ocSuccessor}},
	} {
		v := openCodeScreen(t, c.name)
		if v.text != c.text || v.empty() != c.empty || strings.Join(v.sessions(), ",") != strings.Join(c.sessions, ",") {
			t.Fatalf("%s: text %q empty %v sessions %v", c.name, v.text, v.empty(), v.sessions())
		}
	}
	if _, ok := readOpenCodeView("  Ask anything…\n"); ok {
		t.Fatal("a screen without the composer is a session view")
	}
}

// openCodeToolRow renders a Koinon tool-call row as opencode 1.18.35 does: the glyph and the
// call in the muted color, the space between them in another.
func openCodeToolRow(call string) string {
	return "\x1b[48;2;10;10;10m     \x1b[38;2;128;128;128m⚙\x1b[38;2;255;255;255m \x1b[38;2;128;128;128m" + call + "\x1b[38;2;255;255;255m   "
}

// openCodeComposer is an empty composer with its rule and footer.
func openCodeComposer(draft string) []string {
	rows := []string{"  \x1b[38;2;92;156;245m┃\x1b[38;2;255;255;255m"}
	if draft != "" {
		rows = append(rows, "  ┃  "+draft)
	}
	return append(rows, "  ┃", "  ┃  Build · Synthetic Model", "  ╹"+strings.Repeat("▀", 70), "   /tmp/synthetic/work  ctrl+p commands")
}

// Only the evidenced rendering is a tool-call row: user text, tool output and reply text that
// imitate it are not, a continuation in another color is not joined, and every session that the
// view shows is named.
func TestOpenCodeToolRowImitations(t *testing.T) {
	quoted := "koinon_peers [koinon_session=" + ocSuccessor + "]"
	for _, c := range []struct {
		name string
		rows []string
		want string
	}{
		{"evidenced", []string{openCodeToolRow(quoted)}, ocSuccessor},
		{"user text", []string{"  \x1b[38;2;92;156;245m┃\x1b[38;2;255;255;255m  \x1b[38;2;238;238;238m⚙ " + quoted}, ""},
		{"tool output", []string{"  ┃  ⚙ " + quoted}, ""},
		{"reply text in one color", []string{"\x1b[48;2;10;10;10m     \x1b[38;2;238;238;238m⚙ " + quoted}, ""},
		{"reply without colors", []string{"     ⚙ " + quoted}, ""},
		{"glyph in another color", []string{"     \x1b[38;2;1;1;1m⚙\x1b[38;2;255;255;255m \x1b[38;2;128;128;128m" + quoted}, ""},
		{"other indent", []string{"   " + strings.TrimPrefix(openCodeToolRow(quoted), "\x1b[48;2;10;10;10m     ")}, ""},
		{"not a Koinon tool", []string{openCodeToolRow("bash [command=echo koinon_session=" + ocSuccessor + "]")}, ""},
		{"session not last", []string{openCodeToolRow("koinon_peers [koinon_session=" + ocSuccessor + ", x=1]")}, ""},
		{"wrapped", []string{openCodeToolRow("koinon_memory_recall [query=a long query that"), "       \x1b[38;2;128;128;128mwraps, koinon_session=" + ocSuccessor + "]"}, ocSuccessor},
		{"continuation in another color", []string{openCodeToolRow("koinon_memory_recall [query=a long query that"), "       \x1b[38;2;238;238;238mwraps, koinon_session=" + ocSuccessor + "]"}, ""},
		{"mixed sessions", []string{openCodeToolRow("koinon_peers [koinon_session=" + ocRequester + "]"), "", openCodeToolRow(quoted)}, ocRequester + "," + ocSuccessor},
	} {
		v, ok := readOpenCodeView(strings.Join(append(c.rows, openCodeComposer("")...), "\n"))
		if !ok || strings.Join(v.sessions(), ",") != c.want {
			t.Fatalf("%s: %v %v", c.name, ok, v.sessions())
		}
	}
}

// fakeOpenCode is a synthetic OpenCode server and the terminal that shows its TUI.
type fakeOpenCode struct {
	mu          sync.Mutex
	server      *httptest.Server
	password    string
	titles      map[string]string
	messages    map[string][]any
	busy        map[string]bool
	commands    []openCodeCommand
	permissions []openCodePending
	questions   []openCodePending
	shown       string // the session that the TUI shows
	noApply     bool   // a select returns true but the TUI keeps its session
	draft       string
	extra       []string // rows shown above the composer
	created     int
	selects     []string
	sent        []map[string]string
	replies     []string
	keys        []string
	fences      int
	failFence   bool
	// commandModel is the model that the server records for a command, "" for the requested one.
	commandModel  *openCodePair
	home, dir     string
	pid, start    int64
	createRefused bool
}

func newFakeOpenCode(t *testing.T) *fakeOpenCode {
	f := &fakeOpenCode{password: strings.Repeat("ab", 32), titles: map[string]string{ocRequester: "requester"},
		messages: map[string][]any{}, busy: map[string]bool{}, shown: ocRequester, home: t.TempDir(), dir: t.TempDir(), pid: 5151, start: 99}
	f.requesterReply("openrouter", "qwen/qwen3.6-35b-a3b")
	f.installSkill(t, filepath.Join(f.home, ".config", "opencode", "skills"))
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	previous := openCodeEvidencedPlatforms
	openCodeEvidencedPlatforms = map[string]bool{runtime.GOOS: true}
	t.Cleanup(func() { openCodeEvidencedPlatforms = previous })
	return f
}

// requesterReply makes the requester's newest finished reply come from provider and model.
func (f *fakeOpenCode) requesterReply(provider, model string) {
	f.messages[ocRequester] = []any{map[string]any{"info": map[string]any{"role": "assistant", "finish": "stop", "providerID": provider,
		"modelID": model, "time": map[string]any{"completed": 1000}}}}
}

// installSkill writes the binary's skill under root and lists it as OpenCode does.
func (f *fakeOpenCode) installSkill(t *testing.T, root string) {
	dir := filepath.Join(root, PickupSkillName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), PickupSkill(), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := pickupSkillBody(PickupSkill())
	f.commands = []openCodeCommand{{Name: "review", Source: "command", Template: "other"},
		{Name: PickupSkillName, Source: "skill", Template: body + openCodeTemplateHead + dir + openCodeTemplateTail}}
}

func (f *fakeOpenCode) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, password, ok := r.BasicAuth(); !ok || user != "opencode" || password != f.password {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body map[string]string
	json.NewDecoder(r.Body).Decode(&body)
	reply := func(value any) { json.NewEncoder(w).Encode(value) }
	path := r.URL.Path
	id, rest, _ := strings.Cut(strings.TrimPrefix(path, "/session/"), "/")
	switch {
	case r.Method == "GET" && path == "/command":
		reply(f.commands)
	case r.Method == "GET" && path == "/permission":
		reply(f.permissions)
	case r.Method == "GET" && path == "/question":
		reply(f.questions)
	case r.Method == "GET" && path == "/session/status":
		states := map[string]any{}
		for s, busy := range f.busy {
			if busy {
				states[s] = map[string]string{"type": "busy"}
			}
		}
		reply(states)
	case r.Method == "POST" && path == "/session":
		if f.createRefused {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.created++
		f.titles[ocSuccessor] = body["title"]
		reply(map[string]string{"id": ocSuccessor, "title": body["title"]})
	case r.Method == "POST" && path == "/tui/select-session":
		f.selects = append(f.selects, body["sessionID"])
		if !f.noApply {
			f.shown = body["sessionID"]
		}
		reply(true)
	case r.Method == "POST" && strings.HasPrefix(path, "/permission/"):
		permission := strings.TrimSuffix(strings.TrimPrefix(path, "/permission/"), "/reply")
		f.replies = append(f.replies, permission+" "+body["reply"])
		f.permissions = nil
		reply(true)
	case strings.HasPrefix(path, "/session/") && f.titles[id] == "":
		w.WriteHeader(http.StatusNotFound)
	case r.Method == "GET" && rest == "":
		reply(map[string]string{"id": id, "title": f.titles[id]})
	case r.Method == "GET" && rest == "message":
		messages := f.messages[id]
		if messages == nil {
			messages = []any{}
		}
		reply(messages)
	case r.Method == "POST" && rest == "command":
		f.sent = append(f.sent, body)
		provider, model, _ := strings.Cut(body["model"], "/")
		if f.commandModel != nil {
			provider, model = f.commandModel.provider, f.commandModel.model
		}
		var template string
		for _, c := range f.commands {
			if c.Name == body["command"] {
				template = c.Template
			}
		}
		f.messages[id] = append(f.messages[id], map[string]any{
			"info":  map[string]any{"id": body["messageID"], "role": "user", "model": map[string]string{"providerID": provider, "modelID": model}},
			"parts": []map[string]string{{"type": "text", "text": strings.TrimSpace(template)}}})
		reply(map[string]any{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeOpenCode) screen() string {
	var rows []string
	if len(f.messages[f.shown]) > 0 {
		rows = append(rows, "  ┃  a prompt", "", openCodeToolRow("koinon_peers [koinon_session="+f.shown+"]"), "", "     done", "")
	}
	rows = append(rows, f.extra...)
	return strings.Join(append(rows, openCodeComposer(f.draft)...), "\n") + "\n"
}

func (f *fakeOpenCode) tmux(_ context.Context, socket string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case args[0] == "display-message" && args[len(args)-1] == "#{pane_in_mode}":
		return "0\n", nil
	case args[0] == "display-message":
		return "%6 5151\n", nil
	case args[0] == "capture-pane":
		return f.screen(), nil
	case args[0] == "send-keys":
		f.keys = append(f.keys, strings.Join(args[3:], " "))
		return "", nil
	}
	return "", errors.New("unexpected tmux call")
}

func (f *fakeOpenCode) target(state string) CycleTarget {
	t := CycleTarget{CycleID: "synthetic-cycle", Family: "opencode", SessionID: ocRequester, HostPID: f.pid, HostStart: f.start,
		Socket: "/tmp/synthetic-socket", Pane: "%6", Epoch: ocRequester, Holds: true, Pickup: pickupSkillEntry(), State: state,
		Directory: f.dir, openCode: &LaunchTarget{Address: strings.TrimPrefix(f.server.URL, "http://"), Password: f.password},
		tools: hostTools{tmux: f.tmux,
			processStart:   func(int) (int64, error) { return f.start, nil },
			processParents: func() (map[int]int, error) { return map[int]int{}, nil },
			processCommand: func(int) (string, []string, error) { return "/synthetic/opencode", nil, nil },
			processEnvironment: func(_ int, name string) (string, error) {
				if name == "HOME" {
					return f.home, nil
				}
				return "", nil
			}}}
	if state != cycleReady && state != cycleClearIntent {
		t.SuccessorEpoch = ocSuccessor
	}
	if state == cyclePickupIntent || state == cycleWaitingPickup {
		t.pickupAt = 1793077135435
	}
	return t
}

func (f *fakeOpenCode) fence(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fences++
	if f.failFence {
		return ErrCycleChanged
	}
	return nil
}

func (f *fakeOpenCode) set(change func(*fakeOpenCode)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// Probe needs the platform, the launched server, the pane, the participant address, a model
// pair that the command string carries unchanged and the effective pickup entry.
func TestOpenCodeProbe(t *testing.T) {
	a := openCodeCycle{}
	f := newFakeOpenCode(t)
	if p := a.Probe(context.Background(), f.target(cycleReady)); p.Reason != "" || p.Entry != PickupSkillName {
		t.Fatalf("ready probe: %+v", p)
	}
	for _, c := range []struct {
		name, want string
		change     func(*fakeOpenCode, *CycleTarget)
	}{
		{"platform", "platform_unverified", func(*fakeOpenCode, *CycleTarget) { openCodeEvidencedPlatforms = map[string]bool{} }},
		{"server", "native_registry_unavailable", func(_ *fakeOpenCode, t *CycleTarget) { t.openCode = nil }},
		{"pane", "pane_unproven", func(_ *fakeOpenCode, t *CycleTarget) { t.HostStart = 1 }},
		{"participant", "participant_unheld", func(_ *fakeOpenCode, t *CycleTarget) { t.Holds = false }},
		{"no reply", "model_unavailable", func(f *fakeOpenCode, _ *CycleTarget) { f.messages[ocRequester] = nil }},
		{"provider with a slash", "model_unavailable", func(f *fakeOpenCode, _ *CycleTarget) { f.requesterReply("a/b", "c") }},
		{"no entry", "pickup_entry_missing", func(f *fakeOpenCode, _ *CycleTarget) { f.commands = f.commands[:1] }},
		{"two entries", "pickup_entry_unverified", func(f *fakeOpenCode, _ *CycleTarget) { f.commands = append(f.commands, f.commands[1]) }},
		{"another template", "pickup_entry_differs", func(f *fakeOpenCode, _ *CycleTarget) { f.commands[1].Template = "do something else" }},
		{"a command, not a skill", "pickup_entry_differs", func(f *fakeOpenCode, _ *CycleTarget) { f.commands[1].Source = "command" }},
		{"no body", "pickup_entry_differs", func(f *fakeOpenCode, _ *CycleTarget) {
			f.commands[1].Template = filepath.Join(f.home, ".config", "opencode", "skills", PickupSkillName) + openCodeTemplateTail
		}},
		{"no tail", "pickup_entry_differs", func(f *fakeOpenCode, _ *CycleTarget) {
			f.commands[1].Template = strings.TrimSuffix(f.commands[1].Template, openCodeTemplateTail)
		}},
		{"unknown root", "pickup_entry_unverified", func(f *fakeOpenCode, _ *CycleTarget) {
			f.commands[1].Template = strings.Replace(f.commands[1].Template, f.home, "/elsewhere", 1)
		}},
		{"file differs", "pickup_entry_differs", func(f *fakeOpenCode, _ *CycleTarget) {
			os.WriteFile(filepath.Join(f.home, ".config", "opencode", "skills", PickupSkillName, "SKILL.md"), []byte("other"), 0o600)
		}},
	} {
		f := newFakeOpenCode(t)
		target := f.target(cycleReady)
		c.change(f, &target)
		if p := a.Probe(context.Background(), target); p.Reason != c.want {
			t.Fatalf("%s: %+v", c.name, p)
		}
		openCodeEvidencedPlatforms = map[string]bool{runtime.GOOS: true}
	}
	// An equal copy that OpenCode lists from the person's ~/.claude runs as well.
	f = newFakeOpenCode(t)
	f.installSkill(t, filepath.Join(f.home, ".claude", "skills"))
	if p := a.Probe(context.Background(), f.target(cycleReady)); p.Reason != "" {
		t.Fatalf("equal copy: %+v", p)
	}
	// A pair with a slash in the model carries unchanged.
	pair, err := openCodeRequesterPair(context.Background(), f.target(cycleReady))
	if err != nil || pair != (openCodePair{"openrouter", "qwen/qwen3.6-35b-a3b"}) {
		t.Fatalf("pair: %+v %v", pair, err)
	}
}

// The create, the select and the reset's proof: each input is sent once after a fresh read
// shows the subject ready and after the fence; the proof is that exact session, empty, in an
// empty view.
func TestOpenCodeCreateSelectProof(t *testing.T) {
	a := openCodeCycle{}
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		change func(*fakeOpenCode)
	}{
		{"draft", func(f *fakeOpenCode) { f.draft = "someone types" }},
		{"running turn", func(f *fakeOpenCode) { f.busy[ocRequester] = true }},
		{"dialog", func(f *fakeOpenCode) {
			f.permissions = []openCodePending{{ID: "per_1", SessionID: ocRequester, Permission: "bash"}}
		}},
		{"fence", func(f *fakeOpenCode) { f.failFence = true }},
	} {
		f := newFakeOpenCode(t)
		c.change(f)
		if id, err := a.Create(ctx, f.target(cycleClearIntent), f.fence); err == nil || id != "" || f.created != 0 {
			t.Fatalf("%s: create %q %v %d", c.name, id, err, f.created)
		}
	}
	// A dialog of another session does not stop the input.
	f := newFakeOpenCode(t)
	f.permissions = []openCodePending{{ID: "per_1", SessionID: "ses_other", Permission: "bash"}}
	id, err := a.Create(ctx, f.target(cycleClearIntent), f.fence)
	if err != nil || id != ocSuccessor || f.created != 1 || f.titles[ocSuccessor] != openCodeTitle("synthetic-cycle") {
		t.Fatalf("create: %q %v %+v", id, err, f.titles)
	}
	// Before the select the pane shows the requester: no proof.
	if epoch, err := a.NewEpoch(ctx, f.target(cycleClearReceived)); epoch != "" || err != nil {
		t.Fatalf("proof before select: %q %v", epoch, err)
	}
	if ok, err := a.Select(ctx, f.target(cycleClearReceived), f.fence); !ok || err != nil || strings.Join(f.selects, ",") != ocSuccessor {
		t.Fatalf("select: %v %v %v", ok, err, f.selects)
	}
	if epoch, err := a.NewEpoch(ctx, f.target(cycleClearReceived)); epoch != ocSuccessor || err != nil {
		t.Fatalf("proof: %q %v", epoch, err)
	}
	for _, c := range []struct {
		name   string
		change func(*fakeOpenCode)
	}{
		{"other title", func(f *fakeOpenCode) { f.titles[ocSuccessor] = "another" }},
		{"deleted", func(f *fakeOpenCode) { delete(f.titles, ocSuccessor) }},
		{"messages", func(f *fakeOpenCode) { f.messages[ocSuccessor] = []any{map[string]any{}} }},
		{"view not empty", func(f *fakeOpenCode) { f.extra = []string{"     some reply"} }},
		{"draft", func(f *fakeOpenCode) { f.draft = "typed" }},
	} {
		g := newFakeOpenCode(t)
		g.titles[ocSuccessor], g.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
		c.change(g)
		if epoch, _ := a.NewEpoch(ctx, g.target(cycleClearReceived)); epoch != "" {
			t.Fatalf("%s: proven", c.name)
		}
	}
	// No recorded ID proves nothing; the title is no substitute.
	if epoch, _ := a.NewEpoch(ctx, f.target(cycleClearIntent)); epoch != "" {
		t.Fatalf("proof without the recorded ID: %q", epoch)
	}
	// A select that OpenCode accepts but does not apply leaves another empty session in view:
	// the creation is proven, the display is not.
	g := newFakeOpenCode(t)
	g.titles[ocSuccessor], g.noApply, g.messages["ses_otherempty"], g.titles["ses_otherempty"] = openCodeTitle("synthetic-cycle"), true, nil, "x"
	g.shown = "ses_otherempty"
	a.Select(ctx, g.target(cycleClearReceived), g.fence)
	if epoch, _ := a.NewEpoch(ctx, g.target(cycleClearReceived)); epoch != ocSuccessor {
		t.Fatalf("unapplied select into an empty session: %q", epoch)
	}
	if shown, foreign, err := a.PickupDisplay(ctx, g.target(cycleWaitingPickup)); shown || foreign || err != nil {
		t.Fatalf("display of an empty other session: %v %v %v", shown, foreign, err)
	}
	if len(g.keys) != 0 || len(f.keys) != 0 {
		t.Fatalf("keys sent: %v %v", g.keys, f.keys)
	}
}

// The pickup is the skill command with the derived message ID and the requester's exact model;
// its receipt is that message as the session's only user message.
func TestOpenCodePickupAndReceipt(t *testing.T) {
	a := openCodeCycle{}
	ctx := context.Background()
	f := newFakeOpenCode(t)
	f.titles[ocSuccessor], f.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
	target := f.target(cyclePickupIntent)
	if ok, err := a.PickupReceipt(ctx, target); ok || err != nil {
		t.Fatalf("receipt before the pickup: %v %v", ok, err)
	}
	if err := a.SendPickup(ctx, target, f.fence); err != nil {
		t.Fatal(err)
	}
	id := openCodeMessageID(target)
	if len(f.sent) != 1 || f.sent[0]["command"] != PickupSkillName || f.sent[0]["arguments"] != "" || f.sent[0]["messageID"] != id ||
		f.sent[0]["model"] != "openrouter/qwen/qwen3.6-35b-a3b" {
		t.Fatalf("sent: %+v", f.sent)
	}
	// The ID sorts as OpenCode's own: the intent's time, then the cycle's digits.
	if !strings.HasPrefix(id, "msg_17bb9c84b000") || len(id) != 30 || id != openCodeMessageID(f.target(cyclePickupIntent)) {
		t.Fatalf("message ID %q", id)
	}
	if ok, err := a.PickupReceipt(ctx, target); !ok || err != nil {
		t.Fatalf("receipt: %v %v", ok, err)
	}
	// The pickup is sent only while the successor is empty and idle in an empty view.
	for _, c := range []struct {
		name   string
		change func(*fakeOpenCode)
	}{
		{"messages", func(f *fakeOpenCode) { f.messages[ocSuccessor] = []any{map[string]any{}} }},
		{"busy", func(f *fakeOpenCode) { f.busy[ocSuccessor] = true }},
		{"draft", func(f *fakeOpenCode) { f.draft = "typed" }},
		{"fence", func(f *fakeOpenCode) { f.failFence = true }},
		{"entry", func(f *fakeOpenCode) { f.commands = f.commands[:1] }},
		{"model", func(f *fakeOpenCode) { f.messages[ocRequester] = nil }},
	} {
		g := newFakeOpenCode(t)
		g.titles[ocSuccessor], g.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
		c.change(g)
		if err := a.SendPickup(ctx, g.target(cyclePickupIntent), g.fence); err == nil || len(g.sent) != 0 {
			t.Fatalf("%s: sent %v %+v", c.name, err, g.sent)
		}
	}
	// Another model, another ID, a second user message or another text is no receipt.
	for _, c := range []struct {
		name   string
		change func(*fakeOpenCode, CycleTarget)
	}{
		{"model", func(f *fakeOpenCode, t CycleTarget) {
			f.commandModel = &openCodePair{"openrouter", "qwen"}
			openCodeCycle{}.SendPickup(ctx, t, f.fence)
		}},
		{"id", func(f *fakeOpenCode, t CycleTarget) {
			t.pickupAt++
			openCodeCycle{}.SendPickup(ctx, t, f.fence)
		}},
		{"second user message", func(f *fakeOpenCode, t CycleTarget) {
			openCodeCycle{}.SendPickup(ctx, t, f.fence)
			f.messages[ocSuccessor] = append(f.messages[ocSuccessor], map[string]any{"info": map[string]any{"role": "user"}})
		}},
		{"text", func(f *fakeOpenCode, t CycleTarget) {
			openCodeCycle{}.SendPickup(ctx, t, f.fence)
			f.commands[1].Template += "x"
			os.WriteFile(filepath.Join(f.home, ".config", "opencode", "skills", PickupSkillName, "SKILL.md"), PickupSkill(), 0o600)
		}},
	} {
		g := newFakeOpenCode(t)
		g.titles[ocSuccessor], g.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
		c.change(g, g.target(cyclePickupIntent))
		if ok, _ := a.PickupReceipt(ctx, g.target(cyclePickupIntent)); ok {
			t.Fatalf("%s: a receipt", c.name)
		}
	}
}

// The display proof: a current view with the successor's tool-call row and no other session's.
func TestOpenCodePickupDisplay(t *testing.T) {
	a := openCodeCycle{}
	ctx := context.Background()
	f := newFakeOpenCode(t)
	f.titles[ocSuccessor] = openCodeTitle("synthetic-cycle")
	f.messages[ocSuccessor] = []any{map[string]any{}}
	target := f.target(cycleWaitingPickup)
	if shown, foreign, err := a.PickupDisplay(ctx, target); shown || !foreign || err != nil {
		t.Fatalf("the requester in view: %v %v %v", shown, foreign, err)
	}
	f.set(func(f *fakeOpenCode) { f.shown = ocSuccessor })
	if shown, foreign, err := a.PickupDisplay(ctx, target); !shown || foreign || err != nil {
		t.Fatalf("the successor in view: %v %v %v", shown, foreign, err)
	}
	f.set(func(f *fakeOpenCode) {
		f.extra = []string{openCodeToolRow("koinon_peers [koinon_session=" + ocRequester + "]")}
	})
	if shown, foreign, err := a.PickupDisplay(ctx, target); !shown || !foreign || err != nil {
		t.Fatalf("mixed view: %v %v %v", shown, foreign, err)
	}
	target.HostStart = 1
	if _, _, err := a.PickupDisplay(ctx, target); err == nil {
		t.Fatal("an unproven pane showed the successor")
	}
}

// A pending permission or question of exactly the subject session is its dialog; a permission
// is answered by the maintainer's option while it is the same request, and read back gone.
func TestOpenCodeDialogChoice(t *testing.T) {
	a := openCodeCycle{}
	ctx := context.Background()
	f := newFakeOpenCode(t)
	target := f.target(cycleWaitingPickup)
	f.permissions = []openCodePending{{ID: "per_1", SessionID: ocRequester, Permission: "bash", Patterns: []string{"ls"}}}
	if _, err := a.Dialog(ctx, target); err == nil {
		t.Fatal("the requester's dialog is the successor's")
	}
	f.set(func(f *fakeOpenCode) {
		f.permissions = []openCodePending{{ID: "per_2", SessionID: ocSuccessor, Permission: "bash", Patterns: []string{"uname -s"}}}
	})
	dialog, err := a.Dialog(ctx, target)
	if err != nil || strings.Join(dialog.Options, ",") != "1,2,3" || !strings.Contains(strings.Join(dialog.Lines, "\n"), "uname -s") {
		t.Fatalf("dialog: %+v %v", dialog, err)
	}
	if pane, err := a.Inspect(ctx, target, f.fence); err != nil || pane.Prompt != "native_dialog" {
		t.Fatalf("inspect: %+v %v", pane, err)
	}
	if ok, err := a.Choose(ctx, target, "other", "1", f.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("stale digest: %v %v", ok, err)
	}
	if ok, err := a.Choose(ctx, target, dialog.Digest, "1", f.fence); !ok || err != nil || strings.Join(f.replies, ",") != "per_2 once" {
		t.Fatalf("choose: %v %v %v", ok, err, f.replies)
	}
	f.set(func(f *fakeOpenCode) {
		f.questions = []openCodePending{{ID: "que_1", SessionID: ocSuccessor, Questions: []struct {
			Question string `json:"question"`
		}{{"Which one?"}}}}
	})
	question, err := a.Dialog(ctx, target)
	if err != nil || len(question.Options) != 0 || question.Digest == "" {
		t.Fatalf("question: %+v %v", question, err)
	}
	if ok, err := a.Choose(ctx, target, question.Digest, "1", f.fence); ok || !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("question choice: %v %v", ok, err)
	}
	if a.Answerable("native_dialog") {
		t.Fatal("the adapter answers dialogs itself")
	}
}

// The pickup request ends at its own timeout while OpenCode runs the turn.
func TestOpenCodePickupTimeout(t *testing.T) {
	previous := openCodeCommandTimeout
	openCodeCommandTimeout = 100 * time.Millisecond
	t.Cleanup(func() { openCodeCommandTimeout = previous })
	f := newFakeOpenCode(t)
	f.titles[ocSuccessor], f.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
	release := make(chan struct{})
	handler := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/command") {
			<-release
		}
	})
	t.Cleanup(func() { close(release) })
	start := time.Now()
	if err := (openCodeCycle{}).SendPickup(context.Background(), f.target(cyclePickupIntent), f.fence); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	if ok, err := (openCodeCycle{}).PickupReceipt(context.Background(), f.target(cyclePickupIntent)); !ok || err != nil {
		t.Fatalf("receipt after the timeout: %v %v", ok, err)
	}
}

// The replies' model: each assistant reply of the created session must name the pair that its
// receipted pickup asked for, and there must be one.
func TestOpenCodePickupModel(t *testing.T) {
	a := openCodeCycle{}
	ctx := context.Background()
	f := newFakeOpenCode(t)
	f.titles[ocSuccessor], f.shown = openCodeTitle("synthetic-cycle"), ocSuccessor
	target := f.target(cyclePickupIntent)
	if err := a.SendPickup(ctx, target, f.fence); err != nil {
		t.Fatal(err)
	}
	reply := func(provider, model string) {
		f.set(func(f *fakeOpenCode) {
			f.messages[ocSuccessor] = append(f.messages[ocSuccessor], map[string]any{"info": map[string]any{
				"role": "assistant", "providerID": provider, "modelID": model}})
		})
	}
	if _, err := a.PickupModel(ctx, target); err == nil {
		t.Fatal("no reply proved the model")
	}
	reply("openrouter", "qwen/qwen3.6-35b-a3b")
	if ok, err := a.PickupModel(ctx, target); !ok || err != nil {
		t.Fatalf("same model: %v %v", ok, err)
	}
	reply("other-provider", "config-default")
	if ok, err := a.PickupModel(ctx, target); ok || err != nil {
		t.Fatalf("another model: %v %v", ok, err)
	}
	// Without the receipted pickup no pair is known.
	other := target
	other.pickupAt++
	if _, err := a.PickupModel(ctx, other); err == nil {
		t.Fatal("a pickup with another ID proved the model")
	}
}
