package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Dashboard rooms, chunk 01 (#217): rooms, membership, notices and the member queries.
// Every session, repository and room is synthetic.

func roomCtx(action string) context.Context {
	ctx, _ := withAudit(context.Background(), action, "synthetic")
	return ctx
}

func mustRoom(t *testing.T, s *Store, name string, refill int64) string {
	t.Helper()
	id, err := s.CreateRoom(roomCtx("room-create"), "key-"+name, name, refill)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAdd(t *testing.T, s *Store, room, peer string) bool {
	t.Helper()
	changed, err := s.AddRoomMember(roomCtx("room-add"), room, peer)
	if err != nil {
		t.Fatalf("add %s: %v", peer, err)
	}
	return changed
}

func roster(t *testing.T, s *Store, caller Key, room string) Room {
	t.Helper()
	r, _, err := s.RoomMembers(context.Background(), caller, room, 0, 0)
	if err != nil {
		t.Fatalf("room_members for %v: %v", caller, err)
	}
	return r
}

func ordinalOf(t *testing.T, r Room, label string) int64 {
	t.Helper()
	for _, m := range r.Members {
		if m.Address == label || m.Address == "" && m.PeerName == label {
			return m.Ordinal
		}
	}
	t.Fatalf("no member %s in %+v", label, r.Members)
	return 0
}

func roomNotices(t *testing.T, s *Store, inbox Key) []Message {
	t.Helper()
	var messages []Message
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, _, _, err := readMessages(context.Background(), tx, inbox, 0, 100, "session", &messages); err != nil {
		t.Fatal(err)
	}
	var notices []Message
	for _, m := range messages {
		if m.Room != nil {
			notices = append(notices, m)
		}
	}
	return notices
}

func TestRoomMembershipNoticesAndQueries(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	plain := join(t, s, "claude", "synthetic-plain", "")
	holder := joinAs(t, s, "codex", "synthetic-holder", repo, "")
	outsider := join(t, s, "agy", "synthetic-outsider", "")
	room := mustRoom(t, s, "synthetic room <b>", 7)
	if again, err := s.CreateRoom(roomCtx("room-create"), "key-synthetic room <b>", "synthetic room <b>", 7); err != nil || again != room {
		t.Fatalf("repeated create: %s %v", again, err)
	}
	if _, err := s.CreateRoom(roomCtx("room-create"), "key-synthetic room <b>", "changed", 7); code(err) != "idempotency_conflict" {
		t.Fatalf("changed repeat: %v", err)
	}
	if !mustAdd(t, s, room, plain.Name) || !mustAdd(t, s, room, holder.Name) {
		t.Fatal("add changed nothing")
	}
	// A duplicate add changes nothing: no revision, notice or membership start.
	if mustAdd(t, s, room, plain.Name) {
		t.Fatal("duplicate add changed the roster")
	}
	r := roster(t, s, Key{plain.Family, plain.ID}, room)
	if r.Revision != 3 || len(r.Members) != 2 || r.Name != "synthetic room <b>" {
		t.Fatalf("roster: %+v", r)
	}
	want := []RoomMember{
		{Ordinal: 1, Kind: "session", Family: "claude", PeerName: plain.Name, State: "active", Current: true, Revision: 2},
		{Ordinal: 2, Kind: "participant", Family: "codex", Address: "codex-koinon", PeerName: holder.Name, State: "active", Current: true, Revision: 3},
	}
	if fmt.Sprint(r.Members) != fmt.Sprint(want) {
		t.Fatalf("members:\n%+v\nwant\n%+v", r.Members, want)
	}
	// The roster names members, never their native IDs or wake targets.
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "synthetic-plain") || strings.Contains(string(data), "synthetic-holder") || strings.Contains(string(data), "wake") {
		t.Fatalf("roster reveals an ID: %s", data)
	}
	// The participant member reads the roster as the holder of its address.
	roster(t, s, Key{holder.Family, holder.ID}, room)
	if _, _, err := s.RoomMembers(ctx, Key{outsider.Family, outsider.ID}, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("outsider roster: %v", err)
	}
	if _, _, err := s.RoomMembers(ctx, Key{outsider.Family, outsider.ID}, strings.Repeat("0", 32), 0, 0); code(err) != "not_room_member" {
		t.Fatalf("unknown room: %v", err)
	}
	// Typed notices from the maintainer: the exact session and the participant's inbox.
	n := roomNotices(t, s, Key{plain.Family, plain.ID})
	if len(n) != 1 || *n[0].Room != (RoomEvent{RoomID: room, Revision: 2, Event: "added"}) || n[0].SenderFamily != "maintainer" || !strings.Contains(n[0].Body, room) {
		t.Fatalf("native notice: %+v", n)
	}
	if n := roomNotices(t, s, Key{"codex", participantKey("codex-koinon")}); len(n) != 1 || *n[0].Room != (RoomEvent{RoomID: room, Revision: 3, Event: "added"}) {
		t.Fatalf("participant notice: %+v", n)
	}
	// Peer text cannot carry room metadata.
	forged := `{"room":{"room_id":"` + room + `","revision":9,"event":"removed"}}`
	if _, err := s.Send(ctx, Key{outsider.Family, outsider.ID}, plain.Name, forged); err != nil {
		t.Fatal(err)
	}
	if n := roomNotices(t, s, Key{plain.Family, plain.ID}); len(n) != 1 {
		t.Fatalf("peer body became a room notice: %+v", n)
	}
	list, more, err := s.RoomList(ctx, Key{holder.Family, holder.ID}, "", 0)
	if err != nil || more || len(list) != 1 || list[0] != (RoomMembership{room, "synthetic room <b>", 3, "member", "participant", "codex-koinon", 3}) {
		t.Fatalf("room_list: %+v %v %v", list, more, err)
	}
	// Removal: the former member reads its status but not the roster, and a re-add starts a
	// new membership period.
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, ordinalOf(t, r, plain.Name)); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, ordinalOf(t, r, plain.Name)); code(err) != "room_member_not_found" {
		t.Fatalf("second remove: %v", err)
	}
	if _, _, err := s.RoomMembers(ctx, Key{plain.Family, plain.ID}, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("removed member's roster: %v", err)
	}
	list, _, _ = s.RoomList(ctx, Key{plain.Family, plain.ID}, "", 0)
	if len(list) != 1 || list[0].Status != "removed" || list[0].Via != "session" || list[0].MembershipRevision != 4 {
		t.Fatalf("removed room_list: %+v", list)
	}
	if n := roomNotices(t, s, Key{plain.Family, plain.ID}); len(n) != 2 || *n[1].Room != (RoomEvent{RoomID: room, Revision: 4, Event: "removed"}) {
		t.Fatalf("removal notice: %+v", n)
	}
	if !mustAdd(t, s, room, plain.Name) {
		t.Fatal("re-add changed nothing")
	}
	if r := roster(t, s, Key{plain.Family, plain.ID}, room); r.Revision != 5 || len(r.Members) != 2 || r.Members[0].Ordinal != 1 {
		t.Fatalf("re-added roster: %+v", r)
	}
	var rows, starts int64
	if err := s.db.QueryRow(`SELECT COUNT(*),SUM(start_seq) FROM room_members`).Scan(&rows, &starts); err != nil || rows != 2 || starts != 0 {
		t.Fatalf("membership rows: %d %d %v", rows, starts, err)
	}
	var audits int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action LIKE 'room-%' AND result='accepted'`).Scan(&audits); err != nil || audits != 7 {
		t.Fatalf("audit: %d %v", audits, err)
	}
}

// A participant membership follows the verified holder: the successor reads the roster and
// the fenced former holder cannot. Native expiry keeps the membership, shown inactive.
func TestRoomMembershipFollowsTheParticipantHolder(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	plain := join(t, s, "deepseek", "synthetic-plain", "")
	room := mustRoom(t, s, "synthetic succession", 32)
	mustAdd(t, s, room, a.Name)
	mustAdd(t, s, room, plain.Name)
	if _, _, err := s.RoomMembers(ctx, Key{"codex", "synthetic-b"}, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("non-holder of the member address: %v", err)
	}
	if err := s.ChooseHolder(roomCtx("participant-holder"), "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatal(err)
	}
	if r := roster(t, s, Key{"codex", "synthetic-b"}, room); r.Members[0].PeerName != b.Name || r.Members[0].State != "active" {
		t.Fatalf("successor roster: %+v", r.Members)
	}
	// The former holder registers again fenced: it holds no address and reads nothing.
	if a = joinAs(t, s, "codex", "synthetic-a", repo, ""); !a.Fenced {
		t.Fatalf("former holder: %+v", a)
	}
	if _, _, err := s.RoomMembers(ctx, Key{"codex", "synthetic-a"}, room, 0, 0); code(err) != "not_room_member" {
		t.Fatalf("fenced former holder: %v", err)
	}
	if list, _, err := s.RoomList(ctx, Key{"codex", "synthetic-a"}, "", 0); err != nil || len(list) != 0 {
		t.Fatalf("fenced former holder's room_list: %+v %v", list, err)
	}
	// The notice of a later change reaches the participant's inbox, which the successor reads.
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, 2); err != nil {
		t.Fatal(err)
	}
	if n := roomNotices(t, s, Key{"codex", participantKey("codex-koinon")}); len(n) != 1 {
		t.Fatalf("participant inbox: %+v", n)
	}
	mustAdd(t, s, room, plain.Name)
	// Everybody expires: the membership stays and shows its state; nobody gets a notice.
	clock = clock.Add(2 * time.Minute)
	rooms, err := s.dashboardRooms(ctx)
	if err != nil || len(rooms) != 1 {
		t.Fatal(err)
	}
	states := []string{}
	for _, m := range rooms[0].Members {
		states = append(states, m.State)
	}
	if strings.Join(states, ",") != "unheld,inactive" {
		t.Fatalf("expired states: %v", states)
	}
	if _, err := fixtureExec(s, `UPDATE names SET conflict='["x","y"]',holder_id='' WHERE name='codex-koinon'`); err != nil {
		t.Fatal(err)
	}
	before := count(t, s, `SELECT COUNT(*) FROM messages`)
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, 2); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT COUNT(*) FROM messages`) != before {
		t.Fatal("an inactive member got a notice")
	}
	if rooms, _ = s.dashboardRooms(ctx); rooms[0].Members[0].State != "conflicting" || rooms[0].Members[1].State != "removed" {
		t.Fatalf("conflict and removal: %+v", rooms[0].Members)
	}
}

// Every supported family can be a member; a guessed or inactive caller is refused.
func TestRoomMembersOfEveryFamily(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	room := mustRoom(t, s, "synthetic families", 0)
	for _, family := range SupportedFamilies() {
		session := join(t, s, family, "synthetic-"+family, "")
		mustAdd(t, s, room, session.Name)
		if r := roster(t, s, Key{family, session.ID}, room); r.Members[len(r.Members)-1].Family != family {
			t.Fatalf("%s: %+v", family, r.Members)
		}
	}
	if _, _, err := s.RoomList(ctx, Key{"codex", "synthetic-guessed"}, "", 0); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("guessed caller: %v", err)
	}
	if _, _, err := s.RoomMembers(ctx, Key{"nobody", "x"}, room, 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported family: %v", err)
	}
}

func TestRoomRefusalsLeaveNothing(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	room := mustRoom(t, s, "synthetic refusals", 32)
	gone := join(t, s, "claude", "synthetic-gone", "")
	before := rowsOf(t, s.db)
	refused := map[string]error{}
	_, refused["peer_not_found"] = s.AddRoomMember(roomCtx("room-add"), room, "synthetic-unknown")
	_, refused["peer_not_found "] = s.AddRoomMember(roomCtx("room-add"), room, "maintainer")
	_, refused["room_not_found"] = s.AddRoomMember(roomCtx("room-add"), strings.Repeat("a", 32), gone.Name)
	_, refused["invalid_request"] = s.AddRoomMember(roomCtx("room-add"), "short", gone.Name)
	refused["room_member_not_found"] = s.RemoveRoomMember(roomCtx("room-remove"), room, 1)
	refused["invalid_request "] = s.SetRoomRefill(roomCtx("room-refill"), room, -1)
	refused["room_not_found "] = s.SetRoomRefill(roomCtx("room-refill"), strings.Repeat("b", 32), 1)
	_, refused["invalid_request  "] = s.CreateRoom(roomCtx("room-create"), "k", strings.Repeat("x", 129), 1)
	_, refused["invalid_request   "] = s.CreateRoom(roomCtx("room-create"), "k", "", 1)
	_, refused["invalid_request    "] = s.CreateRoom(roomCtx("room-create"), "k", "\xff", 1)
	clock = clock.Add(2 * time.Minute)
	_, refused["recipient_inactive"] = s.AddRoomMember(roomCtx("room-add"), room, gone.Name)
	for want, err := range refused {
		if errorCode(err) != strings.TrimSpace(want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if rowsOf(t, s.db) != before || count(t, s, `SELECT COUNT(*) FROM room_members`) != 0 || count(t, s, `SELECT COUNT(*) FROM rooms`) != 1 {
		t.Fatal("a refusal changed state")
	}
}

// A repeated create compares with the request that the key first sent, not with the room's
// current refill: after a refill change the original request still returns the room and
// changes nothing, and the new value under the same key is refused.
func TestRoomCreateRepeatAfterARefillChange(t *testing.T) {
	s, _ := testStore(t)
	room := mustRoom(t, s, "synthetic repeat", 32)
	if err := s.SetRoomRefill(roomCtx("room-refill"), room, 64); err != nil {
		t.Fatal(err)
	}
	if again, err := s.CreateRoom(roomCtx("room-create"), "key-synthetic repeat", "synthetic repeat", 32); err != nil || again != room {
		t.Fatalf("original request again: %s %v", again, err)
	}
	if _, err := s.CreateRoom(roomCtx("room-create"), "key-synthetic repeat", "synthetic repeat", 64); code(err) != "idempotency_conflict" {
		t.Fatalf("changed request under the same key: %v", err)
	}
	var refill, allowance, revision int64
	if err := s.db.QueryRow(`SELECT refill,allowance,revision FROM rooms WHERE room_id=?`, room).Scan(&refill, &allowance, &revision); err != nil ||
		refill != 64 || allowance != 0 || revision != 1 || count(t, s, `SELECT COUNT(*) FROM rooms`) != 1 {
		t.Fatalf("after the repeats: %d %d %d %v", refill, allowance, revision, err)
	}
}

// The refill setting is stored and changed without refilling; the allowance stays zero.
func TestRoomRefillSetting(t *testing.T) {
	s, _ := testStore(t)
	for _, refill := range []int64{0, 7, 32, 64} {
		room := mustRoom(t, s, fmt.Sprint("synthetic refill ", refill), refill)
		var stored, allowance int64
		if err := s.db.QueryRow(`SELECT refill,allowance FROM rooms WHERE room_id=?`, room).Scan(&stored, &allowance); err != nil || stored != refill || allowance != 0 {
			t.Fatalf("refill %d: %d %d %v", refill, stored, allowance, err)
		}
		if err := s.SetRoomRefill(roomCtx("room-refill"), room, refill+1); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow(`SELECT refill,allowance FROM rooms WHERE room_id=?`, room).Scan(&stored, &allowance); err != nil || stored != refill+1 || allowance != 0 {
			t.Fatalf("changed refill %d: %d %d %v", refill, stored, allowance, err)
		}
	}
}

func TestRoomLimits(t *testing.T) {
	s, _ := testStore(t)
	room := mustRoom(t, s, "synthetic limits", 32)
	for i := 1; i < maxRooms; i++ {
		mustRoom(t, s, fmt.Sprint("synthetic ", i), 32)
	}
	if _, err := s.CreateRoom(roomCtx("room-create"), "key-over", "over", 32); code(err) != "room_limit" {
		t.Fatalf("65th room: %v", err)
	}
	var sessions []Session
	for i := 0; i <= maxRoomMemberRows; i++ {
		sessions = append(sessions, join(t, s, "deepseek", fmt.Sprintf("synthetic-%03d", i), ""))
	}
	for _, m := range sessions[:maxRoomMembers] {
		mustAdd(t, s, room, m.Name)
	}
	if _, err := s.AddRoomMember(roomCtx("room-add"), room, sessions[maxRoomMembers].Name); code(err) != "room_member_limit" {
		t.Fatalf("33rd member: %v", err)
	}
	// Removed rows stay; the 129th distinct membership is refused, and a re-add is not.
	next := maxRoomMembers
	for removed := int64(1); next < maxRoomMemberRows; removed++ {
		if err := s.RemoveRoomMember(roomCtx("room-remove"), room, removed); err != nil {
			t.Fatal(err)
		}
		mustAdd(t, s, room, sessions[next].Name)
		next++
	}
	if err := s.RemoveRoomMember(roomCtx("room-remove"), room, int64(maxRoomMemberRows)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoomMember(roomCtx("room-add"), room, sessions[maxRoomMemberRows].Name); code(err) != "room_history_limit" {
		t.Fatalf("129th membership: %v", err)
	}
	mustAdd(t, s, room, sessions[0].Name)
	if count(t, s, `SELECT COUNT(*) FROM room_members`) != maxRoomMemberRows {
		t.Fatal("a limit evicted a membership")
	}
	// Pages are bounded and continue after the last ordinal.
	caller := Key{"deepseek", sessions[0].ID}
	first, more, err := s.RoomMembers(context.Background(), caller, room, 0, 30)
	if err != nil || !more || len(first.Members) != 30 {
		t.Fatalf("first page: %d %v %v", len(first.Members), more, err)
	}
	second, more, err := s.RoomMembers(context.Background(), caller, room, first.Members[29].Ordinal, 30)
	if err != nil || more || len(second.Members) != 2 {
		t.Fatalf("second page: %d %v %v", len(second.Members), more, err)
	}
	if _, _, err := s.RoomMembers(context.Background(), caller, room, 0, maxRoomPage+1); errorCode(err) != "invalid_request" {
		t.Fatalf("page over 100: %v", err)
	}
}

// At the ordinary ceiling an add is refused with nothing written: no membership, revision,
// notice or accepted audit.
func TestRoomAddAtTheStorageCeiling(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	room := mustRoom(t, s, "synthetic ceiling", 32)
	member := join(t, s, "codex", "synthetic-member", "")
	before := rowsOf(t, s.db)
	pages, _ := s.pages(ctx, s.db)
	s.storage.mu.Lock()
	s.storage.maxPages = pages + reservePages + commitSlack + appendAllowance - 1
	s.storage.mu.Unlock()
	if _, err := s.AddRoomMember(roomCtx("room-add"), room, member.Name); code(err) != "capacity" {
		t.Fatalf("add at the ceiling: %v", err)
	}
	if rowsOf(t, s.db) != before || count(t, s, `SELECT COUNT(*) FROM room_members`) != 0 || count(t, s, `SELECT revision FROM rooms`) != 1 ||
		count(t, s, `SELECT COUNT(*) FROM audit WHERE action='room-add' AND result='accepted'`) != 0 {
		t.Fatal("a refused add wrote state")
	}
}

// Concurrent adds and removes serialize: each change has its own revision.
func TestRoomConcurrentChanges(t *testing.T) {
	s, _ := testStore(t)
	room := mustRoom(t, s, "synthetic race", 32)
	var members []Session
	for i := 0; i < 8; i++ {
		members = append(members, join(t, s, "opencode", fmt.Sprint("synthetic-race-", i), ""))
	}
	var wg sync.WaitGroup
	for _, m := range members {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				if _, err := s.AddRoomMember(roomCtx("room-add"), room, name); err != nil {
					t.Error(err)
				}
			}
		}(m.Name)
	}
	wg.Wait()
	rooms, err := s.dashboardRooms(context.Background())
	if err != nil || len(rooms[0].Members) != 8 || rooms[0].Revision != 9 {
		t.Fatalf("after concurrent adds: %+v %v", rooms, err)
	}
	for _, m := range rooms[0].Members {
		wg.Add(1)
		go func(ordinal int64) {
			defer wg.Done()
			s.RemoveRoomMember(roomCtx("room-remove"), room, ordinal)
		}(m.Ordinal)
	}
	wg.Wait()
	if count(t, s, `SELECT revision FROM rooms`) != 17 || count(t, s, `SELECT COUNT(DISTINCT revision) FROM room_members`) != 8 ||
		count(t, s, `SELECT SUM(active) FROM room_members`) != 0 {
		t.Fatal("concurrent removes did not serialize")
	}
}

// A schema 16 store (develop d95fac8) gains the room tables without changing its messages,
// sessions, cursors, claims or names; the steps from schema 18 alone do the same.
func TestRoomsUpgradeKeepsExistingState(t *testing.T) {
	root := loadFixtureFile(t, "d95fac8.sql")
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if count(t, s, `PRAGMA user_version`) != schemaVersion || count(t, s, `SELECT COUNT(*) FROM rooms`) != 0 {
		t.Fatal("fixture was not migrated")
	}
	rows := rowsOf(t, s.db)
	if _, err := fixtureExec(s, undoSchemaThirty+undoSchemaTwentyNine+undoSchemaTwentyEight+undoSchemaTwentySeven+undoSchemaTwentySix+undoSchemaTwentyFive+undoSchemaTwentyFour+undoSchemaTwentyThree+undoSchemaTwentyTwo+undoSchemaTwentyOne+undoSchemaTwenty+undoSchemaNineteen+`PRAGMA user_version=18`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, 18); err != nil {
		t.Fatal(err)
	}
	if err := migrate(s.db, 18, schemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(s.db, schemaVersion); err != nil {
		t.Fatal(err)
	}
	if rowsOf(t, s.db) != rows || count(t, s, `SELECT COUNT(*) FROM messages WHERE room_id!='' OR room_revision!=0 OR room_event!='' OR room_seq!=0 OR room_origin!=''`) != 0 {
		t.Fatal("migration changed existing state")
	}
}

// The dashboard's room actions require login, origin and CSRF; each answers a notice and
// records one audit record. The page renders names and peer names as inert text.
func TestDashboardRoomActions(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	member := join(t, s, "codex", "synthetic-member", "")
	client := newActionClient(t, d, root)
	create := url.Values{"name": {"<script>synthetic</script>"}, "refill": {"7"}, "key": {"dashboard-synthetic"}}
	for name, c := range map[string]struct {
		cookie string
		change func(*http.Request)
		form   url.Values
		status int
	}{
		"no session": {"", client.sameOrigin, create, 401},
		"no token":   {client.cookie, client.sameOrigin, create, 403},
		"foreign":    {client.cookie, func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, url.Values{"csrf": {client.csrf}}, 403},
	} {
		if r := dashboardDo(t, d, "POST", "/dashboard/actions/room-create", c.cookie, c.change, c.form); r.status != c.status {
			t.Fatalf("%s: %d", name, r.status)
		}
	}
	if count(t, s, `SELECT COUNT(*) FROM rooms`) != 0 || len(auditAll(t, s)) != 0 {
		t.Fatal("a refused request changed state")
	}
	n := 0
	for _, bad := range []string{"-1", "x", "", "1.5", "99999999999999999999"} {
		if got := client.do("room-create", url.Values{"name": {"synthetic"}, "refill": {bad}, "key": {"dashboard-bad"}}); got != "invalid_request" {
			t.Fatalf("refill %q: %s", bad, got)
		}
		expectAudit(t, s, n, "room-create", "refused", "invalid_request")
		n++
	}
	if got := client.do("room-create", create); got != "room_created" {
		t.Fatalf("create: %s", got)
	}
	expectAudit(t, s, n, "room-create", "accepted", "")
	n++
	var room string
	if err := s.db.QueryRow(`SELECT room_id FROM rooms`).Scan(&room); err != nil {
		t.Fatal(err)
	}
	if got := client.do("room-add", url.Values{"room_id": {room}, "peer": {member.Name}}); got != "room_member_added" {
		t.Fatalf("add: %s", got)
	}
	if r := expectAudit(t, s, n, "room-add", "accepted", ""); !strings.Contains(r.Target, member.Name+" as session") || strings.Contains(r.Target, "synthetic-member") {
		t.Fatalf("add target: %q", r.Target)
	}
	n++
	if got := client.do("room-add", url.Values{"room_id": {room}, "peer": {"synthetic-nobody"}}); got != "peer_not_found" {
		t.Fatalf("unknown peer: %s", got)
	}
	expectAudit(t, s, n, "room-add", "refused", "peer_not_found")
	n++
	if got := client.do("room-refill", url.Values{"room_id": {room}, "refill": {"64"}}); got != "room_refill_saved" {
		t.Fatalf("refill: %s", got)
	}
	expectAudit(t, s, n, "room-refill", "accepted", "")
	n++
	page := dashboardDo(t, d, "GET", "/dashboard/rooms", client.cookie, nil, nil)
	if page.status != 200 || strings.Contains(page.body, "<script>synthetic") || !strings.Contains(page.body, "&lt;script&gt;synthetic") ||
		!strings.Contains(page.body, member.Name) || !strings.Contains(page.body, "refill 64 at each maintainer post, 0 left") {
		t.Fatalf("rooms page: %d %s", page.status, page.body)
	}
	if got := client.do("room-remove", url.Values{"room_id": {room}, "ordinal": {"1"}}); got != "room_member_removed" {
		t.Fatalf("remove: %s", got)
	}
	expectAudit(t, s, n, "room-remove", "accepted", "")
	n++
	if got := client.do("room-remove", url.Values{"room_id": {room}, "ordinal": {"1"}}); got != "room_member_not_found" {
		t.Fatalf("second remove: %s", got)
	}
	expectAudit(t, s, n, "room-remove", "refused", "room_member_not_found")
}

// The room routes answer DeepSeek's registered command caller and refuse a guessed one,
// an unknown field and a non-member; inbox reads return the typed room field.
func TestRoomRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if _, err := d.store.Register(context.Background(), Registration{Family: "deepseek", ID: "synthetic-dsh", Directory: t.TempDir(), TTLSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	caller := Key{"deepseek", "synthetic-dsh"}
	other := join(t, d.store, "codex", "synthetic-other", "")
	room := mustRoom(t, d.store, "synthetic routes", 32)
	var name string
	if err := d.store.db.QueryRow(`SELECT name FROM names WHERE kind='peer' AND session_id='synthetic-dsh'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	mustAdd(t, d.store, room, name)
	status, result := post(t, address, secret, "/v1/rooms/list", map[string]any{"caller": caller})
	rooms := result["result"].(map[string]any)["rooms"].([]any)
	if status != 200 || len(rooms) != 1 || rooms[0].(map[string]any)["status"] != "member" {
		t.Fatalf("list: %d %v", status, result)
	}
	status, result = post(t, address, secret, "/v1/rooms/members", map[string]any{"caller": caller, "room_id": room, "limit": 1})
	if status != 200 || result["result"].(map[string]any)["members"].([]any)[0].(map[string]any)["peer_name"] != name {
		t.Fatalf("members: %d %v", status, result)
	}
	for label, c := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"guessed":    {map[string]any{"caller": Key{"deepseek", "synthetic-guessed"}}, 403, "caller_inactive"},
		"unknown":    {map[string]any{"caller": caller, "family": "codex"}, 400, "invalid_request"},
		"non-member": {map[string]any{"caller": Key{other.Family, other.ID}, "room_id": room}, 409, "not_room_member"},
	} {
		path := "/v1/rooms/list"
		if c.body["room_id"] != nil {
			path = "/v1/rooms/members"
		}
		if status, result := post(t, address, secret, path, c.body); status != c.status || result["code"] != c.code {
			t.Fatalf("%s: %d %v", label, status, result)
		}
	}
	status, result = post(t, address, secret, "/v1/inbox/read", map[string]any{"caller": caller})
	messages := result["inbox"].(map[string]any)["messages"].([]any)
	if status != 200 || len(messages) != 1 || messages[0].(map[string]any)["room"].(map[string]any)["event"] != "added" {
		t.Fatalf("inbox: %d %v", status, result)
	}
}
