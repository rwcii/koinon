package core

import (
	"context"
)

// The audit log of dashboard actions (sprint chunk 09). A record names the time, the
// action, its target, the result and a fixed reason code; it never holds a message body,
// a credential or a secret. An accepted action writes its record in the transaction that
// makes the change, so the record exists exactly when the change does.

const (
	auditRetention = 90 * 86400 * 1000 // milliseconds
	auditMax       = 10000
	auditPage      = 100
)

// AuditRecord is one audit log entry.
type AuditRecord struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	Action string `json:"action"`
	Target string `json:"target"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

type auditContextKey struct{}

// pendingAudit is the record of one action. The first write transaction that commits
// under its context writes it; written then stays true.
type pendingAudit struct {
	action, target string
	written        bool
}

// withAudit returns a context under which the next committed write records the action.
func withAudit(ctx context.Context, action, target string) (context.Context, *pendingAudit) {
	p := &pendingAudit{action: action, target: target}
	return context.WithValue(ctx, auditContextKey{}, p), p
}

// noAudit is ctx without a pending record, for the writes that record the log itself.
func noAudit(ctx context.Context) context.Context {
	return context.WithValue(context.WithoutCancel(ctx), auditContextKey{}, (*pendingAudit)(nil))
}

// writeAudit inserts the pending accepted record of the transaction's context into the
// transaction that the operation marked as its own change. It runs inside Commit, before
// the page check, so a record that does not fit refuses the whole transaction.
func (t *writeTx) writeAudit() (*pendingAudit, error) {
	p, _ := t.ctx.Value(auditContextKey{}).(*pendingAudit)
	if p == nil || p.written || !t.audited {
		return nil, nil
	}
	_, err := t.Tx.ExecContext(t.ctx, `INSERT INTO audit(at,action,target,result) VALUES (?,?,?,'accepted')`,
		t.s.now().UnixMilli(), p.action, p.target)
	return p, err
}

// auditRefusal records an action that was refused after the request checks passed. It
// is written only when storage can hold it; a refusal without a record is still a refusal.
func (s *Store) auditRefusal(ctx context.Context, p *pendingAudit, reason string) {
	if p == nil || p.written {
		return
	}
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit(at,action,target,result,reason) VALUES (?,?,?,'refused',?)`,
		s.now().UnixMilli(), p.action, p.target, reason); err != nil {
		tx.fail(err)
		return
	}
	if tx.Commit() == nil {
		p.written = true
	}
}

// auditLaunch writes the record of a launch before the launcher runs, as `started`, and
// returns its ID; finishLaunch sets the result after the launcher returns. Its fixed codes
// are short, so the update fits the reserve.
func (s *Store) auditLaunch(ctx context.Context, target string) (int64, error) {
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO audit(at,action,target,result) VALUES (?,'launch',?,'started')`,
		s.now().UnixMilli(), target)
	if err != nil {
		return 0, tx.fail(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) finishLaunch(ctx context.Context, id int64, result, reason string) error {
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE audit SET result=?,reason=? WHERE id=?`, result, reason, id); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// closeStartedLaunches marks the launch records that a previous daemon left `started`:
// their result is unknown. A failure leaves them as they are.
func (s *Store) closeStartedLaunches(ctx context.Context) error {
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE audit SET result='unknown',reason='daemon_restarted' WHERE action='launch' AND result='started'`); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// trimAudit removes audit records and participant events past the retention and beyond the
// newest auditMax of each. Removal is reclamation; it may use the reserve.
func (s *Store) trimAudit(ctx context.Context) error {
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit WHERE at<? OR id<=(SELECT id FROM audit ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		s.now().UnixMilli()-auditRetention, auditMax); err != nil {
		return tx.fail(err)
	}
	// Participant events follow the audit log's bounds.
	if _, err := tx.ExecContext(ctx, `DELETE FROM participant_events WHERE at<? OR id<=(SELECT id FROM participant_events ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		s.now().UnixMilli()-auditRetention, auditMax); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}
