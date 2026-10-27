package core

import (
	"context"
	"errors"
	"testing"
)

// agyWitnessFixture is a cycle fixture whose cycle is moved to the agy family at state, with
// the reporter process 9001 under a status-line shell 9000 under the pinned host 4242.
func agyWitnessFixture(t *testing.T, state string) (*cycleFixture, string, map[int]int64) {
	t.Helper()
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	if v == nil {
		t.Fatal("no cycle")
	}
	if _, err := fixtureExec(f.s, `UPDATE context_cycles SET family='agy', state=? WHERE cycle_id=?`, state, v.ID); err != nil {
		t.Fatal(err)
	}
	starts := map[int]int64{9001: 501, 9000: 500, 4242: 77}
	f.s.processStart = func(pid int) (int64, error) {
		if start, ok := starts[pid]; ok {
			return start, nil
		}
		return 0, errors.New("no process")
	}
	f.s.processParents = func() (map[int]int, error) { return map[int]int{9001: 9000, 9000: 4242, 4242: 1}, nil }
	f.s.processCommand = func(pid int) (string, []string, error) {
		if pid == 4242 {
			return "/synthetic/agy", nil, nil
		}
		return "/bin/sh", []string{"sh", "-c", "koinon hook agy-status"}, nil
	}
	return f, v.ID, starts
}

func witnessIDs(s *Store, cycle string) []string {
	var ids []string
	for _, w := range s.agyWitness.of(cycle).List {
		ids = append(ids, w.ID)
	}
	return ids
}

// A report from a descendant of the pinned host is kept for that cycle, from the clear intent
// on; a report that cannot be tied to that host, or comes while no agy cycle waits, keeps
// nothing and changes no session (#512).
func TestAgyWitness(t *testing.T) {
	ctx := context.Background()
	report := func(s *Store, conversation string, pid int, start int64) error {
		return s.WitnessAgy(ctx, AgyWitness{Conversation: conversation, Reporter: ObservationReporter{PID: pid, Start: start}})
	}
	// Before the clear intent nothing is kept.
	f, id, _ := agyWitnessFixture(t, cycleReady)
	if err := report(f.s, "conv-b", 9001, 501); err != nil || len(witnessIDs(f.s, id)) != 0 {
		t.Fatalf("ready: %v %v", err, witnessIDs(f.s, id))
	}
	// From the clear intent: the old conversation's stale report and the new one are both kept,
	// each once, in order of first receipt.
	f, id, starts := agyWitnessFixture(t, cycleClearIntent)
	for _, conversation := range []string{"synthetic-epoch-1", "conv-b", "conv-b"} {
		if err := report(f.s, conversation, 9001, 501); err != nil {
			t.Fatal(err)
		}
	}
	if got := witnessIDs(f.s, id); len(got) != 2 || got[0] != "synthetic-epoch-1" || got[1] != "conv-b" {
		t.Fatalf("kept: %v", got)
	}
	var sessions int
	f.s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id='conv-b'`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("a witness registered a session")
	}
	// The reporter's start, the host's start, an invalid conversation and a nearer host of
	// another session are each refused.
	for name, refused := range map[string]func() error{
		"reporter start": func() error { return report(f.s, "conv-c", 9001, 999) },
		"no reporter":    func() error { return report(f.s, "conv-c", 7777, 1) },
		"host start": func() error {
			starts[4242] = 78
			defer func() { starts[4242] = 77 }()
			return report(f.s, "conv-c", 9001, 501)
		},
		"nearer host": func() error {
			other := join(t, f.s, "codex", "synthetic-nearer", f.repo)
			if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=9000,host_start=500 WHERE family='codex' AND id=?`, other.ID); err != nil {
				t.Fatal(err)
			}
			defer fixtureExec(f.s, `UPDATE sessions SET host_pid=0 WHERE family='codex' AND id=?`, other.ID)
			return report(f.s, "conv-c", 9001, 501)
		},
	} {
		if err := refused(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if err := report(f.s, "", 9001, 501); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty conversation: %v", err)
	}
	if got := witnessIDs(f.s, id); len(got) != 2 {
		t.Fatalf("refused reports kept: %v", got)
	}
	// The bound: more conversations than it keeps make the cycle ambiguous.
	for i := 0; i < agyWitnessMax; i++ {
		report(f.s, "conv-many-"+string(rune('a'+i)), 9001, 501)
	}
	if w := f.s.agyWitness.of(id); !w.Overflow || len(w.List) != agyWitnessMax {
		t.Fatalf("bound: %+v", w)
	}
	// A cycle that ends drops its witnesses at the next report.
	fixtureExec(f.s, `UPDATE context_cycles SET state='failed' WHERE cycle_id=?`, id)
	report(f.s, "conv-d", 9001, 501)
	if w := f.s.agyWitness.of(id); len(w.List) != 0 || w.Overflow {
		t.Fatalf("ended cycle: %+v", w)
	}
}

// A new conversation reported between the clear intent and the clear's receipt record proves
// the reset once the receipt is recorded; before it, and after a daemon restart, nothing does.
func TestAgyWitnessBeforeReceipt(t *testing.T) {
	ctx := context.Background()
	f, id, _ := agyWitnessFixture(t, cycleClearIntent)
	for _, conversation := range []string{"synthetic-epoch-1", "conv-b"} {
		if err := f.s.WitnessAgy(ctx, AgyWitness{Conversation: conversation, Reporter: ObservationReporter{PID: 9001, Start: 501}}); err != nil {
			t.Fatal(err)
		}
	}
	epoch := func(s *Store) string {
		target := s.cycleTarget(ctx, f.cycle(t, id))
		got, err := (agyCycle{}).NewEpoch(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := epoch(f.s); got != "" {
		t.Fatalf("before the receipt: %q", got)
	}
	fixtureExec(f.s, `UPDATE context_cycles SET state=? WHERE cycle_id=?`, cycleClearReceived, id)
	if got := epoch(f.s); got != "conv-b" {
		t.Fatalf("after the receipt: %q", got)
	}
	// A report after the receipt is recorded counts as well.
	g, gid, _ := agyWitnessFixture(t, cycleClearReceived)
	g.s.WitnessAgy(ctx, AgyWitness{Conversation: "conv-e", Reporter: ObservationReporter{PID: 9001, Start: 501}})
	if got, _ := (agyCycle{}).NewEpoch(ctx, g.s.cycleTarget(ctx, g.cycle(t, gid))); got != "conv-e" {
		t.Fatalf("report after the receipt: %q", got)
	}
	// The witnesses are memory-only: a restarted daemon has none, and the cycle is not reset.
	f.s.agyWitness = agyWitnessState{}
	if got := epoch(f.s); got != "" {
		t.Fatalf("after a restart: %q", got)
	}
}

// A report under an unregistered nested agent CLI is not the outer host's, and a conversation
// already known as another host's session is never a witness; the host's own conversations,
// registered there, still are (#521 review F1).
func TestAgyWitnessOwnership(t *testing.T) {
	ctx := context.Background()
	report := func(f *cycleFixture, conversation string) error {
		return f.s.WitnessAgy(ctx, AgyWitness{Conversation: conversation, Reporter: ObservationReporter{PID: 9001, Start: 501}})
	}
	epoch := func(f *cycleFixture, id string) string {
		got, _ := (agyCycle{}).NewEpoch(ctx, f.s.cycleTarget(ctx, f.cycle(t, id)))
		return got
	}
	// reporter -> shell -> unregistered agy -> pinned host
	f, id, starts := agyWitnessFixture(t, cycleClearReceived)
	starts[8000] = 400
	f.s.processParents = func() (map[int]int, error) { return map[int]int{9001: 9000, 9000: 8000, 8000: 4242, 4242: 1}, nil }
	f.s.processCommand = func(pid int) (string, []string, error) {
		if pid == 8000 || pid == 4242 {
			return "/synthetic/agy", nil, nil
		}
		return "/bin/sh", nil, nil
	}
	if err := report(f, "nested-conversation"); err == nil || len(witnessIDs(f.s, id)) != 0 || epoch(f, id) != "" {
		t.Fatalf("nested agy: %v %v", err, witnessIDs(f.s, id))
	}
	// An ancestor whose command cannot be read is no proof either.
	f, id, _ = agyWitnessFixture(t, cycleClearReceived)
	f.s.processCommand = func(pid int) (string, []string, error) {
		if pid == 4242 {
			return "/synthetic/agy", nil, nil
		}
		return "", nil, errors.New("synthetic: no command")
	}
	if err := report(f, "conv-b"); err == nil || len(witnessIDs(f.s, id)) != 0 {
		t.Fatalf("unreadable ancestor: %v %v", err, witnessIDs(f.s, id))
	}
	// A conversation registered under another host, live or retired.
	for _, retired := range []bool{false, true} {
		f, id, _ = agyWitnessFixture(t, cycleClearReceived)
		other := join(t, f.s, "agy", "conversation-of-another-host", f.repo)
		if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=7000,host_start=300 WHERE family='agy' AND id=?`, other.ID); err != nil {
			t.Fatal(err)
		}
		if retired {
			if _, err := fixtureExec(f.s, `UPDATE sessions SET retired_at=1 WHERE family='agy' AND id=?`, other.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := report(f, other.ID); err == nil || len(witnessIDs(f.s, id)) != 0 || epoch(f, id) != "" {
			t.Fatalf("another host's conversation (retired %v): %v %v", retired, err, witnessIDs(f.s, id))
		}
	}
	// The pinned host's own conversation, registered there after the clear, is kept.
	f, id, _ = agyWitnessFixture(t, cycleClearReceived)
	own := join(t, f.s, "agy", "conversation-of-the-host", f.repo)
	if _, err := fixtureExec(f.s, `UPDATE sessions SET host_pid=4242,host_start=77 WHERE family='agy' AND id=?`, own.ID); err != nil {
		t.Fatal(err)
	}
	if err := report(f, own.ID); err != nil || epoch(f, id) != own.ID {
		t.Fatalf("the host's own conversation: %v %v", err, witnessIDs(f.s, id))
	}
}
