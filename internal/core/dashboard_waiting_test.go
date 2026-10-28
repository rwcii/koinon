package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// GET /dashboard/waiting gives the spoken alert the waiting decisions and tasks, page by page,
// with the decision change version (#553). All data is synthetic.
func TestDashboardWaiting(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-waiting-peer", "")
	key := Key{"claude", peer.ID}
	create := func(reference, kind string) {
		mustCreate(t, d.store, key, reference, DecisionFields{Title: ptr("Synthetic " + reference), Kind: &kind,
			Recipients: &[]string{peer.Name}})
	}
	read := func(after int64) waitingPage {
		t.Helper()
		r := dashboardDo(t, d, "GET", fmt.Sprintf("/dashboard/waiting?after=%d", after), client.cookie, nil, nil)
		var page waitingPage
		if r.status != 200 || r.header.Get("Content-Type") != "application/json" || json.Unmarshal([]byte(r.body), &page) != nil {
			t.Fatalf("waiting: %d %q %s", r.status, r.header.Get("Content-Type"), r.body)
		}
		// The dashboard's headers and policy apply to it too.
		if r.header.Get("Content-Security-Policy") == "" || r.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("headers: %v", r.header)
		}
		return page
	}
	empty := read(0)
	if len(empty.Items) != 0 || empty.More || empty.Version == "" {
		t.Fatalf("empty board: %+v", empty)
	}
	create("D-1", "decision")
	create("T-1", "task")
	create("F-1", "fyi")
	// The author's repository is the participant's Git directory; the alert says its name.
	if _, err := fixtureExec(d.store, `UPDATE decision_items SET author_participant=? WHERE reference='D-1'`,
		"claude\x1f/synthetic/projects/koinon/.git\x1fclaude-koinon"); err != nil {
		t.Fatal(err)
	}
	page := read(0)
	if page.Version == empty.Version || page.More || page.Next != 0 || len(page.Items) != 2 ||
		page.Items[0] != (waitingItem{Reference: "D-1", Revision: 1, Kind: "decision", Repository: "koinon"}) ||
		page.Items[1] != (waitingItem{Reference: "T-1", Revision: 1, Kind: "task"}) {
		t.Fatalf("waiting: %+v", page)
	}
	// An edit is a new revision and a new version; a verdict takes the item out.
	if _, err := d.store.DecisionEdit(context.Background(), key, "D-1", 1, DecisionFields{Title: ptr("Edited")}); err != nil {
		t.Fatal(err)
	}
	edited := read(0)
	if edited.Version == page.Version || edited.Items[0].Revision != 2 {
		t.Fatalf("after the edit: %+v", edited)
	}
	if _, err := d.store.DecisionVerdict(context.Background(), "T-1", 1, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if done := read(0); done.Version == edited.Version || len(done.Items) != 1 || done.Items[0].Reference != "D-1" {
		t.Fatalf("after the verdict: %+v", done)
	}
	// More than a page: every item is read once, in ID order, over the pages.
	for i := 0; i < waitingPageSize+5; i++ {
		create(fmt.Sprintf("M-%d", i), "decision")
	}
	var seen []string
	after := int64(0)
	for {
		p := read(after)
		for _, item := range p.Items {
			seen = append(seen, item.Reference)
		}
		if !p.More {
			break
		}
		if len(p.Items) != waitingPageSize || p.Next == 0 {
			t.Fatalf("full page: %d items, next %d", len(p.Items), p.Next)
		}
		after = p.Next
	}
	if len(seen) != waitingPageSize+6 || seen[0] != "D-1" || seen[len(seen)-1] != fmt.Sprintf("M-%d", waitingPageSize+4) {
		t.Fatalf("pages: %d items, %v ... %v", len(seen), seen[:2], seen[len(seen)-1])
	}
	// No login, no items; a bad cursor is refused.
	if r := dashboardDo(t, d, "GET", "/dashboard/waiting", "", nil, nil); r.status != 401 || strings.Contains(r.body, "D-1") {
		t.Fatalf("without a login: %d", r.status)
	}
	for _, bad := range []string{"-1", "x"} {
		if r := dashboardDo(t, d, "GET", "/dashboard/waiting?after="+bad, client.cookie, nil, nil); r.status != 400 {
			t.Fatalf("after=%s: %d", bad, r.status)
		}
	}
}

func TestSpokenRepository(t *testing.T) {
	for path, want := range map[string]string{"/home/me/koinon/.git": "koinon", "/srv/git/tools.git": "tools", "/home/me/work/.git/": "work",
		"": "", "/": "", "/.git": ""} {
		if got := spokenRepository(path); got != want {
			t.Errorf("%q: %q, want %q", path, got, want)
		}
	}
}
