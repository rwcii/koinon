package core

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The maintainer token (PDR 009, Decision 3). koinon mcp --maintainer exchanges the
// maintainer secret once for a token. The daemon keeps only the token's hash, in the store of
// dashboard logins, with the client name that it was issued to; every action under the token
// records that client as its via (Decision 4). The token has the limits of a dashboard login,
// and a secret change or a daemon restart ends it.

var (
	// ErrTokenExpired refuses a maintainer token that is unknown or has ended.
	ErrTokenExpired = Refusal{"maintainer_token_expired", "the maintainer token has ended; start koinon mcp --maintainer again"}
	// ErrClientName refuses a --client name outside MaintainerClientName.
	ErrClientName = Refusal{"invalid_request", "a client name is 1 to 32 characters of a-z, 0-9 and -, and starts with a letter or digit"}
	// MaintainerClientName is the form of a maintainer client's --client name.
	MaintainerClientName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// maintainerContext checks a token and returns ctx with the token's client as its origin. An
// accepted check is the token's use, so it moves the idle limit.
func (d *Daemon) maintainerContext(ctx context.Context, token string) (context.Context, error) {
	client, ok := d.auth.token(token)
	if !ok {
		return ctx, ErrTokenExpired
	}
	return withOrigin(ctx, client), nil
}

// maintainerToken is the token field of every maintainer request.
type maintainerToken struct {
	Token string `json:"token"`
}

func (t maintainerToken) token() string { return t.Token }

type tokened interface{ token() string }

// maintainerRoute decodes one maintainer request, checks its token and runs handle under the
// token's origin.
func maintainerRoute[T tokened](d *Daemon, mux *http.ServeMux, name string, limit int64, handle func(ctx context.Context, w http.ResponseWriter, request T)) {
	mux.HandleFunc("POST /v1/maintainer/"+name, func(w http.ResponseWriter, r *http.Request) {
		var request T
		if err := decodeLimit(w, r, &request, limit); err != nil {
			failure(w, err)
			return
		}
		ctx, err := d.maintainerContext(r.Context(), request.token())
		if err != nil {
			failure(w, err)
			return
		}
		handle(ctx, w, request)
	})
}

// maintainerRead answers a maintainer read with its result.
func maintainerRead(w http.ResponseWriter, result any, err error) {
	if err != nil {
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "result": result})
}

// maintainerAct runs one audited maintainer action under the token's origin, as the
// dashboard's run does: invalid input is refused before any store call, and a refusal writes
// its record with the same origin.
func (d *Daemon) maintainerAct(ctx context.Context, w http.ResponseWriter, action, target string, valid bool, apply func(ctx context.Context) (any, error)) {
	ctx, pending := withAudit(ctx, action, target)
	if !valid {
		d.store.auditRefusal(ctx, pending, "invalid_request")
		failure(w, ErrInvalid)
		return
	}
	result, err := apply(ctx)
	if err != nil {
		d.store.auditRefusal(ctx, pending, errorCode(err))
		failure(w, err)
		return
	}
	respond(w, 200, map[string]any{"ok": true, "result": result})
}

// maintainerBoard is the decision board as board_list reads it: the counts that the board
// shows and one page of the filter's summaries in item ID order.
type maintainerBoard struct {
	Counts maintainerCounts `json:"counts"`
	DecisionPage
}

type maintainerCounts struct {
	Waiting    int64 `json:"waiting"`
	InProgress int64 `json:"in_progress"`
	OpenFYI    int64 `json:"open_fyi"`
	Archived   int64 `json:"archived"`
}

// maintainerBoardRead reads the board's counts and one page of the filter's summaries in one
// read snapshot. The filter is waiting by default.
func (s *Store) maintainerBoardRead(ctx context.Context, filter string, after int64, limit int) (maintainerBoard, error) {
	if filter == "" {
		filter = "waiting"
	}
	if !decisionFilters[filter] || filter == "mine" {
		return maintainerBoard{}, decisionInvalid("filter", "must be waiting, active, in_progress or archived")
	}
	if after < 0 || limit < 0 {
		return maintainerBoard{}, decisionInvalid("after and limit", "must not be negative")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return maintainerBoard{}, err
	}
	defer tx.Rollback()
	var board maintainerBoard
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(state='active' AND kind='decision'),0),COALESCE(SUM(state='in_progress'),0),
		COALESCE(SUM(state='active' AND kind='fyi'),0),COALESCE(SUM(state='archived'),0) FROM decision_items`).
		Scan(&board.Counts.Waiting, &board.Counts.InProgress, &board.Counts.OpenFYI, &board.Counts.Archived); err != nil {
		return maintainerBoard{}, err
	}
	board.DecisionPage, err = decisionPage(ctx, tx, filter, nil, after, limit)
	return board, err
}

// maintainerDecision reads one item as the dashboard's lookup does.
func (s *Store) maintainerDecision(ctx context.Context, reference string) (DecisionItem, error) {
	if err := checkReference(reference); err != nil {
		return DecisionItem{}, err
	}
	return s.decisionRead(ctx, reference)
}

// maintainerHistory is the verdict history as the dashboard shows it, with each verdict's via.
func (s *Store) maintainerHistory(ctx context.Context, reference string, after int64, limit int) (DecisionHistoryPage, error) {
	if err := checkReference(reference); err != nil {
		return DecisionHistoryPage{}, err
	}
	if after < 0 || limit < 0 {
		return DecisionHistoryPage{}, decisionInvalid("after and limit", "must not be negative")
	}
	if limit == 0 || limit > decisionHistoryItems {
		limit = decisionHistoryItems
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DecisionHistoryPage{}, err
	}
	defer tx.Rollback()
	return decisionHistory(ctx, tx, reference, after, limit, true)
}

// maintainerInbox reads the maintainer's own inbox after a sequence, at most limit messages
// and about one MiB of bodies, as an agent's inbox read pages its own. Reading acknowledges
// nothing.
func (s *Store) maintainerInbox(ctx context.Context, after, limit int64) (Inbox, error) {
	if limit == 0 {
		limit = 50
	}
	if after < 0 || limit < 1 || limit > 100 {
		return Inbox{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Inbox{}, err
	}
	defer tx.Rollback()
	result := Inbox{Messages: []Message{}}
	result.LastSeq, result.AckedThrough, result.More, err = readMessages(ctx, tx, maintainerKey, after, limit, "session", &result.Messages)
	return result, err
}

// maintainerRoomRead pages any room's retained posts from the room's start, as the
// dashboard's transcript reads them.
func (s *Store) maintainerRoomRead(ctx context.Context, roomID string, after int64, limit int) (RoomTranscript, error) {
	if limit == 0 {
		limit = maxRoomReadPosts
	}
	if limit < 0 || limit > maxRoomReadPosts || after < 0 || !validRoomID(roomID) {
		return RoomTranscript{}, ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RoomTranscript{}, err
	}
	defer tx.Rollback()
	return roomTranscript(ctx, tx, roomID, 0, after, limit)
}

// maintainerRoom is a room as room-list returns it: its ID, name, roster revision, creation
// time and current and former members.
type maintainerRoom struct {
	ID        string       `json:"room_id"`
	Name      string       `json:"name"`
	Revision  int64        `json:"revision"`
	CreatedAt int64        `json:"created_at"`
	Members   []RoomMember `json:"members"`
}

// maintainerPost is a room-post receipt: the post's sequence and how many members it reached
// or skipped.
type maintainerPost struct {
	RoomID    string `json:"room_id"`
	Seq       int64  `json:"seq"`
	Queued    int    `json:"queued"`
	Skipped   int    `json:"skipped"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// maintainerVerdicts are the verdicts that decision-verdict takes, each with the dashboard
// action that gives it, which names its audit record.
var maintainerVerdicts = map[string]string{"approved": "decision-approve", "declined": "decision-decline", "dismissed": "decision-dismiss"}

// auditName bounds a peer or recipient name in an audit target.
func auditName(name string) string {
	if name == "" || len(name) > 256 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") {
		return "invalid"
	}
	return name
}

// maintainerSessionRoutes add the token exchange and the maintainer's routes (PDR 009,
// Decision 3). Each route takes the token in its body and calls the store method of the
// matching dashboard action or read, with the same refusals; each action writes its audit
// record with the token's client as its origin.
func (d *Daemon) maintainerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/maintainer/session", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Secret string `json:"secret"`
			Client string `json:"client"`
		}
		// JSON escaping can grow a secret of MaintainerSecretMaxBytes to six times its size.
		if err := decodeLimit(w, r, &request, 6*MaintainerSecretMaxBytes+256); err != nil {
			failure(w, err)
			return
		}
		if !MaintainerClientName.MatchString(request.Client) {
			failure(w, ErrClientName)
			return
		}
		client := "mcp:" + request.Client
		token, err := d.passwordLogin(r.Context(), request.Secret, client)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "token": token, "client": client,
			"idle_seconds": int64(sessionIdle.Seconds()), "absolute_seconds": int64(sessionAbsolute.Seconds())})
	})

	maintainerRoute(d, mux, "board-list", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Filter string `json:"filter"`
		After  int64  `json:"after"`
		Limit  int    `json:"limit"`
	}) {
		board, err := d.store.maintainerBoardRead(ctx, q.Filter, q.After, q.Limit)
		maintainerRead(w, board, err)
	})
	maintainerRoute(d, mux, "decision-get", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Reference string `json:"reference"`
	}) {
		item, err := d.store.maintainerDecision(ctx, q.Reference)
		maintainerRead(w, item, err)
	})
	maintainerRoute(d, mux, "decision-history", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Reference string `json:"reference"`
		After     int64  `json:"after"`
		Limit     int    `json:"limit"`
	}) {
		page, err := d.store.maintainerHistory(ctx, q.Reference, q.After, q.Limit)
		maintainerRead(w, page, err)
	})
	maintainerRoute(d, mux, "decision-verdict", 6*decisionNoteLimit+8192, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Reference string `json:"reference"`
		Revision  int64  `json:"revision"`
		Verdict   string `json:"verdict"`
		Note      string `json:"note"`
	}) {
		action, known := maintainerVerdicts[q.Verdict]
		if !known {
			action = "decision-verdict"
		}
		target := "decision invalid"
		if checkReference(q.Reference) == nil {
			target = "decision " + q.Reference
		}
		d.maintainerAct(ctx, w, action, target, known && q.Revision > 0 && checkReference(q.Reference) == nil, func(ctx context.Context) (any, error) {
			verdict, err := d.store.DecisionVerdict(ctx, q.Reference, q.Revision, q.Verdict, q.Note)
			verdict.Via = originOf(ctx)
			return verdict, err
		})
	})
	maintainerRoute(d, mux, "inbox", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		After int64 `json:"after"`
		Limit int64 `json:"limit"`
	}) {
		inbox, err := d.store.maintainerInbox(ctx, q.After, q.Limit)
		maintainerRead(w, inbox, err)
	})
	maintainerRoute(d, mux, "ack", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Through *int64 `json:"through"`
	}) {
		valid := q.Through != nil && *q.Through >= 0
		d.maintainerAct(ctx, w, "acknowledge", maintainerKey.Family+":"+maintainerKey.ID, valid, func(ctx context.Context) (any, error) {
			acked, err := d.store.ack(ctx, maintainerKey, *q.Through, true)
			return map[string]any{"acked_through": acked}, err
		})
	})
	maintainerRoute(d, mux, "send", 6*maxBody+4096, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		To   string `json:"to"`
		Body string `json:"body"`
	}) {
		valid := q.To != "" && len(q.To) <= 256 && !strings.ContainsAny(q.To, "\x00\r\n") && q.Body != "" && len(q.Body) <= maxBody && utf8.ValidString(q.Body)
		target := "to " + q.To + " bytes " + strconv.Itoa(len(q.Body))
		if auditName(q.To) == "invalid" {
			target = "invalid recipient"
		}
		d.maintainerAct(ctx, w, "send", target, valid, func(ctx context.Context) (any, error) {
			return d.store.send(ctx, maintainerKey, q.To, q.Body, true)
		})
	})
	maintainerRoute(d, mux, "room-list", 16384, func(ctx context.Context, w http.ResponseWriter, q struct{ maintainerToken }) {
		rooms, err := d.store.dashboardRooms(ctx)
		views := []maintainerRoom{}
		for _, r := range rooms {
			views = append(views, maintainerRoom{r.ID, r.Name, r.Revision, r.CreatedAt, r.Members})
		}
		maintainerRead(w, map[string]any{"rooms": views}, err)
	})
	maintainerRoute(d, mux, "room-read", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		RoomID string `json:"room_id"`
		After  int64  `json:"after"`
		Limit  int    `json:"limit"`
	}) {
		transcript, err := d.store.maintainerRoomRead(ctx, q.RoomID, q.After, q.Limit)
		maintainerRead(w, transcript, err)
	})
	maintainerRoute(d, mux, "room-post", 6*maxBody+4096, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		RoomID   string   `json:"room_id"`
		Body     string   `json:"body"`
		Key      string   `json:"key"`
		Deadline *float64 `json:"deadline"`
	}) {
		valid := validRoomID(q.RoomID) && q.Body != "" && len(q.Body) <= maxBody && utf8.ValidString(q.Body) && q.Key != "" && len(q.Key) <= 256 && q.Deadline != nil
		d.maintainerAct(ctx, w, "room-post", "room "+roomTarget(q.RoomID)+" bytes "+strconv.Itoa(len(q.Body)), valid, func(ctx context.Context) (any, error) {
			r, err := d.store.DashboardRoomPost(ctx, q.RoomID, q.Body, q.Key, *q.Deadline)
			return maintainerPost{r.RoomID, r.Seq, r.Queued, r.Skipped, r.Duplicate}, err
		})
	})
	maintainerRoute(d, mux, "room-create", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Name string `json:"name"`
		Key  string `json:"key"`
	}) {
		valid := validRoomName(q.Name) && q.Key != "" && len(q.Key) <= 64
		d.maintainerAct(ctx, w, "room-create", "name bytes "+strconv.Itoa(len(q.Name)), valid, func(ctx context.Context) (any, error) {
			// #493 removes the refill.
			id, err := d.store.CreateRoom(ctx, q.Key, q.Name, defaultRoomRefill)
			return map[string]any{"room_id": id}, err
		})
	})
	maintainerRoute(d, mux, "room-add", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		RoomID string `json:"room_id"`
		Peer   string `json:"peer"`
	}) {
		valid := validRoomID(q.RoomID) && auditName(q.Peer) != "invalid"
		target := "room " + roomTarget(q.RoomID) + " add "
		if valid {
			target += q.Peer
		}
		d.maintainerAct(ctx, w, "room-add", target, valid, func(ctx context.Context) (any, error) {
			changed, err := d.store.AddRoomMember(ctx, q.RoomID, q.Peer)
			return map[string]any{"changed": changed}, err
		})
	})
	maintainerRoute(d, mux, "room-remove", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		RoomID  string `json:"room_id"`
		Ordinal int64  `json:"ordinal"`
	}) {
		d.maintainerAct(ctx, w, "room-remove", "room "+roomTarget(q.RoomID)+" member "+strconv.FormatInt(q.Ordinal, 10),
			q.Ordinal > 0 && validRoomID(q.RoomID), func(ctx context.Context) (any, error) {
				return map[string]any{"removed": true}, d.store.RemoveRoomMember(ctx, q.RoomID, q.Ordinal)
			})
	})
	maintainerRoute(d, mux, "peers", 16384, func(ctx context.Context, w http.ResponseWriter, q struct{ maintainerToken }) {
		peers, truncated, err := d.store.maintainerPeers(ctx)
		maintainerRead(w, map[string]any{"peers": peers, "truncated": truncated}, err)
	})
	maintainerRoute(d, mux, "peer-status", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Peer string `json:"peer"`
	}) {
		status, err := d.store.maintainerPeerStatus(ctx, q.Peer)
		maintainerRead(w, status, err)
	})
}
