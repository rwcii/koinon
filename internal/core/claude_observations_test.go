package core

import (
	"context"
	"errors"
	"testing"
)

func TestClaudeStatusAfterClearRoutesOnlyVerifiedHost(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "example")
	before := join(t, s, "claude", "synthetic-before-clear", repo)
	if _, err := s.db.Exec(`UPDATE sessions SET host_pid=4000,host_start=66 WHERE family='claude' AND id=?`, before.ID); err != nil {
		t.Fatal(err)
	}
	s.processStart = func(pid int) (int64, error) {
		switch pid {
		case 999:
			return 50, nil
		case 4000:
			return 66, nil
		}
		return 0, errors.New("unknown process")
	}
	s.processParents = func() (map[int]int, error) { return map[int]int{999: 4000}, nil }
	o := Observation{Caller: Key{"claude", "synthetic-after-clear"},
		Model:          &ObservedValue{Source: "claude_statusline", At: s.now().UnixMilli(), ID: "claude-synthetic"},
		Context:        &ObservedValue{Source: "claude_statusline", At: s.now().UnixMilli(), LimitTokens: int64p(200000), UsedTokens: int64p(500), UsageAvailable: boolp(true)},
		ClaudeReporter: &ObservationReporter{PID: 999, Start: 50}}
	if err := s.Observe(ctx, o); err != nil {
		t.Fatal(err)
	}
	status, err := s.PeerStatus(ctx, Key{before.Family, before.ID}, before.Name)
	if err != nil || !status.Model.Known || !status.Context.Known || *status.Context.UsedTokens != 500 || status.Name != before.Name || status.Alias != before.Alias {
		t.Fatalf("post-clear status: %+v: %v", status, err)
	}
	if _, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family='claude' AND s.id=?`, o.Caller.ID), s.now().UnixMilli()); !errors.Is(err, ErrMissing) {
		t.Fatal("observation created or moved a registration")
	}
	originalParents, originalStart := s.processParents, s.processStart
	for _, bad := range []string{"reporter reused", "host reused", "missing ancestry", "foreign host", "ambiguous", "nested agent"} {
		t.Run(bad, func(t *testing.T) {
			s.processParents, s.processStart = originalParents, originalStart
			s.processCommand = func(int) (string, []string, error) { return "/bin/sh", []string{"sh"}, nil }
			switch bad {
			case "reporter reused":
				s.processStart = func(pid int) (int64, error) {
					if pid == 999 {
						return 51, nil
					}
					return originalStart(pid)
				}
			case "host reused":
				s.processStart = func(pid int) (int64, error) {
					if pid == 4000 {
						return 67, nil
					}
					return originalStart(pid)
				}
			case "missing ancestry":
				s.processParents = func() (map[int]int, error) { return nil, errors.New("unreadable") }
			case "foreign host":
				s.processParents = func() (map[int]int, error) { return map[int]int{999: 4001}, nil }
			case "ambiguous":
				other := join(t, s, "claude", "synthetic-other", repo)
				s.db.Exec(`UPDATE sessions SET host_pid=4000,host_start=66 WHERE family='claude' AND id=?`, other.ID)
				defer s.db.Exec(`UPDATE sessions SET host_pid=0,host_start=0 WHERE family='claude' AND id=?`, other.ID)
			case "nested agent":
				s.processParents = func() (map[int]int, error) { return map[int]int{999: 3000, 3000: 4000}, nil }
				s.processCommand = func(int) (string, []string, error) { return "/opt/agent/bin/codex", []string{"codex"}, nil }
			}
			if err := s.Observe(ctx, o); err == nil {
				t.Fatal("unverified source accepted")
			}
		})
	}
}
