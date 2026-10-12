package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"

	"github.com/rwcii/koinon/internal/library"
)

const maxMachineDecisions = 256
const maxMachineDecisionBodies = 2 << 20

// One ordinary table and its primary-key index; provenance remains private.
const machineDecisionsTable = `CREATE TABLE library_decisions (
	family TEXT NOT NULL, root_digest TEXT NOT NULL CHECK(length(root_digest)=64),
	item_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
	body TEXT NOT NULL CHECK(length(CAST(body AS BLOB))<=8192),
	writer TEXT NOT NULL, updated_at REAL NOT NULL,
	PRIMARY KEY(family,root_digest,item_id))`

type libraryState struct {
	op      sync.Mutex
	catalog library.Catalog
}

type LibraryDecisionRecord struct {
	Revision  int64            `json:"revision"`
	Decision  library.Decision `json:"decision"`
	Unchanged bool             `json:"unchanged,omitempty"`
}

func machineDecision(ctx context.Context, q querier, family, root, item string) (*LibraryDecisionRecord, error) {
	var r LibraryDecisionRecord
	var body string
	err := q.QueryRowContext(ctx, `SELECT revision,body FROM library_decisions WHERE family=? AND root_digest=? AND item_id=?`, family, root, item).Scan(&r.Revision, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.Decision, err = library.ParseDecision([]byte(body))
	if err != nil {
		return nil, memoryError("decision_unavailable", "the retained machine decision is invalid")
	}
	return &r, nil
}

// LibraryDecisionRead is read-only current state, including removed catalog items.
func (s *Store) LibraryDecisionRead(ctx context.Context, caller Key, root, item string) (*LibraryDecisionRecord, error) {
	if !library.ValidDigest(root) || item == "" || len(item) > 128 {
		return nil, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return nil, err
	}
	return machineDecision(ctx, tx, caller.Family, root, item)
}

// LibraryDecision stores compared-revision state. An identical current outcome is
// a pure no-op even with a stale revision after a lost reply. An overwritten outcome
// is not a historical replay. Existing storage admission charges every row and page.
func (s *Store) LibraryDecision(ctx context.Context, caller Key, catalog library.Catalog, root string, compared int64, d library.Decision) (*LibraryDecisionRecord, error) {
	if compared < 0 || !library.ValidDigest(root) || catalog.ValidateCurrent(d, caller.Family, "machine", root) != nil {
		return nil, memoryError("invalid_library_decision", "the decision must match this native family, scope and installed catalog")
	}
	s.library.op.Lock()
	defer s.library.op.Unlock()
	if err := s.libraryCaller(ctx, caller); err != nil {
		return nil, err
	}
	current, err := machineDecision(ctx, s.db, caller.Family, root, d.ItemID)
	if err != nil {
		return nil, err
	}
	var previous *library.Decision
	if current != nil {
		previous = &current.Decision
	}
	d = library.PreserveBaseline(d, previous)
	body, err := d.Encode()
	if err != nil {
		return nil, memoryError("invalid_library_decision", "invalid or oversized machine decision")
	}
	if current != nil {
		old, err := current.Decision.Encode()
		if err != nil {
			return nil, err
		}
		if string(old) == string(body) {
			current.Unchanged = true
			return current, nil
		}
	}
	actual := int64(0)
	if current != nil {
		actual = current.Revision
	}
	if actual != compared {
		return nil, WorkRefusal{Code: "decision_conflict", Message: "the machine decision changed; read current state", Details: map[string]any{"current": current}}
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx.Tx, s.now().UnixMilli(), caller); err != nil {
		return nil, err
	}
	var rows, bytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(body AS BLOB))),0) FROM library_decisions`).Scan(&rows, &bytes); err != nil {
		return nil, err
	}
	if current == nil && rows >= maxMachineDecisions {
		return nil, memoryError("decision_capacity", "machine decision row capacity reached")
	}
	if current != nil {
		old, _ := current.Decision.Encode()
		bytes -= int64(len(old))
	}
	if bytes+int64(len(body)) > maxMachineDecisionBodies {
		return nil, memoryError("decision_capacity", "machine decision body capacity reached")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO library_decisions(family,root_digest,item_id,revision,body,writer,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(family,root_digest,item_id) DO UPDATE SET revision=excluded.revision,body=excluded.body,writer=excluded.writer,updated_at=excluded.updated_at`,
		caller.Family, root, d.ItemID, actual+1, string(body), nativeWorkActorForKey(caller), s.clock()); err != nil {
		return nil, tx.fail(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &LibraryDecisionRecord{Revision: actual + 1, Decision: d}, nil
}

func libraryDecisionMarked(body string) bool {
	var marker struct {
		Library string `json:"library"`
	}
	return json.Unmarshal([]byte(body), &marker) == nil && marker.Library == library.DecisionMarker
}
