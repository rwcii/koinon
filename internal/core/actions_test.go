package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Dashboard action tests (sprint chunk 09): a real daemon on ephemeral loopback ports,
// temporary state, synthetic sessions and a patched launcher.

// undoSchemaThirteen turns a schema 13 database back into schema 12 for migration tests.
const undoSchemaThirteen = `ALTER TABLE sessions DROP COLUMN host_pid; ALTER TABLE sessions DROP COLUMN host_start;
	ALTER TABLE sessions DROP COLUMN tmux_socket; ALTER TABLE sessions DROP COLUMN tmux_pane;
	ALTER TABLE sessions DROP COLUMN succession; `

// undoSchemaTwelve turns a schema 12 database without participant inboxes back into
// schema 11 for migration tests.
const undoSchemaTwelve = `DROP TABLE participant_fences; ALTER TABLE sessions DROP COLUMN acked_by; ALTER TABLE memory_cursors DROP COLUMN actor; ALTER TABLE work_events DROP COLUMN actor; `

// undoSchemaEleven turns a schema 11 database with roleless participants back into
// schema 10 for migration tests.
const undoSchemaEleven = `DROP TABLE participant_events; DROP INDEX names_participant;
	ALTER TABLE names DROP COLUMN conflict; ALTER TABLE names DROP COLUMN role; ALTER TABLE sessions DROP COLUMN role;
	CREATE UNIQUE INDEX names_alias ON names(family, repository) WHERE kind='alias'; `

// undoSchemaTen turns a schema 10 database back into schema 9 for migration tests.
const undoSchemaTen = `ALTER TABLE sessions DROP COLUMN subagent; `

// undoSchemaNine turns a schema 9 database back into schema 8 for migration tests.
const undoSchemaNine = `ALTER TABLE sessions DROP COLUMN ack_mark; ALTER TABLE sessions DROP COLUMN ack_mark_at;
	ALTER TABLE sessions DROP COLUMN purge_at; `

// undoSchemaEight turns a schema 8 database back into schema 7 for migration tests.
const undoSchemaEight = `DROP TABLE imports; `

// undoSchemaSeven turns a schema 7 database back into schema 6 for migration tests.
const undoSchemaSeven = `DELETE FROM names WHERE family='maintainer'; DELETE FROM sessions WHERE family='maintainer';
	DROP INDEX audit_at; DROP TABLE audit; ALTER TABLE messages DROP COLUMN maintainer_ack; `

type actionClient struct {
	t      *testing.T
	d      *Daemon
	cookie string
	csrf   string
}

func newActionClient(t *testing.T, d *Daemon, root string) *actionClient {
	t.Helper()
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	cookie := dashboardLogin(t, d, secret)
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", cookie, nil, nil)
	match := csrfField.FindStringSubmatch(page.body)
	if page.status != 200 || match == nil {
		t.Fatalf("sessions page: %d", page.status)
	}
	return &actionClient{t: t, d: d, cookie: cookie, csrf: match[1]}
}

func (c *actionClient) sameOrigin(r *http.Request) {
	r.Header.Set("Origin", "http://"+c.d.Addresses()[0])
}

// do posts an action and returns its notice code; every action answers 303.
func (c *actionClient) do(action string, form url.Values) string {
	c.t.Helper()
	form.Set("csrf", c.csrf)
	r := dashboardDo(c.t, c.d, "POST", "/dashboard/actions/"+action, c.cookie, c.sameOrigin, form)
	location, err := url.Parse(r.header.Get("Location"))
	if r.status != http.StatusSeeOther || err != nil {
		c.t.Fatalf("%s: %d %q", action, r.status, r.header.Get("Location"))
	}
	return location.Query().Get("notice")
}

func auditAll(t *testing.T, s *Store) []AuditRecord {
	t.Helper()
	records, _, err := s.dashboardAudit(context.Background(), sortOrder(t, &auditSort, "", "", ""))
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// expectAudit checks that exactly one new record was written, with this result.
func expectAudit(t *testing.T, s *Store, before int, action, result, reason string) AuditRecord {
	t.Helper()
	records := auditAll(t, s)
	if len(records) != before+1 {
		t.Fatalf("%s: %d audit records, want %d: %+v", action, len(records), before+1, records)
	}
	r := records[0]
	if r.Action != action || r.Result != result || r.Reason != reason {
		t.Fatalf("audit: %+v, want %s %s %s", r, action, result, reason)
	}
	return r
}

func sessionState(t *testing.T, s *Store, k Key) Session {
	t.Helper()
	got, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDashboardActionsChangeStateAndAudit(t *testing.T) {
	d, root := startTestDaemon(t)
	s, ctx := d.store, context.Background()
	repo := namedRepo(t, "actionrepo")
	a := join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	c := join(t, s, "agy", "synthetic-c", "")
	for _, body := range []string{"one", "two", "three"} {
		if _, err := s.Send(ctx, Key{"codex", "synthetic-a"}, b.Name, body); err != nil {
			t.Fatal(err)
		}
	}
	client := newActionClient(t, d, root)
	n := 0

	// Retire: a stale revision and the maintainer are refused; the current revision retires.
	if got := client.do("retire", url.Values{"family": {"agy"}, "id": {"synthetic-c"}, "revision": {strconv.FormatInt(c.Revision+1, 10)}}); got != "revision_changed" {
		t.Fatalf("stale retire: %s", got)
	}
	expectAudit(t, s, n, "retire", "refused", "revision_changed")
	n++
	if got := client.do("retire", url.Values{"family": {"maintainer"}, "id": {"maintainer"}, "revision": {"1"}}); got != "maintainer_session" {
		t.Fatalf("maintainer retire: %s", got)
	}
	expectAudit(t, s, n, "retire", "refused", "maintainer_session")
	n++
	if got := client.do("retire", url.Values{"family": {"agy"}, "id": {"synthetic-c"}, "revision": {strconv.FormatInt(c.Revision, 10)}}); got != "retired" {
		t.Fatalf("retire: %s", got)
	}
	if r := expectAudit(t, s, n, "retire", "accepted", ""); r.Target != "agy:synthetic-c" {
		t.Fatalf("retire target: %q", r.Target)
	}
	n++
	if got := sessionState(t, s, Key{"agy", "synthetic-c"}); got.State != "retired" {
		t.Fatalf("retired state: %s", got.State)
	}
	if got := client.do("retire", url.Values{"family": {"agy"}, "id": {"synthetic-c"}, "revision": {strconv.FormatInt(c.Revision+1, 10)}}); got != "session_not_active" {
		t.Fatalf("retire again: %s", got)
	}
	expectAudit(t, s, n, "retire", "refused", "session_not_active")
	n++

	// Acknowledge through one, then clear: both are the maintainer's acknowledgements.
	if got := client.do("acknowledge", url.Values{"family": {"claude"}, "id": {"synthetic-b"}, "through": {"9"}}); got != "ack_beyond_last" {
		t.Fatalf("ack beyond: %s", got)
	}
	expectAudit(t, s, n, "acknowledge", "refused", "ack_beyond_last")
	n++
	if got := client.do("acknowledge", url.Values{"family": {"claude"}, "id": {"synthetic-b"}, "through": {"1"}}); got != "acknowledged" {
		t.Fatalf("acknowledge: %s", got)
	}
	expectAudit(t, s, n, "acknowledge", "accepted", "")
	n++
	// The recipient's own acknowledgement of the second message is the recipient's.
	if _, err := s.Ack(ctx, Key{"claude", "synthetic-b"}, 2); err != nil {
		t.Fatal(err)
	}
	if got := client.do("clear", url.Values{"family": {"claude"}, "id": {"synthetic-b"}}); got != "cleared" {
		t.Fatalf("clear: %s", got)
	}
	expectAudit(t, s, n, "clear", "accepted", "")
	n++
	want := map[int64]string{1: "maintainer", 2: "recipient", 3: "maintainer"}
	for id, by := range want {
		outcome, err := s.MessageOutcome(ctx, Key{"codex", "synthetic-a"}, id)
		if err != nil || !outcome.Acknowledged || outcome.AcknowledgedBy != by {
			t.Fatalf("outcome %d: %+v %v", id, outcome, err)
		}
	}
	inbox, err := s.ReadInbox(ctx, Key{"claude", "synthetic-b"}, 0, 50)
	if err != nil || len(inbox.Messages) != 3 || inbox.Messages[0].AcknowledgedBy != "maintainer" || inbox.Messages[1].AcknowledgedBy != "recipient" {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/messages?to="+b.Name, client.cookie, nil, nil)
	if !strings.Contains(page.body, "by maintainer") || !strings.Contains(page.body, "by recipient") {
		t.Fatal("messages view does not show who acknowledged")
	}

	// Send as maintainer; the recipient replies to the maintainer by name.
	if got := client.do("send", url.Values{"to": {b.Name}, "body": {"From the maintainer"}}); got != "sent" {
		t.Fatalf("send: %s", got)
	}
	sent := expectAudit(t, s, n, "send", "accepted", "")
	n++
	if !strings.HasPrefix(sent.Target, "to "+b.Name+" bytes 19 message ") || strings.Contains(sent.Target, "From the maintainer") {
		t.Fatalf("send target: %q", sent.Target)
	}
	inbox, err = s.ReadInbox(ctx, Key{"claude", "synthetic-b"}, 3, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].SenderName != "maintainer" || inbox.Messages[0].SenderFamily != "maintainer" {
		t.Fatalf("maintainer message: %+v %v", inbox, err)
	}
	peers, _, err := s.Peers(ctx, Key{"claude", "synthetic-b"})
	if err != nil || peers[len(peers)-1] != (Peer{Name: "maintainer", Family: "maintainer", State: "active"}) {
		t.Fatalf("peers: %+v %v", peers, err)
	}
	if _, err := s.Send(ctx, Key{"claude", "synthetic-b"}, "maintainer", "Reply to the maintainer"); err != nil {
		t.Fatal(err)
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/messages?to=maintainer", client.cookie, nil, nil); !strings.Contains(page.body, "Reply to the maintainer") {
		t.Fatal("reply not shown")
	}
	if got := client.do("send", url.Values{"to": {"nobody-here"}, "body": {"x"}}); got != "peer_not_found" {
		t.Fatalf("send to nobody: %s", got)
	}
	expectAudit(t, s, n, "send", "refused", "peer_not_found")
	n++
	if got := client.do("clear", url.Values{"family": {"maintainer"}, "id": {"maintainer"}}); got != "cleared" {
		t.Fatalf("clear maintainer: %s", got)
	}
	n++

	// Release a claim on the owner's behalf; a stale generation is refused.
	m := MemoryCaller{Repository: "/synthetic/actions/.git", Family: "codex", Name: a.Name, Consumer: "codex:synthetic-a"}
	item := create(t, s, m, "Synthetic held item")
	generation := mustStart(t, s, m, item, nil)
	rev := revision(t, s, m, item)
	release := func(gen int64) string {
		return client.do("release", url.Values{"repository": {m.Repository}, "work_id": {item}, "revision": {strconv.FormatInt(rev, 10)},
			"generation": {strconv.FormatInt(gen, 10)}, "consumer": {m.Consumer}})
	}
	if got := release(generation + 1); got == "released" {
		t.Fatal("stale generation released")
	}
	if records := auditAll(t, s); len(records) != n+1 || records[0].Action != "release" || records[0].Result != "refused" {
		t.Fatalf("stale release audit: %+v", records[0])
	}
	n++
	if got := release(generation); got != "released" {
		t.Fatalf("release: %s", got)
	}
	expectAudit(t, s, n, "release", "accepted", "")
	n++
	if view := get(t, s, m, item); view["current_claim"] != nil || view["lifecycle"] != "open" {
		t.Fatalf("released view: %v", view)
	}

	// Start a session through a patched launcher; arguments pass as data, never shell text.
	odd := filepath.Join(t.TempDir(), "a b;$(touch x)")
	if err := os.Mkdir(odd, 0700); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	d.launch = func(_ context.Context, args []string) ([]byte, error) {
		gotArgs = args
		return []byte(`{"ok":true,"session":"x","session_id":"$7","pane_id":"%9","socket":"/tmp/s"}`), nil
	}
	if got := client.do("launch", url.Values{"family": {"codex"}, "directory": {odd}}); got != "launched" {
		t.Fatalf("launch: %s", got)
	}
	launched := expectAudit(t, s, n, "launch", "accepted", "tmux $7 %9")
	n++
	wantPrefix := []string{"codex", "--state-dir", d.root, "--address", d.launchAddress(), "--directory", odd, "--tmux-session"}
	if len(gotArgs) != len(wantPrefix)+1 || !reflect.DeepEqual(gotArgs[:len(wantPrefix)], wantPrefix) ||
		!tmuxSessionName.MatchString(gotArgs[len(gotArgs)-1]) || !strings.HasPrefix(gotArgs[len(gotArgs)-1], "codex-a-b--") {
		t.Fatalf("launcher arguments: %q", gotArgs)
	}
	if !strings.Contains(launched.Target, odd) {
		t.Fatalf("launch target: %q", launched.Target)
	}
	// Claude Code starts the same way (#219).
	if got := client.do("launch", url.Values{"family": {"claude"}, "directory": {odd}, "name": {"claude_1"}}); got != "launched" {
		t.Fatalf("claude launch: %s", got)
	}
	expectAudit(t, s, n, "launch", "accepted", "tmux $7 %9")
	n++
	if want := []string{"claude", "--state-dir", d.root, "--address", d.launchAddress(), "--directory", odd, "--tmux-session", "claude_1"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("claude launcher arguments: %q", gotArgs)
	}
	d.launch = func(context.Context, []string) ([]byte, error) { return nil, errors.New("synthetic refusal") }
	if got := client.do("launch", url.Values{"family": {"opencode"}, "directory": {odd}, "name": {"named_1"}}); got != "launch_refused" {
		t.Fatalf("refused launch: %s", got)
	}
	expectAudit(t, s, n, "launch", "refused", "launch_refused")
	n++
	for _, form := range []url.Values{
		{"family": {"deepseek"}, "directory": {odd}},
		// A relative directory that exists where the daemon runs, the package directory.
		{"family": {"codex"}, "directory": {"web"}},
		{"family": {"codex"}, "directory": {filepath.Join(odd, "missing")}},
		{"family": {"codex"}, "directory": {odd}, "name": {"bad name"}},
	} {
		if got := client.do("launch", form); got != "invalid_request" {
			t.Fatalf("launch %v: %s", form, got)
		}
		expectAudit(t, s, n, "launch", "refused", "invalid_request")
		n++
	}

	// The audit view lists the records, newest first, with no message body.
	view := dashboardDo(t, d, "GET", "/dashboard/audit", client.cookie, nil, nil)
	if view.status != 200 || !strings.Contains(view.body, "launch_refused") || strings.Contains(view.body, "From the maintainer") {
		t.Fatalf("audit view: %d", view.status)
	}
	// The notice of an action shows on the view it returns to.
	if page := dashboardDo(t, d, "GET", "/dashboard/sessions?notice=retired", client.cookie, nil, nil); !strings.Contains(page.body, noticeText["retired"]) {
		t.Fatal("notice not shown")
	}
	if page := dashboardDo(t, d, "GET", "/dashboard/sessions?notice=%3Cscript%3E", client.cookie, nil, nil); strings.Contains(page.body, "<script>") ||
		strings.Contains(page.body, `class="notice"`) {
		t.Fatal("a foreign notice was shown")
	}
}

func TestDashboardActionRequestChecks(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	join(t, s, "codex", "synthetic-a", "")
	client := newActionClient(t, d, root)
	form := func() url.Values {
		return url.Values{"family": {"codex"}, "id": {"synthetic-a"}, "revision": {"1"}, "csrf": {client.csrf}}
	}
	for name, c := range map[string]struct {
		cookie string
		change func(*http.Request)
		form   url.Values
		status int
	}{
		"no session":     {"", client.sameOrigin, form(), 401},
		"wrong host":     {client.cookie, func(r *http.Request) { client.sameOrigin(r); r.Host = "localhost:1" }, form(), 403},
		"foreign origin": {client.cookie, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, form(), 403},
		"no origin":      {client.cookie, nil, form(), 403},
		"no token":       {client.cookie, client.sameOrigin, url.Values{"family": {"codex"}, "id": {"synthetic-a"}, "revision": {"1"}}, 403},
		"wrong token": {client.cookie, client.sameOrigin, url.Values{"family": {"codex"}, "id": {"synthetic-a"}, "revision": {"1"},
			"csrf": {strings.Repeat("0", 64)}}, 403},
	} {
		if r := dashboardDo(t, d, "POST", "/dashboard/actions/retire", c.cookie, c.change, c.form); r.status != c.status {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	if got := sessionState(t, s, Key{"codex", "synthetic-a"}); got.State != "active" {
		t.Fatal("a refused request changed state")
	}
	if records := auditAll(t, s); len(records) != 0 {
		t.Fatalf("refused requests were audited: %+v", records)
	}
	// A GET never acts.
	if r := dashboardDo(t, d, "GET", "/dashboard/actions/retire?"+form().Encode(), client.cookie, nil, nil); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("GET action: %d", r.status)
	}
}

func TestDashboardSendFormLimit(t *testing.T) {
	d, root := startTestDaemon(t)
	s, ctx := d.store, context.Background()
	b := join(t, s, "claude", "synthetic-b", "")
	client := newActionClient(t, d, root)
	// "é" is two bytes, six once percent-encoded: the largest body triples on the wire.
	full := strings.Repeat("é", maxBody/2)
	if got := client.do("send", url.Values{"to": {b.Name}, "body": {full}}); got != "sent" {
		t.Fatalf("maximum body: %s", got)
	}
	inbox, err := s.ReadInbox(ctx, Key{"claude", "synthetic-b"}, 0, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].Body != full {
		t.Fatalf("maximum body stored: %v", err)
	}
	if got := client.do("send", url.Values{"to": {b.Name}, "body": {full + "x"}}); got != "invalid_request" {
		t.Fatalf("oversized body: %s", got)
	}
	if got := client.do("send", url.Values{"to": {b.Name}, "body": {"\xff"}}); got != "invalid_request" {
		t.Fatalf("invalid UTF-8: %s", got)
	}
	// Other actions keep the small form limit.
	big := url.Values{"family": {"codex"}, "id": {"x"}, "revision": {"1"}, "pad": {strings.Repeat("p", 8192)}, "csrf": {client.csrf}}
	if r := dashboardDo(t, d, "POST", "/dashboard/actions/retire", client.cookie, client.sameOrigin, big); r.status != 400 && r.status != 403 {
		t.Fatalf("large form: %d", r.status)
	}
}

func TestDashboardActionsAtTheStorageCeiling(t *testing.T) {
	d, root := startTestDaemon(t)
	s, ctx := d.store, context.Background()
	b := join(t, s, "claude", "synthetic-b", "")
	if _, err := s.Send(ctx, Key{"claude", "synthetic-b"}, b.Name, "to clear"); err != nil {
		t.Fatal(err)
	}
	client := newActionClient(t, d, root)
	pages, _ := s.pages(ctx, s.db)
	// The daemon's maintenance writes too, so the limit changes under the storage lock.
	setMaxPages := func(n int64) {
		s.storage.mu.Lock()
		s.storage.maxPages = n
		s.storage.mu.Unlock()
	}
	setMaxPages(pages + reservePages + commitSlack + appendAllowance - 1)
	defer setMaxPages(defaultMaxPages)
	// An ordinary action that cannot write is refused, changes nothing and has no record.
	if got := client.do("send", url.Values{"to": {b.Name}, "body": {"no room"}}); got != "capacity" {
		t.Fatalf("send at the ceiling: %s", got)
	}
	if inbox, _ := s.ReadInbox(ctx, Key{"claude", "synthetic-b"}, 0, 50); len(inbox.Messages) != 1 {
		t.Fatal("refused send stored a message")
	}
	if records := auditAll(t, s); len(records) != 0 {
		t.Fatalf("a record without room: %+v", records)
	}
	// Acknowledgement is progress: it and its record use the reserve.
	if got := client.do("clear", url.Values{"family": {"claude"}, "id": {"synthetic-b"}}); got != "cleared" {
		t.Fatalf("clear at the ceiling: %s", got)
	}
	expectAudit(t, s, 0, "clear", "accepted", "")
}

func TestAuditRetentionAndCap(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Now()
	s.now = func() time.Time { return clock }
	old := clock.UnixMilli() - auditRetention - 1
	if _, err := s.db.Exec(`INSERT INTO audit(at,action,target,result) VALUES (?,'retire','old','accepted')`, old); err != nil {
		t.Fatal(err)
	}
	stmt, err := s.db.Prepare(`INSERT INTO audit(at,action,target,result) VALUES (?,'retire',?,'accepted')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < auditMax+5; i++ {
		if _, err := stmt.Exec(clock.UnixMilli(), strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := s.trimAudit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var oldest string
	s.db.QueryRow(`SELECT COUNT(*) FROM audit`).Scan(&count)
	s.db.QueryRow(`SELECT target FROM audit ORDER BY id LIMIT 1`).Scan(&oldest)
	if count != auditMax || oldest != "5" {
		t.Fatalf("after trim: %d records, oldest %q", count, oldest)
	}
	// Pages of 100, newest first, with no omission or duplicate.
	seen, after := map[int64]bool{}, ""
	for {
		page, next, err := s.dashboardAudit(ctx, sortOrder(t, &auditSort, "", "", after))
		if err != nil || len(page) > auditPage {
			t.Fatal(err)
		}
		for _, r := range page {
			if seen[r.ID] {
				t.Fatalf("duplicate %d", r.ID)
			}
			seen[r.ID] = true
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(seen) != auditMax {
		t.Fatalf("paged %d", len(seen))
	}
}

func TestSchemaSixMigrationAndRollback(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	repo := testRepo(t)
	a := join(t, s, "codex", "synthetic-a", repo)
	if _, err := s.Send(ctx, Key{"codex", "synthetic-a"}, a.Name, "kept"); err != nil {
		t.Fatal(err)
	}
	// A schema 6 database that already holds the name maintainer cannot migrate: the
	// whole step rolls back and the database stays at 6.
	if _, err := s.db.Exec(undoSchemaThirteen + undoSchemaTwelve + undoSchemaEleven + undoSchemaTen + undoSchemaNine + undoSchemaEight + undoSchemaSeven + `INSERT INTO names(name,kind,family,session_id) VALUES ('maintainer','peer','codex','x'); PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	if _, err := openStore(root); err == nil {
		t.Fatal("a conflicting schema 6 database migrated")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	var version, audit int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='audit'").Scan(&audit)
	if version != 6 || audit != 0 {
		t.Fatalf("partial migration: version %d, audit table %d", version, audit)
	}
	if _, err := db.Exec(`DELETE FROM names WHERE name='maintainer'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	restored, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	inbox, err := restored.ReadInbox(ctx, Key{"codex", "synthetic-a"}, 0, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].Body != "kept" || inbox.Messages[0].AcknowledgedBy != "" {
		t.Fatalf("migrated inbox: %+v %v", inbox, err)
	}
	if got := sessionState(t, restored, maintainerKey); got.State != "active" || got.Name != "maintainer" {
		t.Fatalf("maintainer session: %+v", got)
	}
}

func TestMaintainerOnlyThroughTheDashboard(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	s, ctx := d.store, context.Background()
	b := join(t, s, "claude", "synthetic-b", "")
	address := d.Addresses()[0]
	for path, body := range map[string]any{
		"/v1/messages/send":     map[string]any{"caller": maintainerKey, "to": b.Name, "body": "x"},
		"/v1/inbox/read":        map[string]any{"caller": maintainerKey},
		"/v1/inbox/ack":         map[string]any{"caller": maintainerKey, "through": 0},
		"/v1/peers":             map[string]any{"caller": maintainerKey},
		"/v1/sessions/register": map[string]any{"family": "maintainer", "id": "maintainer", "directory": t.TempDir()},
		"/v1/sessions/retire":   map[string]any{"family": "maintainer", "id": "maintainer", "if_revision": 1},
	} {
		if status, result := post(t, address, secret, path, body); status != 400 || result["code"] != "invalid_request" {
			t.Fatalf("%s as maintainer: %d %v", path, status, result)
		}
	}
	// The maintainer's inbox is never woken, and wake health does not count it.
	calls := 0
	s.wake.send = func(context.Context, Session, string) wakeResult { calls++; return wakeResult{"notified", "accepted"} }
	if _, err := s.Send(ctx, Key{"claude", "synthetic-b"}, "maintainer", "for the maintainer"); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeStep(ctx); err != nil || calls != 0 {
		t.Fatalf("maintainer woken: %d %v", calls, err)
	}
	if health, err := s.WakeHealth(ctx); err != nil || health["waiting"] != int64(0) {
		t.Fatalf("wake health: %v %v", health, err)
	}
	if counts, _ := s.Counts(ctx); counts["active"] != 1 {
		t.Fatalf("counts: %v", counts)
	}
	if items, _, _ := s.List(ctx); len(items) != 1 {
		t.Fatalf("sessions list: %+v", items)
	}
	data, _ := json.Marshal(sessionState(t, s, maintainerKey))
	if !strings.Contains(string(data), `"family":"maintainer"`) {
		t.Fatal(string(data))
	}
}

func TestDashboardAckSharesTheWakeBoundary(t *testing.T) {
	s, _, _, receiver, message, _ := wakeFixture(t, "codex")
	ctx := context.Background()
	key := Key{receiver.Family, receiver.ID}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.wake.send = func(context.Context, Session, string) wakeResult {
		once.Do(func() { close(started) })
		<-release
		return wakeResult{"notified", "accepted"}
	}
	done := make(chan error, 1)
	go func() { done <- s.wakeStep(ctx) }()
	<-started
	cleared := make(chan error, 1)
	go func() { _, err := s.ack(ctx, key, -1, true); cleared <- err }()
	select {
	case err := <-cleared:
		t.Fatalf("clear crossed an in-flight submission: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-cleared; err != nil {
		t.Fatal(err)
	}
	// Clear covers the newest sequence at the request; a later message stays unacknowledged.
	later, err := s.Send(ctx, Key{"codex", "synthetic-sender"}, receiver.Name, "after the clear")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ReadInbox(ctx, key, 0, 50)
	if err != nil || inbox.AckedThrough != message.Seq || later.Seq != message.Seq+1 {
		t.Fatalf("after clear: %+v %v", inbox, err)
	}
	// No notice is ever submitted for the cleared sequence.
	calls := 0
	s.wake.send = func(_ context.Context, _ Session, notice string) wakeResult {
		calls++
		if strings.Contains(notice, " "+strconv.FormatInt(message.Seq, 10)+"-") {
			t.Fatalf("notice for a cleared sequence: %q", notice)
		}
		return wakeResult{"notified", "accepted"}
	}
	if err := s.wakeStep(ctx); err != nil || calls != 1 {
		t.Fatalf("wake after clear: %d %v", calls, err)
	}
}

// releaseFields are the fields the dashboard sends for a release.
func releaseFields(id string, rev, generation int64) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	for k, v := range map[string]any{"work_id": id, "if_revision": rev, "claim_generation": generation,
		"checkpoint": "Released by the maintainer from the dashboard"} {
		fields[k], _ = json.Marshal(v)
	}
	return fields
}

// A maintenance write that commits before the release never takes the release's record
// (review finding F1 on #202).
func TestReleaseAuditIgnoresMaintenanceWrites(t *testing.T) {
	s, clock, owner := workStore(t)
	// Refused: the lease is due for expiry, and the generation is stale.
	id := create(t, s, owner, "Synthetic refused release")
	generation := mustStart(t, s, owner, id, map[string]any{"lease_seconds": 60})
	rev := revision(t, s, owner, id)
	*clock = clock.Add(61 * time.Second)
	ctx, pending := withAudit(context.Background(), "release", id)
	_, err := s.Work(ctx, owner, "work-release", releaseFields(id, rev, generation+1))
	if err == nil {
		t.Fatal("stale generation released")
	}
	s.auditRefusal(ctx, pending, errorCode(err))
	if records := auditAll(t, s); len(records) != 1 || records[0].Result != "refused" || records[0].Reason != errorCode(err) {
		t.Fatalf("refused release audited as %+v", records)
	}
	// Accepted: memory expiry is due and runs first inside the same call; the record joins
	// the release's own transaction.
	id = create(t, s, owner, "Synthetic accepted release")
	generation = mustStart(t, s, owner, id, nil)
	rev = revision(t, s, owner, id)
	s.memory.mu.Lock()
	s.memory.lastExpiry = nil
	s.memory.mu.Unlock()
	ctx, pending = withAudit(context.Background(), "release", id)
	if _, err := s.Work(ctx, owner, "work-release", releaseFields(id, rev, generation)); err != nil {
		t.Fatalf("release: %v", err)
	}
	if records := auditAll(t, s); !pending.written || len(records) != 2 || records[0].Result != "accepted" || records[0].Target != id {
		t.Fatalf("accepted release audited as %+v", records)
	}
	// A write that the operation did not mark never takes the record.
	ctx, pending = withAudit(context.Background(), "release", "unmarked")
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if records := auditAll(t, s); pending.written || len(records) != 2 {
		t.Fatalf("an unmarked write took the record: %+v", records)
	}
}

// A launch result that cannot be written is reported and written later; the launch never
// runs twice, and a restart closes a record that stayed open (review finding F2 on #202).
func TestLaunchResultWrittenAfterRecovery(t *testing.T) {
	d, root := startTestDaemon(t)
	client := newActionClient(t, d, root)
	runs := 0
	d.launch = func(context.Context, []string) ([]byte, error) {
		runs++
		d.store.storage.mu.Lock()
		d.store.storage.blocked = "synthetic concurrent checkpoint failure"
		d.store.storage.mu.Unlock()
		return []byte(`{"ok":true,"session_id":"$7","pane_id":"%9"}`), nil
	}
	if got := client.do("launch", url.Values{"family": {"codex"}, "directory": {t.TempDir()}, "name": {"synthetic"}}); got != "launched_unrecorded" {
		t.Fatalf("notice: %s", got)
	}
	if records := auditAll(t, d.store); len(records) != 1 || records[0].Result != "started" {
		t.Fatalf("before recovery: %+v", records)
	}
	if _, err := d.store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.finishLaunches(context.Background())
	if records := auditAll(t, d.store); len(records) != 1 || records[0].Result != "accepted" || records[0].Reason != "tmux $7 %9" {
		t.Fatalf("after recovery: %+v", records)
	}
	if len(d.unfinished) != 0 || runs != 1 {
		t.Fatalf("unfinished %d, launches %d", len(d.unfinished), runs)
	}
	// A record a previous daemon left started is closed at the next start.
	id, err := d.store.auditLaunch(context.Background(), "codex /synthetic tmux left-open")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.store.closeStartedLaunches(context.Background()); err != nil {
		t.Fatal(err)
	}
	var result, reason string
	d.store.db.QueryRow(`SELECT result,reason FROM audit WHERE id=?`, id).Scan(&result, &reason)
	if result != "unknown" || reason != "daemon_restarted" {
		t.Fatalf("left-open record: %s %s", result, reason)
	}
}

// A send to an alias records the session that received it (review finding F3 on #202).
func TestAliasSendAuditNamesTheRecipient(t *testing.T) {
	d, root := startTestDaemon(t)
	b := join(t, d.store, "claude", "synthetic-recipient", namedRepo(t, "audit-alias"))
	if b.Alias == "" || b.Alias == b.Name {
		t.Fatal("the fixture needs a distinct peer name and alias")
	}
	client := newActionClient(t, d, root)
	if got := client.do("send", url.Values{"to": {b.Alias}, "body": {"synthetic"}}); got != "sent" {
		t.Fatal(got)
	}
	var id int64
	d.store.db.QueryRow(`SELECT id FROM messages WHERE sender_family='maintainer'`).Scan(&id)
	// A send to an address reaches the participant, which the record names (chunk 03).
	want := "to " + b.Alias + " bytes 9 message " + strconv.FormatInt(id, 10)
	if record := auditAll(t, d.store)[0]; record.Target != want {
		t.Fatalf("target %q, want %q", record.Target, want)
	}
}
