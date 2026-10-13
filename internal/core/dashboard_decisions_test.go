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
	create("F-1", 5, actionForYou+": restart the synthetic service", "fyi", DecisionFields{})
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
	if _, err := d.store.db.Exec(`UPDATE decision_items SET commands=? WHERE reference='D-2'`, `["echo ok","echo\tbad"]`); err != nil {
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
	for _, want := range []string{"<b>3</b> waiting", "<b>1</b> in progress", "<b>2</b> open fyi", "<b>1</b> archived"} {
		if !strings.Contains(body, want) {
			t.Errorf("counts: no %q", want)
		}
	}
	if got := strings.Join(boardCards(body), " "); got != "F-1 F-2 D-1 D-2 P-1 U-1" {
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
	if !strings.Contains(f1, "decision-dismiss") || strings.Contains(f1, "decision-approve") || strings.Contains(f1, "decision-decline") {
		t.Errorf("fyi card: %s", f1)
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
	for _, want := range []string{peer.Name, "Synthetic claimed work", "?ref=D-1", "?ref=D-2", "not reported"} {
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

	// Lookup is read-only and shows the history with its snapshots.
	if r := client.do("decision-approve", url.Values{"reference": {"D-1"}, "revision": {"1"}}); r != "decision_approved" {
		t.Fatalf("approve D-1: %s", r)
	}
	if _, err := d.store.DecisionEdit(context.Background(), key, "D-1", 1, DecisionFields{Context: ptr("New facts")}); err != nil {
		t.Fatal(err)
	}
	lookup := dashboardDo(t, d, "GET", "/dashboard/decisions?ref=D-1", client.cookie, nil, nil).body
	section := lookup[strings.Index(lookup, `<section class="lookup-result">`):strings.Index(lookup, "</section>")]
	if strings.Contains(section, "<form") || !strings.Contains(section, "Verdict history") || !strings.Contains(section, "<strong>approved</strong> on revision 1") ||
		!strings.Contains(section, "superseded") || !strings.Contains(section, "Plain <strong>bold</strong> line") || !strings.Contains(section, "New facts") {
		t.Errorf("lookup: %s", section)
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
