package core

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// A cycle that times out while it waits for the ready report, before Koinon entered any input
// in the pane, keeps the session's Enable once in each context epoch and Enable revision. The
// next cycle waits until the session reads its own inbox after the timeout
// (DECISIONS.md#READY-TIMEOUT-ENABLE-487). Both facts are cycle events, so they hold across a
// daemon restart.
const (
	cycleEnableKept = "enable_kept" // the ready timeout kept the Enable
	cycleInboxRead  = "inbox_read"  // the session's first inbox read after it
)

// readyTimeoutKeepsEnable reports a cycle that ends failed at its ready deadline
// (waiting_ready_timeout), with no input event at any stage, when no earlier cycle of the
// session kept its Enable under the same epoch and Enable revision. A new Enable has a new
// revision, so it gives one more retry (#584). Every other end, the lifetime deadline
// (cycle_timeout) included, keeps its rule.
func readyTimeoutKeepsEnable(ctx context.Context, q querier, c cycleRow, outcome, reason string) (bool, error) {
	if outcome != cycleFailed || c.State != cycleWaitingReady || !strings.HasPrefix(reason, cycleWaitingReady+"_timeout") {
		return false, nil
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycle_events WHERE cycle_id=? AND stage IN (?,?,?,?,?)`,
		c.ID, cycleBootstrapIntent, cycleClearIntent, cyclePickupIntent, cyclePromptIntent, cycleCompactIntent).Scan(&n); err != nil || n > 0 {
		return false, err
	}
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycles c JOIN context_cycle_events e USING (cycle_id)
		WHERE c.family=? AND c.session_id=? AND c.cycle_id!=? AND c.epoch=? AND c.enable_revision=? AND e.stage=?`,
		c.Family, c.SessionID, c.ID, c.Epoch, c.EnableRevision, cycleEnableKept).Scan(&n)
	return err == nil && n == 0, err
}

// keptTimeout returns the session's newest cycle when it kept the Enable at enableRevision
// under epoch ("" for any epoch) and the session has not read its inbox since, or "".
func keptTimeout(ctx context.Context, q querier, k Key, enableRevision int64, epoch string) (string, error) {
	var id, at string
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT cycle_id,epoch,enable_revision FROM context_cycles WHERE family=? AND session_id=?
		ORDER BY created_at DESC, cycle_id DESC LIMIT 1`, k.Family, k.ID).Scan(&id, &at, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil || revision != enableRevision || epoch != "" && at != epoch {
		return "", err
	}
	var kept, read int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE stage=?), COUNT(*) FILTER (WHERE stage=?)
		FROM context_cycle_events WHERE cycle_id=?`, cycleEnableKept, cycleInboxRead, id).Scan(&kept, &read); err != nil {
		return "", err
	}
	if kept == 0 || read > 0 {
		return "", nil
	}
	return id, nil
}

// noteCycleInboxRead records the session's first inbox read after a kept timeout and arms
// its crossing again, so the next fresh reading at its threshold admits a cycle. Like
// markRead, it is progress: when it cannot be written, the read is still returned.
func (s *Store) noteCycleInboxRead(ctx context.Context, caller Key) {
	enable, err := s.contextEnableNow(ctx, caller)
	if err != nil || !enable.Enabled {
		return
	}
	// The read counts under any epoch: after a restart the daemon has no reading yet, and a
	// read for an epoch that has passed changes nothing.
	if id, err := keptTimeout(ctx, s.db, caller, enable.Revision, ""); err != nil || id == "" {
		return
	}
	tx, err := s.begin(noAudit(ctx), control)
	if err != nil {
		return
	}
	defer tx.Rollback()
	id, err := keptTimeout(ctx, tx, caller, enable.Revision, "")
	if err != nil || id == "" {
		return
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO context_cycle_events(cycle_id,ordinal,at,stage,result,reason)
		SELECT cycle_id,events+1,?,?,?,'' FROM context_cycles WHERE cycle_id=?`, s.now().UnixMilli(), cycleInboxRead, cycleInboxRead, id); err != nil {
		tx.fail(err)
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE context_cycles SET events=events+1 WHERE cycle_id=?`, id); err != nil {
		tx.fail(err)
		return
	}
	tx.onCommit(func() { s.rearm(caller) })
	tx.Commit()
}

// contextEnableNow reads a session's stored Enable outside a transaction.
func (s *Store) contextEnableNow(ctx context.Context, k Key) (ContextEnable, error) {
	var e ContextEnable
	err := s.db.QueryRowContext(ctx, `SELECT enabled,revision,updated_at,threshold FROM context_enables WHERE family=? AND session_id=?`, k.Family, k.ID).
		Scan(&e.Enabled, &e.Revision, &e.UpdatedAt, &e.Threshold)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextEnable{}, nil
	}
	return e, err
}
