package core

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// Read-only projections for the dashboard views. None of them writes, so a view never
// competes with agents for the storage bound.

// SessionWork is a live claim held under a session's key (FAMILY:ID).
type SessionWork struct {
	Repository string  `json:"repository"`
	WorkID     string  `json:"work_id"`
	Title      string  `json:"title"`
	Generation int64   `json:"generation"`
	ExpiresAt  float64 `json:"expires_at"`
}

// sessionClaims maps each consumer key to its live claims. The daemon retains at most
// maxDaemonBundles bundles, which bounds the result.
func (s *Store) sessionClaims(ctx context.Context) (map[string][]SessionWork, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.consumer,m.repository,w.work_id,w.title,b.generation,b.expires_at
		FROM claim_bundles b JOIN work_items w ON w.store=b.store AND w.work_id=b.work_id
		JOIN memory_stores m ON m.store_id=b.store WHERE b.active=1 AND b.expires_at>? ORDER BY b.consumer,w.work_id`, s.clock())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]SessionWork{}
	for rows.Next() {
		var consumer string
		var w SessionWork
		if err := rows.Scan(&consumer, &w.Repository, &w.WorkID, &w.Title, &w.Generation, &w.ExpiresAt); err != nil {
			return nil, err
		}
		result[consumer] = append(result[consumer], w)
	}
	return result, rows.Err()
}

const dashboardSessionPage = 100

// dashboardSessions lists sessions in (family, id) order after a key (nil for the first
// page); next is the last listed key when more follow.
func (s *Store) dashboardSessions(ctx context.Context, after *Key) ([]Session, *Key, error) {
	// Agent sessions only; the maintainer's inbox is on the messages view.
	query, args := sessionQuery+` WHERE s.family!='maintainer'`, []any{}
	if after != nil {
		query += ` AND (s.family,s.id)>(?,?)`
		args = append(args, after.Family, after.ID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY s.family,s.id LIMIT `+strconv.Itoa(dashboardSessionPage+1), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	result := []Session{}
	now := s.now().UnixMilli()
	for rows.Next() {
		r, err := scanSession(rows, now)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(result) <= dashboardSessionPage {
		return result, nil, nil
	}
	result = result[:dashboardSessionPage]
	last := result[len(result)-1]
	return result, &Key{last.Family, last.ID}, nil
}

// MessageRecord is one message as the dashboard lists it.
type MessageRecord struct {
	Message
	RecipientFamily string `json:"recipient_family"`
	RecipientID     string `json:"recipient_id"`
	RecipientName   string `json:"recipient_name"`
	UpdatedAt       int64  `json:"updated_at"`
}

const dashboardMessagePage = 50

// dashboardMessages lists messages newest first, before a message ID (0 for the newest),
// optionally for one recipient. A page holds at most dashboardMessagePage messages and
// about one MiB of bodies; next is the cursor for the following page, or 0.
func (s *Store) dashboardMessages(ctx context.Context, recipient *Key, before int64) ([]MessageRecord, int64, error) {
	if before < 0 {
		return nil, 0, ErrInvalid
	}
	query := `SELECT m.id,m.seq,m.sender_family,m.sender_name,m.body,m.created_at,m.delivery_state,m.delivery_reason,
		m.delivery_updated_at,m.recipient_family,m.recipient_id,
		COALESCE((SELECT name FROM names WHERE kind='peer' AND family=m.recipient_family AND session_id=m.recipient_id),''),
		m.seq<=COALESCE(s.acked_through,0),m.maintainer_ack
		FROM messages m LEFT JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
		WHERE (?1=0 OR m.id<?1)`
	args := []any{before}
	if recipient != nil {
		query += ` AND m.recipient_family=?2 AND m.recipient_id=?3`
		args = append(args, recipient.Family, recipient.ID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY m.id DESC LIMIT `+strconv.Itoa(dashboardMessagePage+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []MessageRecord{}
	size, next := 0, int64(0)
	for rows.Next() {
		var m MessageRecord
		var maintainer bool
		if err := rows.Scan(&m.ID, &m.Seq, &m.SenderFamily, &m.SenderName, &m.Body, &m.CreatedAt, &m.DeliveryState,
			&m.DeliveryReason, &m.UpdatedAt, &m.RecipientFamily, &m.RecipientID, &m.RecipientName, &m.Acknowledged, &maintainer); err != nil {
			return nil, 0, err
		}
		m.AcknowledgedBy = acknowledgedBy(m.Acknowledged, maintainer)
		if len(result) == dashboardMessagePage || (len(result) > 0 && size+len(m.Body) > 1<<20) {
			next = result[len(result)-1].ID
			break
		}
		size += len(m.Body)
		result = append(result, m)
	}
	return result, next, rows.Err()
}

// StoreSummary is one memory store with its head, usage, ceilings and work state.
type StoreSummary struct {
	Repository  string         `json:"repository"`
	StoreID     string         `json:"store_id"`
	Head        int64          `json:"head"`
	Floor       int64          `json:"floor"`
	Usage       memoryUsage    `json:"usage"`
	MaxEntries  int64          `json:"max_entries"`
	MaxLogical  int64          `json:"max_logical"`
	Maintenance map[string]any `json:"work_maintenance"`
	OverdueDebt int64          `json:"overdue_credits"`
	EndDebt     int64          `json:"end_credits"`
	Work        []WorkView     `json:"work"`
}

const dashboardStorePage = 50

// dashboardStores lists stores in repository order after a repository ("" for the
// first page), with their unfinished work when withWork is set; next is the cursor for
// the following page, or "".
func (s *Store) dashboardStores(ctx context.Context, after string, withWork bool) ([]StoreSummary, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository,store_id,head,floor FROM memory_stores WHERE repository>?
		ORDER BY repository LIMIT `+strconv.Itoa(dashboardStorePage+1), after)
	if err != nil {
		return nil, "", err
	}
	var result []StoreSummary
	for rows.Next() {
		var item StoreSummary
		if err := rows.Scan(&item.Repository, &item.StoreID, &item.Head, &item.Floor); err != nil {
			rows.Close()
			return nil, "", err
		}
		result = append(result, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(result) > dashboardStorePage {
		result = result[:dashboardStorePage]
		next = result[len(result)-1].Repository
	}
	limits, now := s.limits(), s.clock()
	for i := range result {
		item := &result[i]
		item.MaxEntries, item.MaxLogical = limits.entries, limits.logical
		if item.Usage, err = usage(ctx, s.db, item.Repository); err != nil {
			return nil, "", err
		}
		if item.Maintenance, err = s.workMaintenanceStatus(ctx, item.Repository); err != nil {
			return nil, "", err
		}
		debt, err := storeDebt(ctx, s.db, item.Repository)
		if err != nil {
			return nil, "", err
		}
		item.OverdueDebt, item.EndDebt = debt.overdue, debt.end
		if !withWork {
			continue
		}
		views, err := s.workViews(ctx, item.Repository, now)
		if err != nil {
			return nil, "", err
		}
		for _, v := range views {
			if v.Lifecycle != "finished" {
				item.Work = append(item.Work, v)
			}
		}
	}
	return result, next, nil
}

// sessionByName resolves a peer name to its session key for the message filter.
func (s *Store) sessionByName(ctx context.Context, name string) (*Key, error) {
	var k Key
	err := s.db.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=?`, name).Scan(&k.Family, &k.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPeerNotFound
	}
	return &k, err
}
