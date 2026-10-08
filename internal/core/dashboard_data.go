package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Read-only projections for the dashboard views. None of them writes, so a view never
// competes with agents for the storage bound.

// SessionWork is a live claim held under a session's key (FAMILY:ID).
type SessionWork struct {
	Repository string  `json:"repository"`
	WorkID     string  `json:"work_id"`
	Title      string  `json:"title"`
	Generation int64   `json:"generation"`
	ExpiresAt  float64 `json:"expires_at"`
}

// sessionClaims maps each consumer key to its live claims. The daemon retains at most
// maxDaemonBundles bundles, which bounds the result.
func (s *Store) sessionClaims(ctx context.Context) (map[string][]SessionWork, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.consumer,m.repository,w.work_id,w.title,b.generation,b.expires_at
		FROM claim_bundles b JOIN work_items w ON w.store=b.store AND w.work_id=b.work_id
		JOIN memory_stores m ON m.store_id=b.store WHERE b.active=1 AND b.expires_at>? ORDER BY b.consumer,w.work_id`, s.clock())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]SessionWork{}
	for rows.Next() {
		var consumer string
		var w SessionWork
		if err := rows.Scan(&consumer, &w.Repository, &w.WorkID, &w.Title, &w.Generation, &w.ExpiresAt); err != nil {
			return nil, err
		}
		result[consumer] = append(result[consumer], w)
	}
	return result, rows.Err()
}

const dashboardSessionPage = 100

// The default order is state: active sessions first, so they are on the first page however
// many expired sessions are kept (#227), and within a state the latest renewal first.
var sessionSort = sortSpec{columns: []sortColumn{
	{key: "state", terms: []sortTerm{{"state", true}, {"-renewed_at", false}}},
	{key: "family", terms: []sortTerm{{"family", true}}},
	{key: "name", terms: []sortTerm{{"name", true}}},
	{key: "repository", terms: []sortTerm{{"repository", true}, {"directory", true}}},
	{key: "registered", terms: []sortTerm{{"registered_at", false}}, desc: true},
	{key: "renewed", terms: []sortTerm{{"renewed_at", false}}, desc: true},
	{key: "expires", terms: []sortTerm{{"expires_at", false}}, desc: true},
}, tie: []sortTerm{{"family", true}, {"id", true}}}

// dashboardSessionView names the columns that sessions sort by; state is computed at now (?1).
const dashboardSessionView = `SELECT s.family AS family,s.id AS id,s.repository AS repository,s.directory AS directory,
	s.wake_target AS wake_target,s.registered_at AS registered_at,s.renewed_at AS renewed_at,s.expires_at AS expires_at,
	s.retired_at AS retired_at,s.purge_at AS purge_at,s.revision AS revision,
	COALESCE((SELECT name FROM names WHERE kind='peer' AND family=s.family AND session_id=s.id),'') AS name,
	COALESCE((SELECT name FROM names WHERE kind='alias' AND family=s.family AND repository=s.repository
		AND s.repository!='' AND holder_id=s.id),'') AS alias,
	CASE WHEN s.retired_at!=0 THEN 'retired' WHEN s.expires_at<=?1 THEN 'expired' ELSE 'active' END AS state
	FROM sessions s WHERE s.family!='maintainer'`

// dashboardSearchMax bounds a sessions search, in bytes.
const dashboardSearchMax = 256

// likePattern matches text as a substring with LIKE ... ESCAPE '\', so the wildcards % and _
// in the text match literally.
func likePattern(text string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(text) + "%"
}

// validRepository accepts a repository path from a dashboard form or query: absolute, at
// most 4,096 bytes, without NUL or line breaks.
func validRepository(repository string) bool {
	return filepath.IsAbs(repository) && len(repository) <= 4096 && !strings.ContainsAny(repository, "\x00\r\n")
}

// dashboardSessions lists agent sessions in the sort's order after its cursor, those that
// match search when it is set; next is the cursor for the following page, or "". The
// maintainer's inbox is on the messages view.
func (s *Store) dashboardSessions(ctx context.Context, order dashboardSort, search string) ([]Session, string, error) {
	if len(search) > dashboardSearchMax {
		return nil, "", ErrInvalid
	}
	where, after, by := order.sql()
	now := s.now().UnixMilli()
	args := append([]any{now}, after...)
	if search != "" {
		// LIKE ignores ASCII case. Each listed field is searched as a substring.
		where += ` AND (name LIKE ? ESCAPE '\' OR alias LIKE ? ESCAPE '\' OR family LIKE ? ESCAPE '\'
			OR repository LIKE ? ESCAPE '\' OR directory LIKE ? ESCAPE '\' OR state LIKE ? ESCAPE '\')`
		for range 6 {
			args = append(args, likePattern(search))
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT family,id,repository,directory,wake_target,registered_at,renewed_at,expires_at,
		retired_at,purge_at,revision,name,alias,`+order.selectList()+` FROM (`+dashboardSessionView+`) WHERE `+where+
		` ORDER BY `+by+` LIMIT `+strconv.Itoa(dashboardSessionPage+1), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result, keys := []Session{}, [][]any{}
	for rows.Next() {
		targets := order.scanTargets()
		r, err := scanSession(withExtra{rows, targets}, now)
		if err != nil {
			return nil, "", err
		}
		result, keys = append(result, r), append(keys, scannedValues(targets))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(result) <= dashboardSessionPage {
		return result, "", nil
	}
	return result[:dashboardSessionPage], order.cursor(keys[dashboardSessionPage-1]), nil
}

// withExtra scans a row's leading columns into the caller's destinations and its
// trailing sort values into extra.
type withExtra struct {
	rows  *sql.Rows
	extra []any
}

func (w withExtra) Scan(dest ...any) error { return w.rows.Scan(append(dest, w.extra...)...) }

// MessageRecord is one message as the dashboard lists it.
type MessageRecord struct {
	Message
	RecipientFamily string `json:"recipient_family"`
	RecipientID     string `json:"recipient_id"`
	RecipientName   string `json:"recipient_name"`
	UpdatedAt       int64  `json:"updated_at"`
}

const dashboardMessagePage = 50

var messageSort = sortSpec{columns: []sortColumn{
	{key: "id", terms: []sortTerm{{"id", false}}, desc: true},
	{key: "to", terms: []sortTerm{{"CASE WHEN recipient_name!='' THEN recipient_name ELSE recipient_family||':'||recipient_id END", true}}},
	{key: "seq", terms: []sortTerm{{"seq", false}}, desc: true},
	{key: "from", terms: []sortTerm{{"sender_name", true}}},
	{key: "sent", terms: []sortTerm{{"created_at", false}}, desc: true},
	{key: "delivery", terms: []sortTerm{{"delivery_state", true}}},
	{key: "acknowledged", terms: []sortTerm{{"acknowledged", false}}, desc: true},
}, tie: []sortTerm{{"id", false}}}

// dashboardMessages lists messages in the sort's order after its cursor, optionally for
// one recipient. A page holds at most dashboardMessagePage messages and about one MiB of
// bodies; next is the cursor for the following page, or "".
func (s *Store) dashboardMessages(ctx context.Context, recipient *Key, order dashboardSort) ([]MessageRecord, string, error) {
	query := `SELECT m.id AS id,m.seq AS seq,m.sender_family AS sender_family,m.sender_name AS sender_name,m.body AS body,
		m.created_at AS created_at,m.delivery_state AS delivery_state,m.delivery_reason AS delivery_reason,
		m.delivery_updated_at AS updated_at,m.recipient_family AS recipient_family,m.recipient_id AS recipient_id,
		COALESCE((SELECT name FROM names WHERE kind='peer' AND family=m.recipient_family AND session_id=m.recipient_id),'') AS recipient_name,
		m.seq<=COALESCE(s.acked_through,0) AS acknowledged,m.maintainer_ack AS maintainer_ack
		FROM messages m LEFT JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id`
	args := []any{}
	if recipient != nil {
		query += ` WHERE m.recipient_family=? AND m.recipient_id=?`
		args = append(args, recipient.Family, recipient.ID)
	}
	where, after, by := order.sql()
	rows, err := s.db.QueryContext(ctx, `SELECT id,seq,sender_family,sender_name,body,created_at,delivery_state,delivery_reason,
		updated_at,recipient_family,recipient_id,recipient_name,acknowledged,maintainer_ack,`+order.selectList()+
		` FROM (`+query+`) WHERE `+where+` ORDER BY `+by+` LIMIT `+strconv.Itoa(dashboardMessagePage+1), append(args, after...)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result, keys := []MessageRecord{}, [][]any{}
	size, next := 0, ""
	for rows.Next() {
		var m MessageRecord
		var maintainer bool
		targets := order.scanTargets()
		if err := rows.Scan(append([]any{&m.ID, &m.Seq, &m.SenderFamily, &m.SenderName, &m.Body, &m.CreatedAt, &m.DeliveryState,
			&m.DeliveryReason, &m.UpdatedAt, &m.RecipientFamily, &m.RecipientID, &m.RecipientName, &m.Acknowledged, &maintainer},
			targets...)...); err != nil {
			return nil, "", err
		}
		m.AcknowledgedBy = acknowledgedBy(m.Acknowledged, maintainer)
		if len(result) == dashboardMessagePage || (len(result) > 0 && size+len(m.Body) > 1<<20) {
			next = order.cursor(keys[len(keys)-1])
			break
		}
		size += len(m.Body)
		result, keys = append(result, m), append(keys, scannedValues(targets))
	}
	return result, next, rows.Err()
}

var auditSort = sortSpec{columns: []sortColumn{
	{key: "id", terms: []sortTerm{{"id", false}}, desc: true},
	{key: "time", terms: []sortTerm{{"at", false}}, desc: true},
	{key: "action", terms: []sortTerm{{"action", true}}},
	{key: "target", terms: []sortTerm{{"target", true}}},
	{key: "result", terms: []sortTerm{{"result", true}}},
}, tie: []sortTerm{{"id", false}}}

// dashboardAudit lists audit records in the sort's order after its cursor; next is the
// cursor for the following page, or "".
func (s *Store) dashboardAudit(ctx context.Context, order dashboardSort) ([]AuditRecord, string, error) {
	where, after, by := order.sql()
	rows, err := s.db.QueryContext(ctx, `SELECT id,at,action,target,result,reason,`+order.selectList()+` FROM audit WHERE `+where+
		` ORDER BY `+by+` LIMIT `+strconv.Itoa(auditPage+1), after...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	result, keys := []AuditRecord{}, [][]any{}
	for rows.Next() {
		var r AuditRecord
		targets := order.scanTargets()
		if err := rows.Scan(append([]any{&r.ID, &r.At, &r.Action, &r.Target, &r.Result, &r.Reason}, targets...)...); err != nil {
			return nil, "", err
		}
		result, keys = append(result, r), append(keys, scannedValues(targets))
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	if len(result) <= auditPage {
		return result, "", nil
	}
	return result[:auditPage], order.cursor(keys[auditPage-1]), nil
}

// StoreSummary is one memory store with its head, usage, ceilings and work state.
type StoreSummary struct {
	Repository  string         `json:"repository"`
	StoreID     string         `json:"store_id"`
	Head        int64          `json:"head"`
	Floor       int64          `json:"floor"`
	Usage       memoryUsage    `json:"usage"`
	MaxEntries  int64          `json:"max_entries"`
	MaxLogical  int64          `json:"max_logical"`
	Maintenance map[string]any `json:"work_maintenance"`
	OverdueDebt int64          `json:"overdue_credits"`
	EndDebt     int64          `json:"end_credits"`
	Work        []WorkView     `json:"work"`
}

const dashboardStorePage = 50

// Stores sort in memory: entries, logical bytes and consumers are computed per store.
var storeSort = sortSpec{columns: []sortColumn{
	{key: "repository", terms: []sortTerm{{text: true}}},
	{key: "store", terms: []sortTerm{{text: true}}},
	{key: "head", terms: []sortTerm{{}}, desc: true},
	{key: "entries", terms: []sortTerm{{}}, desc: true},
	{key: "logical", terms: []sortTerm{{}}, desc: true},
	{key: "consumers", terms: []sortTerm{{}}, desc: true},
	{key: "debt", terms: []sortTerm{{}}, desc: true},
}, tie: []sortTerm{{text: true}}}

func storeSortKey(key string, item StoreSummary) []any {
	var value any
	switch key {
	case "repository":
		value = item.Repository
	case "store":
		value = item.StoreID
	case "head":
		value = item.Head
	case "entries":
		value = item.Usage.Entries
	case "logical":
		value = item.Usage.Logical
	case "consumers":
		value = item.Usage.Consumers
	case "debt":
		value = item.OverdueDebt + item.EndDebt
	}
	return []any{value, item.Repository}
}

// dashboardMemory lists memory stores in the sort's order after its cursor; next is the
// cursor for the following page, or "". A sort by usage needs every store's usage, so
// each is summarized; maintenance status is read for the listed page only.
func (s *Store) dashboardMemory(ctx context.Context, order dashboardSort) ([]StoreSummary, string, error) {
	all, err := s.storeList(ctx, "", -1)
	if err != nil {
		return nil, "", err
	}
	limits := s.limits()
	for i := range all {
		item := &all[i]
		item.MaxEntries, item.MaxLogical = limits.entries, limits.logical
		if item.Usage, err = usage(ctx, s.db, item.Repository); err != nil {
			return nil, "", err
		}
		debt, err := storeDebt(ctx, s.db, item.Repository)
		if err != nil {
			return nil, "", err
		}
		item.OverdueDebt, item.EndDebt = debt.overdue, debt.end
	}
	sort.SliceStable(all, func(i, j int) bool {
		return order.before(storeSortKey(order.Key, all[i]), storeSortKey(order.Key, all[j]))
	})
	result := []StoreSummary{}
	for _, item := range all {
		if order.after == nil || order.before(order.after, storeSortKey(order.Key, item)) {
			result = append(result, item)
		}
	}
	next := ""
	if len(result) > dashboardStorePage {
		result = result[:dashboardStorePage]
		next = order.cursor(storeSortKey(order.Key, result[len(result)-1]))
	}
	for i := range result {
		if result[i].Maintenance, err = s.workMaintenanceStatus(ctx, result[i].Repository); err != nil {
			return nil, "", err
		}
	}
	return result, next, nil
}

// Work rows sort in memory within each store's table; they are not paged.
var workSort = sortSpec{columns: []sortColumn{
	{key: "work", terms: []sortTerm{{text: true}}},
	{key: "title", terms: []sortTerm{{text: true}}},
	{key: "lifecycle", terms: []sortTerm{{text: true}}},
	{key: "proposed", terms: []sortTerm{{text: true}}},
	{key: "owner", terms: []sortTerm{{text: true}}},
	{key: "lease", terms: []sortTerm{{}}, desc: true},
	{key: "progress", terms: []sortTerm{{}}, desc: true},
}, tie: []sortTerm{{text: true}}}

func workSortKey(key string, v WorkView) []any {
	var value any
	switch key {
	case "work":
		value = v.WorkID
	case "title":
		value = v.Title
	case "lifecycle":
		value = v.Lifecycle
	case "proposed":
		value = ""
		if v.ProposedAssignee != nil {
			value = *v.ProposedAssignee
		}
	case "owner":
		value = ""
		if v.CurrentClaim != nil {
			value = v.CurrentClaim.Consumer
		}
	case "lease":
		value = 0.0
		if v.LeaseValid && v.CurrentClaim != nil {
			value = v.CurrentClaim.ExpiresAt
		}
	case "progress":
		value = 0.0
		if v.ProgressDeadline != nil {
			value = *v.ProgressDeadline
		}
	}
	return []any{value, v.WorkID}
}

// dashboardWork lists stores in repository order after a repository ("" for the first
// page), each with its unfinished work in the sort's order; next is the cursor for the
// following page, or "".
func (s *Store) dashboardWork(ctx context.Context, after string, order dashboardSort) ([]StoreSummary, string, error) {
	result, err := s.storeList(ctx, after, dashboardStorePage+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(result) > dashboardStorePage {
		result = result[:dashboardStorePage]
		next = result[len(result)-1].Repository
	}
	now := s.clock()
	for i := range result {
		item := &result[i]
		views, err := s.workViews(ctx, item.Repository, now)
		if err != nil {
			return nil, "", err
		}
		for _, v := range views {
			if v.Lifecycle != "finished" {
				item.Work = append(item.Work, v)
			}
		}
		sort.SliceStable(item.Work, func(i, j int) bool {
			return order.before(workSortKey(order.Key, item.Work[i]), workSortKey(order.Key, item.Work[j]))
		})
	}
	return result, next, nil
}

// storeList reads stores in repository order after a repository, at most limit (-1 for all).
func (s *Store) storeList(ctx context.Context, after string, limit int) ([]StoreSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository,store_id,head,floor FROM memory_stores WHERE repository>?
		ORDER BY repository LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []StoreSummary{}
	for rows.Next() {
		var item StoreSummary
		if err := rows.Scan(&item.Repository, &item.StoreID, &item.Head, &item.Floor); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// peerOption is one suggestion of the send form: a peer name or a held alias, with the
// family and state of its session. It never holds a session ID or a directory.
type peerOption struct {
	Name, Label string
}

// dashboardPeerOptionMax bounds the sessions that the send form suggests.
const dashboardPeerOptionMax = 1000

// dashboardPeerOptions lists the names and held aliases that the send form suggests:
// active sessions, and expired and retired ones after them when all is set, each by name.
// The bound applies after this order, so expired sessions never push out active ones.
func (s *Store) dashboardPeerOptions(ctx context.Context, all bool) ([]peerOption, error) {
	query := `SELECT family,name,alias,state FROM (` + dashboardSessionView + `)`
	if !all {
		query += ` WHERE state='active'`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY CASE state WHEN 'active' THEN 0 WHEN 'expired' THEN 1 ELSE 2 END,
		name,family,id LIMIT `+strconv.Itoa(dashboardPeerOptionMax), s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []peerOption{}
	for rows.Next() {
		var family, name, alias, state string
		if err := rows.Scan(&family, &name, &alias, &state); err != nil {
			return nil, err
		}
		label := family + ", " + state
		options = append(options, peerOption{name, label})
		// An alias is held only while its session is active.
		if alias != "" && state == "active" {
			options = append(options, peerOption{alias, "alias of " + name + ", " + label})
		}
	}
	return options, rows.Err()
}

// sessionByName resolves a peer name to its session key for the message filter.
// A held alias resolves to its holder, as it does for a send; an unheld one is not found.
func (s *Store) sessionByName(ctx context.Context, name string) (*Key, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var kind, family, id, repository, holder string
	err = tx.QueryRowContext(ctx, `SELECT kind,family,session_id,repository,holder_id FROM names WHERE name=?`, name).
		Scan(&kind, &family, &id, &repository, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPeerNotFound
	}
	if err != nil {
		return nil, err
	}
	if kind == "alias" {
		held := false
		if holder != "" {
			if held, err = holds(ctx, tx, s.now().UnixMilli(), family, holder, repository); err != nil {
				return nil, err
			}
		}
		if !held {
			return nil, ErrPeerNotFound
		}
		id = holder
	}
	return &Key{family, id}, nil
}
