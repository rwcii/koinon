package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// A cycle that times out at its ready deadline, before Koinon entered any input in the pane,
// keeps the session's Enable once in each context epoch and Enable revision. The next cycle
// waits until the session reads its own inbox after the timeout
// (DECISIONS.md#READY-TIMEOUT-ENABLE-487). The retry and the read are kept in the session's
// Enable record (schema 37), not in the cycle history that retention trims.

// cycleEnableKept is the event that marks, in the cycle's history, a ready timeout that kept
// the Enable.
const cycleEnableKept = "enable_kept"

// readyTimeoutKeepsEnable reports a cycle that ends failed at its ready deadline
// (waiting_ready_timeout), with no input event at any stage, when the session's Enable has not
// used its retry under the cycle's epoch and Enable revision. A new Enable has a new revision,
// so it gives one more retry (#584). Every other end, the lifetime deadline (cycle_timeout)
// included, keeps its rule.
func readyTimeoutKeepsEnable(ctx context.Context, q querier, c cycleRow, outcome, reason string) (bool, error) {
	if outcome != cycleFailed || c.State != cycleWaitingReady || !strings.HasPrefix(reason, cycleWaitingReady+"_timeout") {
		return false, nil
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycle_events WHERE cycle_id=? AND stage IN (?,?,?,?,?)`,
		c.ID, cycleBootstrapIntent, cycleClearIntent, cyclePickupIntent, cyclePromptIntent, cycleCompactIntent).Scan(&n); err != nil || n > 0 {
		return false, err
	}
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_enables WHERE family=? AND session_id=? AND enabled=1 AND revision=?
		AND NOT (kept_revision=revision AND kept_epoch=?)`, c.Family, c.SessionID, c.EnableRevision, c.Epoch).Scan(&n)
	return err == nil && n == 1, err
}

// keepEnableTx records in the session's Enable that a ready timeout used its retry, with no
// read since.
func keepEnableTx(ctx context.Context, tx *writeTx, c cycleRow) error {
	_, err := tx.ExecContext(ctx, `UPDATE context_enables SET kept_epoch=?,kept_revision=revision,kept_read=0
		WHERE family=? AND session_id=? AND revision=?`, c.Epoch, c.Family, c.SessionID, c.EnableRevision)
	return err
}

// awaitingRead reports a session whose Enable at enableRevision kept a ready timeout under
// epoch ("" for any epoch), with no inbox read by the session since.
func awaitingRead(ctx context.Context, q querier, k Key, enableRevision int64, epoch string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_enables WHERE family=? AND session_id=? AND enabled=1 AND revision=?
		AND kept_revision=revision AND kept_read=0 AND (?='' OR kept_epoch=?)`, k.Family, k.ID, enableRevision, epoch, epoch).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n == 1, err
}

// noteCycleInboxRead records the session's first inbox read after a kept timeout and arms
// its crossing again, so the next fresh reading at its threshold admits a cycle. The read
// counts under any epoch: after a restart the daemon has no reading yet. Like markRead, it is
// progress: when it cannot be written, the read is still returned.
func (s *Store) noteCycleInboxRead(ctx context.Context, caller Key) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_enables WHERE family=? AND session_id=? AND enabled=1
		AND kept_revision=revision AND kept_read=0`, caller.Family, caller.ID).Scan(&n); err != nil || n == 0 {
		return
	}
	tx, err := s.begin(noAudit(ctx), control)
	if err != nil {
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE context_enables SET kept_read=1 WHERE family=? AND session_id=? AND enabled=1
		AND kept_revision=revision AND kept_read=0`, caller.Family, caller.ID)
	if err != nil {
		tx.fail(err)
		return
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 0 {
		return
	}
	tx.onCommit(func() { s.rearm(caller) })
	tx.Commit()
}
