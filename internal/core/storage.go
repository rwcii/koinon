package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Refusal is a typed refusal with a stable code; its message names no private content.
type Refusal struct {
	Code    string
	Message string
}

func (r Refusal) Error() string { return r.Code + ": " + r.Message }

var (
	ErrCapacity       = Refusal{"capacity", "storage capacity reached; nothing was written and stored data is intact"}
	ErrStorageBlocked = Refusal{"storage_blocked", "the write-ahead log could not be proven empty; writes wait for koinon recover"}
	// ErrNotLaunched refuses a session that its launcher did not start: a direct start is
	// islanded. ErrLaunchPending refuses a background job before its job ID is recorded.
	ErrNotLaunched   = Refusal{"not_launched", "start this agent with koinon <family>; a direct start is not registered"}
	ErrLaunchPending = Refusal{"launch_pending", "the background launch has not recorded its job yet; call again"}
	// ErrStaleHolder refuses a call that acts for a participant from a session that does
	// not hold it, such as a former holder after a change of holder.
	ErrStaleHolder = Refusal{"stale_holder", "this session does not hold that participant; another session holds it now"}
)

// The daemon-wide physical bound (docs/PARITY-MEMORY-DESIGN.md, "Storage bound"), for the one
// state database: a write first proves the write-ahead log empty, so the log never holds
// more than one transaction, and the database and that log stay within maxPhysicalBytes.
const (
	pageSize         = 4096
	frameBytes       = 24 + pageSize
	maxSectorSize    = 0x10000
	padFrames        = (maxSectorSize - 1 + frameBytes - 1) / frameBytes
	maxPhysicalBytes = 1 << 30
	defaultMaxPages  = (maxPhysicalBytes - 32 - padFrames*frameBytes) / (pageSize + frameBytes)
	reservePages     = 2048
	commitSlack      = 2
	appendAllowance  = 8
)

type writeClass int

const (
	// ordinary writes add records and may not use the reserve.
	ordinary writeClass = iota
	// control writes record progress, withdrawals and reclamation, and may use the reserve.
	control
)

type storage struct {
	mu       sync.Mutex // one write at a time, from the log proof through the commit
	path     string
	maxPages int64
	blocked  string
}

func (s *Store) ceiling(class writeClass) int64 {
	if class == control {
		return s.storage.maxPages - commitSlack
	}
	return s.storage.maxPages - reservePages - commitSlack
}

// Page credits of one claim bundle (docs/WORK-ITEMS-GO-STORAGE.md): what one overdue
// or expiry control and one release or finish control can allocate.
const (
	overdueCreditPages = 576
	endCreditPages     = 768
)

// debtPages is the page debt of every store's funded work obligations, read from the
// durable credit flags. Every write class keeps it free, so a promised control can end.
func debtPages(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var pages int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(overdue_credit),0)*?+COALESCE(SUM(end_credit),0)*? FROM claim_bundles`,
		overdueCreditPages, endCreditPages).Scan(&pages)
	return pages, err
}

// resetLog proves the write-ahead log empty: a truncating checkpoint that reports nothing
// busy and nothing left, and a log file that is absent or empty. Only absence counts as
// absent; any other failure is a failed proof.
func (s *Store) resetLog(ctx context.Context) error {
	var busy, log, done int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done); err != nil {
		return err
	}
	if busy != 0 || log > 0 {
		return fmt.Errorf("checkpoint left busy=%d log=%d", busy, log)
	}
	info, err := os.Stat(s.storage.path + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() != 0 {
		return fmt.Errorf("log holds %d bytes", info.Size())
	}
	return nil
}

// proofFailed records a failed log proof as a block. A proof that ended because the
// request's context ended proved nothing about the log and wrote nothing: only that request
// is refused, and the next write proves the log again.
func (s *Store) proofFailed(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.storage.blocked = err.Error()
	return ErrStorageBlocked
}

func (s *Store) pages(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var pages int64
	err := q.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages)
	return pages, err
}

// writeTx is one write transaction inside the storage boundary. Commit enforces the page
// ceiling inside the transaction; Commit or Rollback releases the boundary once.
type writeTx struct {
	*sql.Tx
	s     *Store
	ctx   context.Context
	class writeClass
	done  bool
	after []func()
	// audited marks the transaction of a dashboard action's own change; only it carries the
	// action's audit record, never a maintenance write that runs first under the same context.
	audited bool
}

// begin enters the storage boundary: it refuses writes while blocked, proves the log
// empty, admits an ordinary write only below its ceiling (reclaiming once first), and
// starts the transaction.
func (s *Store) begin(ctx context.Context, class writeClass) (*writeTx, error) {
	s.storage.mu.Lock()
	// Refuse a stale participant before admission can reclaim or maintenance can write.
	if err := s.checkParticipantCall(ctx); err != nil {
		s.storage.mu.Unlock()
		return nil, err
	}
	tx, err := s.enter(ctx, class)
	if err != nil {
		s.storage.mu.Unlock()
		return nil, err
	}
	if m, guarded := ctx.Value(participantCallKey{}).(MemoryCaller); guarded {
		if err := s.requireParticipantCaller(ctx, tx, m); err != nil {
			tx.Rollback()
			s.storage.mu.Unlock()
			return nil, err
		}
	}
	return &writeTx{Tx: tx, s: s, ctx: ctx, class: class}, nil
}

func (s *Store) enter(ctx context.Context, class writeClass) (*sql.Tx, error) {
	if s.storage.blocked != "" {
		return nil, ErrStorageBlocked
	}
	if err := s.resetLog(ctx); err != nil {
		return nil, s.proofFailed(ctx, err)
	}
	if class == ordinary {
		pages, err := s.pages(ctx, s.db)
		if err != nil {
			return nil, err
		}
		debt, err := debtPages(ctx, s.db)
		if err != nil {
			return nil, err
		}
		if pages > s.ceiling(ordinary)-debt-appendAllowance {
			if err := s.reclaim(ctx); err != nil {
				return nil, err
			}
			if pages, err = s.pages(ctx, s.db); err != nil {
				return nil, err
			}
			if pages > s.ceiling(ordinary)-debt-appendAllowance {
				return nil, ErrCapacity
			}
		}
	}
	return s.db.BeginTx(ctx, nil)
}

// reclaim returns free pages to the file system. It runs inside the boundary, when an
// ordinary write is first refused; expired records were removed at the request boundary.
func (s *Store) reclaim(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return err
	}
	if err := s.resetLog(ctx); err != nil {
		return s.proofFailed(ctx, err)
	}
	return nil
}

// onCommit runs f after a successful commit, outside the boundary.
func (t *writeTx) onCommit(f func()) { t.after = append(t.after, f) }

func (t *writeTx) Commit() error {
	if t.done {
		return sql.ErrTxDone
	}
	t.done = true
	// A dashboard action's audit record joins the transaction that makes its change.
	audit, err := t.writeAudit()
	// The debt is read after the mutation: a work control has cleared the credit it
	// spends, and a start has added the credits it promises.
	var pages, debt int64
	if err == nil {
		pages, err = t.s.pages(t.ctx, t.Tx)
	}
	if err == nil {
		debt, err = debtPages(t.ctx, t.Tx)
	}
	if err == nil && pages > t.s.ceiling(t.class)-debt {
		err = ErrCapacity
	}
	if err == nil {
		err = t.Tx.Commit()
	}
	if err != nil {
		t.Tx.Rollback()
		// Classify while still inside the boundary, so no writer or recovery runs between
		// the rollback and a recorded block.
		err = t.s.full(err, t.class)
		t.s.storage.mu.Unlock()
		return err
	}
	t.s.storage.mu.Unlock()
	if audit != nil {
		audit.written = true
	}
	// Hooks run outside the boundary; they may read status or start other writes.
	for _, f := range t.after {
		f()
	}
	return nil
}

func (t *writeTx) Rollback() error {
	if t.done {
		return nil
	}
	t.done = true
	defer t.s.storage.mu.Unlock()
	return t.Tx.Rollback()
}

// full turns an engine-full error into a refusal. The reserve covers every control write,
// so a control write that still hits the engine ceiling means the bound no longer holds:
// writes stop until recovery.
func (s *Store) full(err error, class writeClass) error {
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code()&0xff != 13 { // SQLITE_FULL
		return err
	}
	if class == control {
		s.storage.blocked = "the engine ceiling refused a reserved write"
		return ErrStorageBlocked
	}
	return ErrCapacity
}

// fail wraps an error from a write body: the transaction rolls back and an engine-full
// error becomes a refusal.
func (t *writeTx) fail(err error) error {
	if !t.done {
		err = t.s.full(err, t.class)
	}
	t.Rollback()
	return err
}

// Recover proves the log empty again, returns free pages, and clears a blocked state.
func (s *Store) Recover(ctx context.Context) (StorageStatus, error) {
	s.storage.mu.Lock()
	err := s.recoverLocked(ctx)
	s.storage.mu.Unlock()
	if err != nil {
		return StorageStatus{}, err
	}
	return s.StorageStatus(ctx)
}

func (s *Store) recoverLocked(ctx context.Context) error {
	before, err := s.pages(ctx, s.db)
	if err != nil {
		return err
	}
	if err := s.resetLog(ctx); err != nil {
		return s.proofFailed(ctx, err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
		return err
	}
	if err := s.resetLog(ctx); err != nil {
		return s.proofFailed(ctx, err)
	}
	after, err := s.pages(ctx, s.db)
	if err != nil {
		return err
	}
	if after > before {
		s.storage.blocked = "reclamation grew the database"
		return ErrStorageBlocked
	}
	s.storage.blocked = ""
	return nil
}

type StorageStatus struct {
	Pages            int64   `json:"pages"`
	MaxPages         int64   `json:"max_pages"`
	OrdinaryMaxPages int64   `json:"ordinary_max_pages"`
	ReservePages     int64   `json:"reserve_pages"`
	PageBytes        int64   `json:"page_bytes"`
	LogBytes         int64   `json:"log_bytes"`
	WorkDebtPages    int64   `json:"work_debt_pages"`
	Blocked          *string `json:"blocked"`
}

func (s *Store) StorageStatus(ctx context.Context) (StorageStatus, error) {
	pages, err := s.pages(ctx, s.db)
	if err != nil {
		return StorageStatus{}, err
	}
	debt, err := debtPages(ctx, s.db)
	if err != nil {
		return StorageStatus{}, err
	}
	result := StorageStatus{Pages: pages, MaxPages: s.storage.maxPages, OrdinaryMaxPages: s.storage.maxPages - reservePages,
		ReservePages: reservePages, PageBytes: pageSize, WorkDebtPages: debt}
	if info, err := os.Stat(s.storage.path + "-wal"); err == nil {
		result.LogBytes = info.Size()
	}
	s.storage.mu.Lock()
	if s.storage.blocked != "" {
		blocked := s.storage.blocked
		result.Blocked = &blocked
	}
	s.storage.mu.Unlock()
	return result, nil
}
