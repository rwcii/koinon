package core

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A view's daemon fault answers 500 with its own page and a log event, and keeps the
// navigation; an invalid view query keeps 400 "request" (#507). A stored record that cannot be
// read is a daemon fault too, and the fault page loads the whole view, with its actions, once
// the read works again.
func TestDashboardViewFaultIsNotARequestError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	seedState(t, root)
	var mu sync.Mutex
	var events []map[string]any
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0", "[::1]:0"}, LogEvent: func(event string, fields map[string]any) {
		if event == "dashboard_view_failed" {
			mu.Lock()
			events = append(events, fields)
			mu.Unlock()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	client := newActionClient(t, d, root)
	if page := dashboardDo(t, d, "GET", "/dashboard/decisions?status=unknown", client.cookie, nil, nil); page.status != 400 ||
		!strings.Contains(page.body, "The request was not valid.") {
		t.Fatalf("invalid query: %d %s", page.status, page.body)
	}
	// The board's table is gone: the read fails in the daemon.
	if _, err := fixtureExec(d.store, `ALTER TABLE decision_items RENAME TO synthetic_moved`); err != nil {
		t.Fatal(err)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil)
	if page.status != 500 || !strings.Contains(page.body, "The daemon could not read this view.") || strings.Contains(page.body, "The request was not valid.") ||
		!strings.Contains(page.body, `<nav>`) || !strings.Contains(page.body, `data-refresh="5000"`) || strings.Contains(page.body, "synthetic_moved") ||
		strings.Contains(page.body, "no such table") {
		t.Fatalf("fault: %d %s", page.status, page.body)
	}
	if fragment := dashboardDo(t, d, "GET", "/dashboard/decisions?fragment=1", client.cookie, nil, nil); fragment.status != 500 {
		t.Fatalf("fault fragment: %d", fragment.status)
	}
	mu.Lock()
	logged := len(events) > 0 && events[0]["view"] == "decisions" && strings.Contains(events[0]["error"].(string), "decision_items")
	mu.Unlock()
	if !logged {
		t.Fatalf("log events: %v", events)
	}
	if _, err := fixtureExec(d.store, `ALTER TABLE synthetic_moved RENAME TO decision_items`); err != nil {
		t.Fatal(err)
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil); page.status != 200 {
		t.Fatalf("after the fault: %d", page.status)
	}

	// A stored decision whose commands cannot be read refuses decision_unavailable: no change
	// to the request avoids it.
	author := join(t, d.store, "claude", "synthetic-author", "")
	k := Key{"claude", author.ID}
	mustCreate(t, d.store, k, "FAULT-1", decisionFields("Stored record fault", "decision"))
	if _, err := fixtureExec(d.store, `UPDATE decision_items SET commands='not-json' WHERE reference='FAULT-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.DecisionGet(context.Background(), k, "FAULT-1"); refusalCode(err) != "decision_unavailable" {
		t.Fatalf("stored record: %v", err)
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil); page.status != 500 ||
		!strings.Contains(page.body, "The daemon could not read this view.") {
		t.Fatalf("stored record fault: %d %s", page.status, page.body)
	}

	// The fault page keeps the view's title and asks the refresh to load the whole view; it
	// shows no login instructions. The recovered view has its actions again.
	if _, err := fixtureExec(d.store, `ALTER TABLE settings RENAME TO synthetic_moved_settings`); err != nil {
		t.Fatal(err)
	}
	page = dashboardDo(t, d, "GET", "/dashboard/settings", client.cookie, nil, nil)
	if page.status != 500 || !strings.Contains(page.body, "<h1>Settings</h1>") || !strings.Contains(page.body, `data-reload="true"`) ||
		strings.Contains(page.body, "koinon dashboard") || strings.Contains(page.body, "Not signed in") {
		t.Fatalf("settings fault: %d %s", page.status, page.body)
	}
	if _, err := fixtureExec(d.store, `ALTER TABLE synthetic_moved_settings RENAME TO settings`); err != nil {
		t.Fatal(err)
	}
	if fragment := dashboardDo(t, d, "GET", "/dashboard/settings?fragment=1", client.cookie, nil, nil); fragment.status != 200 {
		t.Fatalf("recovered fragment: %d", fragment.status)
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/settings", client.cookie, nil, nil); page.status != 200 ||
		!strings.Contains(page.body, `action="/dashboard/actions/settings"`) || strings.Contains(page.body, `data-reload`) {
		t.Fatalf("recovered view: %d %s", page.status, page.body)
	}
}
