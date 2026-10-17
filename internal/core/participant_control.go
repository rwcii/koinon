package core

import (
	"context"
	"net/http"
)

// participantRoutes adds the command line's participant list and holder choice (#398). The
// choice uses the dashboard's store path and writes the same audit record, which also names
// its origin "cli". No agent tool calls these routes.
func (d *Daemon) participantRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/participants", func(w http.ResponseWriter, r *http.Request) {
		list, truncated, err := d.store.Participants(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "participants": list, "truncated": truncated})
	})
	mux.HandleFunc("POST /v1/participants/holder", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Address    string `json:"address"`
			Peer       string `json:"peer"`
			IfRevision int64  `json:"if_revision"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		target := "participant holder invalid"
		valid := request.Address != "" && len(request.Address) <= 256 && validContextPeer(request.Peer) && validContextPeer(request.Address)
		if valid {
			target = request.Address + " " + request.Peer
		}
		ctx, pending := withAudit(r.Context(), "participant-holder", target)
		pending.origin = "cli"
		former, err := func(ctx context.Context) (string, error) {
			if !valid {
				return "", ErrInvalid
			}
			return d.store.ChooseHolderByPeer(ctx, request.Address, request.Peer, request.IfRevision)
		}(ctx)
		if err != nil {
			d.store.auditRefusal(ctx, pending, errorCode(err))
			failure(w, err)
			return
		}
		d.signalWorkNotices()
		respond(w, 200, map[string]any{"ok": true, "address": request.Address, "holder": request.Peer, "former": former})
	})
}
