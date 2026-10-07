package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The one advisory lease engine for work writers and resource claims
// (docs/WORK-ITEMS-V1.md, "One shared advisory lease engine"). A lease grants no
// permission and fences no file; it records who said they are writing.

const (
	maxBundles       = 16 // retained bundles per store, including inactive ones
	maxDaemonBundles = 32 // retained bundles across all stores (docs/WORK-ITEMS-GO-STORAGE.md)
	maxResources     = 8
	defaultLease     = 900
	minLease         = 60
	maxLease         = 3600
)

// WorkRefusal is a typed work refusal with bounded details, such as the holder of a
// conflicting claim or the current revision.
type WorkRefusal struct {
	Code    string
	Message string
	Details map[string]any
}

func (r WorkRefusal) Error() string { return r.Code + ": " + r.Message }

func workError(code, message string) error { return WorkRefusal{Code: code, Message: message} }

type claimResource struct {
	Kind, Key string
}

// checkText accepts 1 to limit bytes of valid UTF-8.
func checkText(value, field string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return workError("invalid_request", field+" must be 1 to "+fmt.Sprint(limit)+" bytes of UTF-8 text")
	}
	return nil
}

// checkConsumer accepts a consumer or author label: 1 to 128 characters and 512 bytes.
func checkConsumer(value, field string) error {
	if err := checkText(value, field, 512); err != nil {
		return err
	}
	if utf8.RuneCountInString(value) > 128 {
		return workError("invalid_request", field+" exceeds 128 characters")
	}
	return nil
}

func checkWorkID(value string) error {
	if len(value) != 32 || strings.Trim(value, "0123456789abcdef") != "" {
		return workError("invalid_request", "invalid work ID")
	}
	return nil
}

// checkResource validates one claim key. A path is repository-relative with "/" between
// components; "." is the whole repository and the only dot spelling.
func checkResource(r claimResource) error {
	switch r.Kind {
	case "writer", "path", "exact":
	default:
		return workError("invalid_request", "unknown resource kind")
	}
	if err := checkText(r.Key, "resource", 512); err != nil {
		return err
	}
	if strings.ContainsRune(r.Key, '\\') || strings.IndexFunc(r.Key, unicode.IsControl) >= 0 {
		return workError("invalid_request", "ambiguous resource spelling")
	}
	switch {
	case r.Kind == "writer":
		return checkWorkID(r.Key)
	case r.Kind == "path" && r.Key != ".":
		for _, part := range strings.Split(r.Key, "/") {
			if part == "" || part == "." || part == ".." {
				return workError("invalid_request", "a path resource uses relative components")
			}
		}
	}
	return nil
}

// overlaps compares keys by kind and path components, without resolving anything:
// auth overlaps auth/session.py but not authorization.
func overlaps(a, b claimResource) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind != "path" {
		return a.Key == b.Key
	}
	return a.Key == b.Key || a.Key == "." || b.Key == "." || strings.HasPrefix(a.Key, b.Key+"/") || strings.HasPrefix(b.Key, a.Key+"/")
}

// claimBundle is the writer claim for workID followed by the optional resources.
func claimBundle(workID string, optional []claimResource) ([]claimResource, error) {
	if len(optional) > maxResources {
		return nil, workError("invalid_request", "at most eight optional resources are allowed")
	}
	result := []claimResource{{"writer", workID}}
	for _, r := range optional {
		if r.Kind != "path" && r.Kind != "exact" {
			return nil, workError("invalid_request", "optional resources are path or exact keys")
		}
		if err := checkResource(r); err != nil {
			return nil, err
		}
		for _, held := range result {
			if held == r {
				return nil, workError("invalid_request", "duplicate resource")
			}
		}
		result = append(result, r)
	}
	return result, nil
}

// leaseEngine runs inside its caller's work transaction; the caller checks the item and
// writes the matching work event in the same transaction.
type leaseEngine struct {
	ctx   context.Context
	tx    *writeTx
	store string
}

type lease struct {
	Generation int64   `json:"generation"`
	Revision   int64   `json:"revision"`
	ExpiresAt  float64 `json:"expires_at"`
}

type heldBundle struct {
	Generation, Revision, ProgressEpoch               int64
	WorkID, Consumer                                  string
	IssuedAt, RenewedAt, ExpiresAt                    float64
	OverdueRecorded, Active, OverdueCredit, EndCredit bool
}

const bundleColumns = `generation,work_id,consumer,revision,issued_at,renewed_at,expires_at,progress_epoch,
	overdue_recorded,active,overdue_credit,end_credit`

func scanBundle(row scanner) (heldBundle, error) {
	var b heldBundle
	err := row.Scan(&b.Generation, &b.WorkID, &b.Consumer, &b.Revision, &b.IssuedAt, &b.RenewedAt, &b.ExpiresAt,
		&b.ProgressEpoch, &b.OverdueRecorded, &b.Active, &b.OverdueCredit, &b.EndCredit)
	return b, err
}

// bundleFor returns the retained bundle of a work item, if any.
func bundleFor(ctx context.Context, q querier, store, workID string) (*heldBundle, error) {
	b, err := scanBundle(q.QueryRowContext(ctx, `SELECT `+bundleColumns+` FROM claim_bundles WHERE store=? AND work_id=?`, store, workID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func bundleResources(ctx context.Context, q querier, store string, generation int64) ([]claimResource, error) {
	rows, err := q.QueryContext(ctx, `SELECT kind,resource FROM claim_resources WHERE store=? AND generation=? ORDER BY ordinal`, store, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []claimResource
	for rows.Next() {
		var r claimResource
		if err := rows.Scan(&r.Kind, &r.Key); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// counter allocates the next value of a store's durable counter; it never wraps.
func counter(ctx context.Context, tx *writeTx, store, column string) (int64, error) {
	var value int64
	if err := tx.QueryRowContext(ctx, `SELECT `+column+` FROM memory_stores WHERE store_id=?`, store).Scan(&value); err != nil {
		return 0, err
	}
	if value >= math.MaxInt64 {
		return 0, workError("capacity", "a durable work counter is exhausted")
	}
	value++
	_, err := tx.ExecContext(ctx, `UPDATE memory_stores SET `+column+`=? WHERE store_id=?`, value, store)
	return value, err
}

// acquire takes a fresh generation for the writer claim and the optional resources, all
// or nothing. A live overlapping claim refuses with its holder.
func (e leaseEngine) acquire(workID, consumer string, epoch int64, optional []claimResource, now float64, duration int64) (lease, error) {
	keys, err := claimBundle(workID, optional)
	if err != nil {
		return lease{}, err
	}
	rows, err := e.tx.QueryContext(e.ctx, `SELECT `+bundleColumns+` FROM claim_bundles WHERE store=? ORDER BY generation`, e.store)
	if err != nil {
		return lease{}, err
	}
	var held []heldBundle
	for rows.Next() {
		b, err := scanBundle(rows)
		if err != nil {
			rows.Close()
			return lease{}, err
		}
		held = append(held, b)
	}
	rows.Close()
	for _, b := range held {
		if b.Active && b.ExpiresAt > now {
			resources, err := bundleResources(e.ctx, e.tx, e.store, b.Generation)
			if err != nil {
				return lease{}, err
			}
			for _, old := range resources {
				for _, key := range keys {
					if overlaps(key, old) {
						return lease{}, WorkRefusal{Code: "claim_conflict", Message: "a resource has a live owner", Details: map[string]any{
							"work_id": b.WorkID, "consumer": b.Consumer, "generation": b.Generation, "expires_at": b.ExpiresAt,
							"resource": []string{old.Kind, old.Key}}}
					}
				}
			}
		}
		if b.WorkID == workID {
			return lease{}, workError("claim_capacity", "the retained bundle needs expiry reconciliation and reclamation; retry after maintenance")
		}
	}
	if len(held) >= maxBundles {
		return lease{}, workError("claim_capacity", "this store's retained claim bundle limit is reached")
	}
	var all int
	if err := e.tx.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM claim_bundles`).Scan(&all); err != nil {
		return lease{}, err
	}
	if all >= maxDaemonBundles {
		return lease{}, workError("claim_capacity", "the daemon's retained claim bundle limit is reached")
	}
	generation, err := counter(e.ctx, e.tx, e.store, "claim_counter")
	if err != nil {
		return lease{}, err
	}
	expires := now + float64(duration)
	if _, err := e.tx.ExecContext(e.ctx, `INSERT INTO claim_bundles(store,generation,work_id,consumer,revision,issued_at,renewed_at,
		expires_at,progress_epoch) VALUES (?,?,?,?,1,?,?,?,?)`, e.store, generation, workID, consumer, now, now, expires, epoch); err != nil {
		return lease{}, err
	}
	for i, key := range keys {
		if _, err := e.tx.ExecContext(e.ctx, `INSERT INTO claim_resources(store,generation,ordinal,kind,resource) VALUES (?,?,?,?,?)`,
			e.store, generation, i, key.Kind, key.Key); err != nil {
			return lease{}, err
		}
	}
	return lease{Generation: generation, Revision: 1, ExpiresAt: expires}, nil
}

// owner proves that consumer holds generation of workID live now.
func (e leaseEngine) owner(workID, consumer string, generation int64, now float64) (heldBundle, error) {
	b, err := scanBundle(e.tx.QueryRowContext(e.ctx, `SELECT `+bundleColumns+` FROM claim_bundles WHERE store=? AND work_id=?
		AND consumer=? AND generation=? AND active=1`, e.store, workID, consumer, generation))
	if errors.Is(err, sql.ErrNoRows) || err == nil && b.ExpiresAt <= now {
		return heldBundle{}, workError("stale_claim", "this claim generation is not currently owned")
	}
	return b, err
}

// renew extends the current generation; it is not progress and moves no deadline.
func (e leaseEngine) renew(workID, consumer string, generation, revision int64, now float64, duration int64) (lease, error) {
	b, err := e.owner(workID, consumer, generation, now)
	if err != nil {
		return lease{}, err
	}
	if b.Revision != revision {
		return lease{}, WorkRefusal{Code: "revision_conflict", Message: "the claim revision changed",
			Details: map[string]any{"current_claim_revision": b.Revision}}
	}
	if revision == math.MaxInt64 {
		return lease{}, workError("capacity", "the claim revision is exhausted")
	}
	expires := now + float64(duration)
	if _, err := e.tx.ExecContext(e.ctx, `UPDATE claim_bundles SET revision=?,renewed_at=?,expires_at=? WHERE store=? AND generation=?`,
		revision+1, now, expires, e.store, generation); err != nil {
		return lease{}, err
	}
	return lease{Generation: generation, Revision: revision + 1, ExpiresAt: expires}, nil
}

// progress rearms the overdue obligation of the owner's generation for its next epoch.
func (e leaseEngine) progress(workID, consumer string, generation, epoch int64, now float64) error {
	b, err := e.owner(workID, consumer, generation, now)
	if err != nil {
		return err
	}
	if epoch != b.ProgressEpoch+1 {
		return workError("incompatible_store", "work and claim progress epochs disagree")
	}
	_, err = e.tx.ExecContext(e.ctx, `UPDATE claim_bundles SET progress_epoch=?,overdue_recorded=0,overdue_credit=1
		WHERE store=? AND generation=?`, epoch, e.store, generation)
	return err
}

// overdue spends the overdue credit of a generation inside its event's transaction.
func (e leaseEngine) overdue(generation, epoch int64) (bool, error) {
	var active, recorded bool
	var current int64
	err := e.tx.QueryRowContext(e.ctx, `SELECT active,progress_epoch,overdue_recorded FROM claim_bundles WHERE store=? AND generation=?`,
		e.store, generation).Scan(&active, &current, &recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !active || recorded {
		return false, err
	}
	if current != epoch {
		return false, workError("incompatible_store", "work and claim progress epochs disagree")
	}
	// Only equal-size 0/1 flags change, so the row needs no new page.
	_, err = e.tx.ExecContext(e.ctx, `UPDATE claim_bundles SET overdue_credit=0,overdue_recorded=1 WHERE store=? AND generation=?`,
		e.store, generation)
	return err == nil, err
}

// expire ends a lapsed generation and spends its credits.
func (e leaseEngine) expire(generation int64, now float64) (bool, error) {
	var expires float64
	var active bool
	err := e.tx.QueryRowContext(e.ctx, `SELECT expires_at,active FROM claim_bundles WHERE store=? AND generation=?`,
		e.store, generation).Scan(&expires, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !active || expires > now {
		return false, err
	}
	return true, e.end(generation)
}

// release ends the owner's generation (release or finish).
func (e leaseEngine) release(workID, consumer string, generation int64, now float64) error {
	if _, err := e.owner(workID, consumer, generation, now); err != nil {
		return err
	}
	return e.end(generation)
}

func (e leaseEngine) end(generation int64) error {
	// Only equal-size 0/1 flags change; no indexed column is in the SET list.
	_, err := e.tx.ExecContext(e.ctx, `UPDATE claim_bundles SET active=0,overdue_credit=0,end_credit=0 WHERE store=? AND generation=?`,
		e.store, generation)
	return err
}

// reclaimOne deletes the oldest inactive bundle and its resources.
func (e leaseEngine) reclaimOne() (bool, error) {
	var generation int64
	var overdue, end bool
	err := e.tx.QueryRowContext(e.ctx, `SELECT generation,overdue_credit,end_credit FROM claim_bundles WHERE store=? AND active=0
		ORDER BY generation LIMIT 1`, e.store).Scan(&generation, &overdue, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if overdue || end {
		return false, workError("incompatible_store", "an inactive bundle still has funded obligations")
	}
	if _, err := e.tx.ExecContext(e.ctx, `DELETE FROM claim_resources WHERE store=? AND generation=?`, e.store, generation); err != nil {
		return false, err
	}
	_, err = e.tx.ExecContext(e.ctx, `DELETE FROM claim_bundles WHERE store=? AND generation=?`, e.store, generation)
	return err == nil, err
}
