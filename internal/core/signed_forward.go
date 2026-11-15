package core

// Signed actions for another computer (#693 chunk 2; decision.md Decisions 4.5, 5.4 and 6.3).
// A node forwards an envelope whose target is a paired computer's pin, unchanged, as the link
// operation maintainer, and returns the target's answer and status unchanged. The target runs
// it as a forwarded action: for its own pin only, never forwarded again, without the node-only
// operations, and with the remote-safe peer projection of PDR 011 Decision 2.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/signed"
)

const (
	// forwardConnect bounds the dial and handshake of a forwarded action.
	forwardConnect = 15 * time.Second
	// forwardExchange bounds a forwarded action's write, run and reply after its wait.
	forwardExchange = 15 * time.Second
	// forwardTotal bounds a forwarded action on the node: connect, the longest changes wait and
	// the exchange. The node's HTTP write deadline and the MCP client's timeout are longer.
	forwardTotal = forwardConnect + maxChangeWait*time.Second + forwardExchange
	// signedRouteWrite is the write deadline of the signed route on the node.
	signedRouteWrite = forwardTotal + 10*time.Second
)

// SignedCallTimeout is the MCP client's time limit for one signed call: longer than the node's
// write deadline, which is longer than a forwarded action.
const SignedCallTimeout = signedRouteWrite + 15*time.Second

// forwardedKey marks a signed action that came over the link; its value is this computer's pin.
type forwardedKey struct{}

func forwardedPin(ctx context.Context) (string, bool) {
	pin, ok := ctx.Value(forwardedKey{}).(string)
	return pin, ok
}

// forwardedAnswer is a target's answer as the node passes it on: its HTTP status and its JSON.
type forwardedAnswer struct {
	status int
	body   json.RawMessage
}

// maintainerFrame is the body of the link operation maintainer: the envelope's exact bytes.
type maintainerFrame struct {
	Envelope string `json:"envelope"`
}

// maintainerReply is the body of maintainer-ok: the target's HTTP status and answer.
type maintainerReply struct {
	Status int             `json:"status"`
	Answer json.RawMessage `json:"answer"`
}

// replyOverhead is the size of a maintainer-ok frame around its answer, with the longest status.
var replyOverhead = func() int {
	data, _ := json.Marshal(link.Frame{Op: "maintainer-ok", Body: mustJSON(maintainerReply{Status: 599, Answer: json.RawMessage("0")})})
	return len(data) - 1
}()

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// signedAnswerLimit is the bound of an answer as respond writes it: 1 MiB on the node, and on a
// target the room that a maintainer-ok frame leaves (respond's newline is not sent).
func signedAnswerLimit(ctx context.Context) int {
	if _, ok := forwardedPin(ctx); ok {
		return link.MaxMaintainerFrameBytes - replyOverhead + 1
	}
	return maxSignedAnswer
}

// The node's answers about a target that it could not ask, or whose answer did not come.
func computerOffline(target string, lastSeen int64, reason string) error {
	details := map[string]any{"target": target, "last_seen": lastSeen}
	if reason != "" {
		details["reason"] = reason
	}
	return WorkRefusal{Code: "computer_offline", Message: "the node could not reach that computer and sent nothing; the action did not run", Details: details}
}

func outcomeUnknown(target, request string) error {
	return WorkRefusal{Code: "outcome_unknown", Message: "the node sent the action and its answer did not come back; read its outcome", Details: map[string]any{"target": target, "request": request}}
}

// definiteLinkRefusals are the link refusals that a target gives before it runs anything, also
// a target that does not know the operation.
var definiteLinkRefusals = map[string]bool{"link_operation_unknown": true, ErrLinkNoPair.Code: true, ErrLinkRemoteOff.Code: true,
	"invalid_request": true, "link_frame_too_large": true}

// signedForward sends an envelope for a paired computer's pin on the link and returns its answer.
func (d *Daemon) signedForward(ctx context.Context, e signed.Envelope, data []byte) (any, error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	o := d.link.observed
	var observed linkObservation
	i := -1
	if err == nil {
		i = slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Pin == e.Target && p.State == link.Paired })
		if i >= 0 {
			observed = o[s.Pairs[i].ID]
		}
	}
	d.link.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if i < 0 {
		return nil, signedRefusal("target", "the target is neither this computer nor a paired one")
	}
	// The target comes first (Decision 3.3): the node's own operations are refused only for a
	// paired computer's pin.
	if e.Op == "session" || e.Op == "computers" {
		return nil, Refusal{"invalid_request", e.Op + " is an operation of the node: sign it for the node's own pin"}
	}
	p := s.Pairs[i]
	frame, err := link.NewFrame("maintainer", maintainerFrame{Envelope: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return nil, err
	}
	if encoded, _ := json.Marshal(frame); len(encoded) > link.FrameLimit(frame.Op) {
		return nil, ErrSignedTooLarge
	}
	life, gen, err := d.linkLife()
	if err != nil {
		return nil, computerOffline(e.Target, observed.Connected, errorCode(err))
	}
	connectCtx, cancel := context.WithTimeout(ctx, forwardConnect)
	stop := context.AfterFunc(life, cancel)
	session, err := d.connectPair(connectCtx, gen, p)
	stop()
	cancel()
	if err != nil {
		return nil, computerOffline(e.Target, observed.Connected, errorCode(err))
	}
	defer session.close()
	// The pair can have gone while the node connected. The connection is tracked by now, so an
	// unpair that commits after this check closes it (closeLinkPin).
	if err := d.pairStands(gen, p); err != nil {
		if errors.Is(err, ErrLinkNoPair) {
			return nil, signedRefusal("target", "the target is neither this computer nor a paired one")
		}
		return nil, computerOffline(e.Target, observed.Connected, errorCode(err))
	}
	wait := int64(0)
	if e.Op == "changes" {
		if w, ok := e.Args["wait_seconds"].(int64); ok {
			wait = min(max(w, 0), maxChangeWait)
		}
	}
	session.conn.SetDeadline(time.Now().Add(time.Duration(wait)*time.Second + forwardExchange))
	// A request that ends ends the exchange too: its connection's deadline passes at once.
	defer context.AfterFunc(ctx, func() { session.conn.SetDeadline(time.Unix(1, 0)) })()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// From the first byte written the target can run the action: every failure after this
	// point leaves its outcome unknown.
	if err := link.WriteFrame(session.conn, frame); err != nil {
		return nil, outcomeUnknown(e.Target, e.Request)
	}
	if err := d.linkFault("maintainer-after-write"); err != nil {
		return nil, outcomeUnknown(e.Target, e.Request)
	}
	reply, err := link.ReadFrame(session.conn)
	if err != nil {
		return nil, outcomeUnknown(e.Target, e.Request)
	}
	if reply.Op == "error" {
		if definiteLinkRefusals[reply.Error] {
			return nil, WorkRefusal{Code: reply.Error, Message: reply.Detail, Details: map[string]any{"target": e.Target}}
		}
		return nil, outcomeUnknown(e.Target, e.Request)
	}
	answer, ok := validReply(reply)
	if !ok {
		return nil, outcomeUnknown(e.Target, e.Request)
	}
	return answer, nil
}

// validReply takes a maintainer-ok reply that has the form of a route's answer: a JSON object
// whose ok is true with status 200 and a result, or false with a refusal status and a code.
// Its bytes stay as they came.
func validReply(reply link.Frame) (forwardedAnswer, bool) {
	var answer maintainerReply
	if reply.Op != "maintainer-ok" || reply.Decode(&answer) != nil {
		return forwardedAnswer{}, false
	}
	var shape struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Code   *string         `json:"code"`
	}
	trimmed := bytes.TrimSpace(answer.Answer)
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, &shape) != nil || shape.OK == nil {
		return forwardedAnswer{}, false
	}
	if *shape.OK != (answer.Status == http.StatusOK) || answer.Status < 200 || answer.Status > 599 {
		return forwardedAnswer{}, false
	}
	if *shape.OK && len(shape.Result) == 0 || !*shape.OK && (shape.Code == nil || *shape.Code == "") {
		return forwardedAnswer{}, false
	}
	return forwardedAnswer{status: answer.Status, body: answer.Answer}, true
}

// pairStands checks that pair p is still paired, with its key, in generation gen.
func (d *Daemon) pairStands(gen uint64, p link.Pair) error {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.gen != gen {
		return ErrLinkRemoteOff
	}
	s, err := link.ReadState(d.root)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(s.Pairs, func(q link.Pair) bool { return q.ID == p.ID && q.Pin == p.Pin && q.State == link.Paired }) {
		return ErrLinkNoPair
	}
	return nil
}

// answerMaintainer runs the link operation maintainer of a paired key: the envelope runs here,
// for this computer's pin only. A write commits only while the pair and the generation stand, as
// every inbound write does; a read holds no link lock while it waits.
func (d *Daemon) answerMaintainer(pin string, f link.Frame, gen uint64) (link.Frame, bool, error) {
	var body maintainerFrame
	if f.Decode(&body) != nil || len(body.Envelope) > base64.StdEncoding.EncodedLen(signed.MaxEnvelopeBytes+1) {
		return link.Frame{}, true, ErrInvalid
	}
	data, err := base64.StdEncoding.DecodeString(body.Envelope)
	if err != nil {
		return link.Frame{}, true, ErrInvalid
	}
	id, err := d.linkIdentity()
	if err != nil {
		return link.Frame{}, true, err
	}
	paired := func() error {
		if d.link.gen != gen {
			return ErrLinkRemoteOff
		}
		s, err := link.ReadState(d.root)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.Pin == pin && p.State == link.Paired }) {
			return ErrLinkNoPair
		}
		return nil
	}
	d.link.mu.Lock()
	err = paired()
	d.link.mu.Unlock()
	if err != nil {
		return link.Frame{}, true, err
	}
	life, err := d.linkLifeOf(gen)
	if err != nil {
		return link.Frame{}, true, err
	}
	ctx, cancel := context.WithTimeout(life, maxChangeWait*time.Second+forwardExchange)
	defer cancel()
	ctx = context.WithValue(ctx, forwardedKey{}, id.Pin)
	op := ""
	if e, err := signed.Parse(data); err == nil {
		op = e.Op
	}
	var result any
	if signedWrites[op] {
		if err := d.linkFault("maintainer-before-write"); err != nil {
			return link.Frame{}, true, err
		}
		d.link.mu.Lock()
		if err := paired(); err != nil {
			d.link.mu.Unlock()
			return link.Frame{}, true, err
		}
		result, err = d.SignedAction(ctx, data)
		d.link.mu.Unlock()
	} else {
		result, err = d.SignedAction(ctx, data)
	}
	w := &answerWriter{header: http.Header{}}
	maintainerRead(w, result, err)
	reply, err := link.NewFrame("maintainer-ok", maintainerReply{Status: w.status, Answer: trimNewline(w.body)})
	return reply, false, err
}

func trimNewline(b []byte) json.RawMessage {
	for len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

// answerWriter keeps the status and body that a route handler writes, so that a forwarded
// answer is the one that the target's own route gives.
type answerWriter struct {
	header http.Header
	status int
	body   []byte
}

func (w *answerWriter) Header() http.Header { return w.header }

func (w *answerWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *answerWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body = append(w.body, p...)
	return len(p), nil
}

// remotePeers answers peers for another computer: the entries of this computer's directory,
// which hold only what PDR 011 Decision 2 lets cross.
func (d *Daemon) remotePeers(ctx context.Context) (linkDirectory, error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return linkDirectory{}, err
	}
	return d.linkDirectoryOf(ctx, s.Name)
}

// remotePeer answers peer_status for another computer: one directory entry by name or address.
func (d *Daemon) remotePeer(ctx context.Context, name string) (LinkEntry, error) {
	dir, err := d.remotePeers(ctx)
	if err != nil {
		return LinkEntry{}, err
	}
	for _, entry := range dir.Entries {
		if entry.Name == name || entry.Address != "" && entry.Address == name {
			return entry, nil
		}
	}
	return LinkEntry{}, ErrPeerNotFound
}
