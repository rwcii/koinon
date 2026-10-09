package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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

// participant is what assignNames needs of a session: its key, its repository and
// directory, its role and whether it is a sub-agent.
type participant struct {
	family, id, repository, directory, role string
	subagent                                bool
	// wasActive says whether the session was active before this registration or renewal:
	// a recorded holder that had expired holds nothing until the rules give it the
	// participant again.
	wasActive bool
}

// peerName gives a session its permanent peer name, once.
func peerName(ctx context.Context, tx *sql.Tx, family, id, repository, directory string) error {
	var peer string
	err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`, family, id).Scan(&peer)
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sum := sha256.Sum256([]byte(family + "\x00" + id))
	base := family + "-" + label(repository, directory)
	var candidates []string
	for offset := range 256 {
		candidates = append(candidates, fmt.Sprintf("%s-%02x", base, (int(sum[0])+offset)%256))
	}
	candidates = append(candidates, lengths(base, hex.EncodeToString(sum[:]))...)
	return firstFree(ctx, tx, candidates, func(name string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO names(name,kind,family,session_id,repository) VALUES (?,'peer',?,?,?)`, name, family, id, repository)
		return err
	})
}

// assignNamesSchema2 is the naming of schema 2, which its migration applies to the sessions
// of schema 1: one alias per family and repository. Later schemas never call it.
func assignNamesSchema2(ctx context.Context, tx *sql.Tx, now int64, family, id, repository, directory string) error {
	if err := peerName(ctx, tx, family, id, repository, directory); err != nil || repository == "" {
		return err
	}
	var alias, holder string
	err := tx.QueryRowContext(ctx, `SELECT name,holder_id FROM names WHERE kind='alias' AND family=? AND repository=?`, family, repository).Scan(&alias, &holder)
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
	var found int
	if holder != "" {
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=? AND repository=? AND retired_at=0 AND expires_at>?`,
			family, holder, repository, now).Scan(&found)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=? AND repository=? AND retired_at=0 AND expires_at>?`,
		family, id, repository, now).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE names SET holder_id=? WHERE name=?`, id, alias)
	return err
}

// assignNames gives a session its permanent peer name and, for a session with a repository
// that is not a sub-agent, reserves its participant's address (participants sprint, chunk
// 02). It runs in the registration or renewal transaction, so concurrent registrations
// leave at most one holder:
//   - when this session or another active session holds the participant, nothing changes;
//   - otherwise, when this session is the only active session of the participant's family,
//     repository and role that is not a sub-agent, it becomes the holder;
//   - when more than one qualifies, the holder stays empty and the participant records the
//     qualifiers' peer names as its conflict.
//
// Registration and renewal order never decide. A sub-agent gives up an address it holds.
func assignNames(ctx context.Context, tx *sql.Tx, now int64, p participant) error {
	if err := peerName(ctx, tx, p.family, p.id, p.repository, p.directory); err != nil {
		return err
	}
	if p.subagent {
		return releaseAddress(ctx, tx, now, p.family, p.id, "subagent", "daemon")
	}
	if p.repository == "" {
		return nil
	}
	var address, holder, conflict string
	err := tx.QueryRowContext(ctx, `SELECT name,holder_id,conflict FROM names WHERE kind='alias' AND family=? AND repository=? AND role=?`,
		p.family, p.repository, p.role).Scan(&address, &holder, &conflict)
	if errors.Is(err, sql.ErrNoRows) {
		address, err = createAddress(ctx, tx, p)
	}
	if err != nil || holder == p.id && p.wasActive {
		return err
	}
	if holder != "" && holder != p.id {
		valid, err := holds(ctx, tx, now, p.family, holder, p.repository, p.role)
		if err != nil || valid {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,COALESCE(n.name,'') FROM sessions s
		LEFT JOIN names n ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id
		WHERE s.family=? AND s.repository=? AND s.role=? AND s.subagent=0 AND s.retired_at=0 AND s.expires_at>?
		ORDER BY n.name LIMIT 33`, p.family, p.repository, p.role, now)
	if err != nil {
		return err
	}
	var ids, peers []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		ids, peers = append(ids, id), append(peers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	switch {
	case len(ids) == 1 && ids[0] == p.id && holder == p.id:
		// The holder registered again after expiring and is still the only qualifier.
		return nil
	case len(ids) == 1 && ids[0] == p.id:
		return setHolder(ctx, tx, now, address, holder, p.id, "only_qualifier", "session")
	case len(ids) > 1:
		data, err := json.Marshal(peers)
		if err != nil || string(data) == conflict && holder == "" {
			return err
		}
		// A conflict leaves the participant without a holder, also an expired one.
		if _, err := tx.ExecContext(ctx, `UPDATE names SET conflict=?,holder_id='' WHERE name=?`, string(data), address); err != nil {
			return err
		}
		former, err := peerOf(ctx, tx, p.family, holder)
		if err != nil {
			return err
		}
		return participantEvent(ctx, tx, now, address, former, "", "conflict", "session", string(data))
	}
	// A session that is not active, such as a record that a migration kept, takes nothing.
	return nil
}

// createAddress reserves a participant's address: <family>-<label>, with -<role> for a
// role, or a longer free name when that is taken.
func createAddress(ctx context.Context, tx *sql.Tx, p participant) (string, error) {
	base, seed := p.family+"-"+label(p.repository, p.directory), p.repository
	if p.role != "" {
		base, seed = base+"-"+p.role, p.repository+"\x00"+p.role
	}
	sum := sha256.Sum256([]byte(seed))
	var address string
	err := firstFree(ctx, tx, append([]string{base}, lengths(base, hex.EncodeToString(sum[:]))...), func(name string) error {
		address = name
		_, err := tx.ExecContext(ctx, `INSERT INTO names(name,kind,family,repository,role) VALUES (?,'alias',?,?,?)`, name, p.family, p.repository, p.role)
		return err
	})
	return address, err
}

// setHolder makes id the holder of address, clears its conflict and records the change.
func setHolder(ctx context.Context, tx *sql.Tx, now int64, address, former, id, reason, actor string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE names SET holder_id=?,conflict='' WHERE name=?`, id, address); err != nil {
		return err
	}
	var family string
	if err := tx.QueryRowContext(ctx, `SELECT family FROM names WHERE name=?`, address).Scan(&family); err != nil {
		return err
	}
	formerName, err := peerOf(ctx, tx, family, former)
	if err != nil {
		return err
	}
	holderName, err := peerOf(ctx, tx, family, id)
	if err != nil {
		return err
	}
	return participantEvent(ctx, tx, now, address, formerName, holderName, reason, actor, "")
}

// releaseAddress removes a session as the recorded holder of every address and records
// each change. A session that moved to another repository can be recorded for more than one.
func releaseAddress(ctx context.Context, tx *sql.Tx, now int64, family, id, reason, actor string) error {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM names WHERE kind='alias' AND family=? AND holder_id=? ORDER BY name`, family, id)
	if err != nil {
		return err
	}
	var addresses []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			rows.Close()
			return err
		}
		addresses = append(addresses, address)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(addresses) == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE names SET holder_id='' WHERE kind='alias' AND family=? AND holder_id=?`, family, id); err != nil {
		return err
	}
	former, err := peerOf(ctx, tx, family, id)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if err := participantEvent(ctx, tx, now, address, former, "", reason, actor, ""); err != nil {
			return err
		}
	}
	return nil
}

// peerOf is the peer name of a session, or "" for none.
func peerOf(ctx context.Context, tx *sql.Tx, family, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`, family, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// participantEvent records one change of a participant: its holder, its conflict or a
// refused change. Events are trimmed with the audit log.
func participantEvent(ctx context.Context, tx *sql.Tx, now int64, address, former, holder, reason, actor, details string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO participant_events(at,address,former,holder,reason,actor,details) VALUES (?,?,?,?,?,?,?)`,
		now, address, former, holder, reason, actor, details)
	return err
}

// holds reports whether a recorded holder still holds its participant: the holder must be
// active, not a sub-agent, and still registered for the participant's repository and role.
func holds(ctx context.Context, tx *sql.Tx, now int64, family, id, repository, role string) (bool, error) {
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=? AND repository=? AND role=? AND subagent=0
		AND retired_at=0 AND expires_at>?`, family, id, repository, role, now).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
