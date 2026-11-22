package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// #695 (docs/sprints/2026-11-22-signed-answers/contract.md, section 1): the maintainer's post
// answers each member's result in roster order, with the reason that the store found; nothing
// that was not sent is delivered later.
func TestRoomPostRecipients(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	active := join(t, s, "codex", "synthetic-active", "")
	ended := join(t, s, "claude", "synthetic-ended", "")
	lapsed := join(t, s, "agy", "synthetic-lapsed", "")
	unproven := join(t, s, "claude", "synthetic-unproven", "")
	retired := join(t, s, "codex", "synthetic-retired", "")
	deleted := join(t, s, "agy", "synthetic-deleted", "")
	unheld := joinAs(t, s, "codex", "synthetic-holder", repo, "")
	conflicting := joinAs(t, s, "claude", "synthetic-conflict", repo, "")
	room := mustRoom(t, s, "synthetic recipients")
	members := []Session{active, ended, lapsed, unproven, retired, deleted, unheld, conflicting}
	for _, m := range members {
		mustAdd(t, s, room, m.Name)
	}
	start, err := platform.ProcessStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		query string
		args  []any
	}{
		// A host process that is gone; a host that runs but whose registration expired; no
		// host record; a retired session; a participant with no holder; one with a conflict.
		{`UPDATE sessions SET expires_at=1,host_pid=2147483646,host_start=7 WHERE id='synthetic-ended'`, nil},
		{`UPDATE sessions SET expires_at=1,host_pid=?,host_start=? WHERE id='synthetic-lapsed'`, []any{os.Getpid(), start}},
		{`UPDATE sessions SET expires_at=1,host_pid=0,host_start=0 WHERE id='synthetic-unproven'`, nil},
		{`UPDATE sessions SET retired_at=1 WHERE id='synthetic-retired'`, nil},
		// Retention deleted an expired session that is still on the roster.
		{`DELETE FROM sessions WHERE id='synthetic-deleted'`, nil},
		{`UPDATE names SET holder_id='' WHERE kind='alias' AND name='codex-koinon'`, nil},
		{`UPDATE names SET conflict='["x","y"]',holder_id='' WHERE kind='alias' AND name='claude-koinon'`, nil},
	} {
		if _, err := fixtureExec(s, q.query, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	want := []RoomRecipient{
		{active.Name, "sent", ""},
		{ended.Name, "not_sent", "ended"},
		{lapsed.Name, "not_sent", "lapsed"},
		{unproven.Name, "not_sent", "no_liveness_evidence"},
		{retired.Name, "not_sent", "retired"},
		{deleted.Name, "not_sent", "no_liveness_evidence"},
		{"codex-koinon", "not_sent", "unheld"},
		{"claude-koinon", "not_sent", "conflicting"},
	}
	postKey, deadline := key(), s.clock()+600
	first, err := s.DashboardRoomPost(roomCtx("room-post"), room, "synthetic real-time post", postKey, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first.Recipients) != fmt.Sprint(want) || first.Queued != 1 || first.Skipped != 7 {
		t.Fatalf("recipients: %+v", first)
	}
	// The same key answers the stored list after the roster and the states change.
	mustAdd(t, s, room, join(t, s, "agy", "synthetic-late", "").Name)
	if _, err := fixtureExec(s, `UPDATE sessions SET retired_at=1 WHERE id='synthetic-active'`); err != nil {
		t.Fatal(err)
	}
	again, err := s.DashboardRoomPost(roomCtx("room-post"), room, "synthetic real-time post", postKey, deadline)
	if err != nil || fmt.Sprint(again.Recipients) != fmt.Sprint(want) {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	// Only the sent member has a copy, and later posts and wakes give the others none.
	if got := count(t, s, `SELECT COUNT(*) FROM messages WHERE body='synthetic real-time post'`); got != 1 {
		t.Fatalf("copies: %d", got)
	}
	// The answer shapes: the maintainer's names each member; a post of this release with no
	// other member has an empty list; a receipt stored before #695 has none.
	shaped := jsonMap(t, maintainerPostOf(first))
	if list, ok := shaped["recipients"].([]any); !ok || len(list) != len(want) {
		t.Fatalf("maintainer answer: %v", shaped)
	}
	alone := mustRoom(t, s, "synthetic empty room")
	empty, err := s.DashboardRoomPost(roomCtx("room-post"), alone, "synthetic post to nobody", postKey+"-alone", s.clock()+600)
	if err != nil || string(asJSON(t, maintainerPostOf(empty).Recipients)) != "[]" {
		t.Fatalf("empty room: %+v %v", empty, err)
	}
	var legacy RoomPostResult
	if err := json.Unmarshal([]byte(`{"room_id":"`+room+`","seq":1,"queued":1,"skipped":0}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if _, has := jsonMap(t, maintainerPostOf(legacy))["recipients"]; has {
		t.Fatal("an earlier receipt answered a list")
	}
	if _, has := jsonMap(t, first.member())["recipients"]; has {
		t.Fatal("the agent's answer names the members")
	}
	// The Rooms page says each reason in words; an earlier row shows as stored.
	for reason, words := range map[string]string{"ended": "session ended", "unheld": "address not held", "retired": "retired", "inactive": "inactive"} {
		if got := (RoomDelivery{Reason: reason}).ReasonText(); got != words {
			t.Fatalf("%s: %q", reason, got)
		}
	}
}

// #695: a signed send refused for an inactive recipient keeps its reason in the receipt, so the
// repeat and outcome give it; a signed room post's answer and outcome give the stored list
// after the roster changes and after a restart.
func TestSignedRecipientResults(t *testing.T) {
	d, root := startTestDaemon(t)
	s, ctx := d.store, context.Background()
	active := join(t, s, "codex", "synthetic-signed-active", "")
	gone := join(t, s, "claude", "synthetic-signed-gone", "")
	room := mustRoom(t, s, "synthetic signed recipients")
	mustAdd(t, s, room, active.Name)
	mustAdd(t, s, room, gone.Name)
	if _, err := fixtureExec(s, `UPDATE sessions SET retired_at=1 WHERE id='synthetic-signed-gone'`); err != nil {
		t.Fatal(err)
	}
	c := newSignedClient(t, d, 9)
	now := time.Now().Unix()

	// Each recipient refusal answers its repeat exactly as it first answered.
	repo := namedRepo(t, "signedunheld")
	joinAs(t, s, "codex", "synthetic-signed-holder", repo, "")
	if _, err := fixtureExec(s, `UPDATE names SET holder_id='' WHERE kind='alias' AND name='codex-signedunheld'`); err != nil {
		t.Fatal(err)
	}
	answer := func(result any, err error) string {
		w := httptest.NewRecorder()
		maintainerRead(w, result, err)
		return fmt.Sprintf("%d %s", w.Code, w.Body.String())
	}
	for to, code := range map[string]string{"synthetic-nobody": "peer_not_found", "codex-signedunheld": "alias_unheld", gone.Name: "recipient_inactive"} {
		envelope := c.envelope("send", map[string]any{"to": to, "body": "a synthetic instruction"}, c.request(), now, nil)
		result, err := d.SignedAction(ctx, envelope)
		first := answer(result, err)
		if errorCode(err) != code || first != answer(d.SignedAction(ctx, envelope)) {
			t.Fatalf("%s: %s, repeat %s", code, first, answer(d.SignedAction(ctx, envelope)))
		}
	}
	sendRequest := c.request()
	send := c.envelope("send", map[string]any{"to": gone.Name, "body": "a synthetic instruction"}, sendRequest, now, nil)
	if _, err := d.SignedAction(ctx, send); errorCode(err) != "recipient_inactive" {
		t.Fatalf("send: %v", err)
	}
	refused := c.must("outcome", map[string]any{"request": sendRequest, "created": now})
	if refused["state"] != "refused" || refused["code"] != "recipient_inactive" ||
		fmt.Sprint(refused["details"]) != "map[reason:retired]" {
		t.Fatalf("send outcome: %v", refused)
	}
	var repeat InactiveRecipient
	if _, err := d.SignedAction(ctx, send); !errors.As(err, &repeat) || repeat.Reason != "retired" {
		t.Fatalf("send repeat: %v", err)
	}

	postRequest := c.request()
	post := c.envelope("room_post", map[string]any{"room": room, "body": "a synthetic post"}, postRequest, now, nil)
	first, err := d.SignedAction(ctx, post)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint([]any{
		map[string]any{"name": active.Name, "state": "sent"},
		map[string]any{"name": gone.Name, "state": "not_sent", "reason": "retired"},
	})
	if got := jsonMap(t, first)["recipients"]; fmt.Sprint(got) != want {
		t.Fatalf("signed post: %s", asJSON(t, first))
	}
	mustAdd(t, s, room, join(t, s, "agy", "synthetic-signed-late", "").Name)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c.d = d
	again, err := d.SignedAction(ctx, post)
	if err != nil || string(asJSON(t, again)) != string(asJSON(t, first)) {
		t.Fatalf("repeat after restart: %s %v", asJSON(t, again), err)
	}
	outcome := c.must("outcome", map[string]any{"request": postRequest, "created": now})
	if outcome["state"] != "recorded" || fmt.Sprint(outcome["result"].(map[string]any)["recipients"]) != want {
		t.Fatalf("post outcome: %v", outcome)
	}
}
