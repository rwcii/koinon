package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Work items follow docs/WORK-ITEMS-V1.md with the limits of the implementation design
// and the commands of docs/WORK-ITEMS-COMMANDS.md. Work records are reported data: a
// proposal, claim or completion grants no authority and fences no file.

const (
	workEventType = "work-event"
	workRetention = 30 * 86400
	maxWorkRecord = 16 * 1024
	// A mutation's view leaves this much of maxWorkRecord free, so a later observation
	// (overdue and expiry reasons, longer counters and times) and the funded due event
	// that records it always fit.
	viewHeadroom   = 1024
	maxWorkScope   = 8 * 1024
	maxWorkResult  = 2 * 1024
	maxWorkRowCost = 16857 // the stored item row charge the physical proof assumes
	maxWorkList    = 48 * 1024
	workSweepLimit = 32
)

var workTextLimits = map[string]int{"title": 256, "criteria": 4096, "non_goals": 2048, "progress": 2048,
	"checkpoint": 1024, "next_artifact": 1024, "blocker": 1024, "reason": 1024}

// workFields are the request fields of each operation; mutations also take author, key
// and deadline.
var workFields = map[string][]string{
	"work-create":  {"title", "criteria", "non_goals", "proposed_assignee", "references"},
	"work-get":     {"work_id", "revision"},
	"work-list":    {"lifecycle", "owner", "proposed_assignee", "stale", "blocked", "limit"},
	"work-propose": {"work_id", "if_revision", "proposed_assignee"},
	"work-edit":    {"work_id", "if_revision", "claim_generation", "title", "criteria", "non_goals"},
	"work-start":   {"work_id", "if_revision", "checkpoint", "next_artifact", "progress_deadline", "lease_seconds", "resources"},
	"work-update": {"work_id", "if_revision", "claim_generation", "progress", "checkpoint", "next_artifact",
		"progress_deadline", "lifecycle", "blocker", "references", "renew_for"},
	"work-release": {"work_id", "if_revision", "claim_generation", "checkpoint"},
	"work-finish":  {"work_id", "if_revision", "claim_generation", "outcome", "reason", "references"},
	"claim-renew":  {"work_id", "claim_generation", "if_claim_revision", "lease_seconds"},
}

// WorkRequired are the unconditional fields of each operation, which the command line
// also checks before it contacts the daemon.
var WorkRequired = map[string][]string{
	"work-create":  {"title", "criteria", "non_goals", "key", "deadline"},
	"work-get":     {"work_id"},
	"work-propose": {"work_id", "if_revision", "proposed_assignee"},
	"work-edit":    {"work_id", "if_revision"},
	"work-start":   {"work_id", "if_revision", "checkpoint", "next_artifact", "progress_deadline", "key", "deadline"},
	"work-update":  {"work_id", "if_revision", "claim_generation", "progress", "checkpoint", "next_artifact", "progress_deadline"},
	"work-release": {"work_id", "if_revision", "claim_generation", "checkpoint"},
	"work-finish":  {"work_id", "if_revision", "claim_generation", "outcome"},
	"claim-renew":  {"work_id", "claim_generation", "if_claim_revision"},
}

// WorkOperations lists the wire operations.
func WorkOperations() []string {
	return []string{"work-create", "work-get", "work-list", "work-propose", "work-edit", "work-start", "work-update",
		"work-release", "work-finish", "claim-renew"}
}

func workRead(op string) bool { return op == "work-get" || op == "work-list" }

// workRequest is one validated request. present records which fields were sent, so an
// absent field and an explicit null or zero stay distinct.
type workRequest struct {
	op      string
	present map[string]bool
	text    map[string]string
	nulls   map[string]bool
	ints    map[string]int64
	numbers map[string]float64
	bools   map[string]bool
	refs    []string
	res     []claimResource
}

func (r *workRequest) has(name string) bool { return r.present[name] }

// optional returns a nullable text field: nil when absent or null.
func (r *workRequest) optional(name string) *string {
	if v, ok := r.text[name]; ok {
		return &v
	}
	return nil
}

func invalid(message string) error { return workError("invalid_request", message) }

// jsonKind is the JSON type of a raw value: string, number, bool, null, array or object.
func jsonKind(raw json.RawMessage) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case '"':
		return "string"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	case '[':
		return "array"
	case '{':
		return "object"
	}
	return "number"
}

// parseWork decodes and validates a request with strict JSON types: booleans are not
// integers, times are finite numbers, and every counter fits a signed 64-bit integer.
func parseWork(op string, fields map[string]json.RawMessage) (*workRequest, error) {
	allowed := map[string]bool{}
	names, known := workFields[op]
	if !known {
		return nil, invalid("unknown work operation")
	}
	for _, name := range names {
		allowed[name] = true
	}
	if !workRead(op) {
		allowed["author"], allowed["key"], allowed["deadline"] = true, true, true
	}
	r := &workRequest{op: op, present: map[string]bool{}, text: map[string]string{}, nulls: map[string]bool{},
		ints: map[string]int64{}, numbers: map[string]float64{}, bools: map[string]bool{}}
	for name, raw := range fields {
		if !allowed[name] {
			return nil, invalid("unknown request field " + name)
		}
		r.present[name] = true
		kind := jsonKind(raw)
		switch name {
		case "proposed_assignee", "owner", "author":
			if kind == "null" {
				r.nulls[name] = true
				continue
			}
			fallthrough
		case "work_id", "title", "criteria", "non_goals", "progress", "checkpoint", "next_artifact", "blocker", "reason",
			"outcome", "lifecycle", "key":
			var v string
			if kind != "string" || json.Unmarshal(raw, &v) != nil {
				return nil, invalid(name + " must be text")
			}
			r.text[name] = v
		case "revision", "if_revision", "claim_generation", "if_claim_revision", "limit", "lease_seconds", "renew_for":
			var v int64
			if kind != "number" || json.Unmarshal(raw, &v) != nil {
				return nil, invalid(name + " must be an integer")
			}
			r.ints[name] = v
		case "progress_deadline", "deadline":
			var v float64
			if kind != "number" || json.Unmarshal(raw, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > math.MaxInt64 {
				return nil, invalid(name + " must be a finite nonnegative time")
			}
			r.numbers[name] = v
		case "stale", "blocked":
			var v bool
			if kind != "bool" || json.Unmarshal(raw, &v) != nil {
				return nil, invalid(name + " must be a boolean")
			}
			r.bools[name] = v
		case "references":
			var v []string
			if kind != "array" || json.Unmarshal(raw, &v) != nil || len(v) > 8 {
				return nil, invalid("at most eight inert references are allowed")
			}
			for _, ref := range v {
				if err := checkText(ref, "reference", 512); err != nil {
					return nil, err
				}
			}
			r.refs = v
		case "resources":
			var v [][]string
			if kind != "array" || json.Unmarshal(raw, &v) != nil {
				return nil, invalid("resources are [kind, key] pairs")
			}
			for _, pair := range v {
				if len(pair) != 2 {
					return nil, invalid("resources are [kind, key] pairs")
				}
				r.res = append(r.res, claimResource{pair[0], pair[1]})
			}
		}
	}
	return r, r.validate()
}

func (r *workRequest) validate() error {
	op := r.op
	required := append([]string{}, WorkRequired[op]...)
	if !workRead(op) && (r.has("key") || r.has("deadline")) {
		required = append(required, "key", "deadline")
	}
	var missing []string
	seen := map[string]bool{}
	for _, name := range required {
		if !r.has(name) && !seen[name] {
			missing, seen[name] = append(missing, name), true
		}
	}
	if len(missing) > 0 {
		return invalid("missing required fields: " + strings.Join(missing, ", "))
	}
	for _, name := range []string{"author", "proposed_assignee", "owner"} {
		if v, ok := r.text[name]; ok {
			if err := checkConsumer(v, name); err != nil {
				return err
			}
		}
	}
	if r.has("work_id") {
		if err := checkWorkID(r.text["work_id"]); err != nil {
			return err
		}
	}
	for _, name := range []string{"revision", "if_revision", "claim_generation", "if_claim_revision"} {
		if v, ok := r.ints[name]; ok && v < 1 {
			return invalid(name + " must be a positive integer")
		}
	}
	for name, limit := range workTextLimits {
		if v, ok := r.text[name]; ok && v != "" {
			if err := checkText(v, name, limit); err != nil {
				return err
			}
		}
	}
	nonempty := map[string][]string{"work-create": {"title", "criteria", "non_goals"}, "work-start": {"checkpoint", "next_artifact"},
		"work-update": {"progress", "checkpoint", "next_artifact"}, "work-release": {"checkpoint"}}[op]
	if op == "work-edit" {
		nonempty = nil
		for _, name := range []string{"title", "criteria", "non_goals"} {
			if r.has(name) {
				nonempty = append(nonempty, name)
			}
		}
		if len(nonempty) == 0 {
			return invalid("an edit changes at least one of title, criteria and non_goals")
		}
	}
	for _, name := range nonempty {
		if strings.TrimSpace(r.text[name]) == "" {
			return invalid(name + " must not be empty")
		}
	}
	for _, name := range []string{"lease_seconds", "renew_for"} {
		if v, ok := r.ints[name]; ok && (v < minLease || v > maxLease) {
			return invalid(fmt.Sprintf("%s is %d to %d seconds", name, minLease, maxLease))
		}
	}
	if r.has("resources") {
		if _, err := claimBundle(r.text["work_id"], r.res); err != nil {
			return err
		}
	}
	if op == "work-finish" {
		switch r.text["outcome"] {
		case "completed":
			if len(r.refs) == 0 {
				return invalid("completion needs evidence references")
			}
		case "withdrawn":
			if strings.TrimSpace(r.text["reason"]) == "" {
				return invalid("withdrawal needs a reason")
			}
		default:
			return invalid("the outcome is completed or withdrawn")
		}
	}
	if v, ok := r.text["lifecycle"]; ok {
		valid := v == "active" || v == "blocked"
		if op == "work-list" {
			valid = valid || v == "open" || v == "finished"
		}
		if !valid {
			return invalid("invalid lifecycle filter or transition")
		}
	}
	if op == "work-update" && r.text["lifecycle"] == "blocked" && strings.TrimSpace(r.text["blocker"]) == "" {
		return invalid("blocked progress needs a blocker")
	}
	if v, ok := r.ints["limit"]; ok && (v < 1 || v > 100) {
		return invalid("limit is 1 to 100")
	}
	if r.keyed() {
		if err := checkText(r.text["key"], "key", 128); err != nil {
			return err
		}
	}
	return nil
}

func (r *workRequest) keyed() bool { return !workRead(r.op) && r.has("key") }

// fingerprint binds a replay key to the operation, the consumer and the canonical
// request content, so a retry with any other content conflicts.
func (r *workRequest) fingerprint(consumer string) string {
	// The decoded values are hashed, not their spelling: "\u0074" and "t", or 1 and 1.0,
	// are one request. An explicit null stays distinct from an absent field.
	content := map[string]any{"op": r.op, "consumer": consumer}
	for name := range r.present {
		switch {
		case r.nulls[name]:
			content[name] = nil
		case name == "references":
			content[name] = r.refs
		case name == "resources":
			pairs := [][]string{}
			for _, res := range r.res {
				pairs = append(pairs, []string{res.Kind, res.Key})
			}
			content[name] = pairs
		default:
			if v, ok := r.text[name]; ok {
				content[name] = v
			} else if v, ok := r.ints[name]; ok {
				content[name] = v
			} else if v, ok := r.numbers[name]; ok {
				content[name] = v
			} else if v, ok := r.bools[name]; ok {
				content[name] = v
			}
		}
	}
	data, _ := json.Marshal(content)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// workItem is one stored work record.
type workItem struct {
	WorkID             string   `json:"work_id"`
	Revision           int64    `json:"revision"`
	Lifecycle          string   `json:"lifecycle"`
	Title              string   `json:"title"`
	Criteria           string   `json:"criteria"`
	NonGoals           string   `json:"non_goals"`
	ProposedAssignee   *string  `json:"proposed_assignee"`
	CreatedAt          float64  `json:"created_at"`
	CreatedConsumer    string   `json:"created_consumer"`
	FirstStartRevision *int64   `json:"first_start_revision"`
	ScopeRevision      int64    `json:"scope_revision"`
	ProgressEpoch      int64    `json:"progress_epoch"`
	LastProgressAt     *float64 `json:"last_progress_at"`
	ProgressDeadline   *float64 `json:"progress_deadline"`
	Progress           string   `json:"progress"`
	Checkpoint         string   `json:"checkpoint"`
	NextArtifact       string   `json:"next_artifact"`
	Blocker            string   `json:"blocker"`
	LastWriter         *string  `json:"last_writer"`
	LastGeneration     *int64   `json:"last_generation"`
	LastLeaseExpires   *float64 `json:"last_lease_expires"`
	LeaseExpired       bool     `json:"lease_expired"`
	Outcome            *string  `json:"outcome"`
	Reason             string   `json:"reason"`
	References         []string `json:"references"`
	FinishedAt         *float64 `json:"finished_at"`
	ExpiresAt          *float64 `json:"expires_at"`
	LatestSeq          int64    `json:"latest_seq"`
}

const workColumns = `work_id,revision,lifecycle,title,criteria,non_goals,proposed_assignee,created_at,created_consumer,
	first_start_revision,scope_revision,progress_epoch,last_progress_at,progress_deadline,progress,checkpoint,next_artifact,
	blocker,last_writer,last_generation,last_lease_expires,lease_expired,outcome,reason,references_json,finished_at,expires_at,latest_seq`

func scanWork(row scanner) (workItem, error) {
	var w workItem
	var refs string
	err := row.Scan(&w.WorkID, &w.Revision, &w.Lifecycle, &w.Title, &w.Criteria, &w.NonGoals, &w.ProposedAssignee, &w.CreatedAt,
		&w.CreatedConsumer, &w.FirstStartRevision, &w.ScopeRevision, &w.ProgressEpoch, &w.LastProgressAt, &w.ProgressDeadline,
		&w.Progress, &w.Checkpoint, &w.NextArtifact, &w.Blocker, &w.LastWriter, &w.LastGeneration, &w.LastLeaseExpires,
		&w.LeaseExpired, &w.Outcome, &w.Reason, &refs, &w.FinishedAt, &w.ExpiresAt, &w.LatestSeq)
	if err == nil {
		err = json.Unmarshal([]byte(refs), &w.References)
	}
	if w.References == nil {
		w.References = []string{}
	}
	return w, err
}

func (w workItem) referencesJSON() string {
	refs := w.References
	if refs == nil {
		refs = []string{}
	}
	data, _ := json.Marshal(refs)
	return string(data)
}

// rowCost is the logical charge of the stored row: its text bytes plus the row overhead.
func (w workItem) rowCost(store string) int {
	cost := workRowOverhead + len(store) + len(w.WorkID) + len(w.Lifecycle) + len(w.Title) + len(w.Criteria) + len(w.NonGoals) +
		len(w.CreatedConsumer) + len(w.Progress) + len(w.Checkpoint) + len(w.NextArtifact) + len(w.Blocker) + len(w.Reason) +
		len(w.referencesJSON())
	for _, v := range []*string{w.ProposedAssignee, w.LastWriter, w.Outcome} {
		if v != nil {
			cost += len(*v)
		}
	}
	return cost
}

// workCaller is a resolved work request: its store, provenance and consumer key.
type workCaller struct {
	MemoryCaller
	store string // the store's store_id; empty before the store's first write
}

// ResolveWorkCaller resolves a work caller. Without an explicit consumer the consumer is
// the session key FAMILY:ID, not the peer name, which a successor can inherit: a
// replacement session must respect its predecessor's lease.
func (s *Store) ResolveWorkCaller(ctx context.Context, caller Key, consumer *string) (MemoryCaller, error) {
	m, err := s.ResolveMemoryCaller(ctx, caller, consumer)
	if err != nil {
		return m, err
	}
	if consumer == nil {
		m.Consumer = caller.Family + ":" + caller.ID
	}
	if err := checkConsumer(m.Consumer, "consumer"); err != nil {
		return m, workError("invalid_request", "the session key is too long for a consumer key; pass a consumer of 1 to 128 characters")
	}
	return m, nil
}

func storeOf(ctx context.Context, q querier, repo string) (string, error) {
	var store string
	err := q.QueryRowContext(ctx, `SELECT store_id FROM memory_stores WHERE repository=?`, repo).Scan(&store)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return store, err
}

// current returns a work item that is still retained: a finished item past its expiry is
// invisible even before maintenance removes it.
func current(ctx context.Context, q querier, store, workID string, now float64) (workItem, error) {
	w, err := scanWork(q.QueryRowContext(ctx, `SELECT `+workColumns+` FROM work_items WHERE store=? AND work_id=?`, store, workID))
	if errors.Is(err, sql.ErrNoRows) || err == nil && w.ExpiresAt != nil && *w.ExpiresAt <= now {
		return w, workError("work_not_found", "the work item is absent or its retention has expired")
	}
	return w, err
}

// freshness is the one observation rule for views, summaries and filters.
func freshness(lifecycle string, deadline *float64, active bool, leaseExpires, now float64) (live, overdue, unverified bool) {
	live = active && leaseExpires > now
	ongoing := lifecycle == "active" || lifecycle == "blocked"
	overdue = ongoing && deadline != nil && *deadline <= now
	return live, overdue, ongoing && (!live || overdue)
}

type workClaimView struct {
	Consumer   string     `json:"consumer"`
	Generation int64      `json:"generation"`
	Revision   int64      `json:"revision"`
	ExpiresAt  float64    `json:"expires_at"`
	Resources  [][]string `json:"resources"`
}

// WorkView is a work item observed at ObservedAt.
type WorkView struct {
	workItem
	Type                      string         `json:"type"`
	Seq                       int64          `json:"seq"`
	ObservedAt                float64        `json:"observed_at"`
	ObservedLeaseExpires      *float64       `json:"observed_lease_expires"`
	CurrentClaim              *workClaimView `json:"current_claim"`
	LeaseValid                bool           `json:"lease_valid"`
	ProgressOverdue           bool           `json:"progress_overdue"`
	ProgressUnverified        bool           `json:"progress_unverified"`
	ProgressUnverifiedReasons []string       `json:"progress_unverified_reasons"`
	ScopeRevisions            []int64        `json:"scope_revisions"`
	CriteriaChangedAfterStart bool           `json:"criteria_changed_after_start"`
}

// view overlays the live claim observation and the scope history on a stored item.
func view(ctx context.Context, q querier, store string, w workItem, now float64) (WorkView, error) {
	v := WorkView{workItem: w, Type: "work-item", Seq: w.LatestSeq, ObservedAt: now, ProgressUnverifiedReasons: []string{}}
	held, err := bundleFor(ctx, q, store, w.WorkID)
	if err != nil {
		return v, err
	}
	active, expires := false, 0.0
	if held != nil {
		active, expires = held.Active, held.ExpiresAt
		// The deadline used for this observation, even when the retained lease has lapsed;
		// renewal does not rewrite the historical last_lease_expires.
		v.ObservedLeaseExpires = &held.ExpiresAt
	}
	live, overdue, unverified := freshness(w.Lifecycle, w.ProgressDeadline, active, expires, now)
	if live {
		resources, err := bundleResources(ctx, q, store, held.Generation)
		if err != nil {
			return v, err
		}
		claim := &workClaimView{Consumer: held.Consumer, Generation: held.Generation, Revision: held.Revision, ExpiresAt: held.ExpiresAt,
			Resources: [][]string{}}
		for _, r := range resources {
			claim.Resources = append(claim.Resources, []string{r.Kind, r.Key})
		}
		v.CurrentClaim = claim
	}
	v.LeaseValid, v.ProgressOverdue, v.ProgressUnverified = live, overdue, unverified
	if (w.Lifecycle == "active" || w.Lifecycle == "blocked") && !live {
		v.ProgressUnverifiedReasons = append(v.ProgressUnverifiedReasons, "lease_expired")
	}
	if overdue {
		v.ProgressUnverifiedReasons = append(v.ProgressUnverifiedReasons, "progress_deadline_missed")
	}
	rows, err := q.QueryContext(ctx, `SELECT revision,criteria,non_goals FROM work_scope_revisions WHERE store=? AND work_id=? ORDER BY revision`,
		store, w.WorkID)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	var previous [2]string
	for i := 0; rows.Next(); i++ {
		var revision int64
		var scope [2]string
		if err := rows.Scan(&revision, &scope[0], &scope[1]); err != nil {
			return v, err
		}
		v.ScopeRevisions = append(v.ScopeRevisions, revision)
		// Criteria or non-goals that changed after the first start stay flagged, even
		// after a later edit restores them; a title-only edit is not a scope change.
		if i > 0 && w.FirstStartRevision != nil && revision > *w.FirstStartRevision && scope != previous {
			v.CriteriaChangedAfterStart = true
		}
		previous = scope
	}
	return v, rows.Err()
}

// workViews returns the current view of every retained item of a repository's store,
// ordered by work ID.
func (s *Store) workViews(ctx context.Context, repo string, now float64) ([]WorkView, error) {
	store, err := storeOf(ctx, s.db, repo)
	if err != nil || store == "" {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+workColumns+` FROM work_items WHERE store=? AND (expires_at IS NULL OR expires_at>?)
		ORDER BY work_id`, store, now)
	if err != nil {
		return nil, err
	}
	var items []workItem
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, w)
	}
	rows.Close()
	views := make([]WorkView, 0, len(items))
	for _, w := range items {
		v, err := view(ctx, s.db, store, w, now)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

// workPayloads adds the immutable payload of each work event in entries; it never
// substitutes the current record.
func (s *Store) workPayloads(ctx context.Context, repo string, entries []MemoryEntry) error {
	var store string
	for i := range entries {
		if entries[i].Type != workEventType {
			continue
		}
		if store == "" {
			var err error
			if store, err = storeOf(ctx, s.db, repo); err != nil {
				return err
			}
		}
		var kind, payload string
		err := s.db.QueryRowContext(ctx, `SELECT kind,payload FROM work_events WHERE store=? AND seq=?`, store, entries[i].Seq).Scan(&kind, &payload)
		if errors.Is(err, sql.ErrNoRows) {
			return workError("incompatible_store", "a work stream payload is missing")
		}
		if err != nil {
			return err
		}
		entries[i].EventKind, entries[i].Payload = kind, json.RawMessage(payload)
		if entries[i].ScopeTarget != nil {
			entries[i].WorkID = *entries[i].ScopeTarget
		}
	}
	return nil
}

// workWrite runs one work transaction inside the storage boundary and enforces the
// store's ceilings after the mutation, with the debt that the mutation left.
func (s *Store) workWrite(ctx context.Context, repo string, class writeClass, body func(*writeTx, string) error) error {
	tx, err := s.begin(ctx, class)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_stores(repository,store_id) VALUES (?,?)`, repo, randomID()); err != nil {
		return tx.fail(err)
	}
	store, err := storeOf(ctx, tx, repo)
	if err != nil {
		return tx.fail(err)
	}
	if err := body(tx, store); err != nil {
		return tx.fail(err)
	}
	if err := s.enforce(ctx, tx, repo); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// event writes both halves of one work event, the stream entry and its immutable
// payload, advances the head and stores the item with its new latest sequence.
func (s *Store) event(ctx context.Context, tx *writeTx, store string, m MemoryCaller, w *workItem, kind, consumer string, author *string,
	now float64, advanced *bool) error {
	if w.rowCost(store) > maxWorkRowCost {
		return workError("record_too_large", "the stored work record exceeds the physical proof bound")
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `UPDATE memory_stores SET head=head+1 WHERE store_id=? RETURNING head`, store).Scan(&seq); err != nil {
		return err
	}
	w.LatestSeq = seq
	if _, err := tx.ExecContext(ctx, `UPDATE work_items SET revision=?,lifecycle=?,title=?,criteria=?,non_goals=?,proposed_assignee=?,
		first_start_revision=?,scope_revision=?,progress_epoch=?,last_progress_at=?,progress_deadline=?,progress=?,checkpoint=?,
		next_artifact=?,blocker=?,last_writer=?,last_generation=?,last_lease_expires=?,lease_expired=?,outcome=?,reason=?,
		references_json=?,finished_at=?,expires_at=?,latest_seq=? WHERE store=? AND work_id=?`,
		w.Revision, w.Lifecycle, w.Title, w.Criteria, w.NonGoals, w.ProposedAssignee, w.FirstStartRevision, w.ScopeRevision,
		w.ProgressEpoch, w.LastProgressAt, w.ProgressDeadline, w.Progress, w.Checkpoint, w.NextArtifact, w.Blocker, w.LastWriter,
		w.LastGeneration, w.LastLeaseExpires, w.LeaseExpired, w.Outcome, w.Reason, w.referencesJSON(), w.FinishedAt, w.ExpiresAt,
		w.LatestSeq, store, w.WorkID); err != nil {
		return err
	}
	payload, err := view(ctx, tx, store, *w, now)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(payload)
	limit := maxWorkRecord - viewHeadroom
	if kind == "progress-overdue" || kind == "lease-expired" {
		// A funded due transition is never refused for size: its item was admitted with
		// the headroom that its observation needs.
		limit = maxWorkRecord
	}
	if len(data) > limit {
		return workError("record_too_large", "the encoded work view exceeds its byte bound")
	}
	family, name := m.Family, m.Name
	if family == "" {
		// A due transition that maintenance records has no calling session.
		family = "daemon"
	}
	target := w.WorkID
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_entries(repository,`+entryColumns+`) VALUES
		(?,?,?,?,'repo',?,NULL,'',?,?,?,?,?,NULL,NULL,NULL,NULL,NULL,NULL)`, m.Repository, seq, now, workEventType, target, author,
		family, name, consumer, w.Revision); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_events(store,seq,work_id,revision,kind,payload) VALUES (?,?,?,?,?,?)`,
		store, seq, w.WorkID, w.Revision, kind, string(data)); err != nil {
		return err
	}
	tx.onCommit(func() { *advanced = true })
	return nil
}

func (s *Store) scope(ctx context.Context, tx *writeTx, store string, w workItem, consumer string, author *string, now float64) error {
	row := map[string]any{"work_id": w.WorkID, "revision": w.Revision, "ts": now, "consumer": consumer, "author": author,
		"title": w.Title, "criteria": w.Criteria, "non_goals": w.NonGoals}
	if data, _ := json.Marshal(row); len(data) > maxWorkScope {
		return workError("record_too_large", "the encoded scope revision exceeds its byte bound")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO work_scope_revisions(store,work_id,revision,ts,consumer,author,title,criteria,non_goals)
		VALUES (?,?,?,?,?,?,?,?,?)`, store, w.WorkID, w.Revision, now, consumer, author, w.Title, w.Criteria, w.NonGoals)
	return err
}

// Work runs one work operation for a resolved caller. Reads never write; a mutation
// writes its record, scope history, stream entry, event, head and replay result in one
// transaction, and the change hook runs after the commit, outside every lock.
func (s *Store) Work(ctx context.Context, m MemoryCaller, op string, fields map[string]json.RawMessage) (any, error) {
	r, err := parseWork(op, fields)
	if err != nil {
		return nil, err
	}
	if workRead(op) {
		return s.workRead(ctx, m, r, s.clock())
	}
	s.memory.op.Lock()
	advanced := false
	defer func() {
		s.memory.op.Unlock()
		if advanced {
			s.changed(m.Repository)
		}
	}()
	// The time is read once the operation is serialized: a request that waited for the
	// lock is judged at its processing time, so a lease that lapsed meanwhile stays lapsed.
	return s.workMutation(ctx, m, r, s.clock(), &advanced)
}

func (s *Store) workRead(ctx context.Context, m MemoryCaller, r *workRequest, now float64) (any, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	store, err := storeOf(ctx, tx, m.Repository)
	if err != nil {
		return nil, err
	}
	if r.op == "work-list" {
		return listing(ctx, tx, store, r, now)
	}
	w, err := current(ctx, tx, store, r.text["work_id"], now)
	if err != nil {
		return nil, err
	}
	if !r.has("revision") {
		return view(ctx, tx, store, w, now)
	}
	scope := map[string]any{"work_id": w.WorkID}
	var revision int64
	var ts float64
	var consumer, title, criteria, nonGoals string
	var author *string
	err = tx.QueryRowContext(ctx, `SELECT revision,ts,consumer,author,title,criteria,non_goals FROM work_scope_revisions
		WHERE store=? AND work_id=? AND revision=?`, store, w.WorkID, r.ints["revision"]).Scan(&revision, &ts, &consumer, &author, &title,
		&criteria, &nonGoals)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workError("work_not_found", "that scope revision is not retained")
	}
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]any{"revision": revision, "ts": ts, "consumer": consumer, "author": author, "title": title,
		"criteria": criteria, "non_goals": nonGoals} {
		scope[k] = v
	}
	return scope, nil
}

// listing returns capped summaries from one read transaction in work-ID order, with an
// explicit truncated flag: a truncated result is not a complete inventory.
func listing(ctx context.Context, q querier, store string, r *workRequest, now float64) (map[string]any, error) {
	result := map[string]any{"items": []map[string]any{}, "truncated": false, "observed_at": now}
	if store == "" {
		return result, nil
	}
	limit := int64(100)
	if v, ok := r.ints["limit"]; ok {
		limit = v
	}
	rows, err := q.QueryContext(ctx, `SELECT w.work_id,w.revision,w.title,w.lifecycle,w.proposed_assignee,w.progress_deadline,
		b.consumer,COALESCE(b.active,0),COALESCE(b.expires_at,0),w.checkpoint FROM work_items w LEFT JOIN claim_bundles b
		ON b.store=w.store AND b.work_id=w.work_id WHERE w.store=? AND (w.expires_at IS NULL OR w.expires_at>?) ORDER BY w.work_id`, store, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	base, _ := json.Marshal(result)
	used := len(base)
	for rows.Next() {
		var workID, title, lifecycle, checkpoint string
		var revision int64
		var proposed, consumer *string
		var deadline *float64
		var active bool
		var expires float64
		if err := rows.Scan(&workID, &revision, &title, &lifecycle, &proposed, &deadline, &consumer, &active, &expires, &checkpoint); err != nil {
			return nil, err
		}
		live, _, stale := freshness(lifecycle, deadline, active, expires, now)
		var owner *string
		if live {
			owner = consumer
		}
		if !matches(r, "owner", owner) || !matches(r, "proposed_assignee", proposed) ||
			r.has("lifecycle") && r.text["lifecycle"] != lifecycle ||
			r.has("stale") && r.bools["stale"] != stale || r.has("blocked") && r.bools["blocked"] != (lifecycle == "blocked") {
			continue
		}
		summary := map[string]any{"work_id": workID, "revision": revision, "title": title, "lifecycle": lifecycle,
			"proposed_assignee": proposed, "progress_unverified": stale, "progress_deadline": deadline, "lease_valid": live,
			"owner": owner, "observed_at": now, "checkpoint": checkpoint}
		data, _ := json.Marshal(summary)
		size := len(data) + 1
		if int64(len(items)) >= limit || used+size > maxWorkList {
			result["truncated"] = true
			break
		}
		items, used = append(items, summary), used+size
	}
	result["items"] = items
	return result, rows.Err()
}

// matches applies a nullable filter: a sent null matches an absent value.
func matches(r *workRequest, name string, value *string) bool {
	if !r.has(name) {
		return true
	}
	if r.nulls[name] {
		return value == nil
	}
	return value != nil && *value == r.text[name]
}

// replay answers a keyed retry with its retained original result. Replay precedes every
// precondition and maintenance, so a retry returns the original outcome.
func (s *Store) replay(ctx context.Context, m MemoryCaller, r *workRequest, now float64) (string, map[string]any, error) {
	if !r.keyed() {
		return "", nil, nil
	}
	l := s.limits()
	deadline := r.numbers["deadline"]
	if deadline <= now {
		return "", nil, workError("retry_deadline_expired", "the retry deadline has passed; query the item to reconcile")
	}
	if deadline > now+l.idemTTL {
		return "", nil, invalid(fmt.Sprintf("the retry deadline is beyond the %.0f s horizon", l.idemTTL))
	}
	print := r.fingerprint(m.Consumer)
	store, err := storeOf(ctx, s.db, m.Repository)
	if err != nil || store == "" {
		return print, nil, err
	}
	var scheme, stored, operation, result string
	err = s.db.QueryRowContext(ctx, `SELECT scheme,fingerprint,operation,result FROM work_replays WHERE store=? AND consumer=? AND key=?`,
		store, m.Consumer, r.text["key"]).Scan(&scheme, &stored, &operation, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return print, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	// A row is compared by the scheme it was written with; an unknown scheme conflicts.
	if scheme != fingerprintScheme || stored != print || operation != r.op {
		return "", nil, workError("idempotency_conflict", "this work replay key is bound to a different request")
	}
	dec := json.NewDecoder(strings.NewReader(result))
	dec.UseNumber()
	var original map[string]any
	if err := dec.Decode(&original); err != nil {
		return "", nil, err
	}
	original["duplicate"] = true
	return print, original, nil
}

func storeReplay(ctx context.Context, tx *writeTx, store string, m MemoryCaller, r *workRequest, print string, result map[string]any, now float64) error {
	data, _ := json.Marshal(result)
	if len(data) > maxWorkResult {
		return workError("record_too_large", "the encoded work result exceeds its byte bound")
	}
	if !r.keyed() {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO work_replays(store,consumer,key,operation,scheme,fingerprint,seq,ts,deadline,result)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, store, m.Consumer, r.text["key"], r.op, fingerprintScheme, print, result["seq"], now,
		r.numbers["deadline"], string(data))
	return err
}

func (s *Store) workMutation(ctx context.Context, m MemoryCaller, r *workRequest, now float64, advanced *bool) (any, error) {
	print, original, err := s.replay(ctx, m, r, now)
	if err != nil || original != nil {
		return original, err
	}
	if r.has("progress_deadline") {
		if d := r.numbers["progress_deadline"]; !(now < d && d <= now+86400) {
			return nil, invalid("the progress deadline is after now and within one day")
		}
	}
	workID := r.text["work_id"]
	if r.op != "work-create" {
		// An unknown target never triggers maintenance.
		store, err := storeOf(ctx, s.db, m.Repository)
		if err != nil {
			return nil, err
		}
		if _, err := current(ctx, s.db, store, workID, now); err != nil {
			return nil, err
		}
	}
	if err := s.maybeExpire(ctx, m.Repository); err != nil {
		return nil, err
	}
	if r.op != "work-create" {
		if err := s.reconcile(ctx, m.Repository, workID, now, advanced); err != nil {
			return nil, err
		}
	}
	if r.op == "work-start" {
		if err := s.reclaimInactive(ctx, m.Repository, workID); err != nil {
			return nil, err
		}
	}
	class := ordinary
	if r.op == "work-release" || r.op == "work-finish" {
		class = control
	}
	var result map[string]any
	err = s.workWrite(ctx, m.Repository, class, func(tx *writeTx, store string) error {
		// The operation's own change; the expiry and reconciliation above are not.
		tx.audited = true
		var err error
		result, err = s.apply(ctx, tx, store, m, r, now, print, advanced)
		return err
	})
	return result, err
}

// reclaimInactive deletes the target's inactive bundle under ordinary admission before a
// start; a start never overwrites it to avoid paying for the deletion.
func (s *Store) reclaimInactive(ctx context.Context, repo, workID string) error {
	store, err := storeOf(ctx, s.db, repo)
	if err != nil {
		return err
	}
	held, err := bundleFor(ctx, s.db, store, workID)
	if err != nil || held == nil || held.Active {
		return err
	}
	return s.workWrite(ctx, repo, ordinary, func(tx *writeTx, store string) error {
		held, err := bundleFor(ctx, tx, store, workID)
		if err != nil || held == nil || held.Active {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM claim_resources WHERE store=? AND generation=?`, store, held.Generation); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM claim_bundles WHERE store=? AND generation=? AND active=0`, store, held.Generation)
		return err
	})
}

// apply runs a validated mutation inside its transaction.
func (s *Store) apply(ctx context.Context, tx *writeTx, store string, m MemoryCaller, r *workRequest, now float64, print string,
	advanced *bool) (map[string]any, error) {
	engine := leaseEngine{ctx: ctx, tx: tx, store: store}
	author := r.optional("author")
	var w workItem
	var kind string
	if r.op == "work-create" {
		n, err := counter(ctx, tx, store, "work_counter")
		if err != nil {
			return nil, err
		}
		var head int64
		if err := tx.QueryRowContext(ctx, `SELECT head FROM memory_stores WHERE store_id=?`, store).Scan(&head); err != nil {
			return nil, err
		}
		w = workItem{WorkID: fmt.Sprintf("%032x", n), Revision: 1, Lifecycle: "open", Title: r.text["title"], Criteria: r.text["criteria"],
			NonGoals: r.text["non_goals"], ProposedAssignee: r.optional("proposed_assignee"), CreatedAt: now, CreatedConsumer: m.Consumer,
			ScopeRevision: 1, References: r.refs, LatestSeq: head + 1}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_items(store,work_id,revision,lifecycle,title,criteria,non_goals,proposed_assignee,
			created_at,created_consumer,scope_revision,references_json,latest_seq) VALUES (?,?,1,'open',?,?,?,?,?,?,1,?,?)`, store, w.WorkID,
			w.Title, w.Criteria, w.NonGoals, w.ProposedAssignee, now, m.Consumer, w.referencesJSON(), w.LatestSeq); err != nil {
			return nil, err
		}
		if err := s.scope(ctx, tx, store, w, m.Consumer, author, now); err != nil {
			return nil, err
		}
		kind = "created"
	} else {
		var err error
		if w, err = current(ctx, tx, store, r.text["work_id"], now); err != nil {
			return nil, err
		}
		if w.Lifecycle == "finished" {
			return nil, workError("invalid_transition", "finished work is terminal")
		}
		held, err := bundleFor(ctx, tx, store, w.WorkID)
		if err != nil {
			return nil, err
		}
		owned := r.op == "work-update" || r.op == "work-release" || r.op == "work-finish" || r.op == "claim-renew" ||
			r.op == "work-edit" && held != nil && held.Active && held.ExpiresAt > now
		if owned {
			if !r.has("claim_generation") {
				return nil, invalid("a live claim exists; its owner's claim_generation is required")
			}
			b, err := engine.owner(w.WorkID, m.Consumer, r.ints["claim_generation"], now)
			if err != nil {
				return nil, err
			}
			if b.ProgressEpoch != w.ProgressEpoch {
				return nil, workError("incompatible_store", "work and claim progress epochs disagree")
			}
		}
		if r.op == "claim-renew" {
			duration := int64(defaultLease)
			if v, ok := r.ints["lease_seconds"]; ok {
				duration = v
			}
			renewed, err := engine.renew(w.WorkID, m.Consumer, r.ints["claim_generation"], r.ints["if_claim_revision"], now, duration)
			if err != nil {
				return nil, err
			}
			result := map[string]any{"work_id": w.WorkID, "revision": w.Revision, "claim": renewed, "seq": nil, "duplicate": false}
			return result, storeReplay(ctx, tx, store, m, r, print, result, now)
		}
		if r.ints["if_revision"] != w.Revision {
			return nil, WorkRefusal{Code: "revision_conflict", Message: "the work revision changed; a due transition or cleanup may already have committed, so read the item again",
				Details: map[string]any{"current_revision": w.Revision}}
		}
		if w.Revision == math.MaxInt64 {
			return nil, workError("capacity", "the work revision is exhausted")
		}
		w.Revision++
		switch r.op {
		case "work-propose":
			kind, w.ProposedAssignee = "proposed", r.optional("proposed_assignee")
		case "work-edit":
			kind = "edited"
			for name, field := range map[string]*string{"title": &w.Title, "criteria": &w.Criteria, "non_goals": &w.NonGoals} {
				if r.has(name) {
					*field = r.text[name]
				}
			}
			w.ScopeRevision = w.Revision
			if err := s.scope(ctx, tx, store, w, m.Consumer, author, now); err != nil {
				return nil, err
			}
		case "work-start", "work-update":
			w.ProgressEpoch++
			if r.op == "work-start" {
				kind = "started"
				duration := int64(defaultLease)
				if v, ok := r.ints["lease_seconds"]; ok {
					duration = v
				}
				got, err := engine.acquire(w.WorkID, m.Consumer, w.ProgressEpoch, r.res, now, duration)
				if err != nil {
					return nil, err
				}
				writer := m.Consumer
				w.LastWriter, w.LastGeneration, w.LastLeaseExpires, w.LeaseExpired = &writer, &got.Generation, &got.ExpiresAt, false
				if w.FirstStartRevision == nil {
					first := w.Revision
					w.FirstStartRevision = &first
				}
			} else {
				kind = "updated"
				generation := r.ints["claim_generation"]
				if err := engine.progress(w.WorkID, m.Consumer, generation, w.ProgressEpoch, now); err != nil {
					return nil, err
				}
				if renewFor, ok := r.ints["renew_for"]; ok {
					b, err := engine.owner(w.WorkID, m.Consumer, generation, now)
					if err != nil {
						return nil, err
					}
					if _, err := engine.renew(w.WorkID, m.Consumer, generation, b.Revision, now, renewFor); err != nil {
						return nil, err
					}
				}
			}
			lifecycle := "active"
			if v, ok := r.text["lifecycle"]; ok {
				lifecycle = v
			}
			deadline := r.numbers["progress_deadline"]
			at := now
			w.Lifecycle, w.LastProgressAt, w.ProgressDeadline = lifecycle, &at, &deadline
			w.Checkpoint, w.NextArtifact, w.Progress, w.Blocker = r.text["checkpoint"], r.text["next_artifact"], r.text["progress"], ""
			if lifecycle == "blocked" {
				w.Blocker = r.text["blocker"]
			}
			if r.has("references") {
				w.References = r.refs
			}
		case "work-release", "work-finish":
			if err := engine.release(w.WorkID, m.Consumer, r.ints["claim_generation"], now); err != nil {
				return nil, err
			}
			w.ProgressDeadline = nil
			if r.op == "work-release" {
				kind, w.Lifecycle, w.Checkpoint, w.Blocker = "released", "open", r.text["checkpoint"], ""
			} else {
				outcome, at, expires := r.text["outcome"], now, now+workRetention
				kind, w.Lifecycle, w.Outcome, w.Reason = "finished", "finished", &outcome, r.text["reason"]
				w.References, w.FinishedAt, w.ExpiresAt = r.refs, &at, &expires
			}
		}
	}
	if err := s.event(ctx, tx, store, m, &w, kind, m.Consumer, author, now, advanced); err != nil {
		return nil, err
	}
	result := map[string]any{"work_id": w.WorkID, "revision": w.Revision, "seq": w.LatestSeq, "duplicate": false}
	if r.op == "work-start" || r.op == "work-update" {
		held, err := bundleFor(ctx, tx, store, w.WorkID)
		if err != nil {
			return nil, err
		}
		if held != nil {
			result["claim"] = lease{Generation: held.Generation, Revision: held.Revision, ExpiresAt: held.ExpiresAt}
		}
	}
	return result, storeReplay(ctx, tx, store, m, r, print, result, now)
}

// reconcile records at most one due transition for one target: lease expiry wins over
// overdue progress, and durable markers keep a transition from repeating.
func (s *Store) reconcile(ctx context.Context, repo, workID string, now float64, advanced *bool) error {
	store, err := storeOf(ctx, s.db, repo)
	if err != nil || store == "" {
		return err
	}
	w, err := current(ctx, s.db, store, workID, now)
	if err != nil {
		return err
	}
	held, err := bundleFor(ctx, s.db, store, workID)
	if err != nil || held == nil || !held.Active {
		return err
	}
	if held.ProgressEpoch != w.ProgressEpoch {
		return workError("incompatible_store", "work and claim progress epochs disagree")
	}
	expired := held.ExpiresAt <= now
	overdue := w.ProgressDeadline != nil && *w.ProgressDeadline <= now && !held.OverdueRecorded
	if !expired && !overdue {
		return nil
	}
	return s.workWrite(ctx, repo, control, func(tx *writeTx, store string) error {
		engine := leaseEngine{ctx: ctx, tx: tx, store: store}
		w.Revision++
		kind := "progress-overdue"
		if expired {
			kind = "lease-expired"
			if _, err := engine.expire(held.Generation, now); err != nil {
				return err
			}
			w.LeaseExpired = true
		} else if _, err := engine.overdue(held.Generation, w.ProgressEpoch); err != nil {
			return err
		}
		return s.event(ctx, tx, store, MemoryCaller{Repository: repo}, &w, kind, held.Consumer, nil, now, advanced)
	})
}

// reclaimFinished removes one entire finished item past its retention: its record, scope
// history, events and stream entries, raising the floor. The head and the counters stay;
// snapshots and replay results keep their own lifetimes.
func (s *Store) reclaimFinished(ctx context.Context, repo, store string, now float64) (bool, error) {
	var workID string
	err := s.db.QueryRowContext(ctx, `SELECT work_id FROM work_items WHERE store=? AND lifecycle='finished' AND expires_at<=?
		ORDER BY expires_at,work_id LIMIT 1`, store, now).Scan(&workID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	l := s.limits()
	removed := false
	err = s.workWrite(ctx, repo, control, func(tx *writeTx, store string) error {
		var latest int64
		err := tx.QueryRowContext(ctx, `SELECT latest_seq FROM work_items WHERE store=? AND work_id=? AND lifecycle='finished' AND expires_at<=?`,
			store, workID, now).Scan(&latest)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		held, err := bundleFor(ctx, tx, store, workID)
		if err != nil {
			return err
		}
		if held != nil {
			if held.Active {
				return workError("incompatible_store", "finished work still has an active claim")
			}
			// Its inactive bundle is reclaimed first, in its own transaction.
			return nil
		}
		var scopes, events, highest int64
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM work_scope_revisions WHERE store=?1 AND work_id=?2),
			(SELECT COUNT(*) FROM work_events WHERE store=?1 AND work_id=?2),
			(SELECT COALESCE(MAX(seq),0) FROM work_events WHERE store=?1 AND work_id=?2)`, store, workID).Scan(&scopes, &events, &highest); err != nil {
			return err
		}
		if scopes < 1 || scopes > l.workScopesPerItem || events < 1 || events > l.workEvents || highest != latest {
			return workError("incompatible_store", "the expired item's history exceeds its retained bounds or disagrees with its record")
		}
		var inconsistent int
		if err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM work_events w LEFT JOIN memory_entries e ON e.repository=?3 AND e.seq=w.seq
				WHERE w.store=?1 AND w.work_id=?2 AND (e.seq IS NULL OR e.type<>'work-event' OR e.scope_target IS NOT w.work_id
				OR e.revision<>w.revision OR e.body<>'' OR e.expires IS NOT NULL OR e.supersedes IS NOT NULL OR e.revokes IS NOT NULL))+
			(SELECT COUNT(*) FROM memory_entries e WHERE e.repository=?3 AND e.type='work-event' AND e.scope_target=?2
				AND NOT EXISTS (SELECT 1 FROM work_events w WHERE w.store=?1 AND w.seq=e.seq AND w.work_id=?2))`,
			store, workID, repo).Scan(&inconsistent); err != nil {
			return err
		}
		if inconsistent > 0 {
			return workError("incompatible_store", "the expired item has inconsistent stream rows")
		}
		for _, statement := range []string{
			`DELETE FROM memory_entries WHERE repository=?3 AND seq IN (SELECT seq FROM work_events WHERE store=?1 AND work_id=?2)`,
			`DELETE FROM work_events WHERE store=?1 AND work_id=?2`,
			`DELETE FROM work_scope_revisions WHERE store=?1 AND work_id=?2`,
			`DELETE FROM work_items WHERE store=?1 AND work_id=?2`,
		} {
			if _, err := tx.ExecContext(ctx, statement, store, workID, repo); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memory_stores SET floor=MAX(floor,?) WHERE store_id=?`, highest, store); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

// WorkFields lists the request fields of an operation, without author, key and deadline.
func WorkFields(op string) []string { return append([]string{}, workFields[op]...) }
