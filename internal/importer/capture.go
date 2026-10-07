package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openSource opens a source database without waiting for a lock.
func openSource(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(0)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// held is a source whose write lock a guard connection holds.
type held struct {
	db   *sql.DB
	conn *sql.Conn
}

func (h held) release() {
	h.conn.ExecContext(context.Background(), "ROLLBACK")
	h.conn.Close()
	h.db.Close()
}

// Capture copies every source into dir while each source's write lock is held, so no
// writer can commit between the copies. A guard connection holds BEGIN IMMEDIATE on
// every source first; a separate connection then runs VACUUM INTO, which reads one
// consistent snapshot, including the log. A busy source is python_running. The
// sources are never written. Captures are named by position, 0.sqlite3 and so on.
func Capture(ctx context.Context, sources []Source, dir string) ([]string, error) {
	var guards []held
	defer func() {
		for _, h := range guards {
			h.release()
		}
	}()
	for _, s := range sources {
		db, err := openSource(s.Path)
		if err != nil {
			return nil, err
		}
		conn, err := db.Conn(ctx)
		if err != nil {
			db.Close()
			return nil, err
		}
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			conn.Close()
			db.Close()
			if strings.Contains(err.Error(), "locked") || strings.Contains(err.Error(), "busy") {
				return nil, fmt.Errorf("python_running: a writer holds %s", s.Path)
			}
			return nil, fmt.Errorf("source_invalid: cannot lock %s: %w", s.Path, err)
		}
		guards = append(guards, held{db: db, conn: conn})
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	var captures []string
	for i, s := range sources {
		target := filepath.Join(dir, fmt.Sprintf("%d.sqlite3", i))
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		reader, err := openSource(s.Path)
		if err != nil {
			return nil, err
		}
		_, err = reader.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(target, "'", "''")+`'`)
		reader.Close()
		if err != nil {
			return nil, fmt.Errorf("source_invalid: cannot capture %s: %w", s.Path, err)
		}
		if err := check(ctx, target); err != nil {
			return nil, fmt.Errorf("source_invalid: capture of %s: %w", s.Path, err)
		}
		captures = append(captures, target)
	}
	return captures, nil
}

// check runs SQLite's integrity check on a capture.
func check(ctx context.Context, path string) error {
	db, err := openSource(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("integrity check failed: " + result)
	}
	return nil
}
