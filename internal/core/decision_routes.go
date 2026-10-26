package core

import "net/http"

// decisionRoutes serve decision_create, decision_edit, decision_list and decision_get
// (#206, chunk 01), decision_history, decision_pending, decision_ack, decision_close and
// decision_restore (chunk 02), and decision_withdraw (#504). No agent route gives a verdict: only the maintainer does, on the dashboard or with a maintainer token (PDR 009). The caller comes from current native admission. A request with any
// other field, such as a verdict, dismissal, audit or agent-row field, is refused.
func (d *Daemon) decisionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/decisions/create", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    Key      `json:"caller"`
			Reference string   `json:"reference"`
			Key       string   `json:"key"`
			Deadline  *float64 `json:"deadline"`
			DecisionFields
		}
		if err := decodeLimit(w, r, &request, decisionRequestBytes); err != nil {
			failure(w, err)
			return
		}
		if request.Deadline == nil {
			failure(w, decisionInvalid("key and deadline", "are required"))
			return
		}
		result, err := d.store.DecisionCreate(r.Context(), request.Caller, request.Reference, request.DecisionFields, request.Key, *request.Deadline)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": result})
	})
	mux.HandleFunc("POST /v1/decisions/edit", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller     Key    `json:"caller"`
			Reference  string `json:"reference"`
			IfRevision *int64 `json:"if_revision"`
			DecisionFields
		}
		if err := decodeLimit(w, r, &request, decisionRequestBytes); err != nil {
			failure(w, err)
			return
		}
		if request.IfRevision == nil {
			failure(w, decisionInvalid("if_revision", "is required"))
			return
		}
		item, err := d.store.DecisionEdit(r.Context(), request.Caller, request.Reference, *request.IfRevision, request.DecisionFields)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": item})
	})
	mux.HandleFunc("POST /v1/decisions/list", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			Filter string `json:"filter"`
			After  int64  `json:"after"`
			Limit  int    `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		page, err := d.store.DecisionList(r.Context(), request.Caller, request.Filter, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": page})
	})
	mux.HandleFunc("POST /v1/decisions/get", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    Key    `json:"caller"`
			Reference string `json:"reference"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		item, err := d.store.DecisionGet(r.Context(), request.Caller, request.Reference)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": item})
	})
	mux.HandleFunc("POST /v1/decisions/history", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    Key    `json:"caller"`
			Reference string `json:"reference"`
			After     int64  `json:"after"`
			Limit     int    `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		page, err := d.store.DecisionHistory(r.Context(), request.Caller, request.Reference, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": page})
	})
	mux.HandleFunc("POST /v1/decisions/pending", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key   `json:"caller"`
			After  int64 `json:"after"`
			Limit  int   `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		page, err := d.store.DecisionPending(r.Context(), request.Caller, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": page})
	})
	mux.HandleFunc("POST /v1/decisions/ack", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller     Key     `json:"caller"`
			VerdictIDs []int64 `json:"verdict_ids"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		acknowledged, err := d.store.DecisionAck(r.Context(), request.Caller, request.VerdictIDs)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": map[string]any{"acknowledged": acknowledged}})
	})
	mux.HandleFunc("POST /v1/decisions/close", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller     Key      `json:"caller"`
			Reference  string   `json:"reference"`
			IfRevision *int64   `json:"if_revision"`
			Note       string   `json:"note"`
			Key        string   `json:"key"`
			Deadline   *float64 `json:"deadline"`
		}
		if err := decodeLimit(w, r, &request, 6*decisionNoteLimit+8192); err != nil {
			failure(w, err)
			return
		}
		if request.IfRevision == nil || request.Deadline == nil {
			failure(w, decisionInvalid("if_revision, key and deadline", "are required"))
			return
		}
		item, err := d.store.DecisionClose(r.Context(), request.Caller, request.Reference, *request.IfRevision, request.Note, request.Key, *request.Deadline)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": item})
	})
	mux.HandleFunc("POST /v1/decisions/withdraw", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller     Key      `json:"caller"`
			Reference  string   `json:"reference"`
			IfRevision *int64   `json:"if_revision"`
			Reason     string   `json:"reason"`
			Key        string   `json:"key"`
			Deadline   *float64 `json:"deadline"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if request.IfRevision == nil || request.Deadline == nil {
			failure(w, decisionInvalid("if_revision, reason, key and deadline", "are required"))
			return
		}
		item, err := d.store.DecisionWithdraw(r.Context(), request.Caller, request.Reference, *request.IfRevision, request.Reason, request.Key, *request.Deadline)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": item})
	})
	mux.HandleFunc("POST /v1/decisions/restore", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller     Key    `json:"caller"`
			Reference  string `json:"reference"`
			IfRevision int64  `json:"if_revision"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		item, err := d.store.DecisionRestore(r.Context(), request.Caller, request.Reference, request.IfRevision)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": item})
	})
}
