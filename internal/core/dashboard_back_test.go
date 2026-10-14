package core

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// An action returns to the order and filters of the view its form was on (#373): the page
// puts them in each action form's back field, and the redirect keeps that view's known
// parameters under the action's own.
func TestActionsReturnToTheViewOrder(t *testing.T) {
	d, root := startTestDaemon(t)
	join(t, d.store, "codex", "synthetic-a", "")
	client := newActionClient(t, d, root)

	for _, path := range []string{
		"/dashboard/sessions?sort=name&dir=desc&q=codex&notice=retired",
		"/dashboard/sessions?sort=name&dir=desc&q=codex&notice=retired&fragment=1",
	} {
		page := dashboardDo(t, d, "GET", path, client.cookie, nil, nil)
		want := `name="back" value="` + html.EscapeString("dir=desc&q=codex&sort=name") + `"`
		if page.status != 200 || !strings.Contains(page.body, want) {
			t.Fatalf("%s: %d, no %s", path, page.status, want)
		}
	}

	location := func(action string, form url.Values) url.Values {
		t.Helper()
		form.Set("csrf", client.csrf)
		r := dashboardDo(t, d, "POST", "/dashboard/actions/"+action, client.cookie, client.sameOrigin, form)
		target, err := url.Parse(r.header.Get("Location"))
		if r.status != http.StatusSeeOther || err != nil {
			t.Fatalf("%s: %d %q", action, r.status, r.header.Get("Location"))
		}
		return target.Query()
	}
	long := strings.Repeat("x", 513)
	for _, c := range []struct {
		action string
		back   string
		want   url.Values
	}{
		// Unknown parameters, the page position and an oversized value are dropped.
		{"retire", "sort=name&dir=desc&q=synthetic&fragment=1&after=abc&csrf=x&notice=retired&state=" + long,
			url.Values{"sort": {"name"}, "dir": {"desc"}, "q": {"synthetic"}, "notice": {"invalid_request"}}},
		// A back longer than backLimit is not read.
		{"retire", "sort=name&q=" + strings.Repeat("x", backLimit), url.Values{"notice": {"invalid_request"}}},
		// The action's own query wins over back.
		{"send", "sort=sent&dir=asc&to=codex-synthetic&peers=all",
			url.Values{"sort": {"sent"}, "dir": {"asc"}, "to": {"maintainer"}, "peers": {"all"}, "notice": {"invalid_request"}}},
		{"memory-record", "sort=entries&store=/synthetic/repo/.git&q=text&type=note&all=1",
			url.Values{"sort": {"entries"}, "store": {"/synthetic/repo/.git"}, "q": {"text"}, "type": {"note"}, "all": {"1"}, "notice": {"invalid_request"}}},
		{"release", "sort=updated&lifecycle=open&store=/synthetic/repo/.git&item=7",
			url.Values{"sort": {"updated"}, "lifecycle": {"open"}, "store": {"/synthetic/repo/.git"}, "item": {"7"}, "notice": {"invalid_request"}}},
		// A view without sorting or search, and a malformed back, return without them.
		{"room-create", "sort=name&q=synthetic", url.Values{"notice": {"invalid_request"}}},
		{"retire", "sort=name&q=%zz", url.Values{"notice": {"invalid_request"}}},
		{"retire", "", url.Values{"notice": {"invalid_request"}}},
	} {
		if got := location(c.action, url.Values{"back": {c.back}}); got.Encode() != c.want.Encode() {
			t.Errorf("%s with back %.60q: %v, want %v", c.action, c.back, got, c.want)
		}
	}
}

// The back field does not push a form that fit the body limit over it: fields of exactly
// actionFormLimit bytes with the longest back field are read. A body one byte over the
// limit with its back allowance is refused.
func TestActionBodyLimitAllowsTheBackField(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	q := ""
	for len(viewOrder("sessions", url.Values{"sort": {"name"}, "dir": {"desc"}, "q": {q + "Ж"}}).Encode()) <= backLimit {
		q += "Ж"
	}
	back := backField("sessions", url.Values{"sort": {"name"}, "dir": {"desc"}, "q": {q}})
	if len(back) < backLimit-8 {
		t.Fatalf("back field of %d bytes", len(back))
	}
	encoded := "&" + url.Values{"back": {back}}.Encode()
	for _, c := range []struct {
		base   int
		status int
	}{{actionFormLimit, http.StatusSeeOther}, {actionFormLimit + len("&back=") + 3*backLimit + 1 - len(encoded), http.StatusBadRequest}} {
		fields := url.Values{"csrf": {client.csrf}, "family": {"synthetic"}}.Encode() + "&pad="
		body := fields + strings.Repeat("p", c.base-len(fields)) + encoded
		r := dashboardDo(t, d, "POST", "/dashboard/actions/launch", client.cookie, func(r *http.Request) {
			client.sameOrigin(r)
			r.Body, r.ContentLength = io.NopCloser(strings.NewReader(body)), int64(len(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}, nil)
		// An unread body has no CSRF token, so the refusal can be either.
		if r.status != c.status && !(c.status == http.StatusBadRequest && r.status == http.StatusForbidden) {
			t.Errorf("form of %d bytes with a %d-byte back: %d, want %d", c.base, len(back), r.status, c.status)
		}
		if c.status == http.StatusSeeOther {
			if got, _ := url.Parse(r.header.Get("Location")); got.Query().Get("q") != q {
				t.Errorf("redirect %q lost the search", r.header.Get("Location"))
			}
		}
	}
}
