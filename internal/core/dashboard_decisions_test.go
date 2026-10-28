package core

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// The designed decision board (#206, chunk 03). All data is synthetic.

var cardReference = regexp.MustCompile(`<article class="card [^"]*" id="card-([A-Za-z0-9-]+)">`)

func boardCards(body string) []string {
	var refs []string
	for _, m := range cardReference.FindAllStringSubmatch(body, -1) {
		refs = append(refs, m[1])
	}
	return refs
}

// cardOf returns the markup of one card.
func cardOf(t *testing.T, body, reference string) string {
	t.Helper()
	start := strings.Index(body, `id="card-`+reference+`"`)
	if start < 0 {
		t.Fatalf("no card %s in %s", reference, body)
	}
	end := strings.Index(body[start:], "</article>")
	return body[start : start+end]
}

func TestDecisionBoard(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-board-peer", "")
	key := Key{"claude", peer.ID}
	create := func(reference string, order int64, title, kind string, f DecisionFields) {
		f.Order, f.Title, f.Kind = &order, &title, &kind
		if f.Recipients == nil {
			f.Recipients = &[]string{peer.Name}
		}
		mustCreate(t, d.store, key, reference, f)
	}
	create("D-2", 2, "Second decision", "decision", DecisionFields{})
	create("D-1", 1, "First decision", "decision", DecisionFields{
		Context:        ptr("Plain **bold** line\nnext line\n\n```\n<x> héllo\n```"),
		Recommendation: ptr("- one\n- two"),
		Commands:       &[]string{"koinon status", "go test ./..."}})
	create("F-2", 0, "Plain notice", "fyi", DecisionFields{})
	create("F-1", 5, "Second notice", "fyi", DecisionFields{})
	create("T-1", 7, "Restart the synthetic service", "task", DecisionFields{})
	create("P-1", 3, "Approved work", "decision", DecisionFields{})
	create("A-1", 4, "Declined work", "decision", DecisionFields{})
	create("U-1", 6, "Nobody named", "decision", DecisionFields{Recipients: &[]string{"synthetic-nobody"}})
	if got := client.do("decision-approve", url.Values{"reference": {"P-1"}, "revision": {"1"}, "note": {"Use the **small** fix."}}); got != "decision_approved" {
		t.Fatalf("approve: %s", got)
	}
	if got := client.do("decision-decline", url.Values{"reference": {"A-1"}, "revision": {"1"}}); got != "decision_declined" {
		t.Fatalf("decline: %s", got)
	}
	// A stored command that fails the printable-ASCII rule is hidden on read.
	if _, err := fixtureExec(d.store, `UPDATE decision_items SET commands=? WHERE reference='D-2'`, `["echo ok","echo\tbad"]`); err != nil {
		t.Fatal(err)
	}
	// Claimed work of the agent, on its native consumer.
	m := MemoryCaller{Repository: "/synthetic/board/.git", Family: "claude", Name: peer.Name, Consumer: "claude:" + peer.ID}
	id := create_(t, d.store, m, "Synthetic claimed work")
	if _, err := start(d.store, m, id, revision(t, d.store, m, id), nil); err != nil {
		t.Fatal(err)
	}

	page := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil)
	if page.status != 200 {
		t.Fatalf("board: %d %s", page.status, page.body)
	}
	body := page.body
	for _, want := range []string{"<b>4</b> waiting", "<b>0</b> returned", "<b>1</b> in progress", "<b>2</b> open fyi", "<b>1</b> archived"} {
		if !strings.Contains(body, want) {
			t.Errorf("counts: no %q", want)
		}
	}
	if got := strings.Join(boardCards(body), " "); got != "T-1 F-2 D-1 D-2 P-1 F-1 U-1" {
		t.Errorf("card order: %s", got)
	}
	d1 := cardOf(t, body, "D-1")
	for _, want := range []string{"Plain <strong>bold</strong> line<br>next line", "<pre><code>&lt;x&gt; héllo</code></pre>",
		"<ul><li>one</li><li>two</li></ul>", `<div class="command"><pre><code>koinon status</code></pre><button type="button" class="copy">Copy</button></div>`,
		`<div class="command"><pre><code>go test ./...</code></pre>`, "/dashboard/actions/decision-approve", `name="revision" value="1"`} {
		if !strings.Contains(d1, want) {
			t.Errorf("D-1 card: no %q in %s", want, d1)
		}
	}
	d2 := cardOf(t, body, "D-2")
	if !strings.Contains(d2, "1 stored command(s) fail the printable-ASCII rule") || strings.Contains(d2, "echo\tbad") ||
		!regexp.MustCompile(`formaction="/dashboard/actions/decision-approve" disabled`).MatchString(d2) {
		t.Errorf("hidden command card: %s", d2)
	}
	f1 := cardOf(t, body, "F-1")
	if !strings.Contains(f1, "decision-acknowledge") || strings.Contains(f1, "decision-approve") || strings.Contains(f1, "decision-decline") ||
		strings.Contains(f1, "decision-feedback") {
		t.Errorf("fyi card: %s", f1)
	}
	// A task waits with Completed, Declined and Provide feedback (#537).
	t1 := cardOf(t, body, "T-1")
	if !strings.Contains(t1, `<button type="submit" formaction="/dashboard/actions/decision-complete">Completed</button> <button type="submit" formaction="/dashboard/actions/decision-decline">Declined</button> <button type="submit" formaction="/dashboard/actions/decision-feedback">Provide feedback</button>`) ||
		!strings.Contains(t1, ">waiting</span>") || strings.Contains(t1, "decision-approve") || strings.Contains(t1, "decision-acknowledge") {
		t.Errorf("task card: %s", t1)
	}
	p1 := cardOf(t, body, "P-1")
	if strings.Contains(p1, "<form") || !strings.Contains(p1, "Use the <strong>small</strong> fix.") || !strings.Contains(p1, "in progress") {
		t.Errorf("in-progress card: %s", p1)
	}
	if !strings.Contains(cardOf(t, body, "U-1"), "not resolved: unknown_name") {
		t.Error("an unresolved recipient is not flagged")
	}
	if !strings.Contains(body, `<a href="/dashboard/decisions?ref=A-1">A-1</a></td><td>Declined work</td><td>decision</td><td>declined</td>`) {
		t.Errorf("archive row: %s", body)
	}
	// The agent row: public name, claimed work and the waiting references that name it,
	// with no native ID, repository path or wake target.
	row := body[strings.Index(body, `<table class="agents">`):]
	row = row[:strings.Index(row, "</table>")]
	for _, want := range []string{peer.Name, "Synthetic claimed work", "?ref=D-1", "?ref=D-2", "?ref=T-1", "not reported"} {
		if !strings.Contains(row, want) {
			t.Errorf("agent row: no %q in %s", want, row)
		}
	}
	for _, private := range []string{peer.ID, "/synthetic/board", "?ref=P-1", "?ref=U-1", "?ref=F-1"} {
		if strings.Contains(row, private) {
			t.Errorf("agent row shows %q", private)
		}
	}
	if strings.Contains(body, peer.ID) || strings.Contains(body, "wake_target") {
		t.Error("the board shows a native ID or wake target")
	}

	// The lookup of an item that is waiting again after an edit shows its card with the
	// verdict form, and the history with its snapshots.
	if r := client.do("decision-approve", url.Values{"reference": {"D-1"}, "revision": {"1"}}); r != "decision_approved" {
		t.Fatalf("approve D-1: %s", r)
	}
	if _, err := d.store.DecisionEdit(context.Background(), key, "D-1", 1, DecisionFields{Context: ptr("New facts")}); err != nil {
		t.Fatal(err)
	}
	lookup := dashboardDo(t, d, "GET", "/dashboard/decisions?ref=D-1", client.cookie, nil, nil).body
	section := lookup[strings.Index(lookup, `<section class="lookup-result">`):strings.Index(lookup, "</section>")]
	if !strings.Contains(section, `id="lookup-D-1"`) || !strings.Contains(section, `name="revision" value="2"`) || !strings.Contains(section, "Verdict history") || !strings.Contains(section, "<strong>approved</strong> on revision 1") ||
		!strings.Contains(section, "superseded") || !strings.Contains(section, "Plain <strong>bold</strong> line") || !strings.Contains(section, "New facts") {
		t.Errorf("lookup: %s", section)
	}
}

// TestDecisionBoardSubagentRow shows an active subagent like any other agent session: its
// public name, its native work and the waiting references that name it.
func TestDecisionBoardSubagentRow(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	sub, err := d.store.Register(context.Background(), withLaunch(t, d.store, Registration{
		Family: "codex", ID: "synthetic-board-sub", Directory: t.TempDir(), TTLSeconds: 60, Subagent: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, d.store, Key{"codex", sub.ID}, "S-1", DecisionFields{Title: ptr("Subagent decision"), Kind: ptr("decision"), Recipients: &[]string{sub.Name}})
	m := MemoryCaller{Repository: "/synthetic/board/.git", Family: "codex", Name: sub.Name, Consumer: "codex:" + sub.ID}
	id := create_(t, d.store, m, "Synthetic subagent work")
	if _, err := start(d.store, m, id, revision(t, d.store, m, id), nil); err != nil {
		t.Fatal(err)
	}
	body := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil).body
	if !strings.Contains(body, `<table class="agents">`) {
		t.Fatalf("no agent table: %s", body)
	}
	row := body[strings.Index(body, `<table class="agents">`):]
	row = row[:strings.Index(row, "</table>")]
	for _, want := range []string{sub.Name, "Synthetic subagent work", "?ref=S-1"} {
		if !strings.Contains(row, want) {
			t.Errorf("subagent row: no %q in %s", want, row)
		}
	}
	if strings.Contains(body, sub.ID) {
		t.Error("the board shows the subagent's native ID")
	}
}

// create_ creates a work item; create is a local name in TestDecisionBoard.
func create_(t *testing.T, s *Store, m MemoryCaller, title string) string {
	t.Helper()
	return create(t, s, m, title)
}

// TestDecisionBoardNoticeOnCard shows a verdict's answer on its card, not at the top.
func TestDecisionBoardNoticeOnCard(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-board-notice", "")
	mustCreate(t, d.store, Key{"claude", peer.ID}, "N-1", decisionFields("Decide", "decision"))
	form := url.Values{"reference": {"N-1"}, "revision": {"2"}, "csrf": {client.csrf}}
	r := dashboardDo(t, d, "POST", "/dashboard/actions/decision-approve", client.cookie, client.sameOrigin, form)
	location := r.header.Get("Location")
	if !strings.Contains(location, "card=N-1") || !strings.Contains(location, "notice=stale_revision") {
		t.Fatalf("location: %s", location)
	}
	page := dashboardDo(t, d, "GET", location, client.cookie, nil, nil)
	card := cardOf(t, page.body, "N-1")
	if !strings.Contains(card, `<p class="card-notice refused" role="status">The item changed after the page showed it. Read it again; nothing was saved.</p>`) || strings.Contains(page.body, `<p class="notice"`) {
		t.Errorf("stale notice: %s", page.body)
	}
	// A card that the page does not show leaves the notice at the top.
	page = dashboardDo(t, d, "GET", "/dashboard/decisions?card=X-9&notice=stale_revision", client.cookie, nil, nil)
	if !strings.Contains(page.body, `<p class="notice" role="status">The item changed after the page showed it. Read it again; nothing was saved.</p>`) {
		t.Errorf("top notice: %s", page.body)
	}
}

// TestDecisionBoardPages walks the cards and the archive by their keyset links: every item
// appears once, and an insert during the walk does not repeat an item.
func TestDecisionBoardPages(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-board-pages", "")
	key := Key{"claude", peer.ID}
	total := 2*decisionBoardCards + 3
	for i := 0; i < total; i++ {
		f := decisionFields(fmt.Sprintf("Item %d", i), "decision")
		f.Order = ptr(int64(i % 7))
		mustCreate(t, d.store, key, fmt.Sprintf("K-%03d", i), f)
	}
	seen := map[string]int{}
	next, pages := "/dashboard/decisions", 0
	link := regexp.MustCompile(`<a href="(/dashboard/decisions\?cards=[^"]+)">Next items</a>`)
	for next != "" {
		page := dashboardDo(t, d, "GET", strings.ReplaceAll(next, "&amp;", "&"), client.cookie, nil, nil)
		for _, ref := range boardCards(page.body) {
			seen[ref]++
		}
		if pages++; pages == 1 {
			f := decisionFields("Inserted first", "decision")
			f.Order = ptr(int64(-1))
			mustCreate(t, d.store, key, "K-NEW", f)
		}
		next = ""
		if m := link.FindStringSubmatch(page.body); m != nil {
			next = m[1]
		}
	}
	if pages != 3 || len(seen) != total {
		t.Fatalf("pages %d, cards %d of %d", pages, len(seen), total)
	}
	for ref, n := range seen {
		if n != 1 {
			t.Errorf("%s shown %d times", ref, n)
		}
	}
	if bad := dashboardDo(t, d, "GET", "/dashboard/decisions?cards=9:x:bad%20ref", client.cookie, nil, nil); bad.status != 200 || len(boardCards(bad.body)) != decisionBoardCards {
		t.Errorf("an invalid cursor: %d", bad.status)
	}
}

// TestDecisionBoardStatusFilter filters the cards by the status count that the maintainer
// chooses (#411). The counts stay totals, "all" removes the filter, an unknown filter is
// refused, and the filter stays through the lookup, the page links and a verdict's redirect.
func TestDecisionBoardStatusFilter(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-board-filter", "")
	key := Key{"claude", peer.ID}
	create := func(reference string, order int64, kind string) {
		f := decisionFields("Item "+reference, kind)
		f.Order = &order
		mustCreate(t, d.store, key, reference, f)
	}
	create("D-1", 1, "decision")
	create("D-2", 2, "decision")
	create("F-1", 3, "fyi")
	create("P-1", 4, "decision")
	create("A-1", 5, "decision")
	if got := client.do("decision-approve", url.Values{"reference": {"P-1"}, "revision": {"1"}}); got != "decision_approved" {
		t.Fatalf("approve: %s", got)
	}
	if got := client.do("decision-decline", url.Values{"reference": {"A-1"}, "revision": {"1"}}); got != "decision_declined" {
		t.Fatalf("decline: %s", got)
	}
	counts := func(t *testing.T, body string, waiting, progress, fyi, archived int) {
		t.Helper()
		for _, want := range []string{fmt.Sprintf("<b>%d</b> waiting", waiting), fmt.Sprintf("<b>%d</b> in progress", progress),
			fmt.Sprintf("<b>%d</b> open fyi", fyi), fmt.Sprintf("<b>%d</b> archived", archived)} {
			if !strings.Contains(body, want) {
				t.Errorf("counts: no %q", want)
			}
		}
	}
	current := regexp.MustCompile(`<a href="(/dashboard/decisions[^"]*)" aria-current="true">`)
	archiveRow := `<td><a href="/dashboard/decisions?ref=A-1`
	for _, c := range []struct {
		query, cards, link string
		archive            bool
	}{
		{"", "D-1 D-2 F-1 P-1", "/dashboard/decisions", true},
		{"status=waiting", "D-1 D-2", "/dashboard/decisions?status=waiting", false},
		{"status=in_progress", "P-1", "/dashboard/decisions?status=in_progress", false},
		{"status=fyi", "F-1", "/dashboard/decisions?status=fyi", false},
		{"status=archived", "", "/dashboard/decisions?status=archived", true},
		// An empty status is all.
		{"status=", "D-1 D-2 F-1 P-1", "/dashboard/decisions", true},
	} {
		for _, path := range []string{"/dashboard/decisions?" + c.query, "/dashboard/decisions?fragment=1&" + c.query} {
			page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
			if page.status != 200 {
				t.Fatalf("%s: %d %s", path, page.status, page.body)
			}
			// Every filter shows the same totals.
			counts(t, page.body, 2, 1, 1, 1)
			if got := strings.Join(boardCards(page.body), " "); got != c.cards {
				t.Errorf("%s: cards %q, want %q", path, got, c.cards)
			}
			if m := current.FindAllStringSubmatch(page.body, -1); len(m) != 1 || m[0][1] != c.link {
				t.Errorf("%s: current filter %v, want %s", path, m, c.link)
			}
			if got := strings.Contains(page.body, "<h2>Archive</h2>") && strings.Contains(page.body, archiveRow); got != c.archive {
				t.Errorf("%s: archive shown %v", path, got)
			}
			if got := strings.Contains(page.body, "<h2>Items"); got == (c.query == "status=archived") {
				t.Errorf("%s: items heading shown %v", path, got)
			}
		}
	}
	if body := dashboardDo(t, d, "GET", "/dashboard/decisions?status=waiting", client.cookie, nil, nil).body; !strings.Contains(body, "<h2>Items: waiting</h2>") {
		t.Errorf("the filter is not named: %s", body)
	}
	if body := dashboardDo(t, d, "GET", "/dashboard/decisions", client.cookie, nil, nil).body; !strings.Contains(body, "<h2>Items</h2>") {
		t.Errorf("all items heading: %s", body)
	}

	// An unknown filter is refused with the dashboard's request refusal.
	for _, bad := range []string{"bogus", "Waiting", "active", "in%20progress", "waiting%00"} {
		if r := dashboardDo(t, d, "GET", "/dashboard/decisions?status="+bad, client.cookie, nil, nil); r.status != 400 || len(boardCards(r.body)) != 0 {
			t.Errorf("status=%s: %d", bad, r.status)
		}
	}

	// The lookup form, the reference links and the archive page links keep the filter.
	page := dashboardDo(t, d, "GET", "/dashboard/decisions?status=waiting&ref=D-2", client.cookie, nil, nil).body
	for _, want := range []string{`<input type="hidden" name="status" value="waiting">`, `<a href="/dashboard/decisions?ref=D-1&amp;status=waiting">D-1</a>`,
		`<section class="lookup-result">`, `<article class="card kind-decision lookup-card" id="lookup-D-2">`} {
		if !strings.Contains(page, want) {
			t.Errorf("lookup under a filter: no %q", want)
		}
	}
	if got := strings.Join(boardCards(page), " "); got != "D-1" {
		t.Errorf("lookup under a filter: cards %q", got)
	}
	for i := 0; i < decisionBoardArchive; i++ {
		ref := fmt.Sprintf("Z-%02d", i)
		mustCreate(t, d.store, key, ref, decisionFields("Dismissed "+ref, "fyi"))
		if got := client.do("decision-acknowledge", url.Values{"reference": {ref}, "revision": {"1"}}); got != "decision_acknowledged" {
			t.Fatalf("acknowledge %s: %s", ref, got)
		}
	}
	archived := dashboardDo(t, d, "GET", "/dashboard/decisions?status=archived", client.cookie, nil, nil).body
	more := regexp.MustCompile(`<a href="(/dashboard/decisions\?archived=[0-9]+&amp;status=archived)">More archived items</a>`).FindStringSubmatch(archived)
	if more == nil {
		t.Fatalf("no archive page link with the filter: %s", archived)
	}
	next := dashboardDo(t, d, "GET", strings.ReplaceAll(more[1], "&amp;", "&"), client.cookie, nil, nil)
	if next.status != 200 || !strings.Contains(next.body, `&amp;status=archived">`) || len(boardCards(next.body)) != 0 {
		t.Errorf("next archive page: %d %s", next.status, next.body)
	}

	// A verdict returns to the filter its form was on: the back field carries it.
	page = dashboardDo(t, d, "GET", "/dashboard/decisions?status=waiting", client.cookie, nil, nil).body
	if card := cardOf(t, page, "D-1"); !strings.Contains(card, `<input type="hidden" name="back" value="status=waiting">`) {
		t.Fatalf("no back field on the card: %s", card)
	}
	form := url.Values{"reference": {"D-1"}, "revision": {"1"}, "back": {"status=waiting"}, "csrf": {client.csrf}}
	r := dashboardDo(t, d, "POST", "/dashboard/actions/decision-approve", client.cookie, client.sameOrigin, form)
	location, err := url.Parse(r.header.Get("Location"))
	if r.status != 303 || err != nil {
		t.Fatalf("approve: %d %q", r.status, r.header.Get("Location"))
	}
	if q := location.Query(); q.Get("status") != "waiting" || q.Get("card") != "D-1" || q.Get("notice") != "decision_approved" {
		t.Fatalf("redirect: %s", location)
	}
	// The approved card leaves the waiting filter, so its notice shows at the top.
	after := dashboardDo(t, d, "GET", location.String(), client.cookie, nil, nil)
	if after.status != 200 || strings.Join(boardCards(after.body), " ") != "D-2" || !strings.Contains(after.body, `<p class="notice" role="status">`) {
		t.Errorf("after the verdict: %d %s", after.status, after.body)
	}
	counts(t, after.body, 1, 2, 1, 1+decisionBoardArchive)
	// A back field with a filter that the board does not know returns to all items.
	form = url.Values{"reference": {"D-2"}, "revision": {"1"}, "back": {"status=bogus"}, "csrf": {client.csrf}}
	r = dashboardDo(t, d, "POST", "/dashboard/actions/decision-decline", client.cookie, client.sameOrigin, form)
	if location, err = url.Parse(r.header.Get("Location")); err != nil || location.Query().Get("status") != "" || location.Query().Get("notice") != "decision_declined" {
		t.Fatalf("redirect with an unknown filter: %q", r.header.Get("Location"))
	}
	if after := dashboardDo(t, d, "GET", location.String(), client.cookie, nil, nil); after.status != 200 {
		t.Errorf("after an unknown filter: %d", after.status)
	}
}

// lookupSection returns the markup of the lookup result.
func lookupSection(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<section class="lookup-result">`)
	if start < 0 {
		t.Fatalf("no lookup result in %s", body)
	}
	return body[start : start+strings.Index(body[start:], "</section>")]
}

// TestDecisionLookupCard opens an item by reference (#414). A waiting, in-progress or open fyi
// item shows its board card with its actions, at full width above the grid, and an action
// from it returns to the same lookup, with its status filter and the notice on that card. An
// archived or deep-archived item stays read only, an unknown reference keeps its error, and
// the verdict history stays below the item.
func TestDecisionLookupCard(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-lookup-card", "")
	key := Key{"claude", peer.ID}
	create := func(reference string, order int64, kind string) {
		f := decisionFields("Item "+reference, kind)
		f.Order = &order
		f.Recipients = &[]string{peer.Name}
		mustCreate(t, d.store, key, reference, f)
	}
	create("D-1", 1, "decision")
	create("D-2", 2, "decision")
	create("F-1", 3, "fyi")
	create("P-1", 4, "decision")
	create("A-1", 5, "decision")
	if got := client.do("decision-approve", url.Values{"reference": {"P-1"}, "revision": {"1"}, "note": {"Go **ahead**."}}); got != "decision_approved" {
		t.Fatalf("approve: %s", got)
	}
	if got := client.do("decision-decline", url.Values{"reference": {"A-1"}, "revision": {"1"}}); got != "decision_declined" {
		t.Fatalf("decline: %s", got)
	}
	if _, err := fixtureExec(d.store, `INSERT INTO decision_deep_archive(reference,file,offset,length,moved_at) VALUES ('Z-1','synthetic.jsonl',0,1,1700000000000)`); err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		t.Helper()
		page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		if page.status != 200 {
			t.Fatalf("%s: %d %s", path, page.status, page.body)
		}
		return page.body
	}

	// Each active state shows its card with the same actions as in the grid, from a link and
	// from the Find form (the same query), and in the refresh fragment.
	for _, c := range []struct {
		reference, status, want string
		absent                  []string
	}{
		{"D-1", "waiting", `<button type="submit" formaction="/dashboard/actions/decision-approve">Approve</button> <button type="submit" formaction="/dashboard/actions/decision-decline">Decline</button> <button type="submit" formaction="/dashboard/actions/decision-feedback">Provide feedback</button>`,
			[]string{"decision-acknowledge", "decision-complete"}},
		{"F-1", "fyi", `<button type="submit" formaction="/dashboard/actions/decision-acknowledge">Acknowledge</button>`,
			[]string{"decision-approve", "decision-decline", "decision-feedback"}},
		{"P-1", "in_progress", "Go <strong>ahead</strong>.", []string{"<form", "Read only."}},
	} {
		for _, path := range []string{"/dashboard/decisions?ref=" + c.reference + "&status=" + c.status,
			"/dashboard/decisions?fragment=1&ref=" + c.reference + "&status=" + c.status} {
			body := get(path)
			section := lookupSection(t, body)
			if !strings.Contains(section, `<article class="card kind-`) || !strings.Contains(section, `lookup-card" id="lookup-`+c.reference+`">`) ||
				!strings.Contains(section, c.want) || !strings.Contains(section, "<h3>Item "+c.reference+"</h3>") ||
				!strings.Contains(section, "<h3>Verdict history</h3>") || strings.Contains(section, "<h2>"+c.reference+":") {
				t.Errorf("%s: lookup card: %s", path, section)
			}
			for _, absent := range c.absent {
				if strings.Contains(section, absent) {
					t.Errorf("%s: lookup shows %q", path, absent)
				}
			}
			if c.status != "in_progress" {
				for _, want := range []string{`<input type="hidden" name="csrf" value="` + client.csrf + `">`, `name="reference" value="` + c.reference + `"`,
					`<input type="hidden" name="back" value="ref=` + c.reference + `&amp;status=` + c.status + `">`, `<textarea name="note"`} {
					if !strings.Contains(section, want) {
						t.Errorf("%s: lookup form: no %q", path, want)
					}
				}
			}
			// The grid skips the looked-up card, so the item shows once with one form, and
			// shows it again without the lookup.
			if strings.Contains(body, `id="card-`+c.reference+`"`) || strings.Count(body, `name="reference" value="`+c.reference+`"`) > 1 {
				t.Errorf("%s: the grid shows the looked-up card", path)
			}
			without := get(strings.Replace(path, "ref="+c.reference+"&", "", 1))
			if !strings.Contains(without, `id="card-`+c.reference+`"`) || strings.Contains(without, `<section class="lookup-result">`) {
				t.Errorf("%s: the grid without the lookup has no card %s", path, c.reference)
			}
			if strings.Contains(path, "fragment=1") && strings.Contains(body, "<html") {
				t.Errorf("%s: the fragment is a whole page", path)
			}
		}
	}
	// A grid page whose only card is looked up names no empty filter.
	if body := get("/dashboard/decisions?ref=P-1&status=in_progress"); len(boardCards(body)) != 0 || strings.Contains(body, `<p class="empty">No in progress items.</p>`) {
		t.Errorf("only card looked up: %s", body)
	}
	// The approved item shows its verdict in the history.
	if section := lookupSection(t, get("/dashboard/decisions?ref=P-1")); !strings.Contains(section, "<strong>approved</strong> on revision 1") {
		t.Errorf("in-progress history: %s", section)
	}

	// An action from the lookup card returns to the lookup with its filter, and the notice
	// shows on the looked-up card only, once.
	post := func(action string, form url.Values) *url.URL {
		t.Helper()
		form.Set("csrf", client.csrf)
		r := dashboardDo(t, d, "POST", "/dashboard/actions/"+action, client.cookie, client.sameOrigin, form)
		location, err := url.Parse(r.header.Get("Location"))
		if r.status != 303 || err != nil {
			t.Fatalf("%s: %d %q", action, r.status, r.header.Get("Location"))
		}
		return location
	}
	// A stale revision on D-2, looked up with every item shown: the grid skips D-2.
	location := post("decision-approve", url.Values{"reference": {"D-2"}, "revision": {"9"}, "back": {"ref=D-2"}})
	if q := location.Query(); q.Get("ref") != "D-2" || q.Get("card") != "D-2" || q.Get("notice") != "stale_revision" || q.Get("status") != "" {
		t.Fatalf("refused redirect: %s", location)
	}
	body := get(location.String())
	section := lookupSection(t, body)
	if !strings.Contains(section, `<p class="card-notice refused" role="status">The item changed after the page showed it.`) || strings.Count(body, `class="card-notice`) != 1 ||
		strings.Contains(body, `<p class="notice"`) || !strings.Contains(section, "decision-approve") || strings.Contains(body, `id="card-D-2"`) {
		t.Errorf("refused notice: %s", body)
	}
	if got := strings.Join(boardCards(body), " "); got != "D-1 F-1 P-1" {
		t.Errorf("grid during the D-2 lookup: %q", got)
	}
	// Approve D-1 from its lookup under the waiting filter: the item is in progress, the
	// lookup shows its card with the notice, and the grid keeps the filter.
	location = post("decision-approve", url.Values{"reference": {"D-1"}, "revision": {"1"}, "back": {"ref=D-1&status=waiting"}})
	if q := location.Query(); q.Get("ref") != "D-1" || q.Get("status") != "waiting" || q.Get("card") != "D-1" || q.Get("notice") != "decision_approved" {
		t.Fatalf("approve redirect: %s", location)
	}
	body = get(location.String())
	section = lookupSection(t, body)
	if !strings.Contains(section, `id="lookup-D-1"`) || !strings.Contains(section, `<p class="card-notice" role="status">`) ||
		!strings.Contains(section, "in progress") || strings.Contains(section, "<form") || strings.Count(body, `class="card-notice`) != 1 ||
		strings.Contains(body, `<p class="notice"`) || !strings.Contains(section, "<strong>approved</strong> on revision 1") {
		t.Errorf("after approve: %s", body)
	}
	if got := strings.Join(boardCards(body), " "); got != "D-2" || !strings.Contains(body, `aria-current="true">`) || !strings.Contains(body, "<h2>Items: waiting</h2>") {
		t.Errorf("after approve: grid %q", got)
	}
	// Acknowledge F-1 from its lookup: the item is archived, so the lookup is read only and
	// carries the notice.
	location = post("decision-acknowledge", url.Values{"reference": {"F-1"}, "revision": {"1"}, "back": {"ref=F-1&status=fyi"}})
	if q := location.Query(); q.Get("ref") != "F-1" || q.Get("status") != "fyi" || q.Get("notice") != "decision_acknowledged" {
		t.Fatalf("acknowledge redirect: %s", location)
	}
	body = get(location.String())
	section = lookupSection(t, body)
	if !strings.Contains(section, "<h2>F-1: Item F-1</h2>") || !strings.Contains(section, "Read only.") || strings.Contains(section, "<form") ||
		strings.Contains(section, "lookup-card") || !strings.Contains(section, `<p class="card-notice" role="status">`) ||
		strings.Count(body, `class="card-notice`) != 1 || strings.Contains(body, `<p class="notice"`) || !strings.Contains(section, "<strong>acknowledged</strong> on revision 1") {
		t.Errorf("after acknowledge: %s", body)
	}

	// An archived item stays read only with its history; a deep-archived item names the
	// restore; an unknown or malformed reference keeps its error.
	section = lookupSection(t, get("/dashboard/decisions?ref=A-1"))
	if !strings.Contains(section, "<h2>A-1: Item A-1</h2>") || !strings.Contains(section, "decision, archived, revision 1") || !strings.Contains(section, "Read only.") ||
		strings.Contains(section, "<form") || strings.Contains(section, "lookup-card") || !strings.Contains(section, "<strong>declined</strong> on revision 1") {
		t.Errorf("archived lookup: %s", section)
	}
	section = lookupSection(t, get("/dashboard/decisions?ref=Z-1"))
	if !strings.Contains(section, "Z-1 moved to the deep archive at") || strings.Contains(section, "<form") || strings.Contains(section, "<article") {
		t.Errorf("deep-archived lookup: %s", section)
	}
	for ref, want := range map[string]string{"X-404": "no decision item has this reference", "bad%20ref": "not a valid reference"} {
		section = lookupSection(t, get("/dashboard/decisions?ref="+ref))
		if !strings.Contains(section, `<p class="empty">`+want+`</p>`) || strings.Contains(section, "<form") || strings.Contains(section, "<article") {
			t.Errorf("%s lookup: %s", ref, section)
		}
	}
}

// matchRow is one reference of a search's match list.
var matchRow = regexp.MustCompile(`<tr><td><a href="/dashboard/decisions\?ref=([A-Za-z0-9-]+)[^"]*">[^<]*</a></td><td>[^<]*</td><td>[^<]*</td><td>([^<]*)</td></tr>`)

func boardMatches(section string) []string {
	var rows []string
	for _, m := range matchRow.FindAllStringSubmatch(section, -1) {
		rows = append(rows, m[1]+"="+m[2])
	}
	return rows
}

// TestDecisionBoardSearch finds items by all or part of a reference, ignoring ASCII case and
// the spaces around it (#505). Only an exact reference opens the item; other matches list
// their references, deep-archived ones too, in pages. A Clear link ends the search and keeps
// the status filter, and a refresh and a verdict keep the search.
func TestDecisionBoardSearch(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	peer := join(t, d.store, "claude", "synthetic-search", "")
	key := Key{"claude", peer.ID}
	create := func(reference, kind string) {
		f := decisionFields("Item "+reference, kind)
		f.Recipients = &[]string{peer.Name}
		mustCreate(t, d.store, key, reference, f)
	}
	create("tools-4", "decision")
	create("tools-5", "fyi")
	create("Other-1", "decision")
	for i := 1; i <= decisionBoardMatches+2; i++ {
		create(fmt.Sprintf("page-%02d", i), "decision")
	}
	if _, err := fixtureExec(d.store, `INSERT INTO decision_deep_archive(reference,file,offset,length,moved_at) VALUES ('tools-old','synthetic.jsonl',0,1,1700000000000)`); err != nil {
		t.Fatal(err)
	}
	get := func(path string) string {
		t.Helper()
		page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		if page.status != 200 {
			t.Fatalf("%s: %d %s", path, page.status, page.body)
		}
		return page.body
	}

	// An exact reference opens the item, also with spaces around it.
	for _, ref := range []string{"tools-4", "%20tools-4%20"} {
		body := get("/dashboard/decisions?ref=" + ref)
		section := lookupSection(t, body)
		if !strings.Contains(section, `id="lookup-tools-4"`) || boardMatches(section) != nil || !strings.Contains(body, `name="ref" maxlength="32" value="tools-4"`) {
			t.Errorf("%s: exact lookup: %s", ref, section)
		}
	}
	// Part of a reference, in any case, lists the matches with their states, the deep
	// archive included; a case-different whole reference is a one-row list.
	for ref, want := range map[string]string{
		"tools":     "tools-4=waiting tools-5=open tools-old=deep archive",
		"TOOLS-":    "tools-4=waiting tools-5=open tools-old=deep archive",
		"%20ols%20": "tools-4=waiting tools-5=open tools-old=deep archive",
		"other-1":   "Other-1=waiting",
		"-5":        "tools-5=open",
	} {
		section := lookupSection(t, get("/dashboard/decisions?ref="+ref))
		if got := strings.Join(boardMatches(section), " "); got != want || strings.Contains(section, "<article") || strings.Contains(section, `class="empty"`) {
			t.Errorf("%s: matches %q, want %q: %s", ref, got, want, section)
		}
	}
	// No match keeps the message.
	if section := lookupSection(t, get("/dashboard/decisions?ref=zzz")); !strings.Contains(section, `<p class="empty">no decision item has this reference</p>`) || boardMatches(section) != nil {
		t.Errorf("no match: %s", section)
	}

	// The matches come in pages of decisionBoardMatches, in reference order.
	section := lookupSection(t, get("/dashboard/decisions?ref=page"))
	rows := boardMatches(section)
	more := regexp.MustCompile(`<a href="(/dashboard/decisions\?ref=page&amp;found=page-20)">More matches</a>`).FindStringSubmatch(section)
	if len(rows) != decisionBoardMatches || rows[0] != "page-01=waiting" || more == nil {
		t.Fatalf("first page: %q: %s", rows, section)
	}
	section = lookupSection(t, get(strings.ReplaceAll(more[1], "&amp;", "&")))
	if got := strings.Join(boardMatches(section), " "); got != "page-21=waiting page-22=waiting" || strings.Contains(section, "More matches") {
		t.Errorf("second page: %q: %s", got, section)
	}

	// The search keeps the status filter, and Clear returns to that filter without it. A
	// refresh fragment keeps the search.
	for _, path := range []string{"/dashboard/decisions?ref=tools&status=waiting", "/dashboard/decisions?fragment=1&ref=tools&status=waiting"} {
		body := get(path)
		for _, want := range []string{`<input type="hidden" name="status" value="waiting">`, `<a href="/dashboard/decisions?status=waiting">Clear</a>`,
			`<a href="/dashboard/decisions?ref=tools-5&amp;status=waiting">tools-5</a>`, `aria-current="true">`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: no %q in %s", path, want, body)
			}
		}
		if got := strings.Join(boardMatches(lookupSection(t, body)), " "); got != "tools-4=waiting tools-5=open tools-old=deep archive" {
			t.Errorf("%s: matches %q", path, got)
		}
	}
	if body := get("/dashboard/decisions?ref=tools"); !strings.Contains(body, `<a href="/dashboard/decisions">Clear</a>`) {
		t.Errorf("Clear without a filter: %s", body)
	}
	if body := get("/dashboard/decisions"); strings.Contains(body, ">Clear</a>") || strings.Contains(body, "lookup-result") {
		t.Errorf("Clear without a search: %s", body)
	}

	// A verdict from the grid returns to the search.
	form := url.Values{"reference": {"tools-4"}, "revision": {"1"}, "back": {"ref=tools&status=waiting"}, "csrf": {client.csrf}}
	r := dashboardDo(t, d, "POST", "/dashboard/actions/decision-approve", client.cookie, client.sameOrigin, form)
	location, err := url.Parse(r.header.Get("Location"))
	if r.status != 303 || err != nil || location.Query().Get("ref") != "tools" || location.Query().Get("status") != "waiting" {
		t.Fatalf("approve: %d %q", r.status, r.header.Get("Location"))
	}
	if got := strings.Join(boardMatches(lookupSection(t, get(location.String()))), " "); got != "tools-4=in progress tools-5=open tools-old=deep archive" {
		t.Errorf("after approve: matches %q", got)
	}
}
