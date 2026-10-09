package core

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
	_ "modernc.org/sqlite"
)

var ErrInvalid = errors.New("invalid request")
var ErrMissing = errors.New("session not found")
var ErrConflict = errors.New("session revision or lifecycle conflict")

type Session struct {
	Family       string          `json:"family"`
	ID           string          `json:"id"`
	Repository   string          `json:"repository"`
	Directory    string          `json:"directory"`
	WakeTarget   json.RawMessage `json:"wake_target"`
	RegisteredAt int64           `json:"registered_at"`
	RenewedAt    int64           `json:"renewed_at"`
	ExpiresAt    int64           `json:"expires_at"`
	RetiredAt    int64           `json:"retired_at"`
	// PurgeAt is the time of the maintainer's purge mark (#216), or 0.
	PurgeAt  int64  `json:"purge_at,omitempty"`
	Revision int64  `json:"revision"`
	State    string `json:"state"`
	Name     string `json:"name"`
	// Alias is the participant address that the session holds while it is active.
	Alias string `json:"alias,omitempty"`
	// Subagent marks a Codex sub-agent thread, which never holds an alias.
	Subagent bool `json:"subagent,omitempty"`
	// Role, from the session's launch, selects its participant: its family, repository
	// and role. Address is that participant's address, held or not; HoldsAddress says
	// whether this session holds it. Conflict lists the qualifying sessions' peer names
	// while the participant has no holder because more than one qualifies.
	Role         string   `json:"role,omitempty"`
	Address      string   `json:"address,omitempty"`
	HoldsAddress bool     `json:"holds_address,omitempty"`
	Conflict     []string `json:"conflict,omitempty"`
	// Fenced says that the session was replaced as its participant's holder: it registers
	// with its peer name only and never takes the participant again unless the maintainer
	// chooses it (chunk 03).
	Fenced bool `json:"fenced,omitempty"`
}

type Registration struct {
	Family     string          `json:"family"`
	ID         string          `json:"id"`
	Repository string          `json:"repository"`
	Directory  string          `json:"directory"`
	WakeTarget json.RawMessage `json:"wake_target,omitempty"`
	TTLSeconds int64           `json:"ttl_seconds"`
	LaunchID   string          `json:"launch_id,omitempty"`
	// Ancestors are the caller's process and its ancestors, nearest first; a foreground
	// launch admits the caller only when its host process is among them.
	Ancestors []int `json:"ancestors,omitempty"`
	Subagent  bool  `json:"subagent,omitempty"`
}

type Mutation struct {
	Family     string `json:"family"`
	ID         string `json:"id"`
	IfRevision int64  `json:"if_revision"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type Store struct {
	db      *sql.DB
	now     func() time.Time
	storage storage
	memory  memoryState
	work    workState
	// observed and openCode hold session observations, memory-only (sprint chunk 08).
	observed observations
	openCode openCodeCache
	wake     wakeState
	// retention holds the last retention sweep's results (#216).
	retention retentionState
}

func openStore(root string) (*Store, error) { return openStoreFile(root, "state.sqlite3") }

// openStoreFile opens the state database file name in root; an import stages its
// database under another name and renames it into place (sprint chunk 11).
func openStoreFile(root, name string) (*Store, error) {
	path := filepath.Join(root, name)
	f, err := platform.OpenPrivate(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		f, err := platform.OpenPrivate(path+suffix, os.O_RDONLY)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := f.Close(); err != nil {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	// Exclusive locking keeps the log index in process memory, so no shared-memory file
	// exists; page size and incremental auto-vacuum must precede the first table.
	for _, pragma := range []string{"locking_mode(EXCLUSIVE)", "page_size(4096)", "auto_vacuum(INCREMENTAL)",
		"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "cache_spill(0)", "temp_store(MEMORY)",
		fmt.Sprintf("max_page_count(%d)", defaultMaxPages)} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version < 0 || version > schemaVersion {
		db.Close()
		return nil, errors.New("unsupported Go state schema")
	}
	// The catalog is checked as found and again after the migration, before any write.
	if err := checkCatalog(db, version); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db, version, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	if err := checkCatalog(db, schemaVersion); err != nil {
		db.Close()
		return nil, err
	}
	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := checkFormat(db, path); err != nil {
		db.Close()
		return nil, err
	}
	if err := platform.SyncDir(root); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now, storage: storage{path: path, maxPages: defaultMaxPages}}, nil
}

// configure reads back every setting the storage bound depends on. A database created
// before incremental auto-vacuum is converted once; any other mismatch refuses it.
func configure(db *sql.DB) error {
	var vacuum int
	if err := db.QueryRow("PRAGMA auto_vacuum").Scan(&vacuum); err != nil {
		return err
	}
	if vacuum != 2 {
		if _, err := db.Exec("PRAGMA auto_vacuum=INCREMENTAL"); err != nil {
			return err
		}
		if _, err := db.Exec("VACUUM"); err != nil {
			return err
		}
	}
	for _, c := range []struct{ pragma, want string }{
		{"locking_mode", "exclusive"}, {"journal_mode", "wal"}, {"page_size", "4096"}, {"auto_vacuum", "2"},
		{"cache_spill", "0"}, {"temp_store", "2"}, {"synchronous", "2"}, {"max_page_count", fmt.Sprint(defaultMaxPages)},
	} {
		var got string
		if err := db.QueryRow("PRAGMA " + c.pragma).Scan(&got); err != nil {
			return err
		}
		if got != c.want {
			return fmt.Errorf("unsupported_runtime: %s is %s, not %s", c.pragma, got, c.want)
		}
	}
	return nil
}

// checkFormat refuses a database whose header schema format is not 4. Claim controls
// overwrite 0/1 flags in place, and only format 4 stores those integers in equal-size
// records that need no new page (docs/WORK-ITEMS-GO-STORAGE.md).
func checkFormat(db *sql.DB, path string) error {
	// The header is read from the file, so the log is checkpointed into it first.
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 48)
	if _, err := io.ReadFull(f, header); err != nil {
		return err
	}
	if format := binary.BigEndian.Uint32(header[44:48]); format != 4 {
		return fmt.Errorf("unsupported_runtime: database schema format is %d, not 4", format)
	}
	return nil
}

const schemaVersion = 12

// migrate brings the state schema from version to target in one transaction, so a crash
// leaves either the old or the new schema. Each step starts from the version before it.
func migrate(db *sql.DB, version, target int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version < 1 && target >= 1 {
		if _, err := tx.Exec(`CREATE TABLE sessions (
			family TEXT NOT NULL, id TEXT NOT NULL, repository TEXT NOT NULL,
			directory TEXT NOT NULL, wake_target TEXT NOT NULL,
			registered_at INTEGER NOT NULL, renewed_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL, retired_at INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL, PRIMARY KEY (family,id)
		)`); err != nil {
			return err
		}
	}
	if version < 2 && target >= 2 {
		// Version 2: daemon-held names, inboxes and delivery state (sprint chunk 03).
		// Peer names and aliases share one namespace, so a peer name never equals an alias.
		if _, err := tx.Exec(`
			ALTER TABLE sessions ADD COLUMN last_seq INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN acked_through INTEGER NOT NULL DEFAULT 0;
			CREATE TABLE names (
				name TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('peer','alias')),
				family TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
				repository TEXT NOT NULL DEFAULT '', holder_id TEXT NOT NULL DEFAULT ''
			);
			CREATE UNIQUE INDEX names_peer ON names(family, session_id) WHERE kind='peer';
			CREATE UNIQUE INDEX names_alias ON names(family, repository) WHERE kind='alias';
			CREATE TABLE messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				recipient_family TEXT NOT NULL, recipient_id TEXT NOT NULL, seq INTEGER NOT NULL,
				sender_family TEXT NOT NULL, sender_id TEXT NOT NULL, sender_name TEXT NOT NULL,
				body TEXT NOT NULL, created_at INTEGER NOT NULL,
				delivery_state TEXT NOT NULL DEFAULT 'waiting'
					CHECK (delivery_state IN ('waiting','notified','uncertain','failed')),
				delivery_reason TEXT NOT NULL DEFAULT '', delivery_updated_at INTEGER NOT NULL,
				UNIQUE (recipient_family, recipient_id, seq)
			);
			CREATE INDEX messages_sender ON messages(sender_family, sender_id, id);
		`); err != nil {
			return err
		}
		// Sessions recorded by schema 1 get their names now.
		rows, err := tx.Query(`SELECT family,id,repository,directory FROM sessions ORDER BY registered_at,family,id`)
		if err != nil {
			return err
		}
		type key struct{ family, id, repository, directory string }
		var existing []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.family, &k.id, &k.repository, &k.directory); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, k)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		for _, k := range existing {
			if err := assignNamesSchema2(context.Background(), tx, now, k.family, k.id, k.repository, k.directory); err != nil {
				return err
			}
		}
	}
	if version < 3 && target >= 3 {
		// Version 3: private launch targets (sprint chunk 10).
		if _, err := tx.Exec(`CREATE TABLE launches (
			id TEXT PRIMARY KEY, family TEXT NOT NULL, directory TEXT NOT NULL,
			target TEXT NOT NULL, created_at INTEGER NOT NULL
		)`); err != nil {
			return err
		}
	}
	if version < 4 && target >= 4 {
		// Version 4: one memory store per Git common directory (sprint chunk 06). Every table
		// is keyed by the repository the daemon records for a session.
		if _, err := tx.Exec(`
			CREATE TABLE memory_stores (repository TEXT PRIMARY KEY, store_id TEXT NOT NULL,
				head INTEGER NOT NULL DEFAULT 0, floor INTEGER NOT NULL DEFAULT 0);
			CREATE TABLE memory_entries (repository TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				type TEXT NOT NULL, scope TEXT NOT NULL, scope_target TEXT, path TEXT, body TEXT NOT NULL,
				author TEXT, writer_family TEXT NOT NULL, writer_name TEXT NOT NULL, consumer TEXT NOT NULL,
				revision INTEGER NOT NULL, supersedes INTEGER, revokes INTEGER, superseded_by INTEGER,
				revoked_by INTEGER, conflicts_with INTEGER, expires REAL, PRIMARY KEY (repository, seq));
			CREATE INDEX memory_entries_live ON memory_entries(repository, superseded_by, revoked_by, expires);
			CREATE TABLE memory_idem (repository TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				deadline REAL NOT NULL, PRIMARY KEY (repository, consumer, key));
			CREATE TABLE memory_cursors (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				issued INTEGER NOT NULL, snapshot TEXT, bootstrapped INTEGER NOT NULL, resnapshot INTEGER NOT NULL,
				updated REAL NOT NULL, PRIMARY KEY (repository, consumer));
			CREATE TABLE memory_retired (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				at REAL NOT NULL, PRIMARY KEY (repository, consumer));
			CREATE TABLE memory_snapshots (id TEXT PRIMARY KEY, repository TEXT NOT NULL, consumer TEXT NOT NULL,
				head INTEGER NOT NULL, created REAL NOT NULL, items INTEGER NOT NULL, issued INTEGER NOT NULL,
				acked INTEGER NOT NULL DEFAULT 0, acked_at REAL);
			CREATE INDEX memory_snapshots_owner ON memory_snapshots(repository, consumer);
			CREATE TABLE memory_snapshot_items (id TEXT NOT NULL, position INTEGER NOT NULL, seq INTEGER NOT NULL,
				payload TEXT NOT NULL, bytes INTEGER NOT NULL, PRIMARY KEY (id, position));
		`); err != nil {
			return err
		}
	}
	if version < 5 && target >= 5 {
		// Version 5: work items, advisory claims and work events (sprint chunk 07), keyed by
		// the store's 32-character store_id so that row and index sizes stay bounded
		// (docs/WORK-ITEMS-GO-STORAGE.md). The counters never decrease.
		if _, err := tx.Exec(`
			ALTER TABLE memory_stores ADD COLUMN work_counter INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE memory_stores ADD COLUMN claim_counter INTEGER NOT NULL DEFAULT 0;
			CREATE TABLE work_items (store TEXT NOT NULL, work_id TEXT NOT NULL CHECK (length(work_id)=32),
				revision INTEGER NOT NULL CHECK (revision>0),
				lifecycle TEXT NOT NULL CHECK (lifecycle IN ('open','active','blocked','finished')),
				title TEXT NOT NULL, criteria TEXT NOT NULL, non_goals TEXT NOT NULL, proposed_assignee TEXT,
				created_at REAL NOT NULL, created_consumer TEXT NOT NULL, first_start_revision INTEGER,
				scope_revision INTEGER NOT NULL CHECK (scope_revision>0),
				progress_epoch INTEGER NOT NULL DEFAULT 0 CHECK (progress_epoch>=0), last_progress_at REAL,
				progress_deadline REAL, progress TEXT NOT NULL DEFAULT '', checkpoint TEXT NOT NULL DEFAULT '',
				next_artifact TEXT NOT NULL DEFAULT '', blocker TEXT NOT NULL DEFAULT '', last_writer TEXT,
				last_generation INTEGER, last_lease_expires REAL,
				lease_expired INTEGER NOT NULL DEFAULT 0 CHECK (lease_expired IN (0,1)),
				outcome TEXT CHECK (outcome IN ('completed','withdrawn')), reason TEXT NOT NULL DEFAULT '',
				references_json TEXT NOT NULL DEFAULT '[]', finished_at REAL, expires_at REAL,
				latest_seq INTEGER NOT NULL CHECK (latest_seq>0),
				CHECK ((lifecycle='finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL AND expires_at IS NOT NULL)
					OR (lifecycle<>'finished' AND outcome IS NULL AND finished_at IS NULL AND expires_at IS NULL)),
				PRIMARY KEY (store, work_id));
			CREATE TABLE work_scope_revisions (store TEXT NOT NULL, work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0), ts REAL NOT NULL, consumer TEXT NOT NULL, author TEXT,
				title TEXT NOT NULL, criteria TEXT NOT NULL, non_goals TEXT NOT NULL,
				PRIMARY KEY (store, work_id, revision));
			CREATE TABLE claim_bundles (store TEXT NOT NULL, generation INTEGER NOT NULL CHECK (generation>0),
				work_id TEXT NOT NULL, consumer TEXT NOT NULL, revision INTEGER NOT NULL CHECK (revision>0),
				issued_at REAL NOT NULL, renewed_at REAL NOT NULL, expires_at REAL NOT NULL,
				progress_epoch INTEGER NOT NULL CHECK (progress_epoch>0),
				overdue_recorded INTEGER NOT NULL DEFAULT 0 CHECK (overdue_recorded IN (0,1)),
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				overdue_credit INTEGER NOT NULL DEFAULT 1 CHECK (overdue_credit IN (0,1)),
				end_credit INTEGER NOT NULL DEFAULT 1 CHECK (end_credit IN (0,1)),
				PRIMARY KEY (store, generation), UNIQUE (store, work_id));
			CREATE TABLE claim_resources (store TEXT NOT NULL, generation INTEGER NOT NULL,
				ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 8),
				kind TEXT NOT NULL CHECK (kind IN ('writer','path','exact')), resource TEXT NOT NULL,
				PRIMARY KEY (store, generation, ordinal));
			CREATE TABLE work_events (store TEXT NOT NULL, seq INTEGER NOT NULL CHECK (seq>0), work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0),
				kind TEXT NOT NULL CHECK (kind IN ('created','proposed','edited','started','updated','released',
					'finished','progress-overdue','lease-expired')),
				payload TEXT NOT NULL, PRIMARY KEY (store, seq));
			CREATE INDEX work_events_item ON work_events(store, work_id, seq);
			CREATE TABLE work_replays (store TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				operation TEXT NOT NULL, scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER, ts REAL NOT NULL,
				deadline REAL NOT NULL, result TEXT NOT NULL, PRIMARY KEY (store, consumer, key));
		`); err != nil {
			return err
		}
	}
	if version < 6 && target >= 6 {
		// Version 6: bounded durable wake retries. Attempts are persisted before any
		// provider call, so a crash after acceptance cannot lose a wake.
		if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN wake_attempts INTEGER NOT NULL DEFAULT 0;
            ALTER TABLE messages ADD COLUMN wake_next_at INTEGER NOT NULL DEFAULT 0;
            ALTER TABLE messages ADD COLUMN wake_reason TEXT NOT NULL DEFAULT '';
            CREATE INDEX messages_wake ON messages(delivery_state, wake_next_at, recipient_family, recipient_id);`); err != nil {
			return err
		}
	}
	if version < 7 && target >= 7 {
		// Version 7: dashboard actions (sprint chunk 09). The audit log; a 0/1 flag for a
		// message that the maintainer acknowledged, which format 4 rewrites in place; and
		// the built-in maintainer session, which has an inbox and never expires.
		if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN maintainer_ack INTEGER NOT NULL DEFAULT 0;
			CREATE TABLE audit (
				id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, action TEXT NOT NULL,
				target TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX audit_at ON audit(at);
			INSERT INTO sessions(family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,retired_at,revision)
				VALUES ('maintainer','maintainer','','','{}',0,0,9007199254740991,0,1);
			INSERT INTO names(name,kind,family,session_id) VALUES ('maintainer','peer','maintainer','maintainer');`); err != nil {
			return err
		}
	}
	if version < 8 && target >= 8 {
		// Version 8: the record of each Python-era source imported into this database
		// (sprint chunk 11). A source imports once; its digest detects a changed source.
		if _, err := tx.Exec(`CREATE TABLE imports (
			source TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('inbox','memory')),
			digest TEXT NOT NULL, cutoff TEXT NOT NULL, counts TEXT NOT NULL, at INTEGER NOT NULL
		)`); err != nil {
			return err
		}
	}
	if version < 9 && target >= 9 {
		// Version 9: retention (#216). The sweep's acknowledgement mark: every message of a
		// session with a sequence at or below ack_mark was acknowledged by ack_mark_at, which
		// is 0 while no mark waits. purge_at is the time of the maintainer's purge mark, or 0.
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN ack_mark INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN ack_mark_at INTEGER NOT NULL DEFAULT 0;
			ALTER TABLE sessions ADD COLUMN purge_at INTEGER NOT NULL DEFAULT 0;`); err != nil {
			return err
		}
	}
	if version < 10 && target >= 10 {
		// Version 10: a Codex sub-agent thread registers as its own session but never
		// holds an alias (participants sprint, chunk 01).
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN subagent INTEGER NOT NULL DEFAULT 0;`); err != nil {
			return err
		}
	}
	if version < 11 && target >= 11 {
		// Version 11: participants (participants sprint, chunk 02). A participant is a family,
		// repository and role; its address is an alias row with that role. The alias rows of
		// version 10 become participants without a role, with their names and holders. A
		// participant without an active holder and with more than one qualifier records the
		// qualifiers' peer names as its conflict. participant_events records each change.
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN role TEXT NOT NULL DEFAULT '';
			ALTER TABLE names ADD COLUMN role TEXT NOT NULL DEFAULT '';
			ALTER TABLE names ADD COLUMN conflict TEXT NOT NULL DEFAULT '';
			DROP INDEX names_alias;
			CREATE UNIQUE INDEX names_participant ON names(family, repository, role) WHERE kind='alias';
			CREATE TABLE participant_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, address TEXT NOT NULL,
				former TEXT NOT NULL, holder TEXT NOT NULL, reason TEXT NOT NULL,
				actor TEXT NOT NULL CHECK (actor IN ('session','maintainer','daemon')),
				details TEXT NOT NULL DEFAULT ''
			);
			CREATE INDEX participant_events_at ON participant_events(at);
			CREATE INDEX participant_events_address ON participant_events(address, id);`); err != nil {
			return err
		}
	}
	if version < 12 && target >= 12 {
		// Version 12: participant state and fencing (participants sprint, chunk 03). A
		// participant's inbox is an internal session row participant:<address> that never
		// expires; acked_by names the native session (FAMILY:ID) that acknowledged it last.
		// A fence keeps a former holder from acting for, or taking, the participant again
		// until the maintainer chooses it.
		if _, err := tx.Exec(`ALTER TABLE sessions ADD COLUMN acked_by TEXT NOT NULL DEFAULT '';
			ALTER TABLE memory_cursors ADD COLUMN actor TEXT NOT NULL DEFAULT '';
			ALTER TABLE work_events ADD COLUMN actor TEXT NOT NULL DEFAULT '';
			CREATE TABLE participant_fences (
				address TEXT NOT NULL, family TEXT NOT NULL, session_id TEXT NOT NULL,
				at INTEGER NOT NULL, reason TEXT NOT NULL, PRIMARY KEY (address, family, session_id)
			);`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", target)); err != nil {
		return err
	}
	return tx.Commit()
}

var punctuationSpace = regexp.MustCompile(`\s*([(),;])\s*`)

// catalog lists a database's schema objects with their normalized definitions.
func catalog(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []string
	for rows.Next() {
		var kind, name, table, definition string
		if err := rows.Scan(&kind, &name, &table, &definition); err != nil {
			return nil, err
		}
		// Layout is not part of a definition: whitespace collapses, and none is kept next
		// to punctuation.
		definition = punctuationSpace.ReplaceAllString(strings.Join(strings.Fields(definition), " "), "$1")
		objects = append(objects, kind+" "+name+" "+table+" "+definition)
	}
	return objects, rows.Err()
}

// checkCatalog refuses a database whose schema objects differ from those this runtime
// creates for version: an extra index or trigger would break the storage proof, whose
// claim controls assume the published tables and indexes and nothing else.
func checkCatalog(db *sql.DB, version int) error {
	reference, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	reference.SetMaxOpenConns(1)
	if err := migrate(reference, 0, version); err != nil {
		return err
	}
	want, err := catalog(reference)
	if err != nil {
		return err
	}
	got, err := catalog(db)
	if err != nil {
		return err
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		return fmt.Errorf("unsupported_runtime: the state database's schema objects differ from schema %d", version)
	}
	return nil
}

// maintainerKey is the built-in session of the maintainer (sprint chunk 09). Only the
// dashboard acts as it; validKey refuses it, so no /v1/ caller can.
var maintainerKey = Key{Family: "maintainer", ID: "maintainer"}

func validKey(family, id string) bool {
	switch family {
	case "claude", "codex", "deepseek", "agy", "opencode":
	default:
		return false
	}
	// A participant's internal inbox row has the ID participant:<address>; no native
	// session can take that form.
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, "\x00\r\n") && !strings.HasPrefix(id, participantPrefix)
}

func ttl(seconds int64) (int64, error) {
	if seconds == 0 {
		seconds = 900
	}
	if seconds < 60 || seconds > 3600 {
		return 0, ErrInvalid
	}
	return seconds * 1000, nil
}

func repository(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return "", ErrInvalid
	}
	common, err := filepath.EvalSymlinks(strings.TrimSpace(string(data)))
	if err != nil || !filepath.IsAbs(common) {
		return "", ErrInvalid
	}
	return common, nil
}

// CleanGitEnvironment is this process's environment without GIT_ variables, so a caller's
// inherited Git overrides never retarget repository discovery.
func CleanGitEnvironment() []string {
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return env
}

func (s *Store) Register(ctx context.Context, r Registration) (Session, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if !validKey(r.Family, r.ID) || len(r.WakeTarget) > 8192 {
		return Session{}, ErrInvalid
	}
	duration, err := ttl(r.TTLSeconds)
	if err != nil {
		return Session{}, err
	}
	directory, err := filepath.EvalSymlinks(r.Directory)
	if err != nil || !filepath.IsAbs(r.Directory) {
		return Session{}, ErrInvalid
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || len(directory) > 4096 || strings.ContainsAny(directory, "\x00\r\n") {
		return Session{}, ErrInvalid
	}
	// A session need not select a repository. Plain scratch sessions can register
	// without Git; repository-dependent operations are added in later chunks.
	common := ""
	if r.Repository != "" {
		common, err = repository(ctx, r.Repository)
		if err != nil {
			return Session{}, err
		}
		dirCommon, err := repository(ctx, directory)
		if err != nil || common != dirCommon {
			return Session{}, ErrInvalid
		}
	}
	if len(r.WakeTarget) == 0 {
		r.WakeTarget = json.RawMessage(`{}`)
	}
	if !json.Valid(r.WakeTarget) {
		return Session{}, ErrInvalid
	}
	now := s.now().UnixMilli()
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	// Every launcher family registers only with its own launch, which also gives the
	// session its role; DeepSeek has no launcher and keeps its command registration until
	// its MCP path is settled (#199).
	role := ""
	if r.Family == "deepseek" {
		if r.LaunchID != "" || r.Subagent {
			return Session{}, ErrInvalid
		}
	} else if r.WakeTarget, role, err = admitLaunch(ctx, tx.Tx, r, directory); err != nil {
		return Session{}, err
	}
	// Whether the session was active before this registration decides whether a holding
	// recorded for it still stands.
	var wasActive bool
	err = tx.QueryRowContext(ctx, `SELECT retired_at=0 AND expires_at>? FROM sessions WHERE family=? AND id=?`, now, r.Family, r.ID).Scan(&wasActive)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(`+sessionColumns+`) VALUES (?,?,?,?,?,?,?,?,0,1,?,?)
		ON CONFLICT(family,id) DO UPDATE SET repository=excluded.repository,
		directory=excluded.directory,wake_target=excluded.wake_target,renewed_at=excluded.renewed_at,
		expires_at=excluded.expires_at,retired_at=0,revision=sessions.revision+1,subagent=MAX(sessions.subagent,excluded.subagent),
		role=excluded.role`,
		r.Family, r.ID, common, directory, string(r.WakeTarget), now, now, now+duration, r.Subagent, role)
	if err != nil {
		return Session{}, tx.fail(err)
	}
	// A thread stays a sub-agent once it is known to be one.
	var subagent bool
	if err := tx.QueryRowContext(ctx, `SELECT subagent FROM sessions WHERE family=? AND id=?`, r.Family, r.ID).Scan(&subagent); err != nil {
		return Session{}, tx.fail(err)
	}
	if err := assignNames(ctx, tx.Tx, now, participant{r.Family, r.ID, common, directory, role, subagent, wasActive}); err != nil {
		return Session{}, tx.fail(err)
	}
	result, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	return result, tx.Commit()
}

const sessionColumns = `family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,retired_at,revision,subagent,role`

// sessionQuery reads a session with its peer name and its participant: the address, its
// recorded holder and conflict. scanSession shows the address as held only while the
// session is active. A sub-agent or a session without a repository has no participant.
const sessionQuery = `SELECT s.family,s.id,s.repository,s.directory,s.wake_target,s.registered_at,s.renewed_at,
	s.expires_at,s.retired_at,s.purge_at,s.revision,s.subagent,s.role,
	COALESCE((SELECT name FROM names WHERE kind='peer' AND family=s.family AND session_id=s.id),''),
	COALESCE(p.name,''),COALESCE(p.holder_id,''),COALESCE(p.conflict,''),
	EXISTS (SELECT 1 FROM participant_fences f WHERE f.address=p.name AND f.family=s.family AND f.session_id=s.id) FROM sessions s
	LEFT JOIN names p ON p.kind='alias' AND p.family=s.family AND p.repository=s.repository AND p.role=s.role
		AND s.repository!='' AND s.subagent=0`

type scanner interface{ Scan(...any) error }

func scanSession(row scanner, now int64) (Session, error) {
	var result Session
	var target, holder, conflict string
	err := row.Scan(&result.Family, &result.ID, &result.Repository, &result.Directory, &target, &result.RegisteredAt, &result.RenewedAt, &result.ExpiresAt, &result.RetiredAt, &result.PurgeAt, &result.Revision, &result.Subagent, &result.Role, &result.Name, &result.Address, &holder, &conflict, &result.Fenced)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrMissing
	}
	if err != nil {
		return result, err
	}
	result.WakeTarget = json.RawMessage(target)
	result.State = "active"
	if result.ExpiresAt <= now {
		result.State = "expired"
	}
	if result.RetiredAt != 0 {
		result.State = "retired"
	}
	if result.State == "active" && result.Address != "" && holder == result.ID {
		result.Alias, result.HoldsAddress = result.Address, true
	}
	if conflict != "" {
		if err := json.Unmarshal([]byte(conflict), &result.Conflict); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) Mutate(ctx context.Context, r Mutation, retire bool) (Session, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if !validKey(r.Family, r.ID) || r.IfRevision < 1 {
		return Session{}, ErrInvalid
	}
	duration, err := ttl(r.TTLSeconds)
	if err != nil {
		return Session{}, err
	}
	now := s.now().UnixMilli()
	// Renewal and retirement keep existing records current; they may use the reserve.
	tx, err := s.begin(ctx, control)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	current, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	if current.Revision != r.IfRevision || current.State != "active" {
		return Session{}, ErrConflict
	}
	// A session registered without its launch, such as one an older store holds, is not
	// renewed: it expires at the expiry it already has, with its records kept.
	if !retire && current.Family != "deepseek" && !launched(current) {
		return Session{}, ErrNotLaunched
	}
	if retire {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET retired_at=?,revision=revision+1 WHERE family=? AND id=?`, now, r.Family, r.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET renewed_at=?,expires_at=?,revision=revision+1 WHERE family=? AND id=?`, now, now+duration, r.Family, r.ID)
		if err == nil {
			// A renewal takes the repository's alias when its holder expired or retired.
			err = assignNames(ctx, tx.Tx, now, participant{r.Family, r.ID, current.Repository, current.Directory, current.Role, current.Subagent, true})
		}
	}
	if err != nil {
		return Session{}, tx.fail(err)
	}
	result, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	return result, tx.Commit()
}

func (s *Store) List(ctx context.Context) ([]Session, bool, error) {
	// The built-in maintainer session is not an agent session; Peers adds it.
	rows, err := s.db.QueryContext(ctx, sessionQuery+` WHERE s.family!='maintainer' AND `+notParticipantRow+` ORDER BY s.family,s.id LIMIT 1001`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []Session{}
	now := s.now().UnixMilli()
	for rows.Next() {
		r, err := scanSession(rows, now)
		if err != nil {
			return nil, false, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(result) > 1000
	if truncated {
		result = result[:1000]
	}
	return result, truncated, nil
}

func (s *Store) Counts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{"total": 0, "active": 0, "expired": 0, "retired": 0}
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN retired_at!=0 THEN 'retired'
		WHEN expires_at<=? THEN 'expired' ELSE 'active' END,COUNT(*) FROM sessions s WHERE family!='maintainer' AND `+notParticipantRow+` GROUP BY 1`, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
		counts["total"] += count
	}
	return counts, rows.Err()
}
