package core

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The Sessions page selects sessions by family with an exact match (#718): a family name in a
// repository path or a session name does not bring that session in, an unknown family has no
// rows, and the family tally counts the whole store.
func TestDashboardSessionsFamilyFilter(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	ctx := context.Background()
	// The Claude session's repository is named codex, so a text search for codex finds it.
	claude := join(t, s, "claude", "synthetic-claude", namedRepo(t, "codex"))
	codexA := join(t, s, "codex", "synthetic-codex-a", "")
	codexB := join(t, s, "codex", "synthetic-codex-b", "")
	join(t, s, "opencode", "synthetic-opencode", "")
	keys := func(rows []Session) map[string]bool {
		m := map[string]bool{}
		for _, r := range rows {
			m[r.ID] = true
		}
		return m
	}
	order := sortOrder(t, &sessionSort, "name", "asc", "")
	rows, _, err := s.dashboardSessions(ctx, order, "codex", "", "")
	if err != nil || !keys(rows)[claude.ID] {
		t.Fatalf("the text search is the imprecise match that #718 replaces: %v %v", keys(rows), err)
	}
	rows, _, err = s.dashboardSessions(ctx, order, "", "", "codex")
	if got := keys(rows); err != nil || len(got) != 2 || !got[codexA.ID] || !got[codexB.ID] {
		t.Fatalf("family codex: %v %v", got, err)
	}
	for _, family := range []string{"Codex", "code", "unknown"} {
		if rows, _, err := s.dashboardSessions(ctx, order, "", "", family); err != nil || len(rows) != 0 {
			t.Fatalf("family %q: %v %v", family, keys(rows), err)
		}
	}
	if rows, _, err := s.dashboardSessions(ctx, order, "", "retired", "codex"); err != nil || len(rows) != 0 {
		t.Fatalf("family with status: %v %v", keys(rows), err)
	}
	if _, _, err := s.dashboardSessions(ctx, order, "", "", strings.Repeat("x", dashboardSearchMax+1)); err != ErrInvalid {
		t.Fatalf("a family value over the limit: %v", err)
	}

	client := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil)
	for _, want := range []string{`aria-label="Show the sessions of one family"`, `aria-current="true"><b>4</b> all families</a>`,
		`<b>1</b> claude</a>`, `<b>2</b> codex</a>`, `<b>1</b> opencode</a>`} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("tally lacks %q", want)
		}
	}
	page = dashboardDo(t, d, "GET", "/dashboard/sessions?family=codex&status=active", client.cookie, nil, nil)
	if page.status != 200 || strings.Contains(page.body, claude.Name) || !strings.Contains(page.body, codexA.Name) || !strings.Contains(page.body, codexB.Name) {
		t.Fatalf("family page: %d", page.status)
	}
	for _, want := range []string{
		// The chosen family is current, and the tally still counts the whole store.
		`family=codex&amp;sort=state&amp;status=active" aria-current="true"><b>2</b> codex</a>`, `<b>1</b> claude</a>`,
		// The search form, the sort and status links and the back field of the actions keep it.
		`<input type="hidden" name="family" value="codex">`, `family=codex&amp;sort=name&amp;status=active">Name</a>`,
		`family=codex&amp;sort=state"`, `name="back" value="family=codex&amp;status=active"`} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("family page lacks %q", want)
		}
	}
	page = dashboardDo(t, d, "GET", "/dashboard/sessions?family=unknown", client.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "No sessions of family unknown.") {
		t.Fatalf("unknown family: %d", page.status)
	}
}

// The bulk action turns the Enable of each selected session on or off as its own Enable button
// does (#718): at the session and Enable revisions that the page showed, for the selected key,
// with its own audit record, and a refusal for one session does not stop the others.
func TestDashboardBulkContextEnable(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	ctx := context.Background()
	if err := s.SetContextPolicy(roomCtx("context-family-settings"), "claude", ContextHandoffCycle, 70); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContextPolicy(roomCtx("context-family-settings"), "codex", ContextNativeCompaction, 80); err != nil {
		t.Fatal(err)
	}
	a := join(t, s, "claude", "synthetic-a", "")
	b := join(t, s, "codex", "synthetic-b", "")
	c := join(t, s, "codex", "synthetic-c", "")
	other := join(t, s, "opencode", "synthetic-other", "")
	// The cycle is available to every session but c.
	previous := contextCycleProbe
	t.Cleanup(func() { contextCycleProbe = previous })
	contextCycleProbe = func(_ *Store, _ context.Context, k Key) CycleProbe {
		if k.ID == c.ID {
			return CycleProbe{Reason: "host_unpinned"}
		}
		return CycleProbe{Entry: "koinon-pickup"}
	}
	// b keeps its own threshold.
	if _, err := s.SetContextThreshold(roomCtx("context-threshold"), b.Name, 40); err != nil {
		t.Fatal(err)
	}
	enables := func() map[Key]ContextEnable {
		t.Helper()
		e, err := s.contextEnables(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	reload := func(x Session) Session {
		t.Helper()
		r, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, x.Family, x.ID), s.now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	form := func(action string, rows ...struct {
		x      Session
		enable int64
	}) url.Values {
		f := url.Values{"action": {action}}
		for _, r := range rows {
			x := reload(r.x)
			f.Add("s", x.Family+":"+x.ID+":"+strconv.FormatInt(x.Revision, 10))
			f.Set("enable:"+x.Family+":"+x.ID, strconv.FormatInt(r.enable, 10))
		}
		return f
	}
	type row = struct {
		x      Session
		enable int64
	}
	client := newActionClient(t, d, root)
	thresholdB := enables()[Key{b.Family, b.ID}].Threshold
	revisionB := enables()[Key{b.Family, b.ID}].Revision
	before := len(auditAll(t, s))
	if got := client.do("bulk", form("context-on", row{a, 0}, row{b, revisionB}, row{c, 0})); got != "bulk_partial" {
		t.Fatalf("bulk enable: %s", got)
	}
	e := enables()
	if !e[Key{a.Family, a.ID}].Enabled || !e[Key{b.Family, b.ID}].Enabled || e[Key{c.Family, c.ID}].Enabled || e[Key{other.Family, other.ID}].Enabled {
		t.Fatalf("enables: %+v", e)
	}
	if e[Key{b.Family, b.ID}].Threshold != thresholdB || thresholdB != 40 {
		t.Fatalf("b's own threshold: %d, before %d", e[Key{b.Family, b.ID}].Threshold, thresholdB)
	}
	if p := policyOf(t, s, "claude"); p.Mode != ContextHandoffCycle || p.Threshold != 70 {
		t.Fatalf("claude policy changed: %+v", p)
	}
	if p := policyOf(t, s, "codex"); p.Mode != ContextNativeCompaction || p.Threshold != 80 {
		t.Fatalf("codex policy changed: %+v", p)
	}
	records := auditAll(t, s)[:len(auditAll(t, s))-before]
	results := map[string]string{}
	for _, r := range records {
		if r.Action != "context-enable" {
			t.Fatalf("audit action: %+v", r)
		}
		results[r.Target] = r.Result + " " + r.Reason
	}
	if len(records) != 3 || results["context enable "+a.Name+" on revision 1 by dashboard"] != "accepted " ||
		results["context enable "+b.Name+" on revision "+strconv.FormatInt(revisionB+1, 10)+" by dashboard"] != "accepted " ||
		results[c.Family+":"+c.ID+" by dashboard"] != "refused context_cycle_unavailable" {
		t.Fatalf("audit: %+v", records)
	}
	// An Enable that changed since the page, a session that changed since the page and a
	// missing Enable revision are refused; the session that the page showed correctly changes.
	stale := form("context-off", row{a, 0}, row{b, revisionB + 1})
	stale.Add("s", c.Family+":"+c.ID+":"+strconv.FormatInt(reload(c).Revision, 10))
	if got := client.do("bulk", stale); got != "bulk_partial" {
		t.Fatalf("stale enable: %s", got)
	}
	if e := enables(); !e[Key{a.Family, a.ID}].Enabled || e[Key{b.Family, b.ID}].Enabled {
		t.Fatalf("after the stale disable: %+v", e)
	}
	last := auditAll(t, s)
	if last[2].Result != "refused" || last[2].Reason != "context_enable_changed" || last[0].Result != "refused" || last[0].Reason != "invalid_request" {
		t.Fatalf("stale audit: %+v", last[:3])
	}
	changed := form("context-off", row{a, 1})
	changed.Set("s", a.Family+":"+a.ID+":"+strconv.FormatInt(reload(a).Revision+5, 10))
	if got := client.do("bulk", changed); got != "bulk_partial" || !enables()[Key{a.Family, a.ID}].Enabled || auditAll(t, s)[0].Reason != "revision_changed" {
		t.Fatalf("changed session: %s %+v", got, auditAll(t, s)[0])
	}
	if got := client.do("bulk", form("context-off", row{a, 1})); got != "bulk_applied" || enables()[Key{a.Family, a.ID}].Enabled {
		t.Fatalf("disable: %s", got)
	}
	// The older bulk actions keep working with the Enable fields on the page.
	clear := form("clear", row{a, 0})
	if got := client.do("bulk", clear); got != "bulk_applied" {
		t.Fatalf("clear: %s", got)
	}
	// The page offers the two actions and carries each row's Enable revision.
	page := dashboardDo(t, d, "GET", "/dashboard/sessions?family=codex", client.cookie, nil, nil)
	for _, want := range []string{`<option value="context-on">Enable managed context</option>`, `<option value="context-off">Disable managed context</option>`,
		`<input type="hidden" name="enable:codex:synthetic-b" value="` + strconv.FormatInt(enables()[Key{b.Family, b.ID}].Revision, 10) + `" form="bulk">`} {
		if !strings.Contains(page.body, want) {
			t.Fatalf("sessions page lacks %q", want)
		}
	}
}

// The key-bound Enable changes the selected session only, and its Disable ends that session's
// running cycle as the named one does (#718).
func TestSetContextEnableOfBindsTheKey(t *testing.T) {
	f := newCycleFixture(t)
	other := join(t, f.s, "claude", "synthetic-other", "")
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	if v == nil || v.FinishedAt != 0 {
		t.Fatalf("cycle: %+v", v)
	}
	e, _ := f.s.contextEnables(context.Background())
	s := f.reload(t, f.k)
	if _, err := f.s.SetContextEnableOf(roomCtx("context-enable"), f.k, s.Revision, e[f.k].Revision, false); err != nil {
		t.Fatal(err)
	}
	if v = f.status(t, f.k); v.State != cycleCancelled || v.Reason != "disabled" {
		t.Fatalf("the running cycle was not ended: %+v", v)
	}
	if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled || e[Key{other.Family, other.ID}].Revision != 0 {
		t.Fatalf("enables: %+v", e)
	}
	if _, err := f.s.SetContextEnableOf(roomCtx("context-enable"), Key{"claude", "synthetic-missing"}, 1, 0, false); err != ErrMissing {
		t.Fatalf("a key without a session: %v", err)
	}
	if _, err := f.s.SetContextEnableOf(roomCtx("context-enable"), maintainerKey, 1, 0, false); err != ErrInvalid {
		t.Fatalf("the maintainer key: %v", err)
	}
}
