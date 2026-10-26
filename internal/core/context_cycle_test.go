package core

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAdapter is a synthetic cycle adapter: it enters no input and records each call.
type fakeAdapter struct {
	mu      sync.Mutex
	pane    PaneState
	inspect error
	// inMode puts the pane in copy mode: Inspect calls the fence before it would send a key.
	inMode   bool
	clear    bool
	epoch    string
	pickup   bool
	answered bool
	probe    CycleProbe
	calls    []string
	// beforeClear runs as the clear starts, before its fence: a race with a dashboard action.
	beforeClear func()
	dialog      CycleDialog
	// afterChoose runs once the option is entered: the call that the dialog approved.
	afterChoose func()
}

func (f *fakeAdapter) Version() string { return "synthetic-1" }
func (f *fakeAdapter) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}
func (f *fakeAdapter) Probe(context.Context, CycleTarget) CycleProbe {
	f.record("probe")
	return f.probe
}
func (f *fakeAdapter) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	f.record("inspect " + t.Pane)
	if f.inMode {
		if err := fence(ctx); err != nil {
			return PaneState{}, err
		}
		f.record("cancel copy mode")
	}
	return f.pane, f.inspect
}

// Every input passes the fence first, as the real adapter does before each key.
func (f *fakeAdapter) Clear(ctx context.Context, _ CycleTarget, fence CycleFence) (bool, error) {
	if f.beforeClear != nil {
		f.beforeClear()
	}
	if err := fence(ctx); err != nil {
		return false, err
	}
	f.record("clear")
	return f.clear, nil
}
func (f *fakeAdapter) NewEpoch(context.Context, CycleTarget) (string, error) {
	f.record("epoch")
	return f.epoch, nil
}
func (f *fakeAdapter) Pickup(ctx context.Context, _ CycleTarget, fence CycleFence) (bool, error) {
	if err := fence(ctx); err != nil {
		return false, err
	}
	f.record("pickup")
	return f.pickup, nil
}
func (f *fakeAdapter) Answerable(prompt string) bool { return prompt != "native_dialog" }
func (f *fakeAdapter) Answer(ctx context.Context, _ CycleTarget, prompt string, fence CycleFence) (bool, error) {
	if err := fence(ctx); err != nil {
		return false, err
	}
	f.record("answer " + prompt)
	return f.answered, nil
}
func (f *fakeAdapter) Dialog(context.Context, CycleTarget) (CycleDialog, error) {
	f.record("dialog")
	return f.dialog, nil
}
func (f *fakeAdapter) Choose(ctx context.Context, _ CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	if digest != f.dialog.Digest {
		return false, ErrCycleDialogChanged
	}
	if err := fence(ctx); err != nil {
		return false, err
	}
	f.record("choose " + option)
	if f.afterChoose != nil {
		f.afterChoose()
	}
	return true, nil
}
func (f *fakeAdapter) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// cycleFixture is one enabled, launched Claude session in a repository that ignores its
// _handoff directory, with a synthetic adapter and a controllable clock.
type cycleFixture struct {
	s       *Store
	root    string
	repo    string
	session Session
	k       Key
	adapter *fakeAdapter
	clock   time.Time
}

func newCycleFixture(t *testing.T) *cycleFixture {
	t.Helper()
	s, root := testStore(t)
	f := &cycleFixture{s: s, root: root, adapter: &fakeAdapter{pane: PaneState{Idle: true}, clear: true, epoch: "synthetic-epoch-2", pickup: true, answered: true, probe: CycleProbe{Entry: "koinon-pickup"}}}
	f.clock = time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return f.clock }
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": f.adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	f.repo = namedRepo(t, "cycle-repo")
	if err := os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("_handoff/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.session = join(t, s, "claude", "synthetic-a", f.repo)
	f.k = Key{"claude", "synthetic-a"}
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, f.k.ID); err != nil {
		t.Fatal(err)
	}
	f.session = f.reload(t, f.k)
	if _, err := s.SetContextEnable(roomCtx("context-enable"), f.session.Name, f.session.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *cycleFixture) reload(t *testing.T, k Key) Session {
	t.Helper()
	session, err := scanSession(f.s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), f.s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// observe reports used of 1,000 tokens for the session's epoch.
func (f *cycleFixture) observe(t *testing.T, k Key, used int64, epoch string) {
	t.Helper()
	f.clock = f.clock.Add(time.Millisecond) // a newer report replaces the value
	if err := f.s.Observe(context.Background(), Observation{Caller: k, Context: &ObservedValue{Source: "claude_statusline",
		At: f.s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(used), Epoch: epoch}}); err != nil {
		t.Fatal(err)
	}
}

// handoff writes a handoff file in the session's own directory and returns its name.
func (f *cycleFixture) handoff(t *testing.T, agent, name, body string) string {
	t.Helper()
	dir := filepath.Join(f.repo, "_handoff", agent)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// The file is written at the fixture's clock, which a test can move past real time.
	if err := os.Chtimes(filepath.Join(dir, name), f.clock, f.clock); err != nil {
		t.Fatal(err)
	}
	return name
}

func (f *cycleFixture) status(t *testing.T, k Key) *CycleView {
	t.Helper()
	// Keep the session registered while the clock moves past its lifetime.
	if _, err := fixtureExec(f.s, `UPDATE sessions SET expires_at=? WHERE family=? AND id=? AND retired_at=0`, f.clock.Add(time.Hour).UnixMilli(), k.Family, k.ID); err != nil {
		t.Fatal(err)
	}
	v, err := f.s.ContextCycleStatus(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *cycleFixture) step(t *testing.T) {
	t.Helper()
	if err := f.s.cycleStep(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *cycleFixture) debt(t *testing.T) int64 {
	t.Helper()
	debt, err := debtPages(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	return debt
}

// A crossing admits one cycle with its typed overwatch request; the requester reports its
// handoff; the controller clears, proves the new epoch and enters pickup; the retained
// caller reports pickup under its new epoch, and the cycle completes with its credit spent.
func TestContextCycleRetainedCallerFlow(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 700, "synthetic-epoch-1")
	if f.status(t, f.k) != nil {
		t.Fatal("a cycle below the threshold")
	}
	for range 3 {
		f.observe(t, f.k, 800, "synthetic-epoch-1")
	}
	v := f.status(t, f.k)
	if v == nil || v.State != cycleWaitingReady || v.Revision != 1 || v.Role != "requester" || len(v.Events) != 1 {
		t.Fatalf("admission: %+v", v)
	}
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycles`) != 1 || f.debt(t) != cycleCreditPages {
		t.Fatal("repeated samples admitted another cycle or no credit")
	}
	var sender, kind, cycle, body string
	if err := f.s.db.QueryRow(`SELECT sender_family,system_kind,cycle_id,body FROM messages WHERE recipient_family='claude' AND recipient_id=?`, f.k.ID).
		Scan(&sender, &kind, &cycle, &body); err != nil {
		t.Fatal(err)
	}
	if sender != "overwatch" || kind != cycleRequestKind || cycle != v.ID || !strings.Contains(body, v.ID) {
		t.Fatalf("request: %s %s %s %q", sender, kind, cycle, body)
	}
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic handoff")
	// Wrong epoch, revision and artifact are refused; nothing changes.
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-9", v.ID, 1, name); !errors.Is(err, ErrCycleEpoch) {
		t.Fatalf("other epoch: %v", err)
	}
	if got, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 2, name); !errors.Is(err, ErrCycleChanged) || got.State != cycleWaitingReady {
		t.Fatalf("stale revision: %v %+v", err, got)
	}
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, "2026-10-13-120001.md"); !artifactRefused(err, "") {
		t.Fatalf("missing artifact: %v", err)
	}
	ready, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name)
	if err != nil || ready.State != cycleReady || ready.Revision != 2 || ready.Artifact != name {
		t.Fatalf("ready: %v %+v", err, ready)
	}
	// An identical retry returns the receipt and writes nothing.
	events := count(t, f.s, `SELECT COUNT(*) FROM context_cycle_events`)
	if again, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name); err != nil || again.Revision != 2 ||
		count(t, f.s, `SELECT COUNT(*) FROM context_cycle_events`) != events {
		t.Fatalf("retry: %v %+v", err, again)
	}
	// While the cycle owns the pane, the session's ordinary wake waits.
	if held, err := f.s.paneHeldByCycle(context.Background(), f.k); err != nil || !held {
		t.Fatalf("pane not held: %v %v", held, err)
	}
	f.step(t) // inspect, clear intent, clear, received
	if v = f.status(t, f.k); v.State != cycleClearReceived || f.adapter.count("clear") != 1 {
		t.Fatalf("after clear: %+v %v", v, f.adapter.calls)
	}
	f.step(t) // new epoch proven
	if v = f.status(t, f.k); v.State != cycleResetVerified {
		t.Fatalf("after reset: %+v", v)
	}
	f.step(t) // pickup intent, pickup, waiting
	if v = f.status(t, f.k); v.State != cycleWaitingPickup || f.adapter.count("pickup") != 1 {
		t.Fatalf("after pickup input: %+v", v)
	}
	f.step(t) // waiting for the report enters nothing
	if f.adapter.count("clear") != 1 || f.adapter.count("pickup") != 1 {
		t.Fatalf("input repeated: %v", f.adapter.calls)
	}
	// The pre-reset epoch cannot report pickup; the proven new one can.
	if _, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name, "resumed", "synthetic report"); !errors.Is(err, ErrCycleEpoch) {
		t.Fatalf("old epoch pickup: %v", err)
	}
	done, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work")
	if err != nil || done.State != cycleComplete || done.Outcome != cycleComplete || done.ReportBytes == 0 {
		t.Fatalf("pickup: %v %+v", err, done)
	}
	if f.debt(t) != 0 {
		t.Fatalf("credit not spent: %d", f.debt(t))
	}
	enable, _ := f.s.contextEnables(context.Background())
	if !enable[f.k].Enabled {
		t.Fatal("the retained caller lost its Enable")
	}
	if held, _ := f.s.paneHeldByCycle(context.Background(), f.k); held {
		t.Fatal("pane still held after completion")
	}
	// The audit and events hold no native ID, epoch or report text.
	for _, query := range []string{`SELECT group_concat(target||result||reason) FROM audit WHERE action='context-cycle'`,
		`SELECT group_concat(stage||result||reason) FROM context_cycle_events`} {
		var text string
		if err := f.s.db.QueryRow(query).Scan(&text); err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{"synthetic-a", "synthetic-epoch", "synthetic restored work", "synthetic-socket"} {
			if strings.Contains(text, private) {
				t.Fatalf("%q leaks %q", text, private)
			}
		}
	}
}

// Another session, an ordinary message and an acknowledgement advance no cycle.
func TestContextCycleOnlyItsSessionReports(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	if v == nil {
		t.Fatal("no cycle")
	}
	other := join(t, f.s, "claude", "synthetic-b", f.repo)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic handoff")
	if _, err := f.s.ContextCycleReady(context.Background(), Key{"claude", other.ID}, "synthetic-epoch-1", v.ID, 1, name); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("other session: %v", err)
	}
	if _, err := f.s.Send(context.Background(), Key{"claude", other.ID}, f.session.Name, "context_cycle_ready "+v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Ack(context.Background(), f.k, 1); err != nil {
		t.Fatal(err)
	}
	if v2 := f.status(t, f.k); v2.State != cycleWaitingReady || v2.Revision != 1 {
		t.Fatalf("a message or acknowledgement advanced the cycle: %+v", v2)
	}
	if _, err := f.s.Send(context.Background(), Key{"claude", other.ID}, overwatchName, "synthetic"); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("send to overwatch: %v", err)
	}
}

// The artifact must be a new, bounded, regular, ignored and untracked file in the session's
// own handoff directory of the main checkout.
func TestContextCycleArtifactChecks(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	ready := func(name string) error {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name)
		return err
	}
	// Another family's directory, an empty file, a symlink and a large file.
	f.handoff(t, "codex", "2026-10-13-120000.md", "synthetic")
	f.handoff(t, "claude", "2026-10-13-120001.md", "")
	f.handoff(t, "claude", "2026-10-13-120003.md", strings.Repeat("x", maxArtifactBytes+1))
	outside := filepath.Join(t.TempDir(), "outside.md")
	os.WriteFile(outside, []byte("synthetic"), 0o600)
	os.Symlink(outside, filepath.Join(f.repo, "_handoff", "claude", "2026-10-13-120002.md"))
	for _, name := range []string{"2026-10-13-120000.md", "2026-10-13-120001.md", "2026-10-13-120002.md", "2026-10-13-120003.md", "../x.md", "notes.md"} {
		if err := ready(name); !artifactRefused(err, "") && !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// A file older than the request.
	old := f.handoff(t, "claude", "2026-10-13-110000.md", "synthetic")
	past := time.UnixMilli(v.CreatedAt).Add(-time.Minute)
	os.Chtimes(filepath.Join(f.repo, "_handoff", "claude", old), past, past)
	if err := ready(old); !artifactRefused(err, "") {
		t.Fatalf("stale file: %v", err)
	}
	// A tracked file in the directory makes it unusable.
	name := f.handoff(t, "claude", "2026-10-13-120004.md", "synthetic")
	tracked := filepath.Join(f.repo, "_handoff", "claude", "tracked.txt")
	os.WriteFile(tracked, []byte("synthetic"), 0o600)
	if out, err := exec.Command("git", "-C", f.repo, "add", "-f", tracked).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if err := ready(name); !artifactRefused(err, "") {
		t.Fatalf("tracked directory: %v", err)
	}
	exec.Command("git", "-C", f.repo, "rm", "-q", "--cached", tracked).Run()
	if err := ready(name); err != nil {
		t.Fatalf("valid file: %v", err)
	}
}

// cycle reads the cycle's row.
func (f *cycleFixture) cycle(t *testing.T, id string) cycleRow {
	t.Helper()
	rows, err := cyclesWhere(context.Background(), f.s.db, "cycle_id=?", id)
	if err != nil || len(rows) != 1 {
		t.Fatalf("cycle %s: %v %d", id, err, len(rows))
	}
	return rows[0]
}

// artifactRefused reports a cycle_artifact_invalid refusal whose text holds want.
func artifactRefused(err error, want string) bool {
	var refusal Refusal
	return errors.As(err, &refusal) && refusal.Code == ErrCycleArtifact.Code && strings.Contains(refusal.Message, want)
}

// Each artifact refusal names the check that failed, and the directory one names the
// expected directory (#468).
func TestContextCycleArtifactRefusalsNameTheCheck(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	ready := func(name string) error {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name)
		return err
	}
	// The participant-address directory is not the session's own.
	wrong := filepath.Join(f.repo, "_handoff", "claude-synthetic")
	os.MkdirAll(wrong, 0o700)
	os.WriteFile(filepath.Join(wrong, "2026-10-13-120000.md"), []byte("synthetic"), 0o600)
	if err := ready("2026-10-13-120000.md"); !artifactRefused(err, "must be in _handoff/claude/ of the main checkout, and _handoff/claude/ is not a directory there") {
		t.Fatalf("other directory: %v", err)
	}
	os.MkdirAll(filepath.Join(f.repo, "_handoff", "claude"), 0o700)
	if err := ready("2026-10-13-120000.md"); !artifactRefused(err, "_handoff/claude/2026-10-13-120000.md is not a regular file") {
		t.Fatalf("missing file: %v", err)
	}
	// The ready call refuses a bad name as an invalid request first; the check itself names it.
	if _, err := f.s.verifyArtifact(context.Background(), f.cycle(t, v.ID), "notes.md"); !artifactRefused(err, `name "notes.md" is not a handoff file name`) {
		t.Fatalf("name: %v", err)
	}
	empty := f.handoff(t, "claude", "2026-10-13-120001.md", "")
	if err := ready(empty); !artifactRefused(err, "must hold 1 byte to 1 MiB") {
		t.Fatalf("size: %v", err)
	}
	old := f.handoff(t, "claude", "2026-10-13-110000.md", "synthetic")
	past := time.UnixMilli(v.CreatedAt).Add(-time.Minute)
	os.Chtimes(filepath.Join(f.repo, "_handoff", "claude", old), past, past)
	if err := ready(old); !artifactRefused(err, "was last modified before this cycle's request") {
		t.Fatalf("old: %v", err)
	}
	// A directory link inside the handoff path.
	os.RemoveAll(filepath.Join(f.repo, "_handoff", "claude"))
	os.Symlink(wrong, filepath.Join(f.repo, "_handoff", "claude"))
	if err := ready("2026-10-13-120000.md"); !artifactRefused(err, "must be in _handoff/claude/ of the main checkout, and _handoff/claude/ is not a directory there") {
		t.Fatalf("linked directory: %v", err)
	}
	os.Remove(filepath.Join(f.repo, "_handoff", "claude"))
	name := f.handoff(t, "claude", "2026-10-13-120002.md", "synthetic")
	tracked := filepath.Join(f.repo, "_handoff", "claude", "tracked.txt")
	os.WriteFile(tracked, []byte("synthetic"), 0o600)
	if out, err := exec.Command("git", "-C", f.repo, "add", "-f", tracked).CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	if err := ready(name); !artifactRefused(err, "directory _handoff/claude/ holds files that Git tracks") {
		t.Fatalf("tracked: %v", err)
	}
	exec.Command("git", "-C", f.repo, "rm", "-q", "--cached", tracked).Run()
	os.Remove(tracked)
	// A file Git does not ignore.
	os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("_handoff/claude/*.txt\n"), 0o644)
	if err := ready(name); !artifactRefused(err, "_handoff/claude/2026-10-13-120002.md is not ignored by Git") {
		t.Fatalf("not ignored: %v", err)
	}
	os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("_handoff/\n"), 0o644)
	if err := ready(name); err != nil {
		t.Fatalf("valid file: %v", err)
	}
}

// The Handoff path fact of .koinon/skills/handoff.md names the session's handoff directory;
// a facts file without it, or no file, keeps the default _handoff/<agent>/, and an invalid
// file or path is refused with the facts file named (#468).
func TestContextCycleArtifactHandoffPathFact(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	ready := func(name string) error {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name)
		return err
	}
	facts := filepath.Join(f.repo, ".koinon", "skills", "handoff.md")
	os.MkdirAll(filepath.Dir(facts), 0o700)
	write := func(path string) {
		t.Helper()
		text := "# Repository facts — handoff\n\n## Version\n1\n## Skill\nhandoff\n## Sources\n- AGENTS.md\n"
		if path != "" {
			text += "## Handoff path\n" + path + "\n"
		}
		if err := os.WriteFile(facts, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A valid facts file without the fact uses the default.
	write("")
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := f.s.verifyArtifact(context.Background(), f.cycle(t, v.ID), name); err != nil {
		t.Fatalf("default without the fact: %v", err)
	}
	// The fact moves the directory: the default one no longer counts.
	write(".snapshots/<agent>/ in the main checkout")
	if err := ready(name); !artifactRefused(err, ".snapshots/claude/ of the main checkout") {
		t.Fatalf("fact directory not named: %v", err)
	}
	dir := filepath.Join(f.repo, ".snapshots", "claude")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("_handoff/\n.snapshots/\n"), 0o644)
	os.WriteFile(filepath.Join(dir, name), []byte("synthetic"), 0o600)
	os.Chtimes(filepath.Join(dir, name), f.clock, f.clock)
	if _, err := f.s.verifyArtifact(context.Background(), f.cycle(t, v.ID), name); err != nil {
		t.Fatalf("fact directory: %v", err)
	}
	for _, bad := range []string{"../outside/<agent>/", "/tmp/<agent>/", "_handoff/shared/", "_handoff/./<agent>/"} {
		write(bad)
		if err := ready(name); !artifactRefused(err, "directory cannot be read from .koinon/skills/handoff.md") {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	os.WriteFile(facts, []byte("## Version\n2\n"), 0o644)
	if err := ready(name); !artifactRefused(err, "directory cannot be read from .koinon/skills/handoff.md") {
		t.Fatalf("invalid facts: %v", err)
	}
}

// Disable, a mode change and retirement fence the cycle in their own transaction; a
// cancelled cycle that had entered input becomes uncertain; every terminal outcome ends the
// Enable and spends the credit.
func TestContextCycleFences(t *testing.T) {
	for name, fence := range map[string]func(f *cycleFixture) error{
		"disable": func(f *cycleFixture) error {
			e, _ := f.s.contextEnables(context.Background())
			s := f.reload(t, f.k)
			_, err := f.s.SetContextEnable(roomCtx("context-enable"), s.Name, s.Revision, e[f.k].Revision, false)
			return err
		},
		"mode": func(f *cycleFixture) error {
			return f.s.SetContextPolicy(roomCtx("context-family-settings"), "claude", "observe", 75)
		},
		"retire": func(f *cycleFixture) error {
			s := f.reload(t, f.k)
			_, err := f.s.Mutate(context.Background(), Mutation{Family: "claude", ID: f.k.ID, IfRevision: s.Revision}, true)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCycleFixture(t)
			f.observe(t, f.k, 900, "synthetic-epoch-1")
			if err := fence(f); err != nil {
				t.Fatal(err)
			}
			var state, reason string
			f.s.db.QueryRow(`SELECT state,reason FROM context_cycles`).Scan(&state, &reason)
			if state != cycleCancelled || f.debt(t) != 0 {
				t.Fatalf("%s: %s %s debt %d", name, state, reason, f.debt(t))
			}
			if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled {
				t.Fatal("Enable still on")
			}
		})
	}
	// An entered input whose receipt was never recorded is uncertain, not cancelled.
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.advanceCycle(context.Background(), v.ID, 2, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	e, _ := f.s.contextEnables(context.Background())
	s := f.reload(t, f.k)
	if _, err := f.s.SetContextEnable(roomCtx("context-enable"), s.Name, s.Revision, e[f.k].Revision, false); err != nil {
		t.Fatal(err)
	}
	if v = f.status(t, f.k); v.State != cycleUncertain || v.Reason != "disabled" {
		t.Fatalf("intent fenced: %+v", v)
	}
}

// Deadlines end a cycle; an intent left without its receipt (a crash between the intent
// and the input) is never sent again: without proof it is uncertain.
func TestContextCycleDeadlinesAndIntentRecovery(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	f.clock = f.clock.Add(cycleReadyWait)
	f.step(t)
	if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "waiting_ready_timeout.unread" {
		t.Fatalf("ready timeout: %+v", v)
	}
	if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled {
		t.Fatal("a failed cycle kept the Enable")
	}
	// A recorded pickup intent after a restart: no pickup is entered again.
	g := newCycleFixture(t)
	g.observe(t, g.k, 900, "synthetic-epoch-1")
	v := g.status(t, g.k)
	name := g.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := g.s.ContextCycleReady(context.Background(), g.k, "synthetic-epoch-1", v.ID, 1, name); err != nil {
		t.Fatal(err)
	}
	g.step(t)
	g.step(t)
	v = g.status(t, g.k)
	if _, err := g.s.advanceCycle(context.Background(), v.ID, v.Revision, cycleResetVerified, cyclePickupIntent, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	g.step(t)
	if v = g.status(t, g.k); v.State != cycleUncertain || g.adapter.count("pickup") != 0 {
		t.Fatalf("intent recovery: %+v %v", v, g.adapter.calls)
	}
	// A clear without its receipt and without a proven new epoch is uncertain too.
	h := newCycleFixture(t)
	h.adapter.clear = false
	h.observe(t, h.k, 900, "synthetic-epoch-1")
	v = h.status(t, h.k)
	name = h.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	h.s.ContextCycleReady(context.Background(), h.k, "synthetic-epoch-1", v.ID, 1, name)
	h.step(t)
	if v = h.status(t, h.k); v.State != cycleUncertain || v.Reason != "clear_intent_unreceived" {
		t.Fatalf("unreceived clear: %+v", v)
	}
}

// A recognized prompt is answered after its recorded intent and the cycle returns to its
// stage; an unanswered response has an unknown effect, so the cycle is uncertain.
func TestContextCyclePrompt(t *testing.T) {
	f := newCycleFixture(t)
	f.adapter.pane = PaneState{Prompt: "synthetic_trust_prompt"}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleWaitingPrompt || v.PromptFrom != cycleReady {
		t.Fatalf("prompt: %+v", v)
	}
	f.adapter.pane = PaneState{Idle: true}
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleReady || f.adapter.count("answer synthetic_trust_prompt") != 1 {
		t.Fatalf("answered: %+v", v)
	}
	f.adapter.pane, f.adapter.answered = PaneState{Prompt: "synthetic_trust_prompt"}, false
	f.step(t)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleUncertain || v.Reason != "prompt_unanswered" {
		t.Fatalf("unanswered: %+v", v)
	}
}

// overwatch is reserved: no generated name or address can take it.
func TestOverwatchNameReserved(t *testing.T) {
	s, _ := testStore(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if taken, err := nameTaken(context.Background(), tx, overwatchName); err != nil || !taken {
		t.Fatalf("overwatch free: %v %v", taken, err)
	}
}

// A new native session in the same host and pane that holds the requester's participant is
// the managed successor: it finds the cycle by status, reports pickup, and the Enable moves
// to it with the requester's own threshold (#391). A session in another pane is not a successor.
func TestContextCycleSuccessorFlow(t *testing.T) {
	f := newCycleFixture(t)
	if f.session.Address == "" {
		t.Fatalf("no participant address: %+v", f.session)
	}
	if _, err := f.s.SetContextThreshold(roomCtx("context-threshold"), f.session.Name, 85); err != nil {
		t.Fatal(err)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, 1, name); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	f.step(t)
	f.step(t)
	if v = f.status(t, f.k); v.State != cycleWaitingPickup {
		t.Fatalf("waiting: %+v", v)
	}
	// The successor registers in the same host and pane and takes the participant.
	successor := Key{"claude", "synthetic-epoch-2"}
	join(t, f.s, "claude", successor.ID, f.repo)
	other := Key{"claude", "synthetic-other-pane"}
	join(t, f.s, "claude", other.ID, f.repo)
	fixtureExec(f.s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, successor.ID)
	fixtureExec(f.s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%9' WHERE family='claude' AND id=?`, other.ID)
	fixtureExec(f.s, `UPDATE names SET holder_id=? WHERE name=?`, successor.ID, f.session.Address)
	if got := f.status(t, other); got != nil {
		t.Fatalf("another pane found the cycle: %+v", got)
	}
	found := f.status(t, successor)
	if found == nil || found.ID != v.ID || found.Role != "successor" || found.State != cycleWaitingPickup {
		t.Fatalf("successor status: %+v", found)
	}
	if _, err := f.s.ContextCyclePickup(context.Background(), other, "synthetic-epoch-2", v.ID, v.Revision, name, "paused", "synthetic"); !errors.Is(err, ErrCycleNotFound) {
		t.Fatalf("other pane pickup: %v", err)
	}
	done, err := f.s.ContextCyclePickup(context.Background(), successor, "synthetic-epoch-2", v.ID, v.Revision, name, "paused", "synthetic restored work")
	if err != nil || done.State != cycleComplete {
		t.Fatalf("successor pickup: %v %+v", err, done)
	}
	// A lost reply: the identical retry returns the receipt.
	if again, err := f.s.ContextCyclePickup(context.Background(), successor, "synthetic-epoch-2", v.ID, v.Revision, name, "paused", "synthetic restored work"); err != nil || again.State != cycleComplete {
		t.Fatalf("retry: %v %+v", err, again)
	}
	enables, _ := f.s.contextEnables(context.Background())
	if !enables[successor].Enabled || enables[f.k].Enabled || enables[other].Enabled || enables[successor].Threshold != 85 {
		t.Fatalf("Enable carry: %+v", enables)
	}
	if got := f.status(t, successor); got == nil || got.ID != v.ID || got.State != cycleComplete {
		t.Fatalf("successor retained status: %+v", got)
	}
}

// With 16 admitted cycles at their maximum field sizes, a funded work claim and a pending
// work verdict beside them, and ordinary storage full over fragmented trees, every funded
// stage and every terminal control commits within the credit it releases: the most prompt
// rounds the event bound allows, intents and receipts, retained-caller and successor
// pickups, Disable, retirement and timeouts. No credit remains and the database is intact.
func TestContextCycleControlsAtSaturation(t *testing.T) {
	f := newCycleFixture(t)
	f.s.memory.limits.logical, f.s.memory.limits.workLogical, f.s.memory.limits.entries = 1<<40, 1<<40, 1<<20
	socket := "/" + strings.Repeat("s", 4095)
	keys := []Key{}
	for i := range maxActiveCycles {
		k := Key{"claude", "synthetic-saturation-" + strings.Repeat("i", 180) + string(rune('a'+i))}
		join(t, f.s, "claude", k.ID, f.repo)
		if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=?,host_start=77,tmux_socket=?,tmux_pane='%3',expires_at=? WHERE family='claude' AND id=?`,
			5000+i, socket, f.clock.Add(24*time.Hour).UnixMilli(), k.ID); err != nil {
			t.Fatal(err)
		}
		s := f.reload(t, k)
		if _, err := f.s.SetContextEnable(roomCtx("context-enable"), s.Name, s.Revision, 0, true); err != nil {
			t.Fatal(err)
		}
		f.observe(t, k, 900, "synthetic-epoch-"+strings.Repeat("e", 100)+string(rune('a'+i)))
		keys = append(keys, k)
	}
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE `+activeCycle) != maxActiveCycles || f.debt(t) != maxActiveCycles*cycleCreditPages {
		t.Fatalf("admitted %d, debt %d", count(t, f.s, `SELECT COUNT(*) FROM context_cycles`), f.debt(t))
	}
	// A 17th enabled session is not admitted while 16 cycles are unfinished.
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycles`) != maxActiveCycles {
		t.Fatal("admitted beyond the active bound")
	}
	cycles, _ := cyclesWhere(context.Background(), f.s.db, activeCycle+` ORDER BY session_id`)
	name := f.handoff(t, "claude", "2026-10-13-120000-"+strings.Repeat("l", 96)+".md", "synthetic handoff")
	// Two managed successors wait in the pinned host and pane of cycles 14 and 15.
	successors := map[int]Key{}
	for _, i := range []int{14, 15} {
		k := Key{"claude", "synthetic-successor-" + strings.Repeat("u", 180) + string(rune('a'+i))}
		join(t, f.s, "claude", k.ID, f.repo)
		if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=?,host_start=77,tmux_socket=?,tmux_pane='%3',expires_at=? WHERE family='claude' AND id=?`,
			cycles[i].HostPID, socket, f.clock.Add(24*time.Hour).UnixMilli(), k.ID); err != nil {
			t.Fatal(err)
		}
		successors[i] = k
	}
	// A funded work claim and a pending work verdict stand beside the cycles.
	big := strings.Repeat("x", 4096)
	m := participantCaller(t, f.s, f.k)
	claimed := mustWork(t, f.s, m, "work-create", map[string]any{"title": big[:256], "criteria": big, "non_goals": big[:2048], "key": key(), "deadline": now(f.s) + 600})["work_id"].(string)
	generation := mustStart(t, f.s, m, claimed, map[string]any{"checkpoint": big[:1024], "next_artifact": big[:1024], "progress_deadline": now(f.s) + 3600, "lease_seconds": 3600})
	reported := mustWork(t, f.s, m, "work-create", map[string]any{"title": big[:256], "criteria": big, "non_goals": big[:2048], "key": key(), "deadline": now(f.s) + 600})["work_id"].(string)
	writer := other(m, "synthetic-writer")
	out, err := report(f.s, writer, reported, mustStart(t, f.s, writer, reported, nil), map[string]any{"summary": big[:2048]})
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(out["seq"].(float64))
	join(t, f.s, "codex", "synthetic-reviewer", f.repo)
	reviewer := participantCaller(t, f.s, Key{"codex", "synthetic-reviewer"})
	// Fragment the trees a transition writes: rows in random key order, then every other
	// one deleted, so their pages are partly used and free pages are scattered.
	for i := range 3000 {
		if _, err := fixtureExec(f.s, `INSERT INTO audit(at,action,target,result,reason) VALUES (?,'synthetic',?,'synthetic','')`,
			rand.Int64N(1<<40), big[:200+i%100]); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.s.begin(context.Background(), control)
	if err != nil {
		t.Fatal(err)
	}
	for range 200 {
		c := cycles[0]
		c.ID, c.State, c.Outcome, c.Credit, c.FinishedAt, c.Events = newCycleID(), cycleFailed, cycleFailed, 0, f.clock.UnixMilli(), maxCycleEvents
		if err := insertCycle(context.Background(), tx, c); err != nil {
			t.Fatal(err)
		}
		for ordinal := range maxCycleEvents {
			if _, err := tx.Exec(`INSERT INTO context_cycle_events(cycle_id,ordinal,at,stage,result,reason) VALUES (?,?,0,'synthetic','synthetic',?)`,
				c.ID, ordinal+1, strings.Repeat("r", 64)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM audit WHERE action='synthetic' AND id%2=0`,
		`DELETE FROM context_cycle_events WHERE stage='synthetic' AND ordinal%2=0`,
		`DELETE FROM context_cycles WHERE state='failed' AND rowid%2=0`} {
		if _, err := fixtureExec(f.s, query); err != nil {
			t.Fatal(err)
		}
	}
	// Fill ordinary storage.
	f.s.storage.maxPages = count(t, f.s, `PRAGMA page_count`) + reservePages + commitSlack + f.debt(t) + 64
	sender := Key{"claude", keys[0].ID}
	for {
		_, err := f.s.Send(context.Background(), sender, f.reload(t, keys[1]).Name, strings.Repeat("m", 60000))
		if err != nil {
			if code(err) != "capacity" {
				t.Fatal(err)
			}
			break
		}
	}
	// funded runs one control and checks that it grew the database by no more than the
	// credit it released.
	largest := map[string]int64{}
	funded := func(label string, run func() error) {
		t.Helper()
		pages, debt := count(t, f.s, `PRAGMA page_count`), f.debt(t)
		if err := run(); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		growth, released := count(t, f.s, `PRAGMA page_count`)-pages, debt-f.debt(t)
		if growth > released {
			t.Fatalf("%s grew %d pages and released %d", label, growth, released)
		}
		largest[label] = max(largest[label], growth)
	}
	current := func(i int) cycleRow {
		t.Helper()
		c, err := cyclesWhere(context.Background(), f.s.db, `cycle_id=?`, cycles[i].ID)
		if err != nil || len(c) != 1 {
			t.Fatal(err)
		}
		return c[0]
	}
	stepOne := func(i int) error { return f.s.stepCycle(context.Background(), current(i)) }
	state := func(i int) string { return current(i).State }
	for i := 12; i < 16; i++ {
		funded("ready", func() error {
			_, err := f.s.ContextCycleReady(context.Background(), Key{"claude", cycles[i].SessionID}, cycles[i].Epoch, cycles[i].ID, 1, name)
			return err
		})
	}
	// Cycle 12 spends every funded stage on prompt rounds, then its clear intent; its
	// receipt would need a 16th event, so the cycle ends uncertain with the slot kept.
	for range (cycleFundedStages - 2) / 3 {
		f.adapter.pane = PaneState{Prompt: strings.Repeat("p", 64)}
		funded("prompt", func() error { return stepOne(12) })
		f.adapter.pane = PaneState{Idle: true}
		funded("answer", func() error { return stepOne(12) })
	}
	if c := current(12); c.State != cycleReady || c.Events != maxCycleEvents-2 {
		t.Fatalf("prompt rounds: %s with %d events", c.State, c.Events)
	}
	funded("event limit", func() error { return stepOne(12) })
	if c := current(12); c.State != cycleUncertain || c.Reason != "event_limit" || c.Events != maxCycleEvents {
		t.Fatalf("event limit: %s %s with %d events", c.State, c.Reason, c.Events)
	}
	// Cycles 13 to 15 clear, prove the reset and enter pickup at saturation.
	for i := 13; i < 16; i++ {
		for _, want := range []string{cycleClearReceived, cycleResetVerified, cycleWaitingPickup} {
			funded("stage", func() error { return stepOne(i) })
			if got := state(i); got != want {
				t.Fatalf("cycle %d: %s, want %s", i, got, want)
			}
		}
	}
	pickup := func(i int, caller Key) {
		funded("pickup", func() error {
			_, err := f.s.ContextCyclePickup(context.Background(), caller, "synthetic-epoch-2", cycles[i].ID, current(i).Revision, name,
				"resumed", strings.Repeat("r", maxPickupReport))
			return err
		})
	}
	pickup(13, Key{"claude", cycles[13].SessionID})
	// The work controls run before a successor takes the participant that m holds.
	funded("work finish", func() error {
		_, err := work(f.s, m, "work-finish", map[string]any{"work_id": claimed, "if_revision": revision(t, f.s, m, claimed),
			"claim_generation": generation, "outcome": "withdrawn", "reason": "synthetic", "key": key(), "deadline": now(f.s) + 600})
		return err
	})
	funded("work verdict", func() error {
		_, err := verdict(f.s, reviewer, "work-accept", reported, seq, nil)
		return err
	})
	for _, i := range []int{14, 15} {
		if _, err := fixtureExec(f.s, `UPDATE names SET holder_id=? WHERE name=?`, successors[i].ID, cycles[i].Address); err != nil || cycles[i].Address == "" {
			t.Fatalf("successor %d address %q: %v", i, cycles[i].Address, err)
		}
		pickup(i, successors[i])
		if e, _ := f.s.contextEnables(context.Background()); !e[successors[i]].Enabled {
			t.Fatalf("successor %d has no Enable", i)
		}
	}
	for i := range 4 {
		funded("disable", func() error {
			s := f.reload(t, Key{"claude", cycles[i].SessionID})
			e, _ := f.s.contextEnables(context.Background())
			_, err := f.s.SetContextEnable(roomCtx("context-enable"), s.Name, s.Revision, e[Key{"claude", cycles[i].SessionID}].Revision, false)
			return err
		})
	}
	for i := 4; i < 8; i++ {
		funded("retire", func() error {
			s := f.reload(t, Key{"claude", cycles[i].SessionID})
			_, err := f.s.Mutate(context.Background(), Mutation{Family: "claude", ID: cycles[i].SessionID, IfRevision: s.Revision}, true)
			return err
		})
	}
	for i := 8; i < 12; i++ {
		funded("timeout", func() error {
			_, err := f.s.endCycle(context.Background(), cycles[i].ID, 0, cycleFailed, "waiting_ready_timeout")
			return err
		})
	}
	for i := range cycles {
		if !cycleTerminal(state(i)) {
			t.Fatalf("cycle %d left %s", i, state(i))
		}
	}
	var version string
	f.s.db.QueryRow(`SELECT sqlite_version()`).Scan(&version)
	t.Logf("SQLite %s: largest growth: %v", version, largest)
	if credit := count(t, f.s, `SELECT COALESCE(SUM(credit),0) FROM context_cycles`); credit != 0 || verdictCredits(t, f.s) != 0 {
		t.Fatalf("cycle credit left %d, verdict credit %d", credit, verdictCredits(t, f.s))
	}
	var integrity string
	if err := f.s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
}

// The Sessions page shows the cycle's stage, deadline and history without a native ID,
// epoch, pane, socket or report; the own-cycle API refuses unknown fields.
func TestContextCycleDashboardAndRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	adapter := &fakeAdapter{pane: PaneState{Idle: true}, probe: CycleProbe{Entry: "koinon-pickup"}}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	repo := namedRepo(t, "cycle-dashboard")
	a := join(t, s, "claude", "synthetic-dashboard-a", repo)
	fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-private-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, a.ID)
	a, _ = scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, "claude", a.ID), s.now().UnixMilli())
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Observe(context.Background(), Observation{Caller: Key{"claude", a.ID}, Context: &ObservedValue{Source: "claude_statusline",
		At: s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(950), Epoch: "synthetic-private-epoch"}}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT cycle_id FROM context_cycles`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "cycle "+id+" (requester): waiting_ready") || !strings.Contains(page.body, "requested admitted") {
		t.Fatalf("sessions page: %d %s", page.status, page.body)
	}
	// The synthetic host has no Claude registry, so its subagents are unknown, never 0 (#467).
	if !strings.Contains(page.body, "stage until") || !strings.Contains(page.body, "background subagents running since the request: unknown") {
		t.Fatalf("sessions page lacks the deadline or the subagent count: %s", page.body)
	}
	// A refused ready report is listed with its code and text (#485).
	if _, err := s.ContextCycleReady(context.Background(), Key{"claude", a.ID}, "synthetic-private-epoch", id, 1, "2026-10-13-120000.md"); !artifactRefused(err, "") {
		t.Fatalf("ready: %v", err)
	}
	page = dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	if !strings.Contains(page.body, "ready refused at revision 1: cycle_artifact_invalid: the handoff artifact must be in _handoff/claude/") {
		t.Fatalf("sessions page lacks the refused ready report: %s", page.body)
	}
	for _, private := range []string{"synthetic-private-epoch", "synthetic-private-socket"} {
		if strings.Contains(page.body, private) {
			t.Fatalf("page shows %q", private)
		}
	}
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	caller := map[string]any{"family": "claude", "id": a.ID}
	if status, reply := post(t, address, secret, "/v1/cycles/status", map[string]any{"caller": caller}); status != 200 || reply["cycle"] == nil {
		t.Fatalf("status route: %d %v", status, reply)
	}
	if status, _ := post(t, address, secret, "/v1/cycles/status", map[string]any{"caller": caller, "session": "other"}); status != 400 {
		t.Fatalf("unknown field: %d", status)
	}
	if status, reply := post(t, address, secret, "/v1/cycles/ready", map[string]any{"caller": caller, "epoch": "synthetic-private-epoch",
		"cycle_id": id, "revision": 3, "artifact": "2026-10-13-120000.md"}); status != 409 || reply["code"] != "cycle_changed" {
		t.Fatalf("stale ready: %d %v", status, reply)
	}
	// A successful ready report tells the requester to stay paused (#431).
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("_handoff/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "_handoff", "claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "_handoff", "claude", "2026-10-13-120000.md"), []byte("synthetic handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, reply := post(t, address, secret, "/v1/cycles/ready", map[string]any{"caller": caller, "epoch": "synthetic-private-epoch",
		"cycle_id": id, "revision": 1, "artifact": "2026-10-13-120000.md"}); status != 200 || reply["next"] != cycleReadyNext {
		t.Fatalf("ready: %d %v", status, reply)
	}
	// The native inbox read carries the typed request's metadata.
	status, reply := post(t, address, secret, "/v1/inbox/read", map[string]any{"caller": caller, "after": 0, "limit": 10})
	data, _ := json.Marshal(reply)
	for _, field := range []string{`"system_kind":"` + cycleRequestKind + `"`, `"cycle_id":"` + id + `"`, `"cycle_enable_revision":1`} {
		if status != 200 || !strings.Contains(string(data), field) {
			t.Fatalf("inbox read lacks %s: %d %s", field, status, data)
		}
	}
}

// A schema 22 store gains the cycle tables and request columns without changing its state;
// a schema 22 writer refuses the upgraded store.
func TestContextCycleSchemaTwentyThreeKeepsExistingState(t *testing.T) {
	s, root := testStore(t)
	join(t, s, "codex", "synthetic-a", "")
	room := mustRoom(t, s, "synthetic kept room")
	mustHumanPost(t, s, room, "synthetic kept post")
	before := rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaThirty+undoSchemaTwentyNine+undoSchemaTwentyEight+undoSchemaTwentySeven+undoSchemaTwentySix+undoSchemaTwentyFive+undoSchemaTwentyFour+undoSchemaTwentyThree+`PRAGMA user_version=22`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 22); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || count(t, upgraded, `SELECT COUNT(*) FROM context_cycles`) != 0 ||
		count(t, upgraded, `SELECT COUNT(*) FROM messages WHERE system_kind!='' OR cycle_id!=''`) != 0 || rowsOf(t, upgraded.db) != before {
		t.Fatal("schema 23 changed existing state")
	}
	if err := checkCatalog(upgraded.db, 22); err == nil {
		t.Fatal("a schema 22 writer would accept the schema 23 store")
	}
}

// A schema 23 store keeps every cycle row unchanged when schema 24 rebuilds the table with the
// bootstrap states, which a schema 23 table refuses; a schema 23 writer refuses the result.
func TestContextCycleSchemaTwentyFourKeepsCycles(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	rowText := func(q querier) string {
		t.Helper()
		var text string
		if err := q.QueryRowContext(context.Background(), `SELECT group_concat(quote(cycle_id)||quote(credit)||quote(state)||quote(artifact)||quote(epoch)||quote(socket_sum), '|')
			FROM (SELECT *, length(tmux_socket) AS socket_sum FROM context_cycles)`).Scan(&text); err != nil {
			t.Fatal(err)
		}
		return text
	}
	before, rows := rowText(f.s.db), rowsOf(t, f.s.db)
	if _, err := fixtureExec(f.s, undoSchemaThirty+undoSchemaTwentyNine+undoSchemaTwentyEight+undoSchemaTwentySeven+undoSchemaTwentySix+undoSchemaTwentyFive+undoSchemaTwentyFour+`PRAGMA user_version=23`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(f.s.db, 23); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(f.s, `UPDATE context_cycles SET state='bootstrap_intent'`); err == nil {
		t.Fatal("a schema 23 table accepted a bootstrap state")
	}
	f.s.db.Close()
	upgraded, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowText(upgraded.db) != before || rowsOf(t, upgraded.db) != rows {
		t.Fatal("schema 24 changed a cycle row")
	}
	if err := checkCatalog(upgraded.db, 23); err == nil {
		t.Fatal("a schema 23 writer would accept the schema 24 store")
	}
	if _, err := fixtureExec(upgraded, `UPDATE context_cycles SET state='bootstrap_verified'`); err != nil {
		t.Fatalf("schema 24 refused a bootstrap state: %v", err)
	}
}

// Finished cycles past 30 days, and beyond the newest 1,024, are removed with their events;
// unfinished cycles and their credits stay.
func TestContextCycleRetention(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	now := f.clock.UnixMilli()
	for i := range maxTerminalCycles + 2 {
		c := cycleRow{ID: newCycleID(), Revision: 2, State: cycleComplete, Family: "claude", SessionID: "synthetic-old", Peer: "synthetic",
			Mode: "handoff_cycle", Outcome: cycleComplete, CreatedAt: now - int64(i), UpdatedAt: now, FinishedAt: now - int64(i), Events: 1}
		if i == 0 {
			c.FinishedAt = now - terminalCycleRetention.Milliseconds() - 1
		}
		tx, err := f.s.begin(context.Background(), control)
		if err != nil {
			t.Fatal(err)
		}
		insertCycle(context.Background(), tx, c)
		tx.ExecContext(context.Background(), `INSERT INTO context_cycle_events VALUES (?,1,?,'complete','complete','')`, c.ID, now)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.s.trimCycles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE finished_at>0`); n != maxTerminalCycles {
		t.Fatalf("finished kept: %d", n)
	}
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycles WHERE `+activeCycle) != 1 || f.debt(t) != cycleCreditPages {
		t.Fatal("trim touched the unfinished cycle")
	}
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycle_events e WHERE NOT EXISTS (SELECT 1 FROM context_cycles c WHERE c.cycle_id=e.cycle_id)`) != 0 {
		t.Fatal("orphaned events")
	}
}

// Admission is one transaction: after a restart the same cycle and exactly one request
// remain, and repeated samples of the same epoch admit nothing more.
func TestContextCycleSurvivesRestart(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = f.s.now
	f.s = reopened
	again := f.status(t, f.k)
	if again == nil || again.ID != v.ID || again.State != cycleWaitingReady || again.Revision != 1 {
		t.Fatalf("after restart: %+v", again)
	}
	f.observe(t, f.k, 950, "synthetic-epoch-1")
	if count(t, f.s, `SELECT COUNT(*) FROM context_cycles`) != 1 || count(t, f.s, `SELECT COUNT(*) FROM messages WHERE system_kind=?`, cycleRequestKind) != 1 {
		t.Fatal("restart admitted another cycle or request")
	}
}
