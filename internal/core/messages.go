package core

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"unicode/utf8"
)

// Typed message errors. Each maps to one stable API code in failure.
var (
	ErrPeerNotFound      = errors.New("no session has that name")
	ErrAliasUnheld       = errors.New("no active session holds that alias")
	ErrRecipientInactive = errors.New("recipient session is expired or retired")
	ErrCallerInactive    = errors.New("caller session is not active")
	ErrAckBeyondLast     = errors.New("acknowledgement beyond the last sequence")
	ErrMessageNotFound   = errors.New("no message sent by the caller has that id")
)

const maxBody = 65536

// Key names one session. Every message call names its caller with it.
type Key struct {
	Family string `json:"family"`
	ID     string `json:"id"`
}

type Message struct {
	ID             int64  `json:"id"`
	Seq            int64  `json:"seq"`
	SenderFamily   string `json:"sender_family"`
	SenderName     string `json:"sender_name"`
	Body           string `json:"body"`
	CreatedAt      int64  `json:"created_at"`
	DeliveryState  string `json:"delivery_state"`
	DeliveryReason string `json:"delivery_reason,omitempty"`
	Acknowledged   bool   `json:"acknowledged"`
	// AcknowledgedBy is recipient or maintainer for an acknowledged message (chunk 09).
	AcknowledgedBy string `json:"acknowledged_by,omitempty"`
	// Inbox is session for the caller's own inbox and participant for the inbox of the
	// participant that the caller holds (chunk 03).
	Inbox string `json:"inbox"`
}

type Inbox struct {
	Messages     []Message `json:"messages"`
	LastSeq      int64     `json:"last_seq"`
	AckedThrough int64     `json:"acked_through"`
	More         bool      `json:"more"`
	// Participant is the inbox of the participant that the caller holds, if any; its
	// messages are in Messages with Inbox "participant".
	Participant *ParticipantInbox `json:"participant,omitempty"`
}

// ParticipantInbox is the sequence state of a participant's inbox.
type ParticipantInbox struct {
	Address      string `json:"address"`
	LastSeq      int64  `json:"last_seq"`
	AckedThrough int64  `json:"acked_through"`
	More         bool   `json:"more"`
}

type Outcome struct {
	ID             int64  `json:"id"`
	Recipient      string `json:"recipient"`
	Seq            int64  `json:"seq"`
	DeliveryState  string `json:"delivery_state"`
	DeliveryReason string `json:"delivery_reason,omitempty"`
	UpdatedAt      int64  `json:"updated_at"`
	Acknowledged   bool   `json:"acknowledged"`
	AcknowledgedBy string `json:"acknowledged_by,omitempty"`
}

// acknowledgedBy names who acknowledged a message: the maintainer when the dashboard
// marked it, otherwise the recipient.
func acknowledgedBy(acknowledged, maintainer bool) string {
	switch {
	case !acknowledged:
		return ""
	case maintainer:
		return "maintainer"
	}
	return "recipient"
}

// active confirms in tx that the caller is an active session.
func active(ctx context.Context, tx *sql.Tx, now int64, caller Key) error {
	return activeCaller(ctx, tx, now, caller, false)
}

// activeCaller also accepts the built-in maintainer session when the dashboard calls.
func activeCaller(ctx context.Context, tx *sql.Tx, now int64, caller Key, dashboard bool) error {
	if !validKey(caller.Family, caller.ID) && !(dashboard && caller == maintainerKey) {
		return ErrInvalid
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE family=? AND id=? AND retired_at=0 AND expires_at>?`,
		caller.Family, caller.ID, now).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCallerInactive
	}
	return err
}

// Peer is the view of a session that other sessions see. It never carries a wake
// target, a working directory or a revision.
type Peer struct {
	Name       string `json:"name"`
	Alias      string `json:"alias,omitempty"`
	Family     string `json:"family"`
	State      string `json:"state"`
	Repository string `json:"repository"`
	// Subagent marks a Codex sub-agent thread; it never holds an alias.
	Subagent bool `json:"subagent,omitempty"`
	// Role, Address and HoldsAddress report the session's participant (chunk 02).
	Role         string `json:"role,omitempty"`
	Address      string `json:"address,omitempty"`
	HoldsAddress bool   `json:"holds_address,omitempty"`
}

// Peers lists every session for an active caller.
func (s *Store) Peers(ctx context.Context, caller Key) ([]Peer, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	items, truncated, err := s.List(ctx)
	if err != nil {
		return nil, false, err
	}
	peers := make([]Peer, 0, len(items)+1)
	for _, item := range items {
		peers = append(peers, Peer{Name: item.Name, Alias: item.Alias, Family: item.Family, State: item.State, Repository: item.Repository,
			Subagent: item.Subagent, Role: item.Role, Address: item.Address, HoldsAddress: item.HoldsAddress})
	}
	// Agents can send to the maintainer, who reads the inbox in the dashboard (chunk 09).
	peers = append(peers, Peer{Name: "maintainer", Family: maintainerKey.Family, State: "active"})
	return peers, truncated, nil
}

// Send stores one message for the session that a peer name or a held alias names. The
// sequence number and the message are written in one transaction, so a crash leaves
// either both or neither, and sequence numbers stay gapless.
func (s *Store) Send(ctx context.Context, caller Key, to, body string) (Outcome, error) {
	return s.send(ctx, caller, to, body, false)
}

func (s *Store) send(ctx context.Context, caller Key, to, body string, dashboard bool) (Outcome, error) {
	if to == "" || len(to) > 256 || body == "" || len(body) > maxBody || !utf8.ValidString(body) {
		return Outcome{}, ErrInvalid
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	outcome, err := s.sendTx(ctx, tx, caller, to, body, dashboard)
	if err != nil {
		return Outcome{}, err
	}
	return outcome, tx.Commit()
}

// sendTx lets an explicit work handoff commit its release and notification together.
// Callers validate the body and use ordinary admission when adding a message.
func (s *Store) sendTx(ctx context.Context, tx *writeTx, caller Key, to, body string, dashboard bool) (Outcome, error) {
	now := s.now().UnixMilli()
	if err := activeCaller(ctx, tx.Tx, now, caller, dashboard); err != nil {
		return Outcome{}, err
	}
	var kind, family, sessionID, repository, holder, role string
	err := tx.QueryRowContext(ctx, `SELECT kind,family,session_id,repository,holder_id,role FROM names WHERE name=?`, to).
		Scan(&kind, &family, &sessionID, &repository, &holder, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrPeerNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	if kind == "alias" {
		held := false
		if holder != "" {
			if held, err = holds(ctx, tx.Tx, now, family, holder, repository, role); err != nil {
				return Outcome{}, err
			}
		}
		if !held {
			return Outcome{}, ErrAliasUnheld
		}
		// A message to an address belongs to the participant: its holder reads it, and a
		// successor finds it unread (chunk 03).
		if err := ensureParticipantInbox(ctx, tx.Tx, now, to); err != nil {
			return Outcome{}, tx.fail(err)
		}
		sessionID = participantKey(to)
	}
	// A holder sends as its participant, so a reply reaches the participant's inbox.
	sender, err := heldAddress(ctx, tx.Tx, now, caller)
	if err != nil {
		return Outcome{}, err
	}
	if sender == "" {
		if err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`,
			caller.Family, caller.ID).Scan(&sender); err != nil {
			return Outcome{}, err
		}
	}
	var seq int64
	err = tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=?
		AND retired_at=0 AND expires_at>? RETURNING last_seq`, family, sessionID, now).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrRecipientInactive
	}
	if err != nil {
		return Outcome{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,
		sender_id,sender_name,body,created_at,delivery_updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		family, sessionID, seq, caller.Family, caller.ID, sender, body, now, now)
	if err != nil {
		return Outcome{}, tx.fail(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Outcome{}, err
	}
	submitted := to
	// The outcome names the receiving session by its peer name, and a participant by its
	// address.
	if !isParticipantID(sessionID) {
		if err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`,
			family, sessionID).Scan(&to); err != nil {
			return Outcome{}, err
		}
	}
	if p, _ := ctx.Value(auditContextKey{}).(*pendingAudit); p != nil && dashboard {
		// The record names the session that received it: an alias can move later.
		p.target = "to " + to + " bytes " + strconv.Itoa(len(body)) + " message " + strconv.FormatInt(id, 10)
		if submitted != to {
			p.target += " via " + submitted
		}
	}
	return Outcome{ID: id, Recipient: to, Seq: seq, DeliveryState: "waiting", UpdatedAt: now}, nil
}

// ReadInbox returns the caller's own messages after a sequence, at most limit of them
// and about one MiB of bodies, so a page always fits a bounded response.
func (s *Store) ReadInbox(ctx context.Context, caller Key, after, limit int64) (Inbox, error) {
	return s.ReadInboxes(ctx, caller, after, nil, limit)
}

// ReadInboxes returns the caller's own messages after after and, while the caller holds a
// participant, the participant's messages after participantAfter (0 when nil). Each inbox
// takes at most limit messages and about one MiB of bodies. A participantAfter from a
// caller that holds no participant is refused with ErrStaleHolder.
func (s *Store) ReadInboxes(ctx context.Context, caller Key, after int64, participantAfter *int64, limit int64) (Inbox, error) {
	if limit == 0 {
		limit = 50
	}
	if after < 0 || limit < 1 || limit > 100 || participantAfter != nil && *participantAfter < 0 {
		return Inbox{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Inbox{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return Inbox{}, err
	}
	address, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return Inbox{}, err
	}
	if participantAfter != nil && address == "" {
		return Inbox{}, ErrStaleHolder
	}
	result := Inbox{Messages: []Message{}}
	if result.LastSeq, result.AckedThrough, result.More, err = readMessages(ctx, tx, caller, after, limit, "session", &result.Messages); err != nil {
		return Inbox{}, err
	}
	if address != "" {
		from := int64(0)
		if participantAfter != nil {
			from = *participantAfter
		}
		p := &ParticipantInbox{Address: address}
		// The participant's inbox row exists once a message was sent to the address.
		var rows int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE family=? AND id=?`, caller.Family, participantKey(address)).Scan(&rows); err != nil {
			return Inbox{}, err
		}
		if rows == 1 {
			if p.LastSeq, p.AckedThrough, p.More, err = readMessages(ctx, tx, Key{caller.Family, participantKey(address)}, from, limit, "participant", &result.Messages); err != nil {
				return Inbox{}, err
			}
		}
		result.Participant = p
	}
	return result, nil
}

// readMessages appends the messages of one inbox after a sequence and returns its sequence
// state and whether more remain.
func readMessages(ctx context.Context, tx *sql.Tx, inbox Key, after, limit int64, kind string, messages *[]Message) (last, acked int64, more bool, err error) {
	if err := tx.QueryRowContext(ctx, `SELECT last_seq,acked_through FROM sessions WHERE family=? AND id=?`,
		inbox.Family, inbox.ID).Scan(&last, &acked); err != nil {
		return 0, 0, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,sender_family,sender_name,body,created_at,delivery_state,delivery_reason,maintainer_ack
		FROM messages WHERE recipient_family=? AND recipient_id=? AND seq>? ORDER BY seq LIMIT ?`,
		inbox.Family, inbox.ID, after, limit+1)
	if err != nil {
		return 0, 0, false, err
	}
	defer rows.Close()
	size, count := 0, int64(0)
	for rows.Next() {
		var m Message
		var maintainer bool
		if err := rows.Scan(&m.ID, &m.Seq, &m.SenderFamily, &m.SenderName, &m.Body, &m.CreatedAt, &m.DeliveryState, &m.DeliveryReason, &maintainer); err != nil {
			return 0, 0, false, err
		}
		if count == limit || (count > 0 && size+len(m.Body) > 1<<20) {
			more = true
			break
		}
		size += len(m.Body)
		count++
		m.Acknowledged = m.Seq <= acked
		m.AcknowledgedBy = acknowledgedBy(m.Acknowledged, maintainer)
		m.Inbox = kind
		*messages = append(*messages, m)
	}
	return last, acked, more, rows.Err()
}

// Ack acknowledges the caller's own inbox through a sequence. Acknowledgement only moves
// forward; an earlier sequence leaves it unchanged.
func (s *Store) Ack(ctx context.Context, caller Key, through int64) (int64, error) {
	return s.ack(ctx, caller, through, false)
}

// AckParticipant acknowledges the inbox of the participant that the caller holds through
// a sequence, and records the caller as the session that made it. A caller that does not
// hold it is refused with ErrStaleHolder and nothing changes.
func (s *Store) AckParticipant(ctx context.Context, caller Key, through int64) (string, int64, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if through < 0 {
		return "", 0, ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return "", 0, err
	}
	address, err := heldAddress(ctx, tx.Tx, now, caller)
	if err != nil {
		return "", 0, err
	}
	if address == "" {
		return "", 0, ErrStaleHolder
	}
	if err := ensureParticipantInbox(ctx, tx.Tx, now, address); err != nil {
		return "", 0, tx.fail(err)
	}
	inbox := participantKey(address)
	var last, acked int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq,acked_through FROM sessions WHERE family=? AND id=?`,
		caller.Family, inbox).Scan(&last, &acked); err != nil {
		return "", 0, err
	}
	if through > last {
		return "", 0, ErrAckBeyondLast
	}
	if through > acked {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET acked_through=?,acked_by=? WHERE family=? AND id=?`,
			through, caller.Family+":"+caller.ID, caller.Family, inbox); err != nil {
			return "", 0, tx.fail(err)
		}
		acked = through
	}
	return address, acked, tx.Commit()
}

// ack acknowledges an inbox through a sequence. The dashboard acknowledges any existing
// inbox as the maintainer, also of an expired or retired session, and marks the newly
// acknowledged messages; a negative through means the newest sequence at this moment.
func (s *Store) ack(ctx context.Context, caller Key, through int64, dashboard bool) (int64, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if through < 0 && !dashboard {
		return 0, ErrInvalid
	}
	// Acknowledgement is progress; it may use the reserve.
	tx, err := s.begin(ctx, control)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	tx.audited = true
	if !dashboard {
		if err := active(ctx, tx.Tx, s.now().UnixMilli(), caller); err != nil {
			return 0, err
		}
	}
	var last, acked int64
	err = tx.QueryRowContext(ctx, `SELECT last_seq,acked_through FROM sessions WHERE family=? AND id=?`,
		caller.Family, caller.ID).Scan(&last, &acked)
	if dashboard && errors.Is(err, sql.ErrNoRows) {
		return 0, ErrMissing
	}
	if err != nil {
		return 0, err
	}
	if through < 0 {
		through = last
	}
	if through > last {
		return 0, ErrAckBeyondLast
	}
	if through > acked {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET acked_through=? WHERE family=? AND id=?`,
			through, caller.Family, caller.ID); err != nil {
			return 0, tx.fail(err)
		}
		if dashboard {
			// 0 becomes 1: format 4 stores both without a payload byte, so no page grows.
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET maintainer_ack=1 WHERE recipient_family=? AND recipient_id=?
				AND seq>? AND seq<=?`, caller.Family, caller.ID, acked, through); err != nil {
				return 0, tx.fail(err)
			}
		}
		acked = through
	}
	return acked, tx.Commit()
}

// MessageOutcome reports the delivery and acknowledgement state of a message the
// caller sent (#82). Another sender's message reads as not found.
func (s *Store) MessageOutcome(ctx context.Context, caller Key, id int64) (Outcome, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return Outcome{}, err
	}
	var result Outcome
	var maintainer bool
	err = tx.QueryRowContext(ctx, `SELECT m.id,COALESCE(n.name,''),m.seq,m.delivery_state,m.delivery_reason,
		m.delivery_updated_at,m.seq<=s.acked_through,m.maintainer_ack FROM messages m
		JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
		LEFT JOIN names n ON n.kind='peer' AND n.family=m.recipient_family AND n.session_id=m.recipient_id
		WHERE m.id=? AND m.sender_family=? AND m.sender_id=?`, id, caller.Family, caller.ID).
		Scan(&result.ID, &result.Recipient, &result.Seq, &result.DeliveryState, &result.DeliveryReason, &result.UpdatedAt, &result.Acknowledged, &maintainer)
	if errors.Is(err, sql.ErrNoRows) {
		// An ID that the daemon issued and no longer holds was deleted by retention or a
		// purge (#216); its outcome is the fixed state deleted, whichever session sent it.
		var issued int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM sqlite_sequence WHERE name='messages'`).Scan(&issued); err != nil {
			return Outcome{}, err
		}
		if id > 0 && id <= issued {
			if err := tx.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id=?`, id).Scan(new(int)); errors.Is(err, sql.ErrNoRows) {
				return Outcome{ID: id, DeliveryState: "deleted"}, nil
			}
		}
		return Outcome{}, ErrMessageNotFound
	}
	result.AcknowledgedBy = acknowledgedBy(result.Acknowledged, maintainer)
	return result, err
}

// SetDelivery records a message's delivery state. A failed state carries a reason and
// the others carry none, so each message has exactly one state. The wake adapters of
// chunk 04 are its callers.
func (s *Store) SetDelivery(ctx context.Context, id int64, state, reason string) error {
	switch {
	case state == "failed" && reason != "" && len(reason) <= 256:
	case (state == "waiting" || state == "notified" || state == "uncertain") && reason == "":
	default:
		return ErrInvalid
	}
	// Delivery state is progress on a stored message; it may use the reserve.
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE messages SET delivery_state=?,delivery_reason=?,delivery_updated_at=? WHERE id=?`,
		state, reason, s.now().UnixMilli(), id)
	if err != nil {
		return tx.fail(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.Join(err, ErrMessageNotFound)
	}
	return tx.Commit()
}
