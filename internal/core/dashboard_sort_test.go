package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Sort tests (#222): every sortable column in both directions, paged to the end with no
// repeat or omission, and the request handling of sort keys, directions and cursors.

// pageAll reads every page of one order; list returns a page and its next cursor.
func pageAll[T any](t *testing.T, spec *sortSpec, key, dir string, list func(dashboardSort) ([]T, string, error)) []T {
	t.Helper()
	var all []T
	after := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatalf("%s %s: no end of pages", key, dir)
		}
		page, next, err := list(sortOrder(t, spec, key, dir, after))
		if err != nil {
			t.Fatalf("%s %s: %v", key, dir, err)
		}
		all = append(all, page...)
		if next == "" {
			return all
		}
		after = next
	}
}

// checkOrder requires each row's key to follow the previous one strictly, so the order is
// total and no row repeats, and requires want rows.
func checkOrder[T any](t *testing.T, label, dir string, rows []T, want int, key func(T) []any) {
	t.Helper()
	if len(rows) != want {
		t.Fatalf("%s %s: listed %d of %d", label, dir, len(rows), want)
	}
	for i := 1; i < len(rows); i++ {
		c := compareValues(key(rows[i-1]), key(rows[i]))
		if (dir == "asc" && c >= 0) || (dir == "desc" && c <= 0) {
			t.Fatalf("%s %s: row %d %v after %v", label, dir, i, key(rows[i]), key(rows[i-1]))
		}
	}
}

func TestDashboardSessionSortEveryColumn(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	repos := []string{namedRepo(t, "sort-b"), namedRepo(t, "sort-a"), ""}
	want := 0
	for i := range 2*dashboardSessionPage + 7 {
		family, id := families[i%len(families)], fmt.Sprintf("synthetic-%03d", (i*37)%1000)
		session := join(t, s, family, id, repos[i%len(repos)])
		want++
		if i%9 == 0 {
			if _, err := s.Mutate(ctx, Mutation{Family: family, ID: id, IfRevision: session.Revision}, true); err != nil {
				t.Fatal(err)
			}
		}
		// Equal times in runs of three, so the tie-breaker orders them.
		if i%3 == 2 {
			clock = clock.Add(time.Second)
		}
		// The first half expires.
		if i == dashboardSessionPage {
			clock = clock.Add(2 * time.Minute)
		}
	}
	keys := map[string]func(Session) any{
		"family": func(r Session) any { return r.Family }, "name": func(r Session) any { return r.Name },
		"state": func(r Session) any { return r.State }, "registered": func(r Session) any { return r.RegisteredAt },
		"renewed": func(r Session) any { return r.RenewedAt }, "expires": func(r Session) any { return r.ExpiresAt },
	}
	states := map[string]bool{}
	for _, c := range sessionSort.columns {
		for _, dir := range []string{"asc", "desc"} {
			rows := pageAll(t, &sessionSort, c.key, dir, func(o dashboardSort) ([]Session, string, error) { return s.dashboardSessions(ctx, o, "") })
			checkOrder(t, "sessions "+c.key, dir, rows, want, func(r Session) []any {
				states[r.State] = true
				switch c.key {
				case "repository":
					return []any{r.Repository, r.Directory, r.Family, r.ID}
				case "state":
					return []any{r.State, -r.RenewedAt, r.Family, r.ID}
				}
				return []any{keys[c.key](r), r.Family, r.ID}
			})
		}
	}
	if len(states) != 3 {
		t.Fatalf("states: %v", states)
	}
}

func TestDashboardMessageSortEveryColumn(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	repo := namedRepo(t, "sortmessages")
	var names []string
	for _, id := range []string{"synthetic-p", "synthetic-q", "synthetic-r"} {
		names = append(names, join(t, s, "claude", id, repo).Name)
	}
	senders := []Key{{"codex", "synthetic-s1"}, {"agy", "synthetic-s2"}}
	for _, k := range senders {
		join(t, s, k.Family, k.ID, repo)
	}
	want := 0
	for i := range 2*dashboardMessagePage + 9 {
		sent, err := s.Send(ctx, senders[i%2], names[i%3], fmt.Sprintf("synthetic body %d", i))
		if err != nil {
			t.Fatal(err)
		}
		want++
		if i%4 == 0 {
			if err := s.SetDelivery(ctx, sent.ID, "failed", "synthetic_unreachable"); err != nil {
				t.Fatal(err)
			}
		}
		if i%5 == 4 {
			clock = clock.Add(time.Second)
		}
	}
	if _, err := s.Ack(ctx, Key{"claude", "synthetic-q"}, 10); err != nil {
		t.Fatal(err)
	}
	keys := map[string]func(MessageRecord) any{
		"id": func(m MessageRecord) any { return m.ID }, "to": func(m MessageRecord) any { return m.RecipientName },
		"seq": func(m MessageRecord) any { return m.Seq }, "from": func(m MessageRecord) any { return m.SenderName },
		"sent": func(m MessageRecord) any { return m.CreatedAt }, "delivery": func(m MessageRecord) any { return m.DeliveryState },
		"acknowledged": func(m MessageRecord) any {
			if m.Acknowledged {
				return int64(1)
			}
			return int64(0)
		},
	}
	for _, c := range messageSort.columns {
		for _, dir := range []string{"asc", "desc"} {
			rows := pageAll(t, &messageSort, c.key, dir, func(o dashboardSort) ([]MessageRecord, string, error) {
				return s.dashboardMessages(ctx, nil, o)
			})
			checkOrder(t, "messages "+c.key, dir, rows, want, func(m MessageRecord) []any { return []any{keys[c.key](m), m.ID} })
		}
	}
	// A recipient's inbox keeps its filter under a sort.
	k, err := s.sessionByName(ctx, names[0])
	if err != nil {
		t.Fatal(err)
	}
	rows := pageAll(t, &messageSort, "from", "asc", func(o dashboardSort) ([]MessageRecord, string, error) {
		return s.dashboardMessages(ctx, k, o)
	})
	checkOrder(t, "inbox from", "asc", rows, (want+2)/3, func(m MessageRecord) []any {
		if m.RecipientName != names[0] {
			t.Fatalf("filter leaked %s", m.RecipientName)
		}
		return []any{m.SenderName, m.ID}
	})
}

func TestDashboardAuditSortEveryColumn(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	want := 2*auditPage + 11
	for i := range want {
		if _, err := s.db.Exec(`INSERT INTO audit(at,action,target,result) VALUES (?,?,?,?)`, 1_800_000_000_000+int64(i/4),
			[]string{"retire", "send", "launch"}[i%3], fmt.Sprintf("synthetic-%d", i%7), []string{"accepted", "refused"}[i%2]); err != nil {
			t.Fatal(err)
		}
	}
	keys := map[string]func(AuditRecord) any{
		"id": func(r AuditRecord) any { return r.ID }, "time": func(r AuditRecord) any { return r.At },
		"action": func(r AuditRecord) any { return r.Action }, "target": func(r AuditRecord) any { return r.Target },
		"result": func(r AuditRecord) any { return r.Result },
	}
	for _, c := range auditSort.columns {
		for _, dir := range []string{"asc", "desc"} {
			rows := pageAll(t, &auditSort, c.key, dir, func(o dashboardSort) ([]AuditRecord, string, error) { return s.dashboardAudit(ctx, o) })
			checkOrder(t, "audit "+c.key, dir, rows, want, func(r AuditRecord) []any { return []any{keys[c.key](r), r.ID} })
		}
	}
}

func TestDashboardStoreAndWorkSortEveryColumn(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	stores := dashboardStorePage + 6
	for i := range stores {
		m := MemoryCaller{Repository: fmt.Sprintf("/synthetic/sort-%02d/.git", (i*5)%stores), Family: "codex", Name: "synthetic-sorter",
			Consumer: "codex:synthetic-sorter"}
		for j := range 1 + i%3 {
			create(t, s, m, fmt.Sprintf("synthetic %d", j))
		}
	}
	keys := map[string]func(StoreSummary) any{
		"repository": func(r StoreSummary) any { return r.Repository }, "store": func(r StoreSummary) any { return r.StoreID },
		"head": func(r StoreSummary) any { return r.Head }, "entries": func(r StoreSummary) any { return r.Usage.Entries },
		"logical": func(r StoreSummary) any { return r.Usage.Logical }, "consumers": func(r StoreSummary) any { return r.Usage.Consumers },
		"debt": func(r StoreSummary) any { return r.OverdueDebt + r.EndDebt },
	}
	for _, c := range storeSort.columns {
		for _, dir := range []string{"asc", "desc"} {
			rows := pageAll(t, &storeSort, c.key, dir, func(o dashboardSort) ([]StoreSummary, string, error) { return s.dashboardMemory(ctx, o) })
			checkOrder(t, "memory "+c.key, dir, rows, stores, func(r StoreSummary) []any {
				if r.Maintenance == nil {
					t.Fatal("no maintenance status")
				}
				return []any{keys[c.key](r), r.Repository}
			})
		}
	}

	// Work rows sort within their store.
	m := MemoryCaller{Repository: "/synthetic/sort-work/.git", Family: "codex", Name: "synthetic-sorter", Consumer: "codex:synthetic-sorter"}
	for i, title := range []string{"delta", "alpha", "charlie", "bravo", "echo"} {
		id := create(t, s, m, title)
		if i%2 == 0 {
			mustStart(t, s, m, id, map[string]any{"progress_deadline": now(s) + float64(600*(i+1))})
		}
		if i == 1 {
			mustWork(t, s, m, "work-propose", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "proposed_assignee": "synthetic-peer",
				"key": key(), "deadline": now(s) + 600})
		}
	}
	text := func(p *string) any {
		if p == nil {
			return ""
		}
		return *p
	}
	work := map[string]func(WorkView) any{
		"work": func(v WorkView) any { return v.WorkID }, "title": func(v WorkView) any { return v.Title },
		"lifecycle": func(v WorkView) any { return v.Lifecycle }, "proposed": func(v WorkView) any { return text(v.ProposedAssignee) },
		"owner": func(v WorkView) any {
			if v.CurrentClaim == nil {
				return ""
			}
			return v.CurrentClaim.Consumer
		},
		"lease": func(v WorkView) any {
			if v.CurrentClaim == nil || !v.LeaseValid {
				return 0.0
			}
			return v.CurrentClaim.ExpiresAt
		},
		"progress": func(v WorkView) any {
			if v.ProgressDeadline == nil {
				return 0.0
			}
			return *v.ProgressDeadline
		},
	}
	for _, c := range workSort.columns {
		for _, dir := range []string{"asc", "desc"} {
			list, _, err := s.dashboardWork(ctx, "/synthetic/sort-vv", sortOrder(t, &workSort, c.key, dir, ""))
			if err != nil || len(list) != 1 || list[0].Repository != m.Repository {
				t.Fatalf("work stores: %v %v", list, err)
			}
			checkOrder(t, "work "+c.key, dir, list[0].Work, 5, func(v WorkView) []any { return []any{work[c.key](v), v.WorkID} })
		}
	}
}

func TestDashboardSortRequests(t *testing.T) {
	// An unknown column gives the default order and its direction; a direction applies
	// only to a known column.
	for _, q := range []string{"sort=name;DROP+TABLE+sessions&dir=asc", "sort=Name", "sort=", "dir=asc"} {
		values, _ := url.ParseQuery(q)
		order, err := parseSort(&sessionSort, "/dashboard/sessions", values)
		if err != nil || order.Key != "state" || order.Desc {
			t.Fatalf("%s: %+v %v", q, order, err)
		}
		if where, _, by := order.sql(); where != "1" || by != "state ASC,-renewed_at ASC,family ASC,id ASC" {
			t.Fatalf("%s: %s %s", q, where, by)
		}
	}
	values, _ := url.ParseQuery("sort=family&dir=sideways")
	if order, err := parseSort(&sessionSort, "/dashboard/sessions", values); err != nil || order.Key != "family" || order.Desc {
		t.Fatalf("unknown direction: %+v %v", order, err)
	}
	order := sortOrder(t, &messageSort, "", "", "")
	if order.Key != "id" || !order.Desc {
		t.Fatalf("message default: %+v", order)
	}
	order = sortOrder(t, &messageSort, "from", "", "")
	if order.Key != "from" || order.Desc {
		t.Fatalf("first direction: %+v", order)
	}
	// A cursor must match the column's terms: count and types.
	good := order.cursor([]any{"synthetic-name", int64(7)})
	if got := sortOrder(t, &messageSort, "from", "asc", good); compareValues(got.after, []any{"synthetic-name", int64(7)}) != 0 {
		t.Fatalf("cursor: %v", got.after)
	}
	for _, bad := range []string{"x", good + "x", order.cursor([]any{int64(7), int64(7)}), order.cursor([]any{"a"}),
		order.cursor([]any{"a", int64(1), int64(2)}), order.cursor([]any{"a", 1.5}), order.cursor([]any{"a", nil}),
		rawCursor(`["synthetic",1]]`), rawCursor(`["synthetic",1]}`), rawCursor(`["synthetic",1] 2`), rawCursor(`["synthetic",1][]`)} {
		if _, err := parseSort(&messageSort, "", url.Values{"sort": {"from"}, "after": {bad}}); err != ErrInvalid {
			t.Fatalf("cursor %q: %v", bad, err)
		}
	}
	// A keyset condition binds its values as arguments.
	where, args, by := sortOrder(t, &messageSort, "from", "desc", order.cursor([]any{"a' OR 1=1", int64(3)})).sql()
	if where != "(sender_name,id)<(?,?)" || len(args) != 2 || args[0] != "a' OR 1=1" || by != "sender_name DESC,id DESC" {
		t.Fatalf("keyset: %s %v %s", where, args, by)
	}
}

func TestDashboardSortLinks(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	repo := namedRepo(t, "sortlinks")
	a := join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	for i := range dashboardMessagePage + 3 {
		if _, err := s.Send(ctx, Key{"codex", "synthetic-a"}, b.Name, fmt.Sprintf("synthetic %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	cookie := dashboardLogin(t, d, secret)
	page := func(path string) string {
		t.Helper()
		r := dashboardDo(t, d, "GET", path, cookie, nil, nil)
		if r.status != 200 {
			t.Fatalf("%s: %d", path, r.status)
		}
		return r.body
	}
	href := func(body, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(`href="(` + pattern + `[^"]*)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no link %s in:\n%s", pattern, body)
		}
		return strings.ReplaceAll(m[1], "&amp;", "&")
	}
	// The default column shows its direction; the others link to their first direction.
	sessions := page("/dashboard/sessions")
	for _, want := range []string{`<th aria-sort="ascending"><a href="/dashboard/sessions?dir=desc&amp;sort=state">State</a> ▲</th>`,
		`<th><a href="/dashboard/sessions?dir=asc&amp;sort=family">Family</a></th>`,
		`<th><a href="/dashboard/sessions?dir=asc&amp;sort=name">Name</a></th>`, `<p class="counts">2 active, 0 expired, 0 retired</p>`,
		`<th><a href="/dashboard/sessions?dir=desc&amp;sort=registered">Registered</a></th>`, `<th>Model</th>`} {
		if !strings.Contains(sessions, want) {
			t.Fatalf("missing %s", want)
		}
	}
	// A second click reverses the order.
	byName := page("/dashboard/sessions?sort=name&dir=asc")
	if !strings.Contains(byName, `<th aria-sort="ascending"><a href="/dashboard/sessions?dir=desc&amp;sort=name">Name</a> ▲</th>`) ||
		strings.Index(byName, a.Name) > strings.Index(byName, b.Name) != (a.Name > b.Name) {
		t.Fatal("name order")
	}
	reversed := page(href(byName, `/dashboard/sessions\?dir=desc&amp;sort=name`))
	if !strings.Contains(reversed, `aria-sort="descending"`) || strings.Index(reversed, a.Name) < strings.Index(reversed, b.Name) != (a.Name > b.Name) {
		t.Fatal("reversed name order")
	}
	// An unknown column is ignored.
	if body := page("/dashboard/sessions?sort=directory&dir=asc"); !strings.Contains(body, `<th aria-sort="ascending"><a href="/dashboard/sessions?dir=desc&amp;sort=state">`) {
		t.Fatal("unknown column not ignored")
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/audit?after=bm90LWpzb24", cookie, nil, nil); r.status != 400 {
		t.Fatalf("bad cursor: %d", r.status)
	}
	// The recipient filter, the sort and the next page combine in links, and the filter
	// form keeps the sort.
	filtered := page("/dashboard/messages?to=" + b.Name + "&sort=sent&dir=asc")
	if !strings.Contains(filtered, `<input type="hidden" name="sort" value="sent"><input type="hidden" name="dir" value="asc">`) ||
		!strings.Contains(filtered, `href="/dashboard/messages?dir=desc&amp;sort=sent&amp;to=`+b.Name+`"`) {
		t.Fatalf("filter and sort links:\n%s", filtered)
	}
	next := href(filtered, `/dashboard/messages\?after=`)
	if !strings.Contains(next, "dir=asc") || !strings.Contains(next, "sort=sent") || !strings.Contains(next, "to="+b.Name) {
		t.Fatalf("next link: %s", next)
	}
	second := page(next)
	if !strings.Contains(second, "synthetic 52") || strings.Contains(second, "synthetic 0<") {
		t.Fatalf("second page:\n%s", second)
	}
	// The fragment for the 5-second refresh keeps the sort.
	if fragment := page("/dashboard/messages?sort=sent&dir=asc&fragment=1"); !strings.Contains(fragment, `aria-sort="ascending"`) {
		t.Fatal("fragment lost the sort")
	}
}

// TestDashboardActiveSessionsFirst: more expired sessions than a page holds, all with keys
// that sort before the active ones, leave the active sessions on the default first page (#227).
func TestDashboardActiveSessionsFirst(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	for i := range dashboardSessionPage + 20 {
		join(t, s, "agy", fmt.Sprintf("synthetic-%04d", i), "")
	}
	clock = clock.Add(time.Hour)
	join(t, s, "codex", "synthetic-older", "")
	clock = clock.Add(time.Second)
	join(t, s, "codex", "synthetic-newer", "")
	page, next, err := s.dashboardSessions(ctx, sortOrder(t, &sessionSort, "", "", ""), "")
	if err != nil || len(page) != dashboardSessionPage || next == "" {
		t.Fatalf("page of %d, next %q: %v", len(page), next, err)
	}
	if page[0].ID != "synthetic-newer" || page[1].ID != "synthetic-older" || page[0].State != "active" || page[2].State != "expired" {
		t.Fatalf("first rows: %v %v %v", page[0], page[1], page[2])
	}
	counts, err := s.Counts(ctx)
	if err != nil || counts["active"] != 2 || counts["expired"] != dashboardSessionPage+20 {
		t.Fatalf("counts %v %v", counts, err)
	}
}

// rawCursor encodes cursor text as a request carries it, for malformed cursors.
func rawCursor(text string) string { return base64.RawURLEncoding.EncodeToString([]byte(text)) }
