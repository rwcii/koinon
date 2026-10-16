package core

import (
	"context"
	"errors"
	"strings"
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
	status, reply := call("/v1/context/show", map[string]any{})
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
	// A dialog answer takes no caller; a well-formed answer for no waiting dialog is refused.
	answer := map[string]any{"cycle_id": strings.Repeat("a", 32), "revision": 1, "digest": strings.Repeat("b", 64), "option": "1"}
	if status, _ := call("/v1/context/answer", map[string]any{"caller": caller, "cycle_id": answer["cycle_id"], "revision": 1,
		"digest": answer["digest"], "option": "1"}); status != 400 {
		t.Fatalf("answer with a caller: %d", status)
	}
	if status, _ := call("/v1/context/answer", answer); status == 200 || lastAudit().Result != "refused" ||
		lastAudit().Target != "context cycle "+strings.Repeat("a", 32)+" option 1 by cli" {
		t.Fatalf("answer without a dialog: %d %+v", status, lastAudit())
	}
}
