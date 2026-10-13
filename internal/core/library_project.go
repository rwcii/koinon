package core

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/rwcii/koinon/internal/library"
)

func (s *Store) libraryCatalog() (library.Catalog, error) {
	if s.library.catalog.Version != 0 {
		return s.library.catalog, nil
	}
	return library.Embedded("dev")
}

// Project receipts remain memory notes with that API's fixed retry deadlines and
// replacement/conflict behavior. Bind the typed marker to native writer metadata;
// author text is never the source of its family or repository.
func (s *Store) validateProjectDecision(ctx context.Context, m MemoryCaller, r MemoryRecordRequest) error {
	if !libraryDecisionMarked(r.Body) {
		return nil
	}
	d, err := library.ParseDecision([]byte(r.Body))
	if err != nil || r.Type != "decision" || r.Scope != "repo" || m.Family != d.Family || d.Scope != "project" {
		return memoryError("invalid_library_decision", "a library receipt must be a project decision from its native family")
	}
	c, err := s.libraryCatalog()
	if err != nil {
		return err
	}
	if c.ValidateCurrent(d, m.Family, "project", library.ScopeDigest("project", m.Repository)) != nil {
		return memoryError("invalid_library_decision", "the receipt must match this repository and installed catalog")
	}
	var previous *library.Decision
	if r.Supersedes != nil {
		var body, family string
		if err := s.db.QueryRowContext(ctx, `SELECT body,writer_family FROM memory_entries WHERE repository=? AND seq=?`, m.Repository, *r.Supersedes).Scan(&body, &family); err != nil {
			return memoryError("decision_unavailable", "the replaced receipt is unavailable")
		}
		old, err := library.ParseDecision([]byte(body))
		if err != nil || family != m.Family || old.Family != family || old.ScopeDigest != d.ScopeDigest || old.ItemID != d.ItemID || old.Kind != d.Kind {
			return memoryError("invalid_library_decision", "the replaced entry must be this item and scope's receipt")
		}
		previous = &old
	}
	expected := library.PreserveBaseline(d, previous)
	if !slices.Equal(library.OrderedTargets(d.Adopted), library.OrderedTargets(expected.Adopted)) {
		return memoryError("invalid_library_decision", "a decline retains the replaced receipt's adopted digests; adoption records its result digests")
	}
	return nil
}

type libraryUnavailableReceipt struct {
	ItemID      string `json:"item_id"`
	Family      string `json:"family"`
	ScopeDigest string `json:"scope_digest"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
}

// libraryReceiptHistory exposes bounded absence reasons alongside literal recall.
// It never revives an inactive receipt, moves a cursor, or returns its body.
func (s *Store) libraryReceiptHistory(ctx context.Context, m MemoryCaller) (map[string]any, error) {
	var floor int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(library_floor),0) FROM memory_stores WHERE repository=?`, m.Repository).Scan(&floor); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body,writer_family,revoked_by,expires FROM memory_entries WHERE repository=? AND type='decision' AND superseded_by IS NULL AND (revoked_by IS NOT NULL OR expires<=?) AND body LIKE ? ORDER BY seq DESC`, m.Repository, s.clock(), "%"+library.DecisionMarker+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	unavailable := []libraryUnavailableReceipt{}
	overflow := false
	for rows.Next() {
		var body, family string
		var revoked *int64
		var expires *float64
		if err := rows.Scan(&body, &family, &revoked, &expires); err != nil {
			return nil, err
		}
		d, err := library.ParseDecision([]byte(body))
		if err != nil || d.Family != family || d.Scope != "project" {
			continue
		}
		keyBytes, _ := json.Marshal([4]string{d.ItemID, family, d.ScopeDigest, d.Kind})
		key := string(keyBytes)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(unavailable) >= library.MaxItems*2 {
			overflow = true
			break
		}
		reason := "decision_expired"
		if revoked != nil {
			reason = "decision_revoked"
		}
		unavailable = append(unavailable, libraryUnavailableReceipt{d.ItemID, family, d.ScopeDigest, d.Kind, reason})
	}
	return map[string]any{"floor": floor, "unavailable": unavailable, "incomplete": overflow}, rows.Err()
}
