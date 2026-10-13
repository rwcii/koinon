package core

import "net/http"

// decisionRoutes serve decision_create, decision_edit, decision_list and decision_get
// (#206, chunk 01). The caller comes from current native admission. A request with any
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
}
