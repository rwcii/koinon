package core

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// Deep archive (#206, chunk 02): 30 days after an item is archived, and once every delivery
// is acknowledged or skipped, the sweep moves its whole record to an append-only JSON line in
// <state>/decisions/YYYY-MM.jsonl. Nothing is deleted: a locator keeps the reference reserved
// and finds the line, and decision_restore brings the record back.

var decisionLogName = regexp.MustCompile(`^decisions/[0-9]{4}-[0-9]{2}\.jsonl$`)

// decisionRecord is one deep-archived item: every row of the item.
type decisionRecord struct {
	Record     string           `json:"record"`
	ID         int64            `json:"id"`
	Reference  string           `json:"reference"`
	Revision   int64            `json:"revision"`
	Item       map[string]any   `json:"item"`
	Recipients []map[string]any `json:"recipients"`
	Verdicts   []map[string]any `json:"verdicts"`
	Deliveries []map[string]any `json:"deliveries"`
	MovedAt    int64            `json:"moved_at"`
}

const decisionRecordMarker = "koinon.decision.v1"

// decisionState serializes deep-archive passes and keeps the candidate cursor of the
// current walk. afterMark and afterAppend are test hooks between the move's steps.
type decisionState struct {
	mu                     sync.Mutex
	afterAt, afterID       int64
	afterMark, afterAppend func(int64)
}

func rowsAsMaps(ctx context.Context, q querier, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range columns {
			if b, ok := values[i].([]byte); ok {
				values[i] = string(b)
			}
			row[c] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// readDecisionRecord reads every row of item id.
func readDecisionRecord(ctx context.Context, q querier, id int64) (*decisionRecord, error) {
	items, err := rowsAsMaps(ctx, q, `SELECT * FROM decision_items WHERE id=?`, id)
	if err != nil || len(items) != 1 {
		return nil, err
	}
	r := &decisionRecord{Record: decisionRecordMarker, ID: id, Item: items[0]}
	r.Reference, _ = items[0]["reference"].(string)
	r.Revision, _ = items[0]["revision"].(int64)
	if r.Recipients, err = rowsAsMaps(ctx, q, `SELECT * FROM decision_recipients WHERE item_id=? ORDER BY ordinal`, id); err != nil {
		return nil, err
	}
	if r.Verdicts, err = rowsAsMaps(ctx, q, `SELECT * FROM decision_verdicts WHERE item_id=? ORDER BY id`, id); err != nil {
		return nil, err
	}
	r.Deliveries, err = rowsAsMaps(ctx, q, `SELECT d.* FROM decision_deliveries d JOIN decision_verdicts v ON v.id=d.verdict_id
		WHERE v.item_id=? ORDER BY d.verdict_id,d.ordinal`, id)
	return r, err
}

func (s *Store) decisionLogPath(name string) (string, error) {
	if s.root == "" || !decisionLogName.MatchString(name) {
		return "", ErrInvalid
	}
	return filepath.Join(s.root, filepath.FromSlash(name)), nil
}

// openDecisionLog opens a monthly log for appending, creating its private directory and
// file. A file that does not end in a newline gets one, so a partial line left by a crash
// becomes a line of its own that readers skip; no byte is removed. It returns the end
// offset, where the next line starts.
func (s *Store) openDecisionLog(name string) (*os.File, int64, error) {
	path, err := s.decisionLogPath(name)
	if err != nil {
		return nil, 0, err
	}
	dir := filepath.Dir(path)
	if _, err := platform.PrivateDir(dir); err != nil {
		return nil, 0, err
	}
	_, statErr := os.Lstat(path)
	f, err := platform.OpenPrivate(path, os.O_RDWR|os.O_CREATE|os.O_APPEND)
	if err != nil {
		return nil, 0, err
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if err := platform.SyncDir(dir); err != nil {
			f.Close()
			return nil, 0, err
		}
	}
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if end > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, end-1); err != nil {
			f.Close()
			return nil, 0, err
		}
		if last[0] != '\n' {
			if _, err := f.Write([]byte{'\n'}); err != nil {
				f.Close()
				return nil, 0, err
			}
			end++
		}
	}
	return f, end, nil
}

// readDecisionLine reads the complete line at offset, or nil when none is there.
func (s *Store) readDecisionLine(name string, offset int64) ([]byte, error) {
	path, err := s.decisionLogPath(name)
	if err != nil {
		return nil, err
	}
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	// One record holds at most an item, 32 recipients and its verdicts; a line is bounded by
	// the bytes the sweep wrote, read in full up to its newline.
	line, err := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return line, nil
}

// archiveDecisions is one sweep pass: it finishes marked moves left by a crash, then moves
// at most decisionArchiveBatch eligible items, reading at most decisionArchiveWindow
// candidates from a cursor that resumes after items that still wait for an acknowledgement.
func (s *Store) archiveDecisions(ctx context.Context) error {
	s.decisions.mu.Lock()
	defer s.decisions.mu.Unlock()
	marked, err := s.decisionIDs(ctx, `SELECT id FROM decision_items WHERE mark_revision>0 ORDER BY id LIMIT ?`, decisionArchiveBatch)
	if err != nil {
		return err
	}
	for _, id := range marked {
		if err := s.finishMove(ctx, id); err != nil {
			return err
		}
	}
	cutoff := s.now().UnixMilli() - decisionArchiveAfter
	rows, err := s.db.QueryContext(ctx, `SELECT id,archived_at FROM decision_items WHERE state='archived' AND archived_at<?
		AND (archived_at>? OR (archived_at=? AND id>?)) ORDER BY archived_at,id LIMIT ?`,
		cutoff, s.decisions.afterAt, s.decisions.afterAt, s.decisions.afterID, decisionArchiveWindow)
	if err != nil {
		return err
	}
	type candidate struct{ id, at int64 }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.at); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(candidates) < decisionArchiveWindow {
		// The walk reached the end; the next pass starts again from the oldest.
		defer func() { s.decisions.afterAt, s.decisions.afterID = 0, 0 }()
	}
	moved := 0
	for _, c := range candidates {
		s.decisions.afterAt, s.decisions.afterID = c.at, c.id
		var queued int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_verdicts v JOIN decision_deliveries d ON d.verdict_id=v.id
			WHERE v.item_id=? AND d.outcome='queued'`, c.id).Scan(&queued); err != nil {
			return err
		}
		if queued > 0 {
			continue
		}
		if err := s.moveDecision(ctx, c.id); err != nil {
			return err
		}
		if moved++; moved == decisionArchiveBatch {
			break
		}
	}
	return nil
}

func (s *Store) decisionIDs(ctx context.Context, query string, args ...any) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// moveDecision marks the item with its revision, file and offset, appends and syncs its
// line, then deletes its rows and writes the locator in one transaction.
func (s *Store) moveDecision(ctx context.Context, id int64) error {
	name := "decisions/" + time.UnixMilli(s.now().UnixMilli()).UTC().Format("2006-01") + ".jsonl"
	f, offset, err := s.openDecisionLog(name)
	if err != nil {
		return err
	}
	defer f.Close()
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE decision_items SET mark_revision=revision,mark_file=?,mark_offset=? WHERE id=? AND state='archived' AND mark_revision=0`,
		name, offset, id)
	if err != nil {
		return tx.fail(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	if s.decisions.afterMark != nil {
		s.decisions.afterMark(id)
	}
	record, err := readDecisionRecord(ctx, s.db, id)
	if err != nil || record == nil {
		return err
	}
	return s.appendAndDelete(ctx, f, id, record, name, offset)
}

func (s *Store) appendAndDelete(ctx context.Context, f *os.File, id int64, record *decisionRecord, name string, offset int64) error {
	record.MovedAt = s.now().UnixMilli()
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := f.Write(line); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if s.decisions.afterAppend != nil {
		s.decisions.afterAppend(id)
	}
	return s.deleteMoved(ctx, id, record.Reference, record.Revision, name, offset, int64(len(line)), record.MovedAt)
}

// deleteMoved deletes the rows only while the item still has the marked revision, state
// and mark; an edit or verdict in between cleared the mark and keeps the rows.
func (s *Store) deleteMoved(ctx context.Context, id int64, reference string, revision int64, name string, offset, length, at int64) error {
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	defer tx.Rollback()
	var marked int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_items WHERE id=? AND state='archived' AND revision=? AND mark_revision=?
		AND mark_file=? AND mark_offset=?`, id, revision, revision, name, offset).Scan(&marked); err != nil {
		return err
	}
	if marked == 0 {
		return nil
	}
	for _, query := range []string{
		`DELETE FROM decision_deliveries WHERE verdict_id IN (SELECT id FROM decision_verdicts WHERE item_id=?)`,
		`DELETE FROM decision_verdicts WHERE item_id=?`,
		`DELETE FROM decision_recipients WHERE item_id=?`,
		`DELETE FROM decision_items WHERE id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return tx.fail(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO decision_deep_archive(reference,file,offset,length,moved_at) VALUES (?,?,?,?,?)`,
		reference, name, offset, length, at); err != nil {
		return tx.fail(err)
	}
	if err := tx.Commit(); err != nil && !errors.Is(err, ErrCapacity) {
		return err
	}
	return nil
}

// finishMove completes a move that a crash interrupted: a complete line for the marked
// revision at the marked offset finishes the delete; otherwise the line is written again at
// the end of the file.
func (s *Store) finishMove(ctx context.Context, id int64) error {
	var name string
	var offset, revision int64
	var reference string
	if err := s.db.QueryRowContext(ctx, `SELECT mark_file,mark_offset,mark_revision,reference FROM decision_items WHERE id=? AND mark_revision>0`, id).
		Scan(&name, &offset, &revision, &reference); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	line, err := s.readDecisionLine(name, offset)
	if err != nil {
		return err
	}
	var found decisionRecord
	if line != nil && json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &found) == nil && found.Record == decisionRecordMarker &&
		found.ID == id && found.Revision == revision {
		return s.deleteMoved(ctx, id, reference, revision, name, offset, int64(len(line)), found.MovedAt)
	}
	f, end, err := s.openDecisionLog(name)
	if err != nil {
		return err
	}
	defer f.Close()
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE decision_items SET mark_offset=? WHERE id=? AND mark_revision=? AND mark_file=? AND mark_offset=?`,
		end, id, revision, name, offset)
	if err != nil {
		return tx.fail(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	record, err := readDecisionRecord(ctx, s.db, id)
	if err != nil || record == nil {
		return err
	}
	return s.appendAndDelete(ctx, f, id, record, name, end)
}

// deepArchived reports a moved reference's locator, or false.
func deepArchived(ctx context.Context, q querier, reference string) (int64, bool, error) {
	var at int64
	err := q.QueryRowContext(ctx, `SELECT moved_at FROM decision_deep_archive WHERE reference=?`, reference).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return at, err == nil, err
}

// DecisionRestore brings a deep-archived item back as archived, with a new 30-day period.
// It reads the one line at the locator, and the line stays in the log.
func (s *Store) DecisionRestore(ctx context.Context, caller Key, reference string) (DecisionItem, error) {
	if !validKey(caller.Family, caller.ID) {
		return DecisionItem{}, ErrInvalid
	}
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	var name string
	var offset, length int64
	err := s.db.QueryRowContext(ctx, `SELECT file,offset,length FROM decision_deep_archive WHERE reference=?`, reference).Scan(&name, &offset, &length)
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionItem{}, workError("not_found", "no deep-archived item has this reference")
	}
	if err != nil {
		return DecisionItem{}, err
	}
	line, err := s.readDecisionLine(name, offset)
	if err != nil {
		return DecisionItem{}, err
	}
	var record decisionRecord
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSuffix(line, []byte{'\n'})))
	dec.UseNumber()
	if line == nil || int64(len(line)) != length || dec.Decode(&record) != nil ||
		record.Record != decisionRecordMarker || record.Reference != reference {
		return DecisionItem{}, workError("decision_unavailable", "the deep-archive line does not match its locator")
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return DecisionItem{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return DecisionItem{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM decision_deep_archive WHERE reference=? AND file=? AND offset=?`, reference, name, offset)
	if err != nil {
		return DecisionItem{}, tx.fail(err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return DecisionItem{}, workError("not_found", "the item was restored or moved again")
	}
	record.Item["state"], record.Item["archived_at"], record.Item["updated_at"] = "archived", now, now
	record.Item["mark_revision"], record.Item["mark_file"], record.Item["mark_offset"] = 0, "", 0
	for table, rows := range map[string][]map[string]any{"decision_items": {record.Item}, "decision_recipients": record.Recipients,
		"decision_verdicts": record.Verdicts, "decision_deliveries": record.Deliveries} {
		for _, row := range rows {
			if err := insertRow(ctx, tx, table, row); err != nil {
				return DecisionItem{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return DecisionItem{}, err
	}
	return s.decisionRead(ctx, reference)
}

// insertRow inserts a restored row into one of the decision tables. Column names come from
// the table's own schema, never from the line.
func insertRow(ctx context.Context, tx *writeTx, table string, row map[string]any) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	var columns []string
	var values []any
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		if v, ok := row[c]; ok {
			columns = append(columns, c)
			if n, ok := v.(json.Number); ok {
				if i, err := n.Int64(); err == nil {
					v = i
				} else {
					v = n.String()
				}
			}
			values = append(values, v)
		}
	}
	rows.Close()
	if len(columns) == 0 {
		return workError("decision_unavailable", "a deep-archive row has no known column")
	}
	marks := make([]byte, 0, 2*len(columns))
	for i := range columns {
		if i > 0 {
			marks = append(marks, ',')
		}
		marks = append(marks, '?')
	}
	query := "INSERT INTO " + table + "(" + joinColumns(columns) + ") VALUES (" + string(marks) + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		if isConstraint(err) {
			return workError("reference_in_use", "another decision item uses this reference or ID")
		}
		return tx.fail(err)
	}
	return nil
}

func joinColumns(columns []string) string {
	var b bytes.Buffer
	for i, c := range columns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(c)
	}
	return b.String()
}

func isConstraint(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("constraint"))
}
