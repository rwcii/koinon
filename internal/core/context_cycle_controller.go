package core

import (
	"context"
	"errors"
	"time"
)

// contextObserved runs after a context observation was stored, outside the observation
// lock. A new native epoch of a requester whose cycle has not entered any input yet is an
// unrelated clear: it cancels the cycle. Otherwise the observation may admit a cycle.
func (s *Store) contextObserved(ctx context.Context, k Key) {
	evidence := s.contextEvidence(k)
	cycles, err := cyclesWhere(ctx, s.db, activeCycle+` AND family=? AND session_id=?`, k.Family, k.ID)
	if err != nil {
		return
	}
	for _, c := range cycles {
		if evidence.Epoch != "" && evidence.Epoch != c.Epoch && (c.State == cycleWaitingReady || c.State == cycleReady) {
			s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "epoch_changed")
		}
	}
	s.admitContextCycle(ctx, k)
}

// cycleStep advances every unfinished cycle by at most one external effect: deadlines end
// it, a ready cycle clears its pane, a cleared one waits for its new epoch, a reset one
// enters pickup and a prompt is answered. Each input is preceded by its recorded intent,
// and holds wake.mu, so no pane wake interleaves with it.
func (s *Store) cycleStep(ctx context.Context) error {
	cycles, err := cyclesWhere(ctx, s.db, activeCycle+` ORDER BY created_at`)
	if err != nil {
		return err
	}
	for _, c := range cycles {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.stepCycle(ctx, c); err != nil && !errors.Is(err, ErrCycleChanged) && !errors.Is(err, ErrCapacity) {
			return err
		}
	}
	return nil
}

func (s *Store) stepCycle(ctx context.Context, c cycleRow) error {
	now := s.now().UnixMilli()
	switch {
	case now >= c.CycleDeadline:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "cycle_timeout")
		return err
	case now >= c.StageDeadline && !cycleIntent(c.State):
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, c.State+"_timeout")
		return err
	}
	adapter := cycleAdapterOf(c.Family)
	if adapter == nil {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "adapter_unavailable")
		return err
	}
	if ok, err := s.cycleStillEnabled(ctx, c); err != nil || !ok {
		if err == nil {
			_, err = s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "enable_changed")
		}
		return err
	}
	t := c.target()
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	switch c.State {
	case cycleReady, cycleResetVerified:
		pane, err := adapter.Inspect(ctx, t)
		if err != nil {
			return nil // no fresh proof yet; the stage deadline ends a pane that never proves
		}
		if pane.Prompt != "" && !cycleCode.MatchString(pane.Prompt) {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "prompt_unrecognized")
			return err
		}
		if pane.Prompt != "" {
			_, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleWaitingPrompt, pane.Prompt, func(n *cycleRow) { n.PromptFrom = c.State })
			return err
		}
		if !pane.Idle {
			return nil
		}
		if c.State == cycleReady {
			return s.enterInput(ctx, c, cycleClearIntent, cycleClearReceived, func() (bool, error) { return adapter.Clear(ctx, t) })
		}
		return s.enterInput(ctx, c, cyclePickupIntent, cycleWaitingPickup, func() (bool, error) { return adapter.Pickup(ctx, t) })
	case cycleClearIntent, cycleClearReceived:
		// An intent without its receipt is reconciled only by proof, never by sending again.
		epoch, err := adapter.NewEpoch(ctx, t)
		if err == nil && epoch != "" && epoch != c.Epoch && nativeEpoch.MatchString(epoch) {
			_, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleResetVerified, "", func(n *cycleRow) { n.SuccessorEpoch = epoch })
			return err
		}
		if c.State == cycleClearIntent || now >= c.StageDeadline {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "reset_unproven")
			return err
		}
	case cyclePickupIntent:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "pickup_unreceipted")
		return err
	case cycleWaitingPrompt:
		answered, err := adapter.Answer(ctx, t, c.Reason)
		if err != nil || !answered {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "prompt_unanswered")
			return err
		}
		_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPrompt, c.PromptFrom, "prompt_answered", func(n *cycleRow) { n.PromptFrom = "" })
		return err
	}
	return nil
}

// enterInput records the intent, makes the input and records its receipt. A failed or
// unreceived input is uncertain: its effect is unknown, and it is never sent again.
func (s *Store) enterInput(ctx context.Context, c cycleRow, intent, received string, input func() (bool, error)) error {
	c, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, intent, "", nil)
	if err != nil {
		return err
	}
	ok, err := input()
	if err != nil || !ok {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, intent+"_unreceived")
		return err
	}
	_, err = s.advanceCycle(ctx, c.ID, c.Revision, intent, received, "", nil)
	return err
}

// cycleStillEnabled checks that the session's Enable is the one that admitted the cycle.
func (s *Store) cycleStillEnabled(ctx context.Context, c cycleRow) (bool, error) {
	var enabled bool
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT enabled,revision FROM context_enables WHERE family=? AND session_id=?`, c.Family, c.SessionID).
		Scan(&enabled, &revision)
	if err != nil {
		return false, nil
	}
	return enabled && revision == c.EnableRevision, nil
}

// paneHeldByCycle reports whether an unfinished cycle owns the target session's pane, so
// its ordinary wake waits without being marked delivered.
func (s *Store) paneHeldByCycle(ctx context.Context, target Key) (bool, error) {
	var held int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycles c JOIN sessions s ON s.family=? AND s.id=?
		WHERE c.state IN ('ready','clear_intent','clear_received','reset_verified','pickup_intent','waiting_pickup','waiting_prompt')
		AND c.host_pid=s.host_pid AND c.host_start=s.host_start AND c.tmux_socket=s.tmux_socket AND c.tmux_pane=s.tmux_pane`,
		target.Family, target.ID).Scan(&held)
	return held > 0, err
}

// trimCycles removes finished cycles past the retention and beyond the newest
// maxTerminalCycles, with their events. Unfinished cycles and their credits stay.
func (s *Store) trimCycles(ctx context.Context) error {
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := s.now().Add(-terminalCycleRetention).UnixMilli()
	selected := `SELECT cycle_id FROM context_cycles WHERE finished_at>0 AND (finished_at<? OR cycle_id IN
		(SELECT cycle_id FROM context_cycles WHERE finished_at>0 ORDER BY finished_at DESC, cycle_id LIMIT -1 OFFSET ?))`
	if _, err := tx.ExecContext(ctx, `DELETE FROM context_cycle_events WHERE cycle_id IN (`+selected+`)`, cutoff, maxTerminalCycles); err != nil {
		return tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM context_cycles WHERE cycle_id IN (`+selected+`)`, cutoff, maxTerminalCycles); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// maintainCycles runs the cycle controller once a second and trims finished cycles once a
// minute, until the daemon stops.
func (d *Daemon) maintainCycles() {
	defer d.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-d.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for tick := 0; ; tick++ {
		d.store.cycleStep(ctx)
		if tick%60 == 0 {
			d.store.trimCycles(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
