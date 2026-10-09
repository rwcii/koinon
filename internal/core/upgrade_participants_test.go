package core

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// fixtureTime is the clock of testdata/f88a42c.sql, a store that develop f88a42c (schema 9)
// wrote with its own code: two codex sessions and one claude session in one repository,
// each family's alias held, inbox messages with an acknowledgement, a memory cursor with
// a later entry, and a claimed work item. Its paths are rewritten to /synthetic.
var fixtureTime = time.UnixMilli(1790000000000)

// loadFixture writes testdata/f88a42c.sql into a new state directory's database.
func loadFixture(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("testdata", "f88a42c.sql"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	// The state file is private, as the daemon creates it.
	f, err := platform.OpenPrivate(filepath.Join(root, "state.sqlite3"), os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	q := url.Values{}
	for _, pragma := range []string{"page_size(4096)", "auto_vacuum(INCREMENTAL)", "journal_mode(WAL)"} {
		q.Add("_pragma", pragma)
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.Join(root, "state.sqlite3"), RawQuery: q.Encode()}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	return root
}

// rowsOf renders the rows of the tables that hold participant state, for comparison.
func rowsOf(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, query := range []string{
		`SELECT id,recipient_family,recipient_id,seq,sender_id,body FROM messages ORDER BY id`,
		`SELECT family,id,last_seq,acked_through FROM sessions ORDER BY family,id`,
		`SELECT repository,consumer,seq FROM memory_cursors ORDER BY repository,consumer`,
		`SELECT store,generation,work_id,consumer,revision,expires_at,active FROM claim_bundles ORDER BY store,generation`,
		`SELECT name,kind,family,session_id,repository,holder_id FROM names ORDER BY name`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&b, values...)
		}
		rows.Close()
	}
	return b.String()
}

// The upgrade constraint: a develop f88a42c store opens after the migration with every
// alias held by the same session as a participant without a role, and every message,
// acknowledgement, memory cursor and claim unchanged.
func TestUpgradeFromF88a42cKeepsParticipants(t *testing.T) {
	root := loadFixture(t)
	before, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	want := rowsOf(t, before)
	before.Close()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.now = func() time.Time { return fixtureTime }
	if got := rowsOf(t, s.db); got != want {
		t.Fatalf("participant state changed by the migration:\n%s\nwant:\n%s", got, want)
	}
	var roles int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM names WHERE kind='alias' AND role=''`).Scan(&roles); err != nil || roles != 2 {
		t.Fatalf("aliases as participants without a role: %d %v", roles, err)
	}
	items, _, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	held := map[string]string{}
	for _, item := range items {
		if item.HoldsAddress {
			held[item.Address] = item.ID
		}
	}
	if held["codex-koinon"] != "synthetic-holder" || held["claude-koinon"] != "synthetic-claude" || len(held) != 2 {
		t.Fatalf("holders after the migration: %v", held)
	}
	// A migrated session has no launch: its renewal is refused, so it holds its address
	// until its deadline and the participant's next qualifier takes it then.
	if _, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-holder", IfRevision: 1}, false); code(err) != "not_launched" {
		t.Fatalf("renewal of a migrated session: %v", err)
	}
}

// A failed migration leaves the schema 9 store as it was.
func TestFailedParticipantMigrationLeavesStore(t *testing.T) {
	root := loadFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := rowsOf(t, db)
	// An object that schema 11 creates already exists, so its step fails.
	if _, err := db.Exec(`CREATE TABLE participant_events (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db, 9, schemaVersion); err == nil {
		t.Fatal("migration over an existing object succeeded")
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 9 {
		t.Fatalf("version after a failed migration: %d %v", version, err)
	}
	if got := rowsOf(t, db); got != want {
		t.Fatalf("failed migration changed the store:\n%s", got)
	}
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('subagent','role')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("columns of a failed migration kept: %d %v", columns, err)
	}
}
