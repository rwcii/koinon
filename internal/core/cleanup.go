package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// Cleanup from the command line (#388): koinon sessions list, purge, unpurge and retire,
// koinon inbox clear and koinon maintenance sweep. Each change runs the dashboard action's
// store path for one session at its current revision and writes that action's audit record,
// which also names its origin "cli". No agent tool calls these routes.

// cleanupLimit bounds the sessions that one selection reads.
const cleanupLimit = 1000

// CleanupFilter selects sessions by peer name and by state, family and inactivity. Names
// and the filters combine: a session must match every given one.
type CleanupFilter struct {
	Names []string `json:"names"`
	// State is active, expired, retired or purge-marked.
	State  string `json:"state"`
	Family string `json:"family"`
	// InactiveForSeconds selects sessions inactive for at least that long, by the retention
	// rule: since their retirement, otherwise since their expiry.
	InactiveForSeconds int64 `json:"inactive_for_seconds"`
}

// empty says that the filter selects nothing by itself.
func (f CleanupFilter) empty() bool {
	return len(f.Names) == 0 && f.State == "" && f.Family == "" && f.InactiveForSeconds == 0
}

// CleanupSession is one selected session as koinon sessions list shows it.
type CleanupSession struct {
	Name   string `json:"name"`
	Family string `json:"family"`
	ID     string `json:"id"`
	State  string `json:"state"`
	// InactiveSince is the retirement or expiry time, in daemon Unix milliseconds; 0 for an
	// active session.
	InactiveSince  int64 `json:"inactive_since,omitempty"`
	Unacknowledged int64 `json:"unacknowledged"`
	LiveClaim      bool  `json:"live_claim,omitempty"`
	PurgeMarked    bool  `json:"purge_marked,omitempty"`
	Revision       int64 `json:"revision"`
}

// CleanupResult is one session's outcome of a change.
type CleanupResult struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Code   string `json:"code,omitempty"`
}

// ErrCleanupSelection refuses a change without operands or filters.
var ErrCleanupSelection = Refusal{"invalid_request", "name sessions or give a filter: --state, --family or --inactive-for"}

// cleanupStates are the states a filter may name.
var cleanupStates = []string{"active", "expired", "retired", "purge-marked"}

// SelectCleanup returns the sessions that f selects, by peer name, at most cleanupLimit, and
// whether more matched. The maintainer session is never selected. Each name must be a
// session's peer name.
func (s *Store) SelectCleanup(ctx context.Context, f CleanupFilter) ([]CleanupSession, bool, error) {
	if (f.State != "" && !slices.Contains(cleanupStates, f.State)) || len(f.Family) > 64 || f.InactiveForSeconds < 0 || len(f.Names) > cleanupLimit {
		return nil, false, ErrInvalid
	}
	now := s.now().UnixMilli()
	query := `SELECT s.family,s.id,s.last_seq-s.acked_through,` + liveClaim + ` FROM sessions s
		JOIN names n ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id WHERE s.family!='maintainer'`
	var args []any
	if f.Family != "" {
		query += ` AND s.family=?`
		args = append(args, f.Family)
	}
	if f.InactiveForSeconds > 0 {
		query += ` AND ` + strings.ReplaceAll(inactiveSince, "?1", "?")
		cutoff := now - f.InactiveForSeconds*1000
		args = append(args, cutoff, cutoff)
	}
	if len(f.Names) > 0 {
		for _, name := range f.Names {
			var exists int
			err := s.db.QueryRowContext(ctx, `SELECT 1 FROM names WHERE kind='peer' AND name=? AND family!='maintainer'`, name).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, false, Refusal{"peer_not_found", "no session has the peer name " + name}
			}
			if err != nil {
				return nil, false, err
			}
		}
		query += ` AND n.name IN (?` + strings.Repeat(",?", len(f.Names)-1) + `)`
		for _, name := range f.Names {
			args = append(args, name)
		}
	}
	query += ` ORDER BY n.name LIMIT ?`
	args = append(args, cleanupLimit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	type row struct {
		k         Key
		unacked   int64
		liveClaim bool
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.k.Family, &r.k.ID, &r.unacked, &r.liveClaim); err != nil {
			rows.Close()
			return nil, false, err
		}
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(found) > cleanupLimit
	if more {
		found = found[:cleanupLimit]
	}
	list := []CleanupSession{}
	for _, r := range found {
		session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.k.Family, r.k.ID), now)
		if err != nil {
			return nil, false, err
		}
		c := CleanupSession{Name: session.Name, Family: session.Family, ID: session.ID, State: session.State,
			Unacknowledged: r.unacked, LiveClaim: r.liveClaim, PurgeMarked: session.PurgeAt != 0, Revision: session.Revision}
		switch {
		case session.RetiredAt != 0:
			c.InactiveSince = session.RetiredAt
		case session.State != "active":
			c.InactiveSince = session.ExpiresAt
		}
		if f.State == "purge-marked" && !c.PurgeMarked || f.State != "" && f.State != "purge-marked" && f.State != c.State {
			continue
		}
		list = append(list, c)
	}
	return list, more, nil
}

// cleanupApplies says whether action applies to a selected session: retire to an active
// one, purge to an unmarked one, unpurge to a marked one, clear to any.
func cleanupApplies(action string, c CleanupSession) bool {
	switch action {
	case "retire":
		return c.State == "active"
	case "purge":
		return !c.PurgeMarked
	case "unpurge":
		return c.PurgeMarked
	}
	return true
}

// applyCleanup runs action for one session at its revision under its own audit record.
func (s *Store) applyCleanup(ctx context.Context, action string, c CleanupSession) CleanupResult {
	ctx, pending := withAudit(ctx, action, c.Family+":"+c.ID)
	pending.origin = "cli"
	k := Key{c.Family, c.ID}
	var err error
	switch action {
	case "purge", "unpurge":
		err = s.markPurge(ctx, k, c.Revision, action == "purge")
	case "retire":
		_, err = s.Mutate(ctx, Mutation{Family: k.Family, ID: k.ID, IfRevision: c.Revision}, true)
	case "clear":
		_, err = s.ack(ctx, k, -1, true)
	}
	if err == nil {
		return CleanupResult{Name: c.Name, Result: "accepted"}
	}
	code := errorCode(err)
	if errors.Is(err, ErrConflict) {
		code = "revision_changed"
	}
	s.auditRefusal(ctx, pending, code)
	return CleanupResult{Name: c.Name, Result: "refused", Code: code}
}

// cleanupRoutes adds the command line's cleanup routes.
func (d *Daemon) cleanupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/cleanup/sessions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			CleanupFilter
			// Action is list, purge, unpurge, retire or clear. Apply makes the change; without
			// it, the reply shows the selection only.
			Action string `json:"action"`
			Apply  bool   `json:"apply"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if !slices.Contains([]string{"list", "purge", "unpurge", "retire", "clear"}, request.Action) {
			failure(w, ErrInvalid)
			return
		}
		if request.Action != "list" && request.CleanupFilter.empty() {
			failure(w, ErrCleanupSelection)
			return
		}
		selected, more, err := d.store.SelectCleanup(r.Context(), request.CleanupFilter)
		if err != nil {
			failure(w, err)
			return
		}
		sessions := []CleanupSession{}
		for _, c := range selected {
			if request.Action == "list" || cleanupApplies(request.Action, c) {
				sessions = append(sessions, c)
			}
		}
		reply := map[string]any{"ok": true, "action": request.Action, "sessions": sessions, "truncated": more}
		if request.Action != "list" {
			reply["applied"] = request.Apply
		}
		if request.Apply && request.Action != "list" {
			results := []CleanupResult{}
			for _, c := range sessions {
				results = append(results, d.store.applyCleanup(r.Context(), request.Action, c))
			}
			reply["results"] = results
			d.signalWorkNotices()
		}
		respond(w, 200, reply)
	})
	mux.HandleFunc("POST /v1/maintenance/sweep", func(w http.ResponseWriter, r *http.Request) {
		if err := d.sweep(r.Context()); err != nil {
			failure(w, err)
			return
		}
		status, err := d.store.RetentionStatus(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "counts": status.Last, "marked_for_purge": status.MarkedForPurge})
	})
}
