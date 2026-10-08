package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Work items in the dashboard (#225). The maintainer reads finished items too and one
// item with its history, and creates, edits, proposes and finishes items. Every change goes
// through Work, so the revision checks, refusal codes, limits and idempotency are those of an
// agent's request; each work event names family and name `maintainer`.

const (
	// The scope fields of one item, percent-encoded, with the other fields.
	workFormLimit = 3*3*8192 + 4096
	// dashboardEventMax bounds the history that one item's page lists, newest first.
	dashboardEventMax = 200
	// maintainerConsumer is the consumer key of the maintainer's own work requests.
	maintainerConsumer = "maintainer"
)

var workLifecycles = map[string]bool{"": true, "open": true, "active": true, "blocked": true, "finished": true, "all": true}

// maintainerWork is the provenance of the maintainer's work requests for consumer.
func maintainerWork(repository, consumer string) MemoryCaller {
	return MemoryCaller{Repository: repository, Family: maintainerKey.Family, Name: "maintainer", Consumer: consumer}
}

// WorkEventView is one event of an item's history.
type WorkEventView struct {
	Seq          int64
	Kind         string
	Revision     int64
	At           float64
	WriterFamily string
	WriterName   string
	Consumer     string
}

// workItemData is one item with its history, for the item's page.
type workItemData struct {
	Repository string
	Item       WorkView
	Events     []WorkEventView
}

// workData is the work view: the stores with their filtered items, the item that is open,
// and this render's form keys.
type workData struct {
	storesData
	Lifecycle string
	Open      *workItemData
	// Key prefixes this render's idempotency keys; each form adds its own suffix.
	Key      string
	Deadline int64
	// Repositories are every store's repository, for the create form.
	Repositories []string
}

// keep reports whether an item matches the view's lifecycle filter; the default lists
// unfinished items.
func keepLifecycle(filter, lifecycle string) bool {
	switch filter {
	case "":
		return lifecycle != "finished"
	case "all":
		return true
	}
	return filter == lifecycle
}

// workEvents reads an item's newest events with the writer of each.
func (s *Store) workEvents(ctx context.Context, repository, workID string) ([]WorkEventView, error) {
	store, err := storeOf(ctx, s.db, repository)
	if err != nil || store == "" {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT w.seq,w.kind,w.revision,COALESCE(e.ts,0),COALESCE(e.writer_family,''),
		COALESCE(e.writer_name,''),COALESCE(e.consumer,'') FROM work_events w
		LEFT JOIN memory_entries e ON e.repository=? AND e.seq=w.seq
		WHERE w.store=? AND w.work_id=? ORDER BY w.seq DESC LIMIT ?`, repository, store, workID, dashboardEventMax)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []WorkEventView{}
	for rows.Next() {
		var e WorkEventView
		if err := rows.Scan(&e.Seq, &e.Kind, &e.Revision, &e.At, &e.WriterFamily, &e.WriterName, &e.Consumer); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// openItem reads the item that the view's store and item parameters name.
func (d *Daemon) openItem(ctx context.Context, q url.Values) (*workItemData, error) {
	repository, workID := q.Get("store"), q.Get("item")
	if repository == "" && workID == "" {
		return nil, nil
	}
	if !validRepository(repository) || len(workID) != 32 {
		return nil, ErrInvalid
	}
	now := d.store.clock()
	views, err := d.store.workViews(ctx, repository, now)
	if err != nil {
		return nil, err
	}
	for _, v := range views {
		if v.WorkID == workID {
			events, err := d.store.workEvents(ctx, repository, workID)
			return &workItemData{Repository: repository, Item: v, Events: events}, err
		}
	}
	// A finished item past its retention is gone, like an unknown one.
	return nil, ErrInvalid
}

// formKey is a fresh prefix for one render's idempotency keys.
func formKey() (string, error) {
	var key [12]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", err
	}
	return "dashboard-" + hex.EncodeToString(key[:]), nil
}

// lines splits a references field into its non-empty lines.
func lines(text string) []string {
	result := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

// workFieldsOf encodes request fields for Work.
func workFieldsOf(values map[string]any) map[string]json.RawMessage {
	fields := map[string]json.RawMessage{}
	for name, value := range values {
		fields[name], _ = json.Marshal(value)
	}
	return fields
}

// workActions adds the dashboard's work item actions. Each answers 303 to the work view,
// on the item's page when it names one.
func (d *Daemon) workActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	run func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string)) {
	// common reads the fields that every item action has: the store, the item, its revision,
	// and the form's idempotency key and deadline.
	type common struct {
		repository, workID, key string
		revision                int64
		deadline                float64
		valid                   bool
	}
	read := func(r *http.Request) common {
		c := common{repository: r.PostForm.Get("repository"), workID: r.PostForm.Get("work_id"), key: r.PostForm.Get("key")}
		var err1, err2 error
		c.revision, err1 = strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
		c.deadline, err2 = strconv.ParseFloat(r.PostForm.Get("deadline"), 64)
		c.valid = err1 == nil && err2 == nil && c.revision > 0 && validRepository(c.repository) && len(c.workID) == 32 &&
			c.key != "" && len(c.key) <= 250 // a finish adds "-start" for the claim it takes
		return c
	}
	page := func(c common) url.Values {
		if !validRepository(c.repository) || len(c.workID) != 32 {
			return nil
		}
		return url.Values{"store": {c.repository}, "item": {c.workID}}
	}
	target := func(c common, rest string) string {
		if !validRepository(c.repository) || len(c.workID) > 64 {
			return "invalid request"
		}
		return c.repository + " " + c.workID + rest
	}
	// liveClaim reads the item's live claim, if any, from its current view.
	liveClaim := func(ctx context.Context, c common) (*workClaimView, error) {
		views, err := d.store.workViews(ctx, c.repository, d.store.clock())
		if err != nil {
			return nil, err
		}
		for _, v := range views {
			if v.WorkID == c.workID {
				if v.LeaseValid {
					return v.CurrentClaim, nil
				}
				return nil, nil
			}
		}
		return nil, workError("work_not_found", "the work item is absent or its retention has expired")
	}

	mux.HandleFunc("POST /dashboard/actions/work-create", authed(workFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		repository, key := r.PostForm.Get("repository"), r.PostForm.Get("key")
		deadline, err := strconv.ParseFloat(r.PostForm.Get("deadline"), 64)
		title, criteria, nonGoals := r.PostForm.Get("title"), r.PostForm.Get("criteria"), r.PostForm.Get("non_goals")
		valid := err == nil && validRepository(repository) && key != "" && len(key) <= 256 &&
			utf8.ValidString(title+criteria+nonGoals+r.PostForm.Get("proposed_assignee")+r.PostForm.Get("references"))
		label := repository + " create"
		if !validRepository(repository) {
			label = "invalid request"
		}
		run(w, r, "work-create", label, "work", nil, valid, func(ctx context.Context) error {
			if store, err := storeOf(ctx, d.store.db, repository); err != nil || store == "" {
				if err == nil {
					err = memoryError("store_not_found", "no memory store has that repository")
				}
				return err
			}
			values := map[string]any{"title": title, "criteria": criteria, "non_goals": nonGoals, "key": key, "deadline": deadline,
				"references": lines(r.PostForm.Get("references"))}
			if assignee := strings.TrimSpace(r.PostForm.Get("proposed_assignee")); assignee != "" {
				values["proposed_assignee"] = assignee
			}
			_, err := d.store.Work(ctx, maintainerWork(repository, maintainerConsumer), "work-create", workFieldsOf(values))
			return err
		}, "work_created", nil)
	}))

	mux.HandleFunc("POST /dashboard/actions/work-propose", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		c := read(r)
		assignee := strings.TrimSpace(r.PostForm.Get("proposed_assignee"))
		valid := c.valid && utf8.ValidString(assignee)
		run(w, r, "work-propose", target(c, " to "+assignee), "work", page(c), valid, func(ctx context.Context) error {
			// An empty assignee clears the proposal.
			var proposed any
			if assignee != "" {
				proposed = assignee
			}
			_, err := d.store.Work(ctx, maintainerWork(c.repository, maintainerConsumer), "work-propose", workFieldsOf(map[string]any{
				"work_id": c.workID, "if_revision": c.revision, "proposed_assignee": proposed, "key": c.key, "deadline": c.deadline}))
			return err
		}, "work_proposed", nil)
	}))

	mux.HandleFunc("POST /dashboard/actions/work-edit", authed(workFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		c := read(r)
		title, criteria, nonGoals := r.PostForm.Get("title"), r.PostForm.Get("criteria"), r.PostForm.Get("non_goals")
		valid := c.valid && utf8.ValidString(title+criteria+nonGoals)
		run(w, r, "work-edit", target(c, ""), "work", page(c), valid, func(ctx context.Context) error {
			// The dashboard does not edit on behalf of a live claim's owner; the claim is
			// released first, and the page says so.
			claim, err := liveClaim(ctx, c)
			if err != nil {
				return err
			}
			if claim != nil {
				return workError("work_claimed", "the item has a live claim; release it before editing")
			}
			_, err = d.store.Work(ctx, maintainerWork(c.repository, maintainerConsumer), "work-edit", workFieldsOf(map[string]any{
				"work_id": c.workID, "if_revision": c.revision, "title": title, "criteria": criteria, "non_goals": nonGoals,
				"key": c.key, "deadline": c.deadline}))
			return err
		}, "work_edited", nil)
	}))

	mux.HandleFunc("POST /dashboard/actions/work-finish", authed(workFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		c := read(r)
		outcome, reason := r.PostForm.Get("outcome"), r.PostForm.Get("reason")
		valid := c.valid && utf8.ValidString(reason+r.PostForm.Get("references"))
		run(w, r, "work-finish", target(c, " "+outcome), "work", page(c), valid, func(ctx context.Context) error {
			values := map[string]any{"work_id": c.workID, "if_revision": c.revision, "outcome": outcome,
				"key": c.key, "deadline": c.deadline, "references": lines(r.PostForm.Get("references"))}
			if reason != "" {
				values["reason"] = reason
			}
			// A resubmitted form repeats its first request, under that request's consumer, so
			// it replays the first result whatever the item's state is now.
			first, generation, err := d.store.finishReplay(ctx, c.repository, c.workID, c.key)
			if err != nil {
				return err
			}
			if first == maintainerConsumer {
				_, err = d.store.claimAndFinish(ctx, maintainerWork(c.repository, maintainerConsumer), workFieldsOf(values))
				return err
			}
			if first != "" {
				values["claim_generation"] = generation
				_, err = d.store.Work(ctx, maintainerWork(c.repository, first), "work-finish", workFieldsOf(values))
				return err
			}
			claim, err := liveClaim(ctx, c)
			if err != nil {
				return err
			}
			if claim == nil {
				// An unclaimed item is claimed by the maintainer and finished in one
				// transaction, so a refused finish changes nothing.
				_, err = d.store.claimAndFinish(ctx, maintainerWork(c.repository, maintainerConsumer), workFieldsOf(values))
				return err
			}
			// A live claim is finished on behalf of its owner, as the release action
			// releases one; the work event names the maintainer.
			values["claim_generation"] = claim.Generation
			_, err = d.store.Work(ctx, maintainerWork(c.repository, claim.Consumer), "work-finish", workFieldsOf(values))
			return err
		}, "work_finished", nil)
	}))
}

// finishReplay finds the consumer under which a finish with this key was recorded, and
// the claim generation it finished: the item's last generation, which a finish keeps.
func (s *Store) finishReplay(ctx context.Context, repository, workID, key string) (string, int64, error) {
	store, err := storeOf(ctx, s.db, repository)
	if err != nil || store == "" {
		return "", 0, err
	}
	var consumer string
	var generation sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT r.consumer,w.last_generation FROM work_replays r
		LEFT JOIN work_items w ON w.store=r.store AND w.work_id=? WHERE r.store=? AND r.key=? AND r.operation='work-finish'
		ORDER BY r.consumer=? DESC LIMIT 1`, workID, store, key, maintainerConsumer).Scan(&consumer, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return consumer, generation.Int64, err
}

// claimAndFinish claims an unclaimed item as m's consumer and finishes it in one
// transaction, so a refused finish leaves the item as it was. The finish request carries the
// form's key and fields, without a claim generation; its replay record binds them, so a
// resubmitted form returns the first result.
func (s *Store) claimAndFinish(ctx context.Context, m MemoryCaller, fields map[string]json.RawMessage) (any, error) {
	if _, ok := fields["claim_generation"]; ok {
		return nil, invalid("a claim and finish takes no claim generation")
	}
	// The request is validated with a placeholder generation, which is then removed: the
	// fingerprint binds the form's fields, and the start supplies the generation.
	fields["claim_generation"] = json.RawMessage("1")
	r, err := parseWork("work-finish", fields)
	delete(fields, "claim_generation")
	if err != nil {
		return nil, err
	}
	delete(r.present, "claim_generation")
	delete(r.ints, "claim_generation")
	if !r.keyed() {
		return nil, invalid("a claim and finish takes a key")
	}
	s.memory.op.Lock()
	advanced := false
	defer func() {
		s.memory.op.Unlock()
		if advanced {
			s.changed(m.Repository)
		}
	}()
	now := s.clock()
	print, original, err := s.replay(ctx, m, r, now)
	if err != nil || original != nil {
		return original, err
	}
	workID := r.text["work_id"]
	store, err := storeOf(ctx, s.db, m.Repository)
	if err != nil {
		return nil, err
	}
	if _, err := current(ctx, s.db, store, workID, now); err != nil {
		return nil, err
	}
	if err := s.maybeExpire(ctx, m.Repository); err != nil {
		return nil, err
	}
	if err := s.reconcile(ctx, m.Repository, workID, now, &advanced); err != nil {
		return nil, err
	}
	if err := s.reclaimInactive(ctx, m.Repository, workID); err != nil {
		return nil, err
	}
	start, err := parseWork("work-start", workFieldsOf(map[string]any{"work_id": workID, "if_revision": r.ints["if_revision"],
		"checkpoint": "Started by the maintainer to finish it from the dashboard", "next_artifact": "the finish",
		"progress_deadline": now + 3600, "key": r.text["key"] + "-start", "deadline": r.numbers["deadline"]}))
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = s.workWrite(ctx, m.Repository, ordinary, func(tx *writeTx, store string) error {
		tx.audited = true
		started, err := s.apply(ctx, tx, store, m, start, now, "", &advanced)
		if err != nil {
			return err
		}
		claim, ok := started["claim"].(lease)
		if !ok {
			return workError("storage_error", "the start returned no claim")
		}
		r.ints["if_revision"], r.ints["claim_generation"], r.present["claim_generation"] = started["revision"].(int64), claim.Generation, true
		result, err = s.apply(ctx, tx, store, m, r, now, print, &advanced)
		return err
	})
	return result, err
}
