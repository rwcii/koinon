package core

import (
	"context"
	"sync"
	"time"
)

// Work maintenance (docs/WORK-ITEMS-MAINTENANCE.md): one bounded sweep at a time records
// due transitions so idle sessions learn of them, then reclaims at most one inactive
// bundle and one expired finished item per store. It never changes an owner, starts
// work or sends a message.

const workSweepInterval = 30 * time.Second

type workState struct {
	mu          sync.Mutex
	lastSuccess *float64
	fault       *string
	faultAt     *float64
}

func (s *Store) recordSweep(fault error) {
	now := s.clock()
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	if fault == nil {
		s.work.lastSuccess, s.work.fault, s.work.faultAt = &now, nil, nil
		return
	}
	code := "storage_error"
	switch e := fault.(type) {
	case WorkRefusal:
		code = e.Code
	case Refusal:
		code = e.Code
	}
	s.work.fault, s.work.faultAt = &code, &now
}

// MaintainWork runs one sweep over every store that holds work. Each transition and each
// cleanup is its own transaction, so a later failure keeps earlier progress, and the
// durable markers keep a retry from repeating an event.
func (s *Store) MaintainWork(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT s.repository,s.store_id FROM memory_stores s
		WHERE EXISTS (SELECT 1 FROM work_items w WHERE w.store=s.store_id) OR EXISTS (SELECT 1 FROM claim_bundles b WHERE b.store=s.store_id)
		ORDER BY s.store_id`)
	if err != nil {
		s.recordSweep(err)
		return err
	}
	type target struct{ repo, store string }
	var stores []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.repo, &t.store); err != nil {
			rows.Close()
			s.recordSweep(err)
			return err
		}
		stores = append(stores, t)
	}
	rows.Close()
	for _, t := range stores {
		if err := s.maintainStore(ctx, t.repo, t.store); err != nil {
			s.recordSweep(err)
			return err
		}
	}
	s.recordSweep(nil)
	return nil
}

func (s *Store) maintainStore(ctx context.Context, repo, store string) error {
	s.memory.op.Lock()
	advanced := false
	defer func() {
		s.memory.op.Unlock()
		if advanced {
			s.changed(repo)
		}
	}()
	now := s.clock()
	rows, err := s.db.QueryContext(ctx, `SELECT w.work_id FROM work_items w JOIN claim_bundles b ON b.store=w.store AND b.work_id=w.work_id
		WHERE w.store=? AND b.active=1 AND (b.expires_at<=? OR (w.progress_deadline<=? AND b.overdue_recorded=0))
		ORDER BY b.expires_at,w.work_id LIMIT ?`, store, now, now, workSweepLimit)
	if err != nil {
		return err
	}
	var due []string
	for rows.Next() {
		var workID string
		if err := rows.Scan(&workID); err != nil {
			rows.Close()
			return err
		}
		due = append(due, workID)
	}
	rows.Close()
	for _, workID := range due {
		if err := s.reconcile(ctx, repo, workID, now, &advanced); err != nil {
			return err
		}
	}
	var inactive int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM claim_bundles WHERE store=? AND active=0`, store).Scan(&inactive); err != nil {
		return err
	}
	if inactive > 0 {
		// Background cleanup may use the shared control headroom; it still keeps every
		// remaining work credit free.
		if err := s.workWrite(ctx, repo, control, func(tx *writeTx, store string) error {
			_, err := leaseEngine{ctx: ctx, tx: tx, store: store}.reclaimOne()
			return err
		}); err != nil {
			return err
		}
	}
	_, err = s.reclaimFinished(ctx, repo, store, now)
	return err
}

// workMaintenanceStatus reports the sweep diagnostics and a store's counts at this
// moment; the counts are read-only and available while writes are blocked.
func (s *Store) workMaintenanceStatus(ctx context.Context, repo string) (map[string]any, error) {
	now := s.clock()
	s.work.mu.Lock()
	status := map[string]any{"enabled": true, "last_successful_sweep": s.work.lastSuccess, "observed_at": now,
		"fault": s.work.fault, "fault_at": s.work.faultAt, "skipped_submissions": 0}
	s.work.mu.Unlock()
	store, err := storeOf(ctx, s.db, repo)
	if err != nil {
		return nil, err
	}
	var pending, expired, inactive int64
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM claim_bundles b JOIN work_items w ON w.store=b.store AND w.work_id=b.work_id WHERE b.store=?1
			AND b.active=1 AND (b.expires_at<=?2 OR (w.progress_deadline<=?2 AND b.overdue_recorded=0))),
		(SELECT COUNT(*) FROM work_items WHERE store=?1 AND lifecycle='finished' AND expires_at<=?2),
		(SELECT COUNT(*) FROM claim_bundles WHERE store=?1 AND active=0)`, store, now).Scan(&pending, &expired, &inactive)
	status["pending_due"], status["expired_items"], status["inactive_bundles"] = pending, expired, inactive
	return status, err
}
