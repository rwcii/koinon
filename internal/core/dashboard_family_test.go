package core

import (
	"context"
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
