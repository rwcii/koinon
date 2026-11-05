package core

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// Same-host succession uses synthetic hosts and a fake adapter; it never enters a pane.
func sameHostFixture(t *testing.T, family string) (*cycleFixture, *hosts) {
	t.Helper()
	s, root := testStore(t)
	f := &cycleFixture{s: s, root: root, clock: time.Now().Truncate(time.Millisecond),
		adapter: &fakeAdapter{pane: PaneState{Idle: true}, probe: CycleProbe{Entry: "koinon-pickup"}}}
	s.now = func() time.Time { return f.clock }
	h := &hosts{start: map[int]int64{100: 7, 200: 8}, failing: map[int]bool{}}
	s.processStart = h.read
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{family: f.adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	f.repo = namedRepo(t, "same-host")
	f.session = arrive(t, s, arrival{family: family, id: "synthetic-old", repo: f.repo, pid: 100, pane: "%1", toolCall: true})
	f.k = Key{family, f.session.ID}
	if _, err := fixtureExec(s, `UPDATE sessions SET expires_at=? WHERE family=? AND id=?`, f.clock.Add(2*time.Hour).UnixMilli(), family, f.k.ID); err != nil {
		t.Fatal(err)
	}
	if family == "codex" {
		if err := s.SetContextPolicy(roomCtx("context-settings"), family, ContextHandoffCycle, 75); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, true); err != nil {
		t.Fatal(err)
	}
	return f, h
}

func sameHostSuccessor(t *testing.T, f *cycleFixture) Key {
	t.Helper()
	f.clock = f.clock.Add(31 * time.Second)
	next := arrive(t, f.s, arrival{family: f.k.Family, id: "synthetic-new", repo: f.repo, pid: 100, pane: "%1", toolCall: true})
	wantSuccession(t, next, "succeeded", "same_host", f.session.Name)
	return Key{next.Family, next.ID}
}

func reopenSameHostStore(t *testing.T, f *cycleFixture, h *hosts) {
	t.Helper()
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.now = func() time.Time { return f.clock }
	s.processStart = h.read
	f.s = s
}

func TestSameHostCarriesEnableAndThreshold(t *testing.T) {
	ctx := context.Background()
	f, h := sameHostFixture(t, "claude")
	if _, err := f.s.SetContextThreshold(roomCtx("context-threshold"), f.session.Name, 45); err != nil {
		t.Fatal(err)
	}
	// Routine MCP registration renews the session revision after Enable was set. Its
	// unchanged host/pane/participant pins still describe an effective Enable.
	f.session = arrive(t, f.s, arrival{family: "claude", id: f.k.ID, repo: f.repo, pid: 100, pane: "%1", toolCall: true})
	next := sameHostSuccessor(t, f)
	former := f.reload(t, f.k)
	if former.State != "retired" || !former.Fenced {
		t.Fatalf("former holder: %+v", former)
	}
	for _, reopen := range []bool{false, true} {
		if reopen {
			reopenSameHostStore(t, f, h)
		}
		enables, err := f.s.contextEnables(ctx)
		if err != nil || !enables[next].Enabled || enables[next].Threshold != 45 || enables[f.k].Enabled {
			t.Fatalf("carried Enable (reopened %v): %+v %v", reopen, enables, err)
		}
		_, views, _, err := f.s.ContextShow(ctx, "")
		if err != nil || len(views) != 1 || !views[0].Enabled || views[0].Threshold != 45 || views[0].ThresholdSource != "session" {
			t.Fatalf("context view (reopened %v): %+v %v", reopen, views, err)
		}
		// The carried pins also make a repeated user Enable a no-op, not a new retry.
		got, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.reload(t, next).Name, true)
		if err != nil || got.Revision != enables[next].Revision {
			t.Fatalf("carried pins changed: %+v %v", got, err)
		}
	}
}

func TestSameHostEnableRequiresUnchangedPaneAndParticipant(t *testing.T) {
	for _, name := range []string{"changed pane", "missing pane", "changed socket", "missing socket", "other participant", "host cleared", "same pane", "disabled", "absent", "changed Enable pin"} {
		t.Run(name, func(t *testing.T) {
			family := "claude"
			if name == "host cleared" {
				family = "codex"
			}
			f, h := sameHostFixture(t, family)
			a := arrival{family: family, id: "synthetic-new", repo: f.repo, pid: 100, pane: "%1", toolCall: true}
			switch name {
			case "changed pane":
				a.pane = "%2"
			case "missing pane":
				a.pane = ""
			case "changed socket", "missing socket":
				socket := "/synthetic/other-socket"
				if name == "missing socket" {
					socket = ""
				}
				if _, err := fixtureExec(f.s, `UPDATE sessions SET tmux_socket=? WHERE family=? AND id=?`, socket, family, f.k.ID); err != nil {
					t.Fatal(err)
				}
			case "other participant":
				a.repo = namedRepo(t, "another-participant")
			case "host cleared":
				a.clear = true
			case "same pane":
				h.set(100, 0)
				a.pid = 200
			case "disabled":
				if _, err := f.s.SetContextEnableNow(roomCtx("context-enable"), f.session.Name, false); err != nil {
					t.Fatal(err)
				}
			case "absent":
				if _, err := fixtureExec(f.s, `DELETE FROM context_enables WHERE family=? AND session_id=?`, family, f.k.ID); err != nil {
					t.Fatal(err)
				}
			case "changed Enable pin":
				if _, err := fixtureExec(f.s, `UPDATE context_enables SET tmux_pane='%2' WHERE family=? AND session_id=?`, family, f.k.ID); err != nil {
					t.Fatal(err)
				}
			}
			f.clock = f.clock.Add(31 * time.Second)
			next := arrive(t, f.s, a)
			enables, err := f.s.contextEnables(context.Background())
			if err != nil || enables[Key{family, next.ID}].Enabled {
				t.Fatalf("unexpected carry: %+v %v", enables, err)
			}
		})
	}
}

func TestSameHostSuccessorNeedsItsOwnReading(t *testing.T) {
	for _, input := range []bool{false, true} {
		t.Run("input="+strconv.FormatBool(input), func(t *testing.T) {
			f, _ := sameHostFixture(t, "claude")
			f.observe(t, f.k, 900, "synthetic-epoch-old")
			old := f.status(t, f.k)
			outcome := cycleCancelled
			if input {
				// Record an unreconciled input intent without entering any real input.
				if _, err := f.s.advanceCycle(context.Background(), old.ID, old.Revision, cycleWaitingReady, cycleClearIntent, "", nil, nil); err != nil {
					t.Fatal(err)
				}
				outcome = cycleUncertain
			}
			next := sameHostSuccessor(t, f)
			f.step(t)
			row, err := scanCycle(f.s.db.QueryRow(`SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, old.ID))
			if err != nil || row.State != outcome || row.Reason != "enable_changed" {
				t.Fatalf("old cycle: %+v %v", row, err)
			}
			if got := f.status(t, next); got != nil {
				t.Fatalf("successor continued the old cycle: %+v", got)
			}
			if n := count(t, f.s, `SELECT COUNT(*) FROM context_cycles`); n != 1 {
				t.Fatalf("old reading admitted a successor cycle: %d", n)
			}
			f.observe(t, next, 900, "synthetic-epoch-new")
			got := f.status(t, next)
			if got == nil || got.ID == old.ID || got.State != cycleWaitingReady {
				t.Fatalf("no new cycle on successor reading: %+v", got)
			}
			if f.adapter.count("clear") != 0 || f.adapter.count("pickup") != 0 {
				t.Fatal("succession entered input")
			}
		})
	}
}

func TestSameHostCarriesReadyTimeoutRetryByEpoch(t *testing.T) {
	for _, tc := range []struct {
		epoch string
		read  bool
	}{{"synthetic-epoch-old", false}, {"synthetic-epoch-old", true}, {"synthetic-epoch-new", false}, {"synthetic-epoch-new", true}} {
		t.Run(tc.epoch+"/read="+strconv.FormatBool(tc.read), func(t *testing.T) {
			ctx := context.Background()
			f, h := sameHostFixture(t, "claude")
			f.observe(t, f.k, 900, "synthetic-epoch-old")
			f.clock = f.clock.Add(cycleReadyWait)
			f.step(t)
			if tc.read {
				if _, err := f.s.ReadInbox(ctx, f.k, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			// A pending successor may already have its own disabled Enable record. Its
			// revision advances on carry-over; the spent retry must follow that revision.
			pending := join(t, f.s, "claude", "synthetic-new", f.repo)
			if _, err := f.s.SetContextThreshold(roomCtx("context-threshold"), pending.Name, 50); err != nil {
				t.Fatal(err)
			}
			next := sameHostSuccessor(t, f)
			reopenSameHostStore(t, f, h)
			f.observe(t, next, 900, tc.epoch)
			if tc.epoch == "synthetic-epoch-old" && !tc.read {
				if got := f.status(t, next); got != nil {
					t.Fatalf("same epoch skipped its inbox read: %+v", got)
				}
				if _, err := f.s.ReadInbox(ctx, next, 0, 0); err != nil {
					t.Fatal(err)
				}
				f.observe(t, next, 900, tc.epoch)
			}
			if got := f.status(t, next); got == nil || got.State != cycleWaitingReady {
				t.Fatalf("no successor cycle: %+v", got)
			}
			f.clock = f.clock.Add(cycleReadyWait)
			f.step(t)
			enables, err := f.s.contextEnables(ctx)
			wantEnabled := tc.epoch == "synthetic-epoch-new"
			if err != nil || enables[next].Enabled != wantEnabled {
				t.Fatalf("retry for %s: %+v %v", tc.epoch, enables, err)
			}
		})
	}
}

func TestSameHostEnableTransferRollsBackWithSuccession(t *testing.T) {
	f, _ := sameHostFixture(t, "claude")
	if _, err := fixtureExec(f.s, `CREATE TRIGGER reject_holder BEFORE UPDATE OF holder_id ON names
		WHEN OLD.kind='alias' BEGIN SELECT RAISE(ABORT,'synthetic holder failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(31 * time.Second)
	launch, err := f.s.CreateLaunch(context.Background(), LaunchTarget{Family: "claude", Directory: f.repo, CLI: "/synthetic/cli", HostPID: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Register(context.Background(), Registration{Family: "claude", ID: "synthetic-new", Repository: f.repo,
		Directory: f.repo, TTLSeconds: 900, LaunchID: launch, Ancestors: []int{100}, ToolCall: true,
		Tmux: &TmuxPane{Socket: "/synthetic/tmux/default", Pane: "%1"}})
	if err == nil {
		t.Fatal("synthetic failure did not abort succession")
	}
	enables, err := f.s.contextEnables(context.Background())
	if err != nil || !enables[f.k].Enabled || enables[Key{"claude", "synthetic-new"}].Enabled {
		t.Fatalf("partial Enable transfer: %+v %v", enables, err)
	}
	if former := f.reload(t, f.k); former.State != "active" || !former.HoldsAddress || former.Fenced {
		t.Fatalf("partial succession: %+v", former)
	}
}
