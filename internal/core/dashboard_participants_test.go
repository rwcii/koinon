package core

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestDashboardParticipantChoice(t *testing.T) {
	d, root := startTestDaemon(t)
	repo := namedRepo(t, "koinon")
	a := joinAs(t, d.store, "codex", "synthetic-a", repo, "")
	b := joinAs(t, d.store, "codex", "synthetic-b", repo, "")
	review := joinAs(t, d.store, "codex", "synthetic-review", repo, "review")
	sub, err := d.store.Register(context.Background(), withLaunch(t, d.store, Registration{
		Family: "codex", ID: "synthetic-sub", Directory: repo, Repository: repo, Subagent: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	c := newActionClient(t, d, root)
	form := func(s Session, revision int64) url.Values {
		return url.Values{"family": {s.Family}, "id": {s.ID}, "revision": {strconv.FormatInt(revision, 10)}, "address": {a.Address}}
	}
	n := len(auditAll(t, d.store))
	for _, test := range []struct {
		session  Session
		revision int64
		want     string
	}{
		{b, b.Revision + 1, "revision_changed"},
		{review, review.Revision, "not_participant"},
		{sub, sub.Revision, "not_participant"},
		{b, 0, "invalid_request"},
	} {
		if got := c.do("participant-holder", form(test.session, test.revision)); got != test.want {
			t.Fatalf("choice refusal: %s, want %s", got, test.want)
		}
		expectAudit(t, d.store, n, "participant-holder", "refused", test.want)
		n++
		if p := participantOf(t, d.store, a.Address); p.Holder != a.Name {
			t.Fatalf("refused choice changed the holder: %+v", p)
		}
	}
	if got := c.do("participant-holder", form(b, b.Revision)); got != "participant_chosen" {
		t.Fatalf("choice: %s", got)
	}
	if audit := expectAudit(t, d.store, n, "participant-holder", "accepted", ""); audit.Target != a.Address {
		t.Fatalf("choice audit target: %q", audit.Target)
	}
	n++
	if p := participantOf(t, d.store, a.Address); p.Holder != b.Name || p.LastEvent.Reason != "maintainer_choice" || p.LastEvent.Former != a.Name {
		t.Fatalf("chosen holder/event: %+v", p)
	}
	if sessionState(t, d.store, Key{a.Family, a.ID}).HoldsAddress || !sessionState(t, d.store, Key{b.Family, b.ID}).HoldsAddress {
		t.Fatal("session holder flags disagree with the choice")
	}
	// A session that became inactive after the page was loaded cannot be chosen.
	if _, err := d.store.Mutate(context.Background(), Mutation{Family: a.Family, ID: a.ID, IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	a = sessionState(t, d.store, Key{a.Family, a.ID})
	if got := c.do("participant-holder", form(a, a.Revision)); got != "session_not_active" {
		t.Fatalf("inactive choice: %s", got)
	}
	expectAudit(t, d.store, n, "participant-holder", "refused", "session_not_active")
}

func TestDashboardParticipantChoiceProtection(t *testing.T) {
	d, root := startTestDaemon(t)
	repo := namedRepo(t, "koinon")
	a := joinAs(t, d.store, "codex", "synthetic-a", repo, "")
	b := joinAs(t, d.store, "codex", "synthetic-b", repo, "")
	c := newActionClient(t, d, root)
	for _, test := range []struct {
		name   string
		cookie string
		csrf   string
		origin string
	}{
		{"unauthenticated", "", c.csrf, "http://" + d.Addresses()[0]},
		{"missing CSRF", c.cookie, "", "http://" + d.Addresses()[0]},
		{"foreign origin", c.cookie, c.csrf, "http://evil.example"},
	} {
		form := url.Values{"family": {b.Family}, "id": {b.ID}, "revision": {strconv.FormatInt(b.Revision, 10)}, "address": {a.Address}, "csrf": {test.csrf}}
		result := dashboardDo(t, d, "POST", "/dashboard/actions/participant-holder", test.cookie,
			func(r *http.Request) { r.Header.Set("Origin", test.origin) }, form)
		if result.status != http.StatusForbidden && result.status != http.StatusUnauthorized {
			t.Fatalf("%s: %d", test.name, result.status)
		}
		if p := participantOf(t, d.store, a.Address); p.Holder != a.Name {
			t.Fatalf("%s changed the holder: %+v", test.name, p)
		}
	}
}

func TestDashboardParticipantDisplay(t *testing.T) {
	d, root := startTestDaemon(t)
	repo := namedRepo(t, "koinon")
	a := joinAs(t, d.store, "codex", "synthetic-a", repo, "")
	b := joinAs(t, d.store, "codex", "synthetic-b", repo, "")
	c := joinAs(t, d.store, "codex", "synthetic-c", repo, "")
	joinAs(t, d.store, "codex", "synthetic-review", repo, "review")
	if _, err := d.store.Mutate(context.Background(), Mutation{Family: a.Family, ID: a.ID, IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	// Renewal sees two qualifiers and records the unheld conflict.
	b = renew(t, d.store, b)
	client := newActionClient(t, d, root)
	page := func(fragment bool) string {
		path := "/dashboard/sessions"
		if fragment {
			path += "?fragment=1"
		}
		result := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		if result.status != http.StatusOK {
			t.Fatalf("sessions: %d", result.status)
		}
		return result.body
	}
	for _, fragment := range []bool{false, true} {
		body := page(fragment)
		for _, want := range []string{"role default", "role review", "codex-koinon-review", "does not hold address", "holds address", "no active holder", "conflict:", b.Name, c.Name, "last event: conflict", "last event: only_qualifier"} {
			if !strings.Contains(body, want) {
				t.Fatalf("sessions missing %q", want)
			}
		}
		if got := strings.Count(body, `action="/dashboard/actions/participant-holder"`); got != 2 {
			t.Fatalf("holder actions: %d, want two active non-holders", got)
		}
	}
	if got := client.do("participant-holder", url.Values{"family": {b.Family}, "id": {b.ID}, "revision": {strconv.FormatInt(b.Revision, 10)}, "address": {b.Address}}); got != "participant_chosen" {
		t.Fatal(got)
	}
	if body := page(true); strings.Contains(body, "conflict:") || !strings.Contains(body, "last event: maintainer_choice") || !strings.Contains(body, "holder "+b.Name) {
		t.Fatal("fragment did not reflect holder choice and cleared conflict")
	}
}
