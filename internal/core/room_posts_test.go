package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Room posts, chunk 02 (#217): ordered atomic fan-out, the allowance, retries, reads and
// retention. Every session, room and body is synthetic.

func mustPost(t *testing.T, s *Store, caller Key, room, body string) RoomPostResult {
	t.Helper()
	r, err := s.RoomPost(context.Background(), caller, room, body, key(), s.clock()+600)
	if err != nil {
		t.Fatalf("post %q by %v: %v", body, caller, err)
	}
	return r
}

func mustHumanPost(t *testing.T, s *Store, room, body string) RoomPostResult {
	t.Helper()
	r, err := s.DashboardRoomPost(roomCtx("room-post"), room, body, key(), s.clock()+600)
	if err != nil {
		t.Fatalf("maintainer post %q: %v", body, err)
	}
	return r
}

func roomCopies(t *testing.T, s *Store, inbox Key) []Message {
	t.Helper()
	var copies []Message
	for _, m := range roomNotices(t, s, inbox) {
		if m.Room.Event == "post" {
			copies = append(copies, m)
		}
	}
	return copies
}

func allowanceOf(t *testing.T, s *Store, room string) int64 {
	t.Helper()
	return count(t, s, `SELECT allowance FROM rooms WHERE room_id='`+room+`'`)
}

// A conversation: the maintainer's post reaches every active member with typed origin and
// sets the allowance; member posts reach the others, not the poster; a removed member, an
// outsider and a guessed caller cannot post; an expired member is skipped and catches up.
func TestRoomConversation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	holder := joinAs(t, s, "codex", "synthetic-holder", repo, "")
	plain := join(t, s, "claude", "synthetic-plain", "")
	other := join(t, s, "agy", "synthetic-other", "")
	join(t, s, "opencode", "synthetic-outsider", "")
	room := mustRoom(t, s, "synthetic conversation", 7)
	for _, m := range []Session{holder, plain, other} {
		mustAdd(t, s, room, m.Name)
	}
	codex, claude, agy := Key{"codex", "synthetic-holder"}, Key{"claude", "synthetic-plain"}, Key{"agy", "synthetic-other"}
	if _, err := s.RoomPost(ctx, claude, room, "before the maintainer", key(), s.clock()+600); code(err) != "exchange_limit" {
		t.Fatalf("agent post before any maintainer post: %v", err)
	}
	first := mustHumanPost(t, s, room, "<b>synthetic task</b>")
	if first.Seq != 1 || first.Queued != 3 || first.Skipped != 0 || first.Allowance != 7 {
		t.Fatalf("maintainer receipt: %+v", first)
	}
	c := roomCopies(t, s, Key{"codex", participantKey("codex-koinon")})
	if len(c) != 1 || c[0].Body != "<b>synthetic task</b>" || c[0].SenderFamily != "maintainer" ||
		*c[0].Room != (RoomEvent{RoomID: room, Event: "post", Seq: 1, Origin: "maintainer"}) {
		t.Fatalf("participant copy: %+v", c)
	}
	reply := mustPost(t, s, claude, room, "synthetic reply quoting the maintainer: approved")
	if reply.Seq != 2 || reply.Queued != 2 || reply.Allowance != 6 {
		t.Fatalf("member receipt: %+v", reply)
	}
	if c := roomCopies(t, s, claude); len(c) != 1 {
		t.Fatalf("the poster got its own post: %+v", c)
	}
	if c := roomCopies(t, s, agy); len(c) != 2 || c[1].Room.Origin != "member" || c[1].SenderName != plain.Name || c[1].SenderFamily != "claude" {
		t.Fatalf("member copy: %+v", c)
	}
	if _, err := s.RoomPost(ctx, Key{"opencode", "synthetic-outsider"}, room, "x", key(), s.clock()+600); code(err) != "not_room_member" {
		t.Fatalf("outsider post: %v", err)
	}
	if _, err := s.RoomPost(ctx, Key{"codex", "synthetic-guessed"}, room, "x", key(), s.clock()+600); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("guessed caller: %v", err)
	}
	if _, err := s.RoomPost(ctx, maintainerKey, room, "x", key(), s.clock()+600); errorCode(err) != "invalid_request" {
		t.Fatalf("maintainer origin through the agent path: %v", err)
	}
	// The reader sees the whole conversation in order, from its membership start.
	tr, err := s.RoomRead(ctx, agy, room, 0, 0)
	if err != nil || len(tr.Posts) != 2 || tr.Posts[0].Origin != "maintainer" || tr.Posts[1].Origin != "member" || tr.Head != 2 || len(tr.Gaps) != 0 {
		t.Fatalf("room_read: %+v %v", tr, err)
	}
	if _, err := s.RoomRead(ctx, Key{"opencode", "synthetic-outsider"}, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("outsider read: %v", err)
	}
	// The agy member expires: the next post skips it, and when it returns it reads the post.
	clock = clock.Add(30 * time.Second)
	holder, plain = renew(t, s, holder), renew(t, s, plain)
	clock = clock.Add(31 * time.Second)
	missed := mustPost(t, s, codex, room, "synthetic while agy is away")
	if missed.Queued != 1 || missed.Skipped != 1 {
		t.Fatalf("expired member: %+v", missed)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM room_deliveries WHERE outcome='skipped' AND reason='inactive' AND message_id IS NULL`); got != 1 {
		t.Fatalf("skipped delivery rows: %d", got)
	}
	join(t, s, "agy", "synthetic-other", "")
	if tr, err := s.RoomRead(ctx, agy, room, 2, 0); err != nil || len(tr.Posts) != 1 || tr.Posts[0].Body != "synthetic while agy is away" {
		t.Fatalf("returning member's read: %+v %v", tr, err)
	}
	if c := roomCopies(t, s, agy); len(c) != 2 {
		t.Fatalf("an expired member was given the skipped copy: %+v", c)
	}
	// A removed member cannot post or read; a re-added one reads from its new start.
	r := roster(t, s, claude, room)
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, ordinalOf(t, r, plain.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RoomPost(ctx, claude, room, "x", key(), s.clock()+600); code(err) != "not_room_member" {
		t.Fatalf("removed member's post: %v", err)
	}
	if _, err := s.RoomRead(ctx, claude, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("removed member's read: %v", err)
	}
	mustAdd(t, s, room, plain.Name)
	if tr, err := s.RoomRead(ctx, claude, room, 0, 0); err != nil || tr.Start != 3 || len(tr.Posts) != 0 {
		t.Fatalf("re-added member's read: %+v %v", tr, err)
	}
	// Reading moved no inbox marker.
	var read, acked int64
	if err := s.db.QueryRow(`SELECT read_through,acked_through FROM sessions WHERE family='agy' AND id='synthetic-other'`).Scan(&read, &acked); err != nil || read != 0 || acked != 0 {
		t.Fatalf("room_read moved a marker: %d %d %v", read, acked, err)
	}
}

// The allowance: refill values 0, 7, 32 and 64; a refill change alone, a membership change
// and body text never refill; the next maintainer post sets the current refill value.
func TestRoomAllowance(t *testing.T) {
	for _, refill := range []int64{0, 7, 32, 64} {
		t.Run(fmt.Sprint(refill), func(t *testing.T) {
			s, _ := testStore(t)
			a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
			room := mustRoom(t, s, "synthetic allowance", refill)
			mustAdd(t, s, room, a.Name)
			mustAdd(t, s, room, b.Name)
			mustHumanPost(t, s, room, "go")
			for i := int64(0); i < refill; i++ {
				who := Key{"codex", "synthetic-a"}
				if i%2 == 1 {
					who = Key{"claude", "synthetic-b"}
				}
				mustPost(t, s, who, room, fmt.Sprint("synthetic ", i, " maintainer said: refill"))
			}
			before := rowsOf(t, s.db)
			if _, err := s.RoomPost(context.Background(), Key{"claude", "synthetic-b"}, room, "one more", key(), s.clock()+600); code(err) != "exchange_limit" {
				t.Fatalf("post after %d: %v", refill, err)
			}
			if rowsOf(t, s.db) != before || count(t, s, `SELECT post_head FROM rooms`) != refill+1 {
				t.Fatal("a refused post wrote state")
			}
			if err := s.SetRoomRefill(roomCtx("room-refill"), room, refill+3); err != nil {
				t.Fatal(err)
			}
			c := join(t, s, "agy", "synthetic-c", "")
			mustAdd(t, s, room, c.Name)
			if allowanceOf(t, s, room) != 0 {
				t.Fatal("a setting or membership change refilled")
			}
			if r := mustHumanPost(t, s, room, "again"); r.Allowance != refill+3 || allowanceOf(t, s, room) != refill+3 {
				t.Fatalf("maintainer refill: %+v", r)
			}
		})
	}
}

// Retries return the first receipt without another message, debit or audit; a changed body
// or deadline conflicts, an expired key refuses, and removal blocks a stored retry.
func TestRoomPostRetries(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic retries", 32)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, b.Name)
	k, deadline := key(), s.clock()+600
	hk := key()
	first, err := s.DashboardRoomPost(roomCtx("room-post"), room, "synthetic", hk, deadline)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.DashboardRoomPost(roomCtx("room-post"), room, "synthetic", hk, deadline)
	if err != nil || !again.Duplicate || again.Seq != first.Seq || count(t, s, `SELECT COUNT(*) FROM room_posts`) != 1 {
		t.Fatalf("maintainer retry: %+v %v", again, err)
	}
	caller := Key{"codex", "synthetic-a"}
	if _, err := s.RoomPost(ctx, caller, room, "reply", k, deadline); err != nil {
		t.Fatal(err)
	}
	before := rowsOf(t, s.db)
	allowance := allowanceOf(t, s, room)
	if r, err := s.RoomPost(ctx, caller, room, "reply", k, deadline); err != nil || !r.Duplicate || r.Seq != 2 {
		t.Fatalf("retry: %+v %v", r, err)
	}
	if rowsOf(t, s.db) != before || allowanceOf(t, s, room) != allowance {
		t.Fatal("a retry wrote state")
	}
	for name, c := range map[string]struct {
		body     string
		deadline float64
		want     string
	}{
		"changed body":     {"changed", deadline, "idempotency_conflict"},
		"changed deadline": {"reply", deadline + 1, "idempotency_conflict"},
		"expired":          {"reply", s.clock() - 1, "retry_deadline_expired"},
		"beyond horizon":   {"reply", s.clock() + s.limits().idemTTL + 1, "invalid_request"},
	} {
		if _, err := s.RoomPost(ctx, caller, room, c.body, k, c.deadline); errorCode(err) != c.want {
			t.Fatalf("%s: %v", name, err)
		}
	}
	r := roster(t, s, caller, room)
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, ordinalOf(t, r, a.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RoomPost(ctx, caller, room, "reply", k, deadline); code(err) != "not_room_member" {
		t.Fatalf("stored retry after removal: %v", err)
	}
}

// Concurrent posts get one gapless room order; every copy lands in its member's inbox.
func TestRoomConcurrentPostsOrder(t *testing.T) {
	s, _ := testStore(t)
	room := mustRoom(t, s, "synthetic order", 64)
	var members []Key
	for _, family := range []string{"claude", "codex", "agy", "opencode", "deepseek"} {
		m := join(t, s, family, "synthetic-"+family, "")
		mustAdd(t, s, room, m.Name)
		members = append(members, Key{family, m.ID})
	}
	mustHumanPost(t, s, room, "start")
	var wg sync.WaitGroup
	for _, m := range members {
		wg.Add(1)
		go func(k Key) {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				if _, err := s.RoomPost(context.Background(), k, room, fmt.Sprint(k.Family, i), key(), s.clock()+600); err != nil {
					t.Error(err)
				}
			}
		}(m)
	}
	wg.Wait()
	if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 21 || count(t, s, `SELECT MAX(seq) FROM room_posts`) != 21 ||
		count(t, s, `SELECT COUNT(DISTINCT seq) FROM room_posts`) != 21 || allowanceOf(t, s, room) != 44 {
		t.Fatal("concurrent posts lost their order or a debit")
	}
	if count(t, s, `SELECT COUNT(*) FROM messages WHERE room_event='post'`) != 5+20*4 {
		t.Fatal("copies missing")
	}
}

// Failure rolls back everything: at the storage ceiling and at the post and retry limits a
// post writes nothing, uses no allowance and moves no sequence.
func TestRoomPostRefusalsLeaveNothing(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic refusals", 32)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, b.Name)
	mustHumanPost(t, s, room, "go")
	before := rowsOf(t, s.db)
	refuse := func(name, want string) {
		t.Helper()
		_, err := s.RoomPost(ctx, Key{"codex", "synthetic-a"}, room, "synthetic", key(), s.clock()+600)
		if errorCode(err) != want {
			t.Fatalf("%s: %v", name, err)
		}
		if rowsOf(t, s.db) != before || count(t, s, `SELECT post_head FROM rooms`) != 1 || allowanceOf(t, s, room) != 32 ||
			count(t, s, `SELECT COUNT(*) FROM room_deliveries`) != 2 {
			t.Fatalf("%s wrote state", name)
		}
	}
	for _, body := range []string{"", strings.Repeat("x", maxBody+1), "\xff"} {
		if _, err := s.RoomPost(ctx, Key{"codex", "synthetic-a"}, room, body, key(), s.clock()+600); errorCode(err) != "invalid_request" {
			t.Fatalf("body %q: %v", body[:min(len(body), 8)], err)
		}
	}
	pages, _ := s.pages(ctx, s.db)
	s.storage.mu.Lock()
	limit := s.storage.maxPages
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance - 1
	s.storage.mu.Unlock()
	refuse("storage ceiling", "capacity")
	s.storage.mu.Lock()
	s.storage.maxPages = limit
	s.storage.mu.Unlock()
	if _, err := s.db.Exec(`INSERT INTO room_replays(family,caller_id,key,fingerprint,deadline,result)
		WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<?)
		SELECT 'codex','synthetic-filler',v,'x',1e12,'{}' FROM n`, maxRoomReplays-1); err != nil {
		t.Fatal(err)
	}
	refuse("replay limit", "room_replay_limit")
	if _, err := s.db.Exec(`DELETE FROM room_replays WHERE caller_id='synthetic-filler'`); err != nil {
		t.Fatal(err)
	}
}

// At 10,000 retained posts a post refuses and writes nothing.
func TestRoomPostLimit(t *testing.T) {
	s, _ := testStore(t)
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic post limit", 32)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, b.Name)
	mustHumanPost(t, s, room, "go")
	if _, err := s.db.Exec(`INSERT INTO room_posts(room_id,seq,sender_family,sender_id,sender_name,origin,body,created_at)
		WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<?)
		SELECT ?,v,'codex','x','x','member','x',0 FROM n`, maxRoomPosts-1, strings.Repeat("f", 32)); err != nil {
		t.Fatal(err)
	}
	before := rowsOf(t, s.db)
	if _, err := s.RoomPost(context.Background(), Key{"codex", "synthetic-a"}, room, "over", key(), s.clock()+600); code(err) != "room_post_limit" {
		t.Fatalf("10,001st post: %v", err)
	}
	if _, err := s.DashboardRoomPost(roomCtx("room-post"), room, "over", key(), s.clock()+600); code(err) != "room_post_limit" {
		t.Fatalf("maintainer post at the limit: %v", err)
	}
	if rowsOf(t, s.db) != before || allowanceOf(t, s, room) != 32 {
		t.Fatal("a refused post wrote state")
	}
}

// Reads page by count and by about one MiB of bodies, continue after next_after, and name
// the sequences that retention removed.
func TestRoomReadPagesAndGaps(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic read", 64)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, b.Name)
	big := strings.Repeat("y", maxBody)
	for i := 0; i < 20; i++ {
		mustHumanPost(t, s, room, big)
	}
	reader := Key{"claude", "synthetic-b"}
	first, err := s.RoomRead(ctx, reader, room, 0, 0)
	if err != nil || !first.More || len(first.Posts) != 16 || *first.NextAfter != 16 {
		t.Fatalf("byte-bounded page: %d %v %v", len(first.Posts), first.More, err)
	}
	second, err := s.RoomRead(ctx, reader, room, *first.NextAfter, 0)
	if err != nil || second.More || len(second.Posts) != 4 || second.Posts[0].Seq != 17 {
		t.Fatalf("second page: %d %v %v", len(second.Posts), second.More, err)
	}
	if page, err := s.RoomRead(ctx, reader, room, 0, 3); err != nil || len(page.Posts) != 3 || !page.More {
		t.Fatalf("limit 3: %+v %v", page, err)
	}
	if _, err := s.RoomRead(ctx, reader, room, 0, maxRoomReadPosts+1); errorCode(err) != "invalid_request" {
		t.Fatalf("limit over 100: %v", err)
	}
	// Retention removed posts 2 to 4 and 20: the read names both gaps.
	if _, err := s.db.Exec(`DELETE FROM room_posts WHERE seq IN (2,3,4,20)`); err != nil {
		t.Fatal(err)
	}
	page, err := s.RoomRead(ctx, reader, room, 0, 10)
	if err != nil || fmt.Sprint(page.Gaps) != "[{2 4}]" || page.Posts[1].Seq != 5 {
		t.Fatalf("gaps inside a page: %+v %v", page.Gaps, err)
	}
	last, err := s.RoomRead(ctx, reader, room, 16, 0)
	if err != nil || fmt.Sprint(last.Gaps) != "[{20 20}]" || last.More {
		t.Fatalf("gap at the head: %+v %v", last.Gaps, err)
	}
}

// Posts follow their inbox copies: a post stays while any copy is retained, also after its
// member is removed or a session is purged, and goes when every copy is gone; a post without
// copies waits the message retention period. Cleanup writes no message or audit record.
func TestRoomPostRetention(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic retention", 32)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, b.Name)
	mustHumanPost(t, s, room, "two copies")
	solo := mustRoom(t, s, "synthetic empty", 32)
	mustHumanPost(t, s, solo, "no copies")
	cull := func() {
		t.Helper()
		budget := int64(cullBudget)
		if err := s.cullRoomPosts(ctx, &budget); err != nil {
			t.Fatal(err)
		}
	}
	messages, audits := count(t, s, `SELECT COUNT(*) FROM messages`), count(t, s, `SELECT COUNT(*) FROM audit`)
	// One copy is gone (acknowledged and culled, or purged): the post stays.
	if _, err := s.db.Exec(`DELETE FROM messages WHERE room_event='post' AND recipient_family='codex'`); err != nil {
		t.Fatal(err)
	}
	cull()
	if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 2 {
		t.Fatal("a post with a retained copy was deleted")
	}
	// The last copy goes: the post and its deliveries go; the post without copies stays.
	if _, err := s.db.Exec(`DELETE FROM messages WHERE room_event='post'`); err != nil {
		t.Fatal(err)
	}
	cull()
	if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 1 || count(t, s, `SELECT COUNT(*) FROM room_deliveries`) != 0 {
		t.Fatal("a post without copies left")
	}
	clock = clock.Add(messageRetention + time.Second)
	cull()
	if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 0 {
		t.Fatal("a post with no copies outlived the retention period")
	}
	if count(t, s, `SELECT COUNT(*) FROM messages`) != messages-2 || count(t, s, `SELECT COUNT(*) FROM audit`) != audits {
		t.Fatal("cleanup wrote a message or audit record")
	}
	// Expired retry records go; the room sequence is not reused.
	if count(t, s, `SELECT COUNT(*) FROM room_replays`) != 0 {
		t.Fatal("expired retry records remained")
	}
	if r := mustHumanPost(t, s, room, "after cleanup"); r.Seq != 2 {
		t.Fatalf("sequence after cleanup: %+v", r)
	}
}

// A full post at maximum fan-out: 32 copies of a 65,536-byte body, its post row, 32
// delivery rows and its retry record fit the ordinary page accounting, and the measured
// growth stays within the derived bound.
func TestRoomPostStorageBound(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	room := mustRoom(t, s, "synthetic fan-out", 32)
	var members []Session
	for i := 0; i < maxRoomMembers; i++ {
		m := join(t, s, "deepseek", fmt.Sprintf("synthetic-%02d-%s", i, strings.Repeat("i", 200)), "")
		mustAdd(t, s, room, m.Name)
		members = append(members, m)
	}
	before, _ := s.pages(ctx, s.db)
	r := mustHumanPost(t, s, room, strings.Repeat("z", maxBody))
	after, _ := s.pages(ctx, s.db)
	if r.Queued != maxRoomMembers {
		t.Fatalf("fan-out: %+v", r)
	}
	// 33 bodies of 64 KiB each take 17 overflow pages; rows, index entries and B-tree splits
	// add a few more pages per table.
	bound := int64(33*17 + 64)
	if after-before > bound {
		t.Fatalf("one maximum post used %d pages, bound %d", after-before, bound)
	}
	t.Logf("maximum post: %d pages (bound %d)", after-before, bound)
}

// The post and read routes serve DeepSeek's registered command caller; a missing deadline,
// a guessed caller and the maintainer's key are refused.
func TestRoomPostRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-dsh", Directory: t.TempDir(), TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	other := join(t, d.store, "codex", "synthetic-other", "")
	room := mustRoom(t, d.store, "synthetic routes", 32)
	var name string
	if err := d.store.db.QueryRow(`SELECT name FROM names WHERE kind='peer' AND session_id='synthetic-dsh'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, d.store, room, name)
	mustAdd(t, d.store, room, other.Name)
	mustHumanPost(t, d.store, room, "synthetic task")
	caller := Key{"deepseek", "synthetic-dsh"}
	deadline := d.store.clock() + 600
	status, result := post(t, address, secret, "/v1/rooms/post", map[string]any{"caller": caller, "room_id": room, "body": "synthetic reply", "key": "k1", "deadline": deadline})
	if status != 200 || result["result"].(map[string]any)["seq"] != float64(2) || result["result"].(map[string]any)["queued"] != float64(1) {
		t.Fatalf("post: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/rooms/read", map[string]any{"caller": caller, "room_id": room})
	posts := result["result"].(map[string]any)["posts"].([]any)
	if status != 200 || len(posts) != 2 || posts[0].(map[string]any)["origin"] != "maintainer" || posts[1].(map[string]any)["sender_name"] != name {
		t.Fatalf("read: %d %v", status, result)
	}
	for label, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"no deadline": {map[string]any{"caller": caller, "room_id": room, "body": "x", "key": "k2"}, 400, "invalid_request"},
		"guessed":     {map[string]any{"caller": Key{"deepseek", "synthetic-guessed"}, "room_id": room, "body": "x", "key": "k3", "deadline": deadline}, 403, "caller_inactive"},
		"maintainer":  {map[string]any{"caller": maintainerKey, "room_id": room, "body": "x", "key": "k4", "deadline": deadline}, 400, "invalid_request"},
		"origin":      {map[string]any{"caller": caller, "room_id": room, "body": "x", "key": "k5", "deadline": deadline, "origin": "maintainer"}, 400, "invalid_request"},
	} {
		if status, result := post(t, address, secret, "/v1/rooms/post", c.body); status != c.status || result["code"] != c.code {
			t.Fatalf("%s: %d %v", label, status, result)
		}
	}
	if count(t, d.store, `SELECT COUNT(*) FROM room_posts`) != 2 {
		t.Fatal("a refused post was stored")
	}
}

// The dashboard post requires login, origin and CSRF; it stores maintainer origin, sets the
// allowance, and the open room shows the transcript as inert text.
func TestDashboardRoomPost(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-a", "")
	room := mustRoom(t, s, "synthetic dashboard", 9)
	mustAdd(t, s, room, a.Name)
	client := newActionClient(t, d, root)
	deadline := strconv.FormatInt(int64(s.clock()+600), 10)
	form := url.Values{"room_id": {room}, "body": {"<script>synthetic()</script>"}, "key": {"dashboard-post"}, "deadline": {deadline}}
	for name, c := range map[string]struct {
		cookie string
		change func(*http.Request)
		status int
	}{
		"no session": {"", client.sameOrigin, 401},
		"no token":   {client.cookie, client.sameOrigin, 403},
		"foreign":    {client.cookie, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, 403},
	} {
		f := url.Values{"csrf": {client.csrf}}
		if name == "no token" {
			f = url.Values{}
		}
		for k, v := range form {
			f[k] = v
		}
		if r := dashboardDo(t, d, "POST", "/dashboard/actions/room-post", c.cookie, c.change, f); r.status != c.status {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM room_posts`) != 0 || count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-post'`) != 0 {
		t.Fatal("a refused request changed state")
	}
	if got := client.do("room-post", form); got != "room_posted" {
		t.Fatalf("post: %s", got)
	}
	if got := client.do("room-post", form); got != "room_posted" || count(t, s, `SELECT COUNT(*) FROM room_posts`) != 1 {
		t.Fatalf("repeated form: %s", got)
	}
	if allowanceOf(t, s, room) != 9 || count(t, s, `SELECT COUNT(*) FROM room_posts WHERE origin='maintainer' AND sender_family='maintainer'`) != 1 {
		t.Fatal("maintainer post state")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-post' AND result='accepted'`); n != 1 {
		t.Fatalf("accepted room-post audit records: %d", n)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room, client.cookie, nil, nil)
	if page.status != 200 || strings.Contains(page.body, "<script>synthetic") || !strings.Contains(page.body, "&lt;script&gt;synthetic()") ||
		!strings.Contains(page.body, "Post to synthetic dashboard") {
		t.Fatalf("room page: %d %s", page.status, page.body)
	}
	if got := client.do("room-post", url.Values{"room_id": {room}, "body": {""}, "key": {"k"}, "deadline": {deadline}}); got != "invalid_request" {
		t.Fatalf("empty body: %s", got)
	}
}
