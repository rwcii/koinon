package core

import (
	"context"
	"database/sql"
	"errors"
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
}

type Inbox struct {
	Messages     []Message `json:"messages"`
	LastSeq      int64     `json:"last_seq"`
	AckedThrough int64     `json:"acked_through"`
	More         bool      `json:"more"`
}

type Outcome struct {
	ID             int64  `json:"id"`
	Recipient      string `json:"recipient"`
	Seq            int64  `json:"seq"`
	DeliveryState  string `json:"delivery_state"`
	DeliveryReason string `json:"delivery_reason,omitempty"`
	UpdatedAt      int64  `json:"updated_at"`
	Acknowledged   bool   `json:"acknowledged"`
}

// active confirms in tx that the caller is an active session.
func active(ctx context.Context, tx *sql.Tx, now int64, caller Key) error {
	if !validKey(caller.Family, caller.ID) {
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
	peers := make([]Peer, 0, len(items))
	for _, item := range items {
		peers = append(peers, Peer{Name: item.Name, Alias: item.Alias, Family: item.Family, State: item.State, Repository: item.Repository})
	}
	return peers, truncated, nil
}

// Send stores one message for the session that a peer name or a held alias names. The
// sequence number and the message are written in one transaction, so a crash leaves
// either both or neither, and sequence numbers stay gapless.
func (s *Store) Send(ctx context.Context, caller Key, to, body string) (Outcome, error) {
	if to == "" || len(to) > 256 || body == "" || len(body) > maxBody || !utf8.ValidString(body) {
		return Outcome{}, ErrInvalid
	}
	now := s.now().UnixMilli()
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx.Tx, now, caller); err != nil {
		return Outcome{}, err
	}
	var kind, family, sessionID, repository, holder string
	err = tx.QueryRowContext(ctx, `SELECT kind,family,session_id,repository,holder_id FROM names WHERE name=?`, to).
		Scan(&kind, &family, &sessionID, &repository, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrPeerNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	if kind == "alias" {
		held := false
		if holder != "" {
			if held, err = holds(ctx, tx.Tx, now, family, holder, repository); err != nil {
				return Outcome{}, err
			}
		}
		if !held {
			return Outcome{}, ErrAliasUnheld
		}
		sessionID = holder
	}
	var sender string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`,
		caller.Family, caller.ID).Scan(&sender); err != nil {
		return Outcome{}, err
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
	// The outcome names the receiving session by its peer name, also for a send to an alias.
	if err := tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`,
		family, sessionID).Scan(&to); err != nil {
		return Outcome{}, err
	}
	return Outcome{ID: id, Recipient: to, Seq: seq, DeliveryState: "waiting", UpdatedAt: now}, tx.Commit()
}

// ReadInbox returns the caller's own messages after a sequence, at most limit of them
// and about one MiB of bodies, so a page always fits a bounded response.
func (s *Store) ReadInbox(ctx context.Context, caller Key, after, limit int64) (Inbox, error) {
	if limit == 0 {
		limit = 50
	}
	if after < 0 || limit < 1 || limit > 100 {
		return Inbox{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Inbox{}, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx, s.now().UnixMilli(), caller); err != nil {
		return Inbox{}, err
	}
	result := Inbox{Messages: []Message{}}
	if err := tx.QueryRowContext(ctx, `SELECT last_seq,acked_through FROM sessions WHERE family=? AND id=?`,
		caller.Family, caller.ID).Scan(&result.LastSeq, &result.AckedThrough); err != nil {
		return Inbox{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,sender_family,sender_name,body,created_at,delivery_state,delivery_reason
		FROM messages WHERE recipient_family=? AND recipient_id=? AND seq>? ORDER BY seq LIMIT ?`,
		caller.Family, caller.ID, after, limit+1)
	if err != nil {
		return Inbox{}, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Seq, &m.SenderFamily, &m.SenderName, &m.Body, &m.CreatedAt, &m.DeliveryState, &m.DeliveryReason); err != nil {
			return Inbox{}, err
		}
		if int64(len(result.Messages)) == limit || (len(result.Messages) > 0 && size+len(m.Body) > 1<<20) {
			result.More = true
			break
		}
		size += len(m.Body)
		m.Acknowledged = m.Seq <= result.AckedThrough
		result.Messages = append(result.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return Inbox{}, err
	}
	return result, nil
}

// Ack acknowledges the caller's own inbox through a sequence. Acknowledgement only moves
// forward; an earlier sequence leaves it unchanged.
func (s *Store) Ack(ctx context.Context, caller Key, through int64) (int64, error) {
	s.wake.mu.Lock()
	defer s.wake.mu.Unlock()
	if through < 0 {
		return 0, ErrInvalid
	}
	// Acknowledgement is progress; it may use the reserve.
	tx, err := s.begin(ctx, control)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := active(ctx, tx.Tx, s.now().UnixMilli(), caller); err != nil {
		return 0, err
	}
	var last, acked int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq,acked_through FROM sessions WHERE family=? AND id=?`,
		caller.Family, caller.ID).Scan(&last, &acked); err != nil {
		return 0, err
	}
	if through > last {
		return 0, ErrAckBeyondLast
	}
	if through > acked {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET acked_through=? WHERE family=? AND id=?`,
			through, caller.Family, caller.ID); err != nil {
			return 0, tx.fail(err)
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
	err = tx.QueryRowContext(ctx, `SELECT m.id,COALESCE(n.name,''),m.seq,m.delivery_state,m.delivery_reason,
		m.delivery_updated_at,m.seq<=s.acked_through FROM messages m
		JOIN sessions s ON s.family=m.recipient_family AND s.id=m.recipient_id
		LEFT JOIN names n ON n.kind='peer' AND n.family=m.recipient_family AND n.session_id=m.recipient_id
		WHERE m.id=? AND m.sender_family=? AND m.sender_id=?`, id, caller.Family, caller.ID).
		Scan(&result.ID, &result.Recipient, &result.Seq, &result.DeliveryState, &result.DeliveryReason, &result.UpdatedAt, &result.Acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, ErrMessageNotFound
	}
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
