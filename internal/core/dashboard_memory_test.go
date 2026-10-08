package core

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Dashboard memory tests (#224): a real daemon with temporary state and synthetic entries.

func agentNote(t *testing.T, s *Store, repository, kind, body string, supersedes *int64) int64 {
	t.Helper()
	result, err := s.MemoryRecord(context.Background(), MemoryCaller{Repository: repository, Family: "codex", Name: "codex-synthetic-01",
		Consumer: "codex-synthetic-01"}, MemoryRecordRequest{Type: kind, Body: body, Supersedes: supersedes})
	if err != nil {
		t.Fatal(err)
	}
	return result.Seq
}

func entry(t *testing.T, s *Store, repository string, seq int64) MemoryEntry {
	t.Helper()
	e, err := s.memoryEntry(context.Background(), repository, seq)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDashboardMemoryEntriesReadSearchAndPage(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	repository := "/synthetic/memview/.git"
	old := agentNote(t, s, repository, "decision", "synthetic old decision", nil)
	agentNote(t, s, repository, "decision", "synthetic new decision", &old)
	agentNote(t, s, repository, "gotcha", "synthetic GOTCHA about 50% of runs", nil)
	for i := range 55 {
		agentNote(t, s, repository, "finding", "synthetic finding "+strconv.Itoa(i), nil)
	}
	client := newActionClient(t, d, root)
	page := func(query string) string {
		t.Helper()
		r := dashboardDo(t, d, "GET", "/dashboard/memory?"+query, client.cookie, nil, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d", query, r.status)
		}
		return r.body
	}
	store := "store=" + url.QueryEscape(repository)
	// The stores table links to each store's entries.
	if body := page(""); !strings.Contains(strings.ToLower(body), strings.ToLower(`/dashboard/memory?store=`+url.QueryEscape(repository))) {
		t.Fatal("no link to the store's entries")
	}
	first := page(store)
	if !strings.Contains(first, "synthetic finding 54") || strings.Contains(first, "synthetic finding 4<") || !strings.Contains(first, "Older entries") {
		t.Fatal("the first page is not the 50 newest entries")
	}
	if strings.Contains(first, "synthetic old decision") {
		t.Fatal("a replaced entry is listed without all=1")
	}
	entries, next, err := s.dashboardEntries(context.Background(), repository, "", "", false, 0)
	if err != nil || len(entries) != dashboardEntryPage || next == 0 {
		t.Fatalf("page: %d %d %v", len(entries), next, err)
	}
	older := page(store + "&before=" + strconv.FormatInt(next, 10))
	if !strings.Contains(older, "synthetic GOTCHA") || strings.Contains(older, "synthetic finding 54") {
		t.Fatal("the next page does not continue the first")
	}
	// The search matches the body as a substring, ignoring case, with % matched literally.
	found := page(store + "&q=" + url.QueryEscape("gotcha about 50%"))
	if !strings.Contains(found, "synthetic GOTCHA") || strings.Contains(found, "synthetic finding") {
		t.Fatal("search")
	}
	if body := page(store + "&type=decision"); !strings.Contains(body, "synthetic new decision") || strings.Contains(body, "synthetic finding") {
		t.Fatal("type filter")
	}
	all := page(store + "&type=decision&all=1")
	if !strings.Contains(all, "synthetic old decision") || !strings.Contains(all, "replaced by") || !strings.Contains(all, "superseded") {
		t.Fatal("all=1 does not show the replaced entry with its link")
	}
	for _, bad := range []string{"&type=unknown", "&before=x", "&q=" + strings.Repeat("a", 257)} {
		if r := dashboardDo(t, d, "GET", "/dashboard/memory?"+store+bad, client.cookie, nil, nil); r.status != 400 {
			t.Fatalf("%s: %d", bad, r.status)
		}
	}
}

func TestDashboardMemoryActions(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	repository := "/synthetic/memactions/.git"
	target := agentNote(t, s, repository, "decision", "synthetic decision to edit", nil)
	client := newActionClient(t, d, root)
	n := len(auditAll(t, s))
	deadline := strconv.FormatInt(int64(s.clock())+3600, 10)
	form := func(values map[string]string) url.Values {
		v := url.Values{"repository": {repository}, "deadline": {deadline}}
		for k, value := range values {
			v.Set(k, value)
		}
		return v
	}

	// Add: an entry of any type and scope, written as maintainer.
	if got := client.do("memory-record", form(map[string]string{"key": "add-1", "type": "directive", "scope": "task",
		"scope_target": "synthetic-task", "body": "synthetic maintainer directive"})); got != "memory_recorded" {
		t.Fatalf("record: %s", got)
	}
	expectAudit(t, s, n, "memory-record", "accepted", "")
	n++
	var added MemoryEntry
	if err := func() error {
		var err error
		added, err = scanEntry(s.db.QueryRow(`SELECT `+entryColumns+` FROM memory_entries WHERE repository=? ORDER BY seq DESC LIMIT 1`, repository))
		return err
	}(); err != nil {
		t.Fatal(err)
	}
	if added.WriterFamily != "maintainer" || added.WriterName != "maintainer" || added.Type != "directive" || added.Scope != "task" {
		t.Fatalf("added entry: %+v", added)
	}
	// The same form again is a duplicate, not a second entry, and changes nothing.
	if got := client.do("memory-record", form(map[string]string{"key": "add-1", "type": "directive", "scope": "task",
		"scope_target": "synthetic-task", "body": "synthetic maintainer directive"})); got != "memory_recorded" {
		t.Fatalf("resubmit: %s", got)
	}
	if c := count(t, s, `SELECT COUNT(*) FROM memory_entries WHERE repository=? AND writer_family='maintainer'`, repository); c != 1 {
		t.Fatalf("resubmit wrote %d entries", c)
	}
	if len(auditAll(t, s)) != n {
		t.Fatal("a duplicate wrote an audit record")
	}

	// Edit: a supersession by the maintainer; the old entry stays readable with its link.
	if got := client.do("memory-record", form(map[string]string{"key": "edit-1", "type": "decision", "scope": "repo",
		"supersedes": strconv.FormatInt(target, 10), "body": "synthetic decision, edited"})); got != "memory_superseded" {
		t.Fatalf("edit: %s", got)
	}
	expectAudit(t, s, n, "memory-supersede", "accepted", "")
	n++
	edited := entry(t, s, repository, target)
	if edited.SupersededBy == nil || edited.Body != "synthetic decision to edit" {
		t.Fatalf("old entry: %+v", edited)
	}
	if replacement := entry(t, s, repository, *edited.SupersededBy); replacement.WriterFamily != "maintainer" || replacement.Revision != 2 {
		t.Fatalf("replacement: %+v", replacement)
	}

	// Conflict: an edit of an entry that another writer replaced meanwhile is recorded and
	// reported, not lost.
	other := agentNote(t, s, repository, "gotcha", "synthetic gotcha", nil)
	agentNote(t, s, repository, "gotcha", "synthetic gotcha, replaced by an agent", &other)
	if got := client.do("memory-record", form(map[string]string{"key": "edit-2", "type": "gotcha", "scope": "repo",
		"supersedes": strconv.FormatInt(other, 10), "body": "synthetic gotcha, maintainer's edit"})); got != "memory_conflict" {
		t.Fatalf("conflicting edit: %s", got)
	}
	expectAudit(t, s, n, "memory-supersede", "accepted", "")
	n++
	if c := count(t, s, `SELECT COUNT(*) FROM memory_entries WHERE repository=? AND conflicts_with IS NOT NULL AND writer_family='maintainer'`, repository); c != 1 {
		t.Fatalf("conflict not recorded: %d", c)
	}

	// Revoke: a reason is required; the revocation keeps the entry readable.
	revoke := func(key, reason string) string {
		return client.do("memory-revoke", form(map[string]string{"key": key, "seq": strconv.FormatInt(added.Seq, 10), "reason": reason}))
	}
	if got := revoke("revoke-1", "  "); got != "reason_required" {
		t.Fatalf("revoke without reason: %s", got)
	}
	expectAudit(t, s, n, "memory-revoke", "refused", "reason_required")
	n++
	if got := revoke("revoke-2", "synthetic reason"); got != "memory_revoked" {
		t.Fatalf("revoke: %s", got)
	}
	expectAudit(t, s, n, "memory-revoke", "accepted", "")
	n++
	if revoked := entry(t, s, repository, added.Seq); revoked.RevokedBy == nil {
		t.Fatalf("not revoked: %+v", revoked)
	} else if r := entry(t, s, repository, *revoked.RevokedBy); r.Body != "synthetic reason" || r.Type != "directive" || r.WriterFamily != "maintainer" {
		t.Fatalf("revocation: %+v", r)
	}

	// Refusals: an unknown store, an unknown entry, a stale form and a missing key.
	for _, c := range []struct {
		action string
		values map[string]string
		want   string
	}{
		{"memory-record", map[string]string{"key": "x-1", "type": "finding", "body": "b", "repository": "/synthetic/none/.git"}, "store_not_found"},
		{"memory-revoke", map[string]string{"key": "x-2", "seq": "9999", "reason": "r"}, "no_such_entry"},
		{"memory-record", map[string]string{"key": "x-3", "type": "finding", "body": "b", "deadline": strconv.FormatInt(int64(s.clock())-1, 10)}, "retry_deadline_expired"},
		{"memory-record", map[string]string{"type": "finding", "body": "b"}, "invalid_request"},
		{"memory-record", map[string]string{"key": "x-4", "type": "unknown", "body": "b"}, "invalid_request"},
	} {
		if got := client.do(c.action, form(c.values)); got != c.want {
			t.Fatalf("%s %v: %s", c.action, c.values, got)
		}
		expectAudit(t, s, n, c.action, "refused", c.want)
		n++
	}
	// The view shows the maintainer's entries and their forms.
	body := dashboardDo(t, d, "GET", "/dashboard/memory?all=1&store="+url.QueryEscape(repository), client.cookie, nil, nil).body
	for _, want := range []string{"synthetic decision, edited", "maintainer", "revoked by", "conflicts with", `/dashboard/actions/memory-revoke`, "Record an entry"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the view lacks %q", want)
		}
	}
}
