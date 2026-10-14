package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// CheckoutWriter identifies one generation of the existing advisory work lease.
// The token is observable coordination data, not a secret or a permission grant.
type CheckoutWriter struct {
	WorkID     string  `json:"work_id"`
	Generation int64   `json:"generation"`
	Consumer   string  `json:"consumer"`
	Peer       string  `json:"peer,omitempty"`
	Revision   int64   `json:"revision"`
	ExpiresAt  float64 `json:"expires_at"`
	LeaseValid bool    `json:"lease_valid"`
	Checkpoint string  `json:"checkpoint"`
}

type CheckoutStatus struct {
	Checkout
	ObservedAt float64         `json:"observed_at"`
	State      string          `json:"state"`
	Writer     *CheckoutWriter `json:"writer"`
}

func checkCheckoutKey(key string) error {
	const prefix = "checkout:v1:"
	if !strings.HasPrefix(key, prefix) || len(key) != len(prefix)+64 || strings.Trim(key[len(prefix):], "0123456789abcdef") != "" {
		return invalid("checkout_resource must be a derived checkout:v1 resource")
	}
	return nil
}

// CheckoutStatus reads the exact checkout role in one database snapshot. It does not
// create a memory store, reconcile expiry, claim or renew. A retained expired/released
// writer remains visible for checkpoint reconciliation, with lease_valid false.
func (s *Store) CheckoutStatus(ctx context.Context, caller Key, directory string) (CheckoutStatus, error) {
	m, err := s.ResolveWorkCaller(ctx, caller, nil)
	if err != nil {
		return CheckoutStatus{}, err
	}
	checkout, err := CheckoutResource(ctx, directory)
	if err != nil || checkout.Repository != m.Repository {
		return CheckoutStatus{}, workError("repo_unresolved", "checkout must belong to the caller's repository")
	}
	result := CheckoutStatus{Checkout: checkout, ObservedAt: s.clock(), State: "unclaimed"}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	store, err := storeOf(ctx, tx, m.Repository)
	if err != nil || store == "" {
		return result, err
	}
	var b heldBundle
	b, err = scanBundle(tx.QueryRowContext(ctx, `SELECT `+bundleColumns+` FROM claim_bundles WHERE store=? AND generation IN
		(SELECT generation FROM claim_resources WHERE store=? AND kind='exact' AND resource=?) AND work_id IN
		(SELECT work_id FROM work_items WHERE store=? AND (expires_at IS NULL OR expires_at>?))
		ORDER BY CASE WHEN active=1 AND expires_at>? THEN 1 ELSE 0 END DESC,generation DESC LIMIT 1`,
		store, store, checkout.Resource[1], store, result.ObservedAt, result.ObservedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	w, err := current(ctx, tx, store, b.WorkID, result.ObservedAt)
	if err != nil {
		return result, err
	}
	writer := &CheckoutWriter{WorkID: b.WorkID, Generation: b.Generation, Consumer: b.Consumer,
		Revision: w.Revision, ExpiresAt: b.ExpiresAt, LeaseValid: b.Active && b.ExpiresAt > result.ObservedAt, Checkpoint: w.Checkpoint}
	// Participant claims follow the active holder; other custom consumers are never guessed.
	writer.Peer, _, err = checkoutWriterPeer(ctx, tx, m.Repository, b.Consumer, s.now().UnixMilli())
	if err != nil {
		return result, err
	}
	result.Writer = writer
	switch {
	case writer.LeaseValid:
		result.State = "held"
	case !b.Active && !w.LeaseExpired:
		result.State = "released"
	default:
		result.State = "expired"
	}
	return result, nil
}

func checkoutWriterPeer(ctx context.Context, tx *sql.Tx, repository, consumer string, now int64) (string, Key, error) {
	var peer string
	var k Key
	var err error
	if isParticipantID(consumer) {
		err = tx.QueryRowContext(ctx, `SELECT n.name,s.family,s.id FROM names p JOIN sessions s
			ON s.family=p.family AND s.id=p.holder_id AND s.repository=p.repository AND s.role=p.role
			JOIN names n ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id
			WHERE p.kind='alias' AND p.name=? AND p.repository=? AND s.subagent=0 AND s.retired_at=0 AND s.expires_at>?`,
			strings.TrimPrefix(consumer, participantPrefix), repository, now).Scan(&peer, &k.Family, &k.ID)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT name,family,session_id FROM names WHERE kind='peer' AND repository=? AND family||':'||session_id=?`,
			repository, consumer).Scan(&peer, &k.Family, &k.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", Key{}, nil
	}
	return peer, k, err
}

// RequestCheckout sends an ordinary inert inbox message to the currently observed
// native writer. It changes no lease. The notification names the observed generation;
// the recipient must verify it and decide whether to hand back the role.
func (s *Store) RequestCheckout(ctx context.Context, caller Key, directory, note string) (map[string]any, error) {
	if len(note) > 1024 || note != "" && checkText(note, "note", 1024) != nil {
		return nil, invalid("note must be at most 1024 bytes of UTF-8 text")
	}
	s.memory.op.Lock()
	defer s.memory.op.Unlock()
	status, err := s.CheckoutStatus(ctx, caller, directory)
	if err != nil {
		return nil, err
	}
	if status.Writer == nil || !status.Writer.LeaseValid {
		return nil, workError("checkout_unheld", "no live writer role to request; reconcile the checkpoint before explicitly starting work")
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	store, err := storeOf(ctx, tx, status.Repository)
	if err != nil {
		return nil, err
	}
	bundle, err := bundleFor(ctx, tx, store, status.Writer.WorkID)
	if err != nil {
		return nil, err
	}
	if bundle == nil || !bundle.Active || bundle.ExpiresAt <= s.clock() || bundle.Generation != status.Writer.Generation || bundle.Consumer != status.Writer.Consumer {
		return nil, workError("checkout_unheld", "the observed writer changed; read checkout status again")
	}
	peer, owner, err := checkoutWriterPeer(ctx, tx.Tx, status.Repository, bundle.Consumer, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if peer == "" {
		return nil, workError("writer_unaddressable", "the writer has no addressable active holder; coordinate with its owner explicitly")
	}
	if owner == caller {
		return nil, invalid("the caller already holds this checkout role")
	}
	status.Writer.Peer = peer
	body, _ := json.Marshal(map[string]any{"type": "checkout_writer_request", "resource": status.Resource,
		"work_id": status.Writer.WorkID, "claim_generation": status.Writer.Generation, "note": note,
		"meaning": "Request only. Verify current checkout status before deciding whether to hand back the writer role."})
	message, err := s.sendTx(ctx, tx, caller, peer, string(body), false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"checkout": status, "message": message}, tx.Commit()
}

// notifyCheckoutHandoff joins the work-release transaction. Both the checkpoint/release
// and an exact same-repository recipient's message commit, or neither does. Aliases are
// refused because they can move between a request and its handback.
func (s *Store) notifyCheckoutHandoff(ctx context.Context, tx *writeTx, m MemoryCaller, r *workRequest) (Outcome, error) {
	var caller Key
	err := tx.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=? AND family=? AND repository=?`,
		m.Name, m.Family, m.Repository).Scan(&caller.Family, &caller.ID)
	if err != nil {
		return Outcome{}, ErrCallerInactive
	}
	var recipient Key
	err = tx.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=? AND repository=?`,
		r.text["handoff_to"], m.Repository).Scan(&recipient.Family, &recipient.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, workError("invalid_recipient", "handoff_to must name an exact peer in the same repository")
	}
	if err != nil {
		return Outcome{}, err
	}
	if recipient == caller {
		return Outcome{}, invalid("handoff_to must name another session")
	}
	body, _ := json.Marshal(map[string]any{"type": "checkout_writer_handoff", "resource": [2]string{"exact", r.text["checkout_resource"]},
		"work_id": r.text["work_id"], "claim_generation": r.ints["claim_generation"], "checkpoint": r.text["checkpoint"],
		"meaning": "Writer role released. Read the checkpoint and current checkout status, then explicitly work_start with this resource. Only a successful start grants the advisory role; this notification grants no permissions."})
	return s.sendTx(ctx, tx, caller, r.text["handoff_to"], string(body), false)
}
