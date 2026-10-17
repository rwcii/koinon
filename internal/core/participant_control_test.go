package core

import (
	"strings"
	"testing"
)

// The command line's holder choice (#398) makes what the dashboard's Make holder makes: the
// holder, the former holder retired and fenced, the chosen session's fence lifted, and one
// audit record, here with the origin "cli". Refusals carry the dashboard's codes.
func TestParticipantHolderRoute(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	lastAudit := func() AuditRecord {
		t.Helper()
		records := auditAll(t, s)
		if len(records) == 0 {
			t.Fatal("no audit record")
		}
		return records[0]
	}
	if status, reply := post(t, address, secret, "/v1/participants/holder", map[string]any{"address": "codex-koinon", "peer": b.Name}); status != 200 ||
		reply["holder"] != b.Name || reply["former"] != a.Name {
		t.Fatalf("choice: %d %v", status, reply)
	}
	if r := lastAudit(); r.Action != "participant-holder" || r.Result != "accepted" || r.Target != "codex-koinon "+b.Name+" by cli" {
		t.Fatalf("audit: %+v", r)
	}
	p := participantOf(t, s, "codex-koinon")
	if p.Holder != b.Name || p.LastEvent.Reason != "maintainer_choice" || p.LastEvent.Actor != "maintainer" || p.LastEvent.Details != `{"origin":"cli"}` {
		t.Fatalf("participant: %+v %+v", p, p.LastEvent)
	}
	if a = joinAs(t, s, "codex", "synthetic-a", repo, ""); a.HoldsAddress || !a.Fenced {
		t.Fatalf("former holder: %+v", a)
	}
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{"address": "codex-koinon", "peer": "synthetic-nobody"}, 404, "peer_not_found"},
		{map[string]any{"address": "codex-koinon", "peer": a.Name, "if_revision": a.Revision + 1}, 409, "revision_changed"},
		{map[string]any{"address": "codex-elsewhere", "peer": a.Name}, 409, "not_participant"},
		{map[string]any{"address": "", "peer": a.Name}, 400, "invalid_request"},
		{map[string]any{"address": "codex-koinon", "peer": "maintainer"}, 404, "peer_not_found"},
	} {
		status, reply := post(t, address, secret, "/v1/participants/holder", c.body)
		if status != c.status || reply["code"] != c.code {
			t.Fatalf("%v: %d %v", c.body, status, reply)
		}
		if r := lastAudit(); r.Result != "refused" || r.Reason != c.code {
			t.Fatalf("%v: audit %+v", c.body, r)
		}
	}
	// The fenced former holder is chosen again: its fence is lifted, and B is retired.
	if status, reply := post(t, address, secret, "/v1/participants/holder", map[string]any{"address": "codex-koinon", "peer": a.Name, "if_revision": a.Revision}); status != 200 || reply["former"] != b.Name {
		t.Fatalf("choice back: %d %v", status, reply)
	}
	if a = sessionState(t, s, Key{"codex", "synthetic-a"}); !a.HoldsAddress || a.Fenced {
		t.Fatalf("chosen again: %+v", a)
	}
	status, reply := post(t, address, secret, "/v1/participants/holder", map[string]any{"address": "codex-koinon", "peer": b.Name})
	if status != 409 || reply["code"] != "session_not_active" {
		t.Fatalf("retired session: %d %v", status, reply)
	}
	data, err := Call(t.Context(), address, secret, "/v1/participants", nil)
	if err != nil || !strings.Contains(string(data), `"holder":"`+a.Name+`"`) {
		t.Fatalf("list: %s %v", data, err)
	}
}
