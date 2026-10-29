package mcp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// The caught-up mark (DECISIONS.md#IDLE-COMPACT-564): a Codex context reading carries End only
// when the read reached the end of the rollout after a complete record, within its batch,
// while the last turn had ended. Losing the mark is a change, so the daemon learns it at once.

type caughtUpRollout struct {
	*observeHarness
	path  string
	clock time.Time
}

func newCaughtUpRollout(t *testing.T) *caughtUpRollout {
	t.Helper()
	o := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	home := t.TempDir()
	o.env["CODEX_HOME"] = home
	dir := filepath.Join(home, "sessions", "2026", "10", "29")
	os.MkdirAll(dir, 0700)
	r := &caughtUpRollout{observeHarness: o, path: filepath.Join(dir, "rollout-synthetic-thread-1.jsonl"), clock: time.Now()}
	o.s.c.Now = func() time.Time { return r.clock }
	writeLines(t, r.path,
		map[string]any{"timestamp": "2026-10-29T05:00:00.000Z", "type": "session_meta", "payload": map[string]any{"id": "synthetic-thread-1"}},
		idleEvent("01", "task_started", "turn-a"),
		map[string]any{"timestamp": "2026-10-29T05:00:02.000Z", "type": "event_msg", "payload": map[string]any{"type": "token_count",
			"info": map[string]any{"model_context_window": 1000, "last_token_usage": map[string]any{"input_tokens": 800}}}},
		idleEvent("03", "task_complete", "turn-a"))
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	return r
}

func idleEvent(at, kind, turn string) map[string]any {
	return map[string]any{"timestamp": "2026-10-29T05:00:" + at + ".000Z", "type": "event_msg", "payload": map[string]any{"type": kind, "turn_id": turn}}
}

// context observes once and returns the context report, or nil when none was sent.
func (r *caughtUpRollout) context(t *testing.T) *core.ObservedValue {
	t.Helper()
	r.s.observeOnce(context.Background())
	var found *core.ObservedValue
	for _, report := range r.taken(0) {
		if report.Context != nil {
			found = report.Context
		}
	}
	return found
}

func (r *caughtUpRollout) size() int64 {
	info, _ := os.Stat(r.path)
	return info.Size()
}

var rolloutIdentity = regexp.MustCompile(`^[0-9]+-[0-9]+$`)

func TestCaughtUpMarkOnIdleRollout(t *testing.T) {
	r := newCaughtUpRollout(t)
	v := r.context(t)
	if v == nil || v.End == nil || !rolloutIdentity.MatchString(v.End.Rollout) || v.End.Size != r.size() || v.End.Turn != "turn-a" ||
		*v.UsedTokens != 800 || v.Epoch != "synthetic-thread-1" {
		t.Fatalf("idle caught-up reading: %+v %+v", v, v.End)
	}
	first := *v.End
	// An unchanged mark is confirmed once a minute, as every unchanged value is.
	r.clock = r.clock.Add(time.Second)
	if v := r.context(t); v != nil {
		t.Fatalf("resent within a minute: %+v", v)
	}
	r.clock = r.clock.Add(confirmEvery)
	if v := r.context(t); v == nil || v.End == nil || *v.End != first {
		t.Fatalf("confirmation: %+v", v)
	}
	// A partial last record ends the mark at once, though the reading did not change.
	writeLines(t, r.path, `{"timestamp":"2026-10-29T05:00:04.000Z","type":"response_item","payload":{"text":"x`)
	if v := r.context(t); v == nil || v.End != nil || *v.UsedTokens != 800 {
		t.Fatalf("partial last record: %+v", v)
	}
	// Its end catches up again, at the new size.
	writeLines(t, r.path, `"}}`+"\n")
	if v := r.context(t); v == nil || v.End == nil || v.End.Size != r.size() || v.End.Size == first.Size || v.End.Rollout != first.Rollout {
		t.Fatalf("after the record ends: %+v", v)
	}
	// A new turn is busy: no mark, though the read caught up.
	writeLines(t, r.path, idleEvent("05", "task_started", "turn-b"))
	if v := r.context(t); v == nil || v.End != nil {
		t.Fatalf("busy turn: %+v", v)
	}
	writeLines(t, r.path, idleEvent("06", "task_complete", "turn-b"))
	if v := r.context(t); v == nil || v.End == nil || v.End.Turn != "turn-b" {
		t.Fatalf("turn complete: %+v", v)
	}
	// An unfinished record longer than the line limit is being discarded: no mark.
	writeLines(t, r.path, `{"timestamp":"2026-10-29T05:00:07.000Z","type":"response_item","payload":{"text":"`+strings.Repeat("y", rolloutLine+10))
	if v := r.context(t); v == nil || v.End != nil || !r.s.obs.rollouts["synthetic-thread-1"].skipping {
		t.Fatalf("unfinished long record: %+v", v)
	}
	writeLines(t, r.path, `"}}`+"\n")
	if v := r.context(t); v == nil || v.End == nil || v.End.Size != r.size() {
		t.Fatalf("after the long record: %+v", v)
	}
	// One that fills the read buffer exactly ends the read at the end of the file, still inside
	// the record.
	writeLines(t, r.path, `{"text":"`+strings.Repeat("w", rolloutLine+1-len(`{"text":"`)))
	if v := r.context(t); v == nil || v.End != nil || !r.s.obs.rollouts["synthetic-thread-1"].skipping {
		t.Fatalf("long record at the buffer's end: %+v", v)
	}
}

// A read that stops at its batch, with newer records after it, has no mark; nor does a new
// reader (a koinon mcp restart) until it reads to the end.
func TestCaughtUpMarkNeedsTheWholeRollout(t *testing.T) {
	r := newCaughtUpRollout(t)
	if v := r.context(t); v == nil || v.End == nil {
		t.Fatalf("caught up: %+v", v)
	}
	filler := `{"timestamp":"2026-10-29T05:00:04.000Z","type":"response_item","payload":{"text":"` + strings.Repeat("z", 1000) + `"}}` + "\n"
	writeLines(t, r.path, strings.Repeat(filler, (rolloutBatch+rolloutBatch/2)/len(filler)))
	if v := r.context(t); v == nil || v.End != nil || r.s.obs.rollouts["synthetic-thread-1"].offset >= r.size() {
		t.Fatalf("first batch: %+v", v)
	}
	if v := r.context(t); v == nil || v.End == nil || v.End.Size != r.size() {
		t.Fatalf("second batch: %+v", v)
	}
	// A restarted koinon mcp reads the rollout again from its start.
	restarted := &caughtUpRollout{observeHarness: newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex"), path: r.path, clock: r.clock}
	restarted.env["CODEX_HOME"] = r.env["CODEX_HOME"]
	restarted.s.c.Now = func() time.Time { return restarted.clock }
	restarted.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	if v := restarted.context(t); v == nil || v.End != nil {
		t.Fatalf("restart, first batch: %+v", v)
	}
	if v := restarted.context(t); v == nil || v.End == nil {
		t.Fatalf("restart, caught up: %+v", v)
	}
}

// A read failure sends nothing, so the daemon's mark is not confirmed again; the reader then
// reads the rollout from its start.
func TestCaughtUpMarkAfterReadFailure(t *testing.T) {
	r := newCaughtUpRollout(t)
	if v := r.context(t); v == nil || v.End == nil {
		t.Fatalf("caught up: %+v", v)
	}
	if err := os.Chmod(r.path, 0); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(r.path); err == nil {
		f.Close()
		t.Skip("this user reads files without read permission")
	}
	r.clock = r.clock.Add(2 * confirmEvery)
	if v := r.context(t); v != nil {
		t.Fatalf("unreadable rollout reported: %+v", v)
	}
	if reader := r.s.obs.rollouts["synthetic-thread-1"]; reader.offset != 0 || reader.caughtUp {
		t.Fatalf("reader kept its place: %+v", reader)
	}
	os.Chmod(r.path, 0600)
	r.clock = r.clock.Add(rolloutSearch)
	if v := r.context(t); v == nil || v.End == nil || v.End.Size != r.size() {
		t.Fatalf("readable again: %+v", v)
	}
}
