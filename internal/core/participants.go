package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Participants (participants sprint, chunk 02). A participant is a family, a repository and
// an optional role; its address is an alias row of names with that role. assignNames applies
// the holder rules at registration and renewal; ChooseHolder is the maintainer's choice.

// ErrNotParticipant refuses a holder choice for a session that is not a qualifier of the
// participant: a sub-agent, a session without a repository, or one of another participant.
var ErrNotParticipant = Refusal{"not_participant", "the session does not belong to that participant"}

// Participant is one participant with its holder, its conflict and its last event.
type Participant struct {
	Address    string `json:"address"`
	Family     string `json:"family"`
	Repository string `json:"repository"`
	Role       string `json:"role,omitempty"`
	// Holder is the peer name of the active holder, or empty.
	Holder    string            `json:"holder,omitempty"`
	Conflict  []string          `json:"conflict,omitempty"`
	LastEvent *ParticipantEvent `json:"last_event,omitempty"`
}

// ParticipantEvent is one recorded change of a participant. Former and Holder are peer names.
type ParticipantEvent struct {
	At      int64  `json:"at"`
	Former  string `json:"former,omitempty"`
	Holder  string `json:"holder,omitempty"`
	Reason  string `json:"reason"`
	Actor   string `json:"actor"`
	Details string `json:"details,omitempty"`
}

// ChooseHolder is the maintainer's choice of a participant's holder: the session k, at
// revision, active, of that participant and not a sub-agent. It is a control write, so it
// commits at the ordinary storage ceiling, and it records a participant event.
func (s *Store) ChooseHolder(ctx context.Context, address string, k Key, revision int64) error {
	if address == "" || len(address) > 256 || !validKey(k.Family, k.ID) || revision <= 0 {
		return ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	session, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), now)
	if err != nil {
		return err
	}
	if session.Revision != revision || session.State != "active" {
		return ErrConflict
	}
	if session.Address != address {
		return ErrNotParticipant
	}
	var holder string
	if err := tx.QueryRowContext(ctx, `SELECT holder_id FROM names WHERE name=? AND kind='alias'`, address).Scan(&holder); err != nil {
		return err
	}
	// Only the maintainer's choice lifts a fence.
	if _, err := tx.ExecContext(ctx, `DELETE FROM participant_fences WHERE address=? AND family=? AND session_id=?`, address, k.Family, k.ID); err != nil {
		return tx.fail(err)
	}
	if holder != k.ID {
		if err := setHolder(ctx, tx.Tx, now, address, holder, k.ID, "maintainer_choice", "maintainer"); err != nil {
			return tx.fail(err)
		}
	}
	return tx.Commit()
}

// Participants lists the participants with their active holder, conflict and last event,
// at most 1000; truncated says that more exist.
func (s *Store) Participants(ctx context.Context) ([]Participant, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	rows, err := tx.QueryContext(ctx, `SELECT name,family,repository,role,holder_id,conflict FROM names
		WHERE kind='alias' ORDER BY family,repository,role LIMIT 1001`)
	if err != nil {
		return nil, false, err
	}
	type row struct {
		p        Participant
		holderID string
		conflict string
	}
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.p.Address, &r.p.Family, &r.p.Repository, &r.p.Role, &r.holderID, &r.conflict); err != nil {
			rows.Close()
			return nil, false, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(list) > 1000
	if truncated {
		list = list[:1000]
	}
	result := make([]Participant, 0, len(list))
	for _, r := range list {
		p := r.p
		if r.holderID != "" {
			held, err := holds(ctx, tx, now, p.Family, r.holderID, p.Repository, p.Role)
			if err != nil {
				return nil, false, err
			}
			if held {
				if p.Holder, err = peerOf(ctx, tx, p.Family, r.holderID); err != nil {
					return nil, false, err
				}
			}
		}
		if r.conflict != "" {
			if err := json.Unmarshal([]byte(r.conflict), &p.Conflict); err != nil {
				return nil, false, err
			}
		}
		var e ParticipantEvent
		err := tx.QueryRowContext(ctx, `SELECT at,former,holder,reason,actor,details FROM participant_events
			WHERE address=? ORDER BY id DESC LIMIT 1`, p.Address).Scan(&e.At, &e.Former, &e.Holder, &e.Reason, &e.Actor, &e.Details)
		switch {
		case err == nil:
			p.LastEvent = &e
		case !errors.Is(err, sql.ErrNoRows):
			return nil, false, err
		}
		result = append(result, p)
	}
	return result, truncated, nil
}

// Participant state and fencing (participants sprint, chunk 03). The participant's consumer
// key and its inbox's internal session ID are participant:<address>.
const participantPrefix = "participant:"

func participantKey(address string) string { return participantPrefix + address }

func isParticipantID(id string) bool { return strings.HasPrefix(id, participantPrefix) }

// notParticipantRow is a condition on sessions s that leaves out the participants' internal
// inbox rows, for every query that lists or counts agent sessions.
const notParticipantRow = `substr(s.id,1,12)!='participant:'`

// heldAddress returns the address that the caller holds now, or "": an address recorded
// for it whose participant matches its current repository and role while it is active and
// not a sub-agent. A session that moved can still be recorded on an earlier participant.
func heldAddress(ctx context.Context, tx *sql.Tx, now int64, caller Key) (string, error) {
	var address string
	err := tx.QueryRowContext(ctx, `SELECT n.name FROM names n JOIN sessions s ON s.family=n.family AND s.id=n.holder_id
		AND s.repository=n.repository AND s.role=n.role AND s.subagent=0 AND s.retired_at=0 AND s.expires_at>?
		WHERE n.kind='alias' AND n.family=? AND n.holder_id=? ORDER BY n.name LIMIT 1`, now, caller.Family, caller.ID).Scan(&address)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return address, err
}

// requireHolder refuses, with ErrStaleHolder, a call that acts for the participant at
// address unless the caller holds it now. It runs in the call's own transaction, so a
// change of holder that commits first is seen.
func requireHolder(ctx context.Context, tx *sql.Tx, now int64, caller Key, address string) error {
	held, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return err
	}
	if held == "" || held != address {
		return ErrStaleHolder
	}
	return nil
}

// ensureParticipantInbox creates the participant's internal inbox row when it is missing.
// The row never expires, so the participant's messages and acknowledgements outlast every
// holder.
func ensureParticipantInbox(ctx context.Context, tx *sql.Tx, now int64, address string) error {
	var family, repository, role string
	if err := tx.QueryRowContext(ctx, `SELECT family,repository,role FROM names WHERE name=? AND kind='alias'`, address).
		Scan(&family, &repository, &role); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO sessions(family,id,repository,directory,wake_target,registered_at,renewed_at,
		expires_at,retired_at,revision,subagent,role) VALUES (?,?,?,'','{}',?,?,9007199254740991,0,1,0,?)
		ON CONFLICT(family,id) DO NOTHING`, family, participantKey(address), repository, now, now, role)
	return err
}

// fence records that a former holder may not act for, or take, the participant at address
// until the maintainer chooses it.
func fence(ctx context.Context, tx *sql.Tx, now int64, address, family, id, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO participant_fences(address,family,session_id,at,reason) VALUES (?,?,?,?,?)
		ON CONFLICT(address,family,session_id) DO UPDATE SET at=excluded.at,reason=excluded.reason`, address, family, id, now, reason)
	return err
}
