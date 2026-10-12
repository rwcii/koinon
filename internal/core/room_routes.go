package core

import "net/http"

// roomRoutes serve room_list and room_members. The caller comes from current native
// admission: the MCP server's verified identity or DeepSeek's registered command session.
func (d *Daemon) roomRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/rooms/list", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			After  string `json:"after"`
			Limit  int    `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		rooms, more, err := d.store.RoomList(r.Context(), request.Caller, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		result := map[string]any{"rooms": rooms, "more": more}
		if rooms == nil {
			result["rooms"] = []RoomMembership{}
		}
		if more {
			result["next_after"] = rooms[len(rooms)-1].RoomID
		}
		respond(w, 200, map[string]any{"ok": true, "result": result})
	})
	mux.HandleFunc("POST /v1/rooms/members", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			RoomID string `json:"room_id"`
			After  int64  `json:"after"`
			Limit  int    `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		room, more, err := d.store.RoomMembers(r.Context(), request.Caller, request.RoomID, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		result := map[string]any{"room_id": room.ID, "name": room.Name, "revision": room.Revision, "members": room.Members, "more": more}
		if more {
			result["next_after"] = room.Members[len(room.Members)-1].Ordinal
		}
		respond(w, 200, map[string]any{"ok": true, "result": result})
	})
}
