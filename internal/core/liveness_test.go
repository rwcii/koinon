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
