package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PeerObservation deliberately excludes terminal targets and private session metadata.
// Known is false when no current value exists; Reason explains why.
type PeerObservation struct {
	Known          bool   `json:"known"`
	Reason         string `json:"reason,omitempty"`
	Source         string `json:"source,omitempty"`
	At             int64  `json:"at,omitempty"`
	ConfirmedAt    int64  `json:"confirmed_at,omitempty"`
	StaleAfterMS   int64  `json:"stale_after_ms"`
	ID             string `json:"id,omitempty"`
	State          string `json:"state,omitempty"`
	LimitTokens    *int64 `json:"limit_tokens,omitempty"`
	UsedTokens     *int64 `json:"used_tokens,omitempty"`
	UsageAvailable *bool  `json:"usage_available,omitempty"`
}

// PeerStatus carries public peer identity plus the same current observations as the dashboard.
type PeerStatus struct {
	Peer
	ObservedAt int64           `json:"observed_at"`
	Model      PeerObservation `json:"model"`
	Context    PeerObservation `json:"context"`
	Activity   PeerObservation `json:"activity"`
}

// PeerStatus resolves a published name or held alias for an active caller. Expired or
// retired peer names remain readable, with unknown observations. An unheld alias refuses.
func (s *Store) PeerStatus(ctx context.Context, caller Key, name string) (PeerStatus, error) {
	if name == "" || len(name) > 256 {
		return PeerStatus{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PeerStatus{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return PeerStatus{}, err
	}
	var kind, family, id, repository, holder string
	err = tx.QueryRowContext(ctx, `SELECT kind,family,session_id,repository,holder_id FROM names WHERE name=?`, name).
		Scan(&kind, &family, &id, &repository, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		return PeerStatus{}, ErrPeerNotFound
	}
	if err != nil {
		return PeerStatus{}, err
	}
	if kind == "alias" {
		held, err := holds(ctx, tx, now, family, holder, repository)
		if err != nil {
			return PeerStatus{}, err
		}
		if !held {
			return PeerStatus{}, ErrAliasUnheld
		}
		id = holder
	}
	session, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, family, id), now)
	if err != nil {
		return PeerStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return PeerStatus{}, err
	}
	// Use the dashboard projection and a single bounded provider-read budget.
	pull, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	views := s.sessionObservations(pull, session)
	project := func(group string) PeerObservation {
		view := views[group]
		o := PeerObservation{Known: view.Value != nil, Reason: view.Reason,
			StaleAfterMS: observationFreshness[group].Milliseconds()}
		if v := view.Value; v != nil {
			o.Source, o.At, o.ConfirmedAt = v.Source, v.At, view.ConfirmedAt
			switch group {
			case "model":
				o.ID = v.ID
			case "context":
				o.LimitTokens, o.UsedTokens, o.UsageAvailable = v.LimitTokens, v.UsedTokens, v.UsageAvailable
			case "activity":
				o.State = v.State
			}
		}
		return o
	}
	return PeerStatus{Peer: Peer{Name: session.Name, Alias: session.Alias, Family: session.Family,
		State: session.State, Repository: session.Repository}, ObservedAt: s.now().UnixMilli(),
		Model: project("model"), Context: project("context"), Activity: project("activity")}, nil
}
