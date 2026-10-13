package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
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
		// endCycleTx makes an unproven input uncertain.
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "cycle_timeout")
		return err
	case now >= c.StageDeadline && !cycleInputUnproven(c.State):
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, c.State+"_timeout")
		return err
	}
	adapter := cycleAdapterOf(c.Family)
	if adapter == nil {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "adapter_unavailable")
		return err
	}
	if ok, err := cycleEnabled(ctx, s.db, c); err != nil || !ok {
		if err == nil {
			_, err = s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "enable_changed")
		}
		return err
	}
	t := s.cycleTarget(ctx, c)
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	switch c.State {
	case cycleReady, cycleBootstrapVerified, cycleResetVerified:
		pane, err := adapter.Inspect(ctx, t, s.cycleFence(c))
		if err != nil {
			return nil // no fresh proof yet; the stage deadline ends a pane that never proves
		}
		if pane.Prompt != "" && !cycleCode.MatchString(pane.Prompt) {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "prompt_unrecognized")
			return err
		}
		if pane.Prompt != "" {
			_, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleWaitingPrompt, pane.Prompt, nil, func(n *cycleRow) { n.PromptFrom = c.State })
			return err
		}
		if !pane.Idle {
			return nil
		}
		// The saved handoff must still be the reported file before any input. A changed
		// file before the clear needs a new ready report; after it, the cycle fails.
		if _, err := s.verifyArtifact(ctx, c, c.Artifact); err != nil {
			if c.State != cycleResetVerified {
				_, err = s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleWaitingReady, "artifact_changed", nil, func(n *cycleRow) {
					n.Artifact, n.ArtifactDev, n.ArtifactIno, n.ArtifactSize, n.ArtifactMtime = "", 0, 0, 0, 0
				})
			} else {
				_, err = s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "artifact_changed")
			}
			return err
		}
		// The pickup entry that a bare pickup runs is checked before each input.
		probe := adapter.Probe(ctx, t)
		if probe.Reason != "" {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, probe.Reason)
			return err
		}
		switch {
		case c.State == cycleReady && probe.Bootstrap == "interactive":
			return s.enterInput(ctx, c, cycleBootstrapIntent, cycleBootstrapVerified, pinnedRequester(ctx),
				func(fence CycleFence) (bool, error) { return adapter.Bootstrap(ctx, t, fence) })
		case c.State == cycleReady || c.State == cycleBootstrapVerified:
			return s.enterInput(ctx, c, cycleClearIntent, cycleClearReceived, pinnedRequester(ctx),
				func(fence CycleFence) (bool, error) { return adapter.Clear(ctx, t, fence) })
		}
		return s.enterInput(ctx, c, cyclePickupIntent, cycleWaitingPickup, nil,
			func(fence CycleFence) (bool, error) { return adapter.Pickup(ctx, t, probe.Bootstrap, fence) })
	case cycleClearIntent, cycleClearReceived:
		// An intent without its receipt is reconciled only by proof, never by sending again.
		epoch, err := adapter.NewEpoch(ctx, t)
		if err == nil && epoch != "" && epoch != c.Epoch && nativeEpoch.MatchString(epoch) {
			_, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleResetVerified, "", nil, func(n *cycleRow) { n.SuccessorEpoch = epoch })
			return err
		}
		if c.State == cycleClearIntent || now >= c.StageDeadline {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "reset_unproven")
			return err
		}
	case cycleBootstrapIntent:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "bootstrap_unreceipted")
		return err
	case cyclePickupIntent:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "pickup_unreceipted")
		return err
	case cyclePromptIntent:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "prompt_unreceipted")
		return err
	case cycleWaitingPrompt:
		if !adapter.Answerable(c.Reason) {
			// The native session's own settings, or the maintainer, answer this dialog; the
			// cycle returns to its stage once the dialog is gone, or the prompt wait ends it.
			pane, err := adapter.Inspect(ctx, t, s.cycleFence(c))
			if err != nil || pane.Prompt != "" {
				return nil
			}
			_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPrompt, c.PromptFrom, "prompt_resolved", nil, func(n *cycleRow) { n.PromptFrom = "" })
			return err
		}
		// The response is an input like any other: its intent is recorded first, and it is
		// never entered again without proof.
		c, err := s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPrompt, cyclePromptIntent, c.Reason, nil, nil)
		if err != nil {
			return err
		}
		answered, err := adapter.Answer(ctx, t, c.Reason, s.cycleFence(c))
		if err != nil || !answered {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "prompt_unanswered")
			return err
		}
		_, err = s.advanceCycle(ctx, c.ID, c.Revision, cyclePromptIntent, c.PromptFrom, "prompt_answered", nil, func(n *cycleRow) { n.PromptFrom = "" })
		return err
	}
	return nil
}

// enterInput records the intent, makes the input and records its receipt. guard adds the
// caller's checks to the intent's transaction; the input's fence holds the cycle to that
// intent. A failed or unreceived input is uncertain: its effect is unknown, and it is never
// sent again.
func (s *Store) enterInput(ctx context.Context, c cycleRow, intent, received string, guard func(*writeTx, cycleRow, int64) error,
	input func(CycleFence) (bool, error)) error {
	c, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, intent, "", guard, nil)
	if err != nil {
		return err
	}
	ok, err := input(s.cycleFence(c))
	if err != nil || !ok {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, inputReason(intent, err))
		return err
	}
	_, err = s.advanceCycle(ctx, c.ID, c.Revision, intent, received, "", nil, nil)
	return err
}

// cycleFence holds an input to the cycle as c records it: in one read transaction, the cycle
// is still at c's revision and stage, before its deadlines, under the admitting Enable.
func (s *Store) cycleFence(c cycleRow) CycleFence {
	return func(ctx context.Context) error {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		current, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, c.ID))
		if err != nil {
			return err
		}
		now := s.now().UnixMilli()
		if current.Revision != c.Revision || current.State != c.State || now >= current.StageDeadline || now >= current.CycleDeadline {
			return ErrCycleChanged
		}
		if ok, err := cycleEnabled(ctx, tx, current); err != nil || !ok {
			if err == nil {
				err = ErrCycleChanged
			}
			return err
		}
		return nil
	}
}

// cycleTarget is c's target with the session's checkout, the loader and the catalog pickup
// item, and the store's host tools.
func (s *Store) cycleTarget(ctx context.Context, c cycleRow) CycleTarget {
	t := c.target()
	t.tools = s.hostTools()
	t.LoaderDir = CycleLoaderDir(filepath.Dir(s.storage.path))
	t.Loader = claudeLoaderEntry()
	if read, err := s.library.catalog.Read(c.Family, "skill.pickup"); err == nil {
		t.Pickup = PickupEntry{Content: []byte(read.Item.Content), Description: read.Item.Description}
	}
	if s.db.QueryRowContext(ctx, `SELECT directory FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).Scan(&t.Directory) == nil {
		if checkout, err := CheckoutResource(ctx, t.Directory); err == nil {
			t.Checkout = checkout.Directory
		}
	}
	return t
}

// paneHeldByCycle reports whether an unfinished cycle owns the target session's pane, so
// its ordinary wake waits without being marked delivered.
func (s *Store) paneHeldByCycle(ctx context.Context, target Key) (bool, error) {
	var held int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycles c JOIN sessions s ON s.family=? AND s.id=?
		WHERE c.state IN ('ready','bootstrap_intent','bootstrap_verified','clear_intent','clear_received','reset_verified','pickup_intent',
			'waiting_pickup','waiting_prompt','prompt_intent')
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

// cycleActionKey marks a dashboard action whose audit record joins its cycle transition.
type cycleActionKey struct{}

// CycleDialogOf reads the dialog that a waiting cycle's pane shows, for the dashboard. It
// sends nothing.
func (s *Store) CycleDialogOf(ctx context.Context, id string) (CycleDialog, error) {
	c, err := scanCycle(s.db.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil || c.State != cycleWaitingPrompt {
		return CycleDialog{}, ErrCycleChanged
	}
	adapter := cycleAdapterOf(c.Family)
	if adapter == nil || adapter.Answerable(c.Reason) {
		return CycleDialog{}, ErrCycleChanged
	}
	return adapter.Dialog(ctx, s.cycleTarget(ctx, c))
}

// AnswerCycleDialog applies the maintainer's option to a dialog that the adapter does not
// answer itself, only while the cycle is at revision and its pane shows the same dialog
// (digest). Its intent is recorded first, with the action's audit record; an unproven answer
// is uncertain and is never entered again.
func (s *Store) AnswerCycleDialog(ctx context.Context, id string, revision int64, digest, option string) error {
	c, err := scanCycle(s.db.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCycleNotFound
	}
	if err != nil {
		return err
	}
	adapter := cycleAdapterOf(c.Family)
	if c.State != cycleWaitingPrompt || c.Revision != revision || adapter == nil || adapter.Answerable(c.Reason) {
		return ErrCycleChanged
	}
	t := s.cycleTarget(ctx, c)
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if dialog, err := adapter.Dialog(ctx, t); err != nil || dialog.Digest != digest {
		return ErrCycleDialogChanged
	}
	c, err = s.advanceCycle(context.WithValue(ctx, cycleActionKey{}, true), id, revision, cycleWaitingPrompt, cyclePromptIntent, "maintainer_option", nil, nil)
	if err != nil {
		return err
	}
	answered, err := adapter.Choose(ctx, t, digest, option, s.cycleFence(c))
	if errors.Is(err, ErrCycleDialogChanged) {
		// Choose found another dialog before any key: nothing was entered, so the cycle
		// waits again for a response to the dialog that shows now.
		if _, err := s.advanceCycle(ctx, id, c.Revision, cyclePromptIntent, cycleWaitingPrompt, "native_dialog", nil, nil); err != nil {
			return err
		}
		return ErrCycleDialogChanged
	}
	if err != nil || !answered {
		_, endErr := s.endCycle(ctx, id, c.Revision, cycleUncertain, "prompt_unanswered")
		if err == nil {
			err = endErr
		}
		return err
	}
	_, err = s.advanceCycle(ctx, id, c.Revision, cyclePromptIntent, c.PromptFrom, "prompt_answered", nil, func(n *cycleRow) { n.PromptFrom = "" })
	return err
}

// inputReason is the fixed reason of an input that did not complete: the adapter's step code
// when it names one, else the intent left unreceived.
func inputReason(intent string, err error) string {
	var refusal Refusal
	switch {
	case errors.As(err, &refusal):
		return refusal.Code
	case err != nil && cycleCode.MatchString(err.Error()):
		return err.Error()
	}
	return intent + "_unreceived"
}
