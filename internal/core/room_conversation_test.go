package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The open room shows each post's frozen recipients: queued copies with their delivery
// state and acknowledgement, skipped inactive members, and copies that retention removed as
// not retained with unknown acknowledgement. Reading the page or its fragment changes
// nothing, renders bodies as inert text, shows no native ID and needs a login.
func TestDashboardRoomConversationView(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	ctx := context.Background()
	a := join(t, s, "codex", "synthetic-native-a", "")
	b := join(t, s, "claude", "synthetic-native-b", "")
	c := join(t, s, "agy", "synthetic-native-c", "")
	room := mustRoom(t, s, "synthetic view", 32)
	for _, m := range []Session{a, b, c} {
		mustAdd(t, s, room, m.Name)
	}
	client := newActionClient(t, d, root)
	if got := client.do("retire", url.Values{"family": {"agy"}, "id": {c.ID}, "revision": {strconv.FormatInt(c.Revision, 10)}}); got != "retired" {
		t.Fatalf("retire: %s", got)
	}
	first := mustHumanPost(t, s, room, "<script>synthetic()</script> **task**")
	if first.Queued != 2 || first.Skipped != 1 {
		t.Fatalf("first post: %+v", first)
	}
	mustPost(t, s, Key{"codex", a.ID}, room, "synthetic reply")
	// b acknowledges both copies; a's copy of the first post is culled.
	inbox, err := s.ReadInbox(ctx, Key{"claude", b.ID}, 0, 50)
	if err != nil || len(inbox.Messages) == 0 {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	if _, err := s.Ack(ctx, Key{"claude", b.ID}, inbox.Messages[len(inbox.Messages)-1].Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE id=(SELECT d.message_id FROM room_deliveries d JOIN room_posts p ON p.post_id=d.post_id
		WHERE p.seq=1 AND d.family='codex')`); err != nil {
		t.Fatal(err)
	}
	// A later removal leaves the earlier posts' recipient rows as they were.
	r := roster(t, s, Key{"codex", a.ID}, room)
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, ordinalOf(t, r, b.Name)); err != nil {
		t.Fatal(err)
	}
	markers := func() string {
		return rowsOf(t, s.db) + strconv.FormatInt(count(t, s, `SELECT COALESCE(SUM(read_through)*7+SUM(acked_through),0) FROM sessions`), 10)
	}
	before := markers()
	page := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room, client.cookie, nil, nil)
	fragment := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room+"&fragment=1", client.cookie, nil, nil)
	if markers() != before {
		t.Fatal("reading the room changed state")
	}
	for name, body := range map[string]string{"page": page.body, "fragment": fragment.body} {
		if page.status != 200 || fragment.status != 200 {
			t.Fatalf("status: %d %d", page.status, fragment.status)
		}
		for _, want := range []string{
			"&lt;script&gt;synthetic()&lt;/script&gt; **task**",
			a.Name + " <small>codex:\nnot retained, acknowledgement unknown",
			b.Name + " <small>claude:\n",
			", acknowledged by recipient",
			c.Name + " <small>agy:\nskipped (inactive)",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s lacks %q:\n%s", name, want, body)
			}
		}
		if strings.Contains(body, "<script>synthetic") || strings.Contains(body, "synthetic-native-") {
			t.Fatalf("%s shows active HTML or a native ID:\n%s", name, body)
		}
	}
	// A refresh of the newest page adds a new post after the older ones, once each.
	mustHumanPost(t, s, room, "synthetic third")
	fresh := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room+"&fragment=1", client.cookie, nil, nil).body
	if i, j := strings.Index(fresh, "synthetic reply"), strings.Index(fresh, "synthetic third"); i < 0 || j < i ||
		strings.Count(fresh, "synthetic reply") != 1 || strings.Count(fresh, "synthetic third") != 1 {
		t.Fatalf("refreshed page order: %s", fresh)
	}
	// Paging by room sequence: after 1 shows only the later posts.
	later := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room+"&after=1&fragment=1", client.cookie, nil, nil)
	if later.status != 200 || strings.Contains(later.body, "**task**") || !strings.Contains(later.body, "synthetic reply") {
		t.Fatalf("page after 1: %d %s", later.status, later.body)
	}
	if out := dashboardDo(t, d, "GET", "/dashboard/rooms?room="+room, "", nil, nil); out.status == 200 || strings.Contains(out.body, "synthetic reply") {
		t.Fatalf("room without a login: %d", out.status)
	}
}

// deliveriesOf reads a room's recipient rows as the dashboard does.
func deliveriesOf(t *testing.T, s *Store, room string, seq int64) []RoomDelivery {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := roomDeliveries(context.Background(), tx, room, seq, seq)
	if err != nil {
		t.Fatal(err)
	}
	return rows[seq]
}

// A whole conversation with every family: a maintainer post reaches all six, each replies,
// the allowance runs out and refills, another room stays separate, concurrent posts and
// roster changes keep one order, an expired member is skipped and catches up, a replacement
// session is not a member while a participant successor is, a failed wake undoes nothing,
// reads and acknowledgements stay separate, a culled copy is not retained, each accepted
// change has one audit record without a body, and a restart keeps it all.
func TestRoomConversationEndToEnd(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	clock := time.Unix(1_000_000, 0)
	s.now = func() time.Time { return clock }
	repo := namedRepo(t, "koinon")
	failed := countingWake(s, wakeResult{"failed", "synthetic failure"})
	holder := joinAs(t, s, "codex", "synthetic-holder", repo, "")
	successor := joinAs(t, s, "codex", "synthetic-successor", repo, "")
	families := []string{"claude", "codex", "agy", "opencode", "grok", "deepseek"}
	keys := map[string]Key{"codex": {"codex", "synthetic-holder"}}
	names := map[string]string{"codex": holder.Name}
	for _, f := range families {
		if f != "codex" {
			m := join(t, s, f, "synthetic-"+f, "")
			keys[f], names[f] = Key{f, m.ID}, m.Name
		}
	}
	rejoin := func(except string) {
		for _, f := range families {
			switch {
			case f == except:
			case f == "codex":
				joinAs(t, s, "codex", "synthetic-holder", repo, "")
			default:
				join(t, s, f, keys[f].ID, "")
			}
		}
		joinAs(t, s, "codex", "synthetic-successor", repo, "")
	}
	room := mustRoom(t, s, "synthetic conversation", 7)
	other := mustRoom(t, s, "synthetic other room", 7)
	for _, f := range families {
		mustAdd(t, s, room, names[f])
	}
	mustAdd(t, s, other, names["claude"])
	const marker = "BODY-MARKER"

	// The maintainer post reaches all six; a failed wake undoes nothing.
	if r := mustHumanPost(t, s, room, marker+" task"); r.Queued != 6 || r.Skipped != 0 {
		t.Fatalf("maintainer post: %+v", r)
	}
	if err := s.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if *failed == 0 || count(t, s, `SELECT COUNT(*) FROM room_posts`) != 1 || len(deliveriesOf(t, s, room, 1)) != 6 {
		t.Fatalf("after a failed wake: %d calls", *failed)
	}
	// Each family replies; every member gets the others' replies, the poster does not.
	for _, f := range families {
		if r := mustPost(t, s, keys[f], room, marker+" reply from "+f); r.Queued != 5 {
			t.Fatalf("%s reply: %+v", f, r)
		}
	}
	for _, f := range families {
		inbox := keys[f]
		if f == "codex" {
			inbox = Key{"codex", participantKey("codex-koinon")}
		}
		if got := len(roomCopies(t, s, inbox)); got != 6 {
			t.Fatalf("%s copies: %d", f, got)
		}
	}
	// The allowance of 7 runs out; a maintainer post refills it.
	for allowanceOf(t, s, room) > 0 {
		mustPost(t, s, keys["claude"], room, marker+" filler")
	}
	if _, err := s.RoomPost(ctx, keys["agy"], room, "over", key(), s.clock()+600); code(err) != "exchange_limit" {
		t.Fatalf("exhausted allowance: %v", err)
	}
	mustHumanPost(t, s, room, marker+" refill")
	if allowanceOf(t, s, room) != 7 {
		t.Fatal("refill")
	}
	// The other room holds nothing of this one; a non-member cannot read it.
	if count(t, s, `SELECT COUNT(*) FROM room_posts WHERE room_id=?`, other) != 0 {
		t.Fatal("cross-room post")
	}
	if _, err := s.RoomRead(ctx, keys["agy"], other, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("foreign room read: %v", err)
	}
	// Concurrent posts and a roster change keep one gapless order.
	ordinal := ordinalOf(t, roster(t, s, keys["claude"], room), names["opencode"])
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, f := range []string{"claude", "agy", "grok"} {
		wg.Add(1)
		go func(k Key) {
			defer wg.Done()
			_, err := s.RoomPost(ctx, k, room, marker+" concurrent", key(), s.clock()+600)
			errs <- err
		}(keys[f])
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- s.RemoveRoomMember(roomCtx("room-remove"), room, ordinal)
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent change: %v", err)
		}
	}
	mustAdd(t, s, room, names["opencode"])
	head := count(t, s, `SELECT post_head FROM rooms WHERE room_id=?`, room)
	if count(t, s, `SELECT COUNT(*) FROM room_posts WHERE room_id=?`, room) != head || count(t, s, `SELECT MAX(seq) FROM room_posts WHERE room_id=?`, room) != head {
		t.Fatal("room order has a gap")
	}
	// agy expires: the next post skips it; it registers again and reads the missed post.
	clock = clock.Add(2 * time.Minute)
	rejoin("agy")
	missed := mustHumanPost(t, s, room, marker+" while agy is away")
	if missed.Skipped != 1 {
		t.Fatalf("expired member: %+v", missed)
	}
	join(t, s, "agy", keys["agy"].ID, "")
	if tr, err := s.RoomRead(ctx, keys["agy"], room, missed.Seq-1, 0); err != nil || len(tr.Posts) != 1 || tr.Posts[0].Seq != missed.Seq {
		t.Fatalf("catch-up read: %+v %v", tr, err)
	}
	// A replacement session is not a member; the participant's successor is, and the former
	// holder is fenced.
	join(t, s, "claude", "synthetic-claude-replacement", "")
	if _, err := s.RoomPost(ctx, Key{"claude", "synthetic-claude-replacement"}, room, "x", key(), s.clock()+600); code(err) != "not_room_member" {
		t.Fatalf("replacement session: %v", err)
	}
	successor = joinAs(t, s, "codex", "synthetic-successor", repo, "")
	if err := s.ChooseHolder(roomCtx("participant-holder"), "codex-koinon", Key{"codex", "synthetic-successor"}, successor.Revision); err != nil {
		t.Fatal(err)
	}
	mustPost(t, s, Key{"codex", "synthetic-successor"}, room, marker+" from the successor")
	if h := joinAs(t, s, "codex", "synthetic-holder", repo, ""); !h.Fenced {
		t.Fatalf("former holder: %+v", h)
	}
	if _, err := s.RoomPost(ctx, keys["codex"], room, "x", key(), s.clock()+600); code(err) != "not_room_member" {
		t.Fatalf("fenced former holder: %v", err)
	}
	// Reading moves no inbox marker; an acknowledgement shows on the recipient rows.
	var read, acked int64
	marks := func() {
		if err := s.db.QueryRow(`SELECT read_through,acked_through FROM sessions WHERE family='grok' AND id=?`, keys["grok"].ID).Scan(&read, &acked); err != nil {
			t.Fatal(err)
		}
	}
	marks()
	before := read*1000 + acked
	if _, err := s.RoomRead(ctx, keys["grok"], room, 0, 0); err != nil {
		t.Fatal(err)
	}
	marks()
	if read*1000+acked != before {
		t.Fatal("room_read moved an inbox marker")
	}
	grokRow := func(seq int64) RoomDelivery {
		for _, d := range deliveriesOf(t, s, room, seq) {
			if d.Family == "grok" {
				return d
			}
		}
		t.Fatalf("no grok row for post %d", seq)
		return RoomDelivery{}
	}
	if grokRow(1).Acknowledged {
		t.Fatal("acknowledged before an ack")
	}
	inbox, err := s.ReadInbox(ctx, keys["grok"], 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(ctx, keys["grok"], inbox.Messages[len(inbox.Messages)-1].Seq); err != nil {
		t.Fatal(err)
	}
	if r := grokRow(1); !r.Acknowledged || r.AcknowledgedBy != "recipient" {
		t.Fatalf("acknowledged row: %+v", r)
	}
	// A culled copy is not retained, with acknowledgement unknown.
	if _, err := s.db.Exec(`DELETE FROM messages WHERE id=(SELECT d.message_id FROM room_deliveries d JOIN room_posts p ON p.post_id=d.post_id
		WHERE p.room_id=? AND p.seq=1 AND d.family='deepseek')`, room); err != nil {
		t.Fatal(err)
	}
	for _, d := range deliveriesOf(t, s, room, 1) {
		if d.Family == "deepseek" && (d.Outcome != "not_retained" || d.Acknowledged || d.AcknowledgedBy != "") {
			t.Fatalf("culled copy: %+v", d)
		}
	}
	// One accepted audit record per maintainer post and membership change; none holds a body.
	posts := count(t, s, `SELECT COUNT(*) FROM room_posts WHERE origin='maintainer'`)
	if n := count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-post' AND result='accepted'`); n != posts {
		t.Fatalf("post audit records: %d of %d", n, posts)
	}
	adds := count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-add' AND result='accepted'`)
	removes := count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-remove' AND result='accepted'`)
	if adds != int64(len(families)+2) || removes != 1 {
		t.Fatalf("membership audit records: %d adds, %d removes", adds, removes)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM audit WHERE target LIKE ?`, "%"+marker+"%"); n != 0 {
		t.Fatal("an audit record holds a body")
	}
	// A restart keeps the transcript, recipient rows and allowance, and wakes the copies that
	// were waiting from the committed inbox rows.
	waiting := mustHumanPost(t, s, room, marker+" before restart")
	allowance, rows := allowanceOf(t, s, room), len(deliveriesOf(t, s, room, waiting.Seq))
	s.db.Close()
	s2, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.db.Close() })
	s2.now = s.now
	if allowanceOf(t, s2, room) != allowance || len(deliveriesOf(t, s2, room, waiting.Seq)) != rows {
		t.Fatal("restart lost room state")
	}
	if tr, err := s2.RoomRead(ctx, keys["claude"], room, waiting.Seq-1, 0); err != nil || len(tr.Posts) != 1 {
		t.Fatalf("read after restart: %+v %v", tr, err)
	}
	notified := countingWake(s2, wakeResult{"notified", ""})
	if err := s2.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if *notified == 0 {
		t.Fatal("no wake after restart")
	}
}

// The newest page is the newest posts within the page's count and byte bounds: at the byte
// limit a new post appears on refresh, the Earlier link reaches the older ones, and a
// retention gap inside the newest posts is named.
func TestDashboardRoomNewestPageAtTheByteLimit(t *testing.T) {
	s, _ := testStore(t)
	d := &Daemon{store: s}
	ctx := context.Background()
	room := mustRoom(t, s, "synthetic newest page", 32)
	for i := 0; i < 17; i++ {
		mustHumanPost(t, s, room, strings.Repeat("x", maxBody))
	}
	newest := func() roomPage {
		t.Helper()
		data, err := d.roomsView(ctx, url.Values{"room": {room}})
		if err != nil {
			t.Fatal(err)
		}
		return *data.Open
	}
	span := func(p roomPage) string {
		posts := p.Transcript.Posts
		return strconv.FormatInt(posts[0].Seq, 10) + ".." + strconv.FormatInt(posts[len(posts)-1].Seq, 10)
	}
	if p := newest(); span(p) != "2..17" || p.Transcript.More || p.Earlier == nil || *p.Earlier != 0 {
		t.Fatalf("newest page: %s more %v", span(p), p.Transcript.More)
	}
	mustHumanPost(t, s, room, strings.Repeat("y", maxBody))
	if p := newest(); span(p) != "3..18" {
		t.Fatalf("refreshed newest page: %s", span(p))
	}
	earlier, err := d.roomsView(ctx, url.Values{"room": {room}, "after": {"0"}})
	if err != nil || earlier.Open.Transcript.Posts[0].Seq != 1 || !earlier.Open.Transcript.More {
		t.Fatalf("earlier page: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM room_posts WHERE room_id=? AND seq=17`, room); err != nil {
		t.Fatal(err)
	}
	if p := newest(); span(p) != "2..18" || fmt.Sprint(p.Transcript.Gaps) != "[{17 17}]" {
		t.Fatalf("newest page with a gap: %s %v", span(p), p.Transcript.Gaps)
	}
}

// Room writes are ordinary: at the ordinary ceiling a post or membership change refuses
// and borrows no funded work debt, while a funded work finish, an inbox acknowledgement and
// the room cleanup still complete.
func TestRoomWritesDoNotConsumeFundedWorkControls(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, b := join(t, s, "codex", "synthetic-a", ""), join(t, s, "claude", "synthetic-b", "")
	room := mustRoom(t, s, "synthetic saturation", 32)
	mustAdd(t, s, room, a.Name)
	mustHumanPost(t, s, room, "synthetic before the ceiling")
	m := MemoryCaller{Family: "codex", Repository: "/synthetic/room-work/.git", Consumer: "synthetic-room-worker", Name: "synthetic-room-worker"}
	id := create(t, s, m, "synthetic funded work")
	generation := mustStart(t, s, m, id, nil)
	debt := mustDebt(t, s)
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + debt + appendAllowance - 1
	if _, err := s.DashboardRoomPost(roomCtx("room-post"), room, "synthetic maintainer post", key(), s.clock()+600); !errors.Is(err, ErrCapacity) {
		t.Fatalf("maintainer post borrowed work debt: %v", err)
	}
	if _, err := s.RoomPost(ctx, Key{"codex", "synthetic-a"}, room, "synthetic member post", key(), s.clock()+600); !errors.Is(err, ErrCapacity) {
		t.Fatalf("member post borrowed work debt: %v", err)
	}
	if _, err := s.AddRoomMember(roomCtx("room-add"), room, b.Name); !errors.Is(err, ErrCapacity) {
		t.Fatalf("membership change borrowed work debt: %v", err)
	}
	if mustDebt(t, s) != debt || count(t, s, `SELECT COUNT(*) FROM room_posts`) != 1 {
		t.Fatal("a refused room write changed state or funded obligations")
	}
	mustWork(t, s, m, "work-finish", map[string]any{"work_id": id, "if_revision": revision(t, s, m, id), "claim_generation": generation, "outcome": "withdrawn", "reason": "synthetic completed control test", "key": key(), "deadline": now(s) + 600})
	inbox, err := s.ReadInbox(ctx, Key{"codex", "synthetic-a"}, 0, 50)
	if err != nil || len(inbox.Messages) == 0 {
		t.Fatalf("inbox: %v", err)
	}
	if _, err := s.Ack(ctx, Key{"codex", "synthetic-a"}, inbox.Messages[len(inbox.Messages)-1].Seq); err != nil {
		t.Fatalf("acknowledgement at the ceiling: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM messages WHERE room_event='post'`); err != nil {
		t.Fatal(err)
	}
	budget := int64(cullBudget)
	if err := s.cullRoomPosts(ctx, &budget); err != nil || count(t, s, `SELECT COUNT(*) FROM room_posts`) != 0 {
		t.Fatalf("room cleanup at the ceiling: %v", err)
	}
}
