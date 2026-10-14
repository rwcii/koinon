package core

import "context"

// A reported item holds one verdict credit until its accept or reject spends it
// (docs/WORK-ITEMS-GO-STORAGE.md): the pages, slots and logical bytes of that verdict.
const (
	verdictCreditPages = 1024
	maxStoreVerdicts   = 16 // pending verdict credits per store
	maxDaemonVerdicts  = 32 // pending verdict credits across all stores
)

// requireVerdictCaller refuses a verdict from the reporting worker: its consumer, or any
// consumer of the native session that reported (PDR 004, Decision 3). Another native
// session's consumer may record it, including a later holder of the same participant.
func requireVerdictCaller(m MemoryCaller, w workItem) error {
	if w.ReportConsumer != nil && m.Consumer == *w.ReportConsumer ||
		w.ReportActor != "" && nativeWorkActor(m) == w.ReportActor {
		return workError("own_report", "the reporting worker cannot record the verdict on its own report")
	}
	return nil
}

// reserveVerdict refuses a report whose verdict could not be funded: the pending verdict
// limits, and the scope revision that a rejection writes. The store's slots, bytes and
// pages are enforced after the mutation, with the new credit in its debt.
func reserveVerdict(ctx context.Context, tx *writeTx, store, workID string, l memoryLimits) error {
	var inStore, all, scopes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(verdict_credit),0) FROM work_items WHERE store=?`, store).Scan(&inStore); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(verdict_credit),0) FROM work_items`).Scan(&all); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_scope_revisions WHERE store=? AND work_id=?`, store, workID).Scan(&scopes); err != nil {
		return err
	}
	switch {
	case inStore >= maxStoreVerdicts:
		return workError("capacity", "this store's pending verdict limit is reached; the claim is kept")
	case all >= maxDaemonVerdicts:
		return workError("capacity", "the daemon's pending verdict limit is reached; the claim is kept")
	case scopes >= l.workScopesPerItem:
		return workError("capacity", "this item has no scope revision left for a rejection; the claim is kept")
	}
	return nil
}
