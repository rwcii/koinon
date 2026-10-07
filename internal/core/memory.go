package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Memory follows the protocol of PROTOCOL.md ("Memory control protocol") and
// docs/PARITY-MEMORY-DESIGN.md contract 4, with the limits of memory.py.

var memoryTypes = map[string]int{"directive": 0, "decision": 1, "gotcha": 2, "handoff": 3, "finding": 4, "status": 5}
var memoryTails = map[string]int{"finding": 25, "handoff": 25, "status": 10}

// memoryLimits are variables of the store so that tests can reach the ceilings quickly.
type memoryLimits struct {
	body, entries, reservedEntries, idem, consumers, retired, snapshotsPerConsumer int64
	logical, reservedBytes, frameBudget, rowWindow                                 int64
	snapshotTTL, ackRetention, idemTTL, consumerTTL, retiredTTL, expiryInterval    float64
}

var defaultMemoryLimits = memoryLimits{
	body: 8192, entries: 5000, reservedEntries: 64, idem: 20000, consumers: 256, retired: 1024,
	snapshotsPerConsumer: 4, logical: 32 << 20, reservedBytes: 64 * 8192, frameBudget: 262144 - 8192,
	rowWindow: 500, snapshotTTL: 3600, ackRetention: 86400, idemTTL: 86400, consumerTTL: 30 * 86400,
	retiredTTL: 90 * 86400, expiryInterval: 30,
}

const entryOverhead = 512

// fingerprintScheme names how memory_idem.fingerprint was computed, so that rows imported
// from another runtime keep their own scheme (chunk 11).
const fingerprintScheme = "go1"

type memoryState struct {
	// op serializes the operations that read a cursor, snapshot or bound and then write
	// it (record, sync, acknowledgement), so each check holds until its write commits.
	op         sync.Mutex
	mu         sync.Mutex
	lastExpiry map[string]float64
	limits     memoryLimits
	// onChange is called with the repository after a commit that advanced its head.
	onChange func(repository string)
}

func memoryError(code, message string) error { return Refusal{code, message} }

// MemoryCaller is the store and provenance of one memory request.
type MemoryCaller struct {
	Repository string
	Family     string
	Name       string
	Consumer   string
}

type MemoryEntry struct {
	Seq           int64    `json:"seq"`
	TS            float64  `json:"ts"`
	Type          string   `json:"type"`
	Scope         string   `json:"scope"`
	ScopeTarget   *string  `json:"scope_target"`
	Path          *string  `json:"path"`
	Body          string   `json:"body"`
	Author        *string  `json:"author"`
	WriterFamily  string   `json:"writer_family"`
	WriterName    string   `json:"writer_name"`
	Consumer      string   `json:"consumer"`
	Revision      int64    `json:"revision"`
	Supersedes    *int64   `json:"supersedes"`
	Revokes       *int64   `json:"revokes"`
	SupersededBy  *int64   `json:"superseded_by"`
	RevokedBy     *int64   `json:"revoked_by"`
	ConflictsWith *int64   `json:"conflicts_with"`
	Expires       *float64 `json:"expires"`
}

const entryColumns = `seq,ts,type,scope,scope_target,path,body,author,writer_family,writer_name,consumer,revision,
	supersedes,revokes,superseded_by,revoked_by,conflicts_with,expires`

func scanEntry(row scanner) (MemoryEntry, error) {
	var e MemoryEntry
	err := row.Scan(&e.Seq, &e.TS, &e.Type, &e.Scope, &e.ScopeTarget, &e.Path, &e.Body, &e.Author, &e.WriterFamily,
		&e.WriterName, &e.Consumer, &e.Revision, &e.Supersedes, &e.Revokes, &e.SupersededBy, &e.RevokedBy,
		&e.ConflictsWith, &e.Expires)
	return e, err
}

func (s *Store) clock() float64 { return float64(s.now().UnixNano()) / 1e9 }

func (s *Store) limits() memoryLimits {
	s.memory.mu.Lock()
	defer s.memory.mu.Unlock()
	if s.memory.limits.body == 0 {
		s.memory.limits = defaultMemoryLimits
	}
	return s.memory.limits
}

// OnMemoryChange sets the content-free hook that runs after a commit advanced a store's
// head. The hook receives only the repository.
func (s *Store) OnMemoryChange(f func(repository string)) {
	s.memory.mu.Lock()
	s.memory.onChange = f
	s.memory.mu.Unlock()
}

// ResolveMemoryCaller names the store and provenance for an active caller: its recorded
// repository, its family and peer name, and the consumer key (its peer name by default).
func (s *Store) ResolveMemoryCaller(ctx context.Context, caller Key, consumer *string) (MemoryCaller, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return MemoryCaller{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return MemoryCaller{}, err
	}
	var m MemoryCaller
	m.Family = caller.Family
	if err := tx.QueryRowContext(ctx, `SELECT s.repository,COALESCE(n.name,'') FROM sessions s LEFT JOIN names n
		ON n.kind='peer' AND n.family=s.family AND n.session_id=s.id WHERE s.family=? AND s.id=?`,
		caller.Family, caller.ID).Scan(&m.Repository, &m.Name); err != nil {
		return MemoryCaller{}, err
	}
	if m.Repository == "" {
		return MemoryCaller{}, memoryError("repo_unresolved", "this session has no Git repository, so it has no memory store")
	}
	m.Consumer = m.Name
	if consumer != nil {
		// A consumer key names a cursor; it is asserted provenance, never authority.
		if utf8.RuneCountInString(*consumer) > 128 || strings.TrimSpace(*consumer) == "" {
			return MemoryCaller{}, memoryError("invalid_request", "a consumer key is 1 to 128 characters and not blank")
		}
		m.Consumer = strings.TrimSpace(*consumer)
	}
	return m, nil
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type memoryUsage struct {
	Entries   int64 `json:"entries"`
	Logical   int64 `json:"logical"`
	Idem      int64 `json:"idem"`
	Retired   int64 `json:"retired"`
	Snapshots int64 `json:"snapshots"`
	Consumers int64 `json:"consumers"`
}

func usage(ctx context.Context, q querier, repo string) (memoryUsage, error) {
	var u memoryUsage
	var entryBytes, itemBytes, idemBytes, cursorBytes, retiredBytes, snapshotBytes int64
	err := q.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM memory_entries WHERE repository=?1),
		(SELECT COALESCE(SUM(length(CAST(body AS BLOB))+COALESCE(length(CAST(path AS BLOB)),0)+
			COALESCE(length(CAST(author AS BLOB)),0)+COALESCE(length(CAST(scope_target AS BLOB)),0)+
			length(CAST(consumer AS BLOB))+?2),0) FROM memory_entries WHERE repository=?1),
		(SELECT COALESCE(SUM(i.bytes),0) FROM memory_snapshot_items i JOIN memory_snapshots s ON s.id=i.id WHERE s.repository=?1),
		(SELECT COUNT(*) FROM memory_idem WHERE repository=?1),
		(SELECT COALESCE(SUM(length(CAST(key AS BLOB))+length(CAST(consumer AS BLOB))+64),0) FROM memory_idem WHERE repository=?1),
		(SELECT COUNT(*) FROM memory_cursors WHERE repository=?1),
		(SELECT COALESCE(SUM(length(CAST(consumer AS BLOB))+64),0) FROM memory_cursors WHERE repository=?1),
		(SELECT COUNT(*) FROM memory_retired WHERE repository=?1),
		(SELECT COALESCE(SUM(length(CAST(consumer AS BLOB))+64),0) FROM memory_retired WHERE repository=?1),
		(SELECT COUNT(*) FROM memory_snapshots WHERE repository=?1),
		(SELECT COALESCE(SUM(length(id)+length(CAST(consumer AS BLOB))+96),0) FROM memory_snapshots WHERE repository=?1)`,
		repo, entryOverhead).Scan(&u.Entries, &entryBytes, &itemBytes, &u.Idem, &idemBytes, &u.Consumers, &cursorBytes,
		&u.Retired, &retiredBytes, &u.Snapshots, &snapshotBytes)
	u.Logical = entryBytes + itemBytes + idemBytes + cursorBytes + retiredBytes + snapshotBytes
	return u, err
}

// caps are one store's logical ceilings; ordinary writes leave the reserve for withdrawals
// and progress.
func (l memoryLimits) caps(class writeClass) (entries, logical int64) {
	if class == control {
		return l.entries, l.logical
	}
	return l.entries - l.reservedEntries, l.logical - l.reservedBytes
}

// memoryWrite runs one store mutation: logical admission (expiring once on a refusal),
// the storage boundary, the body, and enforcement inside the transaction.
func (s *Store) memoryWrite(ctx context.Context, repo string, class writeClass, need, slots int64, body func(*writeTx) error) error {
	l := s.limits()
	entryCap, logicalCap := l.caps(class)
	for attempt := 0; ; attempt++ {
		u, err := usage(ctx, s.db, repo)
		if err != nil {
			return err
		}
		if u.Entries+slots <= entryCap && u.Logical+need <= logicalCap {
			break
		}
		if attempt > 0 {
			return memoryError("capacity", "this store's capacity is reached; nothing was written and stored data is intact")
		}
		if err := s.expireMemory(ctx, repo, true); err != nil {
			return err
		}
	}
	tx, err := s.begin(ctx, class)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_stores(repository,store_id) VALUES (?,?)`, repo, randomID()); err != nil {
		return tx.fail(err)
	}
	if err := body(tx); err != nil {
		return tx.fail(err)
	}
	u, err := usage(ctx, tx, repo)
	if err != nil {
		return tx.fail(err)
	}
	if u.Entries > entryCap || u.Logical > logicalCap {
		tx.Rollback()
		return memoryError("capacity", "this mutation would exceed the store's capacity; it was rolled back and stored data is intact")
	}
	if u.Idem > l.idem {
		tx.Rollback()
		return memoryError("idem_capacity", "the idempotency window is full; nothing was written")
	}
	return tx.Commit()
}

// memoryProgress records progress (activity, page issuance, acknowledgement); it may use
// the reserve and needs no admission.
func (s *Store) memoryProgress(ctx context.Context, repo string, body func(*writeTx) error) error {
	return s.memoryWrite(ctx, repo, control, 0, 0, body)
}

func randomID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// maybeExpire runs expiry at the request boundary, at most once per interval per store.
func (s *Store) maybeExpire(ctx context.Context, repo string) error {
	l := s.limits()
	now := s.clock()
	s.memory.mu.Lock()
	if s.memory.lastExpiry == nil {
		s.memory.lastExpiry = map[string]float64{}
	}
	due := now-s.memory.lastExpiry[repo] >= l.expiryInterval
	if due {
		s.memory.lastExpiry[repo] = now
	}
	s.memory.mu.Unlock()
	if !due {
		return nil
	}
	return s.expireMemory(ctx, repo, false)
}

// expireMemory removes what has outlived its window: expired entries in batches (raising
// the floor, never moving the head), then snapshots, idempotency rows, tombstones and idle
// consumers, which become tombstones. Nothing inside its window is removed.
func (s *Store) expireMemory(ctx context.Context, repo string, reclaim bool) error {
	l := s.limits()
	now := s.clock()
	for {
		removed := int64(0)
		err := s.memoryExpiryStep(ctx, func(tx *writeTx) error {
			rows, err := tx.QueryContext(ctx, `SELECT seq FROM memory_entries WHERE repository=? AND expires IS NOT NULL
				AND expires<? ORDER BY seq LIMIT 200`, repo, now)
			if err != nil {
				return err
			}
			var seqs []int64
			for rows.Next() {
				var seq int64
				rows.Scan(&seq)
				seqs = append(seqs, seq)
			}
			rows.Close()
			if len(seqs) == 0 {
				return nil
			}
			removed = int64(len(seqs))
			for _, seq := range seqs {
				if _, err := tx.ExecContext(ctx, `DELETE FROM memory_entries WHERE repository=? AND seq=?`, repo, seq); err != nil {
					return err
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE memory_stores SET floor=MAX(floor,?) WHERE repository=?`, seqs[len(seqs)-1], repo)
			return err
		})
		if err != nil {
			return err
		}
		if removed < 200 {
			break
		}
	}
	err := s.memoryExpiryStep(ctx, func(tx *writeTx) error {
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM memory_snapshot_items WHERE id IN (SELECT id FROM memory_snapshots WHERE repository=? AND
				((acked=0 AND created<?) OR (acked=1 AND acked_at<?)))`, []any{repo, now - l.snapshotTTL, now - l.ackRetention}},
			{`DELETE FROM memory_snapshots WHERE repository=? AND ((acked=0 AND created<?) OR (acked=1 AND acked_at<?))`,
				[]any{repo, now - l.snapshotTTL, now - l.ackRetention}},
			{`DELETE FROM memory_idem WHERE repository=? AND deadline<?`, []any{repo, now}},
			{`DELETE FROM memory_retired WHERE repository=? AND at<?`, []any{repo, now - l.retiredTTL}},
			{`INSERT OR REPLACE INTO memory_retired(repository,consumer,seq,at) SELECT repository,consumer,seq,? FROM
				memory_cursors WHERE repository=? AND updated<?`, []any{now, repo, now - l.consumerTTL}},
			{`DELETE FROM memory_cursors WHERE repository=? AND updated<?`, []any{repo, now - l.consumerTTL}},
		} {
			if _, err := tx.ExecContext(ctx, statement.sql, statement.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || !reclaim {
		return err
	}
	s.storage.mu.Lock()
	defer s.storage.mu.Unlock()
	if s.storage.blocked != "" {
		return ErrStorageBlocked
	}
	return s.reclaim(ctx)
}

func (s *Store) memoryExpiryStep(ctx context.Context, body func(*writeTx) error) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := body(tx); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// MemoryRecordRequest is one note. Pointers distinguish an absent field from a zero one.
type MemoryRecordRequest struct {
	Type        string   `json:"type"`
	Body        string   `json:"body"`
	Scope       string   `json:"scope"`
	ScopeTarget *string  `json:"scope_target"`
	Path        *string  `json:"path"`
	Author      *string  `json:"author"`
	Supersedes  *int64   `json:"supersedes"`
	Revokes     *int64   `json:"revokes"`
	Expires     *float64 `json:"expires"`
	Key         *string  `json:"key"`
	Deadline    *float64 `json:"deadline"`
}

type MemoryRecordResult struct {
	Seq                int64    `json:"seq"`
	Duplicate          bool     `json:"duplicate"`
	ConflictsWith      *int64   `json:"conflicts_with,omitempty"`
	Deadline           *float64 `json:"deadline"`
	IdempotencyHorizon *float64 `json:"idempotency_horizon,omitempty"`
}

func fingerprint(r MemoryRecordRequest) string {
	data, _ := json.Marshal(map[string]any{"type": r.Type, "body": r.Body, "scope": r.Scope, "scope_target": r.ScopeTarget,
		"path": r.Path, "supersedes": r.Supersedes, "revokes": r.Revokes, "expires": r.Expires, "author": r.Author})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) MemoryRecord(ctx context.Context, m MemoryCaller, r MemoryRecordRequest) (MemoryRecordResult, error) {
	s.memory.op.Lock()
	advanced := false
	defer func() {
		s.memory.op.Unlock()
		// The hook runs after the commit and outside every lock, so it may start any operation.
		if advanced {
			s.changed(m.Repository)
		}
	}()
	l := s.limits()
	now := s.clock()
	if r.Scope == "" {
		r.Scope = "repo"
	}
	_, known := memoryTypes[r.Type]
	switch {
	case !known:
		return MemoryRecordResult{}, memoryError("invalid_request", "unknown entry type")
	case r.Scope != "repo" && r.Scope != "task" && r.Scope != "session":
		return MemoryRecordResult{}, memoryError("invalid_request", "unknown scope")
	case r.Scope != "repo" && (r.ScopeTarget == nil || *r.ScopeTarget == ""):
		return MemoryRecordResult{}, memoryError("invalid_request", "scope "+r.Scope+" requires a scope target")
	case strings.TrimSpace(r.Body) == "":
		return MemoryRecordResult{}, memoryError("invalid_request", "the body is empty")
	case int64(len(r.Body)) > l.body:
		return MemoryRecordResult{}, memoryError("entry_too_large", fmt.Sprintf("the body exceeds %d bytes", l.body))
	}
	if r.Supersedes != nil && *r.Supersedes == 0 {
		r.Supersedes = nil
	}
	if r.Revokes != nil && *r.Revokes == 0 {
		r.Revokes = nil
	}
	if r.Supersedes != nil && r.Revokes != nil {
		return MemoryRecordResult{}, memoryError("invalid_request", "an entry supersedes or revokes, not both")
	}
	probe, _ := json.Marshal(MemoryEntry{Type: r.Type, Scope: r.Scope, ScopeTarget: r.ScopeTarget, Path: r.Path, Body: r.Body,
		Author: r.Author, WriterFamily: m.Family, WriterName: m.Name, Consumer: m.Consumer, Revision: 1,
		Supersedes: r.Supersedes, Revokes: r.Revokes, Expires: r.Expires})
	if int64(len(probe))+1 > l.frameBudget {
		return MemoryRecordResult{}, memoryError("entry_too_large", "this entry could never be delivered in one page")
	}
	print := ""
	if r.Key != nil {
		switch {
		case *r.Key == "" || len(*r.Key) > 256:
			return MemoryRecordResult{}, memoryError("invalid_request", "an idempotency key is 1 to 256 bytes")
		case r.Deadline == nil || math.IsNaN(*r.Deadline) || math.IsInf(*r.Deadline, 0):
			return MemoryRecordResult{}, memoryError("invalid_request", "a keyed write needs a finite deadline fixed before the first send")
		case *r.Deadline > now+l.idemTTL:
			return MemoryRecordResult{}, memoryError("invalid_request", fmt.Sprintf("the deadline is beyond the %.0f s horizon", l.idemTTL))
		case *r.Deadline <= now:
			return MemoryRecordResult{}, memoryError("retry_deadline_expired", "the retry deadline has passed; the first attempt's outcome is unknown")
		}
		print = fingerprint(r)
		if result, found, err := s.duplicate(ctx, s.db, m, r, print); found || err != nil {
			return result, err
		}
	}
	if err := s.maybeExpire(ctx, m.Repository); err != nil {
		return MemoryRecordResult{}, err
	}
	need := int64(len(r.Body)) + int64(len(m.Consumer)) + entryOverhead
	for _, field := range []*string{r.Path, r.Author, r.ScopeTarget} {
		if field != nil {
			need += int64(len(*field))
		}
	}
	if r.Key != nil {
		need += int64(len(*r.Key)+len(m.Consumer)) + 64
	}
	class := ordinary
	target := r.Supersedes
	if r.Revokes != nil {
		target = r.Revokes
	}
	if target != nil {
		class = control
	}
	var result MemoryRecordResult
	err := s.memoryWrite(ctx, m.Repository, class, need, 1, func(tx *writeTx) error {
		if r.Key != nil {
			// A retry that raced the first attempt finds its row here.
			if found, ok, err := s.duplicate(ctx, tx, m, r, print); ok || err != nil {
				result = found
				return err
			}
		}
		revision := int64(1)
		if target != nil {
			var superseded, revoked sql.NullInt64
			err := tx.QueryRowContext(ctx, `SELECT revision,superseded_by,revoked_by FROM memory_entries WHERE repository=? AND seq=?`,
				m.Repository, *target).Scan(&revision, &superseded, &revoked)
			if errors.Is(err, sql.ErrNoRows) {
				return memoryError("no_such_entry", fmt.Sprintf("entry %d does not exist", *target))
			}
			if err != nil {
				return err
			}
			revision++
			// A competing replacement is retained and reported; only the first one marks the target.
			if superseded.Valid {
				result.ConflictsWith = &superseded.Int64
			} else if revoked.Valid {
				result.ConflictsWith = &revoked.Int64
			}
		}
		var seq int64
		if err := tx.QueryRowContext(ctx, `UPDATE memory_stores SET head=head+1 WHERE repository=? RETURNING head`, m.Repository).Scan(&seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_entries(repository,`+entryColumns+`) VALUES
			(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,NULL,?,?)`, m.Repository, seq, now, r.Type, r.Scope, r.ScopeTarget, r.Path,
			r.Body, r.Author, m.Family, m.Name, m.Consumer, revision, r.Supersedes, r.Revokes, result.ConflictsWith, r.Expires); err != nil {
			return err
		}
		if target != nil && result.ConflictsWith == nil {
			column := "superseded_by"
			if r.Revokes != nil {
				column = "revoked_by"
			}
			if _, err := tx.ExecContext(ctx, `UPDATE memory_entries SET `+column+`=? WHERE repository=? AND seq=?`, seq, m.Repository, *target); err != nil {
				return err
			}
		}
		if r.Key != nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO memory_idem(repository,consumer,key,scheme,fingerprint,seq,ts,deadline)
				VALUES (?,?,?,?,?,?,?,?)`, m.Repository, m.Consumer, *r.Key, fingerprintScheme, print, seq, now, *r.Deadline); err != nil {
				return err
			}
		}
		result.Seq, result.Deadline = seq, r.Deadline
		horizon := l.idemTTL
		result.IdempotencyHorizon = &horizon
		tx.onCommit(func() { advanced = true })
		return nil
	})
	if err != nil {
		return MemoryRecordResult{}, err
	}
	return result, nil
}

// duplicate answers a keyed retry within its deadline. A row is compared by the scheme it
// was written with; an unknown scheme cannot be matched and conflicts.
func (s *Store) duplicate(ctx context.Context, q querier, m MemoryCaller, r MemoryRecordRequest, print string) (MemoryRecordResult, bool, error) {
	var scheme, stored string
	var seq int64
	var deadline float64
	err := q.QueryRowContext(ctx, `SELECT scheme,fingerprint,seq,deadline FROM memory_idem WHERE repository=? AND consumer=? AND key=?`,
		m.Repository, m.Consumer, *r.Key).Scan(&scheme, &stored, &seq, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return MemoryRecordResult{}, false, nil
	}
	if err != nil {
		return MemoryRecordResult{}, false, err
	}
	if scheme != fingerprintScheme || stored != print {
		return MemoryRecordResult{}, false, memoryError("idempotency_conflict", "this key was used with different content")
	}
	if deadline != *r.Deadline {
		return MemoryRecordResult{}, false, memoryError("idempotency_conflict", "this key was used with a different retry deadline")
	}
	return MemoryRecordResult{Seq: seq, Duplicate: true, Deadline: r.Deadline}, true, nil
}

func (s *Store) changed(repo string) {
	s.memory.mu.Lock()
	hook := s.memory.onChange
	s.memory.mu.Unlock()
	if hook != nil {
		hook(repo)
	}
}

type memoryCursor struct {
	seq, issued              int64
	snapshot                 sql.NullString
	bootstrapped, resnapshot bool
}

// register returns the consumer's cursor, creating it on first use within the consumer
// bounds; a retired consumer is told so rather than silently restarted.
func (s *Store) register(ctx context.Context, m MemoryCaller) (memoryCursor, error) {
	l := s.limits()
	now := s.clock()
	read := func() (memoryCursor, bool, error) {
		var c memoryCursor
		err := s.db.QueryRowContext(ctx, `SELECT seq,issued,snapshot,bootstrapped,resnapshot FROM memory_cursors
			WHERE repository=? AND consumer=?`, m.Repository, m.Consumer).Scan(&c.seq, &c.issued, &c.snapshot, &c.bootstrapped, &c.resnapshot)
		if errors.Is(err, sql.ErrNoRows) {
			return c, false, nil
		}
		return c, err == nil, err
	}
	c, found, err := read()
	if err != nil || found {
		return c, err
	}
	var retired int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_retired WHERE repository=? AND consumer=?`, m.Repository, m.Consumer).Scan(&retired); err != nil {
		return c, err
	}
	if retired > 0 {
		return c, memoryError("consumer_retired", "this consumer was retired after inactivity; re-register under a new consumer key, which will resync from a snapshot")
	}
	u, err := usage(ctx, s.db, m.Repository)
	if err != nil {
		return c, err
	}
	if u.Consumers >= l.consumers || u.Consumers+u.Retired >= l.consumers+l.retired {
		return c, memoryError("capacity", "this store's consumer capacity is reached")
	}
	// The charge covers the cursor and its eventual tombstone.
	need := 2 * (int64(len(m.Consumer)) + 64)
	err = s.memoryWrite(ctx, m.Repository, ordinary, need, 0, func(tx *writeTx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_cursors(repository,consumer,seq,issued,snapshot,bootstrapped,
			resnapshot,updated) VALUES (?,?,0,0,NULL,0,0,?)`, m.Repository, m.Consumer, now)
		return err
	})
	if err != nil {
		return c, err
	}
	c, _, err = read()
	return c, err
}

func (s *Store) touch(ctx context.Context, m MemoryCaller) error {
	return s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET updated=? WHERE repository=? AND consumer=?`, s.clock(), m.Repository, m.Consumer)
		return err
	})
}

type memorySnapshot struct {
	consumer      string
	head, items   int64
	issued        int64
	created       float64
	acked         bool
	exists, owned bool
}

func (s *Store) snapshotState(ctx context.Context, m MemoryCaller, id string) (memorySnapshot, error) {
	var st memorySnapshot
	err := s.db.QueryRowContext(ctx, `SELECT consumer,head,items,issued,created,acked FROM memory_snapshots WHERE id=? AND repository=?`,
		id, m.Repository).Scan(&st.consumer, &st.head, &st.items, &st.issued, &st.created, &st.acked)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	st.exists, st.owned = err == nil, err == nil && st.consumer == m.Consumer
	return st, err
}

type MemorySyncRequest struct {
	SnapshotID string `json:"snapshot_id"`
	PageToken  int64  `json:"page_token"`
}

// MemorySync returns a snapshot page or a delta batch and never moves the cursor.
func (s *Store) MemorySync(ctx context.Context, m MemoryCaller, r MemorySyncRequest) (map[string]any, error) {
	s.memory.op.Lock()
	defer s.memory.op.Unlock()
	l := s.limits()
	if err := s.maybeExpire(ctx, m.Repository); err != nil {
		return nil, err
	}
	c, err := s.register(ctx, m)
	if err != nil {
		return nil, err
	}
	if err := s.touch(ctx, m); err != nil {
		return nil, err
	}
	now := s.clock()
	if c.snapshot.Valid {
		st, err := s.snapshotState(ctx, m, c.snapshot.String)
		if err != nil {
			return nil, err
		}
		if !st.owned || !st.acked && now-st.created > l.snapshotTTL {
			// The obligation to finish a snapshot stays; only the unusable snapshot goes.
			if err := s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
				_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET snapshot=NULL,resnapshot=1 WHERE repository=? AND consumer=?`,
					m.Repository, m.Consumer)
				return err
			}); err != nil {
				return nil, err
			}
			c.snapshot, c.resnapshot = sql.NullString{}, true
		}
	}
	var floor int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT floor FROM memory_stores WHERE repository=?),0)`, m.Repository).Scan(&floor); err != nil {
		return nil, err
	}
	if c.snapshot.Valid || c.resnapshot || !c.bootstrapped || c.seq < floor {
		id := c.snapshot.String
		if !c.snapshot.Valid {
			if id, err = s.freeze(ctx, m); err != nil {
				return nil, err
			}
		}
		return s.snapshotPage(ctx, m, id, r)
	}
	return s.delta(ctx, m, c)
}

// freeze copies the entries live at the current head into a snapshot owned by the consumer:
// directives first, then decisions, gotchas, handoffs, findings and status, newest first,
// with the newest 25 findings, 25 handoffs and 10 status entries.
func (s *Store) freeze(ctx context.Context, m MemoryCaller) (string, error) {
	l := s.limits()
	now := s.clock()
	var held int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_snapshots WHERE repository=? AND consumer=? AND
		((acked=0 AND created>=?) OR (acked=1 AND acked_at>=?))`, m.Repository, m.Consumer, now-l.snapshotTTL, now-l.ackRetention).Scan(&held); err != nil {
		return "", err
	}
	if held >= l.snapshotsPerConsumer {
		return "", memoryError("snapshot_capacity", fmt.Sprintf("%d snapshots of this consumer are still retained", held))
	}
	var head int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT head FROM memory_stores WHERE repository=?),0)`, m.Repository).Scan(&head); err != nil {
		return "", err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE repository=? AND seq<=? AND
		(superseded_by IS NULL OR superseded_by>?) AND (revoked_by IS NULL OR revoked_by>?) AND (expires IS NULL OR expires>?)`,
		m.Repository, head, head, head, now)
	if err != nil {
		return "", err
	}
	var members []MemoryEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return "", err
		}
		members = append(members, e)
	}
	rows.Close()
	sort.SliceStable(members, func(i, j int) bool {
		a, b := memoryTypes[members[i].Type], memoryTypes[members[j].Type]
		if a != b {
			return a < b
		}
		return members[i].Seq > members[j].Seq
	})
	kept := members[:0]
	counts := map[string]int{}
	for _, e := range members {
		counts[e.Type]++
		if tail, capped := memoryTails[e.Type]; capped && counts[e.Type] > tail {
			continue
		}
		kept = append(kept, e)
	}
	id := randomID()
	payloads := make([][]byte, len(kept))
	need := int64(len(id)+len(m.Consumer)) + 96
	for i, e := range kept {
		payloads[i], _ = json.Marshal(e)
		need += int64(len(payloads[i]) + 1)
	}
	err = s.memoryWrite(ctx, m.Repository, ordinary, need, 0, func(tx *writeTx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_snapshots(id,repository,consumer,head,created,items,issued,acked)
			VALUES (?,?,?,?,?,?,0,0)`, id, m.Repository, m.Consumer, head, now, len(kept)); err != nil {
			return err
		}
		for i, payload := range payloads {
			if _, err := tx.ExecContext(ctx, `INSERT INTO memory_snapshot_items(id,position,seq,payload,bytes) VALUES (?,?,?,?,?)`,
				id, i, kept[i].Seq, string(payload), len(payload)+1); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET snapshot=? WHERE repository=? AND consumer=?`, id, m.Repository, m.Consumer)
		return err
	})
	return id, err
}

// bounded takes items while their encoded sizes fit one page; a first item that cannot fit
// is reported, never dropped.
func bounded(sizes []int64, budget int64) (int, error) {
	total := int64(0)
	for i, size := range sizes {
		if total+size > budget {
			if i == 0 {
				return 0, memoryError("entry_too_large", "an entry exceeds one page and cannot be delivered")
			}
			return i, nil
		}
		total += size
	}
	return len(sizes), nil
}

func (s *Store) snapshotPage(ctx context.Context, m MemoryCaller, id string, r MemorySyncRequest) (map[string]any, error) {
	l := s.limits()
	st, err := s.snapshotState(ctx, m, id)
	if err != nil {
		return nil, err
	}
	switch {
	case !st.exists:
		return nil, memoryError("snapshot_expired", "the snapshot expired; sync again without a page token")
	case !st.owned:
		return nil, memoryError("foreign_snapshot", "the snapshot belongs to another consumer")
	case !st.acked && s.clock()-st.created > l.snapshotTTL:
		return nil, memoryError("snapshot_expired", "the snapshot expired; sync again without a page token")
	case r.PageToken < 0 || r.PageToken != 0 && r.SnapshotID == "":
		return nil, memoryError("stale_page_token", "a page token continues the snapshot that its snapshot_id names")
	}
	if r.SnapshotID != "" && r.SnapshotID != id {
		other, err := s.snapshotState(ctx, m, r.SnapshotID)
		if err != nil {
			return nil, err
		}
		if other.exists && !other.owned {
			return nil, memoryError("foreign_snapshot", "the snapshot belongs to another consumer")
		}
		return nil, memoryError("stale_page_token", "that snapshot is not this consumer's current snapshot")
	}
	if r.PageToken > st.issued {
		return nil, memoryError("stale_page_token", "pages are issued in order")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload,bytes FROM memory_snapshot_items WHERE id=? AND position>=? ORDER BY position`, id, r.PageToken)
	if err != nil {
		return nil, err
	}
	var payloads []json.RawMessage
	var sizes []int64
	for rows.Next() {
		var payload string
		var size int64
		rows.Scan(&payload, &size)
		payloads, sizes = append(payloads, json.RawMessage(payload)), append(sizes, size)
	}
	rows.Close()
	n, err := bounded(sizes, l.frameBudget)
	if err != nil {
		return nil, err
	}
	next := r.PageToken + int64(n)
	if next > st.issued {
		if err := s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
			_, err := tx.ExecContext(ctx, `UPDATE memory_snapshots SET issued=? WHERE id=? AND issued<?`, next, id, next)
			return err
		}); err != nil {
			return nil, err
		}
	}
	entries := payloads[:n]
	if entries == nil {
		entries = []json.RawMessage{}
	}
	return map[string]any{"kind": "snapshot", "snapshot_id": id, "head": st.head, "entries": entries, "page_token": next,
		"total": st.items, "more": n < len(payloads) || next < st.items}, nil
}

func (s *Store) delta(ctx context.Context, m MemoryCaller, c memoryCursor) (map[string]any, error) {
	l := s.limits()
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE repository=? AND seq>? ORDER BY seq LIMIT ?`,
		m.Repository, c.seq, l.rowWindow)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	var sizes []int64
	var seqs []int64
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		data, _ := json.Marshal(e)
		entries, sizes, seqs = append(entries, data), append(sizes, int64(len(data)+1)), append(seqs, e.Seq)
	}
	rows.Close()
	n, err := bounded(sizes, l.frameBudget)
	if err != nil {
		return nil, err
	}
	end := c.seq
	if n > 0 {
		end = seqs[n-1]
	}
	if end > c.issued {
		if err := s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
			_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET issued=? WHERE repository=? AND consumer=? AND issued<?`,
				end, m.Repository, m.Consumer, end)
			return err
		}); err != nil {
			return nil, err
		}
	}
	var head int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT head FROM memory_stores WHERE repository=?),0)`, m.Repository).Scan(&head); err != nil {
		return nil, err
	}
	page := entries[:n]
	if page == nil {
		page = []json.RawMessage{}
	}
	return map[string]any{"kind": "delta", "entries": page, "cursor": c.seq, "next_cursor": end, "head": head,
		"more": n < len(entries) || end < head}, nil
}

type MemoryAckRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Through    *int64 `json:"through"`
}

// MemoryAck moves the cursor: through a fully issued snapshot, or monotonically through
// an issued delta.
func (s *Store) MemoryAck(ctx context.Context, m MemoryCaller, r MemoryAckRequest) (map[string]any, error) {
	s.memory.op.Lock()
	defer s.memory.op.Unlock()
	l := s.limits()
	if err := s.maybeExpire(ctx, m.Repository); err != nil {
		return nil, err
	}
	c, err := s.register(ctx, m)
	if err != nil {
		return nil, err
	}
	if err := s.touch(ctx, m); err != nil {
		return nil, err
	}
	if r.SnapshotID != "" {
		st, err := s.snapshotState(ctx, m, r.SnapshotID)
		if err != nil {
			return nil, err
		}
		switch {
		case !st.exists:
			return nil, memoryError("snapshot_expired", "the snapshot expired; sync again without a page token")
		case !st.owned:
			return nil, memoryError("foreign_snapshot", "the snapshot belongs to another consumer")
		case st.acked:
			return map[string]any{"cursor": st.head, "snapshot": r.SnapshotID, "complete": true, "replayed": true}, nil
		case !c.snapshot.Valid || c.snapshot.String != r.SnapshotID:
			return nil, memoryError("stale_snapshot", "that snapshot is not this consumer's open snapshot")
		case s.clock()-st.created > l.snapshotTTL:
			return nil, memoryError("snapshot_expired", "the snapshot expired; sync again without a page token")
		case st.issued < st.items:
			return nil, memoryError("snapshot_incomplete", fmt.Sprintf("%d of %d pages were issued; page to the end before acknowledging", st.issued, st.items))
		}
		now := s.clock()
		err = s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
			if _, err := tx.ExecContext(ctx, `UPDATE memory_snapshots SET acked=1,acked_at=? WHERE id=?`, now, r.SnapshotID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET seq=?,issued=MAX(issued,?),snapshot=NULL,bootstrapped=1,
				resnapshot=0 WHERE repository=? AND consumer=?`, st.head, st.head, m.Repository, m.Consumer)
			return err
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"cursor": st.head, "snapshot": r.SnapshotID, "complete": true, "replayed": false}, nil
	}
	through := int64(0)
	if r.Through != nil {
		through = *r.Through
	}
	switch {
	case c.snapshot.Valid || c.resnapshot:
		return nil, memoryError("snapshot_open", "finish and acknowledge the open snapshot first")
	case !c.bootstrapped:
		return nil, memoryError("not_bootstrapped", "acknowledge a complete snapshot first")
	case through < c.seq:
		return map[string]any{"cursor": c.seq, "ignored": "not monotonic"}, nil
	case through > c.issued:
		return nil, memoryError("not_issued", fmt.Sprintf("cannot acknowledge %d; %d was issued", through, c.issued))
	}
	err = s.memoryProgress(ctx, m.Repository, func(tx *writeTx) error {
		_, err := tx.ExecContext(ctx, `UPDATE memory_cursors SET seq=? WHERE repository=? AND consumer=?`, through, m.Repository, m.Consumer)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"cursor": through}, nil
}

// MemoryRecall finds live entries whose body contains the query, newest first. The scan is
// complete and matches the query literally.
func (s *Store) MemoryRecall(ctx context.Context, m MemoryCaller, query string, before int64) (map[string]any, error) {
	l := s.limits()
	if strings.TrimSpace(query) == "" {
		return nil, memoryError("invalid_request", "the query is empty")
	}
	if before <= 0 {
		before = 1 << 62
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE repository=? AND
		superseded_by IS NULL AND revoked_by IS NULL AND (expires IS NULL OR expires>?) AND seq<? AND
		body LIKE ? ESCAPE '\' ORDER BY seq DESC LIMIT ?`, m.Repository, s.clock(), before, "%"+escaped+"%", l.rowWindow+1)
	if err != nil {
		return nil, err
	}
	var entries []json.RawMessage
	var sizes, seqs []int64
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		data, _ := json.Marshal(e)
		entries, sizes, seqs = append(entries, data), append(sizes, int64(len(data)+1)), append(seqs, e.Seq)
	}
	rows.Close()
	beyond := int64(len(entries)) > l.rowWindow
	if beyond {
		entries, sizes = entries[:l.rowWindow], sizes[:l.rowWindow]
	}
	n, err := bounded(sizes, l.frameBudget)
	if err != nil {
		return nil, err
	}
	more := n < len(entries) || beyond
	var next any
	if more && n > 0 {
		next = seqs[n-1]
	}
	page := entries[:n]
	if page == nil {
		page = []json.RawMessage{}
	}
	return map[string]any{"entries": page, "more": more, "indexed": false, "next_before": next}, nil
}

// MemoryStatus reports the store's head, floor, usage, limits, lifetimes and consumers.
func (s *Store) MemoryStatus(ctx context.Context, m MemoryCaller, after string) (map[string]any, error) {
	l := s.limits()
	var head, floor int64
	storeID := ""
	err := s.db.QueryRowContext(ctx, `SELECT head,floor,store_id FROM memory_stores WHERE repository=?`, m.Repository).Scan(&head, &floor, &storeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	u, err := usage(ctx, s.db, m.Repository)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT consumer,seq,snapshot,bootstrapped FROM memory_cursors WHERE repository=? AND consumer>?
		ORDER BY consumer LIMIT ?`, m.Repository, after, l.rowWindow+1)
	if err != nil {
		return nil, err
	}
	var consumers []map[string]any
	var sizes []int64
	for rows.Next() {
		var consumer string
		var seq int64
		var snapshot sql.NullString
		var bootstrapped bool
		rows.Scan(&consumer, &seq, &snapshot, &bootstrapped)
		item := map[string]any{"consumer": consumer, "cursor": seq, "lag": head - seq, "snapshot": nil, "bootstrapped": bootstrapped}
		if snapshot.Valid {
			item["snapshot"] = snapshot.String
		}
		data, _ := json.Marshal(item)
		consumers, sizes = append(consumers, item), append(sizes, int64(len(data)+1))
	}
	rows.Close()
	beyond := int64(len(consumers)) > l.rowWindow
	if beyond {
		consumers, sizes = consumers[:l.rowWindow], sizes[:l.rowWindow]
	}
	n, err := bounded(sizes, l.frameBudget)
	if err != nil {
		return nil, err
	}
	more := n < len(consumers) || beyond
	var next any
	if more && n > 0 {
		next = consumers[n-1]["consumer"]
	}
	page := consumers[:n]
	if page == nil {
		page = []map[string]any{}
	}
	storage, err := s.StorageStatus(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"repository": m.Repository, "store_id": storeID, "head": head, "floor": floor, "usage": u, "fts": false, "indexed": false,
		"record_format": 2, "storage": storage, "consumers": page, "more": more, "next_after": next,
		"limits": map[string]any{"entries": l.entries, "logical_bytes": l.logical, "body": l.body, "reserved_entries": l.reservedEntries,
			"reserved_bytes": l.reservedBytes, "idempotency_rows": l.idem, "consumers": l.consumers, "retired": l.retired,
			"snapshots_per_consumer": l.snapshotsPerConsumer, "page_bytes": l.frameBudget},
		"lifetimes": map[string]any{"snapshot": l.snapshotTTL, "acknowledgement": l.ackRetention, "idempotency": l.idemTTL,
			"consumer": l.consumerTTL, "retired": l.retiredTTL, "expiry_interval": l.expiryInterval},
	}, nil
}
