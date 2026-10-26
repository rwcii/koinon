package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
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
// Each stage is a durable state with a compared revision. Admission funds every remaining
// stage and the one terminal outcome that ends the cycle (docs/CONTEXT-CYCLE-STORAGE.md).

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
	cyclePromptIntent  = "prompt_intent"
	// The interactive pickup bootstrap of #196 chunk 03: its input intent and its proof. No
	// cycle enters them since #406; a cycle stored in one by an earlier binary ends uncertain
	// (bootstrap_intent) or goes on to its clear (bootstrap_verified).
	cycleBootstrapIntent   = "bootstrap_intent"
	cycleBootstrapVerified = "bootstrap_verified"
	cycleComplete          = "complete"
	cycleCancelled         = "cancelled"
	cycleFailed            = "failed"
	cycleUncertain         = "uncertain"
)

// Page credits of one cycle (docs/CONTEXT-CYCLE-STORAGE.md). Admission funds every remaining
// transition: each non-terminal stage is a control write that debits cycleStagePages (the
// cycle row rewrite, its event, its audit row and audit_at entry), and the terminal outcome
// (with two Enable rows and an Enable key) releases the rest. Every event slot but the
// admission's and the one kept for the terminal outcome is a funded stage.
const (
	cycleStagePages    = 280
	cycleTerminalPages = 491
	cycleFundedStages  = maxCycleEvents - 2
	cycleCreditPages   = cycleFundedStages*cycleStagePages + cycleTerminalPages
)

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
	maxCycleRefusals       = 8
)

// overwatchKey is the reserved system sender of managed-cycle requests. It is not the
// maintainer, holds no inbox and no session, and no native session can take its name.
var overwatchKey = Key{Family: "overwatch", ID: "overwatch"}

const overwatchName = "overwatch"

// cycleRequestKind is the system_kind of the typed managed-cycle request message.
const cycleRequestKind = "context_cycle_request"

// contextCyclesTableV23 is the cycle table as schema 23 published it; schema 24 rebuilds it
// with the bootstrap states.
const contextCyclesTableV23 = `CREATE TABLE context_cycles (
	cycle_id TEXT PRIMARY KEY, credit INTEGER NOT NULL CHECK (credit>=0), held INTEGER NOT NULL CHECK (held IN (0,1)),
	revision INTEGER NOT NULL CHECK (revision>0),
	state TEXT NOT NULL CHECK (state IN ('requested','waiting_ready','ready','clear_intent','clear_received',
		'reset_verified','pickup_intent','waiting_pickup','waiting_prompt','prompt_intent','complete','cancelled','failed','uncertain')),
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

const contextCyclesTable = `CREATE TABLE context_cycles (
	cycle_id TEXT PRIMARY KEY, credit INTEGER NOT NULL CHECK (credit>=0), held INTEGER NOT NULL CHECK (held IN (0,1)),
	revision INTEGER NOT NULL CHECK (revision>0),
	state TEXT NOT NULL CHECK (state IN ('requested','waiting_ready','ready','clear_intent','clear_received',
		'reset_verified','pickup_intent','waiting_pickup','waiting_prompt','prompt_intent','bootstrap_intent','bootstrap_verified','complete','cancelled',
		'failed','uncertain')),
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

// contextCycleRefusalsTable holds the refused ready reports of each cycle, the newest
// maxCycleRefusals, with the revision each one answered (#485). They take no event ordinal
// and no revision, so the stage history and its revisions stay as they are.
const contextCycleRefusalsTable = `CREATE TABLE context_cycle_refusals (
	cycle_id TEXT NOT NULL, ordinal INTEGER NOT NULL, revision INTEGER NOT NULL, at INTEGER NOT NULL,
	code TEXT NOT NULL, message TEXT NOT NULL,
	PRIMARY KEY (cycle_id, ordinal)) WITHOUT ROWID;`

// cycleTerminal reports whether a state ends a cycle.
func cycleTerminal(state string) bool {
	return state == cycleComplete || state == cycleCancelled || state == cycleFailed || state == cycleUncertain
}

// cycleHoldsPane reports the states in which the cycle owns its pane: ordinary pane
// wakes wait until it ends, so no notice text interleaves with cycle input.
func cycleHoldsPane(state string) bool {
	switch state {
	case cycleReady, cycleBootstrapIntent, cycleBootstrapVerified, cycleClearIntent, cycleClearReceived, cycleResetVerified,
		cyclePickupIntent, cycleWaitingPickup, cycleWaitingPrompt, cyclePromptIntent:
		return true
	}
	return false
}

// cycleIntent reports the states that recorded an external input whose receipt is not
// recorded yet: their result is unknown until the adapter proves it.
func cycleIntent(state string) bool {
	return state == cycleBootstrapIntent || state == cycleClearIntent || state == cyclePickupIntent || state == cyclePromptIntent
}

// cycleInputUnproven reports the states after an entered input whose effect is not proven:
// a recorded intent, and a received clear whose reset is not verified. A cycle that ends in
// one of them is uncertain, never failed or cancelled.
func cycleInputUnproven(state string) bool { return cycleIntent(state) || state == cycleClearReceived }

// CycleEvent is one durable stage change, with fixed codes and no captured text.
type CycleEvent struct {
	Ordinal int64  `json:"ordinal"`
	At      int64  `json:"at"`
	Stage   string `json:"stage"`
	Result  string `json:"result"`
	Reason  string `json:"reason,omitempty"`
}

// CycleRefusal is a refused ready report: the revision it answered, its time, and the
// refusal's code and text (#485).
type CycleRefusal struct {
	Revision int64  `json:"revision"`
	At       int64  `json:"at"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// CycleView is a cycle as its own session and the dashboard see it. It holds no native
// ID, epoch, host, pane, socket or report text.
type CycleView struct {
	ID            string `json:"cycle_id"`
	Revision      int64  `json:"revision"`
	State         string `json:"state"`
	PromptFrom    string `json:"prompt_from,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Peer          string `json:"peer"`
	Address       string `json:"address,omitempty"`
	Mode          string `json:"mode"`
	Threshold     int64  `json:"threshold"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
	StageDeadline int64  `json:"stage_deadline"`
	CycleDeadline int64  `json:"cycle_deadline"`
	Artifact      string `json:"artifact,omitempty"`
	ArtifactBytes int64  `json:"artifact_bytes,omitempty"`
	// CatalogDigest is the catalog the cycle pinned at admission: the pickup procedure that
	// library_read returns must carry the same digest.
	CatalogDigest string       `json:"catalog_digest,omitempty"`
	Outcome       string       `json:"outcome,omitempty"`
	ReportBytes   int          `json:"report_bytes,omitempty"`
	FinishedAt    int64        `json:"finished_at,omitempty"`
	Role          string       `json:"role"` // requester or successor, for the calling session
	Events        []CycleEvent `json:"events"`
	// Refusals are the refused ready reports, oldest first.
	Refusals []CycleRefusal `json:"ready_refusals"`
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
	Events, FinishedAt, Credit                                int64
	Held                                                      bool // the requester held Address at admission
}

const cycleColumns = `cycle_id,credit,held,revision,state,prompt_from,reason,family,session_id,peer,address,host_pid,host_start,
	tmux_socket,tmux_pane,enable_revision,epoch,mode,threshold,used_tokens,limit_tokens,source_at,confirmed_at,message_seq,
	created_at,updated_at,stage_deadline,cycle_deadline,artifact,artifact_dev,artifact_ino,artifact_size,artifact_mtime,
	adapter,catalog_digest,successor_id,successor_epoch,outcome,report,events,finished_at`

func scanCycle(row interface{ Scan(...any) error }) (cycleRow, error) {
	var c cycleRow
	err := row.Scan(&c.ID, &c.Credit, &c.Held, &c.Revision, &c.State, &c.PromptFrom, &c.Reason, &c.Family, &c.SessionID, &c.Peer, &c.Address,
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
		CatalogDigest: c.CatalogDigest,
		Outcome:       c.Outcome, ReportBytes: len(c.Report), FinishedAt: c.FinishedAt, Role: role, Events: []CycleEvent{},
		Refusals: []CycleRefusal{}}
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
	if err := rows.Err(); err != nil {
		return v, err
	}
	rows.Close()
	rows, err = q.QueryContext(ctx, `SELECT revision,at,code,message FROM context_cycle_refusals WHERE cycle_id=? ORDER BY ordinal`, c.ID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var r CycleRefusal
		if err := rows.Scan(&r.Revision, &r.At, &r.Code, &r.Message); err != nil {
			return v, err
		}
		v.Refusals = append(v.Refusals, r)
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
	ErrCycleNotFound      = Refusal{"cycle_not_found", "this session has no managed cycle with that ID"}
	ErrCycleChanged       = Refusal{"cycle_changed", "the cycle is not at that revision or stage; read its current state"}
	ErrCycleEpoch         = Refusal{"cycle_epoch_unverified", "the native client did not supply this session's verified current epoch"}
	ErrCycleDialogChanged = Refusal{"cycle_dialog_changed", "the pane no longer shows that dialog or option; read the current one"}
	ErrCycleArtifact      = Refusal{"cycle_artifact_invalid", "the handoff artifact is not a new, bounded, ignored and untracked file in this session's own handoff directory"}
)

// CycleTarget is what a cycle adapter acts on: the pinned native host and pane, and the
// pickup skill that the cycle enters after the reset.
type CycleTarget struct {
	CycleID   string
	Family    string
	SessionID string
	HostPID   int64
	HostStart int64
	Socket    string
	Pane      string
	Epoch     string // the pre-reset native epoch
	// SuccessorEpoch is the native epoch that the reset proved, from reset_verified on, or "".
	SuccessorEpoch string
	// Holds reports that the session holds a participant address, which a new native session
	// after the reset must take over to report the pickup (Codex).
	Holds bool
	// Pickup is the koinon-pickup skill that the installed file must equal (#406).
	Pickup PickupEntry
	// ReadyAt is when the session made its current ready report, in Unix milliseconds, while
	// the cycle is ready, else 0. Work after it, such as a background subagent's, is not in
	// the handoff. A resolved dialog returns the cycle to ready without changing it.
	ReadyAt int64
	tools   hostTools
}

// PickupEntry is the bytes and the menu description of one pickup entry.
type PickupEntry struct {
	Content     []byte
	Description string
}

func (c cycleRow) target() CycleTarget {
	return CycleTarget{CycleID: c.ID, Family: c.Family, SessionID: c.SessionID, HostPID: c.HostPID, HostStart: c.HostStart,
		Socket: c.Socket, Pane: c.Pane, Epoch: c.Epoch, SuccessorEpoch: c.SuccessorEpoch}
}

// PaneState is a cycle adapter's fresh inspection of the pinned pane.
type PaneState struct {
	Idle   bool   // an idle, empty input line, with no intervening agent
	Prompt string // a native dialog's fixed code, or ""
	// Seen is a fixed code of what kept the pane from input when it is not Idle, such as
	// status_busy or input_not_empty (#417).
	Seen string
}

// CycleProbe is an adapter's verdict on whether a session's cycle can run. Entry names the
// pickup skill that the cycle enters after the reset (koinon-pickup) when it can run.
type CycleProbe struct {
	Reason string // "" when the cycle can run, else a fixed unavailable reason
	Entry  string
	// Step names the action that removes Reason, such as the command that installs the pickup
	// skill, or "".
	Step string
}

// CycleFence rechecks, before each key an adapter sends, that the cycle is still at its
// revision and stage under the Enable that admitted it; an error stops the input.
type CycleFence func(context.Context) error

// CycleAdapter enters cycle input into one family's native session. Each input method
// proves its target and calls the fence before every key, and returns whether its input
// was received; the effect itself is proven separately (NewEpoch, the pickup report).
type CycleAdapter interface {
	Version() string
	Probe(ctx context.Context, t CycleTarget) CycleProbe
	Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error)
	Clear(ctx context.Context, t CycleTarget, fence CycleFence) (received bool, err error)
	// NewEpoch returns the native epoch that replaced t.Epoch in the same host and pane,
	// or "" while none is proven.
	NewEpoch(ctx context.Context, t CycleTarget) (string, error)
	// Pickup enters the koinon-pickup command after checking that it runs the installed skill.
	Pickup(ctx context.Context, t CycleTarget, fence CycleFence) (received bool, err error)
	// Answerable reports whether the adapter answers a dialog itself; any other dialog waits
	// for the native session or the maintainer.
	Answerable(prompt string) bool
	// Answer responds to an answerable dialog and reports whether the response was read back.
	Answer(ctx context.Context, t CycleTarget, prompt string, fence CycleFence) (bool, error)
	// Dialog reads, without input, the dialog the pane shows: its text, numbered options and
	// a digest of both, for the maintainer's response on the dashboard.
	Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error)
	// Choose enters the maintainer's option while the pane still shows the dialog with digest.
	Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error)
}

// CycleDialog is a native dialog as the pane shows it.
type CycleDialog struct {
	Lines   []string `json:"lines"`
	Options []string `json:"options"` // the option numbers
	Digest  string   `json:"digest"`
}

// cycleAdapters holds the registered adapters by family. Tests register synthetic ones.
var cycleAdapters = map[string]CycleAdapter{}

func cycleAdapterOf(family string) CycleAdapter { return cycleAdapters[family] }

// cycleCode bounds an adapter's version and prompt codes, which the cycle row and its events
// store (docs/CONTEXT-CYCLE-STORAGE.md).
var cycleCode = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func init() {
	// The Claude Code adapter (chunk 03) and the Codex adapter (chunk 04); Probe limits each
	// to evidenced platforms and versions.
	cycleAdapters["claude"] = claudeCycle{}
	cycleAdapters["codex"] = codexCycle{}
}

// contextCycleEnabledFor is the admission's cheap pre-check: an enabled session only; Probe
// runs after it, so ordinary observations of sessions without an Enable cost no probe.
func contextCycleEnabledFor(s *Store, ctx context.Context, k Key) bool {
	enables, err := s.contextEnables(ctx)
	return err == nil && enables[k].Enabled
}

// sessionProbe asks k's family adapter whether k's cycle can run in its pinned host and pane.
func (s *Store) sessionProbe(ctx context.Context, k Key) CycleProbe {
	adapter := cycleAdapterOf(k.Family)
	if adapter == nil {
		return CycleProbe{Reason: "context_cycle_unavailable"}
	}
	row := cycleRow{Family: k.Family, SessionID: k.ID}
	if err := s.db.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`, k.Family, k.ID).
		Scan(&row.HostPID, &row.HostStart, &row.Socket, &row.Pane); err != nil {
		return CycleProbe{Reason: "session_unknown"}
	}
	if row.HostPID <= 0 || row.Pane == "" {
		return CycleProbe{Reason: "host_unpinned"}
	}
	return adapter.Probe(ctx, s.cycleTarget(ctx, row))
}

// cycleRequestBody is the fixed text of the typed request. It names the cycle and asks the
// session to pause at a safe stopping point, write its own handoff, report ready and then
// stay idle, because the clear waits for its idle prompt (#431).
func cycleRequestBody(id string, revision int64, agent string, deadline int64) string {
	by := time.UnixMilli(deadline).UTC().Format("2006-01-02 15:04:05 UTC")
	return "overwatch: managed context cycle " + id + " revision " + strconv.FormatInt(revision, 10) +
		" requests your handoff. Report ready by " + by + ". Pause your work at the next safe stopping point. Let running background subagents finish, or stop them, so that the handoff holds their results; " +
		"stop any that will not finish with enough time left to update the handoff and report ready by " + by + ". Write your handoff " +
		"with your canonical handoff procedure into _handoff/" + agent + "/ of the main checkout (outside a Git checkout, of the directory " +
		"where this session started), or into the directory that the Handoff path fact of " + handoffFacts + " names, then call context_cycle_ready with this cycle ID, " +
		"its revision and the handoff file name. Then end your turn and stay idle: overwatch clears your context at your idle prompt, and asks again " +
		"if your pane does not become idle. See the Managed cycle reporting section of koinon guide."
}

// cycleNoticeSuffix names the unfinished managed cycle whose request is in a wake's range, so
// the notice does not read as one more routine inbox notice (#445). A range without one adds
// nothing.
func (s *Store) cycleNoticeSuffix(ctx context.Context, b wakeBatch) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT m.cycle_id FROM messages m JOIN context_cycles c ON c.cycle_id=m.cycle_id
		WHERE m.recipient_family=? AND m.recipient_id=? AND m.seq BETWEEN ? AND ? AND m.system_kind=? AND c.`+activeCycle+`
		ORDER BY m.seq DESC LIMIT 1`, b.Session.Family, b.Session.ID, b.First, b.Last, cycleRequestKind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return " Managed context-cycle request " + id + ".", nil
}

// cycleReadyNext is the instruction of a successful ready report (#431).
const cycleReadyNext = "Paused and ready: end your turn now and stay idle. Overwatch clears your context at " +
	"your idle prompt and then enters koinon-pickup."

// cycleRepeatable reports whether a ready cycle whose stage deadline passed asks its
// requester again (#431): the pane was read and was not at its idle prompt, such as a turn in
// progress, a draft, an unknown registry status or no input line found (pane_not_ready when
// the adapter named nothing). A pane that could not be read or proven, a Codex home or
// rollout that could not be read, and an idle pane keep the timeout.
func cycleRepeatable(seen string) bool {
	switch seen {
	case "", errTmuxUnavailable.Error(), "pane_unproven", "pane_in_mode", "pane_unreadable", "home_unknown", "rollout_unreadable":
		return false
	}
	return true
}

// cycleRequestedAt is when the cycle's current request was made: the newest repeated
// request (#431), recorded by its waiting_ready event, or the admission.
func cycleRequestedAt(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, c cycleRow) (int64, error) {
	var at sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT MAX(at) FROM context_cycle_events WHERE cycle_id=? AND stage=? AND reason LIKE 'not\_idle.%' ESCAPE '\'`,
		c.ID, cycleWaitingReady).Scan(&at)
	if err != nil || !at.Valid {
		return c.CreatedAt, err
	}
	return at.Int64, nil
}

// repeatCycleRequest asks a requester again whose pane did not become idle after its ready
// report (#431). In one ordinary transaction the cycle returns to waiting_ready under a new
// revision, drops the reported handoff, gets a new readiness deadline within its lifetime
// and puts a new overwatch request in the requester's inbox. The cycle's event and credit
// bounds stay: a repeat that would take the last event slot returns errCycleEventLimit.
func (s *Store) repeatCycleRequest(ctx context.Context, id string, revision int64, seen string) error {
	now := s.now().UnixMilli()
	tx, err := s.begin(noAudit(ctx), ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil {
		return err
	}
	if c.Revision != revision || c.State != cycleReady || now >= c.CycleDeadline {
		return ErrCycleChanged
	}
	if ok, err := cycleEnabled(ctx, tx, c); err != nil || !ok {
		if err == nil {
			err = ErrCycleChanged
		}
		return err
	}
	if ok, err := requesterPinned(ctx, tx, c, now); err != nil || !ok {
		if err == nil {
			err = ErrCycleChanged
		}
		return err
	}
	if c.Credit < cycleStagePages+cycleTerminalPages {
		return errCycleEventLimit
	}
	reason := "not_idle." + seen
	if err := cycleEvent(ctx, tx, &c, now, cycleWaitingReady, "entered", reason); err != nil {
		return err
	}
	c.Revision++
	c.Credit -= cycleStagePages
	c.State, c.Reason, c.UpdatedAt = cycleWaitingReady, reason, now
	c.StageDeadline = min(now+cycleReadyWait.Milliseconds(), c.CycleDeadline)
	c.Artifact, c.ArtifactDev, c.ArtifactIno, c.ArtifactSize, c.ArtifactMtime = "", 0, 0, 0, 0
	var seq int64
	var role string
	if err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq,role`, c.Family, c.SessionID).Scan(&seq, &role); err != nil {
		return tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
		created_at,delivery_updated_at,system_kind,cycle_id,cycle_enable_revision) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Family, c.SessionID, seq, overwatchKey.Family, overwatchKey.ID, overwatchName, cycleRequestBody(c.ID, c.Revision, cycleAgent(c.Family, role), c.StageDeadline), now, now,
		cycleRequestKind, c.ID, c.EnableRevision); err != nil {
		return tx.fail(err)
	}
	if err := updateCycle(ctx, tx, c); err != nil {
		return tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE context_cycles SET message_seq=? WHERE cycle_id=?`, seq, c.ID); err != nil {
		return tx.fail(err)
	}
	if err := cycleAudit(ctx, tx, now, c, cycleWaitingReady, reason); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// admitContextCycle admits a cycle for one session when its context crossed its threshold.
// It writes the cycle, its first event, the typed request and the audit record in one
// ordinary transaction; a refusal stores nothing. It reports whether a cycle was admitted.
func (s *Store) admitContextCycle(ctx context.Context, k Key) (bool, error) {
	adapter := cycleAdapterOf(k.Family)
	if adapter == nil || !cycleCode.MatchString(adapter.Version()) || !contextCycleEnabledFor(s, ctx, k) {
		return false, nil
	}
	// Most observations select nothing: check with reads before entering a write.
	enables, err := s.contextEnables(ctx)
	if err != nil || !enables[k].Enabled {
		return false, err
	}
	policy := s.sessionContextPolicy(k.Family, enables[k])
	evidence := s.contextEvidence(k)
	s.observed.mu.Lock()
	latched := s.latches.latched(k, evidence, policy.Threshold)
	s.observed.mu.Unlock()
	now := s.now().UnixMilli()
	conflict, err := overwatchNameHeld(ctx, s.db)
	if err != nil {
		return false, err
	}
	if eligible, _ := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true,
		OverwatchConflict: conflict, Evidence: evidence, Now: now, LatchedEpoch: latched}); !eligible {
		return false, nil
	}
	// The adapter's checks of the host, pane and pickup entry run before the write.
	if contextCycleProbe(s, ctx, k).Reason != "" {
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
	if conflict, err = overwatchNameHeld(ctx, tx); err != nil {
		return false, err
	}
	// The policy may have changed since the read above (the session's own threshold, #391):
	// the selection and the cycle's recorded snapshot both use the policy in force now.
	policy = s.sessionContextPolicy(k.Family, enable)
	eligible, _ := SelectContextTrigger(ContextTriggerInput{Active: session.State == "active", Enabled: enable.Enabled, Policy: policy,
		CycleAvailable: true, OverwatchConflict: conflict, Evidence: evidence, Now: now, LatchedEpoch: latched})
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
		Address: session.Address, Held: session.HoldsAddress, HostPID: hostPID, HostStart: hostStart, Socket: socket, Pane: pane, EnableRevision: enable.Revision,
		Epoch: evidence.Epoch, Mode: policy.Mode, Threshold: policy.Threshold, Used: *evidence.UsedTokens, Limit: *evidence.LimitTokens,
		SourceAt: evidence.SourceAt, ConfirmedAt: evidence.ConfirmedAt, CreatedAt: now, UpdatedAt: now,
		StageDeadline: now + cycleReadyWait.Milliseconds(), CycleDeadline: now + cycleLifetime.Milliseconds(),
		Adapter: adapter.Version(), CatalogDigest: s.library.catalog.Digest, Credit: cycleCreditPages}
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=? RETURNING last_seq`, k.Family, k.ID).Scan(&seq); err != nil {
		return false, tx.fail(err)
	}
	c.MessageSeq = seq
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
		created_at,delivery_updated_at,system_kind,cycle_id,cycle_enable_revision) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		k.Family, k.ID, seq, overwatchKey.Family, overwatchKey.ID, overwatchName, cycleRequestBody(c.ID, c.Revision, cycleAgent(k.Family, session.Role), c.StageDeadline), now, now,
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
	_, err := tx.ExecContext(ctx, `INSERT INTO context_cycles(`+cycleColumns+`,ready_refusal) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Credit, c.Held, c.Revision, c.State, c.PromptFrom, c.Reason, c.Family, c.SessionID, c.Peer, c.Address, c.HostPID, c.HostStart,
		c.Socket, c.Pane, c.EnableRevision, c.Epoch, c.Mode, c.Threshold, c.Used, c.Limit, c.SourceAt, c.ConfirmedAt, c.MessageSeq,
		c.CreatedAt, c.UpdatedAt, c.StageDeadline, c.CycleDeadline, c.Artifact, c.ArtifactDev, c.ArtifactIno, c.ArtifactSize,
		c.ArtifactMtime, c.Adapter, c.CatalogDigest, c.SuccessorID, c.SuccessorEpoch, c.Outcome, c.Report, c.Events, c.FinishedAt,
		refusalSlot(0, ""))
	return err
}

// cycleAudit writes the cycle's audit record. It names the peer, cycle and fixed codes; it
// never holds a native ID, path or report text.
func cycleAudit(ctx context.Context, tx *writeTx, now int64, c cycleRow, result, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit(at,action,target,result,reason) VALUES (?,'context-cycle',?,?,?)`,
		now, "cycle "+c.ID+" peer "+c.Peer, result, reason)
	return err
}

// advanceCycle moves an unfinished cycle to a non-terminal stage in a control write that
// debits cycleStagePages from the cycle's credit. In its transaction it checks that the
// cycle is still at the expected revision and stage, before its stage and cycle deadlines,
// with the Enable that admitted it, and runs guard for the caller's own checks. A stage that
// would take the event slot kept for the terminal outcome ends the cycle as failed instead.
func (s *Store) advanceCycle(ctx context.Context, id string, revision int64, from, to, reason string,
	guard func(tx *writeTx, c cycleRow, now int64) error, change func(*cycleRow)) (cycleRow, error) {
	now := s.now().UnixMilli()
	// A maintainer's dashboard response carries its action's audit record in this transaction.
	actx, audited := noAudit(ctx), ctx.Value(cycleActionKey{}) != nil
	if audited {
		actx = ctx
	}
	tx, err := s.begin(actx, control)
	if err != nil {
		return cycleRow{}, err
	}
	defer tx.Rollback()
	tx.audited = audited
	c, err := scanCycle(tx.QueryRowContext(ctx, `SELECT `+cycleColumns+` FROM context_cycles WHERE cycle_id=?`, id))
	if err != nil {
		return cycleRow{}, err
	}
	if c.Revision != revision || c.State != from || now >= c.StageDeadline || now >= c.CycleDeadline {
		return c, ErrCycleChanged
	}
	if ok, err := cycleEnabled(ctx, tx, c); err != nil || !ok {
		if err == nil {
			err = ErrCycleChanged
		}
		return c, err
	}
	if guard != nil {
		if err := guard(tx, c, now); err != nil {
			return c, err
		}
	}
	if c.Credit < cycleStagePages+cycleTerminalPages {
		tx.Rollback()
		return s.endCycle(ctx, id, revision, cycleFailed, "event_limit")
	}
	if err := cycleEvent(ctx, tx, &c, now, to, "entered", reason); err != nil {
		if errors.Is(err, errCycleEventLimit) {
			tx.Rollback()
			return s.endCycle(ctx, id, revision, cycleFailed, "event_limit")
		}
		return c, tx.fail(err)
	}
	c.Revision++
	c.Credit -= cycleStagePages
	c.State, c.Reason, c.UpdatedAt = to, reason, now
	switch to {
	case cycleWaitingReady:
		// A return to readiness keeps the current request's readiness deadline: the first
		// request's, or that of the newest repeated request (#431).
		requested, err := cycleRequestedAt(ctx, tx, c)
		if err != nil {
			return c, tx.fail(err)
		}
		c.StageDeadline = requested + cycleReadyWait.Milliseconds()
	case cycleWaitingPrompt:
		c.StageDeadline = now + cyclePromptWait.Milliseconds()
	default:
		c.StageDeadline = now + cycleReceiptWait.Milliseconds()
	}
	c.StageDeadline = min(c.StageDeadline, c.CycleDeadline)
	if change != nil {
		change(&c)
	}
	if err := updateCycle(ctx, tx, c); err != nil {
		return c, tx.fail(err)
	}
	if err := cycleAudit(ctx, tx, now, c, to, reason); err != nil {
		return c, tx.fail(err)
	}
	return c, tx.Commit()
}

// cycleEnabled reports whether the requester's Enable is still the one that admitted c.
func cycleEnabled(ctx context.Context, q querier, c cycleRow) (bool, error) {
	var enabled bool
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT enabled,revision FROM context_enables WHERE family=? AND session_id=?`, c.Family, c.SessionID).
		Scan(&enabled, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && enabled && revision == c.EnableRevision, err
}

// requesterPinned reports whether c's requester is still active in its pinned host and pane,
// with the same participant address, and still holding it if it held it at admission.
func requesterPinned(ctx context.Context, q querier, c cycleRow, now int64) (bool, error) {
	session, err := scanSession(q.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, c.Family, c.SessionID), now)
	if errors.Is(err, ErrMissing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var hostPID, hostStart int64
	var socket, pane string
	if err := q.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`, c.Family, c.SessionID).
		Scan(&hostPID, &hostStart, &socket, &pane); err != nil {
		return false, err
	}
	return session.State == "active" && hostPID == c.HostPID && hostStart == c.HostStart && socket == c.Socket && pane == c.Pane &&
		session.Address == c.Address && (!c.Held || session.HoldsAddress), nil
}

// pinnedRequester is the advanceCycle guard of a requester's own report and of the clear
// that acts on its pane.
func pinnedRequester(ctx context.Context) func(*writeTx, cycleRow, int64) error {
	return func(tx *writeTx, c cycleRow, now int64) error {
		ok, err := requesterPinned(ctx, tx, c, now)
		if err == nil && !ok {
			err = ErrCycleChanged
		}
		return err
	}
}

// overwatchNameHeld reports whether a retained name record (a peer name, alias or
// participant address) is named overwatch, the reserved sender of the cycle request.
func overwatchNameHeld(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (bool, error) {
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM names WHERE name=?)`, overwatchName).Scan(&held)
	return held, err
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

// endCycleTx ends c inside tx and releases its credit. A cycle that ends after an input
// whose effect is not proven is uncertain. complete carries the Enable to the successor
// through carry.
func (s *Store) endCycleTx(ctx context.Context, tx *writeTx, c *cycleRow, outcome, reason string, carry func() error) error {
	now := s.now().UnixMilli()
	if outcome != cycleComplete && cycleInputUnproven(c.State) {
		outcome = cycleUncertain
	}
	if err := cycleEvent(ctx, tx, c, now, outcome, outcome, reason); err != nil {
		return err
	}
	c.Revision++
	c.State, c.Reason, c.Outcome, c.UpdatedAt, c.FinishedAt, c.Credit = outcome, reason, outcome, now, now, 0
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
	switch c.State {
	case cycleResetVerified, cyclePickupIntent, cycleWaitingPickup, cycleWaitingPrompt, cyclePromptIntent:
	default:
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
// An identical retry of an accepted report returns the current state without a write while
// the artifact is unchanged.
func (s *Store) ContextCycleReady(ctx context.Context, caller Key, epoch, id string, revision int64, artifact string) (CycleView, error) {
	if id == "" || revision < 1 || !validArtifactName(artifact) {
		return CycleView{}, ErrInvalid
	}
	if epoch == "" || !nativeEpoch.MatchString(epoch) {
		return CycleView{}, ErrCycleEpoch
	}
	// The ready report holds wake.mu, as each wake submission does: a wake that passed the
	// pane hold before the report ends before ready commits, and none starts after it (#417).
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
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
	if c.State == cycleReady && c.Revision == revision+1 && c.Artifact == artifact && epoch == c.Epoch {
		// The recorded file must still be the one that was reported.
		if _, err := s.verifyArtifact(ctx, c, artifact); err != nil {
			return CycleView{}, err
		}
		return c.view(ctx, s.db, role)
	}
	if c.State != cycleWaitingReady || c.Revision != revision {
		v, _ := c.view(ctx, s.db, role)
		return v, ErrCycleChanged
	}
	if epoch != c.Epoch {
		s.recordReadyRefusal(ctx, c, ErrCycleEpoch)
		return CycleView{}, ErrCycleEpoch
	}
	file, err := s.verifyArtifact(ctx, c, artifact)
	if err != nil {
		s.recordReadyRefusal(ctx, c, err)
		return CycleView{}, err
	}
	next, err := s.advanceCycle(ctx, id, revision, cycleWaitingReady, cycleReady, "", pinnedRequester(ctx), func(c *cycleRow) {
		c.Artifact, c.ArtifactDev, c.ArtifactIno, c.ArtifactSize, c.ArtifactMtime = artifact, file.dev, file.ino, file.size, file.mtime
	})
	if err != nil {
		v, _ := next.view(ctx, s.db, role)
		return v, err
	}
	// A new ready stage starts without an earlier stage's pane observation (#431).
	s.paneSeen.forget(id)
	return next.view(ctx, s.db, role)
}

// refusalSlotWidth is the fixed width of a cycle's ready_refusal slot: a 20-digit revision,
// a space and the refusal code padded to 75 bytes. Admission writes the empty slot, so a
// refusal overwrites it in place with a row of the same size, which allocates no page.
const refusalSlotWidth = 96

// refusalSlot is the slot text for a refusal code at a revision; revision 0 is empty.
func refusalSlot(revision int64, code string) string {
	return fmt.Sprintf("%020d %-75s", revision, code)
}

// parseRefusalSlot reads a slot; an empty or malformed one is revision 0.
func parseRefusalSlot(slot string) (int64, string) {
	fields := strings.Fields(slot)
	if len(slot) != refusalSlotWidth || len(fields) != 2 {
		return 0, ""
	}
	revision, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, ""
	}
	return revision, fields[1]
}

// recordReadyRefusal records a refused ready report on c while c still waits for ready at
// the same revision (#485). The code goes in the cycle's slot, in a control write that
// overwrites it in place, so the timeout can name it even when ordinary storage is full. The
// text joins the history, the newest maxCycleRefusals, as an ordinary write: a full store
// keeps no text and the refusal is returned all the same.
func (s *Store) recordReadyRefusal(ctx context.Context, c cycleRow, err error) {
	var refusal Refusal
	if !errors.As(err, &refusal) {
		return
	}
	s.writeRefusalSlot(ctx, c, refusal.Code)
	// The text is kept as a client receives it: a refusal past the bound keeps its code only.
	if len(refusal.Message) > MaxRefusalMessage || !utf8.ValidString(refusal.Message) {
		refusal.Message = ""
	}
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT MAX(ordinal) FROM context_cycle_refusals WHERE cycle_id=?),0)+1
		FROM context_cycles WHERE cycle_id=? AND state=? AND revision=?`, c.ID, c.ID, cycleWaitingReady, c.Revision).Scan(&ordinal); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO context_cycle_refusals(cycle_id,ordinal,revision,at,code,message) VALUES (?,?,?,?,?,?)`,
		c.ID, ordinal, c.Revision, s.now().UnixMilli(), refusal.Code, refusal.Message); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM context_cycle_refusals WHERE cycle_id=? AND ordinal<=?`, c.ID, ordinal-maxCycleRefusals); err != nil {
		return
	}
	tx.Commit()
}

// writeRefusalSlot overwrites c's slot with code at c's revision, while c waits for ready at
// that revision and has a slot of the fixed width.
func (s *Store) writeRefusalSlot(ctx context.Context, c cycleRow, code string) {
	if len(code) > 75 || strings.ContainsAny(code, " \t\r\n") {
		return
	}
	tx, err := s.begin(noAudit(ctx), control)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE context_cycles SET ready_refusal=? WHERE cycle_id=? AND state=? AND revision=?
		AND length(ready_refusal)=?`, refusalSlot(c.Revision, code), c.ID, cycleWaitingReady, c.Revision, refusalSlotWidth); err != nil {
		return
	}
	tx.Commit()
}

// readyRefusalCode is the code of the newest refused ready report at c's revision, from the
// cycle's slot, or "".
func readyRefusalCode(ctx context.Context, q querier, c cycleRow) (string, error) {
	var slot string
	if err := q.QueryRowContext(ctx, `SELECT ready_refusal FROM context_cycles WHERE cycle_id=?`, c.ID).Scan(&slot); err != nil {
		return "", err
	}
	if revision, code := parseRefusalSlot(slot); revision == c.Revision {
		return code, nil
	}
	return "", nil
}

// pickupRevision reports whether revision is that of one of the cycle's waiting_pickup
// stages: each stage adds one revision and one event, so it is that event's ordinal.
func pickupRevision(ctx context.Context, q querier, id string, revision int64) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM context_cycle_events WHERE cycle_id=? AND stage=? AND ordinal=?`, id, cycleWaitingPickup, revision).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// awaitingPickup reports whether c waits for the successor's pickup report: in waiting_pickup,
// or in a dialog wait that began there, because the approved call can be the report itself.
func awaitingPickup(c cycleRow) bool {
	return c.State == cycleWaitingPickup ||
		(c.State == cycleWaitingPrompt || c.State == cyclePromptIntent) && c.PromptFrom == cycleWaitingPickup
}

// ContextCyclePickup records the managed successor's report that it restored the
// checkpoint from the cycle's artifact. It completes the cycle and carries the Enable to
// the successor. report is private runtime data, never audit text. Only a retry with the
// same successor, epoch, artifact, outcome and report returns the completed cycle.
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
	// The report names the revision of a waiting_pickup stage that the successor read: a
	// dialog wait from it, such as the approval of this report, and its return change the
	// cycle's revision, and the approved call can arrive after either.
	read, err := pickupRevision(ctx, s.db, id, revision)
	if err != nil {
		return CycleView{}, err
	}
	if c.State == cycleComplete && (c.Revision == revision+1 || read) && c.SuccessorID == caller.ID {
		v, err := c.view(ctx, s.db, "successor")
		if err == nil && (c.SuccessorEpoch != epoch || c.Artifact != artifact || c.Reason != outcome || c.Report != report) {
			err = ErrCycleChanged
		}
		return v, err
	}
	if !awaitingPickup(c) || !read || c.Artifact != artifact {
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
	if s.pickupWrite != nil {
		s.pickupWrite()
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
	// The answer to a dialog that approved this report can record the return to
	// waiting_pickup after the read above (#389): the report applies while the cycle still
	// awaits it with the same artifact and epochs, at any revision.
	if !awaitingPickup(current) || current.Artifact != artifact || current.Epoch != c.Epoch || current.SuccessorEpoch != c.SuccessorEpoch ||
		now >= current.StageDeadline || now >= current.CycleDeadline {
		return CycleView{}, ErrCycleChanged
	}
	// The caller must still be the managed successor in the pinned pane, or the requester
	// itself still pinned there, under the Enable that admitted the cycle.
	pinned, err := cycleEnabled(ctx, tx, current)
	if err == nil && pinned {
		if role == "successor" {
			pinned, err = s.successorOf(ctx, tx, current, caller, now)
		} else {
			pinned, err = requesterPinned(ctx, tx, current, now)
		}
	}
	if err != nil {
		return CycleView{}, err
	}
	if !pinned {
		return CycleView{}, ErrCycleChanged
	}
	current.SuccessorID, current.SuccessorEpoch, current.Report = caller.ID, epoch, report
	carry := func() error {
		if caller.ID == current.SessionID {
			return nil // the retained caller keeps its own Enable
		}
		// The Enable moves to the proven successor, pinned to the same host and pane.
		// The requester's own threshold (#391) moves with it.
		_, err := tx.ExecContext(ctx, `INSERT INTO context_enables(family,session_id,enabled,revision,session_revision,address,host_pid,host_start,tmux_socket,tmux_pane,updated_at,threshold)
			SELECT s.family,s.id,1,1,s.revision,?,s.host_pid,s.host_start,s.tmux_socket,s.tmux_pane,?,
			COALESCE((SELECT threshold FROM context_enables WHERE family=? AND session_id=?),0) FROM sessions s WHERE s.family=? AND s.id=?
			ON CONFLICT(family,session_id) DO UPDATE SET enabled=1,revision=context_enables.revision+1,session_revision=excluded.session_revision,
			address=excluded.address,host_pid=excluded.host_pid,host_start=excluded.host_start,tmux_socket=excluded.tmux_socket,
			tmux_pane=excluded.tmux_pane,updated_at=excluded.updated_at,threshold=excluded.threshold`, current.Address, now,
			current.Family, current.SessionID, caller.Family, caller.ID)
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
