package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The change versions of the maintainer mode (PDR 009, Decision 3). All sessions, items,
// rooms and messages are synthetic.

func versionsOf(t *testing.T, s *Store) map[string]string {
	t.Helper()
	versions, err := s.changeVersions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return versions
}

// changeFixture is a frozen-clock store with an agent session that outlives every clock step.
func changeFixture(t *testing.T) *verdictFixture {
	t.Helper()
	f := newVerdictFixture(t)
	if _, err := fixtureExec(f.s, `UPDATE sessions SET expires_at=9007199254740991`); err != nil {
		t.Fatal(err)
	}
	return f
}

// moves runs write and checks that exactly the version of uri changed.
func moves(t *testing.T, s *Store, uri string, write func()) {
	t.Helper()
	before := versionsOf(t, s)
	write()
	if changed := changedURIs(before, versionsOf(t, s)); !slices.Equal(changed, []string{uri}) {
		t.Fatalf("changed %v, want %s", changed, uri)
	}
}

// stays runs write and checks that no version changed.
func stays(t *testing.T, s *Store, name string, write func()) {
	t.Helper()
	before := versionsOf(t, s)
	write()
	if changed := changedURIs(before, versionsOf(t, s)); len(changed) != 0 {
		t.Fatalf("%s changed %v", name, changed)
	}
}

// Each write that changes what a maintainer read shows moves its topic's version in its own
// transaction, with the clock frozen, and only that topic's.
func TestChangeVersionsMoveWithEachWrite(t *testing.T) {
	f := changeFixture(t)
	s, ctx := f.s, context.Background()
	inbox, decisions := MaintainerURI+"inbox", MaintainerURI+"decisions"
	seen := map[string]bool{}
	decisionMoves := func(write func()) {
		t.Helper()
		moves(t, s, decisions, write)
		version := versionsOf(t, s)[decisions]
		if seen[version] {
			t.Fatalf("decision version %s repeats", version)
		}
		seen[version] = true
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var verdicts []int64
	t.Run("decision create", func(t *testing.T) {
		decisionMoves(func() {
			mustCreate(t, s, f.peerKey, "C-1", DecisionFields{Title: ptr("Synthetic"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
		})
	})
	t.Run("decision edit", func(t *testing.T) {
		// The negative control: with the clock frozen, an edit from revision 1 to 2 leaves the
		// aggregate of update times and rows as it was, so only a counter shows it.
		aggregate := func() string {
			var at, rows int64
			if err := s.db.QueryRow(`SELECT MAX(updated_at),COUNT(*) FROM decision_items`).Scan(&at, &rows); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprint(at, " ", rows)
		}
		before := aggregate()
		decisionMoves(func() {
			_, err := s.DecisionEdit(ctx, f.peerKey, "C-1", 1, DecisionFields{Title: ptr("Synthetic, edited")})
			must(err)
		})
		if aggregate() != before {
			t.Fatal("the frozen-clock edit changed the aggregate; the control no longer shows the counter's need")
		}
	})
	t.Run("decision verdict", func(t *testing.T) {
		decisionMoves(func() { verdicts = append(verdicts, mustVerdict(t, s, "C-1", 2, "approved").ID) })
	})
	t.Run("decision close", func(t *testing.T) {
		decisionMoves(func() {
			_, err := s.DecisionClose(ctx, f.peerKey, "C-1", 2, "Done.", "close-1", s.clock()+600)
			must(err)
		})
	})
	t.Run("decision reopen", func(t *testing.T) {
		decisionMoves(func() {
			_, err := s.DecisionEdit(ctx, f.peerKey, "C-1", 2, DecisionFields{Context: ptr("Reopened.")})
			must(err)
		})
	})
	t.Run("decision archive", func(t *testing.T) {
		decisionMoves(func() { verdicts = append(verdicts, mustVerdict(t, s, "C-1", 3, "declined").ID) })
	})
	t.Run("decision removal", func(t *testing.T) {
		if _, err := s.DecisionAck(ctx, f.peerKey, verdicts); err != nil {
			t.Fatal(err)
		}
		*f.clock = f.clock.Add(31 * 24 * time.Hour)
		decisionMoves(func() { must(s.archiveDecisions(ctx)) })
		if count(t, s, `SELECT COUNT(*) FROM decision_items`) != 0 {
			t.Fatal("the item did not move to the deep archive")
		}
	})
	t.Run("decision restore", func(t *testing.T) {
		decisionMoves(func() {
			_, err := s.DecisionRestore(ctx, f.peerKey, "C-1", 0)
			must(err)
		})
	})
	t.Run("decision withdraw and reopen", func(t *testing.T) {
		mustCreate(t, s, f.peerKey, "C-2", decisionFields("Withdraw me", "fyi"))
		decisionMoves(func() {
			_, err := s.DecisionWithdraw(ctx, f.peerKey, "C-2", 1, "done", "withdraw-1", s.clock()+600)
			must(err)
		})
		decisionMoves(func() {
			_, err := s.DecisionRestore(ctx, f.peerKey, "C-2", 1)
			must(err)
		})
	})
	t.Run("inbox insert", func(t *testing.T) {
		moves(t, s, inbox, func() {
			_, err := s.Send(ctx, f.peerKey, "maintainer", "a synthetic report")
			must(err)
		})
	})
	t.Run("inbox daemon notice", func(t *testing.T) {
		moves(t, s, inbox, func() {
			tx, err := s.begin(ctx, control)
			must(err)
			if err := s.noticeMaintainer(ctx, tx, s.now().UnixMilli(), "Koinon: a synthetic notice."); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			must(tx.Commit())
		})
	})
	t.Run("inbox ack", func(t *testing.T) {
		moves(t, s, inbox, func() {
			_, err := s.ack(ctx, maintainerKey, 2, true)
			must(err)
		})
	})
	t.Run("inbox retention", func(t *testing.T) {
		var counts RetentionCounts
		budget := int64(cullBudget)
		stays(t, s, "the acknowledgement mark", func() { must(s.deleteAcknowledged(ctx, &counts, &budget)) })
		*f.clock = f.clock.Add(31 * 24 * time.Hour)
		moves(t, s, inbox, func() { must(s.deleteAcknowledged(ctx, &counts, &budget)) })
		if count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_family='maintainer'`) != 0 {
			t.Fatal("retention kept the maintainer's acknowledged messages")
		}
	})
	var room string
	t.Run("room create", func(t *testing.T) {
		before := versionsOf(t, s)
		room = mustRoom(t, s, "synthetic changes")
		if changed := changedURIs(before, versionsOf(t, s)); !slices.Equal(changed, []string{MaintainerURI + "rooms/" + room}) {
			t.Fatalf("create changed %v", changed)
		}
	})
	roomURI := MaintainerURI + "rooms/" + room
	t.Run("room add", func(t *testing.T) {
		moves(t, s, roomURI, func() { mustAdd(t, s, room, f.peer.Name) })
	})
	t.Run("room post", func(t *testing.T) {
		moves(t, s, roomURI, func() { mustHumanPost(t, s, room, "a synthetic post") })
	})
	t.Run("room remove", func(t *testing.T) {
		moves(t, s, roomURI, func() { must(s.RemoveRoomMember(roomCtx("room-remove"), room, 1)) })
	})
	t.Run("room post retention", func(t *testing.T) {
		// The post's only copy is gone, so the cull deletes the post.
		if _, err := fixtureExec(s, `DELETE FROM messages WHERE room_event='post'`); err != nil {
			t.Fatal(err)
		}
		budget := int64(cullBudget)
		moves(t, s, roomURI, func() { must(s.cullRoomPosts(ctx, &budget)) })
		if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 0 {
			t.Fatal("the cull kept the post")
		}
	})
	t.Run("room delete", func(t *testing.T) {
		moves(t, s, roomURI, func() { must(s.DeleteRoom(roomCtx("room-delete"), room)) })
		if _, ok := versionsOf(t, s)[roomURI]; ok {
			t.Fatal("a deleted room has a version")
		}
	})
}

// A retry that writes nothing, a refused write and a current member's add move no version.
func TestChangeVersionsStayForRetriesAndRefusals(t *testing.T) {
	f := changeFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "R-1", DecisionFields{Title: ptr("Synthetic"), Kind: ptr("decision")})
	room := mustRoom(t, s, "synthetic retries")
	mustAdd(t, s, room, f.peer.Name)
	deadline := s.clock() + 600
	if _, err := s.DashboardRoomPost(roomCtx("room-post"), room, "first", "post-key", deadline); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, f.peerKey, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ack(ctx, maintainerKey, 1, true); err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"decision create retry": func() error {
			_, err := s.DecisionCreate(ctx, f.peerKey, "R-1", DecisionFields{Title: ptr("Synthetic"), Kind: ptr("decision")}, "key-R-1", s.clock()+600)
			return err
		},
		"room create retry": func() error {
			_, err := s.CreateRoom(roomCtx("room-create"), "key-synthetic retries", "synthetic retries")
			return err
		},
		"room post retry": func() error {
			r, err := s.DashboardRoomPost(roomCtx("room-post"), room, "first", "post-key", deadline)
			if err == nil && !r.Duplicate {
				err = errors.New("not a duplicate")
			}
			return err
		},
		"current member add": func() error {
			changed, err := s.AddRoomMember(roomCtx("room-add"), room, f.peer.Name)
			if err == nil && changed {
				err = errors.New("changed")
			}
			return err
		},
		"ack again": func() error {
			_, err := s.ack(ctx, maintainerKey, 1, true)
			return err
		},
	} {
		stays(t, s, name, func() {
			if err := write(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
	for name, write := range map[string]func() error{
		"stale verdict": func() error {
			_, err := s.DecisionVerdict(verdictCtx(), "R-1", 2, "approved", "")
			return err
		},
		"stale edit": func() error {
			_, err := s.DecisionEdit(ctx, f.peerKey, "R-1", 2, DecisionFields{Title: ptr("x")})
			return err
		},
		"ack beyond the last": func() error {
			_, err := s.ack(ctx, maintainerKey, 9, true)
			return err
		},
	} {
		stays(t, s, name, func() {
			if write() == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

// A write that moved its version and then failed at the commit's ceiling rolls the version
// back with everything else.
func TestChangeVersionsRollBackWithTheirWrite(t *testing.T) {
	f := changeFixture(t)
	s, ctx := f.s, context.Background()
	room := mustRoom(t, s, "synthetic rollback")
	before := versionsOf(t, s)
	pages, debt := count(t, s, `PRAGMA page_count`), mustDebt(t, s)
	// Admission passes; the body's overflow pages then exceed the ceiling at the commit.
	s.storage.mu.Lock()
	limit := s.storage.maxPages
	s.storage.maxPages = pages + reservePages + commitSlack + debt + appendAllowance
	s.storage.mu.Unlock()
	body := strings.Repeat("x", maxBody)
	if _, err := s.DashboardRoomPost(roomCtx("room-post"), room, body, "big-post", s.clock()+600); code(err) != "capacity" {
		t.Fatalf("a large post at the ceiling: %v", err)
	}
	if _, err := s.Send(ctx, f.peerKey, "maintainer", body); code(err) != "capacity" {
		t.Fatalf("a large message at the ceiling: %v", err)
	}
	s.storage.mu.Lock()
	s.storage.maxPages = limit
	s.storage.mu.Unlock()
	if changed := changedURIs(before, versionsOf(t, s)); len(changed) != 0 || count(t, s, `SELECT post_head FROM rooms`) != 0 {
		t.Fatalf("rolled-back writes changed %v", changed)
	}
}

// With ordinary storage full, the control writes still move their versions in place: an
// acknowledgement, a daemon notice, a room deletion and the post retention. The counters'
// width is fixed, so their increments allocate no page.
func TestChangeVersionsAtAFullStore(t *testing.T) {
	f := changeFixture(t)
	s, ctx := f.s, context.Background()
	room := mustRoom(t, s, "synthetic full")
	mustAdd(t, s, room, f.peer.Name)
	mustHumanPost(t, s, room, "a synthetic post")
	if _, err := s.Send(ctx, f.peerKey, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	pages, debt := count(t, s, `PRAGMA page_count`), mustDebt(t, s)
	s.storage.mu.Lock()
	s.storage.maxPages = pages + reservePages + commitSlack + debt + appendAllowance - 1
	s.storage.mu.Unlock()
	inbox, roomURI := MaintainerURI+"inbox", MaintainerURI+"rooms/"+room
	stays(t, s, "a refused send", func() {
		if _, err := s.Send(ctx, f.peerKey, "maintainer", "refused"); code(err) != "capacity" {
			t.Fatalf("send at a full store: %v", err)
		}
	})
	stays(t, s, "a refused post", func() {
		if _, err := s.DashboardRoomPost(roomCtx("room-post"), room, "refused", key(), s.clock()+600); code(err) != "capacity" {
			t.Fatalf("post at a full store: %v", err)
		}
	})
	moves(t, s, inbox, func() {
		if _, err := s.ack(ctx, maintainerKey, 1, true); err != nil {
			t.Fatal(err)
		}
	})
	moves(t, s, inbox, func() {
		tx, err := s.begin(ctx, control)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.noticeMaintainer(ctx, tx, s.now().UnixMilli(), "Koinon: a synthetic notice."); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := fixtureExec(s, `DELETE FROM messages WHERE room_event='post'`); err != nil {
		t.Fatal(err)
	}
	budget := int64(cullBudget)
	moves(t, s, roomURI, func() {
		if err := s.cullRoomPosts(ctx, &budget); err != nil {
			t.Fatal(err)
		}
	})
	moves(t, s, roomURI, func() {
		if err := s.DeleteRoom(roomCtx("room-delete"), room); err != nil {
			t.Fatal(err)
		}
	})
	// Increments overwrite the counter in place, also across a change in its digits.
	if _, err := fixtureExec(s, `UPDATE change_versions SET version=printf('%020d',99) WHERE topic='inbox'`); err != nil {
		t.Fatal(err)
	}
	before := count(t, s, `PRAGMA page_count`)
	for range 200 {
		tx, err := s.begin(ctx, control)
		if err != nil {
			t.Fatal(err)
		}
		if err := bumpChange(ctx, tx, changeInbox); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if got := versionsOf(t, s)[inbox]; got != "00000000000000000299" || count(t, s, `PRAGMA page_count`) != before {
		t.Fatalf("version %s, pages %d then %d", got, before, count(t, s, `PRAGMA page_count`))
	}
}

// A deleted room's URI leaves the map, and a room created again with the same form key is a
// new room with a new URI; close and reopen give the board new versions each time.
func TestChangeVersionsRoomLifecycle(t *testing.T) {
	s, _ := testStore(t)
	first := mustRoom(t, s, "synthetic lifecycle")
	before := versionsOf(t, s)
	if err := s.DeleteRoom(roomCtx("room-delete"), first); err != nil {
		t.Fatal(err)
	}
	second := mustRoom(t, s, "synthetic lifecycle")
	after := versionsOf(t, s)
	want := []string{MaintainerURI + "rooms/" + first, MaintainerURI + "rooms/" + second}
	slices.Sort(want)
	if second == first || !slices.Equal(changedURIs(before, after), want) || after[MaintainerURI+"rooms/"+second] != "00000000000000000001" {
		t.Fatalf("delete and create: %v %v", changedURIs(before, after), after)
	}
}

// Schema 32 starts a counter for the inbox, the board and each existing room, and keeps
// every retained row.
func TestSchemaThirtyTwoStartsCounters(t *testing.T) {
	root := t.TempDir()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	rooms := []string{mustRoom(t, s, "synthetic one"), mustRoom(t, s, "synthetic two")}
	a := join(t, s, "codex", "synthetic-a", "")
	if _, err := s.Send(context.Background(), Key{"codex", a.ID}, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(s, undoSchemaThirtyFour+undoSchemaThirtyThree+undoSchemaThirtyTwo+`PRAGMA user_version=31`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 31); err != nil {
		t.Fatal(err)
	}
	rows := rowsOf(t, s.db)
	s.db.Close()
	upgraded, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	want := map[string]string{MaintainerURI + "inbox": "00000000000000000001", MaintainerURI + "decisions": "00000000000000000001"}
	for _, room := range rooms {
		want[MaintainerURI+"rooms/"+room] = "00000000000000000001"
	}
	got := versionsOf(t, upgraded)
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != rows || len(changedURIs(want, got)) != 0 {
		t.Fatalf("schema 32: %v", got)
	}
	if err := checkCatalog(upgraded.db, 31); err == nil {
		t.Fatal("a schema 31 writer would accept the schema 32 store")
	}
}

// The changes route over HTTP: its bounds, an immediate read without a cursor, an early
// return on a change, writes while a poll waits, and a token that ends during a poll.
func TestMaintainerChangesRoute(t *testing.T) {
	d, _, bearer := maintainerDaemon(t)
	s, ctx := d.store, context.Background()
	address := d.Addresses()[0]
	a := join(t, s, "codex", "synthetic-changes-a", "")
	token := newMaintainerToken(t, d, bearer, "synthetic")
	for _, body := range []map[string]any{{"wait_seconds": 0}, {"wait_seconds": 31}, {"wait_seconds": 1, "cursor": map[string]string{"x": "1"}},
		{"wait_seconds": 1, "since": 1}} {
		refusedMaintainer(t, d, bearer, token, "changes", body, "invalid_request")
	}
	now := mustMaintainer(t, d, bearer, token, "changes", map[string]any{"wait_seconds": 30})
	cursor := map[string]string{}
	for uri, version := range now["cursor"].(map[string]any) {
		cursor[uri] = version.(string)
	}
	if len(cursor) != 2 || cursor[MaintainerURI+"inbox"] == "" || len(now["changed"].([]any)) != 0 {
		t.Fatalf("without a cursor: %v", now)
	}
	started := time.Now()
	if quiet := mustMaintainer(t, d, bearer, token, "changes", map[string]any{"wait_seconds": 1, "cursor": cursor}); len(quiet["changed"].([]any)) != 0 ||
		time.Since(started) < time.Second {
		t.Fatalf("a quiet poll: %v after %v", quiet, time.Since(started))
	}
	poll := func(wait int) chan map[string]any {
		replies := make(chan map[string]any, 1)
		go func() {
			data, err := CallTimeout(ctx, address, bearer, "/v1/maintainer/changes", map[string]any{"token": token, "wait_seconds": wait, "cursor": cursor},
				time.Duration(wait+5)*time.Second)
			reply := map[string]any{}
			if err != nil {
				reply["error"] = errorCode(err)
				var refused RefusedError
				if errors.As(err, &refused) {
					reply["error"] = refused.Code
				}
			} else {
				json.Unmarshal(data, &reply)
			}
			replies <- reply
		}()
		return replies
	}
	// Ordinary writes proceed while a poll waits; the poll returns early with the URI.
	replies := poll(20)
	time.Sleep(700 * time.Millisecond)
	started = time.Now()
	if _, err := s.Send(ctx, Key{"codex", a.ID}, "maintainer", "a synthetic report"); err != nil {
		t.Fatal(err)
	}
	mustMaintainer(t, d, bearer, token, "room-create", map[string]any{"name": "synthetic room", "key": "k1"})
	if time.Since(started) > 2*time.Second {
		t.Fatalf("writes during a poll took %v", time.Since(started))
	}
	select {
	case reply := <-replies:
		changed := reply["result"].(map[string]any)["changed"].([]any)
		if len(changed) == 0 || changed[0] != MaintainerURI+"inbox" {
			t.Fatalf("an early return: %v", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not return on a change")
	}
	// A secret change ends the token during the poll.
	cursor = versionsOf(t, s)
	replies = poll(20)
	time.Sleep(300 * time.Millisecond)
	if _, err := d.changeSecret(ctx, "set", "another synthetic secret"); err != nil {
		t.Fatal(err)
	}
	select {
	case reply := <-replies:
		if reply["error"] != "maintainer_token_expired" {
			t.Fatalf("a poll after the secret changed: %v", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not end with the token")
	}
}

// A poll that waits past the server's 10-second deadlines returns normally, and a daemon
// stop ends a waiting poll without delaying the stop.
func TestMaintainerChangesLongPoll(t *testing.T) {
	d, _, bearer := maintainerDaemon(t)
	token := newMaintainerToken(t, d, bearer, "synthetic")
	cursor := versionsOf(t, d.store)
	started := time.Now()
	data, err := CallTimeout(context.Background(), d.Addresses()[0], bearer, "/v1/maintainer/changes",
		map[string]any{"token": token, "wait_seconds": 12, "cursor": cursor}, 17*time.Second)
	var reply struct {
		OK     bool `json:"ok"`
		Result struct {
			Cursor  map[string]string `json:"cursor"`
			Changed []string          `json:"changed"`
		} `json:"result"`
	}
	if err != nil || json.Unmarshal(data, &reply) != nil || !reply.OK || len(reply.Result.Changed) != 0 || len(changedURIs(cursor, reply.Result.Cursor)) != 0 ||
		time.Since(started) < 12*time.Second {
		t.Fatalf("a 12-second poll: %s %v after %v", data, err, time.Since(started))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		CallTimeout(context.Background(), d.Addresses()[0], bearer, "/v1/maintainer/changes",
			map[string]any{"token": token, "wait_seconds": 30, "cursor": cursor}, 35*time.Second)
	}()
	time.Sleep(500 * time.Millisecond)
	started = time.Now()
	if err := d.Close(); err != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("close during a poll: %v after %v", err, time.Since(started))
	}
	wg.Wait()
}

// Each poll is an authenticated request, so a client that only polls keeps its token past
// the idle limit; a check during a poll's wait does not move the limit.
func TestMaintainerChangesRefreshIdle(t *testing.T) {
	d, advance := secretDaemon(t)
	mux := http.NewServeMux()
	d.maintainerSessionRoutes(mux)
	token, err := d.passwordLogin(context.Background(), testSecret, "mcp:synthetic")
	if err != nil {
		t.Fatal(err)
	}
	poll := func() string {
		body, _ := json.Marshal(map[string]any{"token": token, "wait_seconds": 1})
		r := httptest.NewRequest("POST", "/v1/maintainer/changes", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var reply struct {
			Code string `json:"code"`
		}
		json.Unmarshal(w.Body.Bytes(), &reply)
		return reply.Code
	}
	for range 3 {
		advance(sessionIdle - time.Millisecond)
		if got := poll(); got != "" {
			t.Fatalf("a poll within the idle limit: %s", got)
		}
		if !d.auth.live(token) {
			t.Fatal("the token ended while polled")
		}
	}
	advance(sessionIdle - time.Millisecond)
	if !d.auth.live(token) {
		t.Fatal("a check ended the token")
	}
	advance(time.Millisecond)
	if d.auth.live(token) || poll() != "maintainer_token_expired" {
		t.Fatal("a check moved the idle limit")
	}
}

// A work notice to an agent moves no version; a work notice to the maintainer, for a report on
// an item that the maintainer created, moves only the inbox's, with the clock frozen.
func TestChangeVersionsMoveWithWorkNotices(t *testing.T) {
	s, _, creator, owner, _, _ := noticePair(t)
	maintainer := maintainerWork(creator.Repository, maintainerConsumer)
	id := createRoleWork(t, s, maintainer, "codex", "worker")
	stays(t, s, "a work notice to an agent", func() { mustReconcileNotices(t, s) })
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon' AND recipient_family='codex'`) != 1 ||
		count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_family='maintainer'`) != 0 {
		t.Fatal("the assignment notice did not reach only the worker")
	}
	mustReport(t, s, owner, id, mustStart(t, s, owner, id, nil))
	moves(t, s, MaintainerURI+"inbox", func() { mustReconcileNotices(t, s) })
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE sender_family='daemon' AND recipient_family='maintainer'`) != 1 {
		t.Fatal("the report notice did not reach the maintainer")
	}
}
