package core

import (
	"net/http"
)

// messageRoutes adds the message API. Every request names its caller, which must be an
// active session; a caller reads and acknowledges only its own inbox.
func (d *Daemon) messageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/wake/agy-stop", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key `json:"caller"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if request.Caller.Family != "agy" || !validKey(request.Caller.Family, request.Caller.ID) {
			failure(w, ErrInvalid)
			return
		}
		d.store.wake.mu.Lock()
		defer d.store.wake.mu.Unlock()
		notice, err := d.store.offerWake(r.Context(), request.Caller)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "notice": notice})
	})
	mux.HandleFunc("POST /v1/peers", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key `json:"caller"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		items, truncated, err := d.store.Peers(r.Context(), request.Caller)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "peers": items, "truncated": truncated})
	})
	mux.HandleFunc("POST /v1/messages/send", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			To     string `json:"to"`
			Body   string `json:"body"`
		}
		if err := decodeLimit(w, r, &request, 6*maxBody+4096); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		result, err := d.store.Send(r.Context(), request.Caller, request.To, request.Body)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "message": result})
	})
	mux.HandleFunc("POST /v1/inbox/read", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller           Key    `json:"caller"`
			After            int64  `json:"after"`
			ParticipantAfter *int64 `json:"participant_after"`
			Limit            int64  `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		result, err := d.store.ReadInboxes(r.Context(), request.Caller, request.After, request.ParticipantAfter, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "inbox": result})
	})
	mux.HandleFunc("POST /v1/inbox/ack", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller             Key    `json:"caller"`
			Through            int64  `json:"through"`
			ParticipantThrough *int64 `json:"participant_through"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		reply := map[string]any{"ok": true}
		// The participant inbox is acknowledged first, so a stale holder changes nothing.
		if request.ParticipantThrough != nil {
			address, acked, err := d.store.AckParticipant(r.Context(), request.Caller, *request.ParticipantThrough)
			if err != nil {
				failure(w, err)
				return
			}
			reply["participant"] = map[string]any{"address": address, "acked_through": acked}
		}
		acked, err := d.store.Ack(r.Context(), request.Caller, request.Through)
		if err != nil {
			failure(w, err)
			return
		}
		reply["acked_through"] = acked
		respond(w, 200, reply)
	})
	mux.HandleFunc("POST /v1/messages/outcome", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    Key   `json:"caller"`
			MessageID int64 `json:"message_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		result, err := d.store.MessageOutcome(r.Context(), request.Caller, request.MessageID)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "message": result})
	})
}
