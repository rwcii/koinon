package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
