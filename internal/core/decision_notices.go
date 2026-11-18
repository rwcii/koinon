package core

// Notices over the link (#746 chunk 02, docs/sprints/2026-11-18-board-link/). The card's
// computer sends each queued notice to the named agent's computer on the pair's notice
// stream, with its own worker and connection, apart from the direct-message stream. The
// stream follows the rules of #670 chunk 03: a head gets its sequence and digest once, its
// attempt is stored before the frame, a lost answer leaves it uncertain and the same frame is
// sent again, and the receiver stores its result with the stream's high water. A notice holds
// no card text; the receiver stores it as one typed inbox message with the ordinary wake.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// noticeSender is the reserved sender of a notice: family link, which no agent, maintainer or
// overwatch identity has and no frame or send can select, and the name decision-board@COMPUTER.
const (
	noticeFamily = "link"
	noticeSender = "decision-board"
	noticeKind   = "decision_notice"
)

var (
	noticeEventKinds = []string{"named", "changed", "removed", "withdrawn", "closed", "restored", "verdict"}
	noticeVerdicts   = []string{"approved", "declined", "feedback", "completed", "acknowledged", "dismissed"}

	ErrLinkReplyNotice = Refusal{"link_reply_notice", "a decision notice has no sender to answer; read the card with decision_get"}

	// errLinkOldPeer, at the fault point notice-op, makes the receiver answer as an earlier
	// release does: the operation is unknown. A test removes it to stand for the upgrade.
	errLinkOldPeer = errors.New("link fault old peer")
)

// noticeBody is the notice operation. Every field is bounded, so a frame stays far under the
// link's frame bound.
type noticeBody struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	XID       string `json:"xid"`
	Digest    string `json:"digest"`
	Reference string `json:"reference"`
	Recipient string `json:"recipient"`
	Address   string `json:"address"`
	Event     string `json:"event"`
	Revision  int64  `json:"revision"`
	VerdictID int64  `json:"verdict_id,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
}

// noticeHead is a pair's oldest live notice.
type noticeHead struct {
	Row int64
	noticeBody
}

// noticeDigest binds a notice's sequence to its fields.
func noticeDigest(b noticeBody) string {
	h := sha256.New()
	var n [8]byte
	for _, v := range []int64{b.Seq, b.Revision, b.VerdictID} {
		binary.BigEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	for _, s := range []string{b.XID, b.Reference, b.Recipient, b.Address, b.Event, b.Verdict} {
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// valid checks a received notice's fields.
func (b noticeBody) valid() bool {
	verdict := b.Event == "verdict"
	return b.Seq >= 1 && validXID(b.XID) && checkReference(b.Reference) == nil &&
		b.Recipient != "" && len(b.Recipient) <= link.MaxLocalNameBytes && !strings.ContainsAny(b.Recipient, "@\x00\r\n") &&
		len(b.Address) <= link.MaxAddressBytes && !strings.ContainsAny(b.Address, "\x00\r\n") &&
		slices.Contains(noticeEventKinds, b.Event) && b.Revision >= 1 &&
		verdict == (b.VerdictID > 0) && verdict == slices.Contains(noticeVerdicts, b.Verdict) && (verdict || b.Verdict == "")
}

// DecisionNotice is a notice's typed metadata on an inbox message: the card's reference, the
// pair and the current local name of the computer that holds the card, the event, the
// revision, the verdict for a verdict notice, and Get, the reference to read the card with.
type DecisionNotice struct {
	Reference string `json:"reference"`
	Computer  string `json:"computer"`
	Pair      string `json:"pair"`
	Event     string `json:"event"`
	Revision  int64  `json:"revision"`
	VerdictID int64  `json:"verdict_id,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	Get       string `json:"get"`
}

// storedNotice is the part of a notice that the receiver keeps on the message; the computer
// name is not kept, because a rename changes it; a read takes it from the sender name.
type storedNotice struct {
	Reference string `json:"reference"`
	Event     string `json:"event"`
	Revision  int64  `json:"revision"`
	VerdictID int64  `json:"verdict_id,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
}

// projectNotice turns a stored notice into the message's typed metadata.
func projectNotice(raw, pair, sender string) *DecisionNotice {
	var n storedNotice
	if raw == "" || json.Unmarshal([]byte(raw), &n) != nil {
		return nil
	}
	computer := sender[strings.LastIndexByte(sender, '@')+1:]
	return &DecisionNotice{Reference: n.Reference, Computer: computer, Pair: pair, Event: n.Event, Revision: n.Revision,
		VerdictID: n.VerdictID, Verdict: n.Verdict, Get: n.Reference + "@" + computer}
}

// decisionNoticeText is a notice's message body. It names the card and the event, and no card text.
func decisionNoticeText(b noticeBody) string {
	var s string
	switch b.Event {
	case "named":
		s = fmt.Sprintf("You are named on decision card %s on another computer (revision %d).", b.Reference, b.Revision)
	case "changed":
		s = fmt.Sprintf("Decision card %s changed (revision %d).", b.Reference, b.Revision)
	case "removed":
		s = fmt.Sprintf("You are no longer named on decision card %s (revision %d).", b.Reference, b.Revision)
	case "withdrawn", "closed", "restored":
		s = fmt.Sprintf("Decision card %s was %s (revision %d).", b.Reference, b.Event, b.Revision)
	default:
		s = fmt.Sprintf("Decision card %s has a verdict: %s (verdict %d on its computer, revision %d).", b.Reference, b.Verdict, b.VerdictID, b.Revision)
	}
	return s + " The card lives on the computer in this message's sender name; read it there with decision_get " + b.Reference + "@COMPUTER."
}

// signalNotices wakes the notice dispatcher after the transaction commits. The signal never
// blocks and takes no lock: a board write still holds the link lock when its commit runs.
func (s *Store) signalNotices(tx *writeTx) {
	if s.noticeKick != nil {
		tx.onCommit(s.noticeKick)
	}
}

// noticePending lists the pairs with live notices.
func (s *Store) noticePending(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT pair_id FROM decision_notices WHERE `+noticeLive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// noticeNextHead returns the pair's oldest live notice. One without a sequence gets the
// notice stream's next one and its digest in the same transaction, so a notice is bound to
// one sequence and a sequence is never reused.
func (s *Store) noticeNextHead(ctx context.Context, pairID string) (noticeHead, bool, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return noticeHead{}, false, err
	}
	defer tx.Rollback()
	var h noticeHead
	var seq sql.NullInt64
	var digest sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id,xid,seq,digest,reference,recipient,address,event,revision,verdict_id,verdict
		FROM decision_notices WHERE pair_id=? AND `+noticeLive+` ORDER BY id LIMIT 1`, pairID).
		Scan(&h.Row, &h.XID, &seq, &digest, &h.Reference, &h.Recipient, &h.Address, &h.Event, &h.Revision, &h.VerdictID, &h.Verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return noticeHead{}, false, nil
	}
	if err != nil {
		return noticeHead{}, false, err
	}
	h.ID = pairID
	if seq.Valid {
		h.Seq, h.Digest = seq.Int64, digest.String
		return h, true, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO link_streams(pair_id,stream,direction) VALUES (?,'notice','out')`, pairID); err != nil {
		return noticeHead{}, false, tx.fail(err)
	}
	if err := tx.QueryRowContext(ctx, `UPDATE link_streams SET next_seq=next_seq+1 WHERE pair_id=? AND stream='notice' AND direction='out'
		RETURNING next_seq-1`, pairID).Scan(&h.Seq); err != nil {
		return noticeHead{}, false, tx.fail(err)
	}
	h.Digest = noticeDigest(h.noticeBody)
	if _, err := tx.ExecContext(ctx, `UPDATE decision_notices SET seq=?,digest=?,state='sending',updated_at=? WHERE id=?`,
		h.Seq, h.Digest, s.now().UnixMilli(), h.Row); err != nil {
		return noticeHead{}, false, tx.fail(err)
	}
	return h, true, tx.Commit()
}

// noticeMark changes the state of the head with that sequence and digest while it is live.
func (s *Store) noticeMark(ctx context.Context, set, pairID string, seq int64, digest string) (bool, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE decision_notices SET `+set+`,updated_at=? WHERE pair_id=? AND seq=? AND digest=? AND `+noticeLive,
		s.now().UnixMilli(), pairID, seq, digest)
	if err != nil {
		return false, tx.fail(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, tx.Commit()
}

// noticeAttempt stores the attempt before the frame is written: from then until a matching
// answer the head is uncertain.
func (s *Store) noticeAttempt(ctx context.Context, pairID string, seq int64, digest string) (bool, error) {
	return s.noticeMark(ctx, `state='uncertain'`, pairID, seq, digest)
}

// noticeRetried moves the head back to sending after a busy answer.
func (s *Store) noticeRetried(ctx context.Context, pairID string, seq int64, digest string) (bool, error) {
	return s.noticeMark(ctx, `state='sending'`, pairID, seq, digest)
}

// noticeSettle stores the head's final result. The other computer answered, so the pair's
// notices that an earlier answer marked unsupported wait again, with their sequence if they
// have one.
func (s *Store) noticeSettle(ctx context.Context, pairID string, seq int64, digest, result, reason string) (bool, error) {
	if result != "received" && result != "refused" {
		return false, ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE decision_notices SET state=?,reason=?,updated_at=?,finished_at=?
		WHERE pair_id=? AND seq=? AND digest=? AND `+noticeLive, result, reason, now, now, pairID, seq, digest)
	if err != nil {
		return false, tx.fail(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE decision_notices SET state=CASE WHEN seq IS NULL THEN 'queued' ELSE 'uncertain' END,
		reason='',updated_at=? WHERE pair_id=? AND state='unsupported'`, now, pairID); err != nil {
		return false, tx.fail(err)
	}
	return true, tx.Commit()
}

// noticeUnsupported marks the pair's live notices unsupported: the other computer does not
// know the notice operation. It is not final; each keeps its sequence and digest, and the
// worker tries again later.
func (s *Store) noticeUnsupported(ctx context.Context, pairID, reason string) error {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE decision_notices SET state='unsupported',reason=?,updated_at=?
		WHERE pair_id=? AND `+noticeLive, reason, s.now().UnixMilli(), pairID); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// noticeDelivery is one received notice with the pair it came on and this computer's name
// for that pair.
type noticeDelivery struct {
	noticeBody
	Computer string
}

// noticeReceive applies one notice to the pair's incoming notice stream, with the rules of
// linkReceive: at high-water+1 it stores the result, the digest and the new high water in one
// transaction, with the inbox message only for a receipt; at high water it answers the stored
// result; below it answers stale. A busy or full store answers retry and changes nothing.
func (s *Store) noticeReceive(ctx context.Context, in noticeDelivery) (linkResult, error) {
	tx, err := s.begin(ctx, ordinary)
	if errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
		return linkResult{Result: "retry", Reason: errorCode(err)}, nil
	}
	if err != nil {
		return linkResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO link_streams(pair_id,stream,direction) VALUES (?,'notice','in')`, in.ID); err != nil {
		return linkResult{}, tx.fail(err)
	}
	var high int64
	var last, lastReason, lastDigest string
	if err := tx.QueryRowContext(ctx, `SELECT high_water,last_result,last_reason,last_digest FROM link_streams
		WHERE pair_id=? AND stream='notice' AND direction='in'`, in.ID).Scan(&high, &last, &lastReason, &lastDigest); err != nil {
		return linkResult{}, err
	}
	switch {
	case in.Seq == high && high > 0:
		if in.Digest != lastDigest {
			return linkResult{}, errLinkProtocol
		}
		return linkResult{Result: last, Reason: lastReason}, nil
	case in.Seq <= high:
		return linkResult{Result: "stale"}, nil
	case in.Seq != high+1:
		return linkResult{}, errLinkProtocol
	}
	result, err := s.noticeInsert(ctx, tx, in)
	if err != nil {
		if errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
			return linkResult{Result: "retry", Reason: errorCode(err)}, nil
		}
		return linkResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE link_streams SET high_water=?,last_result=?,last_reason=?,last_digest=?
		WHERE pair_id=? AND stream='notice' AND direction='in'`, in.Seq, result.Result, result.Reason, in.Digest, in.ID); err != nil {
		return linkResult{}, tx.fail(err)
	}
	if err := tx.Commit(); errors.Is(err, ErrCapacity) || errors.Is(err, ErrStorageBlocked) {
		return linkResult{Result: "retry", Reason: errorCode(err)}, nil
	} else if err != nil {
		return linkResult{}, err
	}
	return result, nil
}

// noticeInsert resolves NAME as a message from the link does and inserts the notice as one
// typed message from decision-board@COMPUTER. Only an agent's inbox receives a notice; a
// permanent refusal is a result, not an error.
func (s *Store) noticeInsert(ctx context.Context, tx *writeTx, in noticeDelivery) (linkResult, error) {
	var held int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE link_pair=? AND link_xid=?`, in.ID, in.XID).Scan(&held); err != nil {
		return linkResult{}, err
	}
	if held > 0 {
		return linkResult{}, errLinkProtocol
	}
	now := s.now().UnixMilli()
	family, sessionID, err := s.resolveRecipient(ctx, tx, now, in.Recipient)
	if err == nil && !slices.Contains(SupportedFamilies(), family) {
		return linkResult{Result: "refused", Reason: "link_recipient_local"}, nil
	}
	switch {
	case errors.Is(err, ErrPeerNotFound):
		return linkResult{Result: "refused", Reason: "peer_not_found"}, nil
	case errors.Is(err, ErrAliasUnheld):
		return linkResult{Result: "refused", Reason: "alias_unheld"}, nil
	case err != nil:
		return linkResult{}, err
	}
	seq, err := s.nextInboxSeq(ctx, tx, now, family, sessionID)
	if errors.Is(err, ErrRecipientInactive) {
		return linkResult{Result: "refused", Reason: "recipient_inactive"}, nil
	}
	if err != nil {
		return linkResult{}, err
	}
	stored, _ := json.Marshal(storedNotice{Reference: in.Reference, Event: in.Event, Revision: in.Revision, VerdictID: in.VerdictID, Verdict: in.Verdict})
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
		created_at,delivery_updated_at,system_kind,decision_notice,link_pair,link_xid,link_sender) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		family, sessionID, seq, noticeFamily, "link:"+in.ID, noticeSender+"@"+in.Computer, decisionNoticeText(in.noticeBody), now, now,
		noticeKind, string(stored), in.ID, in.XID, noticeSender); err != nil {
		return linkResult{}, tx.fail(err)
	}
	if err := bumpInbox(ctx, tx, Key{family, sessionID}); err != nil {
		return linkResult{}, tx.fail(err)
	}
	return linkResult{Result: "received"}, nil
}

// answerNotice answers one notice frame of a connection of generation gen, under the link lock
// as answerDeliver does: the pair must be paired with this pin, and the commit belongs to the
// open generation.
func (d *Daemon) answerNotice(pin string, f link.Frame, gen uint64) (link.Frame, bool, error) {
	if errors.Is(d.linkFault("notice-op"), errLinkOldPeer) {
		return link.Frame{}, true, Refusal{"link_operation_unknown", "this computer does not know that link operation; update Koinon on both computers"}
	}
	var body noticeBody
	if f.Decode(&body) != nil || !body.valid() {
		return link.Frame{}, true, ErrInvalid
	}
	if noticeDigest(body) != body.Digest {
		return link.Frame{}, true, ErrLinkProtocol
	}
	switch err := d.linkFault("before-notice-receive"); {
	case errors.Is(err, errLinkRetry):
		reply, err := link.NewFrame("notice-ok", linkDeliverReply{ID: body.ID, Seq: body.Seq, Digest: body.Digest, Result: "retry", Reason: "storage_busy"})
		return reply, false, err
	case err != nil:
		return link.Frame{}, true, err
	}
	d.link.mu.Lock()
	if d.link.gen != gen {
		d.link.mu.Unlock()
		return link.Frame{}, true, ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return link.Frame{}, true, err
	}
	i, ok := pairedPin(s, pin, body.ID)
	if !ok {
		d.link.mu.Unlock()
		return link.Frame{}, true, ErrLinkNoPair
	}
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout/2)
	result, err := d.store.noticeReceive(ctx, noticeDelivery{noticeBody: body, Computer: s.Pairs[i].Effective})
	cancel()
	d.link.mu.Unlock()
	if errors.Is(err, errLinkProtocol) {
		return link.Frame{}, true, ErrLinkProtocol
	}
	if err != nil {
		return link.Frame{}, true, err
	}
	// A lost answer after the commit, for tests.
	if err := d.linkFault("after-notice-receive"); err != nil {
		return link.Frame{}, true, err
	}
	reply, err := link.NewFrame("notice-ok", linkDeliverReply{ID: body.ID, Seq: body.Seq, Digest: body.Digest, Result: result.Result, Reason: result.Reason})
	return reply, false, err
}

// noticeSignal is the Store's kick: a nonblocking send on the dispatcher's channel.
func (d *Daemon) noticeSignal() {
	select {
	case d.notices <- struct{}{}:
	default:
	}
}

// noticeDispatch starts or wakes the notice worker of each pair with live notices, at the
// start of generation gen and after each signal, until the generation closes.
func (d *Daemon) noticeDispatch(gen uint64, life context.Context) {
	defer d.wg.Done()
	for {
		d.noticeKickPending(gen)
		select {
		case <-life.Done():
			return
		case <-d.notices:
		}
	}
}

func (d *Daemon) noticeKickPending(gen uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	pending, err := d.store.noticePending(ctx)
	if err != nil || len(pending) == 0 {
		return
	}
	for id := range pending {
		d.noticeKick(gen, id)
	}
}

// noticeKick wakes the pair's notice worker of generation gen, or starts one.
func (d *Daemon) noticeKick(gen uint64, id string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.listener == nil || d.link.life == nil || d.link.gen != gen {
		return
	}
	if ch, ok := d.link.noticeWorkers[id]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
		return
	}
	ch := make(chan struct{}, 1)
	d.link.noticeWorkers[id] = ch
	d.wg.Add(1)
	go d.noticeWorker(gen, d.link.life, id, ch)
}

// noticeWorker sends one pair's notices in generation gen until none is live, the pair is gone
// or the generation closes. It backs off after a busy answer, a lost connection or an earlier
// release's refusal, up to linkBackoffMax.
func (d *Daemon) noticeWorker(gen uint64, life context.Context, id string, kick chan struct{}) {
	defer d.wg.Done()
	defer func() {
		d.link.mu.Lock()
		if d.link.noticeWorkers[id] == kick {
			delete(d.link.noticeWorkers, id)
		}
		d.link.mu.Unlock()
	}()
	d.link.mu.Lock()
	wait := d.link.backoff
	d.link.mu.Unlock()
	if wait <= 0 {
		wait = linkBackoff
	}
	delay := time.Duration(0)
	for {
		if delay > 0 {
			t := time.NewTimer(delay)
			select {
			case <-life.Done():
				t.Stop()
				return
			case <-kick:
			case <-t.C:
			}
			t.Stop()
		}
		if life.Err() != nil {
			return
		}
		more, ok, err := d.noticeBatch(gen, life, id)
		switch {
		case errors.Is(err, errLinkFault):
			return
		case !more:
			if d.noticeIdle(id, kick) {
				return
			}
			delay = 0
		case ok:
			delay = 0
		default:
			if delay == 0 {
				delay = wait
			} else if delay = 2 * delay; delay > linkBackoffMax {
				delay = linkBackoffMax
			}
		}
	}
}

// noticeIdle ends the worker when the pair has no live notice. It reads under the link lock,
// which a board write holds until its commit, so a notice committed after this read signals
// the dispatcher and finds no worker: it starts a new one.
func (d *Daemon) noticeIdle(id string, kick chan struct{}) bool {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	pending, err := d.store.noticePending(ctx)
	if err != nil || pending[id] {
		return false
	}
	if d.link.noticeWorkers[id] == kick {
		delete(d.link.noticeWorkers, id)
	}
	return true
}

// noticeBatch connects once and sends heads until none is live (more false), or until an
// answer asks to wait (ok false).
func (d *Daemon) noticeBatch(gen uint64, life context.Context, id string) (more, ok bool, err error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return true, false, nil
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id })
	if i < 0 {
		d.linkRetireIfGone(id)
		return false, false, nil
	}
	p := s.Pairs[i]
	if p.State != link.Paired {
		return true, false, nil
	}
	ctx, cancel := context.WithTimeout(life, 2*linkIOTimeout)
	session, err := d.connectPair(ctx, gen, p)
	cancel()
	if err != nil {
		return true, false, nil
	}
	defer session.close()
	progress := false
	for frames := 0; ; frames++ {
		if frames == linkMaxFramesConn-4 {
			return true, true, nil
		}
		var h noticeHead
		var found bool
		if !d.linkStore(gen, id, func(ctx context.Context) error {
			var err error
			if h, found, err = d.store.noticeNextHead(ctx, id); err != nil || !found {
				return err
			}
			attempted, err := d.store.noticeAttempt(ctx, id, h.Seq, h.Digest)
			if err == nil && !attempted {
				err = errors.New("notice head changed")
			}
			return err
		}) {
			return true, progress, nil
		}
		if !found {
			return false, progress, nil
		}
		reply, callErr := d.noticeCall(session, h.noticeBody)
		if errors.Is(callErr, errLinkFault) {
			return true, false, errLinkFault
		}
		var refused Refusal
		if errors.As(callErr, &refused) && refused.Code == "link_operation_unknown" {
			// A definite refusal: the other computer runs an earlier release. Nothing ran
			// there; the notices wait, unsupported, and the worker tries again later.
			d.linkStore(gen, id, func(ctx context.Context) error { return d.store.noticeUnsupported(ctx, id, refused.Code) })
			return true, false, nil
		}
		if callErr != nil || reply.ID != id || reply.Seq != h.Seq || reply.Digest != h.Digest {
			return true, false, nil
		}
		switch reply.Result {
		case "received", "refused":
			if !d.linkStore(gen, id, func(ctx context.Context) error {
				_, err := d.store.noticeSettle(ctx, id, h.Seq, h.Digest, reply.Result, reply.Reason)
				return err
			}) {
				return true, false, nil
			}
			progress = true
		case "retry":
			d.linkStore(gen, id, func(ctx context.Context) error {
				_, err := d.store.noticeRetried(ctx, id, h.Seq, h.Digest)
				return err
			})
			return true, false, nil
		default:
			return true, false, nil
		}
	}
}

// noticeCall writes one notice frame and reads its answer.
func (d *Daemon) noticeCall(session linkSession, body noticeBody) (linkDeliverReply, error) {
	var out linkDeliverReply
	f, err := link.NewFrame("notice", body)
	if err != nil {
		return out, err
	}
	session.conn.SetDeadline(time.Now().Add(linkIOTimeout))
	if err := link.WriteFrame(session.conn, f); err != nil {
		return out, err
	}
	if err := d.linkFault("after-notice-write"); err != nil {
		return out, err
	}
	reply, err := link.ReadFrame(session.conn)
	if err != nil {
		return out, err
	}
	if reply.Op == "error" {
		return out, Refusal{reply.Error, reply.Detail}
	}
	if reply.Op != "notice-ok" {
		return out, errors.New("link_reply_invalid")
	}
	return out, reply.Decode(&out)
}
