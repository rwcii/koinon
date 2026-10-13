package core

import (
	"context"
	"github.com/rwcii/koinon/internal/library"
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
	mux.HandleFunc("POST /v1/library/context", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key `json:"caller"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.libraryCaller(r.Context(), request.Caller); err != nil {
			failure(w, err)
			return
		}
		// Internal native-client context: never exposed as a model tool response.
		var directory, repository string
		if err := d.store.db.QueryRowContext(r.Context(), `SELECT directory,repository FROM sessions WHERE family=? AND id=?`, request.Caller.Family, request.Caller.ID).Scan(&directory, &repository); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "directory": directory, "repository": repository})
	})
	mux.HandleFunc("POST /v1/library/decision/read", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller Key    `json:"caller"`
			Root   string `json:"root_digest"`
			Item   string `json:"item_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		value, err := d.store.LibraryDecisionRead(r.Context(), request.Caller, request.Root, request.Item)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "result": value})
	})
	mux.HandleFunc("POST /v1/library/decision", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller   Key              `json:"caller"`
			Root     string           `json:"root_digest"`
			Compared int64            `json:"if_revision"`
			Decision library.Decision `json:"decision"`
		}
		if err := decodeLimit(w, r, &request, 6*library.MaxDecisionBytes+16384); err != nil {
			failure(w, err)
			return
		}
		value, err := d.store.LibraryDecision(r.Context(), request.Caller, d.catalog, request.Root, request.Compared, request.Decision)
		if err != nil {
			failure(w, err)
			return
		}
		d.store.ToolCall(request.Caller)
		respond(w, 200, map[string]any{"ok": true, "result": value})
	})
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
