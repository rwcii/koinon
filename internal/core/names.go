package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// label is the human-facing part of a session's names: the repository directory name
// (`/x/repo/.git` and `/x/repo.git` both give `repo`), or the working directory name for
// a session without a repository. It follows the rules of the Python runtime.
func label(repository, directory string) string {
	base := filepath.Base(directory)
	if repository != "" {
		base = filepath.Base(repository)
		if base == ".git" {
			base = filepath.Base(filepath.Dir(repository))
		} else {
			base = strings.TrimSuffix(base, ".git")
		}
	}
	result := strings.Trim(unsafeLabel.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if len(result) > 32 {
		result = strings.TrimRight(result[:32], "-")
	}
	if result == "" {
		return "session"
	}
	return result
}

func nameTaken(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM names WHERE name=?`, name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// firstFree inserts the first candidate that no peer name or alias holds.
func firstFree(ctx context.Context, tx *sql.Tx, candidates []string, insert func(string) error) error {
	for _, candidate := range candidates {
		taken, err := nameTaken(ctx, tx, candidate)
		if err != nil {
			return err
		}
		if !taken {
			return insert(candidate)
		}
	}
	return errors.New("no free session name")
}

func lengths(base, digest string) []string {
	var result []string
	for n := 4; n <= len(digest); n += 2 {
		result = append(result, base+"-"+digest[:n])
	}
	return result
}

// assignNames gives a session its permanent peer name and, for a session with a
// repository, reserves the alias of its family and repository and takes it when no
// active session of that repository holds it. It runs in the caller's transaction,
// so concurrent registrations leave one holder.
func assignNames(ctx context.Context, tx *sql.Tx, now int64, family, id, repository, directory string, subagent bool) error {
	var peer string
	err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`, family, id).Scan(&peer)
	if errors.Is(err, sql.ErrNoRows) {
		sum := sha256.Sum256([]byte(family + "\x00" + id))
		base := family + "-" + label(repository, directory)
		var candidates []string
		for offset := range 256 {
			candidates = append(candidates, fmt.Sprintf("%s-%02x", base, (int(sum[0])+offset)%256))
		}
		candidates = append(candidates, lengths(base, hex.EncodeToString(sum[:]))...)
		err = firstFree(ctx, tx, candidates, func(name string) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO names(name,kind,family,session_id,repository) VALUES (?,'peer',?,?,?)`, name, family, id, repository)
			return err
		})
	}
	// A Codex sub-agent thread has its peer name only; it never holds or creates an alias.
	if err != nil || repository == "" || subagent {
		return err
	}
	var alias, holder string
	err = tx.QueryRowContext(ctx, `SELECT name,holder_id FROM names WHERE kind='alias' AND family=? AND repository=?`, family, repository).Scan(&alias, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		sum := sha256.Sum256([]byte(repository))
		base := family + "-" + label(repository, directory)
		err = firstFree(ctx, tx, append([]string{base}, lengths(base, hex.EncodeToString(sum[:]))...), func(name string) error {
			alias = name
			_, err := tx.ExecContext(ctx, `INSERT INTO names(name,kind,family,repository) VALUES (?,'alias',?,?)`, name, family, repository)
			return err
		})
	}
	if err != nil || holder == id {
		return err
	}
	if holder != "" {
		valid, err := holds(ctx, tx, now, family, holder, repository)
		if err != nil || valid {
			return err
		}
	}
	// Only an active session takes the alias; a migrated expired or retired record does not.
	if self, err := holds(ctx, tx, now, family, id, repository); err != nil || !self {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE names SET holder_id=? WHERE name=?`, id, alias)
	return err
}

// holds reports whether a recorded alias holder still holds it: the holder must be
// active and still registered for the alias's repository.
func holds(ctx context.Context, tx *sql.Tx, now int64, family, id, repository string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=? AND repository=?
		AND retired_at=0 AND expires_at>?`, family, id, repository, now).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
