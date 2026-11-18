package core

// Remote read and acknowledgement (#746 chunk 03, docs/sprints/2026-11-18-board-link/). An
// agent on a paired computer reads the live card that names it, lists the cards of one paired
// computer that name it, lists its waiting verdicts there, and acknowledges them, at the card's
// computer over the link. Nothing is stored on the agent's computer. The card's computer answers
// only for the remote recipient rows and verdict deliveries of the asking pair whose name is
// one of the agent's names, which the agent's computer takes from the authenticated local
// caller.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// boardCallBudget bounds one remote call: the dial, the TLS handshake and the exchange, inside
// the local API server's 10-second write timeout. A shorter deadline of the local request wins.
var boardCallBudget = 8 * time.Second

var (
	ErrBoardComputerUnknown = Refusal{"link_computer_unknown", "no paired computer has that name or pair; nothing was sent"}
	ErrBoardUnsupported     = Refusal{"link_unsupported", "the other computer runs an earlier Koinon release; nothing ran there"}
)

// boardRequest is the board operation, from the agent's computer to the card's computer.
type boardRequest struct {
	ID         string   `json:"id"`
	Op         string   `json:"op"`
	Names      []string `json:"names"`
	Reference  string   `json:"reference,omitempty"`
	After      int64    `json:"after,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	VerdictIDs []int64  `json:"verdict_ids,omitempty"`
}

// valid bounds a request to the tools' own bounds: at most a peer name and a held address,
// one operation, a reference for get, and at most 32 verdict IDs for ack.
func (r boardRequest) valid() bool {
	if len(r.ID) != 32 || len(r.Names) == 0 || len(r.Names) > 2 || r.After < 0 || r.Limit < 0 || r.Limit > decisionPageItems {
		return false
	}
	for _, n := range r.Names {
		if n == "" || len(n) > link.MaxAddressBytes || strings.ContainsAny(n, "@\x00\r\n") {
			return false
		}
	}
	switch r.Op {
	case "get":
		return checkReference(r.Reference) == nil && len(r.VerdictIDs) == 0
	case "list", "pending":
		return r.Reference == "" && len(r.VerdictIDs) == 0
	case "ack":
		if r.Reference != "" || len(r.VerdictIDs) == 0 || len(r.VerdictIDs) > decisionAckIDs {
			return false
		}
		for _, id := range r.VerdictIDs {
			if id < 1 {
				return false
			}
		}
		return true
	}
	return false
}

// BoardOrigin names the computer that holds a card: the agent's local name for it and the
// pair ID, which a rename or a new pairing under the same name never changes.
type BoardOrigin struct {
	Computer string `json:"computer"`
	Pair     string `json:"pair"`
}

// RemoteItem, RemotePage, RemotePending and RemoteAck are the remote answers with their origin.
type RemoteItem struct {
	DecisionItem
	Origin BoardOrigin `json:"origin"`
}

type RemotePage struct {
	DecisionPage
	Origin BoardOrigin `json:"origin"`
}

type RemotePending struct {
	DecisionPendingPage
	Origin BoardOrigin `json:"origin"`
}

type RemoteAck struct {
	Acknowledged []int64     `json:"acknowledged"`
	Already      []int64     `json:"already"`
	Origin       BoardOrigin `json:"origin"`
}

// nameArgs is names as query arguments after the pair.
func nameArgs(pair string, names []string, more ...any) (string, []any) {
	in := "?" + strings.Repeat(",?", len(names)-1)
	args := []any{pair}
	for _, n := range names {
		args = append(args, n)
	}
	return in, append(args, more...)
}

// remoteGet returns a card that names one of names on the asking pair now. Any other card,
// also one that exists, is not_found.
func remoteGet(ctx context.Context, q querier, pair string, names []string, reference string) (DecisionItem, error) {
	in, args := nameArgs(pair, names, reference)
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_recipients r JOIN decision_items i ON i.id=r.item_id
		WHERE r.kind='remote' AND r.pair_id=? AND r.remote_name IN (`+in+`) AND i.reference=?`, args...).Scan(&n); err != nil {
		return DecisionItem{}, err
	}
	if n == 0 {
		return DecisionItem{}, workError("not_found", "no decision item that names you has this reference")
	}
	return decisionItem(ctx, q, reference)
}

// remoteList is one page of the cards that name one of names on the asking pair now, in item
// ID order, with the local page bounds.
func remoteList(ctx context.Context, q querier, pair string, names []string, after int64, limit int) (DecisionPage, error) {
	if limit <= 0 || limit > decisionPageItems {
		limit = decisionPageItems
	}
	in, args := nameArgs(pair, names, after, limit+1)
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT item_id FROM decision_recipients WHERE kind='remote' AND pair_id=? AND remote_name IN (`+in+`)
		AND item_id>? ORDER BY item_id LIMIT ?`, args...)
	if err != nil {
		return DecisionPage{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DecisionPage{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DecisionPage{}, err
	}
	var summaries []DecisionSummary
	for _, id := range ids {
		var s DecisionSummary
		if err := scanDecisionSummary(q.QueryRowContext(ctx, `SELECT `+decisionSummaryColumns+` FROM decision_items WHERE id=?`, id), &s); err != nil {
			return DecisionPage{}, err
		}
		summaries = append(summaries, s)
	}
	return summaryPage(summaries, limit), nil
}

// remotePending is one page of the queued verdict deliveries to one of names on the asking
// pair, with the verdict's own snapshot: a recipient that an edit removed keeps its earlier
// verdicts, and no later card text reaches it.
func remotePending(ctx context.Context, q querier, pair string, names []string, after int64, limit int) (DecisionPendingPage, error) {
	if limit <= 0 || limit > decisionPageItems {
		limit = decisionPageItems
	}
	in, args := nameArgs(pair, names, after, limit+1)
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT verdict_id FROM decision_deliveries WHERE kind='remote' AND pair_id=? AND remote_name IN (`+in+`)
		AND outcome='queued' AND verdict_id>? ORDER BY verdict_id LIMIT ?`, args...)
	if err != nil {
		return DecisionPendingPage{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return DecisionPendingPage{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return DecisionPendingPage{}, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	page := DecisionPendingPage{Deliveries: []DecisionPending{}}
	used := pendingEnvelope()
	for i, id := range ids {
		var p DecisionPending
		var v DecisionVerdict
		var snapshot string
		if err := q.QueryRowContext(ctx, `SELECT v.id,i.reference,v.verdict,v.revision,v.created_at,v.superseded_at,v.note,v.snapshot
			FROM decision_verdicts v JOIN decision_items i ON i.id=v.item_id WHERE v.id=?`, id).
			Scan(&p.VerdictID, &p.Reference, &p.Verdict, &p.Revision, &p.At, &p.SupersededAt, &v.Note, &snapshot); err != nil {
			return DecisionPendingPage{}, err
		}
		if recordedVerdict(p.Verdict) {
			if json.Unmarshal([]byte(snapshot), &v.Snapshot) != nil {
				return DecisionPendingPage{}, workError("decision_unavailable", "a stored snapshot is invalid")
			}
			v.Revision, v.Verdict, v.At = p.Revision, p.Verdict, p.At
			p.Entry = DecisionVerdictText(v.Snapshot.Where, v)
		}
		data, _ := json.Marshal(p)
		size := len(data) + 1
		if i == limit || (len(page.Deliveries) > 0 && used+size > decisionPendingBytes) {
			last := page.Deliveries[len(page.Deliveries)-1].VerdictID
			page.More, page.NextAfter = true, &last
			break
		}
		used += size
		page.Deliveries = append(page.Deliveries, p)
		if recordedVerdict(p.Verdict) {
			page.Record = &DecisionRecord{Instruction: decisionRecordInstruction}
		}
	}
	return page, nil
}

func pendingEnvelope() int {
	largest := int64(math.MaxInt64)
	envelope, _ := json.Marshal(map[string]any{"ok": true, "result": RemotePending{DecisionPendingPage: DecisionPendingPage{Deliveries: []DecisionPending{},
		More: true, NextAfter: &largest, Record: &DecisionRecord{Instruction: decisionRecordInstruction}},
		Origin: BoardOrigin{Computer: strings.Repeat("x", link.MaxAddressBytes), Pair: strings.Repeat("f", 32)}}})
	return len(envelope) + 1
}

// remoteAck acknowledges the queued deliveries of ids to one of names on the asking pair, in
// the caller's transaction. Already lists the IDs whose delivery to these names was
// acknowledged before; an ID of another recipient, or of no delivery, is in neither list.
func remoteAck(ctx context.Context, tx *writeTx, now int64, pair string, names []string, ids []int64) (RemoteAck, error) {
	out := RemoteAck{Acknowledged: []int64{}, Already: []int64{}}
	for _, id := range ids {
		in, args := nameArgs(pair, names, id)
		result, err := tx.ExecContext(ctx, `UPDATE decision_deliveries SET outcome='acknowledged',acknowledged_at=?
			WHERE kind='remote' AND pair_id=? AND remote_name IN (`+in+`) AND verdict_id=? AND outcome='queued'`, append([]any{now}, args...)...)
		if err != nil {
			return RemoteAck{}, tx.fail(err)
		}
		if n, _ := result.RowsAffected(); n > 0 {
			out.Acknowledged = append(out.Acknowledged, id)
			continue
		}
		var earlier int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND pair_id=? AND remote_name IN (`+in+`)
			AND verdict_id=? AND outcome='acknowledged'`, args...).Scan(&earlier); err != nil {
			return RemoteAck{}, err
		}
		if earlier > 0 {
			out.Already = append(out.Already, id)
		}
	}
	return out, nil
}

// answerBoard answers one board request of the paired computer with pin, in generation gen.
// The link lock is held from the pair check through the answer's read or the
// acknowledgement's commit, as for an inbound delivery, so an unpair or a close of the link
// that returns first leaves no later answer or acknowledgement.
func (d *Daemon) answerBoard(pin string, f link.Frame, gen uint64) (link.Frame, bool, error) {
	var r boardRequest
	if f.Decode(&r) != nil || !r.valid() {
		return link.Frame{}, true, ErrInvalid
	}
	if err := d.linkFault("before-board"); err != nil {
		return link.Frame{}, true, err
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return link.Frame{}, true, ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return link.Frame{}, true, err
	}
	if _, ok := pairedPin(s, pin, r.ID); !ok {
		return link.Frame{}, true, ErrLinkNoPair
	}
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout/2)
	defer cancel()
	var result any
	if r.Op == "ack" {
		result, err = d.store.remoteAckWrite(ctx, r.ID, r.Names, r.VerdictIDs)
	} else {
		result, err = d.store.remoteRead(ctx, r)
	}
	if err != nil {
		return link.Frame{}, true, err
	}
	if err := d.linkFault("after-board"); err != nil {
		return link.Frame{}, true, err
	}
	reply, err := link.NewFrame("board-ok", result)
	return reply, false, err
}

func (s *Store) remoteRead(ctx context.Context, r boardRequest) (any, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	switch r.Op {
	case "get":
		return remoteGet(ctx, tx, r.ID, r.Names, r.Reference)
	case "list":
		return remoteList(ctx, tx, r.ID, r.Names, r.After, r.Limit)
	}
	return remotePending(ctx, tx, r.ID, r.Names, r.After, r.Limit)
}

func (s *Store) remoteAckWrite(ctx context.Context, pair string, names []string, ids []int64) (RemoteAck, error) {
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return RemoteAck{}, err
	}
	defer tx.Rollback()
	out, err := remoteAck(ctx, tx, s.now().UnixMilli(), pair, names, ids)
	if err != nil {
		return RemoteAck{}, err
	}
	if len(out.Acknowledged) > 0 {
		if err := bumpChange(ctx, tx, changeDecisions); err != nil {
			return RemoteAck{}, tx.fail(err)
		}
	}
	return out, tx.Commit()
}

// callerBoardNames are the names of an active caller that a remote card can name: its peer
// name and the participant address it holds now.
func (s *Store) callerBoardNames(ctx context.Context, caller Key) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return nil, err
	}
	return callerNames(ctx, tx, now, caller)
}

// boardTarget names the paired computer of a remote call: by pair ID, which wins, or by its
// current effective name.
func (d *Daemon) boardTarget(computer, pair string) (link.Pair, uint64, error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	listening, gen := d.link.listener != nil && d.link.life != nil, d.link.gen
	d.link.mu.Unlock()
	if err != nil {
		return link.Pair{}, 0, err
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool {
		return p.State == link.Paired && (pair != "" && p.ID == pair || pair == "" && p.Effective == computer)
	})
	if i < 0 {
		return link.Pair{}, 0, ErrBoardComputerUnknown
	}
	if !listening {
		return link.Pair{}, 0, boardOffline(s.Pairs[i], "remote_off")
	}
	return s.Pairs[i], gen, nil
}

func boardOffline(p link.Pair, reason string) error {
	return WorkRefusal{Code: "link_computer_offline", Message: "the computer that holds the card did not answer; nothing was sent",
		Details: map[string]any{"computer": p.Effective, "pair": p.ID, "reason": reason}}
}

func boardUncertain(p link.Pair) error {
	return WorkRefusal{Code: "link_uncertain", Message: "the request was sent and its answer did not come back; an acknowledgement may have been stored; a retry is safe",
		Details: map[string]any{"computer": p.Effective, "pair": p.ID}}
}

// boardCall sends one board request for the caller to the computer named by computer or
// pair, and decodes the answer into out. Before the request frame is written, a failure says
// nothing was sent; a definite refusal from that computer says nothing ran there; any failure
// after the write began is link_uncertain. Nothing is stored here.
func (d *Daemon) boardCall(ctx context.Context, caller Key, computer, pair string, r boardRequest, out any) (BoardOrigin, error) {
	names, err := d.store.callerBoardNames(ctx, caller)
	if err != nil {
		return BoardOrigin{}, err
	}
	if len(names) == 0 {
		return BoardOrigin{}, workError("not_found", "no decision item names you")
	}
	p, gen, err := d.boardTarget(computer, pair)
	if err != nil {
		return BoardOrigin{}, err
	}
	origin := BoardOrigin{Computer: p.Effective, Pair: p.ID}
	r.ID, r.Names = p.ID, names
	ctx, cancel := context.WithTimeout(ctx, boardCallBudget)
	defer cancel()
	session, err := d.connectPair(ctx, gen, p)
	if err != nil {
		return origin, boardOffline(p, errorCode(err))
	}
	defer session.close()
	deadline, _ := ctx.Deadline()
	session.conn.SetDeadline(deadline)
	// A cancel of the local request, or the budget, ends the exchange at once.
	stop := context.AfterFunc(ctx, func() { session.conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	f, err := link.NewFrame("board", r)
	if err != nil {
		return origin, err
	}
	if ctx.Err() != nil {
		return origin, boardOffline(p, "deadline")
	}
	if err := link.WriteFrame(session.conn, f); err != nil {
		return origin, boardUncertain(p)
	}
	if err := d.linkFault("after-board-write"); err != nil {
		return origin, boardUncertain(p)
	}
	reply, err := link.ReadFrame(session.conn)
	if err != nil {
		return origin, boardUncertain(p)
	}
	if reply.Op == "error" {
		if reply.Error == "link_operation_unknown" {
			return origin, ErrBoardUnsupported
		}
		return origin, WorkRefusal{Code: reply.Error, Message: reply.Detail, Details: map[string]any{"computer": p.Effective, "pair": p.ID}}
	}
	if reply.Op != "board-ok" || reply.Decode(out) != nil {
		return origin, boardUncertain(p)
	}
	return origin, nil
}

// RemoteDecisionGet reads the live card REFERENCE at the computer named by computer or pair.
func (d *Daemon) RemoteDecisionGet(ctx context.Context, caller Key, computer, pair, reference string) (RemoteItem, error) {
	if err := checkReference(reference); err != nil {
		return RemoteItem{}, err
	}
	var out RemoteItem
	origin, err := d.boardCall(ctx, caller, computer, pair, boardRequest{Op: "get", Reference: reference}, &out.DecisionItem)
	out.Origin = origin
	return out, err
}

// RemoteDecisionList lists the cards that name the caller at one paired computer.
func (d *Daemon) RemoteDecisionList(ctx context.Context, caller Key, computer, pair string, after int64, limit int) (RemotePage, error) {
	if after < 0 || limit < 0 {
		return RemotePage{}, decisionInvalid("after and limit", "must not be negative")
	}
	var out RemotePage
	origin, err := d.boardCall(ctx, caller, computer, pair, boardRequest{Op: "list", After: after, Limit: limit}, &out.DecisionPage)
	out.Origin = origin
	return out, err
}

// RemoteDecisionPending lists the caller's waiting verdicts at one paired computer.
func (d *Daemon) RemoteDecisionPending(ctx context.Context, caller Key, computer, pair string, after int64, limit int) (RemotePending, error) {
	if after < 0 || limit < 0 {
		return RemotePending{}, decisionInvalid("after and limit", "must not be negative")
	}
	var out RemotePending
	origin, err := d.boardCall(ctx, caller, computer, pair, boardRequest{Op: "pending", After: after, Limit: limit}, &out.DecisionPendingPage)
	out.Origin = origin
	return out, err
}

// RemoteDecisionAck acknowledges the caller's verdicts at one paired computer; the IDs are that
// computer's verdict IDs.
func (d *Daemon) RemoteDecisionAck(ctx context.Context, caller Key, computer, pair string, ids []int64) (RemoteAck, error) {
	if len(ids) == 0 || len(ids) > decisionAckIDs {
		return RemoteAck{}, decisionInvalid("verdict_ids", fmt.Sprintf("must name 1 to %d verdicts", decisionAckIDs))
	}
	var out RemoteAck
	origin, err := d.boardCall(ctx, caller, computer, pair, boardRequest{Op: "ack", VerdictIDs: ids}, &out)
	out.Origin = origin
	return out, err
}

// splitRemoteReference splits REFERENCE@COMPUTER; a plain reference has no computer.
func splitRemoteReference(s string) (reference, computer string) {
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

var _ = errors.New
var _ net.Conn
