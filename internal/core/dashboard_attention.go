package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Attention cursors belong to a browser login. They never update message acknowledgements.
type attentionData struct {
	Waiting, DirectUnread, RoomUnread int64
	Oldest                            string
	DirectHead, RoomHead              int64
	Decisions, Messages, Posts        []attentionLink
	Problems                          []attentionLink
	UnknownActivities                 int64
	Receipts                          []OperationReceipt
	At                                int64
}
type attentionCursor struct{ Direct, Room int64 }

type attentionLink struct{ Label, Href, Detail string }

func (d *Daemon) attentionBrowser(id string) string {
	if browser := d.auth.remoteSessionID(id); browser != "" {
		return browser
	}
	// A domain-separated digest is an identifier, never an accepted credential.
	h := sha256.Sum256([]byte("koinon-attention-browser\x00" + id))
	return hex.EncodeToString(h[:])
}

func (s *Store) attention(ctx context.Context, browser string, origins ...string) (attentionData, error) {
	data := attentionData{At: s.now().UnixMilli(), Oldest: "none waiting"}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return data, err
	}
	defer tx.Rollback()
	var direct, room int64
	var value string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE name=?`, "remote.v2.attention:"+browser).Scan(&value)
	if err != nil && err != sql.ErrNoRows {
		return data, err
	}
	if err == nil {
		var cursor attentionCursor
		if err = json.Unmarshal([]byte(value), &cursor); err != nil {
			return data, err
		}
		direct, room = cursor.Direct, cursor.Room
	}
	var oldest int64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(created_at),0) FROM decision_items WHERE state='active' AND kind!='fyi'`).Scan(&data.Waiting, &oldest); err != nil {
		return data, err
	}
	if oldest != 0 {
		age := time.Duration(max(int64(0), data.At-oldest)) * time.Millisecond
		data.Oldest = age.Round(time.Second).String() + " waiting"
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(id),0) FROM messages WHERE recipient_family='maintainer' AND room_id='' AND id>?`, direct).Scan(&data.DirectUnread, &data.DirectHead); err != nil {
		return data, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(post_id),0) FROM room_posts WHERE post_id>?`, room).Scan(&data.RoomUnread, &data.RoomHead); err != nil {
		return data, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT reference,title FROM decision_items WHERE state='active' AND kind!='fyi' ORDER BY created_at,id LIMIT 20`)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var ref, title string
		if err = rows.Scan(&ref, &title); err != nil {
			rows.Close()
			return data, err
		}
		data.Decisions = append(data.Decisions, attentionLink{ref, "/dashboard/decisions?ref=" + url.QueryEscape(ref), title})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,sender_name,created_at FROM messages WHERE recipient_family='maintainer' AND room_id='' AND id>? ORDER BY id DESC LIMIT 20`, direct)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var id, at int64
		var name string
		if err = rows.Scan(&id, &name, &at); err != nil {
			rows.Close()
			return data, err
		}
		data.Messages = append(data.Messages, attentionLink{"Message " + strconv.FormatInt(id, 10), "/dashboard/messages?to=maintainer&sort=id&dir=desc#message-" + strconv.FormatInt(id, 10), "From " + name + " · " + time.UnixMilli(at).UTC().Format(time.RFC3339)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT post_id,room_id,seq,sender_name FROM room_posts WHERE post_id>? ORDER BY post_id DESC LIMIT 20`, room)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var id, seq int64
		var roomID, name string
		if err = rows.Scan(&id, &roomID, &seq, &name); err != nil {
			rows.Close()
			return data, err
		}
		data.Posts = append(data.Posts, attentionLink{"Post " + strconv.FormatInt(seq, 10), "/dashboard/rooms?room=" + url.QueryEscape(roomID) + "&after=" + strconv.FormatInt(max(int64(0), seq-1), 10), "From " + name})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	origin := ""
	if len(origins) > 0 {
		origin = origins[0]
	}
	if data.Receipts, err = recentReceipts(ctx, tx, data.At, origin, 20); err != nil {
		return data, err
	}

	// Session membership is read from the same database snapshot. Cached observations
	// remain explicitly observations; Attention never probes terminals or wakes agents.
	rows, err = tx.QueryContext(ctx, sessionQuery+` WHERE s.family!='maintainer' AND `+notParticipantRow+` AND s.retired_at=0 ORDER BY s.family,s.id`)
	if err != nil {
		return data, err
	}
	var sessions []Session
	for rows.Next() {
		session, e := scanSession(rows, data.At)
		if e != nil {
			rows.Close()
			return data, e
		}
		sessions = append(sessions, session)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	s.observed.mu.Lock()
	for _, session := range sessions {
		if session.State != "active" {
			data.Problems = append(data.Problems, attentionLink{session.Name, "/dashboard/sessions?q=" + url.QueryEscape(session.Name), "Registration expired; current activity unknown"})
			continue
		}
		activity, ok := s.observed.byKey[Key{session.Family, session.ID}]["activity"]
		if !ok || data.At-activity.seen > observationFreshness["activity"].Milliseconds() {
			data.UnknownActivities++
			continue
		}
		if activity.TurnError != "" {
			data.Problems = append(data.Problems, attentionLink{session.Name, "/dashboard/sessions?q=" + url.QueryEscape(session.Name), "Observed turn error: " + activity.TurnError})
		}
	}
	s.observed.mu.Unlock()
	return data, nil
}

func (d *Daemon) dashboardAttentionRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	mux.HandleFunc("POST /dashboard/actions/attention-read", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		direct, e1 := strconv.ParseInt(r.PostForm.Get("direct"), 10, 64)
		room, e2 := strconv.ParseInt(r.PostForm.Get("room"), 10, 64)
		if e1 != nil || e2 != nil || direct < 0 || room < 0 {
			http.Error(w, "Invalid read cursor", http.StatusBadRequest)
			return
		}
		err := d.store.markAttentionRead(r.Context(), d.attentionBrowser(id), direct, room)

		if err != nil {
			http.Error(w, "Read state could not be saved", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/dashboard/attention", http.StatusSeeOther)
	}))
}

// markAttentionRead writes only this browser's view cursor, bounded by the current
// source heads. The serialized store connection keeps concurrent cursor changes monotonic.
func (s *Store) markAttentionRead(ctx context.Context, browser string, direct, room int64) error {
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var headDirect, headRoom int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM messages WHERE recipient_family='maintainer' AND room_id=''`).Scan(&headDirect); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(post_id),0) FROM room_posts`).Scan(&headRoom); err != nil {
		return err
	}
	var prior string
	var cursor attentionCursor
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE name=?`, "remote.v2.attention:"+browser).Scan(&prior)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		if err = json.Unmarshal([]byte(prior), &cursor); err != nil {
			return err
		}
	}
	cursor.Direct = max(cursor.Direct, min(direct, headDirect))
	cursor.Room = max(cursor.Room, min(room, headRoom))
	value, err := json.Marshal(cursor)
	if err != nil {
		return err
	}
	now := s.now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, "remote.v2.attention:"+browser, string(value), now); err != nil {
		return tx.fail(err)
	}
	// A browser's read position lasts attentionCursorLife after its last mark: longer than a
	// remote login and its kept row, so every live browser keeps its own. Older positions,
	// such as those of ended local logins, are deleted here (#673 review M7).
	if _, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE name GLOB 'remote.v2.attention:*' AND updated_at < ?`, now-attentionCursorLife.Milliseconds()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// attentionCursorLife is how long a browser's Attention read position is kept after its
// last mark: the 48-hour remote login plus the 7 days that its session row is kept.
const attentionCursorLife = remoteSessionLifetime + 7*24*time.Hour
