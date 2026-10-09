package core

import (
	"encoding/json"
	"net/http"
)

func (d *Daemon) checkoutRoutes(mux *http.ServeMux) {
	for _, operation := range []string{"status", "request"} {
		mux.HandleFunc("POST /v1/work/checkout-"+operation, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Caller    Key             `json:"caller"`
				Directory string          `json:"directory"`
				Note      json.RawMessage `json:"note,omitempty"`
			}
			if err := decode(w, r, &request); err != nil || request.Directory == "" || operation == "status" && len(request.Note) != 0 {
				failure(w, ErrInvalid)
				return
			}
			d.store.ToolCall(request.Caller)
			var result any
			var err error
			if operation == "status" {
				result, err = d.store.CheckoutStatus(r.Context(), request.Caller, request.Directory)
			} else {
				note := ""
				if len(request.Note) != 0 && (jsonKind(request.Note) != "string" || json.Unmarshal(request.Note, &note) != nil) {
					failure(w, ErrInvalid)
					return
				}
				result, err = d.store.RequestCheckout(r.Context(), request.Caller, request.Directory, note)
			}
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"ok": true, "result": result})
		})
	}
}
