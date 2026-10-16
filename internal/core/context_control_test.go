package core

import (
	"context"
	"errors"
	"testing"
)

// A session's own threshold replaces its family's while it is set, keeps the Enable and its
// revision, and 0 returns the session to its family threshold (#391).
func TestContextSessionThreshold(t *testing.T) {
	s, _ := testStore(t)
	withCycleAvailable(t)
	a := join(t, s, "claude", "synthetic-a", "")
	k := Key{"claude", "synthetic-a"}
	enableOf := func() ContextEnable {
		t.Helper()
		enables, err := s.contextEnables(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return enables[k]
	}
	if _, err := s.SetContextThreshold(roomCtx("context-threshold"), a.Name, 101); !errors.Is(err, ErrInvalid) {
		t.Fatalf("threshold 101: %v", err)
	}
	if _, err := s.SetContextThreshold(roomCtx("context-threshold"), "no-such-peer", 40); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("unknown peer: %v", err)
	}
	// Without an Enable row, the threshold adds one that is off.
	e, err := s.SetContextThreshold(roomCtx("context-threshold"), a.Name, 40)
	if err != nil || e.Enabled || e.Revision != 1 || e.Threshold != 40 || enableOf() != e {
		t.Fatalf("first threshold: %+v %v %+v", e, err, enableOf())
	}
	if p := s.sessionContextPolicy("claude", enableOf()); p.Threshold != 40 || p.Mode != ContextHandoffCycle {
		t.Fatalf("policy: %+v", p)
	}
	on, err := s.SetContextEnableNow(roomCtx("context-enable"), a.Name, true)
	if err != nil || !on.Enabled || on.Revision != 2 || on.Threshold != 40 {
		t.Fatalf("enable keeps the threshold: %+v %v", on, err)
	}
	// A threshold change keeps the Enable revision, so an admitted cycle continues.
	if e, err := s.SetContextThreshold(roomCtx("context-threshold"), a.Name, 60); err != nil || e.Revision != 2 || !e.Enabled || enableOf().Threshold != 60 {
		t.Fatalf("change: %+v %v", e, err)
	}
	now := s.now().UnixMilli()
	if err := s.Observe(context.Background(), Observation{Caller: k, Context: &ObservedValue{Source: "claude_statusline", At: now,
		LimitTokens: int64p(1000), UsedTokens: int64p(500), Epoch: "synthetic-epoch"}}); err != nil {
		t.Fatal(err)
	}
	session := scanTestSession(t, s, k)
	if row := s.contextRowOf(session, enableOf(), now); row.Eligible || row.Reason != "below_threshold" || row.Threshold != 60 {
		t.Fatalf("at 60%%: %+v", row)
	}
	if _, err := s.SetContextThreshold(roomCtx("context-threshold"), a.Name, 40); err != nil {
		t.Fatal(err)
	}
	if row := s.contextRowOf(session, enableOf(), now); !row.Eligible || row.Threshold != 40 {
		t.Fatalf("at 40%%: %+v", row)
	}
	if e, err := s.SetContextThreshold(roomCtx("context-threshold"), a.Name, 0); err != nil || e.Threshold != 0 || s.sessionContextPolicy("claude", enableOf()).Threshold != contextDefaultThreshold {
		t.Fatalf("back to the family: %+v %v", e, err)
	}
	if row := s.contextRowOf(session, enableOf(), now); row.Reason != "below_threshold" || row.Threshold != contextDefaultThreshold {
		t.Fatalf("family threshold: %+v", row)
	}
}

func scanTestSession(t *testing.T, s *Store, k Key) Session {
	t.Helper()
	session, err := scanSession(s.db.QueryRowContext(context.Background(), sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// The /v1 context routes change what the dashboard changes, and the audit record names the
// origin: the command line, or the calling session (#391).
func TestContextRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	withCycleAvailable(t)
	s := d.store
	a := join(t, s, "claude", "synthetic-a", "")
	b := join(t, s, "codex", "synthetic-b", "")
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	call := func(path string, body map[string]any) (int, map[string]any) {
		t.Helper()
		return post(t, address, secret, path, body)
	}
	lastAudit := func() AuditRecord {
		t.Helper()
		records := auditAll(t, s)
		if len(records) == 0 {
			t.Fatal("no audit record")
		}
		return records[0]
	}
	if status, reply := call("/v1/context/set", map[string]any{"family": "claude", "mode": "handoff_cycle", "threshold": 80}); status != 200 ||
		policyOf(t, s, "claude").Threshold != 80 || lastAudit().Target != "context claude mode handoff_cycle threshold 80 by cli" {
		t.Fatalf("set: %d %v %+v", status, reply, lastAudit())
	}
	if status, _ := call("/v1/context/set", map[string]any{"family": "claude", "mode": "native_compaction", "threshold": 80}); status != 400 ||
		lastAudit().Result != "refused" || lastAudit().Target != "context claude by cli" {
		t.Fatalf("compaction for claude: %d %+v", status, lastAudit())
	}
	// An agent enables another session by name, and its own session without a peer.
	caller := map[string]any{"family": "codex", "id": "synthetic-b"}
	if status, reply := call("/v1/context/enable", map[string]any{"caller": caller, "peer": a.Name, "enabled": true}); status != 200 ||
		lastAudit().Target != "context enable "+a.Name+" on revision 1 by agent "+b.Name {
		t.Fatalf("agent enable: %d %v %+v", status, reply, lastAudit())
	}
	if status, _ := call("/v1/context/threshold", map[string]any{"caller": caller, "threshold": 30}); status != 200 ||
		lastAudit().Target != "context threshold "+b.Name+" 30 by agent "+b.Name {
		t.Fatalf("own threshold: %d %+v", status, lastAudit())
	}
	if status, _ := call("/v1/context/threshold", map[string]any{"peer": a.Name, "threshold": 50}); status != 200 ||
		lastAudit().Target != "context threshold "+a.Name+" 50 by cli" {
		t.Fatalf("cli threshold: %d %+v", status, lastAudit())
	}
	if status, _ := call("/v1/context/enable", map[string]any{"peer": a.Name}); status != 400 {
		t.Fatalf("enable without enabled: %d", status)
	}
	if status, reply := call("/v1/context/enable", map[string]any{"caller": map[string]any{"family": "codex", "id": "no-such"}, "enabled": true}); status != 403 {
		t.Fatalf("unknown caller: %d %v", status, reply)
	}
	status, reply := call("/v1/context/show", map[string]any{"after": ""})
	if status != 200 {
		t.Fatalf("show: %d %v", status, reply)
	}
	sessions := map[string]map[string]any{}
	for _, v := range reply["sessions"].([]any) {
		row := v.(map[string]any)
		sessions[row["peer"].(string)] = row
	}
	if row := sessions[a.Name]; row["enabled"] != true || row["threshold"] != float64(50) || row["threshold_source"] != "session" || row["enable_revision"] != float64(1) {
		t.Fatalf("show %s: %v", a.Name, row)
	}
	if row := sessions[b.Name]; row["enabled"] != false || row["threshold"] != float64(30) || row["mode"] != ContextNativeCompaction {
		t.Fatalf("show %s: %v", b.Name, row)
	}
	if status, _ := call("/v1/context/enable", map[string]any{"peer": a.Name, "enabled": false}); status != 200 ||
		lastAudit().Target != "context enable "+a.Name+" off revision 2 by cli" {
		t.Fatalf("cli disable: %d %+v", status, lastAudit())
	}
}

// context show selects active sessions before its page bound, so retained ended sessions
// never hide one, and pages by peer name (#391 review R1).
func TestContextShowSelectsActiveSessions(t *testing.T) {
	s, _ := testStore(t)
	a := join(t, s, "claude", "zz-active", "")
	now := s.now().UnixMilli()
	insert := func(prefix string, count int, expires int64) {
		t.Helper()
		if _, err := fixtureExec(s, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
			INSERT INTO sessions(family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,revision)
			SELECT 'claude',printf('%s-%04d',?,x),s.repository,s.directory,'{}',?,?,?,1
			FROM n CROSS JOIN sessions s WHERE s.family='claude' AND s.id='zz-active'`, count, prefix, now-2000, now-2000, expires); err != nil {
			t.Fatal(err)
		}
		if _, err := fixtureExec(s, `INSERT INTO names(name,kind,family,session_id)
			SELECT 'claude-synthetic-'||id,'peer',family,id FROM sessions WHERE id LIKE ?`, prefix+"-%"); err != nil {
			t.Fatal(err)
		}
	}
	insert("old", 1000, now-1)
	if _, truncated, err := s.List(context.Background()); err != nil || !truncated {
		t.Fatalf("fixture: %v %v", truncated, err)
	}
	_, views, next, err := s.ContextShow(context.Background(), "")
	if err != nil || len(views) != 1 || views[0].Peer != a.Name || next != "" {
		t.Fatalf("one active session behind 1,000 ended ones: %v %q %+v", err, next, views)
	}
	// 100 more active sessions fill a page; the continuation returns the rest.
	insert("live", contextShowPage, now+600_000)
	seen := map[string]bool{}
	after := ""
	for pages := 0; ; pages++ {
		_, views, next, err := s.ContextShow(context.Background(), after)
		if err != nil || pages > 2 {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, v := range views {
			if seen[v.Peer] {
				t.Fatalf("%s twice", v.Peer)
			}
			seen[v.Peer] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(seen) != contextShowPage+1 || !seen[a.Name] {
		t.Fatalf("pages returned %d sessions", len(seen))
	}
}

// A threshold change while the adapter probes is the threshold that admits the cycle and
// the one it records (#391 review R2).
func TestContextAdmissionRecordsTheThresholdInForce(t *testing.T) {
	f := newCycleFixture(t)
	if _, err := f.s.SetContextThreshold(roomCtx("context-threshold"), f.session.Name, 45); err != nil {
		t.Fatal(err)
	}
	previous := contextCycleProbe
	t.Cleanup(func() { contextCycleProbe = previous })
	changed := false
	contextCycleProbe = func(s *Store, ctx context.Context, k Key) CycleProbe {
		if !changed {
			changed = true
			if _, err := s.SetContextThreshold(roomCtx("context-threshold"), f.session.Name, 80); err != nil {
				t.Fatal(err)
			}
		}
		return previous(s, ctx, k)
	}
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	if v := f.status(t, f.k); !changed || v == nil || v.Threshold != 80 {
		t.Fatalf("cycle: changed %v %+v", changed, v)
	}
}
