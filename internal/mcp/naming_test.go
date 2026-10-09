package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// waitNaming waits for the naming attempt of caller to end and returns its result.
func waitNaming(t *testing.T, s *server, caller core.Key) namingResult {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		busy := s.naming[caller]
		s.mu.Unlock()
		s.obs.mu.Lock()
		got, found := s.obs.naming[caller]
		s.obs.mu.Unlock()
		if !busy && found {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no naming result")
	return namingResult{}
}

func launchedTarget(host int, background bool) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"host_pid": host, "launch_id": strings.Repeat("a", 64), "background": background})
	return data
}

// name_taken is tried again at the next renewal: the name is set once the other session
// with it is gone.
func TestTerminalNameTakenRetried(t *testing.T) {
	f := newNamingFixture(t)
	p := f.newSession("start")
	other := f.newSession("codex-koinon")
	s := f.server(f.env(p))
	caller := core.Key{Family: "codex", ID: "synthetic-codex"}
	session := core.Session{Family: "codex", Name: "codex-koinon-1a", Alias: "codex-koinon", WakeTarget: launchedTarget(p.host, false)}
	s.nameAfterRegistration(caller, session)
	if got := waitNaming(t, s, caller); got.result != "name_taken" || got.target != "codex-koinon" {
		t.Fatalf("taken: %+v", got)
	}
	f.run("kill-session", "-t", other.session)
	s.nameAfterRegistration(caller, session)
	if got := waitNaming(t, s, caller); got.result != "renamed" || f.sessionName(p) != "codex-koinon" {
		t.Fatalf("after release: %+v, named %q", got, f.sessionName(p))
	}
}

// A Claude background job has no TMUX: its terminal is the pane of its attach client on a
// server in the user's socket directory, found by the job's short ID.
func TestTerminalNamingBackgroundJob(t *testing.T) {
	uid := fmt.Sprintf("tmux-%d", os.Getuid())
	f := newNamingFixtureAt(t, filepath.Join(uid, "default"))
	env := map[string]string{"TMUX_TMPDIR": filepath.Dir(filepath.Dir(f.socket))}
	job := "abcd1234-1111-4222-8333-444455556666"
	ctx := context.Background()
	first := f.newSession("first")
	s := f.server(env)

	if got := s.nameAttached(ctx, job, "claude-koinon"); got != "attach_pane_not_found" {
		t.Fatalf("no client: %s", got)
	}
	// A client of another job is not this job's.
	f.mu.Lock()
	f.attach[first.host] = "deadbeef"
	f.mu.Unlock()
	if got := s.nameAttached(ctx, job, "claude-koinon"); got != "attach_pane_not_found" || f.sessionName(first) != "first" {
		t.Fatalf("wrong short ID: %s, named %q", got, f.sessionName(first))
	}
	f.mu.Lock()
	f.attach[first.host] = job[:8]
	f.mu.Unlock()
	if got := s.nameAttached(ctx, job, "claude-koinon"); got != "renamed" || f.sessionName(first) != "claude-koinon" {
		t.Fatalf("one client: %s, named %q", got, f.sessionName(first))
	}
	// Two clients of one job: nothing is renamed.
	second := f.newSession("second")
	f.mu.Lock()
	f.attach[second.host] = job[:8]
	f.mu.Unlock()
	if got := s.nameAttached(ctx, job, "claude-koinon-review"); got != "attach_pane_ambiguous" ||
		f.sessionName(first) != "claude-koinon" || f.sessionName(second) != "second" {
		t.Fatalf("two clients: %s, named %q %q", got, f.sessionName(first), f.sessionName(second))
	}

	// Through registration: a background launch record selects the attach search, and the
	// result is kept for the report.
	f.mu.Lock()
	delete(f.attach, second.host)
	f.mu.Unlock()
	s.claude = true
	caller := core.Key{Family: "claude", ID: job}
	s.nameAfterRegistration(caller, core.Session{Family: "claude", ID: job, Name: "claude-koinon-3c", WakeTarget: launchedTarget(4242, true)})
	if got := waitNaming(t, s, caller); got.result != "renamed" || got.target != "claude-koinon-3c" || f.sessionName(first) != "claude-koinon-3c" {
		t.Fatalf("registration: %+v, named %q", got, f.sessionName(first))
	}
}

// The naming result is reported on its own, without a terminal, so a session outside tmux
// reports why it was not named.
func TestNamingReportedWithoutTerminal(t *testing.T) {
	o := newObserveHarness(t, "codex-mcp-client", "/opt/agent/bin/codex")
	o.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-thread-1"})
	caller := core.Key{Family: "codex", ID: "synthetic-thread-1"}
	o.s.obs.mu.Lock()
	o.s.obs.naming = map[core.Key]namingResult{caller: {result: "not_in_tmux", target: "codex-koinon", at: time.Now().UnixMilli()}}
	o.s.obs.mu.Unlock()
	o.s.observeOnce(context.Background())
	for _, r := range o.taken(1) {
		if r.Caller != caller {
			continue
		}
		if r.Terminal != nil || r.Naming == nil || r.Naming.Source != "koinon_mcp" || r.Naming.Naming != "not_in_tmux" || r.Naming.Target != "codex-koinon" {
			t.Fatalf("report: %+v %+v", r, r.Naming)
		}
		return
	}
	t.Fatal("no naming report")
}
