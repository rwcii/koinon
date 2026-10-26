package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Cleanup from the command line (#388): koinon sessions list, purge, unpurge and retire,
// koinon inbox clear and koinon maintenance sweep. Each change runs the dashboard action's
// store path for one session at its current revision and writes that action's audit record,
// which also names its origin "cli". No agent tool calls these routes.

// cleanupLimit bounds the sessions that one selection reads; a variable only so that tests
// can lower it.
var cleanupLimit = 1000

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

// SelectCleanup returns the sessions that f selects and action applies to (list: all), by
// peer name, at most cleanupLimit, and whether more matched. Every filter is applied before
// the limit. The maintainer session is never selected. Each name must be a session's peer
// name.
func (s *Store) SelectCleanup(ctx context.Context, f CleanupFilter, action string) ([]CleanupSession, bool, error) {
	if (f.State != "" && !slices.Contains(cleanupStates, f.State)) || len(f.Family) > 64 || f.InactiveForSeconds < 0 || len(f.Names) > cleanupLimit {
		return nil, false, ErrInvalid
	}
	now := s.now().UnixMilli()
	// The state as scanSession computes it, at now.
	state := `CASE WHEN s.retired_at!=0 THEN 'retired' WHEN s.expires_at<=? THEN 'expired' ELSE 'active' END`
	query := `SELECT s.family,s.id,s.last_seq-s.acked_through,` + liveClaim + ` FROM sessions s
		JOIN names n ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id WHERE s.family!='maintainer'`
	var args []any
	switch {
	case f.State == "purge-marked" || action == "unpurge":
		query += ` AND s.purge_at!=0`
	case action == "purge":
		query += ` AND s.purge_at=0`
	}
	if f.State != "" && f.State != "purge-marked" {
		query += ` AND ` + state + `=?`
		args = append(args, now, f.State)
	}
	if action == "retire" {
		query += ` AND ` + state + `='active'`
		args = append(args, now)
	}
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
		list = append(list, c)
	}
	return list, more, nil
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
		_, err = s.ackAt(ctx, k, c.Revision)
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
		sessions, more, err := d.store.SelectCleanup(r.Context(), request.CleanupFilter, request.Action)
		if err != nil {
			failure(w, err)
			return
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
	mux.HandleFunc("POST /v1/cleanup/claims", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			HeldBy []string `json:"held_by"`
			Apply  bool     `json:"apply"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		claims, more, err := d.store.InactiveClaims(r.Context(), request.HeldBy)
		if err != nil {
			failure(w, err)
			return
		}
		reply := map[string]any{"ok": true, "claims": claims, "truncated": more, "applied": request.Apply}
		if request.Apply {
			results := []CleanupResult{}
			for _, c := range claims {
				results = append(results, d.store.releaseClaim(r.Context(), c))
			}
			reply["results"] = results
			d.signalWorkNotices()
		}
		respond(w, 200, reply)
	})
	mux.HandleFunc("POST /v1/cleanup/rooms", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			RoomIDs []string `json:"room_ids"`
			Apply   bool     `json:"apply"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		members, err := d.store.InactiveRoomMembers(r.Context(), request.RoomIDs)
		if err != nil {
			failure(w, err)
			return
		}
		reply := map[string]any{"ok": true, "members": members, "applied": request.Apply}
		if request.Apply {
			results := []CleanupResult{}
			for _, m := range members {
				results = append(results, d.store.pruneRoomMember(r.Context(), m))
			}
			reply["results"] = results
		}
		respond(w, 200, reply)
	})
	mux.HandleFunc("POST /v1/rooms/delete", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			RoomID string `json:"room_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		ctx, pending := withAudit(r.Context(), "room-delete", "room "+roomTarget(request.RoomID))
		pending.origin = "cli"
		if err := d.store.DeleteRoom(ctx, request.RoomID); err != nil {
			d.store.auditRefusal(ctx, pending, errorCode(err))
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "room_id": request.RoomID, "deleted": true})
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

// InactiveClaim is a live claim whose holder session is expired or retired, as koinon claims
// release shows it.
type InactiveClaim struct {
	Holder     string `json:"holder"`
	Repository string `json:"repository"`
	WorkID     string `json:"work_id"`
	Title      string `json:"title"`
	Revision   int64  `json:"revision"`
	Generation int64  `json:"generation"`
	Consumer   string `json:"consumer"`
}

// InactiveClaims lists, at most cleanupLimit, the live claims held by expired or retired
// sessions, by the retention sweep's live-claim rule, optionally only those of the sessions
// with the peer names heldBy. A participant's claims belong to the participant and are not
// listed: the next holder continues them.
func (s *Store) InactiveClaims(ctx context.Context, heldBy []string) ([]InactiveClaim, bool, error) {
	if len(heldBy) > cleanupLimit {
		return nil, false, ErrInvalid
	}
	for _, name := range heldBy {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM names WHERE kind='peer' AND name=? AND family!='maintainer'`, name).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, Refusal{"peer_not_found", "no session has the peer name " + name}
		}
		if err != nil {
			return nil, false, err
		}
	}
	query := `SELECT n.name,m.repository,w.work_id,w.title,w.revision,b.generation,b.consumer
		FROM claim_bundles b JOIN work_items w ON w.store=b.store AND w.work_id=b.work_id
		JOIN memory_stores m ON m.store_id=b.store
		JOIN sessions s ON b.consumer=s.family||':'||s.id OR (b.native_actor!='' AND b.native_actor=koinon_native_work_actor(s.family,s.id))
		JOIN names n ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id
		WHERE b.active=1 AND s.family!='maintainer' AND (s.retired_at!=0 OR s.expires_at<=?)`
	args := []any{s.now().UnixMilli()}
	if len(heldBy) > 0 {
		query += ` AND n.name IN (?` + strings.Repeat(",?", len(heldBy)-1) + `)`
		for _, name := range heldBy {
			args = append(args, name)
		}
	}
	query += ` ORDER BY n.name,m.repository,w.work_id LIMIT ?`
	args = append(args, cleanupLimit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	claims := []InactiveClaim{}
	for rows.Next() {
		var c InactiveClaim
		if err := rows.Scan(&c.Holder, &c.Repository, &c.WorkID, &c.Title, &c.Revision, &c.Generation, &c.Consumer); err != nil {
			return nil, false, err
		}
		claims = append(claims, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(claims) > cleanupLimit
	if more {
		claims = claims[:cleanupLimit]
	}
	return claims, more, nil
}

// releaseClaim releases one claim as the dashboard's release does, on behalf of its owner,
// under its own audit record by cli.
func (s *Store) releaseClaim(ctx context.Context, c InactiveClaim) CleanupResult {
	name := c.Repository + " " + c.WorkID
	ctx, pending := withAudit(ctx, "release", c.Repository+" "+c.WorkID+" owner "+c.Consumer)
	pending.origin = "cli"
	fields := map[string]json.RawMessage{}
	for field, value := range map[string]any{"work_id": c.WorkID, "if_revision": c.Revision, "claim_generation": c.Generation,
		"checkpoint": "Released by the maintainer from the command line"} {
		fields[field], _ = json.Marshal(value)
	}
	_, err := s.Work(ctx, MemoryCaller{Repository: c.Repository, Family: maintainerKey.Family, Name: "maintainer", Consumer: c.Consumer}, "work-release", fields)
	if err == nil {
		return CleanupResult{Name: name, Result: "accepted"}
	}
	code := errorCode(err)
	s.auditRefusal(ctx, pending, code)
	return CleanupResult{Name: name, Result: "refused", Code: code}
}

// InactiveRoomMember is a room's current member whose session is expired or retired, as
// koinon rooms prune shows it. A participant member is never listed: the participant stays.
type InactiveRoomMember struct {
	RoomID  string `json:"room_id"`
	Room    string `json:"room"`
	Ordinal int64  `json:"ordinal"`
	Label   string `json:"label"`
	State   string `json:"state"`
}

// InactiveRoomMembers lists the inactive members of the rooms roomIDs, or of every room.
func (s *Store) InactiveRoomMembers(ctx context.Context, roomIDs []string) ([]InactiveRoomMember, error) {
	if len(roomIDs) > 64 {
		return nil, ErrInvalid
	}
	query := `SELECT m.room_id,r.name,m.ordinal,m.label,CASE WHEN s.retired_at!=0 THEN 'retired' ELSE 'expired' END
		FROM room_members m JOIN rooms r ON r.room_id=m.room_id JOIN sessions s ON s.family=m.family AND s.id=m.member_id
		WHERE m.active=1 AND (s.retired_at!=0 OR s.expires_at<=?)`
	args := []any{s.now().UnixMilli()}
	if len(roomIDs) > 0 {
		for _, id := range roomIDs {
			var exists int
			err := s.db.QueryRowContext(ctx, `SELECT 1 FROM rooms WHERE room_id=?`, id).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) || !validRoomID(id) {
				return nil, ErrRoomNotFound
			}
			if err != nil {
				return nil, err
			}
		}
		query += ` AND m.room_id IN (?` + strings.Repeat(",?", len(roomIDs)-1) + `)`
		for _, id := range roomIDs {
			args = append(args, id)
		}
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY r.created_at,m.room_id,m.ordinal`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []InactiveRoomMember{}
	for rows.Next() {
		var m InactiveRoomMember
		if err := rows.Scan(&m.RoomID, &m.Room, &m.Ordinal, &m.Label, &m.State); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// pruneRoomMember removes one member as the dashboard's room-remove does, under its own
// audit record by cli.
func (s *Store) pruneRoomMember(ctx context.Context, m InactiveRoomMember) CleanupResult {
	name := m.RoomID + " " + m.Label
	ctx, pending := withAudit(ctx, "room-remove", "room "+m.RoomID+" member "+strconv.FormatInt(m.Ordinal, 10))
	pending.origin = "cli"
	if err := s.RemoveRoomMember(ctx, m.RoomID, m.Ordinal); err != nil {
		code := errorCode(err)
		s.auditRefusal(ctx, pending, code)
		return CleanupResult{Name: name, Result: "refused", Code: code}
	}
	return CleanupResult{Name: name, Result: "accepted"}
}

// DeleteRoom deletes a room with its memberships, posts and delivery records (#388). The
// copies of its posts already in members' inboxes stay until their own retention; a deleted
// room's ID is never reused, because room IDs are random.
func (s *Store) DeleteRoom(ctx context.Context, roomID string) error {
	if !validRoomID(roomID) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	room, err := roomRow(ctx, tx.Tx, roomID)
	if err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM room_deliveries WHERE post_id IN (SELECT post_id FROM room_posts WHERE room_id=?)`,
		`DELETE FROM room_posts WHERE room_id=?`,
		`DELETE FROM room_members WHERE room_id=?`,
		`DELETE FROM rooms WHERE room_id=?`,
		// A deleted room has no change version (PDR 009, Decision 3).
		`DELETE FROM change_versions WHERE topic=?`,
	} {
		if _, err := tx.ExecContext(ctx, statement, roomID); err != nil {
			return tx.fail(err)
		}
	}
	setAuditTarget(ctx, "room "+roomID+" delete name bytes "+strconv.Itoa(len(room.Name)))
	return tx.Commit()
}
