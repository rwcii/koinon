package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/platform"
)

// countedReader is a process reader that answers every read with one start and error and
// counts the reads.
type countedReader struct {
	mu    sync.Mutex
	reads int
	start int64
	err   error
}

func (r *countedReader) read(int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return r.start, r.err
}

func (r *countedReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

// hostRow is the stored lease, state and revision of k, which a probe must not change.
func hostRow(t *testing.T, s *Store, k Key) string {
	t.Helper()
	session, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%s %d %d", session.State, session.ExpiresAt, session.Revision)
}

// #637: a session whose recorded host process is gone, or whose process ID now names another
// process, is unavailable as host_ended before the adapter's probe runs. A recorded start of
// zero, another read error or a matching start leave the decision to the adapter.
func TestCycleProbeHostEnded(t *testing.T) {
	ended := CycleProbe{Reason: "host_ended", Step: "the host process of this session has ended; enable the session that replaced it"}
	for _, c := range []struct {
		name   string
		start  int64 // the recorded host start
		reader *countedReader
		ended  bool
		reads  int
	}{
		{"gone", 77, &countedReader{err: platform.ErrProcessGone}, true, 1},
		{"wrapped gone", 77, &countedReader{err: fmt.Errorf("synthetic read: %w", platform.ErrProcessGone)}, true, 1},
		{"reused", 77, &countedReader{start: 78}, true, 1},
		{"other error", 77, &countedReader{start: 78, err: errors.New("synthetic inspection error")}, false, 1},
		{"live", 77, &countedReader{start: 77}, false, 1},
		{"zero start", 0, &countedReader{err: platform.ErrProcessGone}, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCycleFixture(t)
			if _, err := fixtureExec(f.s, `UPDATE sessions SET host_start=? WHERE family='claude' AND id=?`, c.start, f.k.ID); err != nil {
				t.Fatal(err)
			}
			before, probes := hostRow(t, f.s, f.k), f.adapter.count("probe")
			reader := c.reader
			f.s.processStart = reader.read
			got := f.s.sessionProbe(context.Background(), f.k)
			if c.ended && got != ended || !c.ended && got != f.adapter.probe {
				t.Fatalf("probe: %+v", got)
			}
			if adapterProbes := f.adapter.count("probe") - probes; c.ended && adapterProbes != 0 || !c.ended && adapterProbes != 1 {
				t.Fatalf("adapter probes: %d", adapterProbes)
			}
			if reader.count() != c.reads {
				t.Fatalf("host reads: %d", reader.count())
			}
			row := f.s.contextRowOf(f.reload(t, f.k), ContextEnable{Enabled: true}, f.clock.UnixMilli())
			if c.ended && (row.Available || row.Unavailable != "host_ended" || row.UnavailableStep != ended.Step) {
				t.Fatalf("sessions row: %+v", row)
			}
			if after := hostRow(t, f.s, f.k); after != before {
				t.Fatalf("the probe changed the session: %s, was %s", after, before)
			}
		})
	}
}

// A compaction's probe stops at the same host check, before the compaction adapter's probe.
func TestCompactionProbeHostEnded(t *testing.T) {
	for _, c := range []struct {
		name   string
		reader *countedReader
	}{
		{"gone", &countedReader{err: platform.ErrProcessGone}},
		{"reused", &countedReader{start: 89}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newCompactFixture(t)
			before, probes, compacts := hostRow(t, f.s, f.k), f.adapter.count("probe"), f.compactor.count("compact probe")
			reader := c.reader
			f.s.processStart = reader.read
			if got := f.s.sessionProbe(context.Background(), f.k); got.Reason != "host_ended" {
				t.Fatalf("probe: %+v", got)
			}
			if f.adapter.count("probe") != probes || f.compactor.count("compact probe") != compacts || reader.count() != 1 {
				t.Fatalf("calls: %v, %d reads", f.compactor.calls, reader.count())
			}
			if after := hostRow(t, f.s, f.k); after != before {
				t.Fatalf("the probe changed the session: %s, was %s", after, before)
			}
		})
	}
}

// hostlessFake is a cycle adapter with no host process, as DeepSeek's.
type hostlessFake struct{ *fakeAdapter }

func (hostlessFake) ReportCheck(context.Context, CycleTarget) (string, error) { return "", nil }

// A hostless adapter's session reads no host process, even with a recorded host.
func TestCycleProbeHostlessReadsNoHost(t *testing.T) {
	s, _ := testStore(t)
	adapter := hostlessFake{&fakeAdapter{probe: CycleProbe{Entry: "koinon-pickup"}}}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"deepseek": adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	k := Key{"deepseek", "synthetic-hostless"}
	join(t, s, k.Family, k.ID, namedRepo(t, "hostless-repo"))
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77 WHERE family=? AND id=?`, k.Family, k.ID); err != nil {
		t.Fatal(err)
	}
	reader := &countedReader{err: platform.ErrProcessGone}
	s.processStart = reader.read
	if got := s.sessionProbe(context.Background(), k); got != adapter.probe || adapter.count("probe") != 1 || reader.count() != 0 {
		t.Fatalf("probe: %+v, %d probes, %d reads", got, adapter.count("probe"), reader.count())
	}
}

// The Sessions page names the ended host and its step in the Enable button's title.
func TestCycleHostEndedSessionsPage(t *testing.T) {
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": &fakeAdapter{pane: PaneState{Idle: true}, probe: CycleProbe{Entry: "koinon-pickup"}}}
	t.Cleanup(func() { cycleAdapters = previous })
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "claude", "synthetic-ended", namedRepo(t, "cycle-host-ended"))
	// The test process's own ID with another start: a process ID that now names another
	// process, read by the daemon's own reader.
	pid, start := liveHost(t)
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=?,host_start=?,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, pid, start+1, a.ID); err != nil {
		t.Fatal(err)
	}
	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	want := `<button type="submit" disabled title="The handoff cycle is unavailable for this session: host_ended; the host process of this session has ended; enable the session that replaced it.">Enable handoff</button>`
	if page.status != 200 || !strings.Contains(page.body, want) {
		t.Fatalf("sessions page: %d %s", page.status, page.body)
	}
}
