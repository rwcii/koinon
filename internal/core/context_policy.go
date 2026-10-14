package core

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"sync/atomic"
	"time"
)

// Context policy (#196, chunk 01): the maintainer's family defaults for context mode and
// threshold, each session's own Enable switch, and the pure selection that says whether a
// session's fresh context observation reaches its threshold. Nothing here enters terminal
// input; the managed cycle is chunks 02 and 03.

// Context modes. Codex's native_compaction leaves its own compaction to the client; the
// mode never edits a native setting.
const (
	ContextObserve          = "observe"
	ContextNativeCompaction = "native_compaction"
	ContextHandoffCycle     = "handoff_cycle"
)

// contextModeValues are the fixed integers the settings rows store for each mode.
var contextModeValues = map[string]int64{ContextObserve: 0, ContextNativeCompaction: 1, ContextHandoffCycle: 2}

// ContextFamilyPolicy is one family's mode and threshold (percent of the context limit).
// Error names a stored value that could not be applied; the default applies instead.
type ContextFamilyPolicy struct {
	Family    string `json:"family"`
	Mode      string `json:"mode"`
	Threshold int64  `json:"threshold"`
	Error     string `json:"error,omitempty"`
}

// contextDefaultThreshold is the approved default threshold (Gate B, #196).
const contextDefaultThreshold = 75

// defaultContextPolicy is a family's approved default: Claude uses the handoff cycle,
// Codex its native compaction, the other families observe.
func defaultContextPolicy(family string) ContextFamilyPolicy {
	mode := ContextObserve
	switch family {
	case "claude":
		mode = ContextHandoffCycle
	case "codex":
		mode = ContextNativeCompaction
	}
	return ContextFamilyPolicy{Family: family, Mode: mode, Threshold: contextDefaultThreshold}
}

// validContextPolicy checks a mode and threshold for a family: native_compaction is
// Codex's only.
func validContextPolicy(family, mode string, threshold int64) bool {
	if _, known := contextModeValues[mode]; !known || threshold < 1 || threshold > 100 {
		return false
	}
	return mode != ContextNativeCompaction || family == "codex"
}

func contextFamily(family string) bool {
	for _, f := range SupportedFamilies() {
		if f == family {
			return true
		}
	}
	return false
}

type contextPolicyState struct {
	current atomic.Pointer[map[string]ContextFamilyPolicy]
}

// ContextPolicy returns each supported family's policy in force, in family order.
func (s *Store) ContextPolicy() []ContextFamilyPolicy {
	current := s.contextPolicy.current.Load()
	result := []ContextFamilyPolicy{}
	for _, family := range SupportedFamilies() {
		p := defaultContextPolicy(family)
		if current != nil {
			if stored, found := (*current)[family]; found {
				p = stored
			}
		}
		result = append(result, p)
	}
	return result
}

func (s *Store) familyContextPolicy(family string) ContextFamilyPolicy {
	for _, p := range s.ContextPolicy() {
		if p.Family == family {
			return p
		}
	}
	return defaultContextPolicy(family)
}

// loadContextPolicy reads the stored family values. A stored pair that is not valid keeps
// the family's default and names the error.
func (s *Store) loadContextPolicy(ctx context.Context) error {
	stored, invalid := map[string]int64{}, map[string]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT name,value FROM settings WHERE name LIKE 'context_%'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		// The column accepts any SQLite value; one that is not an integer is invalid, not a
		// failure to open the store.
		var name string
		var value any
		if err := rows.Scan(&name, &value); err != nil {
			return err
		}
		if n, ok := value.(int64); ok {
			stored[name] = n
		} else {
			invalid[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	policy := map[string]ContextFamilyPolicy{}
	for _, family := range SupportedFamilies() {
		p := defaultContextPolicy(family)
		modeValue, hasMode := stored["context_mode_"+family]
		threshold, hasThreshold := stored["context_threshold_"+family]
		if invalid["context_mode_"+family] || invalid["context_threshold_"+family] {
			p.Error = "stored_value_invalid"
		} else if hasMode || hasThreshold {
			mode := ""
			for name, value := range contextModeValues {
				if value == modeValue {
					mode = name
				}
			}
			if hasMode && hasThreshold && validContextPolicy(family, mode, threshold) {
				p.Mode, p.Threshold = mode, threshold
			} else {
				p.Error = "stored_value_invalid"
			}
		}
		policy[family] = p
	}
	s.contextPolicy.current.Store(&policy)
	return nil
}

// SetContextPolicy stores one family's mode and threshold in one transaction with the
// dashboard action's audit record. It enables no session; it applies after the commit.
func (s *Store) SetContextPolicy(ctx context.Context, family, mode string, threshold int64) error {
	if !contextFamily(family) || !validContextPolicy(family, mode, threshold) {
		return ErrInvalid
	}
	s.settings.write.Lock()
	defer s.settings.write.Unlock()
	// A mode without the cycle cancels pending cycles: it is a control and may use the
	// reserve, for its two bounded settings rows as well.
	class := ordinary
	if mode != ContextHandoffCycle {
		class = control
	}
	tx, err := s.begin(ctx, class)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	for _, setting := range []struct {
		name  string
		value int64
	}{{"context_mode_" + family, contextModeValues[mode]}, {"context_threshold_" + family, threshold}} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES (?,?,?)
			ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, setting.name, setting.value, now); err != nil {
			return tx.fail(err)
		}
	}
	if mode != ContextHandoffCycle {
		// A mode without the cycle cancels the family's pending cycles; a threshold change
		// applies to the next request only.
		if err := s.fenceCyclesTx(ctx, tx, "mode_changed", `family=?`, family); err != nil {
			return tx.fail(err)
		}
	}
	setAuditTarget(ctx, "context "+family+" mode "+mode+" threshold "+strconv.FormatInt(threshold, 10))
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.loadContextPolicy(context.WithoutCancel(ctx))
}

// ContextEnable is one session's Enable switch. The record pins the session's revision,
// participant address, host and tmux pane when it was set; those private fields never
// leave the store.
type ContextEnable struct {
	Enabled   bool  `json:"enabled"`
	Revision  int64 `json:"revision"`
	UpdatedAt int64 `json:"updated_at"`
}

// ErrContextCycleUnavailable refuses an Enable while no verified cycle adapter exists for
// the session's family and platform (#196: chunk 03 supplies the first one).
var ErrContextCycleUnavailable = Refusal{"context_cycle_unavailable", "no verified handoff cycle is available for this session yet"}

// errSessionRevisionChanged refuses an Enable change for a session that changed since the
// dashboard showed it.
var errSessionRevisionChanged = Refusal{"revision_changed", "the session changed since the page was loaded"}

// contextCycleProbe asks the session's family adapter whether its cycle can run for the
// session's pinned host and pane (sessionProbe); tests replace it.
var contextCycleProbe = func(s *Store, ctx context.Context, k Key) CycleProbe { return s.sessionProbe(ctx, k) }

// SetContextEnable turns one session's Enable on or off. The session is named by its peer
// name and the revision the dashboard showed; ifRevision is the Enable record's revision
// (0 when there is none). Enabling needs an available cycle; disabling is always accepted.
// Family settings never create an Enable.
func (s *Store) SetContextEnable(ctx context.Context, peer string, sessionRevision, ifRevision int64, enabled bool) (ContextEnable, error) {
	if peer == "" || sessionRevision < 1 || ifRevision < 0 {
		return ContextEnable{}, ErrInvalid
	}
	// The adapter's Probe reads the host, its pane and its files, so it runs before the
	// transaction; an Enable for a session that changed meanwhile is refused below by its
	// revision.
	var probe CycleProbe
	if enabled {
		var k Key
		if err := s.db.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=? AND family!='maintainer'`, peer).
			Scan(&k.Family, &k.ID); err == nil {
			probe = contextCycleProbe(s, ctx, k)
		}
	}
	// Disabling ends a funded cycle, so it may use the reserve, as every cycle control does.
	class := ordinary
	if !enabled {
		class = control
	}
	tx, err := s.begin(ctx, class)
	if err != nil {
		return ContextEnable{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	var k Key
	err = tx.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=? AND family!='maintainer'`, peer).Scan(&k.Family, &k.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextEnable{}, ErrPeerNotFound
	}
	if err != nil {
		return ContextEnable{}, err
	}
	session, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), now)
	if err != nil {
		return ContextEnable{}, err
	}
	var hostPID, hostStart int64
	var socket, pane string
	if err := tx.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`, k.Family, k.ID).
		Scan(&hostPID, &hostStart, &socket, &pane); err != nil {
		return ContextEnable{}, err
	}
	revision, address := session.Revision, session.Address
	if revision != sessionRevision {
		return ContextEnable{}, errSessionRevisionChanged
	}
	current, err := contextEnableOf(ctx, tx.Tx, k)
	if err != nil {
		return ContextEnable{}, err
	}
	if current.Revision != ifRevision {
		return current, Refusal{"context_enable_changed", "the session's Enable changed since it was shown"}
	}
	if enabled {
		if err := active(ctx, tx.Tx, now, k); err != nil {
			return current, err
		}
		if probe.Reason != "" || probe.Bootstrap == "" {
			return current, ErrContextCycleUnavailable
		}
	}
	if current.Revision == 0 && !enabled {
		setAuditTarget(ctx, "context enable "+peer+" off unchanged")
		return current, tx.Commit()
	}
	next := ContextEnable{Enabled: enabled, Revision: current.Revision + 1, UpdatedAt: now}
	if !enabled {
		// Disable fences the session's cycle at once, also one that is already waiting.
		if err := s.fenceCyclesTx(ctx, tx, "disabled", `family=? AND session_id=?`, k.Family, k.ID); err != nil {
			return current, tx.fail(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO context_enables(family,session_id,enabled,revision,session_revision,address,host_pid,host_start,tmux_socket,tmux_pane,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(family,session_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision,
		session_revision=excluded.session_revision,address=excluded.address,host_pid=excluded.host_pid,host_start=excluded.host_start,
		tmux_socket=excluded.tmux_socket,tmux_pane=excluded.tmux_pane,updated_at=excluded.updated_at`,
		k.Family, k.ID, enabled, next.Revision, revision, address, hostPID, hostStart, socket, pane, now); err != nil {
		return current, tx.fail(err)
	}
	state := "off"
	if enabled {
		state = "on"
	}
	setAuditTarget(ctx, "context enable "+peer+" "+state+" revision "+strconv.FormatInt(next.Revision, 10))
	return next, tx.Commit()
}

func contextEnableOf(ctx context.Context, tx *sql.Tx, k Key) (ContextEnable, error) {
	var e ContextEnable
	err := tx.QueryRowContext(ctx, `SELECT enabled,revision,updated_at FROM context_enables WHERE family=? AND session_id=?`, k.Family, k.ID).
		Scan(&e.Enabled, &e.Revision, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextEnable{}, nil
	}
	return e, err
}

// contextEnables reads the Enable records of the given sessions.
func (s *Store) contextEnables(ctx context.Context) (map[Key]ContextEnable, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT family,session_id,enabled,revision,updated_at FROM context_enables`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[Key]ContextEnable{}
	for rows.Next() {
		var k Key
		var e ContextEnable
		if err := rows.Scan(&k.Family, &k.ID, &e.Enabled, &e.Revision, &e.UpdatedAt); err != nil {
			return nil, err
		}
		result[k] = e
	}
	return result, rows.Err()
}

// Trigger clocks (#196 DoD, approved at Gate B): a trigger needs the context source at most
// two minutes old and the daemon's last confirmation of it at most 90 seconds old.
const (
	contextSourceMaxAge       = 2 * time.Minute
	contextConfirmationMaxAge = 90 * time.Second
)

// ContextEvidence is a session's context observation as the trigger reads it, with its
// private native epoch.
type ContextEvidence struct {
	Found       bool
	UsedTokens  *int64
	LimitTokens *int64
	Epoch       string
	SourceAt    int64 // when the source recorded the value (ms)
	ConfirmedAt int64 // when the daemon last received confirmation (ms)
}

// ContextTriggerInput is everything the selection reads.
type ContextTriggerInput struct {
	Active         bool
	Enabled        bool
	Policy         ContextFamilyPolicy
	CycleAvailable bool
	// OverwatchConflict reports a retained name record named overwatch, the reserved
	// sender of the cycle request (#196): no cycle is admitted until it is resolved.
	OverwatchConflict bool
	Evidence          ContextEvidence
	Now               int64  // ms
	LatchedEpoch      string // the epoch whose crossing was already selected, or ""
}

// SelectContextTrigger returns whether a session's context has reached its threshold for
// the managed cycle, or the fixed reason it has not. It writes nothing.
func SelectContextTrigger(in ContextTriggerInput) (bool, string) {
	e := in.Evidence
	switch {
	case !in.Active:
		return false, "session_inactive"
	case in.Policy.Mode != ContextHandoffCycle:
		return false, "mode_" + in.Policy.Mode
	case !in.Enabled:
		return false, "not_enabled"
	case !in.CycleAvailable:
		return false, "context_cycle_unavailable"
	case in.OverwatchConflict:
		return false, "overwatch_name_conflict"
	case !e.Found:
		return false, "context_unknown"
	case e.UsedTokens == nil || e.LimitTokens == nil:
		return false, "context_counts_unavailable"
	case *e.LimitTokens <= 0 || *e.UsedTokens < 0:
		return false, "context_counts_invalid"
	case e.Epoch == "":
		return false, "epoch_unverified"
	case e.SourceAt > in.Now || e.ConfirmedAt > in.Now:
		return false, "observation_future"
	case in.Now-e.SourceAt > contextSourceMaxAge.Milliseconds():
		return false, "source_stale"
	case in.Now-e.ConfirmedAt > contextConfirmationMaxAge.Milliseconds():
		return false, "confirmation_stale"
	case *e.UsedTokens*100 < in.Policy.Threshold**e.LimitTokens:
		return false, "below_threshold"
	case in.LatchedEpoch == e.Epoch:
		return false, "already_selected_for_epoch"
	}
	return true, ""
}

// contextLatches remembers, per session, the epoch whose crossing was selected, so that
// unchanged high samples are selected once. A new epoch or a compaction (usage falling
// below the threshold) clears it.
type contextLatches struct {
	epochs map[Key]string
}

// latched returns the epoch to pass to SelectContextTrigger for a session's evidence.
func (l *contextLatches) latched(k Key, e ContextEvidence, threshold int64) string {
	latched := l.epochs[k]
	if latched == "" {
		return ""
	}
	if e.Epoch != latched || e.UsedTokens != nil && e.LimitTokens != nil && *e.UsedTokens*100 < threshold**e.LimitTokens {
		delete(l.epochs, k)
		return ""
	}
	return latched
}

// mark records a selected crossing for its epoch.
func (l *contextLatches) mark(k Key, epoch string) {
	if l.epochs == nil {
		l.epochs = map[Key]string{}
	}
	l.epochs[k] = epoch
}
