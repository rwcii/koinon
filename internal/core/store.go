package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	Revision     int64           `json:"revision"`
	State        string          `json:"state"`
	Name         string          `json:"name"`
	Alias        string          `json:"alias,omitempty"`
}

type Registration struct {
	Family     string          `json:"family"`
	ID         string          `json:"id"`
	Repository string          `json:"repository"`
	Directory  string          `json:"directory"`
	WakeTarget json.RawMessage `json:"wake_target,omitempty"`
	TTLSeconds int64           `json:"ttl_seconds"`
	LaunchID   string          `json:"launch_id,omitempty"`
}

type Mutation struct {
	Family     string `json:"family"`
	ID         string `json:"id"`
	IfRevision int64  `json:"if_revision"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func openStore(root string) (*Store, error) {
	path := filepath.Join(root, "state.sqlite3")
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
	for _, pragma := range []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"} {
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
	if err := migrate(db, version); err != nil {
		db.Close()
		return nil, err
	}
	if err := platform.SyncDir(root); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

const schemaVersion = 3

// migrate brings the state schema to schemaVersion in one transaction, so a crash
// leaves either the old or the new schema. Each step starts from the version before it.
func migrate(db *sql.DB, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if version < 1 {
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
	if version < 2 {
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
			if err := assignNames(context.Background(), tx, now, k.family, k.id, k.repository, k.directory); err != nil {
				return err
			}
		}
	}
	if version < 3 {
		// Version 3: private launch targets (sprint chunk 10).
		if _, err := tx.Exec(`CREATE TABLE launches (
			id TEXT PRIMARY KEY, family TEXT NOT NULL, directory TEXT NOT NULL,
			target TEXT NOT NULL, created_at INTEGER NOT NULL
		)`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func validKey(family, id string) bool {
	switch family {
	case "claude", "codex", "deepseek", "agy", "opencode":
	default:
		return false
	}
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, "\x00\r\n")
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	if r.LaunchID != "" {
		if len(r.WakeTarget) != 0 && string(r.WakeTarget) != "{}" {
			return Session{}, ErrInvalid
		}
		r.WakeTarget, err = launchTarget(ctx, tx, r.LaunchID, r.Family, directory)
		if err != nil {
			return Session{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions(`+sessionColumns+`) VALUES (?,?,?,?,?,?,?,?,0,1)
		ON CONFLICT(family,id) DO UPDATE SET repository=excluded.repository,
		directory=excluded.directory,wake_target=excluded.wake_target,renewed_at=excluded.renewed_at,
		expires_at=excluded.expires_at,retired_at=0,revision=sessions.revision+1`,
		r.Family, r.ID, common, directory, string(r.WakeTarget), now, now, now+duration)
	if err != nil {
		return Session{}, err
	}
	if err := assignNames(ctx, tx, now, r.Family, r.ID, common, directory); err != nil {
		return Session{}, err
	}
	result, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	return result, tx.Commit()
}

const sessionColumns = `family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,retired_at,revision`

// sessionQuery reads a session with its peer name and the alias it is recorded to hold;
// scanSession shows the alias only while the session is active.
const sessionQuery = `SELECT s.family,s.id,s.repository,s.directory,s.wake_target,s.registered_at,s.renewed_at,
	s.expires_at,s.retired_at,s.revision,
	COALESCE((SELECT name FROM names WHERE kind='peer' AND family=s.family AND session_id=s.id),''),
	COALESCE((SELECT name FROM names WHERE kind='alias' AND family=s.family AND repository=s.repository
		AND s.repository!='' AND holder_id=s.id),'') FROM sessions s`

type scanner interface{ Scan(...any) error }

func scanSession(row scanner, now int64) (Session, error) {
	var result Session
	var target string
	err := row.Scan(&result.Family, &result.ID, &result.Repository, &result.Directory, &target, &result.RegisteredAt, &result.RenewedAt, &result.ExpiresAt, &result.RetiredAt, &result.Revision, &result.Name, &result.Alias)
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
	if result.State != "active" {
		result.Alias = ""
	}
	return result, nil
}

func (s *Store) Mutate(ctx context.Context, r Mutation, retire bool) (Session, error) {
	if !validKey(r.Family, r.ID) || r.IfRevision < 1 {
		return Session{}, ErrInvalid
	}
	duration, err := ttl(r.TTLSeconds)
	if err != nil {
		return Session{}, err
	}
	now := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	current, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	if current.Revision != r.IfRevision || current.State != "active" {
		return Session{}, ErrConflict
	}
	if retire {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET retired_at=?,revision=revision+1 WHERE family=? AND id=?`, now, r.Family, r.ID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET renewed_at=?,expires_at=?,revision=revision+1 WHERE family=? AND id=?`, now, now+duration, r.Family, r.ID)
		if err == nil {
			// A renewal takes the repository's alias when its holder expired or retired.
			err = assignNames(ctx, tx, now, r.Family, r.ID, current.Repository, current.Directory)
		}
	}
	if err != nil {
		return Session{}, err
	}
	result, err := scanSession(tx.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, r.Family, r.ID), now)
	if err != nil {
		return Session{}, err
	}
	return result, tx.Commit()
}

func (s *Store) List(ctx context.Context) ([]Session, bool, error) {
	rows, err := s.db.QueryContext(ctx, sessionQuery+` ORDER BY s.family,s.id LIMIT 1001`)
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
		WHEN expires_at<=? THEN 'expired' ELSE 'active' END,COUNT(*) FROM sessions GROUP BY 1`, s.now().UnixMilli())
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
