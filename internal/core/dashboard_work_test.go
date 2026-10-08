package core

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// Dashboard work tests (#225): a real daemon with temporary state and synthetic items.

func TestDashboardWorkActions(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	agent := MemoryCaller{Repository: "/synthetic/workactions/.git", Family: "codex", Name: "codex-synthetic-01", Consumer: "codex:synthetic-a"}
	repository := agent.Repository
	claimed := create(t, s, agent, "synthetic claimed item")
	generation := mustStart(t, s, agent, claimed, nil)
	client := newActionClient(t, d, root)
	n := len(auditAll(t, s))
	deadline := strconv.FormatInt(int64(s.clock())+3600, 10)
	item := func(id, key string, values map[string]string) url.Values {
		v := url.Values{"repository": {repository}, "work_id": {id}, "revision": {strconv.FormatInt(revision(t, s, agent, id), 10)},
			"key": {key}, "deadline": {deadline}}
		for k, value := range values {
			v.Set(k, value)
		}
		return v
	}

	// Create: written as maintainer, with a proposal and references.
	if got := client.do("work-create", url.Values{"repository": {repository}, "key": {"create-1"}, "deadline": {deadline},
		"title": {"synthetic dashboard item"}, "criteria": {"synthetic criteria"}, "non_goals": {"synthetic non-goals"},
		"proposed_assignee": {"codex:synthetic-b"}, "references": {"ref-one\n\nref-two\n"}}); got != "work_created" {
		t.Fatalf("create: %s", got)
	}
	expectAudit(t, s, n, "work-create", "accepted", "")
	n++
	var created string
	if err := s.db.QueryRow(`SELECT work_id FROM work_items WHERE title='synthetic dashboard item'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	view := get(t, s, agent, created)
	if view["proposed_assignee"] != "codex:synthetic-b" || view["created_consumer"] != "maintainer" || len(view["references"].([]any)) != 2 {
		t.Fatalf("created item: %+v", view)
	}
	var writer string
	if err := s.db.QueryRow(`SELECT writer_family||':'||writer_name FROM memory_entries WHERE repository=? AND seq=?`,
		repository, int64(view["seq"].(float64))).Scan(&writer); err != nil || writer != "maintainer:maintainer" {
		t.Fatalf("event writer: %q %v", writer, err)
	}

	// Propose and clear.
	if got := client.do("work-propose", item(created, "propose-1", map[string]string{"proposed_assignee": "claude:synthetic-c"})); got != "work_proposed" {
		t.Fatalf("propose: %s", got)
	}
	expectAudit(t, s, n, "work-propose", "accepted", "")
	n++
	if got := client.do("work-propose", item(created, "propose-2", nil)); got != "work_proposed" {
		t.Fatalf("clear: %s", got)
	}
	n++
	if view := get(t, s, agent, created); view["proposed_assignee"] != nil {
		t.Fatalf("not cleared: %+v", view["proposed_assignee"])
	}

	// Edit: a stale revision gets the work refusal code; the current one edits the scope.
	stale := item(created, "edit-1", map[string]string{"title": "x", "criteria": "x", "non_goals": "x"})
	stale.Set("revision", "1")
	if got := client.do("work-edit", stale); got != "revision_conflict" {
		t.Fatalf("stale edit: %s", got)
	}
	expectAudit(t, s, n, "work-edit", "refused", "revision_conflict")
	n++
	if got := client.do("work-edit", item(created, "edit-2", map[string]string{"title": "synthetic edited title",
		"criteria": "synthetic edited criteria", "non_goals": "synthetic non-goals"})); got != "work_edited" {
		t.Fatalf("edit: %s", got)
	}
	expectAudit(t, s, n, "work-edit", "accepted", "")
	n++
	if view := get(t, s, agent, created); view["title"] != "synthetic edited title" || len(view["scope_revisions"].([]any)) != 2 {
		t.Fatalf("edited: %+v", view)
	}
	// An item with a live claim is not edited; the page says to release it first.
	if got := client.do("work-edit", item(claimed, "edit-3", map[string]string{"title": "x", "criteria": "x", "non_goals": "x"})); got != "work_claimed" {
		t.Fatalf("edit of a claimed item: %s", got)
	}
	expectAudit(t, s, n, "work-edit", "refused", "work_claimed")
	n++

	// An outcome without its requirement is refused before the item is claimed.
	if got := client.do("work-finish", item(created, "finish-0", map[string]string{"outcome": "completed"})); got != "invalid_request" {
		t.Fatalf("completion without references: %s", got)
	}
	expectAudit(t, s, n, "work-finish", "refused", "invalid_request")
	n++
	if view := get(t, s, agent, created); view["current_claim"] != nil {
		t.Fatalf("a refused finish claimed the item: %+v", view)
	}
	// Finish an unclaimed item: the maintainer claims and finishes it; one audit record.
	if got := client.do("work-finish", item(created, "finish-1", map[string]string{"outcome": "withdrawn", "reason": "synthetic reason",
		"references": "ref-three"})); got != "work_finished" {
		t.Fatalf("finish: %s", got)
	}
	expectAudit(t, s, n, "work-finish", "accepted", "")
	n++
	if view := get(t, s, agent, created); view["lifecycle"] != "finished" || view["outcome"] != "withdrawn" || view["reason"] != "synthetic reason" {
		t.Fatalf("finished: %+v", view)
	}
	if kinds := strings.Join(events(t, s, agent, created), ","); kinds != "created,proposed,proposed,edited,started,finished" {
		t.Fatalf("events: %s", kinds)
	}
	// Finish a claimed item on behalf of its owner.
	if got := client.do("work-finish", item(claimed, "finish-2", map[string]string{"outcome": "completed", "references": "evidence"})); got != "work_finished" {
		t.Fatalf("finish claimed: %s", got)
	}
	expectAudit(t, s, n, "work-finish", "accepted", "")
	n++
	if view := get(t, s, agent, claimed); view["lifecycle"] != "finished" || view["outcome"] != "completed" || view["last_generation"] != float64(generation) {
		t.Fatalf("finished claimed: %+v", view)
	}
	// Finished is final.
	if got := client.do("work-finish", item(claimed, "finish-3", map[string]string{"outcome": "completed", "references": "evidence"})); got != "invalid_transition" {
		t.Fatalf("finish again: %s", got)
	}
	expectAudit(t, s, n, "work-finish", "refused", "invalid_transition")
	n++

	// Refusals before any write.
	if got := client.do("work-create", url.Values{"repository": {"/synthetic/none/.git"}, "key": {"create-2"}, "deadline": {deadline},
		"title": {"t"}, "criteria": {"c"}, "non_goals": {"n"}}); got != "store_not_found" {
		t.Fatalf("unknown store: %s", got)
	}
	expectAudit(t, s, n, "work-create", "refused", "store_not_found")
	n++
	if got := client.do("work-finish", item(claimed, "finish-4", map[string]string{"outcome": "abandoned"})); got != "invalid_request" {
		t.Fatalf("bad outcome: %s", got)
	}
	expectAudit(t, s, n, "work-finish", "refused", "invalid_request")

	// The view lists finished items on request and opens one item with its history.
	page := func(query string) string {
		t.Helper()
		r := dashboardDo(t, d, "GET", "/dashboard/work?"+query, client.cookie, nil, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d", query, r.status)
		}
		return r.body
	}
	if body := page(""); strings.Contains(body, "synthetic edited title") {
		t.Fatal("the default view lists a finished item")
	}
	if body := page("lifecycle=finished"); !strings.Contains(body, "synthetic edited title") || !strings.Contains(body, "withdrawn") {
		t.Fatal("the finished filter")
	}
	detail := page("lifecycle=all&store=" + url.QueryEscape(repository) + "&item=" + created)
	for _, want := range []string{"synthetic edited criteria", "synthetic reason", "ref-three", "History", "maintainer maintainer", "edited"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("the item page lacks %q", want)
		}
	}
	for _, bad := range []string{"lifecycle=unknown", "store=" + url.QueryEscape(repository) + "&item=short",
		"store=" + url.QueryEscape(repository) + "&item=" + strings.Repeat("f", 32)} {
		if r := dashboardDo(t, d, "GET", "/dashboard/work?"+bad, client.cookie, nil, nil); r.status != 400 {
			t.Fatalf("%s: %d", bad, r.status)
		}
	}
}

// Review regressions on #244 (F1): a finish that Work refuses leaves an unclaimed item as
// it was, with no claim and no event.
func TestDashboardRefusedFinishLeavesItemUnclaimed(t *testing.T) {
	d, root := startTestDaemon(t)
	m := MemoryCaller{Repository: "/synthetic/refused-finish/.git", Family: "codex", Name: "synthetic-peer", Consumer: "codex:synthetic-peer"}
	id := create(t, d.store, m, "synthetic item")
	client := newActionClient(t, d, root)
	deadline := strconv.FormatInt(int64(d.store.clock())+3600, 10)
	for i, form := range []url.Values{
		{"outcome": {"completed"}, "references": {strings.Repeat("x", 513)}},
		{"outcome": {"withdrawn"}, "reason": {strings.Repeat("x", 2000)}},
		{"outcome": {"withdrawn"}},
		{"outcome": {"abandoned"}, "reason": {"r"}},
	} {
		for k, v := range map[string]string{"repository": m.Repository, "work_id": id, "revision": "1", "deadline": deadline,
			"key": "refused-" + strconv.Itoa(i)} {
			form.Set(k, v)
		}
		if got := client.do("work-finish", form); got != "invalid_request" {
			t.Fatalf("finish %v: %s", form, got)
		}
	}
	if item := get(t, d.store, m, id); item["current_claim"] != nil || item["lifecycle"] != "open" || item["revision"] != float64(1) {
		t.Fatalf("a refused finish changed the item: %+v", item)
	}
	if kinds := strings.Join(events(t, d.store, m, id), ","); kinds != "created" {
		t.Fatalf("events: %s", kinds)
	}
}

// Review regressions on #244 (F2): the same finish form again replays the first result,
// for an item that was claimed and one that was not, with no new event or audit record.
func TestDashboardFinishReplay(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(fmt.Sprintf("claimed-%v", claimed), func(t *testing.T) {
			d, root := startTestDaemon(t)
			m := MemoryCaller{Repository: "/synthetic/finish-replay/.git", Family: "codex", Name: "synthetic-peer", Consumer: "codex:synthetic-peer"}
			id := create(t, d.store, m, "synthetic item")
			if claimed {
				mustStart(t, d.store, m, id, nil)
			}
			client := newActionClient(t, d, root)
			form := url.Values{"repository": {m.Repository}, "work_id": {id}, "revision": {strconv.FormatInt(revision(t, d.store, m, id), 10)},
				"key": {"finish-replay"}, "deadline": {strconv.FormatInt(int64(d.store.clock())+3600, 10)}, "outcome": {"completed"},
				"references": {"synthetic evidence"}}
			if got := client.do("work-finish", form); got != "work_finished" {
				t.Fatalf("first finish: %s", got)
			}
			kinds, audits := strings.Join(events(t, d.store, m, id), ","), len(auditAll(t, d.store))
			if got := client.do("work-finish", form); got != "work_finished" {
				t.Fatalf("replayed finish: %s", got)
			}
			if again := strings.Join(events(t, d.store, m, id), ","); again != kinds || len(auditAll(t, d.store)) != audits {
				t.Fatalf("the replay wrote: %s -> %s, audit %d -> %d", kinds, again, audits, len(auditAll(t, d.store)))
			}
			// The same key with other content conflicts.
			form.Set("references", "other evidence")
			if got := client.do("work-finish", form); got != "idempotency_conflict" {
				t.Fatalf("changed form: %s", got)
			}
		})
	}
}
