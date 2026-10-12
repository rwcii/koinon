package core

import (
	"context"
	"net/http"
)

// libraryCaller checks the existing registration without creating a memory consumer,
// adoption decision or filesystem target. Plain peers can read the reference catalog.
func (s *Store) libraryCaller(ctx context.Context, caller Key) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return active(ctx, tx, s.now().UnixMilli(), caller)
}

func (d *Daemon) libraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/library/list", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			After  string `json:"after"`
			Limit  int    `json:"limit"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.libraryCaller(r.Context(), request.Caller); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		page, err := d.catalog.List(request.Caller.Family, request.After, request.Limit)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "library": page})
	})
	mux.HandleFunc("POST /v1/library/read", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			ItemID string `json:"item_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.libraryCaller(r.Context(), request.Caller); err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		item, err := d.catalog.Read(request.Caller.Family, request.ItemID)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "library": item})
	})
}
