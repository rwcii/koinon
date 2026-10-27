package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// paneSeen is what each unfinished cycle's last pane inspection saw, in memory. A stage
// timeout names it, so ready_timeout says why the input never came (#417).
type paneSeen struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *paneSeen) set(id, seen string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]string{}
	}
	p.m[id] = seen
}

// get returns what the cycle's last inspection saw, and whether one ran since the daemon
// started.
func (p *paneSeen) get(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen, ok := p.m[id]
	return seen, ok
}

// forget drops what the cycle's last inspection saw.
func (p *paneSeen) forget(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, id)
}

// keep forgets every cycle that is not in active.
func (p *paneSeen) keep(active []cycleRow) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := map[string]bool{}
	for _, c := range active {
		ids[c.ID] = true
	}
	for id := range p.m {
		if !ids[id] {
			delete(p.m, id)
		}
	}
}

// errPaneInMode refuses the key that returns a pane from copy mode, in an inspection that
// only reads.
var errPaneInMode = errors.New("pane_in_mode")

// inspectSeen is the fixed code of an inspection that failed: a tmux that could not run
// (#418), an unproven pane, a pane in copy mode during a read-only inspection, else a pane or
// registry that could not be read.
func inspectSeen(err error) string {
	switch {
	case errors.Is(err, errTmuxUnavailable):
		return errTmuxUnavailable.Error()
	case errors.Is(err, errClaudeUnproven) || errors.Is(err, errCodexUnproven):
		return "pane_unproven"
	case errors.Is(err, errPaneInMode):
		return "pane_in_mode"
	}
	return "pane_unreadable"
}

// proofError is an adapter's error for a pane proof that failed with err: err itself when
// tmux could not run, else the adapter's unproven error.
func proofError(err, unproven error) error {
	if errors.Is(err, errTmuxUnavailable) {
		return err
	}
	return unproven
}

// timeoutSeen is what a stage that waited for the pane saw last. After a restart no
// inspection has run, so it reads the pane once, sending no key.
func (s *Store) timeoutSeen(ctx context.Context, c cycleRow) string {
	if seen, ok := s.paneSeen.get(c.ID); ok {
		return seen
	}
	adapter := cycleAdapterOf(c.Family)
	if adapter == nil {
		return ""
	}
	t := s.cycleTarget(ctx, c)
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	pane, err := adapter.Inspect(ctx, t, func(context.Context) error { return errPaneInMode })
	switch {
	case err != nil:
		return inspectSeen(err)
	case pane.Prompt != "" && cycleCode.MatchString(pane.Prompt):
		return pane.Prompt
	case pane.Idle:
		return ""
	case pane.Seen != "" && cycleCode.MatchString(pane.Seen):
		return pane.Seen
	}
	return "pane_not_ready"
}

// contextObserved runs after a context observation was stored, outside the observation
// lock. A new native epoch of a requester whose cycle has not entered its managed clear yet is
// an unrelated clear: it cancels the cycle. Otherwise the observation may admit a cycle.
func (s *Store) contextObserved(ctx context.Context, k Key) {
	evidence := s.contextEvidence(k)
	cycles, err := cyclesWhere(ctx, s.db, activeCycle+` AND family=? AND session_id=?`, k.Family, k.ID)
	if err != nil {
		return
	}
	for _, c := range cycles {
		// Before the managed clear, a new native epoch is an unrelated clear; a bootstrap whose
		// input is not yet proven ends uncertain by the shared rule.
		if evidence.Epoch != "" && evidence.Epoch != c.Epoch && (c.State == cycleWaitingReady || c.State == cycleReady ||
			c.State == cycleBootstrapIntent || c.State == cycleBootstrapVerified) {
			s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "epoch_changed")
		}
		if c.Mode == ContextNativeCompaction {
			s.compactionObserved(ctx, c, evidence)
		}
	}
	s.admitContextCycle(ctx, k)
}

// compactionObserved applies a context reading to a compaction (#509). Another epoch ends it
// as epoch_changed, and the new epoch can cross again. Before its deadlines, a fresh reading
// (the trigger's source and confirmation ages, neither in the future) of the same epoch that
// its source recorded after the submission's receipt, below the threshold that the compaction
// recorded, confirms it.
func (s *Store) compactionObserved(ctx context.Context, c cycleRow, e ContextEvidence) {
	if e.Epoch != "" && e.Epoch != c.Epoch {
		s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "epoch_changed")
		return
	}
	now := s.now().UnixMilli()
	if c.State != cycleCompactSent || now >= c.StageDeadline || now >= c.CycleDeadline || !freshEvidence(e, now) {
		return
	}
	sent, err := compactSentAt(ctx, s.db, c)
	if err != nil || e.SourceAt <= sent || *e.UsedTokens*100 >= c.Threshold**e.LimitTokens {
		return
	}
	s.endCycle(ctx, c.ID, c.Revision, cycleComplete, "compacted")
}

// freshEvidence reports a context reading with an epoch and valid counts whose source and
// confirmation are within the trigger's ages and not later than now.
func freshEvidence(e ContextEvidence, now int64) bool {
	return e.Found && e.Epoch != "" && e.UsedTokens != nil && e.LimitTokens != nil && *e.LimitTokens > 0 && *e.UsedTokens >= 0 &&
		e.SourceAt <= now && e.ConfirmedAt <= now && now-e.SourceAt <= contextSourceMaxAge.Milliseconds() &&
		now-e.ConfirmedAt <= contextConfirmationMaxAge.Milliseconds()
}

// compactSentAt is when c recorded its submission's receipt, after the Enter.
func compactSentAt(ctx context.Context, q querier, c cycleRow) (int64, error) {
	var at sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(at) FROM context_cycle_events WHERE cycle_id=? AND stage=?`, c.ID, cycleCompactSent).Scan(&at)
	if err == nil && !at.Valid {
		err = sql.ErrNoRows
	}
	return at.Int64, err
}

// rearm forgets the crossing that admitted a compaction that ended keeping its Enable (before
// its input or under another epoch), so the session's next fresh reading at its threshold
// admits another. endCycleTx calls it once its transaction commits.
func (s *Store) rearm(k Key) {
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	delete(s.latches.epochs, k)
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
	s.paneSeen.keep(cycles)
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
		reason := "cycle_timeout"
		if c.State == cycleWaitingReady {
			code, err := readyRefusalCode(ctx, s.db, c)
			if err != nil {
				return err
			}
			if code != "" {
				reason += ".refused." + code
			}
		}
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, reason)
		return err
	case now >= c.StageDeadline && c.State == cycleCompactWaiting:
		// A compaction whose pane was never ready got no input: it ends with what the pane
		// showed, and the next fresh reading at the threshold admits another (#509).
		reason := cycleCompactWaiting + "_timeout"
		if seen, ok := s.paneSeen.get(c.ID); ok && seen != "" {
			reason += "." + seen
		}
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, reason)
		return err
	case now >= c.StageDeadline && c.State == cycleCompactSent:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "compact_unconfirmed")
		return err
	case now >= c.StageDeadline && !cycleInputUnproven(c.State):
		// A stage that waited for the pane names what the pane last showed. A requester that
		// reported ready but did not become idle is asked again (#431).
		reason := c.State + "_timeout"
		if cycleWaitsForPane(c.State) {
			seen := s.timeoutSeen(ctx, c)
			if c.State == cycleReady && cycleRepeatable(seen) {
				// A repeat refused because the cycle changed, its Enable or pin ended, or the
				// store is full ends the cycle as the timeout did; a changed revision makes
				// that end a no-op.
				switch err := s.repeatCycleRequest(ctx, c.ID, c.Revision, seen); {
				case err == nil:
					return nil
				case errors.Is(err, errCycleEventLimit):
					_, err = s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "event_limit")
					return err
				case !errors.Is(err, ErrCycleChanged) && !errors.Is(err, ErrCapacity):
					return err
				}
			}
			if seen != "" {
				reason += "." + seen
			}
		}
		if c.State == cycleWaitingReady {
			// The reason says whether the session read the request that went unanswered
			// (#445), or names the refusal of its newest ready report at this revision (#485).
			switch code, err := readyRefusalCode(ctx, s.db, c); {
			case err != nil:
				return err
			case code != "":
				reason += ".refused." + code
			default:
				read, err := s.cycleRequestRead(ctx, c)
				if err != nil {
					return err
				}
				if read {
					reason += ".read"
				} else {
					reason += ".unread"
				}
			}
		}
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, reason)
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
	// An asynchronous pickup's receipt is read by API only, without the wake lock, so a
	// pending request delays no other cycle or wake (#513).
	if async, ok := adapter.(AsyncPickupAdapter); ok && c.State == cyclePickupIntent {
		return s.pickupReceipt(ctx, c, t, async)
	}
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	switch c.State {
	case cycleCompactWaiting:
		return s.stepCompaction(ctx, c, t, adapter)
	case cycleCompactIntent:
		// An intent without its receipt is never entered again (#509).
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "compact_unreceipted")
		return err
	case cycleCompactSent:
		return nil // a context reading confirms it (compactionObserved), or its deadline ends it
	case cycleReady, cycleBootstrapVerified, cycleResetVerified:
		pane, err := adapter.Inspect(ctx, t, s.cycleFence(c))
		if err != nil {
			s.paneSeen.set(c.ID, inspectSeen(err))
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
			if pane.Seen == "" || !cycleCode.MatchString(pane.Seen) {
				pane.Seen = "pane_not_ready"
			}
			s.paneSeen.set(c.ID, pane.Seen)
			// Work after the ready report is not in its handoff: ask for a new one now (#456).
			if pane.Seen == subagentAfterReady && c.State == cycleReady {
				switch err := s.repeatCycleRequest(ctx, c.ID, c.Revision, pane.Seen); {
				case errors.Is(err, errCycleEventLimit):
					_, err = s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "event_limit")
					return err
				case err != nil && !errors.Is(err, ErrCycleChanged) && !errors.Is(err, ErrCapacity):
					return err
				}
			}
			return nil
		}
		s.paneSeen.set(c.ID, "")
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
		// The installed pickup skill is checked before each input.
		probe := adapter.Probe(ctx, t)
		if probe.Reason != "" {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, probe.Reason)
			return err
		}
		switch {
		case c.State == cycleReady || c.State == cycleBootstrapVerified:
			if create, ok := adapter.(SessionAdapter); ok {
				return s.createSuccessor(ctx, c, t, create)
			}
			return s.enterInput(ctx, c, cycleClearIntent, cycleClearReceived, pinnedRequester(ctx),
				func(fence CycleFence) (bool, error) { return adapter.Clear(ctx, t, fence) })
		}
		// The pane must still show the epoch that the reset proved: another clear or a new
		// session since then never gets the pending pickup.
		if epoch, err := adapter.NewEpoch(ctx, t); err != nil {
			return nil
		} else if epoch != c.SuccessorEpoch {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, "epoch_changed")
			return err
		}
		if async, ok := adapter.(AsyncPickupAdapter); ok {
			return s.startPickup(ctx, c, t, async)
		}
		return s.enterInput(ctx, c, cyclePickupIntent, cycleWaitingPickup, nil,
			func(fence CycleFence) (bool, error) { return adapter.Pickup(ctx, t, fence) })
	case cycleWaitingPickup:
		// The pane's proof that it shows the successor is recorded once, before the report.
		if display, ok := adapter.(DisplayAdapter); ok {
			if recorded, err := s.recordDisplay(ctx, c, t, display); recorded || err != nil {
				return err
			}
		}
		// A dialog that the successor's first calls raise, such as a tool approval, waits for
		// the native session or the maintainer like any other; the report may come during it.
		// The read sends nothing: the maintainer may be reading the pane in copy mode.
		if dialog, err := adapter.Dialog(ctx, t); err != nil || dialog.Digest == "" {
			return nil
		}
		_, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleWaitingPrompt, "native_dialog", nil, func(n *cycleRow) { n.PromptFrom = c.State })
		return err
	case cycleClearIntent, cycleClearReceived:
		// A created session is shown in the pane once before its proof (#513).
		if create, ok := adapter.(SessionAdapter); ok && c.State == cycleClearReceived {
			if sent, err := s.selectSuccessor(ctx, c, t, create); !sent || err != nil {
				return err
			}
		}
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
		if !adapter.Answerable(cyclePrompt(c)) {
			// The native session's own settings, or the maintainer, answer this dialog; the
			// cycle returns to its stage once the dialog is gone, or the prompt wait ends it.
			// A wait that began in waiting_pickup stays: the report is accepted in it, and
			// each later dialog of the pickup is one more in the same wait (#520).
			if c.PromptFrom == cycleWaitingPickup {
				return nil
			}
			pane, err := adapter.Inspect(ctx, t, s.cycleFence(c))
			if err != nil || pane.Prompt != "" {
				return nil
			}
			_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPrompt, c.PromptFrom, "prompt_resolved", nil, func(n *cycleRow) { n.PromptFrom = "" })
			return err
		}
		// The response is an input like any other: its intent is recorded first, and it is
		// never entered again without proof.
		prompt := cyclePrompt(c)
		c, err := s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPrompt, cyclePromptIntent, prompt, nil, nil)
		if err != nil {
			return err
		}
		answered, err := adapter.Answer(ctx, t, prompt, s.cycleFence(c))
		if err != nil || !answered {
			_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "prompt_unanswered")
			return err
		}
		to, change := promptReturn(c)
		_, err = s.advanceCycle(ctx, c.ID, c.Revision, cyclePromptIntent, to, "prompt_answered", nil, change)
		return err
	}
	return nil
}

// cyclePrompt is the code of the dialog that a prompt wait waits on. A wait that began in
// waiting_pickup waits on the native dialogs of the pickup; its reason can name its last
// answer instead (#520).
func cyclePrompt(c cycleRow) string {
	if c.PromptFrom == cycleWaitingPickup {
		return "native_dialog"
	}
	return c.Reason
}

// promptReturn is the stage that an answered dialog returns its cycle to, with the change
// that ends the wait. A wait that began in waiting_pickup goes on, for the pickup's next
// dialog or its report (#520).
func promptReturn(c cycleRow) (string, func(*cycleRow)) {
	if c.PromptFrom == cycleWaitingPickup {
		return cycleWaitingPrompt, nil
	}
	return c.PromptFrom, func(n *cycleRow) { n.PromptFrom = "" }
}

// stepCompaction enters the native compaction of a waiting compaction once its pane is idle,
// its compaction checks pass and its context is still at the threshold that admitted it. A
// pane that is not ready leaves it waiting. The input intent is recorded first: a submission
// that may have happened is never entered again, and one that surely did not happen, such as
// typed text that was erased, returns the compaction to waiting.
func (s *Store) stepCompaction(ctx context.Context, c cycleRow, t CycleTarget, adapter CycleAdapter) error {
	compact, ok := adapter.(CompactAdapter)
	if !ok {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, "adapter_unavailable")
		return err
	}
	pane, err := adapter.Inspect(ctx, t, s.cycleFence(c))
	switch {
	case err != nil:
		s.paneSeen.set(c.ID, inspectSeen(err))
		return nil
	case pane.Prompt != "":
		s.paneSeen.set(c.ID, "native_dialog")
		return nil
	case !pane.Idle:
		if pane.Seen == "" || !cycleCode.MatchString(pane.Seen) {
			pane.Seen = "pane_not_ready"
		}
		s.paneSeen.set(c.ID, pane.Seen)
		return nil
	}
	s.paneSeen.set(c.ID, "")
	k := Key{c.Family, c.SessionID}
	enables, err := s.contextEnables(ctx)
	if err != nil {
		return err
	}
	// The input needs a fresh reading at the threshold that admitted the compaction: without a
	// reading or a recent confirmation, such as after a restart, it waits; a changed mode or
	// threshold, or a reading that no longer selects it, ends it before any input. The fence
	// holds the mode and threshold through the input, so the confirmation compares with the
	// threshold in force at the send.
	policy, e := s.sessionContextPolicy(c.Family, enables[k]), s.contextEvidence(k)
	switch reason := compactionStale(c, policy, e, s.now().UnixMilli()); reason {
	case "":
	case "context_unknown", "confirmation_stale":
		s.paneSeen.set(c.ID, reason)
		return nil
	default:
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleCancelled, reason)
		return err
	}
	if probe := compact.CompactProbe(ctx, t); probe.Reason != "" {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleFailed, probe.Reason)
		return err
	}
	c, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleCompactWaiting, cycleCompactIntent, "", pinnedRequester(ctx), nil)
	if err != nil {
		return err
	}
	submitted, received, err := compact.Compact(ctx, t, s.cycleFence(c))
	switch {
	case !submitted:
		_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleCompactIntent, cycleCompactWaiting, inputReason(cycleCompactIntent, err), nil, nil)
	case !received:
		_, err = s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, inputReason(cycleCompactIntent, err))
	default:
		_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleCompactIntent, cycleCompactSent, "", nil, nil)
	}
	return err
}

// compactionStale is the fixed reason that a waiting compaction may not enter its input now:
// the session's mode or threshold changed, its reading is of another epoch, or the trigger
// does not select its newest reading (context_unknown, source_stale, below_threshold and the
// others; the crossing's own latch does not count). "" when the input may go.
func compactionStale(c cycleRow, policy ContextFamilyPolicy, e ContextEvidence, now int64) string {
	switch {
	case policy.Mode != ContextNativeCompaction:
		return "mode_changed"
	case policy.Threshold != c.Threshold:
		return "threshold_changed"
	case e.Found && e.Epoch != "" && e.Epoch != c.Epoch:
		return "epoch_changed"
	}
	_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true, Evidence: e, Now: now})
	return reason
}

// compactionPolicyHolds reports whether c's session is still in native_compaction at the
// threshold that c recorded: the family mode, and the session's own threshold read in q.
func (s *Store) compactionPolicyHolds(ctx context.Context, q querier, c cycleRow) (bool, error) {
	var threshold int64
	err := q.QueryRowContext(ctx, `SELECT threshold FROM context_enables WHERE family=? AND session_id=?`, c.Family, c.SessionID).Scan(&threshold)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	policy := s.sessionContextPolicy(c.Family, ContextEnable{Threshold: threshold})
	return policy.Mode == ContextNativeCompaction && policy.Threshold == c.Threshold, nil
}

// cycleWaitsForPane reports a stage that waits for the pane to take input.
// cycleRequestRead reports whether the requester read or acknowledged its inbox through the
// cycle's newest request.
func (s *Store) cycleRequestRead(ctx context.Context, c cycleRow) (bool, error) {
	var through int64
	err := s.db.QueryRowContext(ctx, `SELECT max(acked_through,read_through) FROM sessions WHERE family=? AND id=?`,
		c.Family, c.SessionID).Scan(&through)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && through >= c.MessageSeq, err
}

func cycleWaitsForPane(state string) bool {
	return state == cycleReady || state == cycleBootstrapVerified || state == cycleResetVerified
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

// createSuccessor records the clear intent and creates the successor session (#513). The
// created ID is recorded with the receipt, in the same write that enters clear_received, so a
// restart proves that exact session or nothing; a create without its recorded ID is never sent
// again.
func (s *Store) createSuccessor(ctx context.Context, c cycleRow, t CycleTarget, create SessionAdapter) error {
	c, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cycleClearIntent, "", pinnedRequester(ctx), nil)
	if err != nil {
		return err
	}
	id, err := create.Create(ctx, t, s.cycleFence(c))
	if err != nil || id == "" || id == c.Epoch || !nativeEpoch.MatchString(id) {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, inputReason(cycleClearIntent, err))
		return err
	}
	_, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleClearIntent, cycleClearReceived, "", nil, func(n *cycleRow) { n.SuccessorEpoch = id })
	return err
}

// selectReason is the reason of the same-state clear_received event that records the
// select's intent (#513).
const selectReason = "select_sent"

// selectSuccessor shows the created session in the pane once: its intent is recorded first,
// so a restart never selects again. It reports whether the select was sent before this step,
// so that the reset's proof can be read.
func (s *Store) selectSuccessor(ctx context.Context, c cycleRow, t CycleTarget, create SessionAdapter) (bool, error) {
	if c.SuccessorEpoch == "" {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "reset_unproven")
		return false, err
	}
	var sent int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM context_cycle_events WHERE cycle_id=? AND stage=? AND reason=? LIMIT 1`,
		c.ID, cycleClearReceived, selectReason).Scan(&sent)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	c, err = s.advanceCycle(ctx, c.ID, c.Revision, cycleClearReceived, cycleClearReceived, selectReason, pinnedRequester(ctx), nil)
	if err != nil {
		return false, err
	}
	if ok, err := create.Select(ctx, t, s.cycleFence(c)); err != nil || !ok {
		_, err := s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, inputReason("select", err))
		return false, err
	}
	return false, nil
}

// startPickup records the pickup intent and sends the pickup from a worker (#513). Without a
// free worker the cycle waits in its stage, with nothing recorded.
func (s *Store) startPickup(ctx context.Context, c cycleRow, t CycleTarget, async AsyncPickupAdapter) error {
	if !s.cycleWork.free() {
		return nil
	}
	c, err := s.advanceCycle(ctx, c.ID, c.Revision, c.State, cyclePickupIntent, "", nil, nil)
	if err != nil {
		return err
	}
	t.State, t.pickupAt = c.State, c.UpdatedAt
	fence := s.cycleFence(c)
	// A worker that cannot start sends nothing: the next step finds no receipt.
	s.cycleWork.start(c.ID, func(ctx context.Context) { async.SendPickup(ctx, t, fence) })
	return nil
}

// pickupReceipt reads an asynchronous pickup's receipt back. With it the cycle waits for the
// report; once the worker ended without it, or after a restart that has no worker, the pickup
// is uncertain and is never sent again. A read that fails is tried again until the stage's
// deadline.
func (s *Store) pickupReceipt(ctx context.Context, c cycleRow, t CycleTarget, async AsyncPickupAdapter) error {
	running := s.cycleWork.running(c.ID)
	received, err := async.PickupReceipt(ctx, t)
	switch {
	case err == nil && received:
		_, err := s.advanceCycle(ctx, c.ID, c.Revision, cyclePickupIntent, cycleWaitingPickup, "", nil, nil)
		return err
	case running || err != nil && s.now().UnixMilli() < c.StageDeadline:
		return nil
	}
	_, err = s.endCycle(ctx, c.ID, c.Revision, cycleUncertain, "pickup_unreceipted")
	return err
}

// recordDisplay records, once, a current view of the pane that shows the successor and no
// other session (#513), and reports whether it did.
func (s *Store) recordDisplay(ctx context.Context, c cycleRow, t CycleTarget, display DisplayAdapter) (bool, error) {
	if shown, err := pickupDisplayed(ctx, s.db, c.ID); err != nil || shown {
		return false, err
	}
	if shown, foreign, err := display.PickupDisplay(ctx, t); err != nil || !shown || foreign {
		return false, nil
	}
	_, err := s.advanceCycle(ctx, c.ID, c.Revision, cycleWaitingPickup, cycleWaitingPickup, pickupDisplayedReason, nil, nil)
	return err == nil, err
}

// displayProof is the pane's proof for a DisplayAdapter's pickup report (#513): a current view
// that shows the successor and no other session, or the recorded proof and a current view that
// shows no other session.
func (s *Store) displayProof(ctx context.Context, c cycleRow, display DisplayAdapter) error {
	shown, foreign, err := display.PickupDisplay(ctx, s.cycleTarget(ctx, c))
	if err != nil || foreign {
		return ErrCycleDisplay
	}
	if shown {
		return nil
	}
	if recorded, err := pickupDisplayed(ctx, s.db, c.ID); err != nil || !recorded {
		return ErrCycleDisplay
	}
	return nil
}

// maxCycleWorkers bounds the asynchronous pickups that run at once (#513).
const maxCycleWorkers = 4

// cycleWorkers runs the asynchronous pickups (#513): one per cycle and at most
// maxCycleWorkers at a time, each under a context that the daemon's Close cancels before it
// waits for them.
type cycleWorkers struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	active  map[string]bool
	stopped bool
}

// free reports whether a worker can start.
func (w *cycleWorkers) free() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.stopped && len(w.active) < maxCycleWorkers
}

// running reports whether the cycle's worker runs.
func (w *cycleWorkers) running(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active[id]
}

// start runs one worker for the cycle, unless one runs or none is free or the workers stopped.
func (w *cycleWorkers) start(id string, run func(context.Context)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.active[id] || len(w.active) >= maxCycleWorkers {
		return false
	}
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(context.Background())
		w.active = map[string]bool{}
	}
	w.active[id] = true
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer func() {
			w.mu.Lock()
			delete(w.active, id)
			w.mu.Unlock()
		}()
		run(w.ctx)
	}()
	return true
}

// stop cancels the workers and waits until each one returned.
func (w *cycleWorkers) stop() {
	w.mu.Lock()
	w.stopped = true
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	w.wg.Wait()
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
		// A compaction's input also stops when the session's mode or threshold changed (#509).
		if current.Mode == ContextNativeCompaction {
			if ok, err := s.compactionPolicyHolds(ctx, tx, current); err != nil || !ok {
				if err == nil {
					err = ErrCycleChanged
				}
				return err
			}
		}
		return nil
	}
}

// cycleTarget is c's target with the koinon-pickup skill and the store's host tools. The
// tools run the session's recorded tmux, read now: it is a tool path, not part of the pinned
// identity (#418).
func (s *Store) cycleTarget(ctx context.Context, c cycleRow) CycleTarget {
	t := c.target()
	var executable string
	// A session row that cannot be read leaves "": tmux from PATH.
	_ = s.db.QueryRowContext(ctx, `SELECT tmux_path,directory FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).Scan(&executable, &t.Directory)
	t.tools = s.hostTools(executable)
	if c.Family == "agy" {
		t.witnesses = s.agyWitness.of(c.ID)
		t.observedEpoch = s.contextEvidence(Key{c.Family, c.SessionID}).Epoch
		t.stateRoot = s.root
	}
	if c.Family == "opencode" {
		// The requester's launched server, which also hosts the created successor (#513).
		var raw []byte
		var wake struct {
			LaunchID string `json:"launch_id"`
		}
		if s.db.QueryRowContext(ctx, `SELECT wake_target FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).Scan(&raw) == nil &&
			json.Unmarshal(raw, &wake) == nil {
			if target, err := s.openCodeTarget(ctx, wake.LaunchID); err == nil {
				t.openCode = &target
			}
		}
		var at sql.NullInt64
		if s.db.QueryRowContext(ctx, `SELECT MAX(at) FROM context_cycle_events WHERE cycle_id=? AND stage=?`, c.ID, cyclePickupIntent).Scan(&at) == nil && at.Valid {
			t.pickupAt = at.Int64
		}
	}
	t.Pickup = pickupSkillEntry()
	var held int
	t.Holds = s.db.QueryRowContext(ctx, `SELECT 1 FROM names WHERE kind='alias' AND family=? AND holder_id=? LIMIT 1`, c.Family, c.SessionID).Scan(&held) == nil
	if c.State == cycleReady {
		// The ready report's own event has no reason; a resolved dialog's is prompt_resolved
		// (#456). An unreadable event compares with the cycle's start, which never misses work.
		var at sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT MAX(at) FROM context_cycle_events WHERE cycle_id=? AND stage=? AND reason=''`,
			c.ID, cycleReady).Scan(&at)
		t.ReadyAt = c.CreatedAt
		if err == nil && at.Valid {
			t.ReadyAt = at.Int64
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
			'waiting_pickup','waiting_prompt','prompt_intent','compact_intent','compact_sent')
		AND c.host_pid=s.host_pid AND c.host_start=s.host_start AND c.tmux_socket=s.tmux_socket AND c.tmux_pane=s.tmux_pane`,
		target.Family, target.ID).Scan(&held)
	return held > 0, err
}

// trimCycles removes finished cycles past the retention and beyond the newest
// maxTerminalCycles, with their events and refused ready reports. Unfinished cycles and their credits stay.
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM context_cycle_refusals WHERE cycle_id IN (`+selected+`)`, cutoff, maxTerminalCycles); err != nil {
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
	if adapter == nil || adapter.Answerable(cyclePrompt(c)) {
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
	if c.State != cycleWaitingPrompt || c.Revision != revision || adapter == nil || adapter.Answerable(cyclePrompt(c)) {
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
	to, change := promptReturn(c)
	_, err = s.advanceCycle(ctx, id, c.Revision, cyclePromptIntent, to, "prompt_answered", nil, change)
	if errors.Is(err, ErrCycleChanged) {
		// The approved call can be the pickup report itself, which completes the cycle
		// before the return to its stage is recorded: the answer was received.
		if now, rerr := scanCycle(s.db.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id)); rerr == nil && now.State == cycleComplete {
			return nil
		}
	}
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
