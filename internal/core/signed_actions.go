package core

// Signed maintainer actions (#693, LINK-693-SIGNED; PDR 011 Decision 6;
// docs/sprints/2026-11-14-signed-maintainer/decision.md, Decisions 2, 3, 4 and 7). A maintainer
// client signs each action with its own key; this computer runs an action whose target is its
// own pin under the key's current authorization, in the order of Decision 3.3. A write and its
// receipt commit in one store transaction, after the receipt lookup and the time check in that
// transaction, so a request runs once and a write that waited past its window does not run.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/signed"
)

const (
	// signedWindow is the time, in seconds, within which an envelope's created must be of this
	// computer's clock (Decision 3.3).
	signedWindow = 300
	// signedReceiptWindow is how long a write's receipt is kept (Decision 4.1).
	signedReceiptWindow = 7 * 24 * time.Hour
)

// signedAction is one checked envelope under its authorization. The write transaction that
// runs it checks it again (signedReplay) and records its receipt.
type signedAction struct {
	key      MaintainerKey
	op       string
	request  string
	digest   string
	created  int64
	snapshot string
	// checked is set by the receipt lookup and time check in the write's transaction; a write
	// commits its receipt only after them.
	checked  bool
	recorded bool
	// shape turns the store's result into the operation's answer, which the receipt keeps; nil
	// keeps the store's result. result is that answer as JSON.
	shape  func(any) any
	result json.RawMessage
}

// signedReplayed carries a receipt's recorded answer out of a write's store call.
type signedReplayed struct{ raw json.RawMessage }

func (signedReplayed) Error() string { return "the request has a recorded receipt" }

type signedContextKey struct{}

func signedOf(ctx context.Context) *signedAction {
	a, _ := ctx.Value(signedContextKey{}).(*signedAction)
	return a
}

// receipted reports whether a write of ctx keeps a receipt: a remote form's operation or a
// signed action.
func receipted(ctx context.Context) bool { return operationOf(ctx) != nil || signedOf(ctx) != nil }

// signerText names a signed action's signer in an audit record (Decision 7.2).
func (a *signedAction) signerText() string {
	return a.key.User + " (" + a.key.Client + ") key " + a.key.KeyID
}

func signedRefusal(reason, message string) error {
	return WorkRefusal{Code: "signature_refused", Message: message, Details: map[string]any{"reason": reason}}
}

// signedError turns a refusal of the envelope package into signature_refused with its reason.
func signedError(err error) error {
	var r signed.Refusal
	if errors.As(err, &r) {
		return signedRefusal(r.Reason, r.Error())
	}
	return err
}

// ErrSignedRequestConflict refuses a request ID that this authorization used for another
// envelope (Decision 4.2).
var ErrSignedRequestConflict = Refusal{"request_conflict", "this request ID was used for another action; sign a new request ID"}

var signedWrites = map[string]bool{"decision_verdict": true, "ack": true, "send": true, "room_post": true}

// signedOps are the operations of Decision 2 that a node runs for its own pin.
var signedOps = map[string]bool{"board_list": true, "decision_get": true, "decision_history": true, "decision_verdict": true,
	"inbox": true, "ack": true, "send": true, "room_list": true, "room_read": true, "room_post": true, "peers": true,
	"peer_status": true, "outcome": true, "computers": true, "changes": true}

// checkEnvelope checks an envelope through item 2 of Decision 3.3: its form and version, its
// target, the key's current authorization and the signature. It returns the envelope and
// the authorization.
func (d *Daemon) checkEnvelope(ctx context.Context, data []byte) (signed.Envelope, MaintainerKey, error) {
	e, err := signed.Parse(data)
	if err != nil {
		return e, MaintainerKey{}, signedError(err)
	}
	if e.Op == "enroll" {
		return e, MaintainerKey{}, signedRefusal("version", "an enrollment statement is added with koinon maintainer key add")
	}
	id, err := d.linkIdentity()
	if err != nil {
		return e, MaintainerKey{}, err
	}
	if e.Target != id.Pin {
		return e, MaintainerKey{}, Refusal{"invalid_request", "the target is another computer; forwarding is not built yet"}
	}
	key, err := currentAuthorization(ctx, d.store.db, e.Key)
	if errors.Is(err, sql.ErrNoRows) {
		return e, MaintainerKey{}, d.store.missingKey(ctx, e.Key)
	}
	if err != nil {
		return e, MaintainerKey{}, err
	}
	if key.Authorization != e.Auth {
		return e, MaintainerKey{}, signedRefusal("authorization_changed", "the key was added again since this envelope's authorization")
	}
	if err := signed.Verify(e, key.public); err != nil {
		return e, MaintainerKey{}, signedError(err)
	}
	return e, key, nil
}

// missingKey refuses a key with no current authorization: key_removed when it had one,
// key_unknown when it never had.
func (s *Store) missingKey(ctx context.Context, keyID string) error {
	var removed int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintainer_authorizations WHERE key_id=?`, keyID).Scan(&removed); err != nil {
		return err
	}
	if removed > 0 {
		return signedRefusal("key_removed", "the key's authorization was removed on this computer")
	}
	return signedRefusal("key_unknown", "no authorization on this computer has this key")
}

// inWindow reports whether created is within signedWindow seconds of now.
func inWindow(now time.Time, created int64) bool {
	d := now.Unix() - created
	return d <= signedWindow && d >= -signedWindow
}

// SignedAction runs one envelope whose target is this computer and returns the operation's
// result, as the maintainer routes answer it (Answer shapes).
func (d *Daemon) SignedAction(ctx context.Context, data []byte) (any, error) {
	e, key, err := d.checkEnvelope(ctx, data)
	if err != nil {
		return nil, err
	}
	if !signedOps[e.Op] {
		return nil, Refusal{"invalid_request", "the operation " + strconv.Quote(e.Op) + " does not cross as a signed action"}
	}
	args := signedArgs{values: e.Args}
	if !signedWrites[e.Op] {
		// Item 4 of Decision 3.3: a read runs within 5 minutes of its created.
		if !inWindow(d.store.now(), e.Created) {
			return nil, signedRefusal("time", "the envelope is not within 5 minutes of this computer's clock")
		}
		result, err := d.signedRead(ctx, e, key, &args)
		if err != nil {
			return nil, err
		}
		// A removal that committed while the read ran ends its answer too.
		if err := stillAuthorized(ctx, d.store.db, key); err != nil {
			return nil, err
		}
		return result, nil
	}
	a := &signedAction{key: key, op: e.Op, request: e.Request, digest: e.Digest(), created: e.Created}
	// A receipt answers before write admission, so a full or blocked store still returns it
	// (Decision 3.3 item 3; the write's transaction looks again).
	if raw, found, err := d.store.signedLookup(ctx, a); found || err != nil {
		return raw, err
	}
	ctx = context.WithValue(withOrigin(ctx, "key:"+key.Client), signedContextKey{}, a)
	return d.signedWrite(ctx, e, a, &args)
}

// signedArgs reads the arguments of an envelope; an argument that the operation does not
// take, or of the wrong type, refuses the action.
type signedArgs struct {
	values map[string]any
	used   []string
	bad    bool
}

func (a *signedArgs) str(name string) string {
	a.used = append(a.used, name)
	v, ok := a.values[name]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	a.bad = a.bad || !ok
	return s
}

func (a *signedArgs) integer(name string) (int64, bool) {
	a.used = append(a.used, name)
	v, ok := a.values[name]
	if !ok {
		return 0, false
	}
	n, ok := v.(int64)
	a.bad = a.bad || !ok
	return n, ok
}

// check refuses unknown or mistyped arguments.
func (a *signedArgs) check() error {
	if a.bad {
		return Refusal{"invalid_request", "an argument has the wrong type"}
	}
	for name := range a.values {
		known := false
		for _, u := range a.used {
			known = known || u == name
		}
		if !known {
			return Refusal{"invalid_request", "the operation takes no argument " + strconv.Quote(name)}
		}
	}
	return nil
}

func (d *Daemon) signedRead(ctx context.Context, e signed.Envelope, key MaintainerKey, args *signedArgs) (any, error) {
	ctx = withOrigin(ctx, "key:"+key.Client)
	switch e.Op {
	case "board_list":
		filter := args.str("filter")
		after, _ := args.integer("after")
		limit, _ := args.integer("limit")
		if err := args.check(); err != nil {
			return nil, err
		}
		return d.store.maintainerBoardRead(ctx, filter, after, int(limit))
	case "decision_get":
		reference := args.str("reference")
		if err := args.check(); err != nil {
			return nil, err
		}
		return d.store.maintainerDecision(ctx, reference)
	case "decision_history":
		reference := args.str("reference")
		after, _ := args.integer("after")
		limit, _ := args.integer("limit")
		if err := args.check(); err != nil {
			return nil, err
		}
		return d.store.maintainerHistory(ctx, reference, after, int(limit))
	case "inbox":
		after, _ := args.integer("after")
		limit, _ := args.integer("limit")
		if err := args.check(); err != nil {
			return nil, err
		}
		return d.store.maintainerInbox(ctx, after, limit)
	case "room_list":
		// The rooms are few; after and limit page them in room order.
		after, _ := args.integer("after")
		limit, _ := args.integer("limit")
		if err := args.check(); err != nil {
			return nil, err
		}
		if after < 0 || limit < 0 {
			return nil, ErrInvalid
		}
		rooms, err := d.store.dashboardRooms(ctx)
		if err != nil {
			return nil, err
		}
		views := []maintainerRoom{}
		for i, r := range rooms {
			if int64(i) < after || (limit > 0 && int64(len(views)) >= limit) {
				continue
			}
			views = append(views, maintainerRoom{r.ID, r.Name, r.Revision, r.CreatedAt, r.Members})
		}
		return map[string]any{"rooms": views}, nil
	case "room_read":
		room := args.str("room")
		after, _ := args.integer("after")
		limit, _ := args.integer("limit")
		if err := args.check(); err != nil {
			return nil, err
		}
		return d.store.maintainerRoomRead(ctx, room, after, int(limit))
	case "peers":
		if err := args.check(); err != nil {
			return nil, err
		}
		peers, truncated, err := d.store.maintainerPeers(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"peers": append(peers, d.remoteRows()...), "truncated": truncated}, nil
	case "peer_status":
		peer := args.str("peer")
		if err := args.check(); err != nil {
			return nil, err
		}
		if link.IsRemoteAddress(peer) {
			return d.remotePeerStatus(peer)
		}
		return d.store.maintainerPeerStatus(ctx, peer)
	case "computers":
		if err := args.check(); err != nil {
			return nil, err
		}
		view, _, err := d.computersView()
		return view, err
	case "changes":
		raw, given := args.values["versions"]
		args.used = append(args.used, "versions")
		wait, _ := args.integer("wait_seconds")
		if err := args.check(); err != nil {
			return nil, err
		}
		var held map[string]string
		if given {
			object, isObject := raw.(map[string]any)
			var ok bool
			if held, ok = validVersions(object); !ok || !isObject {
				return nil, Refusal{"invalid_request", "versions maps at most one entry per topic to its 20-character version"}
			}
		}
		if wait < 0 || wait > maxChangeWait {
			return nil, Refusal{"invalid_request", "wait_seconds is 0 to 30"}
		}
		return d.signedChanges(ctx, key, held, time.Duration(wait)*time.Second)
	case "outcome":
		request := args.str("request")
		_, ok := args.integer("created")
		if err := args.check(); err != nil {
			return nil, err
		}
		if !ok || request == "" {
			return nil, Refusal{"invalid_request", "outcome takes the request ID and created of the original request"}
		}
		return d.store.signedOutcome(ctx, key, request)
	}
	return nil, Refusal{"invalid_request", "the operation does not cross as a signed action"}
}

func (d *Daemon) signedWrite(ctx context.Context, e signed.Envelope, a *signedAction, args *signedArgs) (any, error) {
	switch e.Op {
	case "decision_verdict":
		reference := args.str("reference")
		revision, _ := args.integer("revision")
		a.snapshot = args.str("snapshot")
		verdict := args.str("verdict")
		note := args.str("note")
		if err := args.check(); err != nil {
			return nil, err
		}
		action, known := maintainerVerdicts[verdict]
		if !known {
			action = "decision-verdict"
		}
		target := "decision invalid"
		if checkReference(reference) == nil {
			target = "decision " + reference
		}
		valid := known && revision > 0 && checkReference(reference) == nil && len(a.snapshot) == 64
		a.shape = func(r any) any {
			if v, ok := r.(DecisionVerdict); ok {
				v.Via = "key:" + a.key.Client
				return v
			}
			return r
		}
		return d.signedApply(ctx, action, target, valid, func(ctx context.Context) (any, error) {
			return d.store.DecisionVerdict(ctx, reference, revision, verdict, note)
		})
	case "ack":
		through, ok := args.integer("through")
		if err := args.check(); err != nil {
			return nil, err
		}
		a.shape = func(r any) any { return map[string]any{"acked_through": r} }
		return d.signedApply(ctx, "acknowledge", maintainerKey.Family+":"+maintainerKey.ID, ok && through >= 0, func(ctx context.Context) (any, error) {
			return d.store.ack(ctx, maintainerKey, through, true)
		})
	case "send":
		to := args.str("to")
		body := args.str("body")
		replyTo, _ := args.integer("reply_to")
		if err := args.check(); err != nil {
			return nil, err
		}
		valid := to != "" && len(to) <= link.MaxAddressBytes && !strings.ContainsAny(to, "\x00\r\n") && body != "" && len(body) <= maxBody &&
			utf8.ValidString(body) && replyTo >= 0
		target := "to " + to + " bytes " + strconv.Itoa(len(body))
		if auditName(to) == "invalid" {
			target = "invalid recipient"
		}
		return d.signedApply(ctx, "send", target, valid, func(ctx context.Context) (any, error) {
			if link.IsRemoteAddress(to) || replyTo != 0 {
				return d.linkSend(ctx, nil, to, body, replyTo)
			}
			return d.store.send(ctx, maintainerKey, to, body, true)
		})
	case "room_post":
		room := args.str("room")
		body := args.str("body")
		if err := args.check(); err != nil {
			return nil, err
		}
		valid := validRoomID(room) && body != "" && len(body) <= maxBody && utf8.ValidString(body)
		// The room's replay key is the authorization's request; its deadline is the end of the
		// envelope's window, which the receipt's time check enforces first.
		key := "signed:" + a.key.Authorization[:32] + ":" + a.request
		deadline := float64(a.created + signedWindow)
		a.shape = func(r any) any {
			if p, ok := r.(RoomPostResult); ok {
				return maintainerPost{p.RoomID, p.Seq, p.Queued, p.Skipped, p.Duplicate}
			}
			return r
		}
		return d.signedApply(ctx, "room-post", "room "+roomTarget(room)+" bytes "+strconv.Itoa(len(body)), valid, func(ctx context.Context) (any, error) {
			return d.store.DashboardRoomPost(ctx, room, body, key, deadline)
		})
	}
	return nil, Refusal{"invalid_request", "the operation does not cross as a signed action"}
}

// signedApply runs one audited signed write, as maintainerAct does for the token routes: invalid
// input is refused before any store call, and a refusal writes its audit record. The answer
// is the one that the write's receipt keeps, so a repeat and outcome give the same JSON.
func (d *Daemon) signedApply(ctx context.Context, action, target string, valid bool, apply func(ctx context.Context) (any, error)) (any, error) {
	ctx, pending := withAudit(ctx, action, target)
	if !valid {
		d.store.auditRefusal(ctx, pending, "invalid_request")
		return nil, ErrInvalid
	}
	_, err := apply(ctx)
	var replayed signedReplayed
	if errors.As(err, &replayed) {
		return replayed.raw, nil
	}
	if err != nil {
		d.store.auditRefusal(ctx, pending, errorCode(err))
		return nil, err
	}
	a := signedOf(ctx)
	if a == nil || !a.recorded {
		return nil, errors.New("a signed write committed without its receipt")
	}
	return a.result, nil
}

// stillAuthorized checks in q that key's authorization is still the current one.
func stillAuthorized(ctx context.Context, q querier, key MaintainerKey) error {
	current, err := currentAuthorization(ctx, q, key.KeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return signedRefusal("key_removed", "the key's authorization was removed on this computer")
	}
	if err != nil {
		return err
	}
	if current.Authorization != key.Authorization {
		return signedRefusal("authorization_changed", "the key was added again since this envelope's authorization")
	}
	return nil
}

// signedCheck checks in q that the action's authorization is current and looks up its
// receipt: found with raw for a recorded answer, or found with the receipt's refusal.
func signedCheck(ctx context.Context, q querier, a *signedAction, now time.Time) (json.RawMessage, bool, error) {
	if err := stillAuthorized(ctx, q, a.key); err != nil {
		return nil, false, err
	}
	r, err := signedReceipt(ctx, q, a.key.Authorization, a.request, now.UnixMilli())
	if err != nil || r == nil {
		return nil, false, err
	}
	if r.digest != a.digest {
		return nil, true, ErrSignedRequestConflict
	}
	if r.state == "refused" {
		return nil, true, r.refusal()
	}
	return r.result, true, nil
}

// signedLookup is signedCheck in a read transaction, before write admission.
func (s *Store) signedLookup(ctx context.Context, a *signedAction) (json.RawMessage, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	return signedCheck(ctx, tx, a, s.now())
}

// signedReplay is item 3 of Decision 3.3 inside the write's transaction: the authorization is
// still current, a receipt of this request answers now, and otherwise created must be within
// 5 minutes of this computer's clock at this moment. found reports a receipt's answer.
func signedReplay[T any](t *writeTx, a *signedAction) (result T, found bool, err error) {
	now := t.s.now()
	raw, found, err := signedCheck(t.ctx, t.Tx, a, now)
	if err != nil {
		return result, found, err
	}
	if found {
		return result, true, signedReplayed{raw}
	}
	if !inWindow(now, a.created) {
		return result, false, signedRefusal("time", "the envelope is not within 5 minutes of this computer's clock")
	}
	if _, err := t.ExecContext(t.ctx, `DELETE FROM signed_receipts WHERE expires_at<=?`, now.UnixMilli()); err != nil {
		return result, false, err
	}
	a.checked = true
	return result, false, nil
}

// storedReceipt is one row of signed_receipts.
type storedReceipt struct {
	state, code, digest string
	result              json.RawMessage
}

// refusedResult is the result column of a refused receipt.
type refusedResult struct {
	Error   string         `json:"error"`
	Details map[string]any `json:"details,omitempty"`
}

func (r storedReceipt) refusal() error {
	var refused refusedResult
	json.Unmarshal(r.result, &refused)
	return WorkRefusal{Code: r.code, Message: refused.Error, Details: refused.Details}
}

func signedReceipt(ctx context.Context, q querier, authorization, request string, now int64) (*storedReceipt, error) {
	var r storedReceipt
	var result string
	err := q.QueryRowContext(ctx, `SELECT state,code,digest,result FROM signed_receipts WHERE authorization=? AND request=? AND expires_at>?`,
		authorization, request, now).Scan(&r.state, &r.code, &r.digest, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	r.result = json.RawMessage(result)
	return &r, err
}

// writeSignedReceipt records the receipt of a signed write in its transaction.
func (t *writeTx) writeSignedReceipt(a *signedAction, state, code string, result []byte) error {
	if !a.checked {
		return errors.New("signed write committed without its receipt check")
	}
	if a.recorded {
		return nil
	}
	if state == "recorded" && !json.Valid(result) {
		return errors.New("signed write committed without its result")
	}
	now := t.s.now().UnixMilli()
	if _, err := t.ExecContext(t.ctx, `INSERT INTO signed_receipts(authorization,request,op,digest,state,code,result,created_at,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, a.key.Authorization, a.request, a.op, a.digest, state, code, string(result), now,
		now+signedReceiptWindow.Milliseconds()); err != nil {
		return err
	}
	a.recorded = true
	return nil
}

// signedRefused records a refusal of the write's own checks, before any mutation, as the
// receipt's answer.
func (t *writeTx) signedRefused(a *signedAction, err error) error {
	refused := refusedResult{Error: err.Error()}
	var work WorkRefusal
	if errors.As(err, &work) {
		refused = refusedResult{Error: work.Message, Details: work.Details}
	}
	data, _ := json.Marshal(refused)
	return t.writeSignedReceipt(a, "refused", errorCode(err), data)
}

// signedOutcome answers outcome (Decision 4.3): the receipt of the authorization's request, or
// unknown_request with this computer's clock. The lookup and the clock read are one
// transaction on the store's single connection, so a write that passed its time check has
// committed or rolled back before it.
func (s *Store) signedOutcome(ctx context.Context, key MaintainerKey, request string) (map[string]any, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The envelope's authorization must still be current when the lookup runs (Decision 4.3).
	if err := stillAuthorized(ctx, tx, key); err != nil {
		return nil, err
	}
	now := s.now()
	r, err := signedReceipt(ctx, tx, key.Authorization, request, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	switch {
	case r == nil:
		return map[string]any{"state": "unknown_request", "now": now.Unix()}, nil
	case r.state == "refused":
		var refused refusedResult
		json.Unmarshal(r.result, &refused)
		answer := map[string]any{"state": "refused", "code": r.code}
		if len(refused.Details) > 0 {
			answer["details"] = refused.Details
		}
		return answer, nil
	}
	return map[string]any{"state": "recorded", "result": r.result}, nil
}

// shownSnapshot is the snapshot digest of an item at its revision, with the commands that
// decision_get shows: a command that fails the printable-ASCII rule is hidden there, so it is
// left out here too.
func shownSnapshot(reference string, item DecisionItem) string {
	shown := []string{}
	for _, c := range item.Commands {
		if printableCommand(c) {
			shown = append(shown, c)
		}
	}
	return signed.SnapshotDigest(reference, item.Revision, item.Kind, item.Title, item.Where, item.Context, item.Recommendation, item.AgentText, shown)
}

// SignedSession opens a signed maintainer session from a session envelope (Decision 5.1): the
// envelope's checks of Decision 3.3, then a token with the limits of a maintainer token, for
// the authorization's client. It returns the token and the client, key:CLIENT.
func (d *Daemon) SignedSession(ctx context.Context, data []byte) (string, string, error) {
	e, key, err := d.checkEnvelope(ctx, data)
	if err != nil {
		return "", "", err
	}
	if e.Op != "session" {
		return "", "", Refusal{"invalid_request", "a session opens with a session envelope"}
	}
	args := signedArgs{values: e.Args}
	client := args.str("client")
	if err := args.check(); err != nil {
		return "", "", err
	}
	if !MaintainerClientName.MatchString(client) {
		return "", "", ErrClientName
	}
	if !inWindow(d.store.now(), e.Created) {
		return "", "", signedRefusal("time", "the envelope is not within 5 minutes of this computer's clock")
	}
	a := &signedAction{key: key}
	if err := d.store.auditAccepted(ctx, "maintainer_login", "key:"+key.Client+" by "+a.signerText()); err != nil {
		return "", "", err
	}
	token, err := d.auth.startSigned(key)
	return token, "key:" + key.Client, err
}

// MaintainerTokenHeader carries a signed session's token on the loopback API, whose
// Authorization header holds the daemon's bearer secret.
const MaintainerTokenHeader = "Koinon-Maintainer-Token"

// readEnvelope reads a request body that is one envelope, at most one byte more than an
// envelope's bound, so that Parse refuses a larger one.
func readEnvelope(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Header.Get("Content-Type") != "application/json" {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, signed.MaxEnvelopeBytes+1))
	if err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}

// signedRoutes add the signed session and the signed action routes (Decision 5). The body of
// each is one envelope; the action route takes the session's token in MaintainerTokenHeader.
func (d *Daemon) signedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/maintainer/signed/session", func(w http.ResponseWriter, r *http.Request) {
		data, err := readEnvelope(w, r)
		if err != nil {
			failure(w, err)
			return
		}
		token, client, err := d.SignedSession(r.Context(), data)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "token": token, "client": client,
			"idle_seconds": int64(sessionIdle.Seconds()), "absolute_seconds": int64(sessionAbsolute.Seconds())})
	})
	mux.HandleFunc("POST /v1/maintainer/signed", func(w http.ResponseWriter, r *http.Request) {
		// The token is checked before the envelope is read (Answer shapes, item 2).
		key, ok := d.auth.signedToken(r.Header.Get(MaintainerTokenHeader))
		if ok && stillAuthorized(r.Context(), d.store.db, key) != nil {
			d.auth.endKey(key.KeyID)
			ok = false
		}
		if !ok {
			failure(w, ErrTokenExpired)
			return
		}
		data, err := readEnvelope(w, r)
		if err != nil {
			failure(w, err)
			return
		}
		// The server's write deadline is 10 seconds; a changes wait gets its 30 seconds and 5
		// seconds more.
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add((maxChangeWait + 5) * time.Second))
		result, err := d.SignedAction(r.Context(), data)
		maintainerRead(w, result, err)
	})
}
