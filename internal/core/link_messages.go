package core

// Messages between paired computers (#670 chunk 03, LINK-670-WHAT items 1, 3 and 4,
// LINK-670-OFFLINE). A send to NAME@COMPUTER goes into this daemon's outbox; one worker for
// each paired computer delivers the pair's messages in order on the link, one at a time, and
// stores each result before the next message (the stream rules of definition-of-done.md).

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/link"
)

// linkBackoff is the first wait after a busy answer or a lost connection; it doubles to
// linkBackoffMax.
const (
	linkBackoff    = time.Second
	linkBackoffMax = time.Minute
)

// The fault points' answers, for tests: errLinkFault stops the worker at once with no further
// write, as a process that ends there does; at the receiver, errLinkRetry answers retry as a
// busy store does, and errLinkDrop closes the connection before any commit.
var (
	errLinkFault = errors.New("link fault point")
	errLinkRetry = errors.New("link fault retry")
	errLinkDrop  = errors.New("link fault drop")
)

// linkDeliverBody is the deliver operation. The body is bytes, so JSON carries it as base64
// and the largest valid body fits one frame whatever its characters.
type linkDeliverBody struct {
	ID        string `json:"id"`
	Seq       int64  `json:"seq"`
	XID       string `json:"xid"`
	Recipient string `json:"recipient"`
	Sender    string `json:"sender"`
	Family    string `json:"family"`
	Body      []byte `json:"body"`
	Digest    string `json:"digest"`
}

// linkCancelBody is the ordered cancel of an attempted message (stream rule 4).
type linkCancelBody struct {
	ID     string `json:"id"`
	Seq    int64  `json:"seq"`
	Digest string `json:"digest"`
}

// linkDeliverReply answers deliver and cancel. It echoes the pair, sequence and digest, so a
// late answer never resolves another message.
type linkDeliverReply struct {
	ID     string `json:"id"`
	Seq    int64  `json:"seq"`
	Digest string `json:"digest"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

var ErrLinkProtocol = Refusal{"link_protocol_error", "the other computer sent an operation out of order or with a changed message"}

func validXID(x string) bool {
	b, err := hex.DecodeString(x)
	return err == nil && len(b) == 16 && hex.EncodeToString(b) == x
}

// answerDeliver is the receiver's deliver or cancel: only for a paired key and its own pair
// ID, and only while the connection's generation is the open link's.
func (d *Daemon) answerDeliver(pin string, f link.Frame, gen uint64) (link.Frame, bool, error) {
	var in linkDelivery
	var digest string
	if f.Op == "cancel" {
		var body linkCancelBody
		if f.Decode(&body) != nil || body.Seq < 1 {
			return link.Frame{}, true, ErrInvalid
		}
		in = linkDelivery{Pair: body.ID, Cancel: true, Seq: body.Seq, Digest: body.Digest}
		digest = body.Digest
	} else {
		var body linkDeliverBody
		// An empty or non-agent family is a stored origin refusal, not a malformed frame, so
		// the stream goes on (A6).
		if f.Decode(&body) != nil || body.Seq < 1 || !validXID(body.XID) || len(body.Family) > 64 ||
			body.Recipient == "" || len(body.Recipient) > 256 || body.Sender == "" || len(body.Sender) > 256 ||
			len(body.Body) == 0 || len(body.Body) > maxBody || !utf8.Valid(body.Body) {
			return link.Frame{}, true, ErrInvalid
		}
		text := string(body.Body)
		digest = linkDigest(body.Seq, body.XID, body.Recipient, body.Sender, body.Family, text)
		if digest != body.Digest {
			return link.Frame{}, true, ErrLinkProtocol
		}
		in = linkDelivery{Pair: body.ID, Seq: body.Seq, XID: body.XID, Recipient: body.Recipient, Sender: body.Sender,
			Family: body.Family, Body: text, Digest: digest}
	}
	switch err := d.linkFault("before-receive"); {
	case errors.Is(err, errLinkRetry):
		reply, err := link.NewFrame("deliver-ok", linkDeliverReply{ID: in.Pair, Seq: in.Seq, Digest: digest, Result: "retry", Reason: "storage_busy"})
		return reply, false, err
	case err != nil:
		return link.Frame{}, true, err
	}
	// The link lock fences the commit to this generation, as for every inbound write.
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
	i, ok := pairedPin(s, pin, in.Pair)
	if !ok {
		d.link.mu.Unlock()
		return link.Frame{}, true, ErrLinkNoPair
	}
	in.Computer = s.Pairs[i].Effective
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout/2)
	result, err := d.store.linkReceive(ctx, in)
	cancel()
	d.link.mu.Unlock()
	if errors.Is(err, errLinkProtocol) {
		return link.Frame{}, true, ErrLinkProtocol
	}
	if err != nil {
		return link.Frame{}, true, err
	}
	reply, err := link.NewFrame("deliver-ok", linkDeliverReply{ID: in.Pair, Seq: in.Seq, Digest: digest, Result: result.Result, Reason: result.Reason})
	return reply, false, err
}

// linkSend queues a send to NAME@COMPUTER, or a reply to a received message's sender on the
// pair it came from (A1 of the chunk's criteria). The caller and the body are checked as for
// any send. The maintainer sends only on this computer (LINK-670-WHAT item 5).
func (d *Daemon) linkSend(ctx context.Context, caller *Key, to, body string, replyTo int64) (Outcome, error) {
	if body == "" || len(body) > maxBody || !utf8.ValidString(body) {
		return Outcome{}, ErrInvalid
	}
	if caller == nil {
		return Outcome{}, ErrLinkMaintainerRemote
	}
	if err := d.store.activeCaller(ctx, *caller, d.store.now().UnixMilli()); err != nil {
		return Outcome{}, err
	}
	var pairID, name, computer string
	if replyTo != 0 {
		var err error
		if pairID, name, err = d.store.linkReplyTarget(ctx, *caller, replyTo); err != nil {
			return Outcome{}, err
		}
	} else {
		var err error
		if name, computer, err = link.ParseAddress(to); err != nil {
			return Outcome{}, ErrLinkAddressInvalid
		}
	}
	// The outbox lock orders this enqueue with an unpair's retirement: a message queued just
	// before an unpair is retired with the pair's other messages.
	d.link.outbox.Lock()
	defer d.link.outbox.Unlock()
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return Outcome{}, err
	}
	var i int
	if replyTo != 0 {
		if i = slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == pairID }); i < 0 {
			return Outcome{}, ErrLinkReplyPairGone
		}
		if to != "" && to != name+"@"+s.Pairs[i].Effective {
			return Outcome{}, ErrLinkReplyTarget
		}
	} else if i = slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Effective == computer }); i < 0 {
		return Outcome{}, ErrLinkComputerUnknown
	}
	p := s.Pairs[i]
	outcome, err := d.store.linkEnqueue(ctx, *caller, p.ID, p.Effective, name, body)
	if err != nil {
		return Outcome{}, err
	}
	d.linkKick(p.ID)
	return outcome, nil
}

// linkCancelMessage is the maintainer's cancel of a queued message.
func (d *Daemon) linkCancelMessage(ctx context.Context, xid string) (LinkQueued, error) {
	if !validXID(xid) {
		return LinkQueued{}, ErrLinkMessageGone
	}
	d.link.outbox.Lock()
	pair, q, err := d.store.linkCancel(ctx, xid)
	d.link.outbox.Unlock()
	if err == nil {
		d.linkKick(pair)
	}
	return q, err
}

// linkRetirePair ends a removed pair's queue. The link state is written first, so a crash
// between the two writes is finished by linkReconcile.
func (d *Daemon) linkRetirePair(id string) error {
	d.link.outbox.Lock()
	defer d.link.outbox.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	return d.store.linkRetire(ctx, id)
}

// linkRetireIfGone retires a pair's queue when the current link state, read under the outbox
// lock, does not list the pair. A snapshot read earlier can precede a new pairing and its first
// send; this read cannot, because every send reads the link state under the same lock.
func (d *Daemon) linkRetireIfGone(id string) error {
	d.link.outbox.Lock()
	defer d.link.outbox.Unlock()
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return err
	}
	if slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id }) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	return d.store.linkRetire(ctx, id)
}

// linkReconcile retires the queue of every pair that the link state no longer lists, then
// starts a worker for each paired computer with unfinished messages. It runs when the link
// opens.
func (d *Daemon) linkReconcile(gen uint64) {
	defer d.wg.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 2*linkIOTimeout)
	defer cancel()
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return
	}
	ids, err := d.store.linkPairIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if !slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id }) {
			d.linkRetireIfGone(id)
		}
	}
	d.linkKickPending(gen)
}

// linkKickPending starts or wakes the worker of each paired computer with unfinished
// messages, in generation gen.
func (d *Daemon) linkKickPending(gen uint64) {
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	pending, err := d.store.linkPending(ctx)
	if err != nil || len(pending) == 0 {
		return
	}
	d.link.mu.Lock()
	open := d.link.gen == gen && d.link.listener != nil
	d.link.mu.Unlock()
	if !open {
		return
	}
	for id := range pending {
		d.linkKick(id)
	}
}

// linkKick wakes the pair's worker of the open generation, or starts one.
func (d *Daemon) linkKick(id string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.listener == nil || d.link.life == nil {
		return
	}
	if ch, ok := d.link.workers[id]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
		return
	}
	if d.link.workers == nil {
		d.link.workers = map[string]chan struct{}{}
	}
	ch := make(chan struct{}, 1)
	d.link.workers[id] = ch
	d.wg.Add(1)
	go d.linkWorker(d.link.gen, d.link.life, id, ch)
}

func (d *Daemon) linkFault(point string) error {
	d.link.mu.Lock()
	hook := d.link.hook
	d.link.mu.Unlock()
	if hook != nil {
		return hook(point)
	}
	return nil
}

// linkWorker delivers one pair's messages in generation gen until the queue is empty, the
// pair is gone or the generation closes.
func (d *Daemon) linkWorker(gen uint64, life context.Context, id string, kick chan struct{}) {
	defer d.wg.Done()
	defer func() {
		d.link.mu.Lock()
		if d.link.workers[id] == kick {
			delete(d.link.workers, id)
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
		more, ok, err := d.linkDeliverBatch(gen, life, id)
		switch {
		case errors.Is(err, errLinkFault):
			return
		case !more:
			if d.linkIdle(gen, id, kick) {
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

// linkIdle ends the worker when the pair has no unfinished message, under the outbox lock, so
// that a send that queues after this check starts a new worker.
func (d *Daemon) linkIdle(gen uint64, id string, kick chan struct{}) bool {
	d.link.outbox.Lock()
	defer d.link.outbox.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	pending, err := d.store.linkPending(ctx)
	if err == nil && pending[id] {
		return false
	}
	d.link.mu.Lock()
	if d.link.workers[id] == kick {
		delete(d.link.workers, id)
	}
	d.link.mu.Unlock()
	return true
}

// linkDeliverBatch connects once and delivers heads until the queue is empty (more false),
// or until an answer asks to wait (ok false). ok true with more true means progress.
func (d *Daemon) linkDeliverBatch(gen uint64, life context.Context, id string) (more, ok bool, err error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return true, false, nil
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id })
	if i < 0 {
		// A pair that is gone without an unpair here (a pending pair that expired or was
		// cancelled) ends its queue as an unpair does.
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
	// The other computer closes a connection after linkMaxFramesConn frames; the worker
	// connects again before that, so no answer is lost to the limit.
	for frames := 0; ; frames++ {
		if frames == linkMaxFramesConn-4 {
			return true, true, nil
		}
		h, found, err := d.linkPrepareHead(gen, id)
		if err != nil {
			return true, progress, nil
		}
		if !found {
			return false, progress, nil
		}
		var reply linkDeliverReply
		var callErr error
		if h.Cancel {
			callErr = d.linkCall(session, "cancel", linkCancelBody{ID: id, Seq: h.Seq, Digest: h.Digest}, &reply)
		} else {
			callErr = d.linkCall(session, "deliver", linkDeliverBody{ID: id, Seq: h.Seq, XID: h.XID, Recipient: h.Recipient,
				Sender: h.Sender, Family: h.Family, Body: []byte(h.Body), Digest: h.Digest}, &reply)
		}
		if errors.Is(callErr, errLinkFault) {
			return true, false, errLinkFault
		}
		// A lost or mismatched answer leaves the head uncertain, as its attempt stored it.
		if callErr != nil || reply.ID != id || reply.Seq != h.Seq || reply.Digest != h.Digest {
			return true, false, nil
		}
		switch reply.Result {
		case "received", "refused", "cancelled":
			if !d.linkStore(gen, id, func(ctx context.Context) error {
				_, err := d.store.linkSettle(ctx, id, h.Seq, h.Digest, reply.Result, reply.Reason)
				return err
			}) {
				return true, false, nil
			}
			progress = true
		case "retry":
			d.linkStore(gen, id, func(ctx context.Context) error {
				_, err := d.store.linkRetried(ctx, id, h.Seq, h.Digest)
				return err
			})
			return true, false, nil
		default:
			// stale: an answer for an earlier sequence than the head's; it resolves nothing.
			return true, false, nil
		}
	}
}

// linkCall writes one frame and reads its answer, with the after-write fault point between.
func (d *Daemon) linkCall(session linkSession, op string, body any, out *linkDeliverReply) error {
	f, err := link.NewFrame(op, body)
	if err != nil {
		return err
	}
	session.conn.SetDeadline(time.Now().Add(linkIOTimeout))
	if err := link.WriteFrame(session.conn, f); err != nil {
		return err
	}
	if err := d.linkFault("after-write"); err != nil {
		return err
	}
	reply, err := link.ReadFrame(session.conn)
	if err != nil {
		return err
	}
	if reply.Op == "error" {
		return Refusal{reply.Error, reply.Detail}
	}
	if reply.Op != "deliver-ok" {
		return errors.New("link_reply_invalid")
	}
	return reply.Decode(out)
}

// linkPrepareHead takes the pair's head and stores its attempt before any frame is written
// (A9): from here a crash, Remote off or a closed generation leaves it uncertain.
func (d *Daemon) linkPrepareHead(gen uint64, id string) (linkHead, bool, error) {
	var h linkHead
	var found bool
	ok := d.linkStore(gen, id, func(ctx context.Context) error {
		var err error
		if h, found, err = d.store.linkNextHead(ctx, id); err != nil || !found {
			return err
		}
		attempted, err := d.store.linkAttempt(ctx, id, h.Seq, h.Digest)
		if err == nil && !attempted {
			err = errors.New("link head changed")
		}
		return err
	})
	if !ok {
		return linkHead{}, false, errors.New("link store refused")
	}
	return h, found, nil
}

// linkStore runs one outbox write of generation gen under the outbox lock, only while gen is
// the open link's and the current link state lists pair id as paired: an old generation's
// worker, and a worker whose pair was unpaired, store nothing. An unpair writes the link state
// before it retires the queue under the same lock, so no attempt begins after an unpair.
func (d *Daemon) linkStore(gen uint64, id string, write func(ctx context.Context) error) bool {
	d.link.outbox.Lock()
	defer d.link.outbox.Unlock()
	if _, err := d.linkLifeOf(gen); err != nil {
		return false
	}
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil || !slices.ContainsFunc(s.Pairs, func(p link.Pair) bool { return p.ID == id && p.State == link.Paired }) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), linkIOTimeout)
	defer cancel()
	return write(ctx) == nil
}

// LinkQueue is the queue to one paired computer: its unfinished messages and the finished ones
// that retention keeps, without bodies.
type LinkQueue struct {
	Pair     string       `json:"pair"`
	Computer string       `json:"computer"`
	Messages []LinkQueued `json:"messages"`
	// Waiting and Finished count all of the computer's messages; Truncated says that
	// Messages leaves out older finished ones.
	Waiting   int  `json:"waiting"`
	Finished  int  `json:"finished"`
	Truncated bool `json:"truncated,omitempty"`
}

// linkQueueShown is the most finished entries shown for one computer. Every waiting message
// is shown, at most the per-computer limit.
const linkQueueShown = 200

// linkQueues lists the queue of each paired computer, or of the one that ref names.
func (d *Daemon) linkQueues(ctx context.Context, ref string) ([]LinkQueue, error) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return nil, err
	}
	pairs := s.Pairs
	if ref != "" {
		i := findPair(s, ref)
		if i < 0 {
			return nil, ErrLinkNoPair
		}
		pairs = pairs[i : i+1]
	}
	queues := []LinkQueue{}
	for _, p := range pairs {
		rows, waiting, finished, err := d.store.linkQueue(ctx, p.ID, int(linkQueuePairCount), linkQueueShown)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []LinkQueued{}
		}
		queues = append(queues, LinkQueue{Pair: p.ID, Computer: p.Effective, Messages: rows, Waiting: waiting, Finished: finished,
			Truncated: len(rows) < waiting+finished})
	}
	return queues, nil
}
