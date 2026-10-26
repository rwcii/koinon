package core

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A view's daemon fault answers 500 with its own page and a log event, and keeps the
// navigation; an invalid view query keeps 400 "request" (#507).
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
}
