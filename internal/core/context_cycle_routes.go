package core

import "net/http"

// cycleRoutes adds the own-cycle API (#196). The native client names the caller and
// attaches its verified current epoch; no request names another session, a host, a pane
// or a permission.
func (d *Daemon) cycleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/cycles/status", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key `json:"caller"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		cycle, err := d.store.ContextCycleStatus(r.Context(), request.Caller)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "cycle": cycle})
	})
	mux.HandleFunc("POST /v1/cycles/ready", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller   Key    `json:"caller"`
			Epoch    string `json:"epoch"`
			CycleID  string `json:"cycle_id"`
			Revision int64  `json:"revision"`
			Artifact string `json:"artifact"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		cycle, err := d.store.ContextCycleReady(r.Context(), request.Caller, request.Epoch, request.CycleID, request.Revision, request.Artifact)
		if err == nil {
			// The requester stays paused: the clear waits for its idle prompt (#431).
			next := cycleReadyNext
			if request.Caller.Family == "deepseek" {
				next = deepSeekReadyNext
			}
			respond(w, 200, map[string]any{"ok": true, "cycle": cycle, "next": next})
			return
		}
		cycleReply(w, cycle, err)
	})
	mux.HandleFunc("POST /v1/cycles/pickup", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller   Key    `json:"caller"`
			Epoch    string `json:"epoch"`
			CycleID  string `json:"cycle_id"`
			Revision int64  `json:"revision"`
			Artifact string `json:"artifact"`
			Outcome  string `json:"outcome"`
			Report   string `json:"report"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		cycle, err := d.store.ContextCyclePickup(r.Context(), request.Caller, request.Epoch, request.CycleID, request.Revision,
			request.Artifact, request.Outcome, request.Report)
		cycleReply(w, cycle, err)
	})
}

// cycleReply answers a ready or pickup call. A stale or conflicting report returns the
// cycle's current state with its refusal, so the caller can read where it stands.
func cycleReply(w http.ResponseWriter, cycle CycleView, err error) {
	if err != nil {
		if refusal, ok := err.(Refusal); ok && refusal == ErrCycleChanged && cycle.ID != "" {
			respond(w, http.StatusConflict, map[string]any{"ok": false, "code": refusal.Code, "error": refusal.Message, "details": map[string]any{"cycle": cycle}})
			return
		}
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "cycle": cycle})
}
