package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

const operationReceiptPrefix = "remote.v2.receipt:"
const operationReceiptWindow = 7 * 24 * time.Hour

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// OperationReceipt records host acceptance, independently of delivery or acknowledgement.
// Its namespace is the daemon's private store and operation ID, never the login session.
// Versioned JSON uses namespaced settings rows so older binaries can still open the
// unchanged schema. SQLite's non-STRICT INTEGER affinity preserves JSON as TEXT.
type OperationReceipt struct {
	Version      int             `json:"version"`
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	Origin       string          `json:"origin"`
	Type         string          `json:"type"`
	Target       string          `json:"target"`
	Digest       string          `json:"digest"`
	CreatedAt    int64           `json:"created_at"`
	ExpiresAt    int64           `json:"expires_at"`
	State        string          `json:"state"`
	Code         string          `json:"code"`
	ResultTarget string          `json:"result_target,omitempty"`
	Result       json.RawMessage `json:"result,omitempty"`
}
type operationContextKey struct{}

func operationOf(ctx context.Context) *OperationReceipt {
	p, _ := ctx.Value(operationContextKey{}).(*OperationReceipt)
	return p
}

// operationContext binds a remote form's semantic fields, excluding CSRF, navigation,
// and session identity. Reauthentication therefore leaves the deduplication key intact.
func (d *Daemon) operationContext(r *http.Request, ctx context.Context, session, action, target, accepted string) (context.Context, error) {
	if remoteOrigin(r) == "" || r.PostForm.Get("operation_id") == "" {
		return ctx, nil
	}
	id := r.PostForm.Get("operation_id")
	created, err := strconv.ParseInt(r.PostForm.Get("operation_created_at"), 10, 64)
	now := d.store.now().UnixMilli()
	if err != nil || !operationIDPattern.MatchString(id) || created <= 0 || created > now+int64(time.Minute/time.Millisecond) {
		return ctx, ErrInvalid
	}
	if now >= created+operationReceiptWindow.Milliseconds() {
		return ctx, ErrRoomPostLate
	}
	fields := map[string]string{"action": action, "target": target}
	switch action {
	case "send":
		fields["to"], fields["body"] = r.PostForm.Get("to"), r.PostForm.Get("body")
	case "room-post":
		fields["room_id"], fields["body"] = r.PostForm.Get("room_id"), r.PostForm.Get("body")
	default:
		fields["reference"], fields["revision"], fields["note"] = r.PostForm.Get("reference"), r.PostForm.Get("revision"), r.PostForm.Get("note")
	}
	data, _ := json.Marshal(fields)
	digest := sha256.Sum256(data)
	if session == "" {
		if token, ok := d.auth.session(r); ok {
			session = d.auth.remoteSessionID(token)
		}
	}
	p := &OperationReceipt{Version: 1, ID: id, SessionID: session, Origin: remoteOrigin(r), Type: action, Target: target, Digest: hex.EncodeToString(digest[:]), CreatedAt: created, ExpiresAt: created + operationReceiptWindow.Milliseconds(), State: "accepted", Code: accepted}
	return context.WithValue(ctx, operationContextKey{}, p), nil
}

func operationReceipt(ctx context.Context, q querier, id string) (*OperationReceipt, error) {
	var data string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE name=?`, operationReceiptPrefix+id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p OperationReceipt
	if err = json.Unmarshal([]byte(data), &p); err != nil {
		return nil, err
	}
	if p.Version != 1 || p.ID != id {
		return nil, ErrInvalid
	}
	return &p, nil
}

// A retained result is readable even when ordinary write admission is full. A
// second lookup inside the serialized write transaction fences concurrent originals.
func operationLookup[T any](ctx context.Context, q querier, now int64) (result T, found bool, err error) {
	p := operationOf(ctx)
	if p == nil {
		return result, false, nil
	}
	old, err := operationReceipt(ctx, q, p.ID)
	if err != nil || old == nil {
		return result, false, err
	}
	if old.Digest != p.Digest || old.Origin != p.Origin || old.Type != p.Type || old.Target != p.Target {
		return result, true, ErrRoomPostRepeat
	}
	if now >= old.ExpiresAt {
		return result, true, ErrRoomPostLate
	}
	if old.State != "accepted" {
		return result, true, Refusal{old.Code, "the original operation was refused"}
	}
	if len(old.Result) > 0 {
		err = json.Unmarshal(old.Result, &result)
	}
	return result, true, err
}
func operationReplay[T any](t *writeTx) (result T, found bool, err error) {
	if operationOf(t.ctx) == nil {
		return result, false, nil
	}
	result, found, err = operationLookup[T](t.ctx, t.Tx, t.s.now().UnixMilli())
	if found || err != nil {
		return result, found, err
	}
	if _, err = t.ExecContext(t.ctx, `DELETE FROM settings WHERE name GLOB 'remote.v2.receipt:*' AND updated_at<=?`, t.s.now().UnixMilli()); err != nil {
		return result, false, err
	}
	var retained int
	if err = t.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM settings WHERE name GLOB 'remote.v2.receipt:*'`).Scan(&retained); err != nil {
		return result, false, err
	}
	if retained >= 10000 {
		return result, false, ErrCapacity
	}
	return result, false, nil
}

func (t *writeTx) writeOperationReceipt() error {
	p := operationOf(t.ctx)
	if p == nil || !t.audited {
		return nil
	}
	if audit, _ := t.ctx.Value(auditContextKey{}).(*pendingAudit); audit != nil {
		p.ResultTarget = audit.target
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = t.ExecContext(t.ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?)`, operationReceiptPrefix+p.ID, string(data), p.ExpiresAt)
	return err
}
func (t *writeTx) operationResult(result any) {
	if p := operationOf(t.ctx); p != nil {
		p.Result, _ = json.Marshal(result)
	}
}

// operationRefusal commits a durable revision-conflict result before returning it.
// Call only before any mutation has occurred in this transaction.
func (t *writeTx) operationRefusal(err error) error {
	p := operationOf(t.ctx)
	if p == nil {
		return err
	}
	p.State = "conflict"
	p.Code = errorCode(err)
	t.audited = false // refusal audit remains the caller's responsibility
	data, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if _, e = t.ExecContext(t.ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?)`, operationReceiptPrefix+p.ID, string(data), p.ExpiresAt); e != nil {
		return e
	}
	if e = t.Commit(); e != nil {
		return e
	}
	return err
}

func (s *Store) recentOperationReceipts(ctx context.Context, limit int) ([]OperationReceipt, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `SELECT value FROM settings WHERE name GLOB 'remote.v2.receipt:*' AND updated_at>? ORDER BY updated_at DESC LIMIT ?`, s.now().UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationReceipt{}
	for rows.Next() {
		var data string
		var p OperationReceipt
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		if p.Version != 1 {
			return nil, ErrInvalid
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (d *Daemon) operationReceiptRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	mux.HandleFunc("GET /dashboard/receipts", authed(func(w http.ResponseWriter, r *http.Request, _ string) {
		id := r.URL.Query().Get("id")
		if !operationIDPattern.MatchString(id) {
			http.Error(w, "invalid operation ID", http.StatusBadRequest)
			return
		}
		p, err := operationReceipt(r.Context(), d.store.db, id)
		if err != nil {
			http.Error(w, "receipt storage unavailable", http.StatusServiceUnavailable)
			return
		}
		// A host's local maintainer may reconcile its remote actions; a remote request must
		// also match the original external origin after the ordinary authentication checks.
		if p != nil && remoteOrigin(r) != "" && p.Origin != remoteOrigin(r) {
			http.Error(w, "operation origin conflict", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if p == nil {
			json.NewEncoder(w).Encode(map[string]any{"found": false, "authoritative": true, "server_time": d.store.now().UnixMilli()})
			return
		}
		if d.store.now().UnixMilli() >= p.ExpiresAt {
			json.NewEncoder(w).Encode(map[string]any{"found": false, "authoritative": true, "expired": true, "server_time": d.store.now().UnixMilli()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"found": true, "authoritative": true, "receipt": p, "server_time": d.store.now().UnixMilli()})
	}))
}
