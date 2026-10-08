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
	// A custom consumer is not necessarily a native session. Never guess its recipient.
	err = tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND repository=? AND family||':'||session_id=?`,
		m.Repository, b.Consumer).Scan(&writer.Peer)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
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
	if status.Writer.Peer == "" {
		return nil, workError("writer_unaddressable", "the writer uses a custom consumer; coordinate with its owner explicitly")
	}
	if status.Writer.Consumer == caller.Family+":"+caller.ID {
		return nil, invalid("the caller already holds this checkout role")
	}
	body, _ := json.Marshal(map[string]any{"type": "checkout_writer_request", "resource": status.Resource,
		"work_id": status.Writer.WorkID, "claim_generation": status.Writer.Generation, "note": note,
		"meaning": "Request only. Verify current checkout status before deciding whether to hand back the writer role."})
	message, err := s.Send(ctx, caller, status.Writer.Peer, string(body))
	if err != nil {
		return nil, err
	}
	return map[string]any{"checkout": status, "message": message}, nil
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
