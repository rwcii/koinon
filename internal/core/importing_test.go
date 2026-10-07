package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Import tests (sprint chunk 11) on hand-built sources; the importer package tests the
// mapping of real Python-era trees.

func inboxSource(id string) ImportSource {
	return ImportSource{Path: "/py/sessions/" + id + "/inbox.sqlite3", Kind: "inbox", Scope: [2]string{"codex", id},
		Counts: map[string]int64{"messages": 3},
		Tables: []ImportTable{
			{Name: "sessions", Columns: []string{"family", "id", "repository", "directory", "wake_target", "registered_at", "renewed_at", "expires_at", "retired_at", "revision", "last_seq", "acked_through"},
				Rows: [][]any{{"codex", id, "", "", "{}", int64(1000), int64(1000), int64(1000), int64(0), int64(1), int64(5), int64(2)}}},
			{Name: "names", Columns: []string{"name", "kind", "family", "session_id", "repository", "holder_id"},
				Rows: [][]any{{"codex-python-" + id, "peer", "codex", id, "", ""}}},
			{Name: "messages", Columns: []string{"recipient_family", "recipient_id", "seq", "sender_family", "sender_id", "sender_name", "body", "created_at", "delivery_state", "delivery_updated_at"},
				Rows: [][]any{
					{"codex", id, int64(3), "legacy", "uds:/tmp/s.sock", "claude-x", "third", int64(900), "notified", int64(900)},
					{"codex", id, int64(4), "legacy", "uds:/tmp/s.sock", "claude-x", "fourth", int64(950), "waiting", int64(950)},
					{"codex", id, 5.0, "legacy", "uds:/tmp/s.sock", "claude-x", "fifth", int64(990), "waiting", int64(990)},
				}},
		}}
}

func TestImportRecordsVerifiesAndSkips(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	src := inboxSource("thread-1")
	if err := PrepareImport(&src); err != nil {
		t.Fatal(err)
	}
	if skipped, err := s.Import(ctx, src); err != nil || skipped {
		t.Fatalf("import: %v %v", skipped, err)
	}
	if skipped, err := s.Import(ctx, src); err != nil || !skipped {
		t.Fatalf("second import: %v %v", skipped, err)
	}
	if err := s.VerifyImport(ctx, src); err != nil {
		t.Fatal(err)
	}
	records, err := s.ImportRecords(ctx)
	if err != nil || len(records) != 1 || records[0].Digest != src.Digest || records[0].Counts["messages"] != 3 {
		t.Fatalf("records %+v %v", records, err)
	}
	if empty, err := s.Empty(ctx); err != nil || empty {
		t.Fatalf("empty after import: %v %v", empty, err)
	}
	// A source whose content changed is refused, and a corrupted row fails verification.
	changed := inboxSource("thread-1")
	changed.Tables[2].Rows[0][6] = "edited"
	if err := PrepareImport(&changed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, changed); !refusedAs(err, "source_changed") {
		t.Fatalf("changed source: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET body='tampered' WHERE seq=4`); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyImport(ctx, src); !refusedAs(err, "verify_failed") {
		t.Fatalf("tampered row: %v", err)
	}
}

func refusedAs(err error, code string) bool {
	var r Refusal
	return errors.As(err, &r) && r.Code == code
}

func TestPrepareImportRefusesUnknownShapes(t *testing.T) {
	for name, change := range map[string]func(*ImportSource){
		"table":  func(s *ImportSource) { s.Tables[0].Name = "audit" },
		"column": func(s *ImportSource) { s.Tables[0].Columns[0] = "wake_attempts_x" },
		"width":  func(s *ImportSource) { s.Tables[2].Rows[0] = s.Tables[2].Rows[0][:3] },
		"kind":   func(s *ImportSource) { s.Kind = "launches" },
		"value":  func(s *ImportSource) { s.Tables[2].Rows[0][7] = struct{}{} },
	} {
		src := inboxSource("thread-1")
		change(&src)
		if err := PrepareImport(&src); !refusedAs(err, "source_invalid") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestImportCapacityWritesNothing(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	entries := [][]any{}
	for i := int64(1); i <= defaultMemoryLimits.entries+1; i++ {
		entries = append(entries, []any{"/repo/.git", i, 1.0, "finding", "repo", nil, nil, "body", nil, "legacy", "c", "c", int64(1), nil, nil, nil, nil, nil, nil})
	}
	src := ImportSource{Path: "/py/memory/k/memory.sqlite3", Kind: "memory", Scope: [2]string{"/repo/.git", strings.Repeat("a", 32)},
		Tables: []ImportTable{
			{Name: "memory_stores", Columns: []string{"repository", "store_id", "head", "floor", "work_counter", "claim_counter"},
				Rows: [][]any{{"/repo/.git", strings.Repeat("a", 32), int64(len(entries)), int64(0), int64(0), int64(0)}}},
			{Name: "memory_entries", Columns: []string{"repository", "seq", "ts", "type", "scope", "scope_target", "path", "body", "author", "writer_family",
				"writer_name", "consumer", "revision", "supersedes", "revokes", "superseded_by", "revoked_by", "conflicts_with", "expires"}, Rows: entries},
		}}
	if err := PrepareImport(&src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, src); !refusedAs(err, "capacity") {
		t.Fatalf("over capacity: %v", err)
	}
	if empty, err := s.Empty(ctx); err != nil || !empty {
		t.Fatalf("a refused import wrote records: %v %v", empty, err)
	}
	// An import that reaches the database page ceiling is refused the same way.
	var pages int64
	s.db.QueryRow("PRAGMA page_count").Scan(&pages)
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages+8)); err != nil {
		t.Fatal(err)
	}
	big := inboxSource("thread-big")
	for i := range big.Tables[2].Rows {
		big.Tables[2].Rows[i][6] = strings.Repeat("x", 60000)
	}
	if err := PrepareImport(&big); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, big); !refusedAs(err, "capacity") {
		t.Fatalf("page ceiling: %v", err)
	}
	if empty, err := s.Empty(ctx); err != nil || !empty {
		t.Fatalf("a refused import wrote records: %v %v", empty, err)
	}
}

// TestImportedSessionRegistersAndWakes: an imported session registers again with the
// same inbox and name; its unacknowledged waiting messages are woken, its acknowledged
// and already notified ones are not, and new messages continue its sequence.
func TestImportedSessionRegistersAndWakes(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	src := inboxSource("thread-1")
	if err := PrepareImport(&src); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(ctx, src); err != nil {
		t.Fatal(err)
	}
	repo := testRepo(t)
	notices := []string{}
	s.wake.send = func(_ context.Context, session Session, notice string) wakeResult {
		if session.ID != "thread-1" || strings.Contains(notice, "fourth") {
			t.Fatalf("wrong wake %s %q", session.ID, notice)
		}
		notices = append(notices, notice)
		return wakeResult{"notified", "accepted"}
	}
	// An expired imported session gets no wake.
	if err := s.wakeStep(ctx); err != nil || len(notices) != 0 {
		t.Fatalf("expired session woken: %v %v", notices, err)
	}
	r := registration(repo, "codex")
	r.ID = "thread-1"
	session, err := s.Register(ctx, r)
	if err != nil || session.Name != "codex-python-thread-1" {
		t.Fatalf("register: %+v %v", session, err)
	}
	if err := s.wakeStep(ctx); err != nil || len(notices) != 1 || !strings.Contains(notices[0], "4-5") {
		t.Fatalf("wake after registration: %q %v", notices, err)
	}
	inbox, err := s.ReadInbox(ctx, Key{"codex", "thread-1"}, 0, 10)
	if err != nil || len(inbox.Messages) != 3 || inbox.Messages[0].Seq != 3 {
		t.Fatalf("inbox %+v %v", inbox, err)
	}
	sender := join(t, s, "claude", "go-sender", repo)
	outcome, err := s.Send(ctx, Key{sender.Family, sender.ID}, session.Name, "after the import")
	if err != nil || outcome.Seq != 6 {
		t.Fatalf("new message: %+v %v", outcome, err)
	}
	if _, err := s.Ack(ctx, Key{"codex", "thread-1"}, 6); err != nil {
		t.Fatal(err)
	}
	notices = nil
	if err := s.wakeStep(ctx); err != nil || len(notices) != 0 {
		t.Fatalf("acknowledged messages woken: %q %v", notices, err)
	}
	data, _ := json.Marshal(session)
	if strings.Contains(string(data), "fourth") {
		t.Fatal("body leaked into the session")
	}
}
