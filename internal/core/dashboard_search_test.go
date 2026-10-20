package core

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"
	"testing"
)

// Search and suggestion tests (#221, #223).

func TestDashboardSessionSearch(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	percent, other := namedRepo(t, "pct%dir"), namedRepo(t, "pctXdir")
	under, otherUnder := namedRepo(t, "us_r"), namedRepo(t, "usXr")
	held := join(t, s, "codex", "synthetic-held", percent)
	join(t, s, "codex", "synthetic-other", other)
	join(t, s, "agy", "synthetic-under", under)
	join(t, s, "agy", "synthetic-other-under", otherUnder)
	retired := join(t, s, "opencode", "synthetic-retired", "")
	if _, err := s.Mutate(ctx, Mutation{Family: retired.Family, ID: retired.ID, IfRevision: retired.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if held.Alias == "" {
		t.Fatalf("no alias held: %+v", held)
	}
	search := func(q string) []string {
		t.Helper()
		page, next, err := s.dashboardSessions(ctx, sortOrder(t, &sessionSort, "name", "asc", ""), q, "")
		if err != nil || next != "" {
			t.Fatalf("search %q: %v %q", q, err, next)
		}
		ids := []string{}
		for _, r := range page {
			ids = append(ids, r.ID)
		}
		return ids
	}
	for _, c := range []struct{ q, want string }{
		{held.Name, "synthetic-held"},                                       // peer name
		{strings.ToUpper(held.Alias), "synthetic-held"},                     // alias, any case
		{"OPENCODE", "synthetic-retired"},                                   // family
		{"pct%dir", "synthetic-held"},                                       // repository and directory; % is literal
		{"t%d", "synthetic-held"},                                           // % is not a wildcard
		{"s_r", "synthetic-under"},                                          // _ is not a wildcard
		{"retired", "synthetic-retired"},                                    // state
		{retired.Directory[len(retired.Directory)-6:], "synthetic-retired"}, // directory only
	} {
		if got := search(c.q); strings.Join(got, ",") != c.want {
			t.Fatalf("search %q: %v, want %s", c.q, got, c.want)
		}
	}
	if got := search("no-such-session"); len(got) != 0 {
		t.Fatalf("no match: %v", got)
	}
	if _, _, err := s.dashboardSessions(ctx, sortOrder(t, &sessionSort, "", "", ""), strings.Repeat("x", dashboardSearchMax+1), ""); err != ErrInvalid {
		t.Fatalf("long search: %v", err)
	}
}

func TestDashboardSessionSearchPaging(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	match, miss := namedRepo(t, "search-match"), namedRepo(t, "search-miss")
	want := 0
	for i := range 2*dashboardSessionPage + 5 {
		repo := miss
		if i%3 != 0 {
			repo, want = match, want+1
		}
		join(t, s, []string{"codex", "agy"}[i%2], fmt.Sprintf("synthetic-%04d", i), repo)
	}
	for _, dir := range []string{"asc", "desc"} {
		rows := pageAll(t, &sessionSort, "name", dir, func(o dashboardSort) ([]Session, string, error) {
			return s.dashboardSessions(ctx, o, "search-match", "")
		})
		checkOrder(t, "search by name", dir, rows, want, func(r Session) []any { return []any{r.Name, r.Family, r.ID} })
		for _, r := range rows {
			if r.Directory != match {
				t.Fatalf("non-matching row: %+v", r)
			}
		}
	}
}

func TestDashboardSearchAndSuggestionPages(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	repo := namedRepo(t, "suggest")
	active := join(t, s, "codex", "synthetic-active", repo)
	expired := join(t, s, "agy", "synthetic-expired", "")
	// A recent expiry: retention deletes sessions that expired more than 30 days ago.
	if _, err := fixtureExecContext(ctx, s, `UPDATE sessions SET expires_at=? WHERE family=? AND id=?`, s.now().UnixMilli()-1000, expired.Family, expired.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Cull(ctx); err != nil {
		t.Fatal(err)
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
	// The search form sits outside the refreshed list, keeps the order and carries the query.
	found := page("/dashboard/sessions?q=" + active.Name + "&sort=name&dir=desc")
	if !strings.Contains(found, `name="q" value="`+active.Name+`"`) || !strings.Contains(found, `<input type="hidden" name="sort" value="name">`) ||
		!strings.Contains(found, active.Name) || strings.Contains(found, expired.Name) {
		t.Fatalf("search page:\n%s", found)
	}
	if list := page("/dashboard/sessions?q=" + active.Name + "&fragment=1"); strings.Contains(list, `name="q"`) || !strings.Contains(list, active.Name) {
		t.Fatalf("fragment:\n%s", list)
	}
	if none := page("/dashboard/sessions?q=%25none%25"); !strings.Contains(none, "No session matches “%none%”.") {
		t.Fatalf("no match:\n%s", none)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions?q="+strings.Repeat("x", dashboardSearchMax+1), cookie, nil, nil); r.status != 400 {
		t.Fatalf("long search: %d", r.status)
	}
	// The send form suggests active names and held aliases, with family and state only.
	options := func(body string) []string {
		m := regexp.MustCompile(`(?s)<datalist id="peer-names">(.*?)</datalist>`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("no datalist:\n%s", body)
		}
		return regexp.MustCompile(`<option value="[^"]*">[^<]*</option>`).FindAllString(m[1], -1)
	}
	messages := page("/dashboard/messages")
	want := []string{
		`<option value="` + active.Name + `">codex, active</option>`,
		`<option value="` + active.Alias + `">alias of ` + active.Name + `, codex, active</option>`,
	}
	if got := options(messages); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("active options: %v", got)
	}
	all := page("/dashboard/messages?peers=all")
	got := options(all)
	if len(got) != 3 || got[2] != `<option value="`+expired.Name+`">agy, expired</option>` || !strings.Contains(all, "Active sessions only") {
		t.Fatalf("all options: %v", got)
	}
	for _, private := range []string{active.ID, expired.ID, active.Directory, expired.Directory} {
		if m := regexp.MustCompile(`(?s)<datalist.*</datalist>`).FindString(all); strings.Contains(m, private) {
			t.Fatalf("datalist holds %q", private)
		}
	}
	// The recipient filter resolves a held alias to its holder, as a send does.
	sender := join(t, s, "agy", "synthetic-sender", "")
	if _, err := s.Send(ctx, Key{sender.Family, sender.ID}, active.Name, "synthetic alias filter body"); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{active.Name, active.Alias} {
		if body := page("/dashboard/messages?to=" + to); !strings.Contains(body, "synthetic alias filter body") || !strings.Contains(body, "Inbox of "+to) {
			t.Fatalf("filter %s:\n%s", to, body)
		}
	}
	// The recipient filter keeps the suggestion choice and the order.
	if !strings.Contains(all, `<input type="hidden" name="peers" value="all">`) || !strings.Contains(messages, `list="peer-names"`) {
		t.Fatal("filter form")
	}
}

// TestDashboardPeerOptionsBound: more expired sessions than the bound, all with keys that sort
// before the active one, never push the active session out of the suggestions.
func TestDashboardPeerOptionsBound(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range dashboardPeerOptionMax + 1 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions (family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,revision)
			VALUES ('agy',?,'','/synthetic','{}',1,1,2,1)`, fmt.Sprintf("synthetic-%04d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	active := join(t, s, "codex", "synthetic-active", "")
	options, err := s.dashboardPeerOptions(ctx, false)
	if err != nil || len(options) != 1 || options[0].Name != active.Name {
		t.Fatalf("active options: %v %v", options, err)
	}
	options, err = s.dashboardPeerOptions(ctx, true)
	if err != nil || len(options) != dashboardPeerOptionMax || options[0].Name != active.Name || options[1].Label != "agy, expired" {
		t.Fatalf("all options: %d %v %v", len(options), options[:2], err)
	}
}

// The sessions view's status filter keeps only the sessions in one state, with the search
// and across pages; an unknown status is invalid (#429).
func TestDashboardSessionStatusFilter(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	match := namedRepo(t, "status-match")
	retired := 0
	for i := range 2*dashboardSessionPage + 5 {
		r := join(t, s, []string{"codex", "agy"}[i%2], fmt.Sprintf("synthetic-%04d", i), match)
		if i%3 == 0 {
			if _, err := s.Mutate(ctx, Mutation{Family: r.Family, ID: r.ID, IfRevision: r.Revision}, true); err != nil {
				t.Fatal(err)
			}
			retired++
		}
	}
	other := join(t, s, "opencode", "synthetic-other", namedRepo(t, "status-miss"))
	for _, c := range []struct {
		state, search string
		want          int
	}{
		{"retired", "", retired},
		{"active", "", 2*dashboardSessionPage + 5 - retired + 1},
		{"active", "status-match", 2*dashboardSessionPage + 5 - retired},
		{"expired", "", 0},
		{"", "", 2*dashboardSessionPage + 5 + 1},
	} {
		for _, dir := range []string{"asc", "desc"} {
			rows := pageAll(t, &sessionSort, "name", dir, func(o dashboardSort) ([]Session, string, error) {
				return s.dashboardSessions(ctx, o, c.search, c.state)
			})
			checkOrder(t, "status "+c.state, dir, rows, c.want, func(r Session) []any { return []any{r.Name, r.Family, r.ID} })
			for _, r := range rows {
				if c.state != "" && r.State != c.state || c.search != "" && r.ID == other.ID {
					t.Fatalf("status %q search %q: row %+v", c.state, c.search, r)
				}
			}
		}
	}
	for _, state := range []string{"bogus", "Active", "purge-marked"} {
		if _, _, err := s.dashboardSessions(ctx, sortOrder(t, &sessionSort, "", "", ""), "", state); err != ErrInvalid {
			t.Fatalf("status %q: %v", state, err)
		}
	}
}

// The sessions page shows a button with the count for each status; the chosen one filters
// the list and is kept by the sort links, the search form and the action forms' back field.
// The list offers a box that selects all sessions on the page (#429).
func TestDashboardSessionStatusPage(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	active := join(t, s, "codex", "synthetic-active", "")
	expired := join(t, s, "agy", "synthetic-expired", "")
	if _, err := fixtureExecContext(ctx, s, `UPDATE sessions SET expires_at=? WHERE family=? AND id=?`, s.now().UnixMilli()-1000, expired.Family, expired.ID); err != nil {
		t.Fatal(err)
	}
	retired := join(t, s, "opencode", "synthetic-retired", "")
	if _, err := s.Mutate(ctx, Mutation{Family: retired.Family, ID: retired.ID, IfRevision: retired.Revision}, true); err != nil {
		t.Fatal(err)
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
	button := func(query, label string, current bool) string {
		b := `<a href="/dashboard/sessions?` + query + `"`
		if current {
			b += ` aria-current="true"`
		}
		return b + `>` + label + `</a>`
	}
	all := page("/dashboard/sessions")
	for _, want := range []string{button("dir=asc&amp;sort=state", "<b>3</b> all", true),
		button("dir=asc&amp;sort=state&amp;status=active", "<b>1</b> active", false),
		button("dir=asc&amp;sort=state&amp;status=expired", "<b>1</b> expired", false),
		button("dir=asc&amp;sort=state&amp;status=retired", "<b>1</b> retired", false),
		`<input type="checkbox" data-select-all aria-controls="sessions"> Select all sessions on this page`, `<table id="sessions">`} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %s in:\n%s", want, all)
		}
	}
	for _, name := range []string{active.Name, expired.Name, retired.Name} {
		if !strings.Contains(all, name) {
			t.Fatalf("all: no %s", name)
		}
	}
	// The counts stay totals under a filter; the sort and the search keep the filter.
	filtered := page("/dashboard/sessions?status=expired&sort=name&dir=desc")
	for _, want := range []string{button("dir=desc&amp;sort=name", "<b>3</b> all", false),
		button("dir=desc&amp;sort=name&amp;status=expired", "<b>1</b> expired", true),
		button("dir=desc&amp;sort=name&amp;status=active", "<b>1</b> active", false),
		`<a href="/dashboard/sessions?dir=asc&amp;sort=family&amp;status=expired">Family</a>`,
		`<input type="hidden" name="status" value="expired">`,
		`name="back" value="` + html.EscapeString("dir=desc&sort=name&status=expired") + `"`} {
		if !strings.Contains(filtered, want) {
			t.Fatalf("missing %s in:\n%s", want, filtered)
		}
	}
	if !strings.Contains(filtered, expired.Name) || strings.Contains(filtered, active.Name) || strings.Contains(filtered, retired.Name) {
		t.Fatalf("expired filter:\n%s", filtered)
	}
	if none := page("/dashboard/sessions?status=retired&q=" + active.Name); !strings.Contains(none, "No session matches “"+active.Name+"” among the retired sessions.") ||
		strings.Contains(none, "data-select-all") {
		t.Fatalf("search under filter:\n%s", none)
	}
	if list := page("/dashboard/sessions?status=active&fragment=1"); !strings.Contains(list, button("dir=asc&amp;sort=state&amp;status=active", "<b>1</b> active", true)) ||
		!strings.Contains(list, active.Name) || strings.Contains(list, expired.Name) {
		t.Fatalf("fragment:\n%s", list)
	}
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions?status=bogus", cookie, nil, nil); r.status != 400 {
		t.Fatalf("unknown status: %d", r.status)
	}
}
