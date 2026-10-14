package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Memory entries in the dashboard (#224). The maintainer reads one store's entries and
// records, supersedes and revokes entries as writer family and name `maintainer`. Every
// change goes through MemoryRecord, so the limits, idempotency, capacity refusals and
// conflict reporting are those of an agent's write; an entry is never changed in place.

const (
	dashboardEntryPage = 50
	// A body of the largest entry, percent-encoded, with the other fields.
	memoryFormLimit = 3*8192 + 4096
)

// maintainerMemory is the provenance of the maintainer's memory writes.
func maintainerMemory(repository string) MemoryCaller {
	return MemoryCaller{Repository: repository, Family: maintainerKey.Family, Name: "maintainer", Consumer: "maintainer"}
}

// EntryView is a memory entry as the dashboard lists it, with its state at read time.
type EntryView struct {
	MemoryEntry
	State string
}

// entriesData is one store's entries: the filters, a page and the form keys of this render.
type entriesData struct {
	Repository string
	Search     string
	Type       string
	All        bool
	Entries    []EntryView
	Next       int64
	// Key prefixes this render's idempotency keys; each form adds its own suffix, so a
	// resubmitted form is a duplicate and never a second entry.
	Key      string
	Deadline int64
	Types    []string
}

// memoryData is the memory view: the stores, and the entries of the selected store.
type memoryData struct {
	storesData
	Selected *entriesData
}

func entryTypes() []string {
	types := make([]string, 0, len(memoryTypes))
	for t := range memoryTypes {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return memoryTypes[types[i]] < memoryTypes[types[j]] })
	return types
}

// dashboardEntries lists a store's notes, newest first, before a sequence. Without all it
// lists only live entries; search matches body or path as a substring, ignoring ASCII case.
func (s *Store) dashboardEntries(ctx context.Context, repository, search, kind string, all bool, before int64) ([]EntryView, int64, error) {
	if !validRepository(repository) || len(search) > dashboardSearchMax || (kind != "" && !knownType(kind)) || before < 0 {
		return nil, 0, ErrInvalid
	}
	if before == 0 {
		before = 1 << 62
	}
	now := s.clock()
	where := `repository=? AND type<>'work-event' AND seq<?`
	args := []any{repository, before}
	if !all {
		where += ` AND superseded_by IS NULL AND revoked_by IS NULL AND (expires IS NULL OR expires>?)`
		args = append(args, now)
	}
	if search != "" {
		where += ` AND (body LIKE ? ESCAPE '\' OR COALESCE(path,'') LIKE ? ESCAPE '\')`
		args = append(args, likePattern(search), likePattern(search))
	}
	if kind != "" {
		where += ` AND type=?`
		args = append(args, kind)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE `+where+
		` ORDER BY seq DESC LIMIT `+strconv.Itoa(dashboardEntryPage+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := []EntryView{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		v := EntryView{MemoryEntry: e, State: "live"}
		switch {
		case e.RevokedBy != nil:
			v.State = "revoked"
		case e.SupersededBy != nil:
			v.State = "superseded"
		case e.Expires != nil && *e.Expires <= now:
			v.State = "expired"
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(result) <= dashboardEntryPage {
		return result, 0, nil
	}
	result = result[:dashboardEntryPage]
	return result, result[len(result)-1].Seq, nil
}

func knownType(kind string) bool {
	_, ok := memoryTypes[kind]
	return ok
}

// selectedEntries reads the memory view's store parameters: store, q, type, all and before.
func (d *Daemon) selectedEntries(ctx context.Context, q url.Values) (*entriesData, error) {
	repository := q.Get("store")
	if repository == "" {
		return nil, nil
	}
	before := int64(0)
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return nil, ErrInvalid
		}
		before = n
	}
	data := &entriesData{Repository: repository, Search: q.Get("q"), Type: q.Get("type"), All: q.Get("all") == "1", Types: entryTypes()}
	var err error
	if data.Entries, data.Next, err = d.store.dashboardEntries(ctx, repository, data.Search, data.Type, data.All, before); err != nil {
		return nil, err
	}
	var key [12]byte
	if _, err := rand.Read(key[:]); err != nil {
		return nil, err
	}
	data.Key = hex.EncodeToString(key[:])
	// The deadline leaves a minute of the idempotency horizon for the submission.
	data.Deadline = int64(d.store.clock() + d.store.limits().idemTTL - 60)
	return data, nil
}

// memoryEntry reads one note of a store for an action; a work event is not a note.
func (s *Store) memoryEntry(ctx context.Context, repository string, seq int64) (MemoryEntry, error) {
	e, err := scanEntry(s.db.QueryRowContext(ctx, `SELECT `+entryColumns+` FROM memory_entries WHERE repository=? AND seq=?
		AND type<>'work-event'`, repository, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return e, memoryError("no_such_entry", "the entry does not exist")
	}
	return e, err
}

// memoryActions adds the dashboard's memory actions: record a new entry or a replacement
// of one (supersedes), and revoke one with a reason. Each answers 303 to the store's entries.
func (d *Daemon) memoryActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	// act audits one memory action like the other actions. A recorded entry that another
	// writer's replacement or revocation preceded is kept and reported as memory_conflict.
	act := func(w http.ResponseWriter, r *http.Request, action, target, repository string, valid bool,
		apply func(context.Context) (MemoryRecordResult, error), accepted string) {
		query := url.Values{"store": {repository}}
		if !validRepository(repository) {
			query = nil
		}
		ctx, pending := withAudit(r.Context(), action, target)
		if !valid {
			d.store.auditRefusal(ctx, pending, "invalid_request")
			done(w, r, "memory", query, "invalid_request")
			return
		}
		result, err := apply(ctx)
		if err != nil {
			code := errorCode(err)
			d.store.auditRefusal(ctx, pending, code)
			done(w, r, "memory", query, code)
			return
		}
		if result.ConflictsWith != nil {
			accepted = "memory_conflict"
		}
		done(w, r, "memory", query, accepted)
	}
	// keyed reads a form's idempotency key and deadline.
	keyed := func(r *http.Request) (*string, *float64, bool) {
		key := r.PostForm.Get("key")
		deadline, err := strconv.ParseFloat(r.PostForm.Get("deadline"), 64)
		if key == "" || len(key) > 256 || err != nil {
			return nil, nil, false
		}
		return &key, &deadline, true
	}
	optional := func(r *http.Request, name string) *string {
		if v := r.PostForm.Get(name); v != "" {
			return &v
		}
		return nil
	}
	// existing refuses a repository without a memory store, which a write would create.
	existing := func(ctx context.Context, repository string) error {
		store, err := storeOf(ctx, d.store.db, repository)
		if err == nil && store == "" {
			err = memoryError("store_not_found", "no memory store has that repository")
		}
		return err
	}

	mux.HandleFunc("POST /dashboard/actions/memory-record", authed(memoryFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		repository, body := r.PostForm.Get("repository"), r.PostForm.Get("body")
		key, deadline, ok := keyed(r)
		valid := ok && validRepository(repository) && utf8.ValidString(body)
		var supersedes *int64
		action, accepted := "memory-record", "memory_recorded"
		target := repository + " " + r.PostForm.Get("type") + " bytes " + strconv.Itoa(len(body))
		if v := r.PostForm.Get("supersedes"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			valid = valid && err == nil && n > 0
			supersedes = &n
			action, accepted = "memory-supersede", "memory_superseded"
			target = repository + " supersedes " + strconv.FormatInt(n, 10) + " bytes " + strconv.Itoa(len(body))
		}
		if !validRepository(repository) || len(target) > 4400 {
			target = "invalid request"
		}
		act(w, r, action, target, repository, valid, func(ctx context.Context) (MemoryRecordResult, error) {
			if err := existing(ctx, repository); err != nil {
				return MemoryRecordResult{}, err
			}
			return d.store.MemoryRecord(ctx, maintainerMemory(repository), MemoryRecordRequest{Type: r.PostForm.Get("type"),
				Body: body, Scope: r.PostForm.Get("scope"), ScopeTarget: optional(r, "scope_target"), Path: optional(r, "path"),
				Supersedes: supersedes, Key: key, Deadline: deadline})
		}, accepted)
	}))

	mux.HandleFunc("POST /dashboard/actions/memory-revoke", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		repository, reason := r.PostForm.Get("repository"), r.PostForm.Get("reason")
		key, deadline, ok := keyed(r)
		seq, err := strconv.ParseInt(r.PostForm.Get("seq"), 10, 64)
		valid := ok && err == nil && seq > 0 && validRepository(repository) && utf8.ValidString(reason)
		target := repository + " revokes " + strconv.FormatInt(seq, 10)
		if !validRepository(repository) {
			target = "invalid request"
		}
		act(w, r, "memory-revoke", target, repository, valid, func(ctx context.Context) (MemoryRecordResult, error) {
			if strings.TrimSpace(reason) == "" {
				return MemoryRecordResult{}, memoryError("reason_required", "a revocation needs a reason")
			}
			if err := existing(ctx, repository); err != nil {
				return MemoryRecordResult{}, err
			}
			// The revocation keeps the revoked entry's type and scope; its body is the reason.
			e, err := d.store.memoryEntry(ctx, repository, seq)
			if err != nil {
				return MemoryRecordResult{}, err
			}
			return d.store.MemoryRecord(ctx, maintainerMemory(repository), MemoryRecordRequest{Type: e.Type, Body: reason,
				Scope: e.Scope, ScopeTarget: e.ScopeTarget, Path: e.Path, Revokes: &seq, Key: key, Deadline: deadline})
		}, "memory_revoked")
	}))
}
