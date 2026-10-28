package core

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// decisionBoardCards and decisionBoardArchive are the cards and archive rows of one board
	// page; further pages follow by keyset links, so the board never fails at a number of items.
	decisionBoardCards   = 25
	decisionBoardArchive = 20
	// decisionBoardMatches is the number of references that one page of a search lists.
	decisionBoardMatches = 20
	// decisionBoardAgents bounds the agent table; the Sessions view lists every session.
	decisionBoardAgents = 200
	// decisionAgentRefs is how many waiting references an agent row names before "+N more".
	decisionAgentRefs = 8
)

// decisionPriority sorts tasks, the maintainer's own work, before the other cards (#537).
const decisionPriority = `CASE WHEN kind='task' THEN 0 ELSE 1 END`

// decisionStatus is one status filter of the board (#411): the label of its empty card list,
// the fixed SQL condition of its cards ("" when it lists no cards), and whether it lists the
// archive.
type decisionStatus struct {
	label, cards string
	archive      bool
}

// decisionStatuses are the board's status filters by their status parameter; "" is all. A
// request names only a key; the SQL comes from this table.
var decisionStatuses = map[string]decisionStatus{
	"":            {"active, returned or in-progress", `state IN ('active','returned','in_progress')`, true},
	"waiting":     {"waiting", `state='active' AND kind!='fyi'`, false},
	"returned":    {"returned", `state='returned'`, false},
	"in_progress": {"in progress", `state='in_progress'`, false},
	"fyi":         {"open fyi", `state='active' AND kind='fyi'`, false},
	"archived":    {"archived", "", true},
}

type decisionCounts struct {
	Waiting, Returned, InProgress, OpenFYI, Archived int64
}

// decisionAgent is one row of the board's agent table, from daemon data only: the public
// name, observed activity and context, current claimed work and the waiting references that
// name the agent. It holds no native ID, repository path or wake target.
type decisionAgent struct {
	Name     string
	Activity SessionView
	Context  SessionView
	Work     []decisionAgentWork
	Waiting  []string
	More     int64
}

type decisionAgentWork struct {
	WorkID, Title string
}

// decisionsData is the Decisions board (#206, chunk 03).
type decisionsData struct {
	Counts decisionCounts
	// Status is the status filter shown ("" is all); the counts stay totals under it.
	Status, StatusLabel    string
	ShowCards, ShowArchive bool
	Agents                 []decisionAgent
	MoreAgents             bool
	Cards                  []DecisionItem
	NextCards              string
	// CardsAt is the cursor of the page shown, which a verdict form returns to.
	CardsAt string
	Archive DecisionPage
	Lookup  string
	Item    *DecisionItem
	History *DecisionHistoryPage
	// WithdrawalPage is the looked-up item's page of all withdrawals, when asked for (#504).
	WithdrawalPage *DecisionHistoryPage
	MovedAt        int64
	LookupErr      string
	// Matches are the references that contain the looked-up text, ignoring ASCII case, when
	// no item has it as its exact reference (#505); NextMatches is the last reference of the
	// page when more follow.
	Matches     []decisionMatch
	NextMatches string
	// LookupCard is set when the looked-up item is waiting, in progress or an open fyi: it
	// shows as its card, and the grid skips it, so it appears once with one form (#414).
	// GridSkipped is set when the grid page had that card.
	LookupCard, GridSkipped bool
	// Card is the reference of the card that the last action changed; its notice shows on
	// that card when the page shows it.
	Card        string
	CardNotice  string
	CardRefused bool
}

// cardShown reports whether the board shows the card of the last action: in the grid or as
// the looked-up item.
func (d decisionsData) cardShown() bool {
	if d.Item != nil && d.Item.Reference == d.Card {
		return true
	}
	for _, item := range d.Cards {
		if item.Reference == d.Card {
			return true
		}
	}
	return false
}

// NoticeOn reports whether the notice of the last action shows on the card of reference.
// The grid skips a looked-up card (#414), so the notice shows once.
func (d decisionsData) NoticeOn(reference string) bool {
	return d.CardNotice != "" && d.Card == reference
}

// decisionMatch is one reference that a search found, with its item's title, kind and state
// label; a deep-archived reference has no title or kind.
type decisionMatch struct {
	Reference, Title, Kind, State string
}

// decisionCard is one card of the board: the page that shows it, its item, and whether it is
// the looked-up item (#414), which shows at full width above the grid and not in it.
type decisionCard struct {
	Page   dashboardPage
	Item   DecisionItem
	Lookup bool
}

// cardCursor is the keyset position of a card: its priority, item order and reference.
type cardCursor struct {
	priority, order int64
	reference       string
}

func parseCardCursor(v string) (cardCursor, bool) {
	parts := strings.SplitN(v, ":", 3)
	if len(parts) != 3 || checkReference(parts[2]) != nil {
		return cardCursor{}, false
	}
	p, err1 := strconv.ParseInt(parts[0], 10, 64)
	o, err2 := strconv.ParseInt(parts[1], 10, 64)
	return cardCursor{p, o, parts[2]}, err1 == nil && err2 == nil && (p == 0 || p == 1)
}

func (c cardCursor) String() string {
	return strconv.FormatInt(c.priority, 10) + ":" + strconv.FormatInt(c.order, 10) + ":" + c.reference
}

func (s *Store) dashboardDecisions(ctx context.Context, q url.Values) (decisionsData, error) {
	data := decisionsData{Card: q.Get("card")}
	// An unknown status filter is refused like any other malformed view query.
	status, ok := decisionStatuses[q.Get("status")]
	if !ok {
		return data, ErrInvalid
	}
	data.Status, data.StatusLabel = q.Get("status"), status.label
	data.ShowCards, data.ShowArchive = status.cards != "", status.archive
	if _, ok := parseCardCursor(q.Get("cards")); ok {
		data.CardsAt = q.Get("cards")
	}
	// The agent rows read observations, which may pull from agents, before the read
	// transaction takes the store's one connection.
	var err error
	if data.Agents, data.MoreAgents, err = s.decisionAgents(ctx); err != nil {
		return data, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, decisionCountQuery).
		Scan(&data.Counts.Waiting, &data.Counts.Returned, &data.Counts.InProgress, &data.Counts.OpenFYI, &data.Counts.Archived); err != nil {
		return data, err
	}
	if data.ShowCards {
		if err := s.decisionCards(ctx, tx, status.cards, q.Get("cards"), &data); err != nil {
			return data, err
		}
	}
	if data.ShowArchive {
		from, err := strconv.ParseInt(q.Get("archived"), 10, 64)
		if err != nil || from < 0 {
			from = 0
		}
		if data.Archive, err = decisionPage(ctx, tx, "archived", nil, from, decisionBoardArchive); err != nil {
			return data, err
		}
	}
	// A search ignores the spaces of a pasted reference (#505).
	if reference := strings.TrimSpace(q.Get("ref")); reference != "" {
		data.Lookup = reference
		if checkReference(reference) != nil {
			data.LookupErr = "not a valid reference"
			return data, nil
		}
		item, err := decisionItem(ctx, tx, reference)
		switch code := errorCode(err); {
		case code == "not_found":
			if err := decisionMatches(ctx, tx, reference, q.Get("found"), &data); err != nil {
				return data, err
			}
			if len(data.Matches) == 0 {
				data.LookupErr = "no decision item has this reference"
			}
			return data, nil
		case code == "deep_archived":
			if data.MovedAt, _, err = deepArchived(ctx, tx, reference); err != nil {
				return data, err
			}
			return data, nil
		case err != nil:
			return data, err
		}
		data.Item = &item
		if data.LookupCard = item.State == "active" || item.State == "returned" || item.State == "in_progress"; data.LookupCard {
			cards := data.Cards[:0]
			for _, card := range data.Cards {
				if card.Reference != reference {
					cards = append(cards, card)
				}
			}
			data.GridSkipped, data.Cards = len(cards) < len(data.Cards), cards
		}
		after, err := strconv.ParseInt(q.Get("history"), 10, 64)
		if err != nil || after < 0 {
			after = 0
		}
		history, err := decisionHistory(ctx, tx, reference, after, decisionHistoryItems, true)
		if err != nil {
			return data, err
		}
		data.History = &history
		// withdrawals=ID pages every withdrawal after ID, oldest first (#504).
		if q.Has("withdrawals") {
			from, err := strconv.ParseInt(q.Get("withdrawals"), 10, 64)
			if err != nil || from < 0 {
				from = 0
			}
			page, err := withdrawalHistory(ctx, tx, reference, from, decisionHistoryItems)
			if err != nil {
				return data, err
			}
			data.WithdrawalPage = &page
		}
	}
	return data, nil
}

// decisionMatches reads one page of the references that contain text, ignoring ASCII case,
// after the reference after, from the items and the deep archive, in reference order (#505).
func decisionMatches(ctx context.Context, tx *sql.Tx, text, after string, data *decisionsData) error {
	if checkReference(after) != nil {
		after = ""
	}
	pattern := likePattern(text)
	rows, err := tx.QueryContext(ctx, `SELECT reference,title,kind,state FROM (
		SELECT reference,title,kind,CASE WHEN state='archived' AND COALESCE((SELECT w.reopened_at=0 FROM decision_withdrawals w
			WHERE w.item_id=decision_items.id ORDER BY w.id DESC LIMIT 1),0) THEN 'withdrawn' ELSE state END AS state
			FROM decision_items WHERE reference LIKE ? ESCAPE '\'
		UNION ALL SELECT reference,'','','deep_archived' FROM decision_deep_archive WHERE reference LIKE ? ESCAPE '\')
		WHERE reference>? ORDER BY reference LIMIT `+strconv.Itoa(decisionBoardMatches+1), pattern, pattern, after)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m decisionMatch
		if err := rows.Scan(&m.Reference, &m.Title, &m.Kind, &m.State); err != nil {
			return err
		}
		switch {
		case m.State == "active" && m.Kind == "fyi":
			m.State = "open"
		case m.State == "active":
			m.State = "waiting"
		case m.State == "in_progress":
			m.State = "in progress"
		case m.State == "returned":
			m.State = "returned to author"
		case m.State == "deep_archived":
			m.State = "deep archive"
		}
		data.Matches = append(data.Matches, m)
	}
	if len(data.Matches) > decisionBoardMatches {
		data.Matches = data.Matches[:decisionBoardMatches]
		data.NextMatches = data.Matches[len(data.Matches)-1].Reference
	}
	return rows.Err()
}

// decisionCards reads one page of the cards that match a status filter's fixed condition
// (by default the active, returned and in-progress items): tasks first, then item order and
// reference.
func (s *Store) decisionCards(ctx context.Context, tx *sql.Tx, match, after string, data *decisionsData) error {
	where, args := "", []any{}
	if c, ok := parseCardCursor(after); ok {
		where, args = ` AND (`+decisionPriority+`,item_order,reference)>(?,?,?)`, []any{c.priority, c.order, c.reference}
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+decisionPriority+`,item_order,reference FROM decision_items
		WHERE (`+match+`)`+where+` ORDER BY 1,2,3 LIMIT `+strconv.Itoa(decisionBoardCards+1), args...)
	if err != nil {
		return err
	}
	var cursors []cardCursor
	for rows.Next() {
		var c cardCursor
		if err := rows.Scan(&c.priority, &c.order, &c.reference); err != nil {
			rows.Close()
			return err
		}
		cursors = append(cursors, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(cursors) > decisionBoardCards {
		cursors = cursors[:decisionBoardCards]
		data.NextCards = cursors[len(cursors)-1].String()
	}
	for _, c := range cursors {
		item, err := decisionItem(ctx, tx, c.reference)
		if err != nil {
			return err
		}
		data.Cards = append(data.Cards, item)
	}
	return nil
}

// decisionAgents builds the agent table from the active agent sessions, their observations
// and their live claims: native claims, and participant claims on the current holder.
// Unknown values stay unknown with their reason.
func (s *Store) decisionAgents(ctx context.Context) ([]decisionAgent, bool, error) {
	now := s.now().UnixMilli()
	rows, err := s.db.QueryContext(ctx, sessionQuery+` WHERE s.family!='maintainer' AND `+notParticipantRow+`
		AND s.retired_at=0 AND s.expires_at>? ORDER BY s.family,s.id LIMIT `+strconv.Itoa(decisionBoardAgents+1), now)
	if err != nil {
		return nil, false, err
	}
	var sessions []Session
	for rows.Next() {
		r, err := scanSession(rows, now)
		if err != nil {
			rows.Close()
			return nil, false, err
		}
		sessions = append(sessions, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(sessions) > decisionBoardAgents
	if more {
		sessions = sessions[:decisionBoardAgents]
	}
	claims, err := s.sessionClaims(ctx)
	if err != nil {
		return nil, false, err
	}
	// Pulled observations share one short budget, so unreachable agents cannot stall the page.
	pull, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	agents := make([]decisionAgent, 0, len(sessions))
	for _, session := range sessions {
		observed := s.sessionObservations(pull, session)
		agent := decisionAgent{Name: session.Name, Activity: observed["activity"], Context: observed["context"]}
		names := []any{session.Name}
		work := claims[session.Family+":"+session.ID]
		if session.HoldsAddress {
			names = append(names, session.Address)
			work = append(work, claims["participant:"+session.Address]...)
		}
		for _, w := range work {
			agent.Work = append(agent.Work, decisionAgentWork{w.WorkID, w.Title})
		}
		if agent.Waiting, agent.More, err = waitingReferences(ctx, s.db, names); err != nil {
			return nil, false, err
		}
		agents = append(agents, agent)
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, more, nil
}

// waitingReferences returns the first references of the waiting decision items and tasks
// that name one of names as a recipient, and how many more there are.
func waitingReferences(ctx context.Context, tx querier, names []any) ([]string, int64, error) {
	in := "?" + strings.Repeat(",?", len(names)-1)
	match := ` FROM decision_items i WHERE i.state='active' AND i.kind!='fyi'
		AND EXISTS (SELECT 1 FROM decision_recipients r WHERE r.item_id=i.id AND r.name IN (` + in + `))`
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)`+match, names...).Scan(&total); err != nil || total == 0 {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.reference`+match+` ORDER BY i.item_order,i.reference LIMIT `+strconv.Itoa(decisionAgentRefs), names...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total - int64(len(refs)), rows.Err()
}

// decisionActionVerdicts are the dashboard's verdict actions, each with the verdict it gives;
// Decline gives declined on a decision item and on a task.
var decisionActionVerdicts = map[string]string{"decision-approve": "approved", "decision-decline": "declined", "decision-feedback": "feedback",
	"decision-complete": "completed", "decision-acknowledge": "acknowledged"}

// decisionAccepted reports whether a notice code is the answer of an accepted verdict action.
func decisionAccepted(code string) bool {
	for _, verdict := range decisionActionVerdicts {
		if code == "decision_"+verdict {
			return true
		}
	}
	return false
}

// decisionCountQuery counts the board's statuses: waiting decision items and tasks, returned,
// in-progress, open fyi and archived items.
const decisionCountQuery = `SELECT COALESCE(SUM(state='active' AND kind!='fyi'),0),COALESCE(SUM(state='returned'),0),
	COALESCE(SUM(state='in_progress'),0),COALESCE(SUM(state='active' AND kind='fyi'),0),COALESCE(SUM(state='archived'),0) FROM decision_items`

// decisionFormLimit fits a 4 KiB note, percent-encoded, with the other fields.
const decisionFormLimit = 3*decisionNoteLimit + 4096

// decisionActions adds the maintainer's verdicts (#206, chunk 02; #537): Approve, Decline and
// Provide feedback on a decision item, Completed, Declined and Provide feedback on a task, and
// Acknowledge on an fyi item, on the revision the page showed, behind the login, Host, Origin
// and CSRF checks; a maintainer token gives the same verdicts (PDR 009). The answer returns to
// the board page that showed the card, with its status filter and lookup from the back field,
// and with the notice on that card.
func (d *Daemon) decisionActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	run func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string)) {
	for action, verdict := range decisionActionVerdicts {
		action, verdict := action, verdict
		mux.HandleFunc("POST /dashboard/actions/"+action, authed(decisionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
			reference, note := r.PostForm.Get("reference"), r.PostForm.Get("note")
			revision, ok := formInt(r, "revision")
			valid := ok && revision > 0 && checkReference(reference) == nil
			query := url.Values{}
			if valid {
				query.Set("card", reference)
				if _, ok := parseCardCursor(r.PostForm.Get("cards")); ok {
					query.Set("cards", r.PostForm.Get("cards"))
				}
			}
			// A status filter that the board does not know returns to all items, not to a
			// refused page.
			if back, err := url.ParseQuery(r.PostForm.Get("back")); err == nil {
				if _, ok := decisionStatuses[back.Get("status")]; !ok {
					query.Set("status", "")
				}
			}
			run(w, r, action, "decision "+reference, "decisions", query, valid, func(ctx context.Context) error {
				_, err := d.store.DecisionVerdict(ctx, reference, revision, verdict, note)
				return err
			}, "decision_"+verdict, nil)
		}))
	}
}
