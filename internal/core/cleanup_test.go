package core

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The command line's cleanup (#388) selects by name, state, family and inactivity, never the
// maintainer session, and changes each session through the dashboard's store path under its
// own audit record by cli.
func TestCleanupSelectionAndChanges(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a := join(t, s, "codex", "synthetic-a", "")
	b := join(t, s, "claude", "synthetic-b", "")
	c := join(t, s, "agy", "synthetic-c", "")
	if _, err := s.Mutate(ctx, Mutation{Family: c.Family, ID: c.ID, IfRevision: c.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Key{a.Family, a.ID}, b.Name, "for the cleanup"); err != nil {
		t.Fatal(err)
	}
	// a and b expire at 1060; c retired at 1000; d registers at 1400.
	clock = time.Unix(1400, 0)
	d := join(t, s, "codex", "synthetic-d", "")
	names := func(f CleanupFilter) []string {
		t.Helper()
		list, more, err := s.SelectCleanup(ctx, f, "list")
		if err != nil || more {
			t.Fatalf("%+v: %v %v", f, err, more)
		}
		var got []string
		for _, c := range list {
			got = append(got, c.Name)
		}
		return got
	}
	same := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, name := range got {
			seen[name] = true
		}
		for _, name := range want {
			if !seen[name] {
				return false
			}
		}
		return true
	}
	for _, c2 := range []struct {
		f    CleanupFilter
		want []string
	}{
		{CleanupFilter{}, []string{a.Name, b.Name, c.Name, d.Name}},
		{CleanupFilter{State: "expired"}, []string{a.Name, b.Name}},
		{CleanupFilter{State: "retired"}, []string{c.Name}},
		{CleanupFilter{State: "active"}, []string{d.Name}},
		{CleanupFilter{Family: "codex"}, []string{a.Name, d.Name}},
		{CleanupFilter{InactiveForSeconds: 350}, []string{c.Name}},
		{CleanupFilter{InactiveForSeconds: 300}, []string{a.Name, b.Name, c.Name}},
		{CleanupFilter{Names: []string{a.Name, d.Name}, State: "expired"}, []string{a.Name}},
	} {
		if got := names(c2.f); !same(got, c2.want...) {
			t.Fatalf("%+v: %v, want %v", c2.f, got, c2.want)
		}
	}
	for _, f := range []CleanupFilter{{Names: []string{"synthetic-nobody"}}, {Names: []string{"maintainer"}}} {
		if _, _, err := s.SelectCleanup(ctx, f, "list"); code(err) != "peer_not_found" {
			t.Fatalf("%+v: %v", f, err)
		}
	}
	if _, _, err := s.SelectCleanup(ctx, CleanupFilter{State: "gone"}, "list"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown state: %v", err)
	}
	selected := func(name string) CleanupSession {
		t.Helper()
		list, _, err := s.SelectCleanup(ctx, CleanupFilter{Names: []string{name}}, "list")
		if err != nil || len(list) != 1 {
			t.Fatalf("%s: %v %v", name, list, err)
		}
		return list[0]
	}
	if got := selected(b.Name); got.Unacknowledged != 1 || got.State != "expired" || got.InactiveSince != 1060000 {
		t.Fatalf("b: %+v", got)
	}
	lastAudit := func() AuditRecord {
		t.Helper()
		return auditAll(t, s)[0]
	}
	// A clear at a revision that changed since the selection acknowledges nothing (Codex F2).
	staleClear := selected(b.Name)
	staleClear.Revision++
	if r := s.applyCleanup(ctx, "clear", staleClear); r.Result != "refused" || r.Code != "revision_changed" || selected(b.Name).Unacknowledged != 1 {
		t.Fatalf("stale clear: %+v %+v", r, selected(b.Name))
	}
	for _, step := range []struct {
		action, name, target string
	}{
		{"purge", a.Name, "codex:synthetic-a by cli"},
		{"retire", d.Name, "codex:synthetic-d by cli"},
		{"clear", b.Name, "claude:synthetic-b by cli"},
		{"unpurge", a.Name, "codex:synthetic-a by cli"},
	} {
		if r := s.applyCleanup(ctx, step.action, selected(step.name)); r.Result != "accepted" {
			t.Fatalf("%s %s: %+v", step.action, step.name, r)
		}
		if r := lastAudit(); r.Action != step.action || r.Result != "accepted" || r.Target != step.target {
			t.Fatalf("%s audit: %+v", step.action, r)
		}
	}
	if got := selected(d.Name); got.State != "retired" {
		t.Fatalf("d after retire: %+v", got)
	}
	if got := selected(b.Name); got.Unacknowledged != 0 {
		t.Fatalf("b after clear: %+v", got)
	}
	if got := selected(a.Name); got.PurgeMarked {
		t.Fatalf("a after unpurge: %+v", got)
	}
	// A session that changed after its selection is refused and audited.
	stale := selected(a.Name)
	stale.Revision--
	if r := s.applyCleanup(ctx, "purge", stale); r.Result != "refused" || r.Code != "revision_changed" {
		t.Fatalf("stale: %+v", r)
	}
	if r := lastAudit(); r.Result != "refused" || r.Reason != "revision_changed" || r.Target != "codex:synthetic-a by cli" {
		t.Fatalf("stale audit: %+v", r)
	}
	// Retire selects active sessions only, unpurge marked ones only, purge unmarked ones only.
	for _, tc := range []struct {
		action string
		want   []string
	}{
		{"retire", nil},
		{"unpurge", nil},
		{"purge", []string{a.Name, b.Name, c.Name, d.Name}},
		{"clear", []string{a.Name, b.Name, c.Name, d.Name}},
	} {
		list, _, err := s.SelectCleanup(ctx, CleanupFilter{}, tc.action)
		var got []string
		for _, x := range list {
			got = append(got, x.Name)
		}
		if err != nil || !same(got, tc.want...) {
			t.Fatalf("%s selects %v %v", tc.action, got, err)
		}
	}
	// Every filter applies before the limit: with a limit of one, the one expired session
	// after an active one is still found (Codex F1).
	previous := cleanupLimit
	cleanupLimit = 1
	defer func() { cleanupLimit = previous }()
	if got := names(CleanupFilter{State: "retired", Family: "codex"}); !same(got, d.Name) {
		t.Fatalf("limited retired: %v", got)
	}
	if list, more, err := s.SelectCleanup(ctx, CleanupFilter{}, "list"); err != nil || len(list) != 1 || !more {
		t.Fatalf("limited list: %v %v %v", list, more, err)
	}
	cleanupLimit = previous

}

// The routes refuse a change without a selection, show the selection without apply, apply
// it with apply, and run the sweep, which deletes a purge-marked session.
func TestCleanupRoutes(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-a", "")
	b := join(t, s, "claude", "synthetic-b", "")
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	if status, reply := post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "retire", "apply": true}); status != 400 || reply["code"] != "invalid_request" {
		t.Fatalf("no selection: %d %v", status, reply)
	}
	status, reply := post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "list"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 2 {
		t.Fatalf("list: %d %v", status, reply)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "purge", "family": "codex"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 1 || reply["applied"] != false || reply["results"] != nil {
		t.Fatalf("preview: %d %v", status, reply)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt != 0 || got.State != "active" {
		t.Fatalf("preview changed a: %+v", got)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "purge", "names": []string{a.Name}, "apply": true})
	results, _ := reply["results"].([]any)
	if status != 200 || len(results) != 1 || results[0].(map[string]any)["result"] != "accepted" {
		t.Fatalf("purge: %d %v", status, reply)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt == 0 || got.State != "retired" {
		t.Fatalf("purged a: %+v", got)
	}
	status, reply = post(t, address, secret, "/v1/maintenance/sweep", map[string]any{})
	counts, _ := reply["counts"].(map[string]any)
	if status != 200 || counts == nil || counts["sessions_by_purge"] != float64(1) {
		t.Fatalf("sweep: %d %v", status, reply)
	}
	status, reply = post(t, address, secret, "/v1/cleanup/sessions", map[string]any{"action": "list"})
	if listed, _ := reply["sessions"].([]any); status != 200 || len(listed) != 1 || listed[0].(map[string]any)["name"] != b.Name {
		t.Fatalf("after the sweep: %d %v", status, reply)
	}
}

// koinon claims release lists and releases only the claims of expired or retired sessions;
// koinon rooms prune removes only inactive session members; a room delete removes the room
// (#388).
func TestCleanupClaimsAndRooms(t *testing.T) {
	s, _ := retentionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "claims")
	held := join(t, s, "codex", "synthetic-held", repo)
	live := join(t, s, "claude", "synthetic-live", repo)
	m := MemoryCaller{Repository: "/synthetic/claims/.git", Family: "codex", Name: held.Name, Consumer: "codex:synthetic-held"}
	id := create(t, s, m, "synthetic held item")
	mustStart(t, s, m, id, nil)
	ml := MemoryCaller{Repository: "/synthetic/claims/.git", Family: "claude", Name: live.Name, Consumer: "claude:synthetic-live"}
	mustStart(t, s, ml, create(t, s, ml, "synthetic live item"), nil)
	if claims, _, err := s.InactiveClaims(ctx, nil); err != nil || len(claims) != 0 {
		t.Fatalf("active holders: %+v %v", claims, err)
	}
	if _, err := s.Mutate(ctx, Mutation{Family: held.Family, ID: held.ID, IfRevision: held.Revision}, true); err != nil {
		t.Fatal(err)
	}
	claims, _, err := s.InactiveClaims(ctx, nil)
	if err != nil || len(claims) != 1 || claims[0].Holder != held.Name || claims[0].WorkID != id || claims[0].Consumer != "codex:synthetic-held" {
		t.Fatalf("inactive claims: %+v %v", claims, err)
	}
	if only, _, err := s.InactiveClaims(ctx, []string{live.Name}); err != nil || len(only) != 0 {
		t.Fatalf("held by the live session: %+v %v", only, err)
	}
	if _, _, err := s.InactiveClaims(ctx, []string{"synthetic-nobody"}); code(err) != "peer_not_found" {
		t.Fatalf("unknown holder: %v", err)
	}
	if r := s.releaseClaim(ctx, claims[0]); r.Result != "accepted" {
		t.Fatalf("release: %+v", r)
	}
	if item := get(t, s, m, id); item["lifecycle"] != "open" {
		t.Fatalf("released item: %+v", item)
	}
	if r := auditAll(t, s)[0]; r.Action != "release" || r.Result != "accepted" || r.Target != "/synthetic/claims/.git "+id+" owner codex:synthetic-held by cli" {
		t.Fatalf("release audit: %+v", r)
	}
	if again, _, _ := s.InactiveClaims(ctx, nil); len(again) != 0 {
		t.Fatalf("after the release: %+v", again)
	}

	x := join(t, s, "agy", "synthetic-x", "")
	y := join(t, s, "opencode", "synthetic-y", "")
	room, err := s.CreateRoom(roomCtx("room-create"), "synthetic-room", "synthetic room", 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{x.Name, y.Name} {
		if _, err := s.AddRoomMember(roomCtx("room-add"), room, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Mutate(ctx, Mutation{Family: x.Family, ID: x.ID, IfRevision: x.Revision}, true); err != nil {
		t.Fatal(err)
	}
	members, err := s.InactiveRoomMembers(ctx, nil)
	if err != nil || len(members) != 1 || members[0].RoomID != room || members[0].State != "retired" {
		t.Fatalf("inactive members: %+v %v", members, err)
	}
	if r := s.pruneRoomMember(ctx, members[0]); r.Result != "accepted" {
		t.Fatalf("prune: %+v", r)
	}
	if r := auditAll(t, s)[0]; r.Action != "room-remove" || r.Result != "accepted" || !strings.HasSuffix(r.Target, " by cli") {
		t.Fatalf("prune audit: %+v", r)
	}
	if left, _ := s.InactiveRoomMembers(ctx, []string{room}); len(left) != 0 {
		t.Fatalf("after the prune: %+v", left)
	}
	if err := s.DeleteRoom(roomCtx("room-delete"), room); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InactiveRoomMembers(ctx, []string{room}); code(err) != "room_not_found" {
		t.Fatalf("deleted room: %v", err)
	}
	var rows int
	s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM rooms)+(SELECT COUNT(*) FROM room_members)`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("rows left: %d", rows)
	}
	if err := s.DeleteRoom(roomCtx("room-delete"), room); code(err) != "room_not_found" {
		t.Fatalf("second delete: %v", err)
	}
}

// The dashboard's bulk action applies the single action to each selected session under its
// own audit record; a purge needs its confirmation, and a stale or built-in selection is
// refused while the others apply. The room delete needs its confirmation too (#388).
func TestDashboardBulkAndRoomDelete(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	a := join(t, s, "codex", "synthetic-a", "")
	b := join(t, s, "claude", "synthetic-b", "")
	client := newActionClient(t, d, root)
	pick := func(sessions ...Session) []string {
		var values []string
		for _, x := range sessions {
			x = sessionState(t, s, Key{x.Family, x.ID})
			values = append(values, x.Family+":"+x.ID+":"+strconv.FormatInt(x.Revision, 10))
		}
		return values
	}
	if got := client.do("bulk", url.Values{"action": {"purge"}, "s": pick(a, b)}); got != "confirmation_required" {
		t.Fatalf("unconfirmed purge: %s", got)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt != 0 {
		t.Fatalf("purged without confirmation: %+v", got)
	}
	if got := client.do("bulk", url.Values{"action": {"wipe"}, "s": pick(a)}); got != "invalid_request" {
		t.Fatalf("unknown action: %s", got)
	}
	if got := client.do("bulk", url.Values{"action": {"wipe"}}); got != "invalid_request" {
		t.Fatalf("unknown action without a selection: %s", got)
	}
	// A known action without a selection names the missing selection and changes nothing (#442).
	for _, form := range []url.Values{{"action": {"retire"}}, {"action": {"purge"}, "confirm": {"purge"}}} {
		if got := client.do("bulk", form); got != "no_selection" {
			t.Fatalf("%v without a selection: %s", form, got)
		}
		if r := auditAll(t, s)[0]; r.Action != "bulk" || r.Target != "invalid by dashboard" || r.Result != "refused" || r.Reason != "no_selection" {
			t.Fatalf("empty selection audit: %+v", r)
		}
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.State != "active" || got.PurgeAt != 0 {
		t.Fatalf("changed without a selection: %+v", got)
	}
	if text := notice("no_selection"); text != "Refused: select one or more sessions first. Nothing was changed." {
		t.Fatalf("notice: %q", text)
	}
	// A bulk clear at a stale revision acknowledges nothing (Codex F2).
	if _, err := s.Send(context.Background(), Key{b.Family, b.ID}, a.Name, "unread"); err != nil {
		t.Fatal(err)
	}
	if got := client.do("bulk", url.Values{"action": {"clear"}, "s": {pick(a)[0] + "0"}}); got != "bulk_partial" {
		t.Fatalf("stale clear: %s", got)
	}
	if list, _, _ := s.SelectCleanup(context.Background(), CleanupFilter{Names: []string{a.Name}}, "list"); len(list) != 1 || list[0].Unacknowledged != 1 {
		t.Fatalf("after the stale clear: %+v", list)
	}
	before := len(auditAll(t, s))
	if got := client.do("bulk", url.Values{"action": {"retire"}, "s": pick(a, b)}); got != "bulk_applied" {
		t.Fatalf("retire: %s", got)
	}
	records := auditAll(t, s)
	if len(records) != before+2 || records[0].Action != "retire" || records[1].Action != "retire" || records[0].Result != "accepted" {
		t.Fatalf("retire audit: %+v", records[:2])
	}
	stale := pick(a)[0] + "0"
	if got := client.do("bulk", url.Values{"action": {"purge"}, "confirm": {"purge"}, "s": {stale, pick(b)[0], "maintainer:maintainer:1"}}); got != "bulk_partial" {
		t.Fatalf("partial purge: %s", got)
	}
	if got := sessionState(t, s, Key{b.Family, b.ID}); got.PurgeAt == 0 {
		t.Fatalf("b not purged: %+v", got)
	}
	if got := sessionState(t, s, Key{a.Family, a.ID}); got.PurgeAt != 0 {
		t.Fatalf("stale a purged: %+v", got)
	}
	room, err := s.CreateRoom(roomCtx("room-create"), "synthetic-room", "synthetic room", 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.do("room-delete", url.Values{"room_id": {room}}); got != "invalid_request" {
		t.Fatalf("unconfirmed delete: %s", got)
	}
	if got := client.do("room-delete", url.Values{"room_id": {room}, "confirm": {"delete"}}); got != "room_deleted" {
		t.Fatalf("delete: %s", got)
	}
	if r := auditAll(t, s)[0]; r.Action != "room-delete" || r.Result != "accepted" {
		t.Fatalf("delete audit: %+v", r)
	}
}
