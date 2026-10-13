package core

import (
	"context"
	"database/sql"
	"strconv"
)

// decisionsData is the read-only Decisions view (#206, chunk 01): one page of active items
// and an optional item found by reference. Chunk 03 replaces it with the designed board.
type decisionsData struct {
	Page      DecisionPage
	Lookup    string
	Item      *DecisionItem
	LookupErr string
}

func (s *Store) dashboardDecisions(ctx context.Context, after, reference string) (decisionsData, error) {
	var data decisionsData
	from, err := strconv.ParseInt(after, 10, 64)
	if after != "" && (err != nil || from < 0) {
		from = 0
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	if data.Page, err = decisionPage(ctx, tx, "active", nil, from, decisionPageItems); err != nil {
		return data, err
	}
	if reference != "" {
		data.Lookup = reference
		if checkReference(reference) != nil {
			data.LookupErr = "not a valid reference"
			return data, nil
		}
		item, err := decisionItem(ctx, tx, reference)
		if code := errorCode(err); code == "not_found" {
			data.LookupErr = "no decision item has this reference"
		} else if err != nil {
			return data, err
		} else {
			data.Item = &item
		}
	}
	return data, nil
}
