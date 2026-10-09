package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Participants sprint, chunk 01: only a launched session registers. The daemon admits a
// launcher family through its own launch only, on every path; DeepSeek registers without.

func TestDaemonAdmitsOnlyLaunchedSessions(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	directory, other := testRepo(t), testRepo(t)
	address := d.Addresses()[0]
	refused := func(label string, r Registration) {
		t.Helper()
		_, err := Call(context.Background(), address, secret, "/v1/sessions/register", r)
		var refusal RefusedError
		if !errors.As(err, &refusal) || refusal.Code != "not_launched" {
			t.Fatalf("%s: %v", label, err)
		}
	}
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		base := Registration{Family: family, ID: "synthetic-" + family, Directory: directory, Ancestors: []int{syntheticHost}}
		if family == "claude" {
			base.WakeTarget = json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, syntheticHost))
		}
		refused(family+" without a launch", base)
		unknown := base
		unknown.LaunchID = strings.Repeat("c", 64)
		refused(family+" with an unknown launch", unknown)
		elsewhere := withLaunch(t, d.store, Registration{Family: family, ID: base.ID, Directory: other})
		elsewhere.Directory = directory
		refused(family+" with another directory's launch", elsewhere)
		otherFamily := "codex"
		if family == "codex" {
			otherFamily = "agy"
		}
		foreign := withLaunch(t, d.store, Registration{Family: otherFamily, ID: base.ID, Directory: directory})
		foreign.Family, foreign.WakeTarget = family, base.WakeTarget
		refused(family+" with another family's launch", foreign)
	}
	if counts, err := d.store.Counts(context.Background()); err != nil || counts["total"] != 0 {
		t.Fatalf("a refused registration was stored: %+v %v", counts, err)
	}
	// DeepSeek keeps its command registration without a launch (#199), and never takes one.
	if _, err := Call(context.Background(), address, secret, "/v1/sessions/register", Registration{Family: "deepseek", ID: "synthetic-deepseek", Directory: directory}); err != nil {
		t.Fatalf("deepseek: %v", err)
	}
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-deepseek", Directory: directory, LaunchID: strings.Repeat("c", 64)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deepseek with a launch: %v", err)
	}
}

func TestLaunchBelongsToItsHost(t *testing.T) {
	s, _ := testStore(t)
	directory := testRepo(t)
	ctx := context.Background()
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		r := withLaunch(t, s, Registration{Family: family, ID: "synthetic-" + family, Directory: directory})
		// Another process tree: the launch's host is not among the caller's ancestors.
		stranger := r
		stranger.Ancestors = []int{77, 1}
		if _, err := s.Register(ctx, stranger); !errors.Is(err, ErrNotLaunched) {
			t.Fatalf("%s: a caller outside the host was admitted: %v", family, err)
		}
		if family == "claude" {
			// A nested Claude inherits the launch ID, but its own process is not the host.
			nested := r
			nested.Ancestors = []int{88, syntheticHost}
			nested.WakeTarget = json.RawMessage(`{"claude_pid":88}`)
			if _, err := s.Register(ctx, nested); !errors.Is(err, ErrNotLaunched) {
				t.Fatalf("nested claude admitted: %v", err)
			}
			for _, target := range []string{`{}`, `{"claude_pid":0}`, `{"claude_pid":4242,"cli":"/x"}`} {
				bad := r
				bad.WakeTarget = json.RawMessage(target)
				if _, err := s.Register(ctx, bad); !errors.Is(err, ErrInvalid) {
					t.Fatalf("claude target %s: %v", target, err)
				}
			}
		}
		// A process further down the host's tree, such as an MCP server, is admitted.
		r.Ancestors = []int{91, syntheticHost, 1}
		session, err := s.Register(ctx, r)
		if err != nil {
			t.Fatalf("%s: %v", family, err)
		}
		if !launched(session) {
			t.Fatalf("%s: session without its launch: %s", family, session.WakeTarget)
		}
		if family == "claude" && !strings.Contains(string(session.WakeTarget), fmt.Sprintf(`"claude_pid":%d`, syntheticHost)) {
			t.Fatalf("claude target lost claude_pid: %s", session.WakeTarget)
		}
	}
	if _, err := s.Register(ctx, Registration{Family: "codex", ID: "synthetic-deep", Directory: directory,
		LaunchID: strings.Repeat("c", 64), Ancestors: make([]int, maxAncestors+1)}); !errors.Is(err, ErrNotLaunched) {
		t.Fatalf("unbounded ancestry: %v", err)
	}
}

// A background job inherits the Claude service's environment, so a later job can carry an
// earlier job's launch ID (live-checks F5). The recorded job ID admits only its own job.
func TestBackgroundLaunchAdmitsOnlyItsJob(t *testing.T) {
	s, _ := testStore(t)
	directory := testRepo(t)
	ctx := context.Background()
	if _, err := s.CreateLaunch(ctx, LaunchTarget{Family: "codex", Directory: directory, CLI: "/synthetic/cli", HostPID: 1, Background: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("background codex launch: %v", err)
	}
	if _, err := s.CreateLaunch(ctx, LaunchTarget{Family: "claude", Directory: directory, CLI: "/synthetic/cli", HostPID: 1, Background: true, JobID: "0123abcd"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("job ID set at creation: %v", err)
	}
	l1, err := s.CreateLaunch(ctx, LaunchTarget{Family: "claude", Directory: directory, CLI: "/synthetic/cli", HostPID: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	job := func(id string, pid int) Registration {
		return Registration{Family: "claude", ID: id, Directory: directory, LaunchID: l1, Ancestors: []int{pid, 2},
			WakeTarget: json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, pid))}
	}
	a := job("0123abcd-1111-4222-8333-444455556666", 500)
	b := job("fedc9876-1111-4222-8333-444455556666", 600)
	// Before the launcher records the job, the job's first call waits and registers nothing.
	if _, err := s.Register(ctx, a); !errors.Is(err, ErrLaunchPending) {
		t.Fatalf("before the job ID: %v", err)
	}
	if counts, err := s.Counts(ctx); err != nil || counts["total"] != 0 {
		t.Fatalf("pending registration stored: %+v %v", counts, err)
	}
	for _, bad := range []string{"", "0123", "0123ABCD", "0123abcd;rm", strings.Repeat("a", 37), "--------", "0123abcd-1111", "0123abcd1111"} {
		if err := s.SetLaunchJob(ctx, l1, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("job ID %q: %v", bad, err)
		}
	}
	if err := s.SetLaunchJob(ctx, l1, "0123abcd"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLaunchJob(ctx, l1, "0123abcd"); err != nil {
		t.Fatalf("repeated record: %v", err)
	}
	if err := s.SetLaunchJob(ctx, l1, "99999999"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second job ID: %v", err)
	}
	// Job B started directly in the same directory inherits L1 and is refused; A registers.
	if _, err := s.Register(ctx, b); !errors.Is(err, ErrNotLaunched) {
		t.Fatalf("inherited launch admitted: %v", err)
	}
	session, err := s.Register(ctx, a)
	if err != nil || !launched(session) {
		t.Fatalf("job A: %+v %v", session, err)
	}
	if err := s.RetireLaunch(ctx, l1); !errors.Is(err, ErrConflict) {
		t.Fatalf("a started job's launch retired: %v", err)
	}
	// A launch whose claude --bg failed is retired and admits nothing.
	failed, err := s.CreateLaunch(ctx, LaunchTarget{Family: "claude", Directory: directory, CLI: "/synthetic/cli", HostPID: 1, Background: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetireLaunch(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLaunchJob(ctx, failed, "0123abcd"); !errors.Is(err, ErrMissing) {
		t.Fatalf("job of a retired launch: %v", err)
	}
	retired := a
	retired.LaunchID = failed
	if _, err := s.Register(ctx, retired); !errors.Is(err, ErrNotLaunched) {
		t.Fatalf("retired launch admitted: %v", err)
	}
	// Only a background launch takes a job or retires this way.
	foreground := withLaunch(t, s, Registration{Family: "claude", ID: "synthetic-foreground", Directory: directory})
	if err := s.SetLaunchJob(ctx, foreground.LaunchID, "0123abcd"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreground job: %v", err)
	}
	if err := s.RetireLaunch(ctx, foreground.LaunchID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("foreground retire: %v", err)
	}
}

// A session that an older store holds without a launch record is refused at each renewal,
// as an old koinon mcp would keep trying, and expires at the expiry it had, with its inbox,
// acknowledgements, cursor and claims kept.
func TestUnlaunchedSessionExpiresAfterUpgrade(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	repo := testRepo(t)
	sender := join(t, s, "codex", "synthetic-sender", repo)
	old := join(t, s, "claude", "synthetic-old", repo)
	if _, err := s.Send(ctx, Key{"codex", "synthetic-sender"}, old.Name, "synthetic kept"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(ctx, Key{"claude", "synthetic-old"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Key{"codex", "synthetic-sender"}, old.Name, "synthetic unread"); err != nil {
		t.Fatal(err)
	}
	// As the earlier runtime stored it: a Claude wake target without a launch.
	if _, err := s.db.Exec(`UPDATE sessions SET wake_target='{"claude_pid":4242}' WHERE family='claude' AND id='synthetic-old'`); err != nil {
		t.Fatal(err)
	}
	var expires, acked int64
	if err := s.db.QueryRow(`SELECT expires_at,acked_through FROM sessions WHERE family='claude' AND id='synthetic-old'`).Scan(&expires, &acked); err != nil {
		t.Fatal(err)
	}
	revision := old.Revision
	for range 3 {
		_, err := Call(ctx, d.Addresses()[0], secret, "/v1/sessions/renew", Mutation{Family: "claude", ID: "synthetic-old", IfRevision: revision})
		var refusal RefusedError
		if !errors.As(err, &refusal) || refusal.Code != "not_launched" {
			t.Fatalf("renewal of an unlaunched session: %v", err)
		}
	}
	var after, ackedAfter int64
	if err := s.db.QueryRow(`SELECT expires_at,acked_through FROM sessions WHERE family='claude' AND id='synthetic-old'`).Scan(&after, &ackedAfter); err != nil {
		t.Fatal(err)
	}
	if after != expires || ackedAfter != acked || ackedAfter != 1 {
		t.Fatalf("refused renewal changed the record: %d->%d acked %d->%d", expires, after, acked, ackedAfter)
	}
	var unread int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient_family='claude' AND recipient_id='synthetic-old'`).Scan(&unread); err != nil || unread != 2 {
		t.Fatalf("inbox not kept: %d %v", unread, err)
	}
	// A launched session still renews, and the record expires on time.
	if _, err := s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-sender", IfRevision: sender.Revision}, false); err != nil {
		t.Fatalf("launched renewal: %v", err)
	}
	clock := time.UnixMilli(expires + 1)
	s.now = func() time.Time { return clock }
	items, _, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == "synthetic-old" && item.State != "expired" {
			t.Fatalf("unlaunched session did not expire: %+v", item)
		}
	}
}

// A Codex sub-agent thread registers as its own session with a peer name, and never holds
// or takes an alias, even when no other session of its family holds it.
func TestSubagentNeverHoldsAnAlias(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	ctx := context.Background()
	sub := withLaunch(t, s, Registration{Family: "codex", ID: "synthetic-subagent", Repository: repo, Directory: repo, TTLSeconds: 60, Subagent: true})
	session, err := s.Register(ctx, sub)
	if err != nil || !session.Subagent || session.Alias != "" || session.Name == "" {
		t.Fatalf("sub-agent: %+v %v", session, err)
	}
	if session, err = s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-subagent", IfRevision: session.Revision}, false); err != nil || session.Alias != "" {
		t.Fatalf("sub-agent renewal: %+v %v", session, err)
	}
	user := join(t, s, "codex", "synthetic-user", repo)
	if user.Alias != "codex-koinon" {
		t.Fatalf("user thread: %+v", user)
	}
	peers, _, err := s.Peers(ctx, Key{"codex", "synthetic-user"})
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range peers {
		if peer.Name == session.Name && (!peer.Subagent || peer.Alias != "") {
			t.Fatalf("listed sub-agent: %+v", peer)
		}
		if peer.Name == user.Name && peer.Subagent {
			t.Fatalf("user thread listed as a sub-agent: %+v", peer)
		}
	}
	// A thread that took the alias before it was known to be a sub-agent gives it up, keeps
	// its peer name and stays a sub-agent when a later registration does not say so.
	late := withLaunch(t, s, Registration{Family: "codex", ID: "synthetic-user", Repository: repo, Directory: repo, TTLSeconds: 60})
	late.Subagent = true
	session, err = s.Register(ctx, late)
	if err != nil || !session.Subagent || session.Alias != "" || session.Name != user.Name {
		t.Fatalf("late sub-agent: %+v %v", session, err)
	}
	late.Subagent = false
	if session, err = s.Register(ctx, late); err != nil || !session.Subagent || session.Alias != "" {
		t.Fatalf("sub-agent registered again without metadata: %+v %v", session, err)
	}
	if session, err = s.Mutate(ctx, Mutation{Family: "codex", ID: "synthetic-user", IfRevision: session.Revision}, false); err != nil || session.Alias != "" {
		t.Fatalf("late sub-agent renewal: %+v %v", session, err)
	}
	other := join(t, s, "codex", "synthetic-user-2", repo)
	if other.Alias != "codex-koinon" {
		t.Fatalf("released alias not taken: %+v", other)
	}
}

// A full job ID admits only the session with that ID; a short one admits only a full session
// ID that begins with it.
func TestBackgroundJobIDForms(t *testing.T) {
	s, _ := testStore(t)
	directory := testRepo(t)
	ctx := context.Background()
	launch := func(job string) string {
		t.Helper()
		id, err := s.CreateLaunch(ctx, LaunchTarget{Family: "claude", Directory: directory, CLI: "/synthetic/cli", HostPID: 1, Background: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetLaunchJob(ctx, id, job); err != nil {
			t.Fatal(err)
		}
		return id
	}
	register := func(launch, id string) error {
		_, err := s.Register(ctx, Registration{Family: "claude", ID: id, Directory: directory, LaunchID: launch, Ancestors: []int{500},
			WakeTarget: json.RawMessage(`{"claude_pid":500}`)})
		return err
	}
	full := launch("0123abcd-1111-4222-8333-444455556666")
	if err := register(full, "0123abcd-1111-4222-8333-444455556666-different"); !errors.Is(err, ErrNotLaunched) {
		t.Fatalf("longer ID admitted by a full job ID: %v", err)
	}
	if err := register(full, "0123abcd-1111-4222-8333-444455556666"); err != nil {
		t.Fatalf("full job ID: %v", err)
	}
	short := launch("9876fedc")
	for _, id := range []string{"9876fedc", "9876fedcxyz", "9876fedc-not-a-session-id"} {
		if err := register(short, id); !errors.Is(err, ErrNotLaunched) {
			t.Fatalf("short job ID admitted %q: %v", id, err)
		}
	}
	if err := register(short, "9876fedc-1111-4222-8333-444455556666"); err != nil {
		t.Fatalf("short job ID: %v", err)
	}
}
