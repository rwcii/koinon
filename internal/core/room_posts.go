package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Room posts (#217, chunk 02). A post is one canonical row with its room sequence, sender
// and typed origin; one ordinary transaction also stores an inbox copy for each active
// member in the roster snapshot, a delivery row for every member (queued or skipped), the
// allowance change and the poster's retry record. Wakes follow through the existing queue.

const (
	maxRoomPosts     = 10000 // retained posts, daemon-wide
	maxRoomReplays   = 10000 // retained retry records, daemon-wide
	maxRoomReadPosts = 100
	roomOriginHuman  = "maintainer"
	roomOriginMember = "member"
	roomEventPost    = "post"
)

var (
	ErrExchangeLimit  = Refusal{"exchange_limit", "the room's agent-post allowance is used up until the maintainer posts again"}
	ErrRoomPostLimit  = Refusal{"room_post_limit", fmt.Sprintf("the daemon already retains %d room posts", maxRoomPosts)}
	ErrRoomRetryLimit = Refusal{"room_replay_limit", fmt.Sprintf("the daemon already retains %d room post retry records", maxRoomReplays)}
	ErrRoomPostRepeat = Refusal{"idempotency_conflict", "this room post key is bound to a different request"}
	ErrRoomPostLate   = Refusal{"retry_deadline_expired", "the retry deadline has passed; read the room to reconcile"}
)

// RoomPostResult is a poster's receipt: its post's sequence and how many members it
// reached or skipped, never who they are.
type RoomPostResult struct {
	RoomID    string `json:"room_id"`
	Seq       int64  `json:"seq"`
	Queued    int    `json:"queued"`
	Skipped   int    `json:"skipped"`
	Allowance int64  `json:"allowance"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// RoomPost is one post as room_read and the dashboard transcript show it.
type RoomPost struct {
	Seq          int64  `json:"seq"`
	SenderFamily string `json:"sender_family"`
	SenderName   string `json:"sender_name"`
	Origin       string `json:"origin"`
	Body         string `json:"body"`
	CreatedAt    int64  `json:"created_at"`
}

// RoomGap is a range of room sequences, inclusive, that retention no longer holds.
type RoomGap struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// RoomTranscript is one page of a room's retained posts after a sequence.
type RoomTranscript struct {
	RoomID    string     `json:"room_id"`
	Start     int64      `json:"start"`
	Head      int64      `json:"head"`
	Posts     []RoomPost `json:"posts"`
	Gaps      []RoomGap  `json:"gaps"`
	More      bool       `json:"more"`
	NextAfter *int64     `json:"next_after,omitempty"`
}

func roomPostFingerprint(roomID, body string, deadline float64) string {
	sum := sha256.Sum256([]byte(roomID + "\x00" + strconv.FormatFloat(deadline, 'g', -1, 64) + "\x00" + body))
	return hex.EncodeToString(sum[:])
}

// RoomPost posts body to a room as the caller, a current active member. Each new post uses
// one unit of the room's allowance.
func (s *Store) RoomPost(ctx context.Context, caller Key, roomID, body, key string, deadline float64) (RoomPostResult, error) {
	return s.roomPost(ctx, caller, false, roomID, body, key, deadline)
}

// DashboardRoomPost posts body as the maintainer from the dashboard: it reaches every active
// member and sets the allowance to the room's refill setting.
func (s *Store) DashboardRoomPost(ctx context.Context, roomID, body, key string, deadline float64) (RoomPostResult, error) {
	return s.roomPost(ctx, maintainerKey, true, roomID, body, key, deadline)
}

// roomPost takes maintainer origin only from DashboardRoomPost; on the agent path the caller
// must be a valid native session, so the maintainer's key is refused there.
func (s *Store) roomPost(ctx context.Context, caller Key, maintainer bool, roomID, body, key string, deadline float64) (RoomPostResult, error) {
	if !maintainer && !validKey(caller.Family, caller.ID) {
		return RoomPostResult{}, ErrInvalid
	}
	if !validRoomID(roomID) || body == "" || len(body) > maxBody || !utf8.ValidString(body) || key == "" || len(key) > 256 ||
		math.IsNaN(deadline) || math.IsInf(deadline, 0) {
		return RoomPostResult{}, ErrInvalid
	}
	clock := s.clock()
	if deadline <= clock {
		return RoomPostResult{}, ErrRoomPostLate
	}
	if deadline > clock+s.limits().idemTTL {
		return RoomPostResult{}, Refusal{"invalid_request", fmt.Sprintf("the retry deadline is beyond the %.0f s horizon", s.limits().idemTTL)}
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return RoomPostResult{}, err
	}
	defer tx.Rollback()
	tx.audited = maintainer
	now := s.now().UnixMilli()
	// The caller's current membership is checked before its retry record: an accepted
	// earlier post grants no post after removal.
	var senders []Key
	if !maintainer {
		if err := active(ctx, tx.Tx, now, caller); err != nil {
			return RoomPostResult{}, err
		}
		if senders, err = callerMemberships(ctx, tx.Tx, now, caller); err != nil {
			return RoomPostResult{}, err
		}
		if _, err := currentMembershipStart(ctx, tx.Tx, roomID, senders); err != nil {
			return RoomPostResult{}, err
		}
	}
	print := roomPostFingerprint(roomID, body, deadline)
	var stored, result string
	err = tx.QueryRowContext(ctx, `SELECT fingerprint,result FROM room_replays WHERE family=? AND caller_id=? AND key=?`,
		caller.Family, caller.ID, key).Scan(&stored, &result)
	if err == nil {
		if stored != print {
			return RoomPostResult{}, ErrRoomPostRepeat
		}
		var original RoomPostResult
		if err := json.Unmarshal([]byte(result), &original); err != nil {
			return RoomPostResult{}, err
		}
		// A repeat changes nothing: the transaction is not committed, so it writes no audit.
		original.Duplicate = true
		return original, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RoomPostResult{}, err
	}
	var name string
	var refill, allowance, head int64
	err = tx.QueryRowContext(ctx, `SELECT name,refill,allowance,post_head FROM rooms WHERE room_id=?`, roomID).Scan(&name, &refill, &allowance, &head)
	if errors.Is(err, sql.ErrNoRows) {
		return RoomPostResult{}, ErrRoomNotFound
	}
	if err != nil {
		return RoomPostResult{}, err
	}
	origin, sender := roomOriginHuman, "maintainer"
	if maintainer {
		// PDR 005, Decision 2: a maintainer post sets the allowance to the refill setting
		// in force now; it never adds to what is left.
		allowance = refill
	} else {
		if allowance == 0 {
			return RoomPostResult{}, ErrExchangeLimit
		}
		allowance--
		origin = roomOriginMember
		if sender, err = senderName(ctx, tx.Tx, now, caller); err != nil {
			return RoomPostResult{}, err
		}
	}
	var posts, replays int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM room_posts),(SELECT COUNT(*) FROM room_replays)`).Scan(&posts, &replays); err != nil {
		return RoomPostResult{}, err
	}
	if posts >= maxRoomPosts {
		return RoomPostResult{}, ErrRoomPostLimit
	}
	if replays >= maxRoomReplays {
		return RoomPostResult{}, ErrRoomRetryLimit
	}
	seq := head + 1
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET post_head=?,allowance=? WHERE room_id=?`, seq, allowance, roomID); err != nil {
		return RoomPostResult{}, tx.fail(err)
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO room_posts(room_id,seq,sender_family,sender_id,sender_name,origin,body,created_at)
		VALUES (?,?,?,?,?,?,?,?)`, roomID, seq, caller.Family, caller.ID, sender, origin, body, now)
	if err != nil {
		return RoomPostResult{}, tx.fail(err)
	}
	postID, err := inserted.LastInsertId()
	if err != nil {
		return RoomPostResult{}, err
	}
	receipt := RoomPostResult{RoomID: roomID, Seq: seq, Allowance: allowance}
	if err := s.fanOut(ctx, tx, roomID, postID, seq, caller, sender, origin, body, senders, &receipt); err != nil {
		return RoomPostResult{}, err
	}
	data, _ := json.Marshal(receipt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_replays(family,caller_id,key,fingerprint,deadline,result) VALUES (?,?,?,?,?,?)`,
		caller.Family, caller.ID, key, print, deadline, string(data)); err != nil {
		return RoomPostResult{}, tx.fail(err)
	}
	setAuditTarget(ctx, fmt.Sprintf("room %s post %d bytes %d queued %d skipped %d", roomID, seq, len(body), receipt.Queued, receipt.Skipped))
	return receipt, tx.Commit()
}

// senderName is the name a member posts under: the address it holds, else its peer name.
func senderName(ctx context.Context, tx *sql.Tx, now int64, caller Key) (string, error) {
	name, err := heldAddress(ctx, tx, now, caller)
	if err != nil || name != "" {
		return name, err
	}
	err = tx.QueryRowContext(ctx, `SELECT name FROM names WHERE kind='peer' AND family=? AND session_id=?`, caller.Family, caller.ID).Scan(&name)
	return name, err
}

// fanOut snapshots the room's current members and gives each one, except the poster's own
// memberships, an inbox copy or a skipped outcome.
func (s *Store) fanOut(ctx context.Context, tx *writeTx, roomID string, postID, seq int64, caller Key, sender, origin, body string,
	exclude []Key, receipt *RoomPostResult) error {
	now := s.now().UnixMilli()
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,family,member_id,label FROM room_members WHERE room_id=? AND active=1 ORDER BY ordinal`, roomID)
	if err != nil {
		return err
	}
	type member struct {
		ordinal int64
		key     Key
		label   string
	}
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ordinal, &m.key.Family, &m.key.ID, &m.label); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	if err := rows.Close(); err != nil {
		return err
	}
next:
	for _, m := range members {
		for _, k := range exclude {
			if m.key == k {
				continue next
			}
		}
		reason := ""
		if isParticipantID(m.key.ID) {
			address := m.key.ID[len(participantPrefix):]
			state, err := participantState(ctx, tx.Tx, now, address)
			if err != nil {
				return err
			}
			if state != roomStateActive {
				reason = state
			} else if err := ensureParticipantInbox(ctx, tx.Tx, now, address); err != nil {
				return tx.fail(err)
			}
		}
		var messageID sql.NullInt64
		if reason == "" {
			var inboxSeq int64
			err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=?
				AND retired_at=0 AND expires_at>? RETURNING last_seq`, m.key.Family, m.key.ID, now).Scan(&inboxSeq)
			if errors.Is(err, sql.ErrNoRows) {
				reason = roomStateInactive
			} else if err != nil {
				return err
			} else {
				result, err := tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,
					body,created_at,delivery_updated_at,room_id,room_event,room_seq,room_origin) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
					m.key.Family, m.key.ID, inboxSeq, caller.Family, caller.ID, sender, body, now, now, roomID, roomEventPost, seq, origin)
				if err != nil {
					return tx.fail(err)
				}
				id, err := result.LastInsertId()
				if err != nil {
					return err
				}
				messageID = sql.NullInt64{Int64: id, Valid: true}
			}
		}
		outcome := "queued"
		if reason != "" {
			outcome = "skipped"
			receipt.Skipped++
		} else {
			receipt.Queued++
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO room_deliveries(post_id,ordinal,family,member_id,label,outcome,reason,message_id)
			VALUES (?,?,?,?,?,?,?,?)`, postID, m.ordinal, m.key.Family, m.key.ID, m.label, outcome, reason, messageID); err != nil {
			return tx.fail(err)
		}
	}
	return nil
}

// currentMembershipStart is the exclusive lower bound of the caller's current membership in
// the room: the lowest start of its current rows, by exact session or held address.
func currentMembershipStart(ctx context.Context, tx *sql.Tx, roomID string, keys []Key) (int64, error) {
	start := int64(-1)
	for _, k := range keys {
		var s int64
		err := tx.QueryRowContext(ctx, `SELECT start_seq FROM room_members WHERE room_id=? AND family=? AND member_id=? AND active=1`,
			roomID, k.Family, k.ID).Scan(&s)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if start < 0 || s < start {
			start = s
		}
	}
	if start < 0 {
		return 0, ErrNotRoomMember
	}
	return start, nil
}

// RoomRead pages the room's retained posts after the caller's membership start, in one read
// snapshot with the membership. It moves no inbox read or acknowledgement marker.
func (s *Store) RoomRead(ctx context.Context, caller Key, roomID string, after int64, limit int) (RoomTranscript, error) {
	if limit == 0 {
		limit = maxRoomReadPosts
	}
	if limit < 0 || limit > maxRoomReadPosts || after < 0 || !validRoomID(roomID) {
		return RoomTranscript{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RoomTranscript{}, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return RoomTranscript{}, err
	}
	keys, err := callerMemberships(ctx, tx, now, caller)
	if err != nil {
		return RoomTranscript{}, err
	}
	start, err := currentMembershipStart(ctx, tx, roomID, keys)
	if err != nil {
		return RoomTranscript{}, err
	}
	return roomTranscript(ctx, tx, roomID, start, max(after, start), limit)
}

// roomTranscript reads posts after from, at most limit of them and about one MiB of bodies,
// and names the sequences in the page's range that retention no longer holds.
func roomTranscript(ctx context.Context, tx *sql.Tx, roomID string, start, from int64, limit int) (RoomTranscript, error) {
	t := RoomTranscript{RoomID: roomID, Start: start, Posts: []RoomPost{}, Gaps: []RoomGap{}}
	if err := tx.QueryRowContext(ctx, `SELECT post_head FROM rooms WHERE room_id=?`, roomID).Scan(&t.Head); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, ErrRoomNotFound
		}
		return t, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT seq,sender_family,sender_name,origin,body,created_at FROM room_posts
		WHERE room_id=? AND seq>? ORDER BY seq LIMIT ?`, roomID, from, limit+1)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var p RoomPost
		if err := rows.Scan(&p.Seq, &p.SenderFamily, &p.SenderName, &p.Origin, &p.Body, &p.CreatedAt); err != nil {
			return t, err
		}
		if len(t.Posts) == limit || len(t.Posts) > 0 && size+len(p.Body) > 1<<20 {
			t.More = true
			break
		}
		size += len(p.Body)
		t.Posts = append(t.Posts, p)
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	end := t.Head
	if t.More {
		last := t.Posts[len(t.Posts)-1].Seq
		end, t.NextAfter = last, &last
	}
	previous := from
	for _, p := range t.Posts {
		if p.Seq > previous+1 {
			t.Gaps = append(t.Gaps, RoomGap{previous + 1, p.Seq - 1})
		}
		previous = p.Seq
	}
	if end > previous {
		t.Gaps = append(t.Gaps, RoomGap{previous + 1, end})
	}
	return t, nil
}

// cullRoomPosts deletes, in bounded control writes, the posts whose inbox copies are all
// gone, the posts without copies after the message retention period, their delivery rows,
// and expired retry records. It writes no notice or audit record.
func (s *Store) cullRoomPosts(ctx context.Context, budget *int64) error {
	messages, _ := s.retentionPeriods()
	for *budget > 0 {
		batch := min(int64(cullBatch), *budget)
		tx, err := s.begin(ctx, control)
		if err != nil {
			return err
		}
		cutoff := s.now().Add(-messages).UnixMilli()
		rows, err := tx.QueryContext(ctx, `SELECT p.post_id FROM room_posts p WHERE NOT EXISTS (SELECT 1 FROM room_deliveries d
			JOIN messages m ON m.id=d.message_id WHERE d.post_id=p.post_id) AND (p.created_at<? OR EXISTS (SELECT 1 FROM
			room_deliveries d WHERE d.post_id=p.post_id AND d.message_id IS NOT NULL)) ORDER BY p.post_id LIMIT ?`, cutoff, batch)
		if err != nil {
			tx.Rollback()
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				tx.Rollback()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			tx.Rollback()
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM room_deliveries WHERE post_id=?`, id); err != nil {
				tx.Rollback()
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM room_posts WHERE post_id=?`, id); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		*budget -= int64(len(ids))
		if int64(len(ids)) < batch {
			break
		}
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM room_replays WHERE rowid IN (SELECT rowid FROM room_replays WHERE deadline<? LIMIT ?)`,
		s.clock(), cullBatch); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
