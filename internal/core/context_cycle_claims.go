package core

import (
	"context"
	"database/sql"
	"errors"
)

// Work claims during a managed cycle (#489). Admission extends the session's live claims to
// cycleClaimGrace seconds past the cycle deadline, in its own transaction, so the clear and
// the pickup cannot outlast them. A renew while the cycle is unfinished keeps the longer
// expiry. Neither changes a claim's revision, generation, renewal time or progress.

const cycleClaimGrace = 60 // seconds after the cycle deadline

// cycleClaimMatch selects the claim_bundles row that the cycle row c's session holds: by its
// peer name, its family:id, its native work actor, or the participant address it held at
// admission.
const cycleClaimMatch = `(claim_bundles.consumer=c.peer OR claim_bundles.consumer=c.family||':'||c.session_id
	OR (claim_bundles.native_actor!='' AND claim_bundles.native_actor=koinon_native_work_actor(c.family,c.session_id))
	OR (c.held=1 AND c.address!='' AND claim_bundles.consumer='` + participantPrefix + `'||c.address))`

// protectCycleClaims extends the live claims of the admitted cycle's session in its
// repository's work store to the protection floor. An expired claim stays expired, and a
// longer expiry stays.
func protectCycleClaims(ctx context.Context, tx *writeTx, c cycleRow, repository string, now int64) error {
	if repository == "" {
		return nil
	}
	store, err := storeOf(ctx, tx, repository)
	if err != nil || store == "" {
		return err
	}
	floor := float64(c.CycleDeadline)/1000 + cycleClaimGrace
	_, err = tx.ExecContext(ctx, `UPDATE claim_bundles SET expires_at=? WHERE store=? AND active=1 AND expires_at>? AND expires_at<?
		AND EXISTS (SELECT 1 FROM context_cycles c WHERE c.cycle_id=? AND `+cycleClaimMatch+`)`,
		floor, store, float64(now)/1000, floor, c.ID)
	return err
}

// cycleProtects reports whether an unfinished cycle of the session that holds the claim
// generation protects it.
func cycleProtects(ctx context.Context, q querier, store string, generation int64) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM claim_bundles JOIN context_cycles c ON c.finished_at=0
		JOIN sessions s ON s.family=c.family AND s.id=c.session_id
		JOIN memory_stores m ON m.repository=s.repository AND m.store_id=claim_bundles.store
		WHERE claim_bundles.store=? AND claim_bundles.generation=? AND `+cycleClaimMatch+` LIMIT 1`, store, generation).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
