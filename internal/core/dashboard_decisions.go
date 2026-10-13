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
	// decisionBoardAgents bounds the agent table; the Sessions view lists every session.
	decisionBoardAgents = 200
	// decisionAgentRefs is how many waiting references an agent row names before "+N more".
	decisionAgentRefs = 8
	// actionForYou is the title marker of an fyi item that asks the maintainer for manual work.
	actionForYou = "ACTION FOR YOU"
)

// decisionPriority sorts ACTION FOR YOU fyi items before the other cards.
const decisionPriority = `CASE WHEN kind='fyi' AND substr(title,1,14)='` + actionForYou + `' THEN 0 ELSE 1 END`

type decisionCounts struct {
	Waiting, InProgress, OpenFYI, Archived int64
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
	Counts     decisionCounts
	Agents     []decisionAgent
	MoreAgents bool
	Cards      []DecisionItem
	NextCards  string
	// CardsAt is the cursor of the page shown, which a verdict form returns to.
	CardsAt   string
	Archive   DecisionPage
	Lookup    string
	Item      *DecisionItem
	History   *DecisionHistoryPage
	MovedAt   int64
	LookupErr string
	// Card is the reference of the card that the last action changed; its notice shows on
	// that card when the page shows it.
	Card        string
	CardNotice  string
	CardRefused bool
}

// cardShown reports whether the board shows the card of the last action.
func (d decisionsData) cardShown() bool {
	for _, item := range d.Cards {
		if item.Reference == d.Card {
			return true
		}
	}
	return false
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
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='active' AND kind='decision'),0),COALESCE(SUM(state='in_progress'),0),
		COALESCE(SUM(state='active' AND kind='fyi'),0),COALESCE(SUM(state='archived'),0) FROM decision_items`).
		Scan(&data.Counts.Waiting, &data.Counts.InProgress, &data.Counts.OpenFYI, &data.Counts.Archived); err != nil {
		return data, err
	}
	if err := s.decisionCards(ctx, tx, q.Get("cards"), &data); err != nil {
		return data, err
	}
	from, err := strconv.ParseInt(q.Get("archived"), 10, 64)
	if err != nil || from < 0 {
		from = 0
	}
	if data.Archive, err = decisionPage(ctx, tx, "archived", nil, from, decisionBoardArchive); err != nil {
		return data, err
	}
	if reference := q.Get("ref"); reference != "" {
		data.Lookup = reference
		if checkReference(reference) != nil {
			data.LookupErr = "not a valid reference"
			return data, nil
		}
		item, err := decisionItem(ctx, tx, reference)
		switch code := errorCode(err); {
		case code == "not_found":
			data.LookupErr = "no decision item has this reference"
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
		after, err := strconv.ParseInt(q.Get("history"), 10, 64)
		if err != nil || after < 0 {
			after = 0
		}
		history, err := decisionHistory(ctx, tx, reference, after, decisionHistoryItems)
		if err != nil {
			return data, err
		}
		data.History = &history
	}
	return data, nil
}

// decisionCards reads one page of active and in-progress cards: ACTION FOR YOU fyi items
// first, then item order and reference.
func (s *Store) decisionCards(ctx context.Context, tx *sql.Tx, after string, data *decisionsData) error {
	where, args := "", []any{}
	if c, ok := parseCardCursor(after); ok {
		where, args = ` AND (`+decisionPriority+`,item_order,reference)>(?,?,?)`, []any{c.priority, c.order, c.reference}
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+decisionPriority+`,item_order,reference FROM decision_items
		WHERE state IN ('active','in_progress')`+where+` ORDER BY 1,2,3 LIMIT `+strconv.Itoa(decisionBoardCards+1), args...)
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
		AND s.retired_at=0 AND s.expires_at>? AND s.subagent=0 ORDER BY s.family,s.id LIMIT `+strconv.Itoa(decisionBoardAgents+1), now)
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

// waitingReferences returns the first references of the waiting decision items that name
// one of names as a recipient, and how many more there are.
func waitingReferences(ctx context.Context, tx querier, names []any) ([]string, int64, error) {
	in := "?" + strings.Repeat(",?", len(names)-1)
	match := ` FROM decision_items i WHERE i.state='active' AND i.kind='decision'
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

// decisionFormLimit fits a 4 KiB note, percent-encoded, with the other fields.
const decisionFormLimit = 3*decisionNoteLimit + 4096

// decisionActions adds the maintainer's verdicts (#206, chunk 02): Approve and Decline on a
// decision item and Dismiss on an fyi item, on the revision the page showed. They are
// dashboard actions only, behind the login, Host, Origin and CSRF checks. The answer returns
// to the board page that showed the card, with the notice on that card.
func (d *Daemon) decisionActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc,
	run func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string)) {
	for action, verdict := range map[string]string{"decision-approve": "approved", "decision-decline": "declined", "decision-dismiss": "dismissed"} {
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
			run(w, r, action, "decision "+reference, "decisions", query, valid, func(ctx context.Context) error {
				_, err := d.store.DecisionVerdict(ctx, reference, revision, verdict, note)
				return err
			}, "decision_"+verdict, nil)
		}))
	}
}
