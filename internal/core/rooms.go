package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// Dashboard rooms (#217, chunk 01): rooms, their membership and membership notices. The
// maintainer creates a room and changes its roster in the dashboard; members read their
// rooms and the roster through room_list and room_members. A member is the participant
// address that the selected session holds, otherwise that exact native session. Posts are
// chunk 02; the membership start is stored for it.

const (
	maxRooms           = 64
	maxRoomMembers     = 32  // active members of one room
	maxRoomMemberRows  = 128 // current and former membership rows of one room
	maxRoomNameBytes   = 128
	maxRoomPage        = 100
	roomEventAdded     = "added"
	roomEventRemoved   = "removed"
	roomMemberSession  = "session"
	roomMemberAddress  = "participant"
	roomStateActive    = "active"
	roomStateInactive  = "inactive"
	roomStateUnheld    = "unheld"
	roomStateConflicts = "conflicting"
)

var (
	ErrRoomNotFound     = Refusal{"room_not_found", "no room has that ID"}
	ErrRoomLimit        = Refusal{"room_limit", fmt.Sprintf("the daemon already has %d rooms", maxRooms)}
	ErrRoomMemberLimit  = Refusal{"room_member_limit", fmt.Sprintf("the room already has %d active members", maxRoomMembers)}
	ErrRoomHistoryLimit = Refusal{"room_history_limit", fmt.Sprintf("the room already has %d current and former memberships", maxRoomMemberRows)}
	ErrRoomKeyConflict  = Refusal{"idempotency_conflict", "that form key created a room with another name"}
	ErrRoomMemberAbsent = Refusal{"room_member_not_found", "the room has no such current member"}
	// ErrNotRoomMember answers a roster query from a caller that is not a current member,
	// also for an unknown room, so a query reveals no other room.
	ErrNotRoomMember = Refusal{"not_room_member", "this session is not a current member of that room"}
)

// RoomEvent is the typed room metadata of an inbox message: a membership notice from a
// dashboard change, or a copy of a room post. Message body text never supplies it.
type RoomEvent struct {
	RoomID string `json:"room_id"`
	// Revision is the roster revision of a membership change (added or removed).
	Revision int64  `json:"revision,omitempty"`
	Event    string `json:"event"`
	// Seq and Origin are a post's room sequence and typed origin, maintainer or member
	// (chunk 02); the origin comes from the posting path, never from the body.
	Seq    int64  `json:"seq,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// Room is one room as the dashboard shows it, with its current and former members.
type Room struct {
	ID        string       `json:"room_id"`
	Name      string       `json:"name"`
	Revision  int64        `json:"revision"`
	CreatedAt int64        `json:"created_at"`
	Members   []RoomMember `json:"members"`
}

// RoomMember is one membership row. It names a participant by its address and a native
// member by its peer name; it never carries a native ID or wake target.
type RoomMember struct {
	Ordinal  int64  `json:"ordinal"`
	Kind     string `json:"member"`
	Family   string `json:"family"`
	Address  string `json:"address,omitempty"`
	PeerName string `json:"peer_name,omitempty"`
	State    string `json:"state"`
	Current  bool   `json:"current"`
	Revision int64  `json:"revision"`
}

// RoomMembership is one room in a caller's room_list.
type RoomMembership struct {
	RoomID   string `json:"room_id"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	// Status is member or removed; Via is participant or session, with the address for a
	// participant membership.
	Status             string `json:"status"`
	Via                string `json:"via"`
	Address            string `json:"address,omitempty"`
	MembershipRevision int64  `json:"membership_revision"`
}

func validRoomName(name string) bool {
	return name != "" && len(name) <= maxRoomNameBytes && utf8.ValidString(name)
}

func validRoomID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// CreateRoom creates an empty room. key is the form's idempotency key: a repeat with the
// name that it first sent returns the room it created, and another name is refused. The
// rooms table keeps its refill and allowance columns, which nothing reads since #493.
func (s *Store) CreateRoom(ctx context.Context, key, name string) (string, error) {
	if !validRoomName(name) || key == "" || len(key) > 64 {
		return "", ErrInvalid
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	tx.audited = true
	var existing, existingName string
	err = tx.QueryRowContext(ctx, `SELECT room_id,name FROM rooms WHERE create_key=?`, key).Scan(&existing, &existingName)
	if err == nil {
		if existingName != name {
			return "", ErrRoomKeyConflict
		}
		setAuditTarget(ctx, "room "+existing+" create repeated")
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var rooms int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM rooms`).Scan(&rooms); err != nil {
		return "", err
	}
	if rooms >= maxRooms {
		return "", ErrRoomLimit
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rooms(room_id,name,revision,refill,allowance,post_head,created_at,create_key,create_refill)
		VALUES (?,?,1,0,0,0,?,?,0)`, id, name, s.now().UnixMilli(), key); err != nil {
		return "", tx.fail(err)
	}
	setAuditTarget(ctx, "room "+id+" name bytes "+strconv.Itoa(len(name)))
	return id, tx.Commit()
}

// AddRoomMember adds the active session with peer name to a room: as the participant whose
// address it holds, otherwise as that exact session. It reports whether the roster changed;
// adding a current member changes nothing. The roster revision, membership, notice to the
// member and audit commit together or not at all.
func (s *Store) AddRoomMember(ctx context.Context, roomID, peerName string) (bool, error) {
	if !validRoomID(roomID) || peerName == "" || len(peerName) > 256 {
		return false, ErrInvalid
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	room, err := roomRow(ctx, tx.Tx, roomID)
	if err != nil {
		return false, err
	}
	var session Key
	err = tx.QueryRowContext(ctx, `SELECT family,session_id FROM names WHERE kind='peer' AND name=?`, peerName).Scan(&session.Family, &session.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (session == maintainerKey || isParticipantID(session.ID)) {
		return false, ErrPeerNotFound
	}
	if err != nil {
		return false, err
	}
	if err := active(ctx, tx.Tx, now, session); err != nil {
		if errors.Is(err, ErrCallerInactive) {
			return false, ErrRecipientInactive
		}
		return false, err
	}
	member, kind, label := session, roomMemberSession, peerName
	address, err := heldAddress(ctx, tx.Tx, now, session)
	if err != nil {
		return false, err
	}
	if address != "" {
		member, kind, label = Key{session.Family, participantKey(address)}, roomMemberAddress, address
	}
	var current bool
	var ordinal int64
	err = tx.QueryRowContext(ctx, `SELECT active,ordinal FROM room_members WHERE room_id=? AND family=? AND member_id=?`,
		roomID, member.Family, member.ID).Scan(&current, &ordinal)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if current {
		setAuditTarget(ctx, "room "+roomID+" add "+peerName+" as "+kind+" "+label+" unchanged")
		return false, tx.Commit()
	}
	var activeMembers, rows int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(active),0),COUNT(*) FROM room_members WHERE room_id=?`, roomID).Scan(&activeMembers, &rows); err != nil {
		return false, err
	}
	if activeMembers >= maxRoomMembers {
		return false, ErrRoomMemberLimit
	}
	if !exists && rows >= maxRoomMemberRows {
		return false, ErrRoomHistoryLimit
	}
	revision := room.Revision + 1
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET revision=? WHERE room_id=?`, revision, roomID); err != nil {
		return false, tx.fail(err)
	}
	// An initial add or a re-add starts the membership at the room's current post head.
	if exists {
		_, err = tx.ExecContext(ctx, `UPDATE room_members SET active=1,revision=?,start_seq=?,label=?,changed_at=?
			WHERE room_id=? AND ordinal=?`, revision, room.PostHead, label, now, roomID, ordinal)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO room_members(room_id,ordinal,family,member_id,label,active,revision,start_seq,changed_at)
			VALUES (?,(SELECT COALESCE(MAX(ordinal),0)+1 FROM room_members WHERE room_id=?),?,?,?,1,?,?,?)`,
			roomID, roomID, member.Family, member.ID, label, revision, room.PostHead, now)
	}
	if err != nil {
		return false, tx.fail(err)
	}
	if err := s.roomNotice(ctx, tx, room, member, revision, roomEventAdded); err != nil {
		return false, err
	}
	setAuditTarget(ctx, "room "+roomID+" add "+peerName+" as "+kind+" "+label+" revision "+strconv.FormatInt(revision, 10))
	return true, tx.Commit()
}

// RemoveRoomMember ends the current membership with ordinal. The row stays, so the former
// member's room_list reports the removal; an active recipient also gets a notice.
func (s *Store) RemoveRoomMember(ctx context.Context, roomID string, ordinal int64) error {
	if !validRoomID(roomID) || ordinal <= 0 {
		return ErrInvalid
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.audited = true
	room, err := roomRow(ctx, tx.Tx, roomID)
	if err != nil {
		return err
	}
	var member Key
	var label string
	err = tx.QueryRowContext(ctx, `SELECT family,member_id,label FROM room_members WHERE room_id=? AND ordinal=? AND active=1`,
		roomID, ordinal).Scan(&member.Family, &member.ID, &label)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRoomMemberAbsent
	}
	if err != nil {
		return err
	}
	revision := room.Revision + 1
	if _, err := tx.ExecContext(ctx, `UPDATE rooms SET revision=? WHERE room_id=?`, revision, roomID); err != nil {
		return tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE room_members SET active=0,revision=?,changed_at=? WHERE room_id=? AND ordinal=?`,
		revision, s.now().UnixMilli(), roomID, ordinal); err != nil {
		return tx.fail(err)
	}
	if err := s.roomNotice(ctx, tx, room, member, revision, roomEventRemoved); err != nil {
		return err
	}
	setAuditTarget(ctx, "room "+roomID+" remove "+label+" revision "+strconv.FormatInt(revision, 10))
	return tx.Commit()
}

type roomRecord struct {
	ID       string
	Name     string
	Revision int64
	PostHead int64
}

func roomRow(ctx context.Context, tx *sql.Tx, roomID string) (roomRecord, error) {
	r := roomRecord{ID: roomID}
	err := tx.QueryRowContext(ctx, `SELECT name,revision,post_head FROM rooms WHERE room_id=?`, roomID).Scan(&r.Name, &r.Revision, &r.PostHead)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrRoomNotFound
	}
	return r, err
}

// roomNotice queues one maintainer notice of a membership change for the member when its
// recipient is active now: the participant's inbox while a session holds the address, or
// the exact native session. An inactive member learns the change from its next room_list.
func (s *Store) roomNotice(ctx context.Context, tx *writeTx, room roomRecord, member Key, revision int64, event string) error {
	now := s.now().UnixMilli()
	if isParticipantID(member.ID) {
		address := member.ID[len(participantPrefix):]
		state, err := participantState(ctx, tx.Tx, now, address)
		if err != nil || state != roomStateActive {
			return err
		}
		if err := ensureParticipantInbox(ctx, tx.Tx, now, address); err != nil {
			return tx.fail(err)
		}
	}
	var seq int64
	err := tx.QueryRowContext(ctx, `UPDATE sessions SET last_seq=last_seq+1 WHERE family=? AND id=?
		AND retired_at=0 AND expires_at>? RETURNING last_seq`, member.Family, member.ID, now).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	roomID := room.ID
	name := strconv.Quote(room.Name)
	body := "The maintainer added this session to the Koinon room " + name + " (" + roomID + ", revision " +
		strconv.FormatInt(revision, 10) + "). Read its roster with room_members."
	if event == roomEventRemoved {
		body = "The maintainer removed this session from the Koinon room " + name + " (" + roomID + ", revision " +
			strconv.FormatInt(revision, 10) + ")."
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,
		created_at,delivery_updated_at,room_id,room_revision,room_event) VALUES (?,?,?,?,?,'maintainer',?,?,?,?,?,?)`,
		member.Family, member.ID, seq, maintainerKey.Family, maintainerKey.ID, body, now, now, roomID, revision, event)
	if err != nil {
		return tx.fail(err)
	}
	return nil
}

// participantState is active while an active qualifier holds the address, conflicting while
// the maintainer's holder choice is needed, and unheld otherwise.
func participantState(ctx context.Context, tx *sql.Tx, now int64, address string) (string, error) {
	var family, holder, conflict, repository, role string
	err := tx.QueryRowContext(ctx, `SELECT family,holder_id,conflict,repository,role FROM names WHERE kind='alias' AND name=?`, address).
		Scan(&family, &holder, &conflict, &repository, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return roomStateUnheld, nil
	}
	if err != nil {
		return "", err
	}
	if conflict != "" {
		return roomStateConflicts, nil
	}
	if holder == "" {
		return roomStateUnheld, nil
	}
	held, err := holds(ctx, tx, now, family, holder, repository, role)
	if err != nil || !held {
		return roomStateUnheld, err
	}
	return roomStateActive, nil
}

// memberView turns a membership row into its displayed form: a participant by address and
// its holder's peer name, a native member by its peer name, with its current state.
func memberView(ctx context.Context, tx *sql.Tx, now int64, family, memberID, label string) (RoomMember, error) {
	m := RoomMember{Family: family}
	if isParticipantID(memberID) {
		m.Kind, m.Address = roomMemberAddress, memberID[len(participantPrefix):]
		state, err := participantState(ctx, tx, now, m.Address)
		if err != nil {
			return m, err
		}
		m.State = state
		if state == roomStateActive {
			err := tx.QueryRowContext(ctx, `SELECT p.name FROM names a JOIN names p ON p.kind='peer' AND p.family=a.family AND p.session_id=a.holder_id
				WHERE a.kind='alias' AND a.name=?`, m.Address).Scan(&m.PeerName)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return m, err
			}
		}
		return m, nil
	}
	m.Kind, m.PeerName, m.State = roomMemberSession, label, roomStateInactive
	if err := active(ctx, tx, now, Key{family, memberID}); err == nil {
		m.State = roomStateActive
	} else if !errors.Is(err, ErrCallerInactive) && !errors.Is(err, ErrInvalid) {
		return m, err
	}
	return m, nil
}

// callerMemberships are the membership keys of a caller: its exact native session and the
// participant whose address it holds now. A fenced or replaced former holder holds none.
func callerMemberships(ctx context.Context, tx *sql.Tx, now int64, caller Key) ([]Key, error) {
	keys := []Key{caller}
	address, err := heldAddress(ctx, tx, now, caller)
	if err != nil {
		return nil, err
	}
	if address != "" {
		keys = append(keys, Key{caller.Family, participantKey(address)})
	}
	return keys, nil
}

func roomPageLimit(limit int) (int, error) {
	if limit < 0 || limit > maxRoomPage {
		return 0, ErrInvalid
	}
	if limit == 0 {
		return maxRoomPage, nil
	}
	return limit, nil
}

// RoomList returns the rooms in which the caller has a current or former membership, by
// room ID after after. A current membership is reported in preference to a former one.
func (s *Store) RoomList(ctx context.Context, caller Key, after string, limit int) ([]RoomMembership, bool, error) {
	limit, err := roomPageLimit(limit)
	if err != nil || len(after) > 32 {
		return nil, false, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return nil, false, err
	}
	keys, err := callerMemberships(ctx, tx, now, caller)
	if err != nil {
		return nil, false, err
	}
	participant := Key{}
	if len(keys) > 1 {
		participant = keys[1]
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.room_id,r.name,r.revision,m.member_id,m.active,m.revision FROM room_members m
		JOIN rooms r ON r.room_id=m.room_id
		WHERE ((m.family=? AND m.member_id=?) OR (m.family=? AND m.member_id=?)) AND r.room_id>?
		ORDER BY r.room_id, m.active DESC, m.member_id`, caller.Family, caller.ID, participant.Family, participant.ID, after)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var list []RoomMembership
	more := false
	for rows.Next() {
		var r RoomMembership
		var memberID string
		var current bool
		if err := rows.Scan(&r.RoomID, &r.Name, &r.Revision, &memberID, &current, &r.MembershipRevision); err != nil {
			return nil, false, err
		}
		if n := len(list); n > 0 && list[n-1].RoomID == r.RoomID {
			continue
		}
		if len(list) == limit {
			more = true
			break
		}
		r.Status, r.Via = "removed", roomMemberSession
		if current {
			r.Status = "member"
		}
		if isParticipantID(memberID) {
			r.Via, r.Address = roomMemberAddress, memberID[len(participantPrefix):]
		}
		list = append(list, r)
	}
	return list, more, rows.Err()
}

// RoomMembers returns the current roster of a room, by ordinal after after, to a caller that
// is a current member: the exact native member or the current holder of a member address.
func (s *Store) RoomMembers(ctx context.Context, caller Key, roomID string, after int64, limit int) (Room, bool, error) {
	limit, err := roomPageLimit(limit)
	if err != nil || after < 0 || !validRoomID(roomID) {
		return Room{}, false, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, false, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	if err := active(ctx, tx, now, caller); err != nil {
		return Room{}, false, err
	}
	keys, err := callerMemberships(ctx, tx, now, caller)
	if err != nil {
		return Room{}, false, err
	}
	member := false
	for _, k := range keys {
		var found int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM room_members WHERE room_id=? AND family=? AND member_id=? AND active=1`,
			roomID, k.Family, k.ID).Scan(&found)
		if err == nil {
			member = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return Room{}, false, err
		}
	}
	if !member {
		return Room{}, false, ErrNotRoomMember
	}
	room := Room{ID: roomID, Members: []RoomMember{}}
	if err := tx.QueryRowContext(ctx, `SELECT name,revision,created_at FROM rooms WHERE room_id=?`, roomID).
		Scan(&room.Name, &room.Revision, &room.CreatedAt); err != nil {
		return Room{}, false, err
	}
	members, more, err := roomMemberRows(ctx, tx, now, roomID, `AND active=1 AND ordinal>?`, after, limit)
	room.Members = members
	return room, more, err
}

func roomMemberRows(ctx context.Context, tx *sql.Tx, now int64, roomID, filter string, after int64, limit int) ([]RoomMember, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,family,member_id,label,active,revision FROM room_members
		WHERE room_id=? `+filter+` ORDER BY ordinal LIMIT ?`, roomID, after, limit+1)
	if err != nil {
		return nil, false, err
	}
	type row struct {
		ordinal, revision       int64
		family, memberID, label string
		current                 bool
	}
	var scanned []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ordinal, &r.family, &r.memberID, &r.label, &r.current, &r.revision); err != nil {
			rows.Close()
			return nil, false, err
		}
		scanned = append(scanned, r)
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	more := len(scanned) > limit
	if more {
		scanned = scanned[:limit]
	}
	members := make([]RoomMember, 0, len(scanned))
	for _, r := range scanned {
		m, err := memberView(ctx, tx, now, r.family, r.memberID, r.label)
		if err != nil {
			return nil, false, err
		}
		m.Ordinal, m.Current, m.Revision = r.ordinal, r.current, r.revision
		if !r.current {
			m.State = "removed"
		}
		members = append(members, m)
	}
	return members, more, nil
}

// dashboardRooms lists every room with its current and former members for the maintainer.
func (s *Store) dashboardRooms(ctx context.Context) ([]Room, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	rows, err := tx.QueryContext(ctx, `SELECT room_id,name,revision,created_at FROM rooms ORDER BY created_at,room_id`)
	if err != nil {
		return nil, err
	}
	var rooms []Room
	for rows.Next() {
		var r Room
		if err := rows.Scan(&r.ID, &r.Name, &r.Revision, &r.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		rooms = append(rooms, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range rooms {
		if rooms[i].Members, _, err = roomMemberRows(ctx, tx, now, rooms[i].ID, `AND ordinal>?`, 0, maxRoomMemberRows); err != nil {
			return nil, err
		}
	}
	return rooms, nil
}
