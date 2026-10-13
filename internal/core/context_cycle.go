package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Managed context cycles (#196, chunk 02). A cycle is admitted when an enabled session's
// context reaches its family threshold; it asks the session, through a typed overwatch
// request in its inbox, to write its own handoff. The session reports ready, a cycle
// adapter clears its context and enters pickup, and the successor reports its pickup.
// Each stage is a durable state with a compared revision; one funded terminal outcome
// ends every cycle (docs/CONTEXT-CYCLE-STORAGE.md).

const (
	cycleRequested     = "requested"
	cycleWaitingReady  = "waiting_ready"
	cycleReady         = "ready"
	cycleClearIntent   = "clear_intent"
	cycleClearReceived = "clear_received"
	cycleResetVerified = "reset_verified"
	cyclePickupIntent  = "pickup_intent"
	cycleWaitingPickup = "waiting_pickup"
	cycleWaitingPrompt = "waiting_prompt"
	cycleComplete      = "complete"
	cycleCancelled     = "cancelled"
	cycleFailed        = "failed"
	cycleUncertain     = "uncertain"
)

// cycleCreditPages funds one cycle's terminal outcome (docs/CONTEXT-CYCLE-STORAGE.md): the
// cycle row rewrite, its event, its audit row with the audit_at entry and two Enable rows,
// 422 pages at a B-tree height of 17 with overflow and pointer-map pages, rounded up.
const cycleCreditPages = 512

// Cycle bounds (#196 definition of done, approved at Gate B).
const (
	maxActiveCycles        = 16
	maxCycleEvents         = 16
	maxTerminalCycles      = 1024
	terminalCycleRetention = 30 * 24 * time.Hour
	cycleReadyWait         = 15 * time.Minute
	cycleReceiptWait       = 2 * time.Minute
	cyclePromptWait        = 10 * time.Minute
	cycleLifetime          = 30 * time.Minute
	maxArtifactName        = 128
	maxArtifactBytes       = 1 << 20
	maxPickupReport        = 1024
)

// overwatchKey is the reserved system sender of managed-cycle requests. It is not the
// maintainer, holds no inbox and no session, and no native session can take its name.
var overwatchKey = Key{Family: "overwatch", ID: "overwatch"}

const overwatchName = "overwatch"

// cycleRequestKind is the system_kind of the typed managed-cycle request message.
const cycleRequestKind = "context_cycle_request"

const contextCyclesTable = `CREATE TABLE context_cycles (
	cycle_id TEXT PRIMARY KEY, credit INTEGER NOT NULL CHECK (credit IN (0,1)),
	revision INTEGER NOT NULL CHECK (revision>0),
	state TEXT NOT NULL CHECK (state IN ('requested','waiting_ready','ready','clear_intent','clear_received',
		'reset_verified','pickup_intent','waiting_pickup','waiting_prompt','complete','cancelled','failed','uncertain')),
	prompt_from TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
	family TEXT NOT NULL, session_id TEXT NOT NULL, peer TEXT NOT NULL, address TEXT NOT NULL,
	host_pid INTEGER NOT NULL, host_start INTEGER NOT NULL, tmux_socket TEXT NOT NULL, tmux_pane TEXT NOT NULL,
	enable_revision INTEGER NOT NULL, epoch TEXT NOT NULL, mode TEXT NOT NULL, threshold INTEGER NOT NULL,
	used_tokens INTEGER NOT NULL, limit_tokens INTEGER NOT NULL, source_at INTEGER NOT NULL, confirmed_at INTEGER NOT NULL,
	message_seq INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
	stage_deadline INTEGER NOT NULL, cycle_deadline INTEGER NOT NULL,
	artifact TEXT NOT NULL DEFAULT '', artifact_dev INTEGER NOT NULL DEFAULT 0, artifact_ino INTEGER NOT NULL DEFAULT 0,
	artifact_size INTEGER NOT NULL DEFAULT 0, artifact_mtime INTEGER NOT NULL DEFAULT 0,
	adapter TEXT NOT NULL DEFAULT '', catalog_digest TEXT NOT NULL DEFAULT '',
	successor_id TEXT NOT NULL DEFAULT '', successor_epoch TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL DEFAULT '', report TEXT NOT NULL DEFAULT '',
	events INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0);`

const contextCycleEventsTable = `CREATE TABLE context_cycle_events (
	cycle_id TEXT NOT NULL, ordinal INTEGER NOT NULL, at INTEGER NOT NULL,
	stage TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL,
	PRIMARY KEY (cycle_id, ordinal)) WITHOUT ROWID;`

// cycleTerminal reports whether a state ends a cycle.
func cycleTerminal(state string) bool {
	return state == cycleComplete || state == cycleCancelled || state == cycleFailed || state == cycleUncertain
}

// cycleHoldsPane reports the states in which the cycle owns its pane: ordinary pane
// wakes wait until it ends, so no notice text interleaves with cycle input.
func cycleHoldsPane(state string) bool {
	switch state {
	case cycleReady, cycleClearIntent, cycleClearReceived, cycleResetVerified, cyclePickupIntent, cycleWaitingPickup, cycleWaitingPrompt:
		return true
	}
	return false
}

// cycleIntent reports the states that recorded an external input whose receipt is not
// recorded yet: their result is unknown until the adapter proves it.
func cycleIntent(state string) bool { return state == cycleClearIntent || state == cyclePickupIntent }

// CycleEvent is one durable stage change, with fixed codes and no captured text.
type CycleEvent struct {
	Ordinal int64  `json:"ordinal"`
	At      int64  `json:"at"`
	Stage   string `json:"stage"`
	Result  string `json:"result"`
	Reason  string `json:"reason,omitempty"`
}

// CycleView is a cycle as its own session and the dashboard see it. It holds no native
// ID, epoch, host, pane, socket or report text.
type CycleView struct {
	ID            string       `json:"cycle_id"`
	Revision      int64        `json:"revision"`
	State         string       `json:"state"`
	PromptFrom    string       `json:"prompt_from,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	Peer          string       `json:"peer"`
	Address       string       `json:"address,omitempty"`
	Mode          string       `json:"mode"`
	Threshold     int64        `json:"threshold"`
	CreatedAt     int64        `json:"created_at"`
	UpdatedAt     int64        `json:"updated_at"`
	StageDeadline int64        `json:"stage_deadline"`
	CycleDeadline int64        `json:"cycle_deadline"`
	Artifact      string       `json:"artifact,omitempty"`
	ArtifactBytes int64        `json:"artifact_bytes,omitempty"`
	Outcome       string       `json:"outcome,omitempty"`
	ReportBytes   int          `json:"report_bytes,omitempty"`
	FinishedAt    int64        `json:"finished_at,omitempty"`
	Role          string       `json:"role"` // requester or successor, for the calling session
	Events        []CycleEvent `json:"events"`
}

// cycleRow is the full private record.
type cycleRow struct {
	ID, State, PromptFrom, Reason, Peer, Address, Mode, Epoch string
	Family, SessionID, Socket, Pane                           string
	Revision, HostPID, HostStart, EnableRevision, Threshold   int64
	Used, Limit, SourceAt, ConfirmedAt, MessageSeq            int64
	CreatedAt, UpdatedAt, StageDeadline, CycleDeadline        int64
	Artifact                                                  string
	ArtifactDev, ArtifactIno, ArtifactSize, ArtifactMtime     int64
	Adapter, CatalogDigest, SuccessorID, SuccessorEpoch       string
	Outcome, Report                                           string
	Events, FinishedAt                                        int64
	Credit                                                    bool
}

const cycleColumns = `cycle_id,credit,revision,state,prompt_from,reason,family,session_id,peer,address,host_pid,host_start,
	tmux_socket,tmux_pane,enable_revision,epoch,mode,threshold,used_tokens,limit_tokens,source_at,confirmed_at,message_seq,
	created_at,updated_at,stage_deadline,cycle_deadline,artifact,artifact_dev,artifact_ino,artifact_size,artifact_mtime,
	adapter,catalog_digest,successor_id,successor_epoch,outcome,report,events,finished_at`

func scanCycle(row interface{ Scan(...any) error }) (cycleRow, error) {
	var c cycleRow
	err := row.Scan(&c.ID, &c.Credit, &c.Revision, &c.State, &c.PromptFrom, &c.Reason, &c.Family, &c.SessionID, &c.Peer, &c.Address,
		&c.HostPID, &c.HostStart, &c.Socket, &c.Pane, &c.EnableRevision, &c.Epoch, &c.Mode, &c.Threshold, &c.Used, &c.Limit,
		&c.SourceAt, &c.ConfirmedAt, &c.MessageSeq, &c.CreatedAt, &c.UpdatedAt, &c.StageDeadline, &c.CycleDeadline,
		&c.Artifact, &c.ArtifactDev, &c.ArtifactIno, &c.ArtifactSize, &c.ArtifactMtime, &c.Adapter, &c.CatalogDigest,
		&c.SuccessorID, &c.SuccessorEpoch, &c.Outcome, &c.Report, &c.Events, &c.FinishedAt)
	return c, err
}

func cyclesWhere(ctx context.Context, q querier, where string, args ...any) ([]cycleRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []cycleRow
	for rows.Next() {
		c, err := scanCycle(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

const activeCycle = `state NOT IN ('complete','cancelled','failed','uncertain')`

func (c cycleRow) view(ctx context.Context, q querier, role string) (CycleView, error) {
	v := CycleView{ID: c.ID, Revision: c.Revision, State: c.State, PromptFrom: c.PromptFrom, Reason: c.Reason, Peer: c.Peer,
		Address: c.Address, Mode: c.Mode, Threshold: c.Threshold, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		StageDeadline: c.StageDeadline, CycleDeadline: c.CycleDeadline, Artifact: c.Artifact, ArtifactBytes: c.ArtifactSize,
		Outcome: c.Outcome, ReportBytes: len(c.Report), FinishedAt: c.FinishedAt, Role: role, Events: []CycleEvent{}}
	rows, err := q.QueryContext(ctx, `SELECT ordinal,at,stage,result,reason FROM context_cycle_events WHERE cycle_id=? ORDER BY ordinal`, c.ID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var e CycleEvent
		if err := rows.Scan(&e.Ordinal, &e.At, &e.Stage, &e.Result, &e.Reason); err != nil {
			return v, err
		}
		v.Events = append(v.Events, e)
	}
	return v, rows.Err()
}

func newCycleID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// cycleEvent appends one event. The last slot is kept for the terminal outcome: a
// non-terminal stage that would take it is refused with errCycleEventLimit, and the
// caller ends the cycle instead.
func cycleEvent(ctx context.Context, tx *writeTx, c *cycleRow, now int64, stage, result, reason string) error {
	if !cycleTerminal(stage) && c.Events >= maxCycleEvents-1 {
		return errCycleEventLimit
	}
	c.Events++
	_, err := tx.ExecContext(ctx, `INSERT INTO context_cycle_events(cycle_id,ordinal,at,stage,result,reason) VALUES (?,?,?,?,?,?)`,
		c.ID, c.Events, now, stage, result, reason)
	return err
}

var errCycleEventLimit = errors.New("cycle event limit")

// Refusals of the own-cycle calls.
var (
	ErrCycleNotFound = Refusal{"cycle_not_found", "this session has no managed cycle with that ID"}
	ErrCycleChanged  = Refusal{"cycle_changed", "the cycle is not at that revision or stage; read its current state"}
	ErrCycleEpoch    = Refusal{"cycle_epoch_unverified", "the native client did not supply this session's verified current epoch"}
	ErrCycleArtifact = Refusal{"cycle_artifact_invalid", "the handoff artifact is not a new, bounded, ignored and untracked file in this session's own handoff directory"}
)

// cycleTarget is what a cycle adapter acts on: the pinned native host and pane.
type CycleTarget struct {
	CycleID   string
	Family    string
	SessionID string
	HostPID   int64
	HostStart int64
	Socket    string
	Pane      string
	Epoch     string // the pre-reset native epoch
}

func (c cycleRow) target() CycleTarget {
	return CycleTarget{CycleID: c.ID, Family: c.Family, SessionID: c.SessionID, HostPID: c.HostPID, HostStart: c.HostStart,
		Socket: c.Socket, Pane: c.Pane, Epoch: c.Epoch}
}

// PaneState is a cycle adapter's fresh inspection of the pinned pane.
type PaneState struct {
	Idle   bool   // an idle prompt that accepts input, with no intervening agent
	Prompt string // a recognized native prompt's fixed code, or ""
}

// CycleAdapter enters cycle input into one family's native session. Chunk 03 supplies the
// first verified adapter; until then none is registered and Enable is refused. Each method
// proves its own target before acting and returns whether its effect was received.
type CycleAdapter interface {
	Version() string
	Inspect(ctx context.Context, t CycleTarget) (PaneState, error)
	Clear(ctx context.Context, t CycleTarget) (received bool, err error)
	// NewEpoch returns the native epoch that replaced t.Epoch in the same host and pane,
	// or "" while none is proven.
	NewEpoch(ctx context.Context, t CycleTarget) (string, error)
	Pickup(ctx context.Context, t CycleTarget) (received bool, err error)
	// Answer responds to a recognized prompt and reports whether the response was read back.
	Answer(ctx context.Context, t CycleTarget, prompt string) (bool, error)
}

// cycleAdapters holds the registered adapters by family. Tests register synthetic ones.
var cycleAdapters = map[string]CycleAdapter{}

func cycleAdapterOf(family string) CycleAdapter { return cycleAdapters[family] }

// cycleCode bounds an adapter's version and prompt codes, which the cycle row and its events
// store (docs/CONTEXT-CYCLE-STORAGE.md).
var cycleCode = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func init() {
	contextCycleAvailable = func(family string) bool { return cycleAdapters[family] != nil }
}

// cycleRequestBody is the fixed text of the typed request. It names the cycle and asks the
// session to finish or pause its step and write its own handoff.
func cycleRequestBody(id string, revision int64) string {
	return "overwatch: managed context cycle " + id + " revision " + strconv.FormatInt(revision, 10) +
		" requests your handoff. Finish or pause your current step, write your handoff with your canonical handoff " +
		"procedure, then call context_cycle_ready with this cycle ID, its revision and the handoff file name. " +
		"See the Managed cycle reporting section of koinon guide."
}

// admitContextCycle admits a cycle for one session when its context crossed its threshold.
// It writes the cycle, its first event, the typed request and the audit record in one
// ordinary transaction; a refusal stores nothing. It reports whether a cycle was admitted.
func (s *Store) admitContextCycle(ctx context.Context, k Key) (bool, error) {
	adapter := cycleAdapterOf(k.Family)
	if adapter == nil || !cycleCode.MatchString(adapter.Version()) {
		return false, nil
	}
	policy := s.familyContextPolicy(k.Family)
	evidence := s.contextEvidence(k)
	s.observed.mu.Lock()
	latched := s.latches.latched(k, evidence, policy.Threshold)
	s.observed.mu.Unlock()
	now := s.now().UnixMilli()
	// Most observations select nothing: check with reads before entering a write.
	if enables, err := s.contextEnables(ctx); err != nil || !enables[k].Enabled {
		return false, err
	}
	if eligible, _ := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true,
		Evidence: evidence, Now: now, LatchedEpoch: latched}); !eligible {
		return false, nil
	}
	tx, err := s.begin(noAudit(ctx), ordinary)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	session, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), now)
	if err != nil {
		return false, err
	}
	enable, err := contextEnableOf(ctx, tx.Tx, k)
	if err != nil {
		return false, err
	}
	eligible, _ := SelectContextTrigger(ContextTriggerInput{Active: session.State == "active", Enabled: enable.Enabled, Policy: policy,
		CycleAvailable: true, Evidence: evidence, Now: now, LatchedEpoch: latched})
	if !eligible {
		return false, nil
	}
	var pin struct {
		hostPID, hostStart int64
		socket, pane       string
	}
	if err := tx.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM context_enables WHERE family=? AND session_id=?`,
		k.Family, k.ID).Scan(&pin.hostPID, &pin.hostStart, &pin.socket, &pin.pane); err != nil {
		return false, err
	}
	var hostPID, hostStart int64
	var socket, pane string
	if err := tx.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`, k.Family, k.ID).
		Scan(&hostPID, &hostStart, &socket, &pane); err != nil {
		return false, err
	}
	// The Enable pinned a verified launched host and pane; the session must still have it.
	if hostPID <= 0 || hostStart <= 0 || pane == "" || hostPID != pin.hostPID || hostStart != pin.hostStart || socket != pin.socket || pane != pin.pane {
		return false, nil
	}
	current, err := cyclesWhere(ctx, tx, activeCycle+` AND ((family=? AND session_id=?) OR (host_pid=? AND host_start=?))`, k.Family, k.ID, hostPID, hostStart)
	if err != nil || len(current) > 0 {
		return false, err
	}
	var active int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM context_cycles WHERE `+activeCycle).Scan(&active); err != nil {
		return false, err
	}
	if active >= maxActiveCycles {
		return false, nil
	}
	c := cycleRow{ID: newCycleID(), Revision: 1, State: cycleWaitingReady, Family: k.Family, SessionID: k.ID, Peer: session.Name,
		Address: session.Address, HostPID: hostPID, HostStart: hostStart, Socket: socket, Pane: pane, EnableRevision: enable.Revision,
		Epoch: evidence.Epoch, Mode: policy.Mode, Threshold: policy.Threshold, Used: *evidence.UsedTokens, Limit: *evidence.LimitTokens,
		SourceAt: evidence.SourceAt, ConfirmedAt: evidence.ConfirmedAt, CreatedAt: now, UpdatedAt: now,
		StageDeadline: now + cycleReadyWait.Milliseconds(), CycleDeadline: now + cycleLifetime.Milliseconds(),
		Adapter: adapter.Version(), CatalogDigest: s.library.catalog.Digest, Credit: true}
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq`, k.Family, k.ID).Scan(&seq); err != nil {
		return false, tx.fail(err)
	}
	c.MessageSeq = seq
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
		created_at,delivery_updated_at,system_kind,cycle_id,cycle_enable_revision) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		k.Family, k.ID, seq, overwatchKey.Family, overwatchKey.ID, overwatchName, cycleRequestBody(c.ID, c.Revision), now, now,
		cycleRequestKind, c.ID, c.EnableRevision); err != nil {
		return false, tx.fail(err)
	}
	if err := insertCycle(ctx, tx, c); err != nil {
		return false, tx.fail(err)
	}
	if err := cycleEvent(ctx, tx, &c, now, cycleRequested, "admitted", ""); err != nil {
		return false, tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE context_cycles SET events=? WHERE cycle_id=?`, c.Events, c.ID); err != nil {
		return false, tx.fail(err)
	}
	if err := cycleAudit(ctx, tx, now, c, "requested", ""); err != nil {
		return false, tx.fail(err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.observed.mu.Lock()
	s.latches.mark(k, evidence.Epoch)
	s.observed.mu.Unlock()
	return true, nil
}

func insertCycle(ctx context.Context, tx *writeTx, c cycleRow) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO context_cycles(`+cycleColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Credit, c.Revision, c.State, c.PromptFrom, c.Reason, c.Family, c.SessionID, c.Peer, c.Address, c.HostPID, c.HostStart,
		c.Socket, c.Pane, c.EnableRevision, c.Epoch, c.Mode, c.Threshold, c.Used, c.Limit, c.SourceAt, c.ConfirmedAt, c.MessageSeq,
		c.CreatedAt, c.UpdatedAt, c.StageDeadline, c.CycleDeadline, c.Artifact, c.ArtifactDev, c.ArtifactIno, c.ArtifactSize,
		c.ArtifactMtime, c.Adapter, c.CatalogDigest, c.SuccessorID, c.SuccessorEpoch, c.Outcome, c.Report, c.Events, c.FinishedAt)
	return err
}

// cycleAudit writes the cycle's audit record. It names the peer, cycle and fixed codes; it
// never holds a native ID, path or report text.
func cycleAudit(ctx context.Context, tx *writeTx, now int64, c cycleRow, result, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit(at,action,target,result,reason) VALUES (?,'context-cycle',?,?,?)`,
		now, "cycle "+c.ID+" peer "+c.Peer, result, reason)
	return err
}

// advanceCycle moves an unfinished cycle to a non-terminal stage in an ordinary write,
// after checking that it is still at the expected revision and state. A stage that would
// take the event slot kept for the terminal outcome ends the cycle as failed instead.
func (s *Store) advanceCycle(ctx context.Context, id string, revision int64, from, to, reason string, change func(*cycleRow)) (cycleRow, error) {
	now := s.now().UnixMilli()
	tx, err := s.begin(noAudit(ctx), ordinary)
	if err != nil {
		return cycleRow{}, err
	}
	defer tx.Rollback()
	c, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil {
		return cycleRow{}, err
	}
	if c.Revision != revision || c.State != from {
		return c, ErrCycleChanged
	}
	if err := cycleEvent(ctx, tx, &c, now, to, "entered", reason); err != nil {
		if errors.Is(err, errCycleEventLimit) {
			tx.Rollback()
			return s.endCycle(ctx, id, revision, cycleFailed, "event_limit")
		}
		return c, tx.fail(err)
	}
	c.Revision++
	c.State, c.Reason, c.UpdatedAt = to, reason, now
	switch to {
	case cycleReady, cycleClearIntent, cycleResetVerified, cyclePickupIntent:
		c.StageDeadline = now + cycleReceiptWait.Milliseconds()
	case cycleClearReceived:
		c.StageDeadline = now + cycleReceiptWait.Milliseconds()
	case cycleWaitingPickup:
		// The successor's pickup report may take the rest of the cycle's lifetime.
		c.StageDeadline = c.CycleDeadline
	case cycleWaitingPrompt:
		c.StageDeadline = now + cyclePromptWait.Milliseconds()
	}
	if change != nil {
		change(&c)
	}
	if err := updateCycle(ctx, tx, c); err != nil {
		return c, tx.fail(err)
	}
	return c, tx.Commit()
}

func updateCycle(ctx context.Context, tx *writeTx, c cycleRow) error {
	_, err := tx.ExecContext(ctx, `UPDATE context_cycles SET credit=?,revision=?,state=?,prompt_from=?,reason=?,updated_at=?,
		stage_deadline=?,artifact=?,artifact_dev=?,artifact_ino=?,artifact_size=?,artifact_mtime=?,successor_id=?,successor_epoch=?,
		outcome=?,report=?,events=?,finished_at=? WHERE cycle_id=?`,
		c.Credit, c.Revision, c.State, c.PromptFrom, c.Reason, c.UpdatedAt, c.StageDeadline, c.Artifact, c.ArtifactDev, c.ArtifactIno,
		c.ArtifactSize, c.ArtifactMtime, c.SuccessorID, c.SuccessorEpoch, c.Outcome, c.Report, c.Events, c.FinishedAt, c.ID)
	return err
}

// endCycle records a cycle's one terminal outcome in a control write that spends the
// cycle's credit. Failed, cancelled and uncertain cycles end the session's Enable.
func (s *Store) endCycle(ctx context.Context, id string, revision int64, outcome, reason string) (cycleRow, error) {
	tx, err := s.begin(noAudit(ctx), control)
	if err != nil {
		return cycleRow{}, err
	}
	defer tx.Rollback()
	c, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil {
		return cycleRow{}, err
	}
	if cycleTerminal(c.State) || revision != 0 && c.Revision != revision {
		return c, ErrCycleChanged
	}
	if err := s.endCycleTx(ctx, tx, &c, outcome, reason, nil); err != nil {
		return c, tx.fail(err)
	}
	return c, tx.Commit()
}

// endCycleTx ends c inside tx. A terminal outcome of an intent state that is not proven
// becomes uncertain. complete carries the Enable to the successor through carry.
func (s *Store) endCycleTx(ctx context.Context, tx *writeTx, c *cycleRow, outcome, reason string, carry func() error) error {
	now := s.now().UnixMilli()
	if outcome == cycleCancelled && cycleIntent(c.State) {
		outcome, reason = cycleUncertain, "input_unreconciled"
	}
	if err := cycleEvent(ctx, tx, c, now, outcome, outcome, reason); err != nil {
		return err
	}
	c.Revision++
	c.State, c.Reason, c.Outcome, c.UpdatedAt, c.FinishedAt, c.Credit = outcome, reason, outcome, now, now, false
	if err := updateCycle(ctx, tx, *c); err != nil {
		return err
	}
	if outcome == cycleComplete {
		if carry != nil {
			if err := carry(); err != nil {
				return err
			}
		}
	} else if _, err := tx.ExecContext(ctx, `UPDATE context_enables SET enabled=0,revision=revision+1,updated_at=?
		WHERE family=? AND session_id=? AND enabled=1`, now, c.Family, c.SessionID); err != nil {
		return err
	}
	return cycleAudit(ctx, tx, now, *c, outcome, reason)
}

// fenceCyclesTx ends the unfinished cycles that where selects, inside a dashboard action's
// own transaction, as cancelled (uncertain with an unreconciled input).
func (s *Store) fenceCyclesTx(ctx context.Context, tx *writeTx, reason, where string, args ...any) error {
	cycles, err := cyclesWhere(ctx, tx, activeCycle+` AND (`+where+`)`, args...)
	if err != nil {
		return err
	}
	for i := range cycles {
		if err := s.endCycleTx(ctx, tx, &cycles[i], cycleCancelled, reason, nil); err != nil {
			return err
		}
	}
	return nil
}

// cycleOfCaller finds the caller's cycle by ID: as its requester, or as the managed
// successor in the requester's pinned host and pane that holds the same participant (or the
// requester itself under a new native epoch).
func (s *Store) cycleOfCaller(ctx context.Context, q querier, caller Key, id string, now int64) (cycleRow, string, error) {
	cycles, err := cyclesWhere(ctx, q, `cycle_id=? AND family=?`, id, caller.Family)
	if err != nil {
		return cycleRow{}, "", err
	}
	if len(cycles) == 0 {
		return cycleRow{}, "", ErrCycleNotFound
	}
	c := cycles[0]
	if c.SessionID == caller.ID {
		return c, "requester", nil
	}
	if c.SuccessorID == caller.ID {
		return c, "successor", nil
	}
	ok, err := s.successorOf(ctx, q, c, caller, now)
	if err != nil {
		return c, "", err
	}
	if !ok {
		return cycleRow{}, "", ErrCycleNotFound
	}
	return c, "successor", nil
}

// successorOf reports whether caller is a managed successor of c: an active session of the
// same family in the same host and pane that holds c's participant address.
func (s *Store) successorOf(ctx context.Context, q querier, c cycleRow, caller Key, now int64) (bool, error) {
	if c.State != cycleResetVerified && c.State != cyclePickupIntent && c.State != cycleWaitingPickup && c.State != cycleWaitingPrompt {
		return false, nil
	}
	session, err := scanSession(q.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, caller.Family, caller.ID), now)
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var hostPID, hostStart int64
	var socket, pane string
	if err := q.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`, caller.Family, caller.ID).
		Scan(&hostPID, &hostStart, &socket, &pane); err != nil {
		return false, err
	}
	return session.State == "active" && hostPID == c.HostPID && hostStart == c.HostStart && socket == c.Socket && pane == c.Pane &&
		c.Address != "" && session.Address == c.Address && session.HoldsAddress, nil
}

// ContextCycleStatus returns the caller's current cycle, or its newest retained one, as its
// requester or managed successor. It reads only and moves no cursor.
func (s *Store) ContextCycleStatus(ctx context.Context, caller Key) (*CycleView, error) {
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, now, caller); err != nil {
		return nil, err
	}
	// An unfinished cycle of the caller comes first: its own, then one whose managed
	// successor it is (found by pane and participant before it reports); else its newest.
	own := `family=? AND (session_id=? OR successor_id=?)`
	cycles, err := cyclesWhere(ctx, tx, activeCycle+` AND `+own+` ORDER BY created_at DESC LIMIT 1`, caller.Family, caller.ID, caller.ID)
	if err != nil {
		return nil, err
	}
	if len(cycles) == 0 {
		pending, err := cyclesWhere(ctx, tx, activeCycle+` AND family=? ORDER BY created_at DESC`, caller.Family)
		if err != nil {
			return nil, err
		}
		for _, c := range pending {
			ok, err := s.successorOf(ctx, tx, c, caller, now)
			if err != nil {
				return nil, err
			}
			if ok {
				cycles = []cycleRow{c}
				break
			}
		}
	}
	if len(cycles) == 0 {
		if cycles, err = cyclesWhere(ctx, tx, own+` ORDER BY created_at DESC LIMIT 1`, caller.Family, caller.ID, caller.ID); err != nil {
			return nil, err
		}
	}
	if len(cycles) == 0 {
		return nil, nil
	}
	role := "requester"
	if cycles[0].SessionID != caller.ID {
		role = "successor"
	}
	v, err := cycles[0].view(ctx, tx, role)
	return &v, err
}

// ContextCycleReady records the requester's report that its handoff artifact is written.
// epoch is the caller's verified current native epoch, attached by its native client.
// An identical retry of an accepted report returns the current state without a write.
func (s *Store) ContextCycleReady(ctx context.Context, caller Key, epoch, id string, revision int64, artifact string) (CycleView, error) {
	if id == "" || revision < 1 || !validArtifactName(artifact) {
		return CycleView{}, ErrInvalid
	}
	if epoch == "" || !nativeEpoch.MatchString(epoch) {
		return CycleView{}, ErrCycleEpoch
	}
	now := s.now().UnixMilli()
	if err := s.activeCaller(ctx, caller, now); err != nil {
		return CycleView{}, err
	}
	c, role, err := s.cycleOfCaller(ctx, s.db, caller, id, now)
	if err != nil {
		return CycleView{}, err
	}
	if role != "requester" {
		return CycleView{}, ErrCycleNotFound
	}
	if c.State == cycleReady && c.Revision == revision+1 && c.Artifact == artifact {
		return c.view(ctx, s.db, role)
	}
	if c.State != cycleWaitingReady || c.Revision != revision {
		v, _ := c.view(ctx, s.db, role)
		return v, ErrCycleChanged
	}
	if epoch != c.Epoch {
		return CycleView{}, ErrCycleEpoch
	}
	file, err := s.verifyArtifact(ctx, c, artifact)
	if err != nil {
		return CycleView{}, err
	}
	next, err := s.advanceCycle(ctx, id, revision, cycleWaitingReady, cycleReady, "", func(c *cycleRow) {
		c.Artifact, c.ArtifactDev, c.ArtifactIno, c.ArtifactSize, c.ArtifactMtime = artifact, file.dev, file.ino, file.size, file.mtime
	})
	if err != nil {
		v, _ := next.view(ctx, s.db, role)
		return v, err
	}
	return next.view(ctx, s.db, role)
}

// ContextCyclePickup records the managed successor's report that it restored the
// checkpoint from the cycle's artifact. It completes the cycle and carries the Enable to
// the successor. report is private runtime data, never audit text.
func (s *Store) ContextCyclePickup(ctx context.Context, caller Key, epoch, id string, revision int64, artifact, outcome, report string) (CycleView, error) {
	if id == "" || revision < 1 || !validArtifactName(artifact) || outcome != "resumed" && outcome != "paused" ||
		report == "" || len(report) > maxPickupReport || !utf8.ValidString(report) {
		return CycleView{}, ErrInvalid
	}
	if epoch == "" || !nativeEpoch.MatchString(epoch) {
		return CycleView{}, ErrCycleEpoch
	}
	now := s.now().UnixMilli()
	if err := s.activeCaller(ctx, caller, now); err != nil {
		return CycleView{}, err
	}
	c, role, err := s.cycleOfCaller(ctx, s.db, caller, id, now)
	if err != nil {
		return CycleView{}, err
	}
	if c.State == cycleComplete && c.Revision == revision+1 && c.SuccessorID == caller.ID && c.SuccessorEpoch == epoch && c.Artifact == artifact {
		return c.view(ctx, s.db, "successor")
	}
	if c.State != cycleWaitingPickup || c.Revision != revision || c.Artifact != artifact {
		v, _ := c.view(ctx, s.db, role)
		return v, ErrCycleChanged
	}
	// The successor runs under a new native epoch: a new session, or the requester's
	// retained caller after its proven reset.
	if epoch == c.Epoch || c.SuccessorEpoch != "" && epoch != c.SuccessorEpoch {
		return CycleView{}, ErrCycleEpoch
	}
	if _, err := s.verifyArtifact(ctx, c, artifact); err != nil {
		return CycleView{}, err
	}
	tx, err := s.begin(noAudit(ctx), control)
	if err != nil {
		return CycleView{}, err
	}
	defer tx.Rollback()
	current, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil {
		return CycleView{}, err
	}
	if current.Revision != revision || current.State != cycleWaitingPickup {
		return CycleView{}, ErrCycleChanged
	}
	if role == "successor" {
		ok, err := s.successorOf(ctx, tx, current, caller, now)
		if err != nil {
			return CycleView{}, err
		}
		if !ok {
			return CycleView{}, ErrCycleNotFound
		}
	}
	current.SuccessorID, current.SuccessorEpoch, current.Report = caller.ID, epoch, report
	carry := func() error {
		if caller.ID == current.SessionID {
			return nil // the retained caller keeps its own Enable
		}
		// The Enable moves to the proven successor, pinned to the same host and pane.
		_, err := tx.ExecContext(ctx, `INSERT INTO context_enables(family,session_id,enabled,revision,session_revision,address,host_pid,host_start,tmux_socket,tmux_pane,updated_at)
			SELECT s.family,s.id,1,1,s.revision,?,s.host_pid,s.host_start,s.tmux_socket,s.tmux_pane,? FROM sessions s WHERE s.family=? AND s.id=?
			ON CONFLICT(family,session_id) DO UPDATE SET enabled=1,revision=context_enables.revision+1,session_revision=excluded.session_revision,
			address=excluded.address,host_pid=excluded.host_pid,host_start=excluded.host_start,tmux_socket=excluded.tmux_socket,
			tmux_pane=excluded.tmux_pane,updated_at=excluded.updated_at`, current.Address, now, caller.Family, caller.ID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE context_enables SET enabled=0,revision=revision+1,updated_at=? WHERE family=? AND session_id=? AND enabled=1`,
			now, current.Family, current.SessionID)
		return err
	}
	if err := s.endCycleTx(ctx, tx, &current, cycleComplete, outcome, carry); err != nil {
		return CycleView{}, tx.fail(err)
	}
	if err := tx.Commit(); err != nil {
		return CycleView{}, err
	}
	return current.view(ctx, s.db, "successor")
}

func (s *Store) activeCaller(ctx context.Context, caller Key, now int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return active(ctx, tx, now, caller)
}

func validArtifactName(name string) bool {
	return name != "" && len(name) <= maxArtifactName && handoffName.MatchString(name) && !strings.Contains(name, "..")
}
