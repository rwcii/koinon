package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Context settings outside the dashboard (#391): the command line and agents, through MCP,
// read and change the family mode and threshold, a session's Enable and a session's own
// threshold. Each change uses the dashboard's store path and writes the same audit record,
// which also names its origin: "cli", or "agent PEER" for a calling session.

// ContextSessionView is one active session's context-cycle settings and trigger state, as
// koinon context show and context_show report them. It holds no epoch, host or pane.
type ContextSessionView struct {
	Peer           string `json:"peer"`
	Family         string `json:"family"`
	Enabled        bool   `json:"enabled"`
	EnableRevision int64  `json:"enable_revision"`
	Mode           string `json:"mode"`
	Threshold      int64  `json:"threshold"`
	// ThresholdSource is "session" for the session's own threshold, otherwise "family".
	ThresholdSource string `json:"threshold_source"`
	Available       bool   `json:"available"`
	Unavailable     string `json:"unavailable,omitempty"`
	Eligible        bool   `json:"eligible"`
	Reason          string `json:"reason,omitempty"`
	CycleID         string `json:"cycle_id,omitempty"`
	CycleRevision   int64  `json:"cycle_revision,omitempty"`
	CycleState      string `json:"cycle_state,omitempty"`
	// Dialog is the native dialog that the session's managed cycle waits on, as the Sessions
	// page shows it. The Sessions page or keys in the session's pane answer it.
	Dialog *CycleDialog `json:"dialog,omitempty"`
}

// contextShowPage bounds one page of koinon context show: each row runs its adapter's Probe.
const contextShowPage = 100

// ContextShow returns the family policies and one page of the active agent sessions' context
// state, by peer name after after. next is the last peer name of a full page whose
// continuation may hold more sessions, or "".
func (s *Store) ContextShow(ctx context.Context, after string) ([]ContextFamilyPolicy, []ContextSessionView, string, error) {
	now := s.now().UnixMilli()
	// Active sessions are selected before the page bound, so retained ended sessions never
	// take their place.
	rows, err := s.db.QueryContext(ctx, `SELECT n.family,n.session_id FROM names n JOIN sessions s ON s.family=n.family AND s.id=n.session_id
		WHERE n.kind='peer' AND n.family!='maintainer' AND `+notParticipantRow+` AND s.retired_at=0 AND s.expires_at>? AND n.name>?
		ORDER BY n.name LIMIT ?`, now, after, contextShowPage)
	if err != nil {
		return nil, nil, "", err
	}
	var keys []Key
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.Family, &k.ID); err != nil {
			rows.Close()
			return nil, nil, "", err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, "", err
	}
	enables, err := s.contextEnables(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	views, next := []ContextSessionView{}, ""
	for _, k := range keys {
		session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), now)
		if err != nil {
			return nil, nil, "", err
		}
		if len(keys) == contextShowPage {
			next = session.Name
		}
		if session.State != "active" {
			continue
		}
		row := s.contextRowOf(session, enables[k], now)
		v := ContextSessionView{Peer: session.Name, Family: session.Family, Enabled: row.Enable.Enabled, EnableRevision: row.Enable.Revision,
			Mode: row.Mode, Threshold: row.Threshold, ThresholdSource: "family", Available: row.Available, Unavailable: row.Unavailable,
			Eligible: row.Eligible, Reason: row.Reason}
		if row.Enable.Threshold > 0 {
			v.ThresholdSource = "session"
		}
		if row.Managed != nil {
			v.CycleID, v.CycleRevision, v.CycleState, v.Dialog = row.Managed.ID, row.Managed.Revision, row.Managed.State, row.Dialog
		}
		views = append(views, v)
	}
	return s.ContextPolicy(), views, next, nil
}

// validContextPeer bounds a peer name that a context change names, before it reaches the
// audit record.
func validContextPeer(peer string) bool {
	return len(peer) > 0 && len(peer) <= 256 && !strings.ContainsAny(peer, "\x00\r\n")
}

// callerPeer is a calling session's peer name.
func (s *Store) callerPeer(ctx context.Context, caller Key) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=? AND family!='maintainer'`,
		caller.Family, caller.ID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrCallerInactive
	}
	return name, err
}

// contextRoutes adds the context settings API (#391). A request without a caller comes from
// the command line; one with a caller comes from that session through MCP, and without a
// peer it names the caller's own session.
func (d *Daemon) contextRoutes(mux *http.ServeMux) {
	// origin resolves the request's origin and the session it names.
	origin := func(ctx context.Context, caller *Key, peer string) (string, string, error) {
		if caller == nil {
			return "cli", peer, nil
		}
		d.store.ToolCall(*caller)
		name, err := d.store.callerPeer(ctx, *caller)
		if err != nil {
			return "", "", err
		}
		if peer == "" {
			peer = name
		}
		return "agent " + name, peer, nil
	}
	// change runs one audited change under its origin, as the dashboard's actions do.
	change := func(w http.ResponseWriter, r *http.Request, action, target, from string, apply func(ctx context.Context) (any, error)) {
		ctx, pending := withAudit(r.Context(), action, target)
		pending.origin = from
		result, err := apply(ctx)
		if err != nil {
			d.store.auditRefusal(ctx, pending, errorCode(err))
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "result": result})
	}
	mux.HandleFunc("POST /v1/context/show", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller *Key   `json:"caller"`
			After  string `json:"after"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if _, _, err := origin(r.Context(), request.Caller, ""); err != nil {
			failure(w, err)
			return
		}
		families, sessions, next, err := d.store.ContextShow(r.Context(), request.After)
		if err != nil {
			failure(w, err)
			return
		}
		reply := map[string]any{"ok": true, "families": families, "sessions": sessions}
		if next != "" {
			reply["next_after"] = next
		}
		respond(w, 200, reply)
	})
	mux.HandleFunc("POST /v1/context/set", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    *Key   `json:"caller"`
			Family    string `json:"family"`
			Mode      string `json:"mode"`
			Threshold int64  `json:"threshold"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		from, _, err := origin(r.Context(), request.Caller, "")
		if err != nil {
			failure(w, err)
			return
		}
		target := "context invalid"
		if contextFamily(request.Family) {
			target = "context " + request.Family
		}
		change(w, r, "context-family-settings", target, from, func(ctx context.Context) (any, error) {
			if err := d.store.SetContextPolicy(ctx, request.Family, request.Mode, request.Threshold); err != nil {
				return nil, err
			}
			return d.store.familyContextPolicy(request.Family), nil
		})
	})
	mux.HandleFunc("POST /v1/context/enable", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller  *Key   `json:"caller"`
			Peer    string `json:"peer"`
			Enabled *bool  `json:"enabled"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		from, peer, err := origin(r.Context(), request.Caller, request.Peer)
		if err == nil && (request.Enabled == nil || !validContextPeer(peer)) {
			err = ErrInvalid
		}
		if err != nil {
			failure(w, err)
			return
		}
		state := "off"
		if *request.Enabled {
			state = "on"
		}
		change(w, r, "context-enable", "context enable "+peer+" "+state, from, func(ctx context.Context) (any, error) {
			return d.store.SetContextEnableNow(ctx, peer, *request.Enabled)
		})
	})
	mux.HandleFunc("POST /v1/context/threshold", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Caller    *Key   `json:"caller"`
			Peer      string `json:"peer"`
			Threshold *int64 `json:"threshold"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		from, peer, err := origin(r.Context(), request.Caller, request.Peer)
		if err == nil && (request.Threshold == nil || !validContextPeer(peer)) {
			err = ErrInvalid
		}
		if err != nil {
			failure(w, err)
			return
		}
		change(w, r, "context-threshold", "context threshold "+peer+" "+strconv.FormatInt(*request.Threshold, 10), from,
			func(ctx context.Context) (any, error) {
				return d.store.SetContextThreshold(ctx, peer, *request.Threshold)
			})
	})
}
