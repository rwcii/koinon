package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// joinAs registers a launched session whose launch carries role.
func joinAs(t *testing.T, s *Store, family, id, directory, role string) Session {
	t.Helper()
	target := LaunchTarget{Family: family, Directory: directory, CLI: "/synthetic/cli", HostPID: syntheticHost, Role: role}
	launch, err := s.CreateLaunch(context.Background(), target)
	if err != nil {
		t.Fatalf("launch with role %q: %v", role, err)
	}
	r := Registration{Family: family, ID: id, Repository: directory, Directory: directory, TTLSeconds: 60, LaunchID: launch, Ancestors: []int{syntheticHost}}
	if family == "claude" {
		r.WakeTarget = json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, syntheticHost))
	}
	got, err := s.Register(context.Background(), r)
	if err != nil {
		t.Fatalf("register %s:%s: %v", family, id, err)
	}
	return got
}

func renew(t *testing.T, s *Store, k Session) Session {
	t.Helper()
	got, err := s.Mutate(context.Background(), Mutation{Family: k.Family, ID: k.ID, IfRevision: k.Revision, TTLSeconds: 60}, false)
	if err != nil {
		t.Fatalf("renew %s: %v", k.Name, err)
	}
	return got
}

func participantOf(t *testing.T, s *Store, address string) Participant {
	t.Helper()
	list, _, err := s.Participants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Address == address {
			return p
		}
	}
	t.Fatalf("no participant %s in %+v", address, list)
	return Participant{}
}

func TestValidRole(t *testing.T) {
	for role, want := range map[string]bool{
		"review": true, "r": true, "review-2": true, "x" + strings.Repeat("a", 23): true,
		"": false, "Review": false, "2review": false, "-review": false, "review_2": false,
		"x" + strings.Repeat("a", 24): false, "abc": false, "dead-beef": false, "a1": false,
	} {
		if ValidRole(role) != want {
			t.Errorf("ValidRole(%q) = %v", role, !want)
		}
	}
	s, _ := testStore(t)
	if _, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "codex", Directory: t.TempDir(), CLI: "/synthetic/cli",
		HostPID: 1, Role: "Bad"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid role recorded: %v", err)
	}
}

// Criterion 1: a participant is a family, a repository and an optional role. Linked
// worktrees share their repository's participants; a sub-agent has none.
func TestParticipantAddresses(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	plain := joinAs(t, s, "codex", "synthetic-plain", repo, "")
	review := joinAs(t, s, "codex", "synthetic-review", repo, "review")
	if plain.Address != "codex-koinon" || !plain.HoldsAddress || plain.Alias != "codex-koinon" || plain.Role != "" {
		t.Fatalf("participant without a role: %+v", plain)
	}
	if review.Address != "codex-koinon-review" || !review.HoldsAddress || review.Role != "review" {
		t.Fatalf("participant with a role: %+v", review)
	}
	// A linked worktree of the repository shares both participants.
	worktree := filepath.Join(t.TempDir(), "koinon-wt")
	for _, args := range [][]string{
		{"-c", "user.name=synthetic", "-c", "user.email=synthetic@example.com", "commit", "-q", "--allow-empty", "-m", "synthetic"},
		{"worktree", "add", "-q", worktree},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	worktree, _ = filepath.EvalSymlinks(worktree)
	second := joinAs(t, s, "codex", "synthetic-wt-review", worktree, "review")
	if second.Address != "codex-koinon-review" || second.HoldsAddress {
		t.Fatalf("worktree session of a held participant: %+v", second)
	}
	third := joinAs(t, s, "codex", "synthetic-wt-new", worktree, "build")
	if third.Address != "codex-koinon-build" || !third.HoldsAddress {
		t.Fatalf("role without a participant row: %+v", third)
	}
	// A sub-agent has its peer name only.
	sub := withLaunch(t, s, Registration{Family: "codex", ID: "synthetic-sub", Repository: repo, Directory: repo, TTLSeconds: 60, Subagent: true})
	got, err := s.Register(context.Background(), sub)
	if err != nil || got.Address != "" || got.HoldsAddress || got.Name == "" {
		t.Fatalf("sub-agent: %+v %v", got, err)
	}
	// A send to each address reaches its own participant's inbox, which its holder reads.
	sender := Key{"codex", "synthetic-plain"}
	for address, holder := range map[string]Session{"codex-koinon": plain, "codex-koinon-review": review, "codex-koinon-build": third} {
		if out, err := s.Send(context.Background(), sender, address, "synthetic"); err != nil || out.Recipient != address {
			t.Fatalf("send to %s: %+v %v", address, out, err)
		}
		inbox, err := s.ReadInboxes(context.Background(), Key{holder.Family, holder.ID}, 0, nil, 10)
		if err != nil || inbox.Participant == nil || inbox.Participant.Address != address || inbox.Participant.LastSeq != 1 {
			t.Fatalf("holder of %s: %+v %v", address, inbox, err)
		}
	}
}

// Criterion 2: one holder, never chosen by order. A holder is replaced only when it is no
// longer active and exactly one session qualifies; two qualifiers leave the participant
// without a holder and record the conflict, whichever renews first.
func TestParticipantHolderRules(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		s, _ := testStore(t)
		repo := namedRepo(t, "koinon")
		clock := time.Unix(1000, 0)
		s.now = func() time.Time { return clock }
		a := joinAs(t, s, "codex", "synthetic-a", repo, "")
		b := joinAs(t, s, "codex", "synthetic-b", repo, "")
		c := joinAs(t, s, "codex", "synthetic-c", repo, "")
		if !a.HoldsAddress || b.HoldsAddress || c.HoldsAddress {
			t.Fatalf("first holds: %+v %+v %+v", a, b, c)
		}
		// Leases are 60 s from t=1000. B and C renew at 1030; at 1061 A has expired, and
		// both renew again. Two qualify, so neither holds.
		clock = clock.Add(30 * time.Second)
		b, c = renew(t, s, b), renew(t, s, c)
		clock = clock.Add(31 * time.Second)
		if reversed {
			c, b = renew(t, s, c), renew(t, s, b)
		} else {
			b, c = renew(t, s, b), renew(t, s, c)
		}
		if b.HoldsAddress || c.HoldsAddress {
			t.Fatalf("a qualifier took the address by order (reversed %v): %+v %+v", reversed, b, c)
		}
		want := []string{b.Name, c.Name}
		if !reflect.DeepEqual(b.Conflict, want) || !reflect.DeepEqual(c.Conflict, want) {
			t.Fatalf("conflict: %v %v, want %v", b.Conflict, c.Conflict, want)
		}
		if _, err := s.Send(context.Background(), Key{"codex", "synthetic-b"}, "codex-koinon", "x"); !errors.Is(err, ErrAliasUnheld) {
			t.Fatalf("send without a holder: %v", err)
		}
		p := participantOf(t, s, "codex-koinon")
		if p.Holder != "" || !reflect.DeepEqual(p.Conflict, want) || p.LastEvent == nil || p.LastEvent.Reason != "conflict" || p.LastEvent.Former != a.Name {
			t.Fatalf("participant: %+v", p)
		}
		// Repeated renewals record the same conflict once.
		events := countEvents(t, s, "codex-koinon")
		b, c = renew(t, s, b), renew(t, s, c)
		if n := countEvents(t, s, "codex-koinon"); n != events {
			t.Fatalf("repeated conflict recorded again: %d -> %d", events, n)
		}
		// B renews at 1100; C expires at 1121; at 1122 B is the one qualifier.
		clock = clock.Add(39 * time.Second)
		b = renew(t, s, b)
		clock = clock.Add(22 * time.Second)
		if b = renew(t, s, b); !b.HoldsAddress || b.Conflict != nil {
			t.Fatalf("one qualifier: %+v", b)
		}
		p = participantOf(t, s, "codex-koinon")
		// The conflict cleared the expired holder, so the change has no former holder.
		if p.Holder != b.Name || p.Conflict != nil || p.LastEvent.Reason != "only_qualifier" || p.LastEvent.Holder != b.Name || p.LastEvent.Former != "" {
			t.Fatalf("participant after the one qualifier: %+v %+v", p, p.LastEvent)
		}
	}
}

func countEvents(t *testing.T, s *Store, address string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM participant_events WHERE address=?`, address).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A holder that the dashboard retires frees the participant for the next qualifier.
func TestRetiredHolderThenNewQualifier(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "claude", "synthetic-a", repo, "review")
	if _, err := s.Mutate(context.Background(), Mutation{Family: "claude", ID: "synthetic-a", IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	b := joinAs(t, s, "claude", "synthetic-b", repo, "review")
	if !b.HoldsAddress || b.Address != "claude-koinon-review" {
		t.Fatalf("new qualifier: %+v", b)
	}
	p := participantOf(t, s, "claude-koinon-review")
	if p.LastEvent.Former != a.Name || p.LastEvent.Holder != b.Name || p.LastEvent.Actor != "session" {
		t.Fatalf("event: %+v", p.LastEvent)
	}
}

// An address that a role would give is taken by another participant's address: the
// participant gets a longer free name. A long label with the longest role still fits.
func TestParticipantAddressCollisionAndLength(t *testing.T) {
	s, _ := testStore(t)
	other := namedRepo(t, "koinon-review")
	if got := joinAs(t, s, "codex", "synthetic-other", other, ""); got.Address != "codex-koinon-review" {
		t.Fatalf("other repository: %+v", got)
	}
	repo := namedRepo(t, "koinon")
	got := joinAs(t, s, "codex", "synthetic-role", repo, "review")
	if !strings.HasPrefix(got.Address, "codex-koinon-review-") || !got.HoldsAddress {
		t.Fatalf("collision: %+v", got)
	}
	long := namedRepo(t, strings.Repeat("l", 40))
	role := "r" + strings.Repeat("x", 23)
	got = joinAs(t, s, "agy", "synthetic-long", long, role)
	if got.Address != "agy-"+strings.Repeat("l", 32)+"-"+role || !got.HoldsAddress {
		t.Fatalf("long address: %+v", got)
	}
}

// The maintainer's choice sets the holder of a participant, is audited and recorded, and
// refuses a sub-agent, another participant's session, a changed revision and an inactive one.
func TestChooseHolder(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	review := joinAs(t, s, "codex", "synthetic-review", repo, "review")
	sub, err := s.Register(context.Background(), withLaunch(t, s, Registration{Family: "codex", ID: "synthetic-sub", Repository: repo, Directory: repo, TTLSeconds: 60, Subagent: true}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := withAudit(context.Background(), "participant-holder", "codex-koinon")
	for name, c := range map[string]struct {
		k        Session
		revision int64
		want     error
	}{
		"sub-agent":         {sub, sub.Revision, ErrNotParticipant},
		"other participant": {review, review.Revision, ErrNotParticipant},
		"changed revision":  {b, b.Revision + 1, ErrConflict},
	} {
		if err := s.ChooseHolder(ctx, "codex-koinon", Key{c.k.Family, c.k.ID}, c.revision); !errors.Is(err, c.want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatal(err)
	}
	p := participantOf(t, s, "codex-koinon")
	if p.Holder != b.Name || p.LastEvent.Reason != "maintainer_choice" || p.LastEvent.Actor != "maintainer" || p.LastEvent.Former != a.Name {
		t.Fatalf("choice: %+v %+v", p, p.LastEvent)
	}
	var audited int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action='participant-holder' AND target='codex-koinon' AND result='accepted'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("audit: %d %v", audited, err)
	}
	// The choice retired and fenced A (chunk 03). A registers again with its peer name only.
	if a = joinAs(t, s, "codex", "synthetic-a", repo, ""); a.HoldsAddress || !a.Fenced {
		t.Fatalf("former holder registered again: %+v", a)
	}
	// Only the maintainer's choice lifts the fence and makes it the holder again; B is
	// retired and fenced in turn.
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-a"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	if a = sessionState(t, s, Key{"codex", "synthetic-a"}); !a.HoldsAddress || a.Fenced {
		t.Fatalf("chosen again: %+v", a)
	}
	if b = sessionState(t, s, Key{"codex", "synthetic-b"}); b.State != "retired" || !b.Fenced {
		t.Fatalf("replaced holder: %+v", b)
	}
	// An expired session is not chosen.
	clock = clock.Add(61 * time.Second)
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-review"}, review.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired session chosen: %v", err)
	}
}

// At the ordinary storage ceiling, a send to an address is refused with capacity, and the
// maintainer's holder choice still commits: it is a control write.
func TestChooseHolderAtTheStorageCeiling(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	pages, _ := s.pages(ctx, s.db)
	s.storage.mu.Lock()
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance - 1
	s.storage.mu.Unlock()
	if _, err := s.Send(ctx, Key{"codex", "synthetic-b"}, "codex-koinon", "no room"); code(err) != "capacity" {
		t.Fatalf("send at the ceiling: %v", err)
	}
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatalf("choice at the ceiling: %v", err)
	}
	if p := participantOf(t, s, "codex-koinon"); p.Holder != b.Name {
		t.Fatalf("holder: %+v", p)
	}
}

// A holder whose registration expired holds nothing when it registers again: the holder
// rules apply to it like to any qualifier, whether or not a renewal of another session
// recorded the conflict first.
func TestExpiredHolderRegistersAgain(t *testing.T) {
	for _, observed := range []bool{true, false} {
		s, _ := testStore(t)
		repo := namedRepo(t, "koinon")
		clock := time.Unix(1000, 0)
		s.now = func() time.Time { return clock }
		a := joinAs(t, s, "codex", "synthetic-a", repo, "")
		b := joinAs(t, s, "codex", "synthetic-b", repo, "")
		c := joinAs(t, s, "codex", "synthetic-c", repo, "")
		clock = clock.Add(30 * time.Second)
		b, c = renew(t, s, b), renew(t, s, c)
		clock = clock.Add(31 * time.Second)
		if observed {
			b, c = renew(t, s, b), renew(t, s, c)
		}
		again := joinAs(t, s, "codex", "synthetic-a", repo, "")
		p := participantOf(t, s, "codex-koinon")
		// The conflict cleared and fenced A: before A's return when B and C renewed, else at it.
		want := []string{b.Name, c.Name}
		if !observed {
			want = []string{a.Name, b.Name, c.Name}
		}
		if again.HoldsAddress || !again.Fenced || p.Holder != "" || !reflect.DeepEqual(p.Conflict, want) {
			t.Fatalf("expired holder took the address back (observed %v): %+v %+v", observed, again, p)
		}
		if _, err := s.Send(context.Background(), Key{"codex", "synthetic-b"}, "codex-koinon", "x"); !errors.Is(err, ErrAliasUnheld) {
			t.Fatalf("send to a conflicted address: %v", err)
		}
		// With the others gone, the fenced former holder still takes nothing; a new session
		// is the one qualifier.
		clock = clock.Add(61 * time.Second)
		if again = joinAs(t, s, "codex", "synthetic-a", repo, ""); again.HoldsAddress || !again.Fenced {
			t.Fatalf("fenced holder after the conflict: %+v", again)
		}
		if d := joinAs(t, s, "codex", "synthetic-d", repo, ""); !d.HoldsAddress {
			t.Fatalf("new session after the conflict: %+v", d)
		}
	}
	// An expired holder that is still the only qualifier keeps its address on return.
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	clock = clock.Add(61 * time.Second)
	if again := joinAs(t, s, "codex", "synthetic-a", repo, ""); !again.HoldsAddress {
		t.Fatalf("lone expired holder: %+v", again)
	}
}
