package core

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// roomsData is the rooms view (#217): every room with its current and former members, the
// active peer names that the add form suggests, the forms' key and deadline, and the open
// room's transcript page.
type roomsData struct {
	Rooms    []Room
	Peers    []peerOption
	Key      string
	Deadline int64
	Open     *roomPage
}

// roomPage is one page of an open room's transcript, oldest first, with the links to the
// pages before and after it.
type roomPage struct {
	Room       Room
	Transcript RoomTranscript
	// Deliveries holds each shown post's recipient rows by room sequence.
	Deliveries map[int64][]RoomDelivery
	Earlier    *int64
	Later      *int64
}

// dashboardRoomPage is the number of posts that the open room shows.
const dashboardRoomPage = 50

func (d *Daemon) roomsView(ctx context.Context, q url.Values) (roomsData, error) {
	rooms, err := d.store.dashboardRooms(ctx)
	if err != nil {
		return roomsData{}, err
	}
	options, err := d.store.dashboardPeerOptions(ctx, false)
	if err != nil {
		return roomsData{}, err
	}
	// A room adds a session by its peer name; a held alias is not offered.
	peers := []peerOption{}
	for _, o := range options {
		if !strings.HasPrefix(o.Label, "alias of ") {
			peers = append(peers, o)
		}
	}
	key, err := formKey()
	data := roomsData{Rooms: rooms, Peers: peers, Key: key, Deadline: int64(d.store.clock() + d.store.limits().idemTTL - 60)}
	if err != nil || q.Get("room") == "" {
		return data, err
	}
	id := q.Get("room")
	for _, r := range rooms {
		if r.ID == id {
			data.Open = &roomPage{Room: r}
		}
	}
	if data.Open == nil {
		return data, ErrInvalid
	}
	// Without after, the page shows the newest posts. The head, posts, recipient rows and
	// their inbox states come from one read transaction; reading acknowledges nothing.
	var after int64
	explicit := q.Get("after") != ""
	if explicit {
		if after, err = strconv.ParseInt(q.Get("after"), 10, 64); err != nil || after < 0 {
			return data, ErrInvalid
		}
	}
	tx, err := d.store.db.BeginTx(ctx, nil)
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	if !explicit {
		if after, err = roomTailStart(ctx, tx, id); err != nil {
			return data, err
		}
	}
	if data.Open.Transcript, err = roomTranscript(ctx, tx, id, 0, after, dashboardRoomPage); err != nil {
		return data, err
	}
	if posts := data.Open.Transcript.Posts; len(posts) > 0 {
		if data.Open.Deliveries, err = roomDeliveries(ctx, tx, id, posts[0].Seq, posts[len(posts)-1].Seq); err != nil {
			return data, err
		}
	}
	if after > 0 {
		earlier := max(0, after-dashboardRoomPage)
		data.Open.Earlier = &earlier
	}
	data.Open.Later = data.Open.Transcript.NextAfter
	return data, nil
}

// roomActions adds the dashboard's room actions: create a room, change its refill setting,
// and add or remove a member. Each answers 303 to the rooms view.
func (d *Daemon) roomActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	run func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string)) {
	mux.HandleFunc("POST /dashboard/actions/room-create", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		name, key := r.PostForm.Get("name"), r.PostForm.Get("key")
		refill, ok := formInt(r, "refill")
		run(w, r, "room-create", "name bytes "+strconv.Itoa(len(name)), "rooms", nil,
			ok && validRoomName(name) && key != "" && len(key) <= 64,
			func(ctx context.Context) error {
				_, err := d.store.CreateRoom(ctx, key, name, refill)
				return err
			}, "room_created", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/room-refill", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		room := r.PostForm.Get("room_id")
		refill, ok := formInt(r, "refill")
		run(w, r, "room-refill", "room "+roomTarget(room), "rooms", nil, ok && validRoomID(room),
			func(ctx context.Context) error { return d.store.SetRoomRefill(ctx, room, refill) }, "room_refill_saved", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/room-add", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		room, peer := r.PostForm.Get("room_id"), r.PostForm.Get("peer")
		valid := validRoomID(room) && peer != "" && len(peer) <= 256 && !strings.ContainsAny(peer, "\x00\r\n")
		target := "room " + roomTarget(room) + " add "
		if valid {
			target += peer
		}
		// Adding a current member changes nothing; the audit target records that.
		run(w, r, "room-add", target, "rooms", nil, valid, func(ctx context.Context) error {
			_, err := d.store.AddRoomMember(ctx, room, peer)
			return err
		}, "room_member_added", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/room-post", authed(sendFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		room, body, key := r.PostForm.Get("room_id"), r.PostForm.Get("body"), r.PostForm.Get("key")
		deadline, err := strconv.ParseFloat(r.PostForm.Get("deadline"), 64)
		valid := err == nil && validRoomID(room) && body != "" && len(body) <= maxBody && utf8.ValidString(body) && key != "" && len(key) <= 256
		var query url.Values
		if validRoomID(room) {
			query = url.Values{"room": {room}}
		}
		run(w, r, "room-post", "room "+roomTarget(room)+" bytes "+strconv.Itoa(len(body)), "rooms", query, valid,
			func(ctx context.Context) error {
				_, err := d.store.DashboardRoomPost(ctx, room, body, key, deadline)
				return err
			}, "room_posted", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/room-delete", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		room := r.PostForm.Get("room_id")
		run(w, r, "room-delete", "room "+roomTarget(room), "rooms", nil, validRoomID(room) && r.PostForm.Get("confirm") == "delete",
			func(ctx context.Context) error { return d.store.DeleteRoom(ctx, room) }, "room_deleted", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/room-remove", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		room := r.PostForm.Get("room_id")
		ordinal, ok := formInt(r, "ordinal")
		run(w, r, "room-remove", "room "+roomTarget(room)+" member "+strconv.FormatInt(ordinal, 10), "rooms", nil,
			ok && ordinal > 0 && validRoomID(room),
			func(ctx context.Context) error { return d.store.RemoveRoomMember(ctx, room, ordinal) }, "room_member_removed", nil)
	}))
}

// roomTarget is a room ID for an audit target, or a placeholder for invalid input.
func roomTarget(id string) string {
	if validRoomID(id) {
		return id
	}
	return "invalid"
}

// roomTailStart returns the sequence after which the newest page starts: the newest posts,
// at most dashboardRoomPage of them and, past the first, about one MiB of bodies, as
// roomTranscript then reads them in order.
func roomTailStart(ctx context.Context, tx *sql.Tx, roomID string) (int64, error) {
	var head int64
	if err := tx.QueryRowContext(ctx, `SELECT post_head FROM rooms WHERE room_id=?`, roomID).Scan(&head); err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,length(CAST(body AS BLOB)) FROM room_posts WHERE room_id=? ORDER BY seq DESC LIMIT ?`, roomID, dashboardRoomPage)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	start, size, n := head, 0, 0
	for rows.Next() {
		var seq int64
		var bytes int
		if err := rows.Scan(&seq, &bytes); err != nil {
			return 0, err
		}
		if n > 0 && size+bytes > 1<<20 {
			break
		}
		start, size, n = seq-1, size+bytes, n+1
	}
	return start, rows.Err()
}
