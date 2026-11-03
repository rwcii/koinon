package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A conversation turn after the ready report (#592). The records copy the shapes of a Claude
// Code transcript and a Codex rollout on this machine (2026-11-03); all text is synthetic.

func stamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000Z") }

func claudeLine(kind string, at time.Time, content any) string {
	record := map[string]any{"type": kind, "uuid": "synthetic"}
	if !at.IsZero() {
		record["timestamp"] = stamp(at)
	}
	if content != nil {
		record["message"] = map[string]any{"role": kind, "content": content}
	}
	line, _ := json.Marshal(record)
	return string(line)
}

func blocks(types ...string) []map[string]any {
	var out []map[string]any
	for _, kind := range types {
		out = append(out, map[string]any{"type": kind, "text": "synthetic", "tool_use_id": "synthetic"})
	}
	return out
}

// readyTranscript is a session's transcript up to its pause message: a prompt, the ready
// report's tool call and result, then the pause message and the turn's end, written after the
// report, and a record without a time.
func readyTranscript(ready time.Time) []string {
	return []string{
		claudeLine("user", ready.Add(-time.Minute), "synthetic request"),
		claudeLine("assistant", ready.Add(-time.Second), blocks("tool_use")),
		claudeLine("user", ready.Add(50*time.Millisecond), blocks("tool_result")),
		claudeLine("attachment", ready.Add(60*time.Millisecond), nil),
		claudeLine("assistant", ready.Add(5*time.Second), blocks("text")),
		claudeLine("system", ready.Add(5*time.Second), nil),
		`{"type":"cost-state","sessionId":"synthetic"}`,
	}
}

// claudeTranscript writes the session's transcript at projects/-synthetic-repo/SESSION.jsonl.
func claudeTranscript(t *testing.T, home, session string, records ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-synthetic-repo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, session+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Only a record after the ready report that starts a turn counts: a prompt, a delivered
// message, mixed blocks or unknown content; the session's own reply and the ready report's
// tool result do not. The report's own time is before it.
func TestClaudeTurnSince(t *testing.T) {
	home, session := t.TempDir(), "316b9458-6b5a-47c3-b863-af1745fb54a4"
	root := filepath.Join(home, ".claude")
	ready := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	after := ready.Add(10 * time.Second)
	for _, c := range []struct {
		name  string
		extra []string
		want  bool
	}{
		{"pause only", nil, false},
		{"prompt", []string{claudeLine("user", after, "synthetic maintainer message")}, true},
		{"delivered message", []string{claudeLine("user", after, blocks("text"))}, true},
		{"mixed blocks", []string{claudeLine("user", after, blocks("tool_result", "text"))}, true},
		{"tool results", []string{claudeLine("user", after, blocks("tool_result", "tool_result"))}, false},
		{"no content", []string{claudeLine("user", after, nil)}, true},
		{"empty content", []string{claudeLine("user", after, []any{})}, true},
		{"no time after the report", []string{claudeLine("user", time.Time{}, "synthetic")}, true},
		{"at the report", []string{claudeLine("user", ready, "synthetic")}, false},
		{"one millisecond after", []string{claudeLine("user", ready.Add(time.Millisecond), "synthetic")}, true},
		{"assistant", []string{claudeLine("assistant", after, blocks("text"))}, false},
	} {
		claudeTranscript(t, home, session, append(readyTranscript(ready), c.extra...)...)
		got, err := claudeTurnSince(root, session, ready.UnixMilli())
		if err != nil || got != c.want {
			t.Errorf("%s: %v %v", c.name, got, err)
		}
	}
	// "At the report" is the mark when it is the last timed record at or before the report: a
	// record of that time is before the report.
	path := claudeTranscript(t, home, session, append(readyTranscript(ready), "{not json")...)
	if _, err := claudeTurnSince(root, session, ready.UnixMilli()); err == nil {
		t.Error("a partial last record read as clean")
	}
	os.WriteFile(path, []byte(strings.Join(append(readyTranscript(ready), "{not json", claudeLine("assistant", after, nil)), "\n")+"\n"), 0o600)
	if _, err := claudeTurnSince(root, session, ready.UnixMilli()); err == nil {
		t.Error("an unreadable record after the report read as clean")
	}
	// A transcript that starts after the report is read whole.
	claudeTranscript(t, home, session, claudeLine("assistant", after, nil))
	if got, err := claudeTurnSince(root, session, ready.UnixMilli()); got || err != nil {
		t.Errorf("whole file: %v %v", got, err)
	}
	// No transcript, two transcripts and a pattern ID are errors.
	if _, err := claudeTurnSince(root, "00000000-0000-4000-8000-000000000000", ready.UnixMilli()); err == nil {
		t.Error("a missing transcript read as clean")
	}
	other := filepath.Join(root, "projects", "-other-repo")
	os.MkdirAll(other, 0o700)
	os.WriteFile(filepath.Join(other, session+".jsonl"), []byte(claudeLine("assistant", after, nil)+"\n"), 0o600)
	if _, err := claudeTurnSince(root, session, ready.UnixMilli()); err == nil {
		t.Error("two transcripts read as clean")
	}
	if _, err := claudeTurnSince(root, "*", ready.UnixMilli()); err == nil {
		t.Error("a pattern session ID was read")
	}
}

// The window doubles until it holds a record at or before the report; a window that cannot
// reach the report is an error.
func TestRecordTurnSinceWindow(t *testing.T) {
	ready := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	filler := claudeLine("assistant", ready.Add(time.Minute), strings.Repeat("x", 64<<10))
	write := func(fillers int, tail ...string) {
		lines := []string{claudeLine("user", ready.Add(-time.Minute), "synthetic")}
		for range fillers {
			lines = append(lines, filler)
		}
		os.WriteFile(path, []byte(strings.Join(append(lines, tail...), "\n")+"\n"), 0o600)
	}
	// About 3 MiB after the report: the window grows to 4 MiB.
	write(48)
	if got, err := recordTurnSince(path, ready.UnixMilli(), claudeTurnRecord); got || err != nil {
		t.Fatalf("grown window: %v %v", got, err)
	}
	write(48, claudeLine("user", ready.Add(2*time.Minute), "synthetic"))
	if got, err := recordTurnSince(path, ready.UnixMilli(), claudeTurnRecord); !got || err != nil {
		t.Fatalf("turn in a grown window: %v %v", got, err)
	}
	// More than 16 MiB after the report, and more before it: no mark in the largest window.
	lines := []string{claudeLine("user", ready.Add(-time.Minute), strings.Repeat("y", 1<<20))}
	for range 272 {
		lines = append(lines, filler)
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	if _, err := recordTurnSince(path, ready.UnixMilli(), claudeTurnRecord); !errors.Is(err, errRecordWindow) {
		t.Fatalf("window past the bound: %v", err)
	}
	if _, err := recordTurnSince(filepath.Join(t.TempDir(), "missing.jsonl"), ready.UnixMilli(), claudeTurnRecord); err == nil {
		t.Fatal("a missing file read as clean")
	}
	if _, err := recordTurnSince(t.TempDir(), ready.UnixMilli(), claudeTurnRecord); err == nil {
		t.Fatal("a directory read as clean")
	}
}

// Inspect reports the turn and the unread transcript; neither is idle.
func TestClaudeInspectTurnAfterReady(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	tg := term.target(t)
	ready := time.Now().Add(-time.Minute)
	tg.ReadyAt = ready.UnixMilli()
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state != (PaneState{Seen: transcriptUnread}) {
		t.Fatalf("no transcript: %+v %v", state, err)
	}
	claudeTranscript(t, term.home, term.epoch, readyTranscript(ready)...)
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("pause only: %+v %v", state, err)
	}
	claudeTranscript(t, term.home, term.epoch, append(readyTranscript(ready), claudeLine("user", ready.Add(6*time.Second), "synthetic"))...)
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || state != (PaneState{Seen: turnAfterReady}) {
		t.Fatalf("prompt after the report: %+v %v", state, err)
	}
	// After the clear there is no ready report to compare with.
	tg.ReadyAt = 0
	if state, err := (claudeCycle{}).Inspect(ctx, tg, term.fence); err != nil || !state.Idle {
		t.Fatalf("no ready report: %+v %v", state, err)
	}
}

// A turn that the transcript shows after Inspect, before the clear's Enter, erases /clear and
// refuses with errTurnAfterReady; a quiet transcript gets the clear.
func TestClaudeClearChecksTheTranscriptBeforeEnter(t *testing.T) {
	ctx := context.Background()
	term := newClaudeTerminal(t)
	term.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	term.write()
	tg := term.target(t)
	ready := time.Now().Add(-time.Minute)
	tg.ReadyAt = ready.UnixMilli()
	claudeTranscript(t, term.home, term.epoch, append(readyTranscript(ready), claudeLine("user", ready.Add(6*time.Second), "synthetic"))...)
	ok, err := (claudeCycle{}).Clear(ctx, tg, term.fence)
	if ok || !errors.Is(err, errTurnAfterReady) || slices.Contains(term.keys, "Enter") || term.cleared != 0 || term.input != "" {
		t.Fatalf("turn before Enter: %v %v keys %q input %q", ok, err, term.keys, term.input)
	}
	quiet := newClaudeTerminal(t)
	quiet.epoch = term.epoch
	quiet.write()
	qt := quiet.target(t)
	qt.ReadyAt = ready.UnixMilli()
	claudeTranscript(t, quiet.home, quiet.epoch, readyTranscript(ready)...)
	if ok, err := (claudeCycle{}).Clear(ctx, qt, quiet.fence); !ok || err != nil || quiet.cleared != 1 {
		t.Fatalf("quiet clear: %v %v", ok, err)
	}
	// A turn written after the check with the menu open, when Escape closes the menu, is found
	// by the last read before the Enter.
	late := newClaudeTerminal(t)
	late.epoch = term.epoch
	late.write()
	lt := late.target(t)
	lt.ReadyAt = ready.UnixMilli()
	claudeTranscript(t, late.home, late.epoch, readyTranscript(ready)...)
	late.onKey = func(key string) {
		if key == "Escape" {
			claudeTranscript(t, late.home, late.epoch, append(readyTranscript(ready), claudeLine("user", ready.Add(7*time.Second), "synthetic"))...)
		}
	}
	ok, err = (claudeCycle{}).Clear(ctx, lt, late.fence)
	if ok || !errors.Is(err, errTurnAfterReady) || slices.Contains(late.keys, "Enter") || late.cleared != 0 || late.input != "" ||
		!slices.Contains(late.keys, "Escape") {
		t.Fatalf("turn after the menu check: %v %v keys %q input %q", ok, err, late.keys, late.input)
	}
}

// codexRolloutLines writes the thread's rollout with timed records.
func codexRolloutLines(t *testing.T, term *codexTerminal, thread string, records ...string) {
	t.Helper()
	dir := filepath.Join(term.codex, "sessions", "2026", "10", "14")
	os.MkdirAll(dir, 0o700)
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"id": thread, "originator": "codex-tui", "cli_version": "0.162.0"}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-10-14T00-00-00-"+thread+".jsonl"),
		[]byte(strings.Join(append([]string{string(meta)}, records...), "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func codexEvent(kind, turn string, at time.Time) string {
	line, _ := json.Marshal(map[string]any{"timestamp": stamp(at), "type": "event_msg",
		"payload": map[string]any{"type": kind, "turn_id": turn}})
	return string(line)
}

// Codex: a turn that started after the ready report is not ready and refuses the clear before
// its first key; the turn of the ready report does not count.
func TestCodexTurnAfterReady(t *testing.T) {
	ctx := context.Background()
	term := newCodexTerminal(t)
	target := term.target(t)
	ready := time.Now().Add(-time.Minute)
	target.ReadyAt = ready.UnixMilli()
	reported := []string{codexEvent("task_started", "turn-1", ready.Add(-time.Minute)), codexEvent("task_complete", "turn-1", ready.Add(time.Second))}
	codexRolloutLines(t, term, "thread-a", reported...)
	if state, err := (codexCycle{}).Inspect(ctx, target, term.fence); err != nil || !state.Idle {
		t.Fatalf("report turn only: %+v %v", state, err)
	}
	codexRolloutLines(t, term, "thread-a", append(reported, codexEvent("task_started", "turn-2", ready.Add(5*time.Second)),
		codexEvent("task_complete", "turn-2", ready.Add(8*time.Second)))...)
	if state, err := (codexCycle{}).Inspect(ctx, target, term.fence); err != nil || state != (PaneState{Seen: turnAfterReady}) {
		t.Fatalf("turn after the report: %+v %v", state, err)
	}
	if ok, err := (codexCycle{}).Clear(ctx, target, term.fence); ok || !errors.Is(err, errTurnAfterReady) || len(term.sent()) != 0 {
		t.Fatalf("clear: %v %v %q", ok, err, term.sent())
	}
	// A rollout whose end cannot be read is not ready, and the clear stays a plain refusal.
	codexRolloutLines(t, term, "thread-a", append(reported, "{partial")...)
	if state, _ := (codexCycle{}).Inspect(ctx, target, term.fence); state.Idle {
		t.Fatalf("partial rollout: %+v", state)
	}
	if line := codexEvent("task_started", "x", ready); !bytes.Contains([]byte(line), []byte(`"task_started"`)) {
		t.Fatal(line)
	}
}

// The controller: a turn that Inspect sees asks for a new handoff without a clear; one that
// appears before the clear's Enter returns the cycle to ready, and the next step asks again.
func TestContextCycleAsksAgainAfterATurn(t *testing.T) {
	f := newCycleFixture(t)
	ready, _ := f.readyCycle(t)
	f.adapter.pane = PaneState{Seen: turnAfterReady}
	f.step(t)
	again := f.status(t, f.k)
	if again.State != cycleWaitingReady || again.Reason != "not_idle."+turnAfterReady || again.Revision != ready.Revision+1 || f.adapter.count("clear") != 0 {
		t.Fatalf("asked again: %+v clears %d", again, f.adapter.count("clear"))
	}
	// An unread transcript waits: no clear, no new request.
	g := newCycleFixture(t)
	g.readyCycle(t)
	g.adapter.pane = PaneState{Seen: transcriptUnread}
	g.step(t)
	if v := g.status(t, g.k); v.State != cycleReady || g.adapter.count("clear") != 0 || len(g.overwatchRequests(t)) != 1 {
		t.Fatalf("unread: %+v", v)
	}
	// The turn between Inspect and the clear's Enter.
	h := newCycleFixture(t)
	first, _ := h.readyCycle(t)
	h.adapter.clearErr = errTurnAfterReady
	h.step(t)
	back := h.status(t, h.k)
	if back.State != cycleReady || back.Reason != turnAfterReady || back.Revision != first.Revision+2 || h.adapter.count("clear") != 1 {
		t.Fatalf("refused clear: %+v clears %d", back, h.adapter.count("clear"))
	}
	h.adapter.clearErr = nil
	h.adapter.pane = PaneState{Seen: turnAfterReady}
	h.step(t)
	if v := h.status(t, h.k); v.State != cycleWaitingReady || v.Reason != "not_idle."+turnAfterReady || len(h.overwatchRequests(t)) != 2 {
		t.Fatalf("asked again after the refused clear: %+v", v)
	}
}

// A turn that lands at the Enter's fence, after every earlier check, refuses the clear with
// nothing entered: the check runs after the fence, right before the key (Codex and Claude).
func TestTurnAtTheClearsEnterFence(t *testing.T) {
	term := newCodexTerminal(t)
	target := term.target(t)
	ready := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	target.ReadyAt = ready.UnixMilli()
	reported := []string{codexEvent("task_started", "ready-turn", ready.Add(-time.Minute)), codexEvent("task_complete", "ready-turn", ready.Add(time.Second))}
	codexRolloutLines(t, term, "thread-a", reported...)
	term.atFence = func() {
		if term.fences == 1 { // /clear is typed; the next key is its Enter.
			codexRolloutLines(t, term, "thread-a", append(reported, codexEvent("task_started", "new-turn", ready.Add(5*time.Second)),
				codexEvent("task_complete", "new-turn", ready.Add(8*time.Second)))...)
		}
	}
	ok, err := (codexCycle{}).Clear(context.Background(), target, term.fence)
	if ok || !errors.Is(err, errTurnAfterReady) || slices.Contains(term.sent(), "Enter") || term.threads != 1 || term.input != "" {
		t.Fatalf("Codex: ok=%v err=%v keys=%q threads=%d input=%q", ok, err, term.sent(), term.threads, term.input)
	}

	claude := newClaudeTerminal(t)
	claude.epoch = "316b9458-6b5a-47c3-b863-af1745fb54a4"
	claude.write()
	ct := claude.target(t)
	ct.ReadyAt = ready.UnixMilli()
	claudeTranscript(t, claude.home, claude.epoch, readyTranscript(ready)...)
	calls := 0
	fence := func(ctx context.Context) error {
		calls++
		if calls == 3 { // /clear and Escape passed; this is the Enter's fence.
			claudeTranscript(t, claude.home, claude.epoch, append(readyTranscript(ready), claudeLine("user", ready.Add(6*time.Second), "synthetic"))...)
		}
		return claude.fence(ctx)
	}
	ok, err = (claudeCycle{}).Clear(context.Background(), ct, fence)
	if ok || !errors.Is(err, errTurnAfterReady) || slices.Contains(claude.keys, "Enter") || claude.cleared != 0 || claude.input != "" {
		t.Fatalf("Claude: ok=%v err=%v keys=%q clears=%d input=%q fences=%d", ok, err, claude.keys, claude.cleared, claude.input, calls)
	}
}

// A record appended during a read that found no turn is read again, and its turn counts; a
// record that changes on every read is an error.
func TestRecordTurnSinceChangedDuringRead(t *testing.T) {
	ready := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	os.WriteFile(path, []byte(claudeLine("assistant", ready.Add(-time.Second), nil)+"\n"), 0o600)
	appendLine := func(line string) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	appended := false
	got, err := recordTurnSince(path, ready.UnixMilli(), func(line []byte) (int64, bool, bool, bool) {
		if !appended {
			appended = true
			appendLine(claudeLine("user", ready.Add(time.Second), "synthetic"))
		}
		return claudeTurnRecord(line)
	})
	if !got || err != nil {
		t.Fatalf("appended turn: %v %v", got, err)
	}
	os.WriteFile(path, []byte(claudeLine("assistant", ready.Add(-time.Second), nil)+"\n"), 0o600)
	reads := 0
	_, err = recordTurnSince(path, ready.UnixMilli(), func(line []byte) (int64, bool, bool, bool) {
		reads++
		appendLine(claudeLine("assistant", ready.Add(time.Second), nil))
		return claudeTurnRecord(line)
	})
	if !errors.Is(err, errRecordChanged) || reads < recordReads {
		t.Fatalf("always changing: %v after %d records", err, reads)
	}
}

// A clear refused for input that has not started, or for a record that could not be read back
// to the ready report, returns the cycle to ready with nothing entered (#593). While Inspect
// still sees it, the cycle waits and asks for no new handoff, also at the stage's deadline,
// which ends the cycle as for any pane that never became ready.
func TestContextCycleWaitsForQueuedInput(t *testing.T) {
	for _, refusal := range []error{errInputQueued, errHistoryUnread} {
		f := newCycleFixture(t)
		first, _ := f.readyCycle(t)
		f.adapter.clearErr = refusal
		f.step(t)
		back := f.status(t, f.k)
		if back.State != cycleReady || back.Reason != refusal.Error() || back.Revision != first.Revision+2 || f.adapter.count("clear") != 1 {
			t.Fatalf("%v: refused clear: %+v", refusal, back)
		}
		f.adapter.clearErr = nil
		f.adapter.pane = PaneState{Seen: refusal.Error()}
		f.step(t)
		if v := f.status(t, f.k); v.State != cycleReady || len(f.overwatchRequests(t)) != 1 || f.adapter.count("clear") != 1 {
			t.Fatalf("%v: waits: %+v", refusal, v)
		}
		f.clock = f.clock.Add(cycleReadyWait + time.Second)
		f.step(t)
		if v := f.status(t, f.k); v.State != cycleFailed || v.Reason != "ready_timeout."+refusal.Error() || len(f.overwatchRequests(t)) != 1 {
			t.Fatalf("%v: deadline: %+v", refusal, v)
		}
	}
}
