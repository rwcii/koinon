package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
			c.key != "" && len(c.key) <= 256
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
		// The outcome's own requirement is checked first, so an unclaimed item is never
		// claimed for a finish that would be refused.
		valid := c.valid && utf8.ValidString(reason+r.PostForm.Get("references")) &&
			(outcome == "completed" && len(lines(r.PostForm.Get("references"))) > 0 ||
				outcome == "withdrawn" && strings.TrimSpace(reason) != "")
		run(w, r, "work-finish", target(c, " "+outcome), "work", page(c), valid, func(ctx context.Context) error {
			claim, err := liveClaim(ctx, c)
			if err != nil {
				return err
			}
			consumer, revision := maintainerConsumer, c.revision
			var generation int64
			if claim != nil {
				// A live claim is finished on behalf of its owner, as the release action
				// releases one; the work event names the maintainer.
				consumer, generation = claim.Consumer, claim.Generation
			} else {
				// An unclaimed item is claimed by the maintainer and finished at once. The
				// start carries no audit record; the finish does.
				result, err := d.store.Work(noAudit(ctx), maintainerWork(c.repository, maintainerConsumer), "work-start", workFieldsOf(map[string]any{
					"work_id": c.workID, "if_revision": c.revision, "checkpoint": "Started by the maintainer to finish it from the dashboard",
					"next_artifact": "the finish", "progress_deadline": d.store.clock() + 3600, "key": c.key + "-start", "deadline": c.deadline}))
				if err != nil {
					return err
				}
				// A replayed start returns its stored JSON, so the result is read as JSON.
				var started struct {
					Revision int64 `json:"revision"`
					Claim    struct {
						Generation int64 `json:"generation"`
					} `json:"claim"`
				}
				data, _ := json.Marshal(result)
				if err := json.Unmarshal(data, &started); err != nil {
					return err
				}
				revision, generation = started.Revision, started.Claim.Generation
			}
			values := map[string]any{"work_id": c.workID, "if_revision": revision, "claim_generation": generation, "outcome": outcome,
				"key": c.key, "deadline": c.deadline, "references": lines(r.PostForm.Get("references"))}
			if reason != "" {
				values["reason"] = reason
			}
			_, err = d.store.Work(ctx, maintainerWork(c.repository, consumer), "work-finish", workFieldsOf(values))
			return err
		}, "work_finished", nil)
	}))
}
