package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// sendTo registers a fresh DeepSeek sender and sends one message to the named recipient.
func sendTo(t *testing.T, s *Store, repo, to string) error {
	t.Helper()
	sender := Key{"deepseek", "synthetic-sender"}
	if _, err := s.Register(context.Background(), Registration{Family: sender.Family, ID: sender.ID, Repository: repo, Directory: repo, TTLSeconds: 900}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Send(context.Background(), sender, to, "liveness check")
	return err
}

func wantInactive(t *testing.T, err error, reason string) {
	t.Helper()
	var inactive InactiveRecipient
	if !errors.As(err, &inactive) || inactive.Reason != reason || !errors.Is(err, ErrRecipientInactive) {
		t.Fatalf("send: got %v, want recipient_inactive %s", err, reason)
	}
}

// TestLivenessKeepsARunningIdleSession: no renewal arrives, as when the session is idle or
// its koinon mcp was replaced, but its host process runs, so the sweep keeps the session
// past several lifetimes without changing its revision. A session that no sweep kept and
// that expired is not revived.
func TestLivenessKeepsARunningIdleSession(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	h.set(100, 7)
	first := arrive(t, s, arrival{family: "codex", id: "synthetic-idle", repo: repo, pid: 100})
	for range 6 {
		advance(10 * time.Minute)
		if err := s.MaintainLiveness(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got := sessionOf(t, s, "codex", "synthetic-idle")
	if got.State != "active" || got.Revision != first.Revision || got.Liveness != "host" {
		t.Fatalf("after an hour without renewal: %+v", got)
	}
	listed, _, err := s.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].HostVerifiedAt != s.now().UnixMilli() {
		t.Fatalf("list %+v %v", listed, err)
	}
	if err := sendTo(t, s, repo, got.Name); err != nil {
		t.Fatalf("send to the running session: %v", err)
	}
	// peers carries the evidence: host with its check time, and none for DeepSeek.
	peers, _, err := s.Peers(context.Background(), Key{"codex", "synthetic-idle"})
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]Peer{}
	for _, p := range peers {
		evidence[p.Family] = p
	}
	if p := evidence["codex"]; p.Liveness != "host" || p.HostVerifiedAt != s.now().UnixMilli() {
		t.Fatalf("peer with a host: %+v", p)
	}
	if p := evidence["deepseek"]; p.Liveness != "none" || p.HostVerifiedAt != 0 {
		t.Fatalf("peer without evidence: %+v", p)
	}
	// The daemon was stopped past the deadline: the host runs, but nothing revives it.
	advance(16 * time.Minute)
	if err := s.MaintainLiveness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "synthetic-idle"); got.State != "expired" {
		t.Fatalf("an expired session was revived: %+v", got)
	}
	wantInactive(t, sendTo(t, s, repo, got.Name), "lapsed")
}

// TestLivenessLetsOtherSessionsExpire: a host that ended, a reused process ID, a host that
// cannot be read, a sub-agent and a session without a host record are not extended; each
// expires at its lifetime, and a send names why.
func TestLivenessLetsOtherSessionsExpire(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	for pid := 200; pid <= 204; pid++ {
		h.set(pid, 9)
	}
	ended := arrive(t, s, arrival{family: "codex", id: "synthetic-ended", repo: repo, pid: 200})
	reused := arrive(t, s, arrival{family: "codex", id: "synthetic-reused", repo: repo, pid: 201})
	unread := arrive(t, s, arrival{family: "codex", id: "synthetic-unread", repo: repo, pid: 202})
	arrive(t, s, arrival{family: "claude", id: "synthetic-parent", repo: repo, pid: 203})
	sub := arrive(t, s, arrival{family: "codex", id: "synthetic-sub", repo: repo, pid: 204, subagent: true})
	retired := arrive(t, s, arrival{family: "claude", id: "synthetic-retired", repo: testRepo(t), pid: 203})
	if _, err := s.Mutate(context.Background(), Mutation{Family: "claude", ID: retired.ID, IfRevision: retired.Revision}, true); err != nil {
		t.Fatal(err)
	}
	h.set(200, 0)
	h.set(201, 10)
	h.fail(202, true)
	none, err := s.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-none", Repository: repo, Directory: repo, TTLSeconds: 900})
	if err != nil {
		t.Fatal(err)
	}
	if none.Liveness != "none" || sessionOf(t, s, "codex", sub.ID).Liveness != "none" {
		t.Fatalf("liveness without evidence: %+v", none)
	}
	for range 2 {
		advance(8 * time.Minute)
		if err := s.MaintainLiveness(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// The host of the parent and the sub-agent still runs: only the parent is kept.
	if got := sessionOf(t, s, "claude", "synthetic-parent"); got.State != "active" {
		t.Fatalf("parent: %+v", got)
	}
	for _, c := range []struct {
		family, id, name, reason string
	}{
		{"codex", ended.ID, ended.Name, "ended"},
		{"codex", reused.ID, reused.Name, "ended"},
		{"codex", unread.ID, unread.Name, "no_liveness_evidence"},
		{"codex", sub.ID, sub.Name, "no_liveness_evidence"},
		{"deepseek", none.ID, none.Name, "no_liveness_evidence"},
		{"claude", retired.ID, retired.Name, "retired"},
	} {
		if got := sessionOf(t, s, c.family, c.id); got.State == "active" {
			t.Fatalf("%s was kept: %+v", c.id, got)
		}
		wantInactive(t, sendTo(t, s, repo, c.name), c.reason)
	}
}

// TestLivenessRechecksInsideTheWrite: the sweep reads hosts outside its write. A session
// whose deadline passes during that read is not revived.
func TestLivenessRechecksInsideTheWrite(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	h.set(300, 5)
	arrive(t, s, arrival{family: "codex", id: "synthetic-deadline", repo: repo, pid: 300})
	read := s.processStart
	advance(899 * time.Second)
	s.processStart = func(pid int) (int64, error) { advance(2 * time.Second); return read(pid) }
	if err := s.MaintainLiveness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "synthetic-deadline"); got.State != "expired" {
		t.Fatalf("revived at its deadline: %+v", got)
	}
}

// TestLivenessSkipsASessionThatChanged: a session that registered again as a sub-agent
// while the sweep read its host keeps that registration.
func TestLivenessSkipsASessionThatChanged(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	h.set(301, 6)
	arrive(t, s, arrival{family: "codex", id: "synthetic-turned", repo: repo, pid: 301})
	advance(10 * time.Minute)
	read := s.processStart
	once := false
	s.processStart = func(pid int) (int64, error) {
		if !once {
			once = true
			arrive(t, s, arrival{family: "codex", id: "synthetic-turned", repo: repo, pid: 301, subagent: true})
			advance(time.Second)
		}
		return read(pid)
	}
	if err := s.MaintainLiveness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "synthetic-turned"); !got.Subagent || got.ExpiresAt != s.now().UnixMilli()-1000+900_000 {
		t.Fatalf("the sub-agent registration was changed by the sweep: %+v", got)
	}
}

// TestLivenessKeepsOneSessionPerHost: a Claude host that changed its session ID has two
// active rows (#751). The sweep keeps only the one that registered or renewed last; the
// earlier one expires at its deadline, so the status line's host lookup after a clear finds
// one session. A host that returns to an earlier ID moves the evidence back to it.
func TestLivenessKeepsOneSessionPerHost(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := testRepo(t)
	h.set(300, 11)
	h.set(301, 12)
	old := arrive(t, s, arrival{family: "claude", id: "synthetic-old", repo: repo, pid: 300})
	alone := arrive(t, s, arrival{family: "claude", id: "synthetic-alone", repo: repo, pid: 301})
	advance(time.Minute)
	arrive(t, s, arrival{family: "claude", id: "synthetic-new", repo: repo, pid: 300})
	sweep := func() {
		t.Helper()
		if err := s.MaintainLiveness(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		advance(8 * time.Minute)
		sweep()
	}
	for _, c := range []struct {
		family, id, state string
	}{
		{"claude", old.ID, "expired"},
		{"claude", "synthetic-new", "active"},
		{"claude", alone.ID, "active"},
	} {
		if got := sessionOf(t, s, c.family, c.id); got.State != c.state {
			t.Fatalf("%s: state %s, want %s", c.id, got.State, c.state)
		}
	}
	keys, err := s.activeKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var onHost int
	for _, id := range []string{old.ID, "synthetic-new"} {
		if keys[Key{"claude", id}] {
			onHost++
		}
	}
	if onHost != 1 {
		t.Fatalf("%d active Claude sessions on one host", onHost)
	}
	// A status line after a clear reaches the remaining session through its host.
	h.set(999, 50)
	s.processParent = parentIn(map[int]int{999: 300})
	reading := Observation{Caller: Key{"claude", "synthetic-after-clear"},
		Context:        &ObservedValue{Source: "claude_statusline", At: s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(600), UsageAvailable: boolp(true)},
		ClaudeReporter: &ObservationReporter{PID: 999, Start: 50}}
	if err := s.Observe(context.Background(), reading); err != nil {
		t.Fatalf("post-clear reading: %v", err)
	}
	if e := s.contextEvidence(Key{"claude", "synthetic-new"}); !e.Found || *e.UsedTokens != 600 {
		t.Fatalf("post-clear reading not stored for the remaining session: %+v", e)
	}

	// The host returns to an earlier ID before it expired: its registration makes it the
	// later one, and the other row now gets no extension.
	h.set(302, 13)
	first := arrive(t, s, arrival{family: "claude", id: "synthetic-first", repo: testRepo(t), pid: 302})
	advance(time.Minute)
	second := arrive(t, s, arrival{family: "claude", id: "synthetic-second", repo: testRepo(t), pid: 302})
	advance(time.Minute)
	arrive(t, s, arrival{family: "claude", id: first.ID, repo: first.Directory, pid: 302})
	for range 3 {
		advance(8 * time.Minute)
		sweep()
	}
	if got := sessionOf(t, s, "claude", first.ID); got.State != "active" {
		t.Fatalf("returned session: %s", got.State)
	}
	if got := sessionOf(t, s, "claude", second.ID); got.State != "expired" {
		t.Fatalf("left session: %s", got.State)
	}
}

// TestLivenessRechecksTheHostInsideTheWrite: a later session that registers on the host
// while the sweep reads it takes the host's evidence before the write (#751).
func TestLivenessRechecksTheHostInsideTheWrite(t *testing.T) {
	s, h, advance := successionStore(t)
	h.set(310, 14)
	old := arrive(t, s, arrival{family: "claude", id: "synthetic-read-old", repo: testRepo(t), pid: 310})
	advance(10 * time.Minute)
	read, once := s.processStart, false
	s.processStart = func(pid int) (int64, error) {
		if pid == 310 && !once {
			once = true
			advance(time.Millisecond)
			arrive(t, s, arrival{family: "claude", id: "synthetic-read-new", repo: testRepo(t), pid: 310})
		}
		return read(pid)
	}
	if err := s.MaintainLiveness(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "claude", old.ID); got.ExpiresAt != old.ExpiresAt {
		t.Fatalf("the earlier session was extended from %d to %d", old.ExpiresAt, got.ExpiresAt)
	}
}

// TestLivenessKeepsBothSessionsOfOneTime: two sessions on one host with the same
// registration time are both extended, since neither is later; the next renewal of one
// orders them (#751).
func TestLivenessKeepsBothSessionsOfOneTime(t *testing.T) {
	s, h, advance := successionStore(t)
	h.set(311, 15)
	z := arrive(t, s, arrival{family: "claude", id: "synthetic-z", repo: testRepo(t), pid: 311})
	a := arrive(t, s, arrival{family: "claude", id: "synthetic-a", repo: testRepo(t), pid: 311})
	sweep := func() {
		t.Helper()
		for range 3 {
			advance(8 * time.Minute)
			if err := s.MaintainLiveness(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	sweep()
	for _, id := range []string{z.ID, a.ID} {
		if got := sessionOf(t, s, "claude", id); got.State != "active" {
			t.Fatalf("%s: %s", id, got.State)
		}
	}
	current := sessionOf(t, s, "claude", z.ID)
	if _, err := s.Mutate(context.Background(), Mutation{Family: "claude", ID: z.ID, IfRevision: current.Revision}, false); err != nil {
		t.Fatal(err)
	}
	sweep()
	if got := sessionOf(t, s, "claude", z.ID); got.State != "active" {
		t.Fatalf("renewed session: %s", got.State)
	}
	if got := sessionOf(t, s, "claude", a.ID); got.State != "expired" {
		t.Fatalf("earlier session: %s", got.State)
	}
}
