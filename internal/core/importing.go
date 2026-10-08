package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// An import writes the records of one Python-era source in their mapped representation
// (sprint chunk 11, PROTOCOL.md "Import of Python-era state"). The importer reads and
// maps the source; this file validates the rows against the published schema, writes
// them in one transaction, reads them back and compares both by canonical digest.

// ImportTable is one table's rows of a source, in the columns the importer names.
type ImportTable struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// ImportSource is one Python-era source. Scope names the records it owns: the family
// and session ID of an inbox, or the repository and store ID of a memory store.
type ImportSource struct {
	Path   string           `json:"path"`
	Kind   string           `json:"kind"`
	Scope  [2]string        `json:"scope"`
	Cutoff json.RawMessage  `json:"cutoff"`
	Counts map[string]int64 `json:"counts"`
	Tables []ImportTable    `json:"tables"`
	// Digest is set by PrepareImport over the normalized tables.
	Digest string `json:"digest"`
}

// ImportRecord is a source that this database has imported.
type ImportRecord struct {
	Source string           `json:"source"`
	Kind   string           `json:"kind"`
	Digest string           `json:"digest"`
	Cutoff json.RawMessage  `json:"cutoff"`
	Counts map[string]int64 `json:"counts"`
	At     int64            `json:"at"`
}

// importScopes are the tables each kind may write, with the filter that selects a
// source's rows again for verification: ?1 and ?2 are the source's scope.
var importScopes = map[string]map[string]string{
	"inbox": {
		"sessions": "family=?1 AND id=?2",
		"names":    "kind='peer' AND family=?1 AND session_id=?2",
		"messages": "recipient_family=?1 AND recipient_id=?2",
	},
	"memory": {
		"memory_stores":         "repository=?1 AND store_id=?2",
		"memory_entries":        "repository=?1",
		"memory_idem":           "repository=?1",
		"memory_cursors":        "repository=?1",
		"memory_retired":        "repository=?1",
		"memory_snapshots":      "repository=?1",
		"memory_snapshot_items": "id IN (SELECT id FROM memory_snapshots WHERE repository=?1)",
		"work_items":            "store=?2",
		"work_scope_revisions":  "store=?2",
		"claim_bundles":         "store=?2",
		"claim_resources":       "store=?2",
		"work_events":           "store=?2",
		"work_replays":          "store=?2",
	},
}

var importTypes struct {
	once    sync.Once
	columns map[string]map[string]string
	err     error
}

// columnTypes reads each published table's declared column types from a reference
// database of the current schema.
func columnTypes() (map[string]map[string]string, error) {
	importTypes.once.Do(func() {
		db, err := sql.Open("sqlite", "file::memory:")
		if err != nil {
			importTypes.err = err
			return
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		if importTypes.err = migrate(db, 0, schemaVersion); importTypes.err != nil {
			return
		}
		importTypes.columns = map[string]map[string]string{}
		for _, tables := range importScopes {
			for table := range tables {
				rows, err := db.Query(`SELECT name,type FROM pragma_table_info(?)`, table)
				if err != nil {
					importTypes.err = err
					return
				}
				types := map[string]string{}
				for rows.Next() {
					var name, kind string
					if err := rows.Scan(&name, &kind); err != nil {
						rows.Close()
						importTypes.err = err
						return
					}
					types[name] = strings.ToUpper(kind)
				}
				rows.Close()
				importTypes.columns[table] = types
			}
		}
	})
	return importTypes.columns, importTypes.err
}

func importRefusal(code, message string) error { return Refusal{code, message} }

// normalize converts a value to the storage class its column's affinity gives it, so
// that a digest over the mapped rows equals a digest over the rows read back.
func normalize(kind string, value any) (any, error) {
	switch v := value.(type) {
	case nil, string:
		return v, nil
	case []byte:
		return string(v), nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	case int:
		value = int64(v)
	case float32:
		value = float64(v)
	}
	switch v := value.(type) {
	case int64:
		if kind == "REAL" {
			return float64(v), nil
		}
		return v, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, importRefusal("source_invalid", "a source value is not a finite number")
		}
		if kind == "INTEGER" && v == math.Trunc(v) && math.Abs(v) < 1<<53 {
			return int64(v), nil
		}
		return v, nil
	}
	return nil, importRefusal("source_invalid", fmt.Sprintf("unsupported source value type %T", value))
}

// encode is one value's canonical text; its storage class is part of it.
func encode(value any) string {
	switch v := value.(type) {
	case nil:
		return "n"
	case int64:
		return "i" + strconv.FormatInt(v, 10)
	case float64:
		return "f" + strconv.FormatFloat(v, 'g', -1, 64)
	case string:
		return "s" + strconv.Quote(v)
	}
	return "?"
}

// digestTables is the canonical digest of tables: tables by name, rows as sets.
func digestTables(tables []ImportTable) string {
	type canonical struct {
		Name    string   `json:"name"`
		Columns []string `json:"columns"`
		Rows    []string `json:"rows"`
	}
	list := []canonical{}
	for _, t := range tables {
		c := canonical{Name: t.Name, Columns: t.Columns, Rows: []string{}}
		for _, row := range t.Rows {
			parts := make([]string, len(row))
			for i, value := range row {
				parts[i] = encode(value)
			}
			c.Rows = append(c.Rows, strings.Join(parts, ","))
		}
		sort.Strings(c.Rows)
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, _ := json.Marshal(list)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// PrepareImport validates a mapped source against the published schema, normalizes
// its values and sets its digest. A table may appear once; every row has one value
// per named column.
func PrepareImport(src *ImportSource) error {
	types, err := columnTypes()
	if err != nil {
		return err
	}
	scopes, ok := importScopes[src.Kind]
	if !ok || src.Path == "" || src.Scope[0] == "" || (src.Kind == "inbox" && src.Scope[1] == "") {
		return importRefusal("source_invalid", "an import source needs a path, a kind and a scope")
	}
	seen := map[string]bool{}
	for ti := range src.Tables {
		t := &src.Tables[ti]
		if _, ok := scopes[t.Name]; !ok || seen[t.Name] {
			return importRefusal("source_invalid", "table "+t.Name+" is not importable for "+src.Kind)
		}
		seen[t.Name] = true
		columns := map[string]bool{}
		for _, c := range t.Columns {
			if _, ok := types[t.Name][c]; !ok || columns[c] {
				return importRefusal("source_invalid", "column "+t.Name+"."+c+" is not importable")
			}
			columns[c] = true
		}
		for _, row := range t.Rows {
			if len(row) != len(t.Columns) {
				return importRefusal("source_invalid", "a row of "+t.Name+" does not match its columns")
			}
			for i, value := range row {
				if row[i], err = normalize(types[t.Name][t.Columns[i]], value); err != nil {
					return err
				}
			}
		}
	}
	if len(src.Cutoff) == 0 {
		src.Cutoff = json.RawMessage(`{}`)
	}
	if !json.Valid(src.Cutoff) {
		return importRefusal("source_invalid", "the source cutoff is not JSON")
	}
	src.Digest = digestTables(src.Tables)
	return nil
}

// readBack selects a source's rows again, in the columns it named.
func readBack(ctx context.Context, q querier, src ImportSource) ([]ImportTable, error) {
	scopes := importScopes[src.Kind]
	result := []ImportTable{}
	for _, t := range src.Tables {
		rows, err := q.QueryContext(ctx, `SELECT `+strings.Join(t.Columns, ",")+` FROM `+t.Name+` WHERE `+scopes[t.Name], src.Scope[0], src.Scope[1])
		if err != nil {
			return nil, err
		}
		read := ImportTable{Name: t.Name, Columns: t.Columns}
		for rows.Next() {
			values := make([]any, len(t.Columns))
			pointers := make([]any, len(t.Columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			read.Rows = append(read.Rows, values)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		result = append(result, read)
	}
	return result, nil
}

func scanImport(row scanner) (ImportRecord, error) {
	var r ImportRecord
	var cutoff, counts string
	if err := row.Scan(&r.Source, &r.Kind, &r.Digest, &cutoff, &counts, &r.At); err != nil {
		return r, err
	}
	r.Cutoff = json.RawMessage(cutoff)
	if err := json.Unmarshal([]byte(counts), &r.Counts); err != nil {
		return r, err
	}
	return r, nil
}

// Import writes one prepared source in one transaction and verifies it by reading it
// back. A source this database already imported with the same digest is skipped and
// reports true; one imported with another digest is refused as source_changed.
func (s *Store) Import(ctx context.Context, src ImportSource) (bool, error) {
	if src.Digest == "" {
		return false, importRefusal("source_invalid", "the source was not prepared")
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		if errors.Is(err, ErrCapacity) {
			return false, importRefusal("capacity", "the Go state database cannot hold this import; nothing was written")
		}
		return false, err
	}
	defer tx.Rollback()
	previous, err := scanImport(tx.QueryRowContext(ctx, `SELECT source,kind,digest,cutoff,counts,at FROM imports WHERE source=?`, src.Path))
	switch {
	case err == nil && previous.Digest == src.Digest && previous.Kind == src.Kind:
		return true, nil
	case err == nil:
		return false, importRefusal("source_changed", "source "+src.Path+" was imported with another content")
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	for _, t := range src.Tables {
		if len(t.Rows) == 0 {
			continue
		}
		statement := `INSERT INTO ` + t.Name + `(` + strings.Join(t.Columns, ",") + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?,", len(t.Columns)), ",") + `)`
		for _, row := range t.Rows {
			if _, err := tx.ExecContext(ctx, statement, row...); err != nil {
				if strings.Contains(err.Error(), "constraint") {
					return false, importRefusal("source_conflict", "a record of "+src.Path+" conflicts with an imported record: "+err.Error())
				}
				return false, s.importFull(tx, err)
			}
		}
	}
	read, err := readBack(ctx, tx, src)
	if err != nil {
		return false, err
	}
	if digestTables(read) != src.Digest {
		return false, importRefusal("verify_failed", "the records of "+src.Path+" read back differ from the source")
	}
	if src.Kind == "memory" {
		// The store must fit the full ceilings its Python store kept, before the reserve.
		l := s.limits()
		u, err := usage(ctx, tx, src.Scope[0])
		if err != nil {
			return false, err
		}
		d, err := storeDebt(ctx, tx, src.Scope[0])
		if err != nil {
			return false, err
		}
		if err := l.exceeded(u, l.caps(control, d), 0, 0); err != nil {
			return false, importRefusal("capacity", "memory store "+src.Path+" exceeds the Go memory capacity: "+err.Error())
		}
	}
	counts, _ := json.Marshal(src.Counts)
	if _, err := tx.ExecContext(ctx, `INSERT INTO imports(source,kind,digest,cutoff,counts,at) VALUES (?,?,?,?,?,?)`,
		src.Path, src.Kind, src.Digest, string(src.Cutoff), string(counts), s.now().UnixMilli()); err != nil {
		return false, s.importFull(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return false, s.importFull(tx, err)
	}
	return false, nil
}

func (s *Store) importFull(tx *writeTx, err error) error {
	if errors.Is(tx.fail(err), ErrCapacity) {
		return importRefusal("capacity", "the Go state database cannot hold this import; nothing was written")
	}
	return err
}

// VerifyImport compares a prepared source with this database: the import record's
// digest and the records read back must both equal the source's digest.
func (s *Store) VerifyImport(ctx context.Context, src ImportSource) error {
	previous, err := scanImport(s.db.QueryRowContext(ctx, `SELECT source,kind,digest,cutoff,counts,at FROM imports WHERE source=?`, src.Path))
	if errors.Is(err, sql.ErrNoRows) {
		return importRefusal("not_imported", "source "+src.Path+" has not been imported")
	}
	if err != nil {
		return err
	}
	if previous.Digest != src.Digest {
		return importRefusal("source_changed", "source "+src.Path+" differs from its import")
	}
	read, err := readBack(ctx, s.db, src)
	if err != nil {
		return err
	}
	if digestTables(read) != src.Digest {
		return importRefusal("verify_failed", "the records of "+src.Path+" differ from the source")
	}
	return nil
}

// ImportRecords lists the sources this database has imported.
func (s *Store) ImportRecords(ctx context.Context) ([]ImportRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source,kind,digest,cutoff,counts,at FROM imports ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ImportRecord{}
	for rows.Next() {
		r, err := scanImport(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Empty reports whether the database holds nothing but the built-in maintainer session.
func (s *Store) Empty(ctx context.Context) (bool, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sessions WHERE family!='maintainer')+
		(SELECT COUNT(*) FROM names WHERE name!='maintainer')+
		(SELECT COUNT(*) FROM messages)+(SELECT COUNT(*) FROM launches)+
		(SELECT COUNT(*) FROM memory_stores)+(SELECT COUNT(*) FROM memory_entries)+
		(SELECT COUNT(*) FROM work_items)+(SELECT COUNT(*) FROM audit)+(SELECT COUNT(*) FROM imports)`).Scan(&n)
	return n == 0, err
}

// OpenImportStore opens the state database file name in a private root for an import,
// outside a running daemon. The caller holds the root's daemon lock.
func OpenImportStore(root, name string) (*Store, error) {
	if name == "" || strings.ContainsAny(name, "/\x00") {
		return nil, errors.New("invalid state database name")
	}
	return openStoreFile(root, name)
}

// Close checkpoints the log into the database file and closes it, so the file alone
// holds every committed record and may be renamed.
func (s *Store) Close() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return errors.Join(err, s.db.Close())
}
