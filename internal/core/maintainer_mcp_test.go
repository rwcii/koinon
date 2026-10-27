package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The maintainer token and the maintainer routes (PDR 009, Decisions 3 and 4). All
// sessions, items, rooms and secrets are synthetic.

// maintainerDaemon is a test daemon with the maintainer secret set at a low PBKDF2 cost.
func maintainerDaemon(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	d, root := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	return d, root, bearer
}

// maintainerToken exchanges the secret for a token of client.
func newMaintainerToken(t *testing.T, d *Daemon, bearer, client string) string {
	t.Helper()
	status, reply := post(t, d.Addresses()[0], bearer, "/v1/maintainer/session", map[string]any{"secret": testSecret, "client": client})
	token, _ := reply["token"].(string)
	if status != 200 || len(token) != 64 || reply["client"] != "mcp:"+client || reply["idle_seconds"] != float64(86400) ||
		reply["absolute_seconds"] != float64(604800) {
		t.Fatalf("session: %d %v", status, reply)
	}
	return token
}

// maintainerCall calls one maintainer route with a token.
func maintainerCall(t *testing.T, d *Daemon, bearer, token, route string, body map[string]any) (int, map[string]any) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["token"] = token
	return post(t, d.Addresses()[0], bearer, "/v1/maintainer/"+route, body)
}

// mustMaintainer calls a route that must succeed and returns its result.
func mustMaintainer(t *testing.T, d *Daemon, bearer, token, route string, body map[string]any) map[string]any {
	t.Helper()
	status, reply := maintainerCall(t, d, bearer, token, route, body)
	result, _ := reply["result"].(map[string]any)
	if status != 200 || reply["ok"] != true || result == nil {
		t.Fatalf("%s: %d %v", route, status, reply)
	}
	return result
}

// refusedMaintainer calls a route that must be refused with code.
func refusedMaintainer(t *testing.T, d *Daemon, bearer, token, route string, body map[string]any, code string) map[string]any {
	t.Helper()
	status, reply := maintainerCall(t, d, bearer, token, route, body)
	if status == 200 || reply["code"] != code {
		t.Fatalf("%s %v: %d %v, want %s", route, body, status, reply, code)
	}
	return reply
}

func TestMaintainerSessionRoute(t *testing.T) {
	d, root := startTestDaemon(t)
	d.store.secretCost = 1000
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	session := func(secret, client string) (int, map[string]any) {
		return post(t, address, bearer, "/v1/maintainer/session", map[string]any{"secret": secret, "client": client})
	}
	if status, reply := session(testSecret, "koinon-voice"); status == 200 || reply["code"] != "maintainer_secret_unset" {
		t.Fatalf("no secret: %d %v", status, reply)
	}
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"", "Voice", "-voice", "voice_1", "voice client", strings.Repeat("a", 33), "mcp:voice"} {
		if status, reply := session(testSecret, client); status != 400 || reply["code"] != "invalid_request" {
			t.Fatalf("client %q: %d %v", client, status, reply)
		}
	}
	if status, reply := post(t, address, bearer, "/v1/maintainer/session", map[string]any{"secret": testSecret, "client": "voice", "via": "dashboard"}); status != 400 || reply["code"] != "invalid_request" {
		t.Fatalf("unknown field: %d %v", status, reply)
	}
	if status, reply := session("wrong secret "+strings.Repeat("y", 20), "koinon-voice"); status == 200 || reply["code"] != "maintainer_secret_invalid" {
		t.Fatalf("wrong secret: %d %v", status, reply)
	}
	token := newMaintainerToken(t, d, bearer, "koinon-voice")
	if !strings.HasSuffix(auditRows(t, d), "maintainer_login mcp:koinon-voice accepted") {
		t.Fatalf("login audit:\n%s", auditRows(t, d))
	}
	// A token is no dashboard login, and a dashboard login is no token.
	if r := dashboardDo(t, d, "GET", "/dashboard/sessions", token, nil, nil); r.status != 401 {
		t.Fatalf("token as a cookie: %d", r.status)
	}
	cookie := dashboardLogin(t, d, bearer)
	refusedMaintainer(t, d, bearer, cookie, "peers", nil, "maintainer_token_expired")
	mustMaintainer(t, d, bearer, token, "peers", nil)
	// The bearer secret is still required.
	if status, reply := post(t, address, "0000000000000000000000000000000000000000000000000000000000000000", "/v1/maintainer/peers",
		map[string]any{"token": token}); status != 401 || reply["code"] != "unauthorized" {
		t.Fatalf("without the bearer secret: %d %v", status, reply)
	}
	// Five failures in a row start the wait: a start during it is refused with the wait.
	for range 5 {
		session("wrong secret "+strings.Repeat("y", 20), "koinon-voice")
	}
	status, reply := session(testSecret, "koinon-voice")
	details, _ := reply["details"].(map[string]any)
	if status != http.StatusTooManyRequests || reply["code"] != "maintainer_secret_backoff" || details["retry_after_seconds"] != float64(1) ||
		details["retry_after"] == nil {
		t.Fatalf("during the wait: %d %v", status, reply)
	}
}

// Each maintainer route calls the dashboard's store method with its refusals; each action
// records the client as its origin.
func TestMaintainerRoutes(t *testing.T) {
	d, _, bearer := maintainerDaemon(t)
	s, ctx := d.store, context.Background()
	repo := namedRepo(t, "maintainerrepo")
	a := join(t, s, "codex", "synthetic-maintainer-a", repo)
	b := join(t, s, "claude", "synthetic-maintainer-b", "")
	aKey := Key{"codex", a.ID}
	mustCreate(t, s, aKey, "M-1", DecisionFields{Title: ptr("Ship it"), Kind: ptr("decision"), Recipients: &[]string{a.Name}})
	mustCreate(t, s, aKey, "M-2", DecisionFields{Title: ptr("Note"), Kind: ptr("fyi")})
	token := newMaintainerToken(t, d, bearer, "test-client")
	call := func(route string, body map[string]any) map[string]any {
		return mustMaintainer(t, d, bearer, token, route, body)
	}
	refused := func(route string, body map[string]any, code string) map[string]any {
		return refusedMaintainer(t, d, bearer, token, route, body, code)
	}

	board := call("board-list", nil)
	counts := board["counts"].(map[string]any)
	if counts["waiting"] != float64(1) || counts["open_fyi"] != float64(1) || len(board["items"].([]any)) != 1 {
		t.Fatalf("board: %v", board)
	}
	if active := call("board-list", map[string]any{"filter": "active"}); len(active["items"].([]any)) != 2 {
		t.Fatalf("active board: %v", active)
	}
	refused("board-list", map[string]any{"filter": "mine"}, "invalid_request")
	if item := call("decision-get", map[string]any{"reference": "M-1"}); item["title"] != "Ship it" || item["revision"] != float64(1) {
		t.Fatalf("item: %v", item)
	}
	refused("decision-get", map[string]any{"reference": "M-404"}, "not_found")
	reply := refused("decision-verdict", map[string]any{"reference": "M-1", "revision": 2, "verdict": "approved"}, "stale_revision")
	if reply["details"].(map[string]any)["current_revision"] != float64(1) {
		t.Fatalf("stale details: %v", reply)
	}
	refused("decision-verdict", map[string]any{"reference": "M-404", "revision": 1, "verdict": "approved"}, "not_found")
	refused("decision-verdict", map[string]any{"reference": "M-1", "revision": 1, "verdict": "maybe"}, "invalid_request")
	refused("decision-verdict", map[string]any{"reference": "M-2", "revision": 1, "verdict": "approved"}, "invalid_verdict")
	if v := call("decision-verdict", map[string]any{"reference": "M-1", "revision": 1, "verdict": "approved", "note": "Go."}); v["verdict"] != "approved" || v["via"] != "mcp:test-client" {
		t.Fatalf("verdict: %v", v)
	}
	refused("decision-verdict", map[string]any{"reference": "M-1", "revision": 1, "verdict": "approved"}, "decided")
	history := call("decision-history", map[string]any{"reference": "M-1"})
	if verdicts := history["verdicts"].([]any); len(verdicts) != 1 || verdicts[0].(map[string]any)["via"] != "mcp:test-client" {
		t.Fatalf("history: %v", history)
	}
	refused("decision-history", map[string]any{"reference": "M-404"}, "not_found")

	// Messages: the maintainer's inbox, acknowledgement and send.
	if _, err := s.Send(ctx, aKey, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	inbox := call("inbox", nil)
	if messages := inbox["messages"].([]any); len(messages) != 1 || messages[0].(map[string]any)["body"] != "a synthetic report" || inbox["last_seq"] != float64(1) {
		t.Fatalf("inbox: %v", inbox)
	}
	refused("ack", map[string]any{"through": 5}, "ack_beyond_last")
	refused("ack", nil, "invalid_request")
	if acked := call("ack", map[string]any{"through": 1}); acked["acked_through"] != float64(1) {
		t.Fatalf("ack: %v", acked)
	}
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_family='maintainer' AND ack_via='mcp:test-client' AND ack_session='maintainer' AND maintainer_ack=1`) != 1 {
		t.Fatal("ack via")
	}
	if sent := call("send", map[string]any{"to": b.Name, "body": "a synthetic instruction"}); sent["recipient"] != b.Name {
		t.Fatalf("send: %v", sent)
	}
	refused("send", map[string]any{"to": "synthetic-nobody", "body": "x"}, "peer_not_found")
	got, err := s.ReadInbox(ctx, Key{"claude", b.ID}, 0, 10)
	if err != nil || len(got.Messages) != 1 || got.Messages[0].SenderFamily != "maintainer" || got.Messages[0].Body != "a synthetic instruction" {
		t.Fatalf("agent inbox: %+v %v", got, err)
	}

	// Rooms.
	room := call("room-create", map[string]any{"name": "synthetic room", "key": "room-key-1"})["room_id"].(string)
	if again := call("room-create", map[string]any{"name": "synthetic room", "key": "room-key-1"}); again["room_id"] != room {
		t.Fatalf("repeated create: %v", again)
	}
	refused("room-create", map[string]any{"name": "other name", "key": "room-key-1"}, "idempotency_conflict")
	if added := call("room-add", map[string]any{"room_id": room, "peer": a.Name}); added["changed"] != true {
		t.Fatalf("add: %v", added)
	}
	unknownRoom := strings.Repeat("0", 32)
	refused("room-add", map[string]any{"room_id": room, "peer": "synthetic-nobody"}, "peer_not_found")
	refused("room-add", map[string]any{"room_id": unknownRoom, "peer": a.Name}, "room_not_found")
	deadline := float64(time.Now().Unix() + 600)
	if posted := call("room-post", map[string]any{"room_id": room, "body": "a synthetic post", "key": "post-1", "deadline": deadline}); posted["seq"] != float64(1) || posted["queued"] != float64(1) || posted["allowance"] != nil {
		t.Fatalf("post: %v", posted)
	}
	refused("room-post", map[string]any{"room_id": unknownRoom, "body": "x", "key": "post-2", "deadline": deadline}, "room_not_found")
	if read := call("room-read", map[string]any{"room_id": room}); len(read["posts"].([]any)) != 1 || read["posts"].([]any)[0].(map[string]any)["origin"] != "maintainer" {
		t.Fatalf("read: %v", read)
	}
	refused("room-read", map[string]any{"room_id": unknownRoom}, "room_not_found")
	rooms := call("room-list", nil)["rooms"].([]any)
	if len(rooms) != 1 || len(rooms[0].(map[string]any)["members"].([]any)) != 1 {
		t.Fatalf("rooms: %v", rooms)
	}
	// The maintainer routes carry no refill or allowance (#493 removes them).
	if _, ok := rooms[0].(map[string]any)["refill"]; ok {
		t.Fatalf("room-list has refill: %v", rooms)
	}
	refused("room-create", map[string]any{"name": "third room", "key": "room-key-3", "refill": 5}, "invalid_request")
	call("room-remove", map[string]any{"room_id": room, "ordinal": 1})
	refused("room-remove", map[string]any{"room_id": room, "ordinal": 1}, "room_member_not_found")

	// Peers: the agent sessions, without the maintainer.
	peers := call("peers", nil)["peers"].([]any)
	names := []string{}
	for _, p := range peers {
		names = append(names, p.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != strings.Join([]string{a.Name, b.Name}, ",") && strings.Join(names, ",") != strings.Join([]string{b.Name, a.Name}, ",") {
		t.Fatalf("peers: %v", names)
	}
	if status := call("peer-status", map[string]any{"peer": a.Name}); status["name"] != a.Name || status["family"] != "codex" {
		t.Fatalf("peer status: %v", status)
	}
	refused("peer-status", map[string]any{"peer": "synthetic-nobody"}, "peer_not_found")

	// Requests: an unknown field is refused, and an unknown token ends nothing else.
	refused("peers", map[string]any{"caller": map[string]any{"family": "codex", "id": a.ID}}, "invalid_request")
	refusedMaintainer(t, d, bearer, strings.Repeat("a", 64), "peers", nil, "maintainer_token_expired")
	refusedMaintainer(t, d, bearer, "short", "board-list", nil, "maintainer_token_expired")

	// The maintainer mode registers no session.
	if count(t, s, `SELECT COUNT(*) FROM sessions WHERE family NOT IN ('maintainer','codex','claude')`) != 0 {
		t.Fatal("a session for the maintainer client")
	}
	// Every action's record names the client, also a refusal; reads write none.
	records := auditAll(t, s)
	actions := map[string]bool{}
	for _, r := range records {
		if r.Action == "maintainer_login" || r.Action == "maintainer_secret" {
			continue
		}
		actions[r.Action+" "+r.Result] = true
		if !strings.HasSuffix(r.Target, " by mcp:test-client") {
			t.Fatalf("record without the client: %+v", r)
		}
	}
	for _, want := range []string{"decision-approve accepted", "decision-approve refused", "decision-verdict refused", "acknowledge accepted",
		"acknowledge refused", "send accepted", "send refused", "room-create accepted", "room-create refused", "room-add accepted",
		"room-add refused", "room-post accepted", "room-post refused", "room-remove accepted", "room-remove refused"} {
		if !actions[want] {
			t.Fatalf("no %s record: %v", want, actions)
		}
	}
	if len(actions) != 15 {
		t.Fatalf("records: %v", actions)
	}
}

// A maintainer token ends at the idle limit, at the absolute limit, and when the secret is
// set, generated or cleared; each authenticated request moves the idle limit.
func TestMaintainerTokenEnds(t *testing.T) {
	d, advance := secretDaemon(t)
	ctx := context.Background()
	start := func() string {
		t.Helper()
		token, err := d.passwordLogin(ctx, testSecret, "mcp:synthetic")
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	use := func(token string) error {
		_, err := d.maintainerContext(ctx, token)
		return err
	}
	// Idle: a request just before the limit moves it; a request after a full idle period fails.
	token := start()
	for range 3 {
		advance(sessionIdle - time.Millisecond)
		if err := use(token); err != nil {
			t.Fatalf("within the idle limit: %v", err)
		}
	}
	advance(sessionIdle)
	if err := use(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("after the idle limit: %v", err)
	}
	// Absolute: requests keep it from idling, but not past seven days.
	token = start()
	for elapsed := time.Duration(0); elapsed+23*time.Hour < sessionAbsolute; elapsed += 23 * time.Hour {
		advance(23 * time.Hour)
		if err := use(token); err != nil {
			t.Fatalf("at %v: %v", elapsed, err)
		}
	}
	advance(23 * time.Hour)
	if err := use(token); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("after the absolute limit: %v", err)
	}
	// A secret change ends every token.
	for _, action := range []string{"set", "generate", "clear"} {
		if err := d.store.setMaintainerSecret(ctx, testSecret, "set"); err != nil {
			t.Fatal(err)
		}
		token := start()
		secret := ""
		if action == "set" {
			secret = "another synthetic secret"
		}
		if _, err := d.changeSecret(ctx, action, secret); err != nil {
			t.Fatal(err)
		}
		if err := use(token); !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("after %s: %v", action, err)
		}
	}
}

// A daemon restart ends every token.
func TestMaintainerTokenEndsAtRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	seedState(t, root)
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	token := newMaintainerToken(t, d, bearer, "synthetic")
	mustMaintainer(t, d, bearer, token, "board-list", nil)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	refusedMaintainer(t, d, bearer, token, "board-list", nil, "maintainer_token_expired")
	newMaintainerToken(t, d, bearer, "synthetic")
}

// Via: two clients and the dashboard give three distinct origins, in the audit log and in the
// verdict history that the maintainer reads; agents never see via.
func TestMaintainerViaOrigins(t *testing.T) {
	d, root, bearer := maintainerDaemon(t)
	s, ctx := d.store, context.Background()
	a := join(t, s, "codex", "synthetic-via-a", "")
	aKey := Key{"codex", a.ID}
	for _, ref := range []string{"VIA-1", "VIA-2", "VIA-3"} {
		mustCreate(t, s, aKey, ref, DecisionFields{Title: ptr("Synthetic " + ref), Kind: ptr("decision"), Recipients: &[]string{a.Name}})
	}
	one, two := newMaintainerToken(t, d, bearer, "client-one"), newMaintainerToken(t, d, bearer, "client-two")
	mustMaintainer(t, d, bearer, one, "decision-verdict", map[string]any{"reference": "VIA-1", "revision": 1, "verdict": "approved"})
	mustMaintainer(t, d, bearer, two, "decision-verdict", map[string]any{"reference": "VIA-2", "revision": 1, "verdict": "declined"})
	client := newActionClient(t, d, root)
	if got := client.do("decision-approve", url.Values{"reference": {"VIA-3"}, "revision": {"1"}}); got != "decision_approved" {
		t.Fatalf("dashboard verdict: %s", got)
	}
	// Refused actions record the origin too.
	refusedMaintainer(t, d, bearer, one, "decision-verdict", map[string]any{"reference": "VIA-1", "revision": 1, "verdict": "approved"}, "decided")
	if got := client.do("decision-approve", url.Values{"reference": {"VIA-1"}, "revision": {"1"}}); got != "decided" {
		t.Fatalf("dashboard refusal: %s", got)
	}
	var records []string
	for _, r := range auditAll(t, s) {
		if strings.HasPrefix(r.Action, "decision-") {
			records = append(records, r.Result+" "+r.Target)
		}
	}
	all := strings.Join(records, "\n")
	for _, want := range []string{"accepted decision VIA-1 revision 1 approved verdict 1 by mcp:client-one",
		"accepted decision VIA-2 revision 1 declined verdict 2 by mcp:client-two", "accepted decision VIA-3 revision 1 approved verdict 3 by dashboard",
		"refused decision VIA-1 by mcp:client-one", "refused decision VIA-1 by dashboard"} {
		if !strings.Contains(all+"\n", want+"\n") {
			t.Fatalf("no record %q in:\n%s", want, all)
		}
	}
	if len(records) != 5 {
		t.Fatalf("records:\n%s", all)
	}
	// The maintainer's history and the dashboard show via; an agent's history does not.
	for ref, via := range map[string]string{"VIA-1": "mcp:client-one", "VIA-2": "mcp:client-two", "VIA-3": "dashboard"} {
		page, err := s.maintainerHistory(ctx, ref, 0, 0)
		if err != nil || len(page.Verdicts) != 1 || page.Verdicts[0].Via != via {
			t.Fatalf("%s history: %+v %v", ref, page, err)
		}
		agent, err := s.DecisionHistory(ctx, aKey, ref, 0, 0, false)
		if err != nil || len(agent.Verdicts) != 1 || agent.Verdicts[0].Via != "" {
			t.Fatalf("%s agent history: %+v %v", ref, agent, err)
		}
		view := dashboardDo(t, d, "GET", "/dashboard/decisions?ref="+ref, client.cookie, nil, nil)
		if view.status != 200 || !strings.Contains(view.body, ", via "+via) {
			t.Fatalf("%s board: %d", ref, view.status)
		}
	}
	status, reply := post(t, d.Addresses()[0], bearer, "/v1/decisions/history", map[string]any{"caller": aKey, "reference": "VIA-1"})
	data, _ := json.Marshal(reply)
	if status != 200 || strings.Contains(string(data), "via") || strings.Contains(string(data), "client-one") {
		t.Fatalf("agent route: %d %s", status, data)
	}
	got, err := s.DecisionGet(ctx, aKey, "VIA-1")
	if err != nil || got.Verdict == nil || got.Verdict.Via != "" {
		t.Fatalf("agent get: %+v %v", got, err)
	}
	// Each client's acknowledgement records its own origin; the dashboard's records dashboard.
	for i, ack := range []func(){
		func() { mustMaintainer(t, d, bearer, one, "ack", map[string]any{"through": 1}) },
		func() { mustMaintainer(t, d, bearer, two, "ack", map[string]any{"through": 2}) },
		func() {
			if got := client.do("acknowledge", url.Values{"family": {"maintainer"}, "id": {"maintainer"}, "through": {"3"}}); got != "acknowledged" {
				t.Fatalf("dashboard ack: %s", got)
			}
		},
	} {
		if _, err := s.Send(ctx, aKey, "maintainer", "synthetic "+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
		ack()
	}
	rows, err := s.db.Query(`SELECT ack_via FROM messages WHERE recipient_family='maintainer' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	var vias []string
	for rows.Next() {
		var via string
		rows.Scan(&via)
		vias = append(vias, via)
	}
	rows.Close()
	if strings.Join(vias, ",") != "mcp:client-one,mcp:client-two,dashboard" {
		t.Fatalf("ack via: %v", vias)
	}
}

// No presented or correct secret reaches the audit log, the daemon log, the store or a reply.
func TestMaintainerSecretTextStaysOut(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	seedState(t, root)
	var mu sync.Mutex
	var log strings.Builder
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, LogEvent: func(event string, fields map[string]any) {
		data, _ := json.Marshal(fields)
		mu.Lock()
		log.WriteString(event + " " + string(data) + "\n")
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.store.secretCost = 1000
	if _, err := d.changeSecret(context.Background(), "set", testSecret); err != nil {
		t.Fatal(err)
	}
	bearer, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	wrong := "synthetic wrong secret " + strings.Repeat("q", 12)
	var replies strings.Builder
	for _, secret := range []string{wrong, testSecret} {
		_, reply := post(t, d.Addresses()[0], bearer, "/v1/maintainer/session", map[string]any{"secret": secret, "client": "synthetic"})
		data, _ := json.Marshal(reply)
		replies.Write(data)
	}
	d.store.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	mu.Lock()
	daemonLog := log.String()
	mu.Unlock()
	for name, text := range map[string]string{"audit": auditRows(t, d), "daemon log": daemonLog, "store": storeBytes(t, root), "replies": replies.String()} {
		if strings.Contains(text, testSecret) || strings.Contains(text, wrong) || strings.Contains(text, "qqqqqqqq") {
			t.Fatalf("%s holds a secret", name)
		}
	}
}

func TestSchemaThirtyOneKeepsState(t *testing.T) {
	root := t.TempDir()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	a := join(t, s, "codex", "synthetic-a", "")
	mustCreate(t, s, Key{"codex", a.ID}, "S-1", DecisionFields{Title: ptr("Synthetic"), Kind: ptr("decision")})
	mustVerdict(t, s, "S-1", 1, "approved")
	if _, err := fixtureExec(s, undoSchemaThirtyFour+undoSchemaThirtyThree+undoSchemaThirtyTwo+undoSchemaThirtyOne+`PRAGMA user_version=30`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 30); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, s.db)
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || count(t, upgraded, `SELECT COUNT(*) FROM decision_verdicts WHERE via=''`) != 1 ||
		rowsOf(t, upgraded.db) != rows {
		t.Fatal("schema 31 changed retained state")
	}
	page, err := upgraded.maintainerHistory(context.Background(), "S-1", 0, 0)
	if err != nil || len(page.Verdicts) != 1 || page.Verdicts[0].Via != "" {
		t.Fatalf("history of an earlier verdict: %+v %v", page, err)
	}
	if err := checkCatalog(upgraded.db, 30); err == nil {
		t.Fatal("a schema 30 writer would accept the schema 31 store")
	}
}
