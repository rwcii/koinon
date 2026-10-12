package core

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// roomsData is the rooms view (#217, chunk 01): every room with its current and former
// members, the active peer names that the add form suggests and the create form's key.
type roomsData struct {
	Rooms []Room
	Peers []peerOption
	Key   string
}

func (d *Daemon) roomsView(ctx context.Context) (roomsData, error) {
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
	return roomsData{Rooms: rooms, Peers: peers, Key: key}, err
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
