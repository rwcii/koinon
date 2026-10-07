package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/rwcii/koinon/internal/core"
)

// The sender family of an imported message: a Python-era sender is identified only by
// the address and name it asserted.
const legacyFamily = "legacy"

// The fingerprint scheme of imported replay rows, so no Go request matches them.
const legacyScheme = "py1"

var envelope = regexp.MustCompile(`(?s)\A<cross-session-message from="([^"]*)"(?: from-name="([^"]*)")?>\n(.*)\n</cross-session-message>\z`)

// Skipped counts the source rows an import does not carry, by kind.
type Skipped map[string]int64

func columns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func query(ctx context.Context, db *sql.DB, statement string, args ...any) ([][]any, error) {
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
		}
		result = append(result, values)
	}
	return result, rows.Err()
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	case string:
		var i int64
		_, err := fmt.Sscan(n, &i)
		return i, err == nil
	}
	return 0, false
}

// mapInbox maps a captured inbox to a session row, its peer name and its messages.
func mapInbox(ctx context.Context, db *sql.DB, s Source) (core.ImportSource, Skipped, error) {
	skipped := Skipped{}
	src := core.ImportSource{Path: s.Path, Kind: "inbox", Scope: [2]string{s.Family, s.Thread}, Counts: map[string]int64{}}
	inboxColumns, err := columns(ctx, db, "inbox")
	if err != nil {
		return src, nil, err
	}
	if !inboxColumns["seq"] || !inboxColumns["frame"] {
		return src, nil, fmt.Errorf("source_invalid: %s has no inbox table", s.Path)
	}
	kind := "'peer'"
	if inboxColumns["kind"] {
		kind = "kind"
	}
	rows, err := query(ctx, db, `SELECT seq,received,frame,`+kind+` FROM inbox ORDER BY seq`)
	if err != nil {
		return src, nil, err
	}
	var ack, last int64
	meta, err := query(ctx, db, `SELECT value FROM inbox_meta WHERE key='ack_through'`)
	if err == nil && len(meta) == 1 {
		if v, ok := integer(meta[0][0]); ok {
			ack = v
		}
	}
	if sequence, err := query(ctx, db, `SELECT seq FROM sqlite_sequence WHERE name='inbox'`); err == nil && len(sequence) == 1 {
		last, _ = integer(sequence[0][0])
	}
	// The delivery ledger's last notice outcome for each sequence.
	outcomes := map[int64]string{}
	if ledger, err := columns(ctx, db, "delivery_record"); err == nil && ledger["data"] {
		records, err := query(ctx, db, `SELECT seq,data FROM delivery_record WHERE seq IS NOT NULL`)
		if err != nil {
			return src, nil, err
		}
		for _, r := range records {
			seq, _ := integer(r[0])
			var data struct {
				Stages map[string]struct {
					Outcome string `json:"outcome"`
				} `json:"stages"`
			}
			text, _ := r[1].(string)
			if json.Unmarshal([]byte(text), &data) == nil {
				if n, ok := data.Stages["notified"]; ok {
					outcomes[seq] = n.Outcome
				}
			}
		}
	}
	var messages [][]any
	var newest int64
	for _, r := range rows {
		seq, _ := integer(r[0])
		if seq > last {
			last = seq
		}
		if r[3] != "peer" {
			skipped[fmt.Sprint(r[3])]++
			continue
		}
		var frame struct {
			Type    string `json:"type"`
			From    string `json:"from"`
			Message struct {
				Content *string `json:"content"`
			} `json:"message"`
		}
		text, _ := r[2].(string)
		if json.Unmarshal([]byte(text), &frame) != nil || frame.Type != "user" || frame.Message.Content == nil {
			skipped[frameKind(frame.Type)]++
			continue
		}
		// The body is the content unchanged; an envelope only names the sender.
		body, id, name := *frame.Message.Content, frame.From, ""
		if m := envelope.FindStringSubmatch(body); m != nil {
			id, name = m[1], m[2]
		}
		if id == "" {
			id = "unknown"
		}
		if name == "" {
			name = id
		}
		received, _ := number(r[1])
		created := int64(received * 1000)
		if created > newest {
			newest = created
		}
		state := "waiting"
		switch {
		case outcomes[seq] == "delivered" || seq <= s.Checkpoint:
			state = "notified"
		case outcomes[seq] == "unknown":
			state = "uncertain"
		}
		messages = append(messages, []any{s.Family, s.Thread, seq, legacyFamily, id, name, body, created, state, created})
	}
	if ack > last {
		return src, nil, fmt.Errorf("source_invalid: %s acknowledges beyond its last sequence", s.Path)
	}
	// The session imports expired, without a wake target; its next registration with the
	// same family and ID gets this inbox. Its times come from the source alone, so a
	// capture of the same source maps to the same rows.
	src.Tables = append(src.Tables, core.ImportTable{Name: "sessions",
		Columns: []string{"family", "id", "repository", "directory", "wake_target", "registered_at", "renewed_at", "expires_at", "retired_at", "revision", "last_seq", "acked_through"},
		Rows:    [][]any{{s.Family, s.Thread, "", "", "{}", newest, newest, newest, int64(0), int64(1), last, ack}}})
	if s.Name != "" {
		src.Tables = append(src.Tables, core.ImportTable{Name: "names",
			Columns: []string{"name", "kind", "family", "session_id", "repository", "holder_id"},
			Rows:    [][]any{{s.Name, "peer", s.Family, s.Thread, "", ""}}})
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "messages",
		Columns: []string{"recipient_family", "recipient_id", "seq", "sender_family", "sender_id", "sender_name", "body", "created_at", "delivery_state", "delivery_updated_at"},
		Rows:    messages})
	cutoff, _ := json.Marshal(map[string]int64{"last_seq": last, "ack_through": ack})
	src.Cutoff = cutoff
	src.Counts["messages"] = int64(len(messages))
	src.Counts["last_seq"] = last
	src.Counts["ack_through"] = ack
	return src, skipped, nil
}

func frameKind(t string) string {
	if t == "control" {
		return "control"
	}
	return "invalid_frame"
}

var workColumns = map[string][]string{
	"work_items": {"work_id", "revision", "lifecycle", "title", "criteria", "non_goals", "proposed_assignee", "created_at",
		"created_consumer", "first_start_revision", "scope_revision", "progress_epoch", "last_progress_at", "progress_deadline",
		"progress", "checkpoint", "next_artifact", "blocker", "last_writer", "last_generation", "last_lease_expires",
		"lease_expired", "outcome", "reason", "references_json", "finished_at", "expires_at", "latest_seq"},
	"work_scope_revisions": {"work_id", "revision", "ts", "consumer", "author", "title", "criteria", "non_goals"},
	"claim_bundles": {"generation", "work_id", "consumer", "revision", "issued_at", "renewed_at", "expires_at", "progress_epoch",
		"overdue_recorded", "active", "overdue_credit", "end_credit"},
	"claim_resources": {"generation", "ordinal", "kind", "resource"},
	"work_events":     {"seq", "work_id", "revision", "kind", "payload"},
}

// mapMemory maps a captured memory store to the Go memory and work tables of its
// repository. The store metadata keeps its source values; counters are never rebuilt.
func mapMemory(ctx context.Context, db *sql.DB, s Source) (core.ImportSource, Skipped, error) {
	skipped := Skipped{}
	src := core.ImportSource{Path: s.Path, Kind: "memory", Counts: map[string]int64{}}
	metaRows, err := query(ctx, db, `SELECT key,value FROM meta`)
	if err != nil {
		return src, nil, fmt.Errorf("source_invalid: %s has no memory metadata", s.Path)
	}
	meta := map[string]any{}
	for _, r := range metaRows {
		meta[fmt.Sprint(r[0])] = r[1]
	}
	if repo, _ := meta["repo"].(string); repo != s.Key {
		return src, nil, fmt.Errorf("source_invalid: %s records repository key %q, not its directory's %s", s.Path, meta["repo"], s.Key)
	}
	storeID, _ := meta["store_id"].(string)
	if len(storeID) != 32 {
		return src, nil, fmt.Errorf("source_invalid: %s has no 32-character store ID", s.Path)
	}
	counter := func(name string) int64 {
		v, _ := integer(meta[name])
		return v
	}
	src.Scope = [2]string{s.Repository, storeID}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_stores",
		Columns: []string{"repository", "store_id", "head", "floor", "work_counter", "claim_counter"},
		Rows:    [][]any{{s.Repository, storeID, counter("head"), counter("floor"), counter("work_id_counter"), counter("claim_generation")}}})

	entries, err := query(ctx, db, `SELECT seq,ts,type,scope,scope_target,path,body,author,consumer,revision,supersedes,
		revokes,superseded_by,revoked_by,conflicts_with,expires FROM entries ORDER BY seq`)
	if err != nil {
		return src, nil, err
	}
	entryRows := [][]any{}
	for _, r := range entries {
		consumer := r[8]
		if consumer == nil {
			consumer = ""
		}
		// The writer is the reported author, else the consumer; the family is legacy.
		writer := r[7]
		if w, _ := writer.(string); w == "" {
			writer = consumer
		}
		for i, column := range []string{"seq", "ts", "type", "scope", "body", "revision"} {
			index := []int{0, 1, 2, 3, 6, 9}[i]
			if r[index] == nil {
				return src, nil, fmt.Errorf("source_invalid: an entry of %s has no %s", s.Path, column)
			}
		}
		entryRows = append(entryRows, []any{s.Repository, r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], legacyFamily, writer, consumer,
			r[9], r[10], r[11], r[12], r[13], r[14], r[15]})
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_entries",
		Columns: []string{"repository", "seq", "ts", "type", "scope", "scope_target", "path", "body", "author", "writer_family",
			"writer_name", "consumer", "revision", "supersedes", "revokes", "superseded_by", "revoked_by", "conflicts_with", "expires"},
		Rows: entryRows})

	idemColumns, err := columns(ctx, db, "idem")
	if err != nil {
		return src, nil, err
	}
	operation, result := "'note'", "NULL"
	if idemColumns["operation"] {
		operation, result = "operation", "result"
	}
	idem, err := query(ctx, db, `SELECT key,fingerprint,seq,ts,deadline,`+operation+`,`+result+` FROM idem ORDER BY key`)
	if err != nil {
		return src, nil, err
	}
	noteRows, replayRows := [][]any{}, [][]any{}
	for _, r := range idem {
		key, _ := r[0].(string)
		if r[5] == "note" {
			// A note key is repository NUL consumer NUL key.
			parts := strings.SplitN(key, "\x00", 3)
			if len(parts) != 3 || parts[0] != s.Key || r[2] == nil {
				return src, nil, fmt.Errorf("source_invalid: %s holds an unreadable note replay key", s.Path)
			}
			noteRows = append(noteRows, []any{s.Repository, parts[1], parts[2], legacyScheme, r[1], r[2], r[3], r[4]})
			continue
		}
		// A work replay key is a digest of repository, consumer and key; the consumer
		// cannot be recovered, so it stays empty and no Go request matches the row.
		if r[6] == nil {
			return src, nil, fmt.Errorf("source_invalid: a work replay of %s has no result", s.Path)
		}
		replayRows = append(replayRows, []any{storeID, "", key, r[5], legacyScheme, r[1], r[2], r[3], r[4], r[6]})
	}
	src.Tables = append(src.Tables,
		core.ImportTable{Name: "memory_idem", Columns: []string{"repository", "consumer", "key", "scheme", "fingerprint", "seq", "ts", "deadline"}, Rows: noteRows},
		core.ImportTable{Name: "work_replays", Columns: []string{"store", "consumer", "key", "operation", "scheme", "fingerprint", "seq", "ts", "deadline", "result"}, Rows: replayRows})

	cursors, err := query(ctx, db, `SELECT consumer,seq,issued,snapshot,bootstrapped,resnapshot,updated FROM cursors ORDER BY consumer`)
	if err != nil {
		return src, nil, err
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_cursors",
		Columns: []string{"repository", "consumer", "seq", "issued", "snapshot", "bootstrapped", "resnapshot", "updated"},
		Rows:    prefix(s.Repository, cursors)})
	retired, err := query(ctx, db, `SELECT consumer,seq,at FROM retired ORDER BY consumer`)
	if err != nil {
		return src, nil, err
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_retired", Columns: []string{"repository", "consumer", "seq", "at"}, Rows: prefix(s.Repository, retired)})
	// A pending snapshot's acked is NULL in Python and 0 in Go.
	snapshots, err := query(ctx, db, `SELECT id,consumer,head,created,items,issued,COALESCE(acked,0),acked_at FROM snapshots ORDER BY id`)
	if err != nil {
		return src, nil, err
	}
	snapshotRows := [][]any{}
	for _, r := range snapshots {
		snapshotRows = append(snapshotRows, append([]any{r[0], s.Repository}, r[1:]...))
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_snapshots",
		Columns: []string{"id", "repository", "consumer", "head", "created", "items", "issued", "acked", "acked_at"}, Rows: snapshotRows})
	items, err := query(ctx, db, `SELECT id,position,seq,payload,bytes FROM snapshot_items ORDER BY id,position`)
	if err != nil {
		return src, nil, err
	}
	src.Tables = append(src.Tables, core.ImportTable{Name: "memory_snapshot_items", Columns: []string{"id", "position", "seq", "payload", "bytes"}, Rows: items})

	for _, table := range []string{"work_items", "work_scope_revisions", "claim_bundles", "claim_resources", "work_events"} {
		present, err := columns(ctx, db, table)
		if err != nil {
			return src, nil, err
		}
		if len(present) == 0 {
			continue
		}
		rows, err := query(ctx, db, `SELECT `+strings.Join(workColumns[table], ",")+` FROM `+table)
		if err != nil {
			return src, nil, fmt.Errorf("source_invalid: %s table %s: %w", s.Path, table, err)
		}
		src.Tables = append(src.Tables, core.ImportTable{Name: table, Columns: append([]string{"store"}, workColumns[table]...), Rows: prefix(storeID, rows)})
	}
	if search, err := query(ctx, db, `SELECT 1 FROM sqlite_master WHERE name='search'`); err == nil && len(search) == 1 {
		skipped["search_index"]++
	}
	cutoff, _ := json.Marshal(map[string]int64{"head": counter("head"), "work_counter": counter("work_id_counter"), "claim_counter": counter("claim_generation")})
	src.Cutoff = cutoff
	src.Counts["entries"] = int64(len(entryRows))
	src.Counts["head"] = counter("head")
	for _, t := range src.Tables {
		switch t.Name {
		case "memory_cursors", "memory_retired", "memory_snapshots", "work_items", "claim_bundles", "work_events":
			src.Counts[strings.TrimPrefix(t.Name, "memory_")] = int64(len(t.Rows))
		}
	}
	src.Counts["replays"] = int64(len(noteRows) + len(replayRows))
	return src, skipped, nil
}

func prefix(value any, rows [][]any) [][]any {
	result := make([][]any, 0, len(rows))
	for _, r := range rows {
		result = append(result, append([]any{value}, r...))
	}
	return result
}

// Map maps one capture of source s.
func Map(ctx context.Context, capture string, s Source) (core.ImportSource, Skipped, error) {
	db, err := openSource(capture)
	if err != nil {
		return core.ImportSource{}, nil, err
	}
	defer db.Close()
	var src core.ImportSource
	var skipped Skipped
	switch s.Kind {
	case "inbox":
		src, skipped, err = mapInbox(ctx, db, s)
	case "memory":
		src, skipped, err = mapMemory(ctx, db, s)
	default:
		err = errors.New("unknown source kind")
	}
	if err != nil {
		return src, nil, err
	}
	if err := core.PrepareImport(&src); err != nil {
		return src, nil, err
	}
	return src, skipped, nil
}
