package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Export (#589): every decision item with its revisions, verdicts, snapshots, notes,
// recipients, deliveries and withdrawals, live and deep-archived, as the deep archive's JSON
// lines, so that a person or CI can read a copy of the board without the daemon. A page holds
// the records after a cursor: the deep-archived items by reference, then the live items by ID.
// Each page reads in one transaction with the board's change version, which every move and
// restore changes in its own transaction, so a client whose pages all name one version has
// every item exactly once. A record larger than a page goes alone in its pages as parts, byte
// ranges of the record, so no reply exceeds the client's transport limit. An acknowledgement
// changes a record without changing the version, so each part names the SHA-256 of the whole
// record that it was cut from, and a client joins only the parts of one digest. The cursor of
// a later part holds the version and the digest, and a board or record that changed since
// is export_changed, so the client starts again.

// decisionExportItems bounds the records of one page. decisionExportBytes bounds their bytes
// and one part of a larger record; tests lower it to read small records in parts.
const decisionExportItems = 100

var decisionExportBytes int64 = 4 << 20

// Export phases: the deep-archived items, then the live items.
const (
	exportDeep = "deep"
	exportLive = "live"
)

// DecisionExportPage is one page of an export. Version is the board's change version that the
// page read; Next is the cursor of the next page when More is set. A page with Part holds no
// records.
type DecisionExportPage struct {
	Version string              `json:"version"`
	Records []json.RawMessage   `json:"records"`
	Part    *DecisionExportPart `json:"part,omitempty"`
	More    bool                `json:"more"`
	Next    *DecisionExportAt   `json:"next,omitempty"`
}

// DecisionExportPart is a byte range of one record larger than a page: the record's bytes from
// Offset, the SHA-256 of the whole record in hex, and Final on its last range.
type DecisionExportPart struct {
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
	Digest string `json:"digest"`
	Final  bool   `json:"final"`
}

// DecisionExportAt is an export cursor: the phase, the last reference or item ID read in it,
// and, inside a record read as parts, the bytes already read, the board version and the
// record's digest.
type DecisionExportAt struct {
	Phase          string `json:"phase"`
	AfterReference string `json:"after_reference,omitempty"`
	AfterID        int64  `json:"after_id,omitempty"`
	Offset         int64  `json:"offset,omitempty"`
	Version        string `json:"version,omitempty"`
	Digest         string `json:"digest,omitempty"`
}

// errExportChanged refuses a cursor inside a record whose board version or record changed
// since its part was read.
var errExportChanged = workError("export_changed", "the board changed since the last part; start the export again")

// DecisionExport reads one page of the export at the cursor; an empty phase starts it.
func (s *Store) DecisionExport(ctx context.Context, caller Key, at DecisionExportAt, limit int) (DecisionExportPage, error) {
	if at.Phase == "" {
		at = DecisionExportAt{Phase: exportDeep}
	}
	if at.Phase != exportDeep && at.Phase != exportLive || at.AfterID < 0 || at.Offset < 0 || limit < 0 {
		return DecisionExportPage{}, decisionInvalid("cursor", "phase must be deep or live, and after_id, offset and limit must not be negative")
	}
	if at.Offset > 0 && (at.Version == "" || at.Digest == "") {
		return DecisionExportPage{}, decisionInvalid("cursor", "a cursor with an offset needs its version and digest")
	}
	if limit == 0 || limit > decisionExportItems {
		limit = decisionExportItems
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionExportPage{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return DecisionExportPage{}, err
	}
	page := DecisionExportPage{Records: []json.RawMessage{}}
	if err := tx.QueryRowContext(ctx, `SELECT version FROM change_versions WHERE topic=?`, changeDecisions).Scan(&page.Version); err != nil {
		return DecisionExportPage{}, err
	}
	if at.Offset > 0 && at.Version != page.Version {
		return DecisionExportPage{}, errExportChanged
	}
	used := 0
	var partErr error
	// add puts a record in the page, or one part of a record larger than a page in a page of
	// its own, and reports whether the page has room for another.
	add := func(record []byte, next DecisionExportAt) bool {
		if len(page.Records) == 0 && (at.Offset > 0 || int64(len(record)) > decisionExportBytes) {
			sum := sha256.Sum256(record)
			digest := hex.EncodeToString(sum[:])
			if at.Offset > 0 && digest != at.Digest {
				partErr = errExportChanged
				return false
			}
			if at.Offset >= int64(len(record)) {
				partErr = decisionInvalid("offset", "the offset is not inside the next record")
				return false
			}
			end := min(at.Offset+decisionExportBytes, int64(len(record)))
			page.Part = &DecisionExportPart{Offset: at.Offset, Data: record[at.Offset:end], Digest: digest, Final: end == int64(len(record))}
			page.More = true
			if page.Part.Final {
				page.Next = &next
			} else {
				rest := at
				rest.Offset, rest.Version, rest.Digest = end, page.Version, digest
				page.Next = &rest
			}
			return false
		}
		if len(page.Records) == limit || len(page.Records) > 0 && int64(used+len(record)+1) > decisionExportBytes {
			page.More = true
			return false
		}
		used += len(record) + 1
		page.Records = append(page.Records, json.RawMessage(record))
		page.Next = &next
		return true
	}
	if at.Phase == exportDeep {
		type locator struct {
			reference, file string
			offset, length  int64
		}
		var moved []locator
		rows, err := tx.QueryContext(ctx, `SELECT reference,file,offset,length FROM decision_deep_archive WHERE reference>?
			ORDER BY reference LIMIT ?`, at.AfterReference, limit+1)
		if err != nil {
			return DecisionExportPage{}, err
		}
		for rows.Next() {
			var l locator
			if err := rows.Scan(&l.reference, &l.file, &l.offset, &l.length); err != nil {
				rows.Close()
				return DecisionExportPage{}, err
			}
			moved = append(moved, l)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return DecisionExportPage{}, err
		}
		for _, l := range moved {
			line, err := s.readDecisionLine(l.file, l.offset)
			if err != nil {
				return DecisionExportPage{}, err
			}
			record := bytes.TrimSuffix(line, []byte{'\n'})
			var head struct {
				Record    string `json:"record"`
				Reference string `json:"reference"`
			}
			if line == nil || int64(len(line)) != l.length || json.Unmarshal(record, &head) != nil ||
				head.Record != decisionRecordMarker || head.Reference != l.reference {
				return DecisionExportPage{}, workError("decision_unavailable", "the deep-archive line of "+l.reference+" does not match its locator")
			}
			if !add(record, DecisionExportAt{Phase: exportDeep, AfterReference: l.reference}) {
				return page, partErr
			}
		}
		if at.Offset > 0 {
			return DecisionExportPage{}, decisionInvalid("offset", "the offset has no next record")
		}
		// The deep archive is read; the live items follow.
		page.More, page.Next = true, &DecisionExportAt{Phase: exportLive}
		return page, nil
	}
	var ids []int64
	rows, err := tx.QueryContext(ctx, `SELECT id FROM decision_items WHERE id>? ORDER BY id LIMIT ?`, at.AfterID, limit+1)
	if err != nil {
		return DecisionExportPage{}, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DecisionExportPage{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DecisionExportPage{}, err
	}
	for _, id := range ids {
		record, err := readDecisionRecord(ctx, tx, id)
		if err != nil {
			return DecisionExportPage{}, err
		}
		if record == nil {
			return DecisionExportPage{}, errors.New("a listed decision item has no row")
		}
		data, err := json.Marshal(record)
		if err != nil {
			return DecisionExportPage{}, err
		}
		if !add(data, DecisionExportAt{Phase: exportLive, AfterID: id}) {
			return page, partErr
		}
	}
	if at.Offset > 0 {
		return DecisionExportPage{}, decisionInvalid("offset", "the offset has no next record")
	}
	page.Next = nil
	return page, nil
}
