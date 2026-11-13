package core

// The stream rules under faults (#670 chunk 03; criteria M5 to M8, A4 to A9a): crash points,
// lost answers, busy answers, delayed old handlers, cancel, unpair and Remote off.

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

func setLinkHook(h *linkHost, f func(point string) error) {
	h.d.link.mu.Lock()
	h.d.link.hook = f
	h.d.link.mu.Unlock()
}

// fastLinkHosts pairs alpha with beta, with short worker waits, and joins one sender on
// alpha and one receiver on beta.
func fastLinkHosts(t *testing.T) (*linkNet, *linkHost, *linkHost, Session, Session) {
	t.Helper()
	n, a, b := pairedLinkHosts(t)
	for _, h := range []*linkHost{a, b} {
		h.d.link.mu.Lock()
		h.d.link.backoff = 20 * time.Millisecond
		h.d.link.mu.Unlock()
	}
	sender := joinAs(t, a.d.store, "codex", "synthetic-sender", linkRepo(t, "send"), "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-receiver", linkRepo(t, "recv"), "")
	return n, a, b, sender, receiver
}

func linkStateOf(t *testing.T, h *linkHost, s Session, id string) string {
	t.Helper()
	status, reply := callerPost(t, h, "/v1/messages/outcome", map[string]any{"caller": agentKey(s), "link_id": id})
	if status != 200 {
		t.Fatalf("delivery %s: %d %v", id, status, reply)
	}
	m, _ := reply["message"].(map[string]any)
	return m["delivery_state"].(string)
}

func waitLinkStateIs(t *testing.T, h *linkHost, s Session, id, want string) {
	t.Helper()
	waitFor(t, "message "+id+" never reached "+want+", last "+linkStateOf(t, h, s, id), func() bool {
		return linkStateOf(t, h, s, id) == want
	})
}

func bodies(t *testing.T, h *linkHost, s Session) []string {
	t.Helper()
	var out []string
	for _, m := range readInbox(t, h, s) {
		out = append(out, m["body"].(string))
	}
	return out
}

func cycleRemote(t *testing.T, h *linkHost) {
	t.Helper()
	ctx := context.Background()
	if err := h.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
}

// A refusal settles its message and the next one goes; the order is the send order.
func TestLinkMessageOrderAndRefusal(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	to := receiver.Name + "@beta"
	ids := []string{mustQueue(t, a, sender, to, "one"), mustQueue(t, a, sender, "nobody@beta", "two"), mustQueue(t, a, sender, to, "three")}
	for i, want := range []string{"received", "refused", "received"} {
		waitLinkStateIs(t, a, sender, ids[i], want)
	}
	status, reply := callerPost(t, a, "/v1/messages/outcome", map[string]any{"caller": agentKey(sender), "link_id": ids[1]})
	if m, _ := reply["message"].(map[string]any); status != 200 || m["delivery_reason"] != "peer_not_found" {
		t.Fatalf("refusal reason: %v", reply)
	}
	if got := strings.Join(bodies(t, b, receiver), ","); got != "one,three" {
		t.Fatalf("inbox order %s", got)
	}
}

// A crash right after the frame write (the fault point stops the worker with no further
// write) leaves the message "may have been delivered"; the next generation sends the same
// sequence again and it is received exactly once (A9). Remote disable in the same window
// has the same result.
func TestLinkMessageCrashAfterWrite(t *testing.T) {
	for _, mode := range []string{"crash", "disable"} {
		t.Run(mode, func(t *testing.T) {
			_, a, b, sender, receiver := fastLinkHosts(t)
			var once sync.Once
			inWindow := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			setLinkHook(a, func(point string) error {
				if point != "after-write" {
					return nil
				}
				var stop error
				once.Do(func() {
					close(inWindow)
					if mode == "crash" {
						stop = errLinkFault
						return
					}
					<-release
				})
				return stop
			})
			id := mustQueue(t, a, sender, receiver.Name+"@beta", "once only")
			<-inWindow
			if mode == "disable" {
				if err := a.d.remoteDisable(context.Background()); err != nil {
					t.Fatal(err)
				}
				releaseOnce.Do(func() { close(release) })
			}
			waitFor(t, "receiver commit", func() bool { return len(bodies(t, b, receiver)) == 1 })
			if got := linkStateOf(t, a, sender, id); got != "may_have_been_delivered" {
				t.Fatalf("after the window: %s", got)
			}
			if mode == "crash" {
				cycleRemote(t, a)
			} else if err := a.d.remoteEnable(context.Background(), 0); err != nil {
				t.Fatal(err)
			}
			waitLinkStateIs(t, a, sender, id, "received")
			if got := bodies(t, b, receiver); len(got) != 1 {
				t.Fatalf("inbox %v", got)
			}
		})
	}
}

// After a lost receipt, a busy answer does not return the message to queued (A9a), and an
// older handler that commits after that busy answer resolves it, once.
func TestLinkMessageLostReceiptThenBusy(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	// Attempt 1: the receiver's handler is held before its commit; the sender stops after
	// its write.
	first := make(chan struct{})
	holdFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(holdFirst) }) }
	// A failed check must not leave the receiver's handler held: the daemon's close waits
	// for it.
	t.Cleanup(release)
	var calls sync.Mutex
	n := 0
	setLinkHook(b, func(point string) error {
		if point != "before-receive" {
			return nil
		}
		calls.Lock()
		n++
		k := n
		calls.Unlock()
		switch k {
		case 1:
			close(first)
			<-holdFirst
			return nil
		case 2:
			return errLinkRetry
		}
		return nil
	})
	var senderOnce sync.Once
	setLinkHook(a, func(point string) error {
		var stop error
		if point == "after-write" {
			senderOnce.Do(func() { stop = errLinkFault })
		}
		return stop
	})
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "late but once")
	<-first
	// Attempt 2 in a new generation gets busy: the earlier attempt is unknown, so the state
	// stays "may have been delivered".
	setLinkHook(a, nil)
	a.d.link.mu.Lock()
	a.d.link.backoff = time.Hour
	a.d.link.mu.Unlock()
	cycleRemote(t, a)
	waitFor(t, "busy answer", func() bool {
		calls.Lock()
		defer calls.Unlock()
		return n >= 2
	})
	time.Sleep(100 * time.Millisecond)
	if got := linkStateOf(t, a, sender, id); got != "may_have_been_delivered" {
		t.Fatalf("after busy: %s", got)
	}
	// The older handler commits now; the next attempt reads its stored result.
	release()
	waitFor(t, "late commit", func() bool { return len(bodies(t, b, receiver)) == 1 })
	a.d.link.mu.Lock()
	a.d.link.backoff = 20 * time.Millisecond
	a.d.link.mu.Unlock()
	cycleRemote(t, a)
	waitLinkStateIs(t, a, sender, id, "received")
	if got := bodies(t, b, receiver); len(got) != 1 {
		t.Fatalf("inbox %v", got)
	}
}

// A busy answer to a first attempt keeps the message queued and is retried.
func TestLinkMessageBusyIsRetried(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	var mu sync.Mutex
	busy := 3
	setLinkHook(b, func(point string) error {
		mu.Lock()
		defer mu.Unlock()
		if point == "before-receive" && busy > 0 {
			busy--
			return errLinkRetry
		}
		return nil
	})
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "after busy")
	waitLinkStateIs(t, a, sender, id, "received")
	if got := bodies(t, b, receiver); len(got) != 1 {
		t.Fatalf("inbox %v", got)
	}
}

func maintainerQueue(t *testing.T, h *linkHost) []any {
	t.Helper()
	token := newMaintainerToken(t, h.d, linkBearer(t, h), "link-test")
	status, reply := maintainerCall(t, h.d, linkBearer(t, h), token, "link-queue", nil)
	if status != 200 {
		t.Fatalf("queue: %d %v", status, reply)
	}
	return reply["result"].([]any)
}

func cancelMessage(t *testing.T, h *linkHost, id string) {
	t.Helper()
	token := newMaintainerToken(t, h.d, linkBearer(t, h), "link-test")
	mustMaintainer(t, h.d, linkBearer(t, h), token, "link-cancel-message", map[string]any{"link_id": id})
}

// Cancel: a queued message and a middle one end at once; the others go in order.
func TestLinkMessageCancelQueued(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	to := receiver.Name + "@beta"
	ids := []string{mustQueue(t, a, sender, to, "one"), mustQueue(t, a, sender, to, "two"), mustQueue(t, a, sender, to, "three")}
	waitFor(t, "first attempt", func() bool { return linkStateOf(t, a, sender, ids[0]) == "queued" })
	cancelMessage(t, a, ids[1])
	if got := linkStateOf(t, a, sender, ids[1]); got != "cancelled" {
		t.Fatalf("middle: %s", got)
	}
	queues := maintainerQueue(t, a)
	if q := queues[0].(map[string]any); len(q["messages"].([]any)) != 3 || q["computer"] != "beta" {
		t.Fatalf("queue %v", queues)
	}
	// The dashboard lists the queue and cancels one more.
	fourth := mustQueue(t, a, sender, to, "four")
	c := newActionClient(t, a.d, a.root)
	page := dashboardDo(t, a.d, "GET", "/dashboard/settings", c.cookie, nil, nil)
	if !strings.Contains(page.body, "Messages to beta") || !strings.Contains(page.body, fourth) {
		t.Fatal("settings page lacks the queue")
	}
	if code := c.do("link-cancel-message", url.Values{"link_id": {fourth}}); code != "link_message_cancelled" {
		t.Fatal(code)
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	a.d.linkKick(a.state(t).Pairs[0].ID)
	waitLinkStateIs(t, a, sender, ids[2], "received")
	if got := strings.Join(bodies(t, b, receiver), ","); got != "one,three" {
		t.Fatalf("inbox %s", got)
	}
	if got := linkStateOf(t, a, sender, fourth); got != "cancelled" {
		t.Fatalf("dashboard cancel: %s", got)
	}
}

// Cancel of an attempted head: before the receiver's commit it ends cancelled there; after
// the commit, with the cancel's answer lost once, the stored result stays received. Later
// messages wait until the cancel is resolved (stream rule 4).
func TestLinkMessageCancelAttemptedHead(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := map[bool]string{false: "before-commit", true: "after-commit"}[committed]
		t.Run(name, func(t *testing.T) {
			_, a, b, sender, receiver := fastLinkHosts(t)
			var mu sync.Mutex
			writes, answers := 0, false
			hold := make(chan struct{})
			var holdOnce sync.Once
			open := func() { holdOnce.Do(func() { close(hold) }) }
			t.Cleanup(open)
			setLinkHook(b, func(point string) error {
				if point == "before-receive" && !committed {
					<-hold
					return errLinkDrop
				}
				return nil
			})
			setLinkHook(a, func(point string) error {
				if point != "after-write" {
					return nil
				}
				mu.Lock()
				writes++
				k, open := writes, answers
				mu.Unlock()
				// The deliver's answer is lost; with a commit, every answer until the test
				// opens them, so the cancel's answers are lost too.
				if k == 1 || committed && !open {
					return errLinkFault
				}
				return nil
			})
			to := receiver.Name + "@beta"
			head := mustQueue(t, a, sender, to, "maybe")
			waitFor(t, "first write", func() bool { mu.Lock(); defer mu.Unlock(); return writes == 1 })
			later := mustQueue(t, a, sender, to, "later")
			cancelMessage(t, a, head)
			if got := linkStateOf(t, a, sender, head); got != "may_have_been_delivered" {
				t.Fatalf("cancel requested: %s", got)
			}
			if !committed {
				open()
				setLinkHook(b, nil)
			} else {
				waitFor(t, "commit", func() bool { return len(bodies(t, b, receiver)) == 1 })
			}
			cycleRemote(t, a)
			if committed {
				// The cancel is sent and its answer is lost: the head stays unresolved and
				// the later message is never sent.
				start := 0
				mu.Lock()
				start = writes
				mu.Unlock()
				waitFor(t, "lost cancel answers", func() bool { mu.Lock(); defer mu.Unlock(); return writes >= start+1 })
				if got := linkStateOf(t, a, sender, later); got != "queued" {
					t.Fatalf("later message did not wait: %s", got)
				}
				if got := linkStateOf(t, a, sender, head); got != "may_have_been_delivered" {
					t.Fatalf("head while its cancel waits: %s", got)
				}
				mu.Lock()
				answers = true
				mu.Unlock()
				cycleRemote(t, a)
			}
			want := map[bool]string{false: "cancelled", true: "received"}[committed]
			waitLinkStateIs(t, a, sender, head, want)
			waitLinkStateIs(t, a, sender, later, "received")
			wantInbox := map[bool]string{false: "later", true: "maybe,later"}[committed]
			if got := strings.Join(bodies(t, b, receiver), ","); got != wantInbox {
				t.Fatalf("inbox %s", got)
			}
		})
	}
}

// Unpair: a message without a sequence is cancelled and an attempted one may have been
// delivered; a new pairing does not revive them, and an old reply never reaches the new pair
// of the same name (A1, M8).
func TestLinkMessageUnpairAndRepair(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	to := receiver.Name + "@beta"
	first := mustQueue(t, a, sender, to, "first")
	waitLinkStateIs(t, a, sender, first, "received")
	// A message from beta, for the reply test after re-pairing.
	in := mustQueue(t, b, receiver, fromName(sender)+"@alpha", "question")
	waitLinkStateIs(t, b, receiver, in, "received")
	question := int64(readInbox(t, a, sender)[0]["id"].(float64))

	var once sync.Once
	setLinkHook(a, func(point string) error {
		var stop error
		if point == "after-write" {
			once.Do(func() { stop = errLinkFault })
		}
		return stop
	})
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	attempted := mustQueue(t, a, sender, to, "attempted")
	waitFor(t, "attempt", func() bool { return linkStateOf(t, a, sender, attempted) == "may_have_been_delivered" })
	setLinkHook(a, nil)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	queued := mustQueue(t, a, sender, to, "queued")
	if err := a.d.linkUnpair(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}
	if got := linkStateOf(t, a, sender, queued); got != "cancelled" {
		t.Fatalf("queued after unpair: %s", got)
	}
	if got := linkStateOf(t, a, sender, attempted); got != "may_have_been_delivered" {
		t.Fatalf("attempted after unpair: %s", got)
	}
	// Re-pair under the same name: nothing old is sent, and the old reply is refused.
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := b.d.linkUnpair(context.Background(), "alpha"); err != nil && refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatal(err)
	}
	pairHosts(t, b, a)
	if status, reply := linkSendAs(t, a, sender, "", "answer", question); status == 200 || reply["code"] != "link_reply_pair_gone" {
		t.Fatalf("old reply: %d %v", status, reply)
	}
	fresh := mustQueue(t, a, sender, to, "fresh")
	waitLinkStateIs(t, a, sender, fresh, "received")
	time.Sleep(200 * time.Millisecond)
	if got := linkStateOf(t, a, sender, attempted); got != "may_have_been_delivered" {
		t.Fatalf("attempted after re-pair: %s", got)
	}
	if got := strings.Join(bodies(t, b, receiver), ","); got != "first,attempted,fresh" && got != "first,fresh" {
		t.Fatalf("inbox %s", got)
	}
	if strings.Contains(strings.Join(bodies(t, b, receiver), ","), "queued") {
		t.Fatal("a cancelled message arrived")
	}
}

// A rename keeps replies on the pair, and the received messages show the new name.
func TestLinkMessageReplyAfterRename(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "hi")
	waitLinkStateIs(t, a, sender, id, "received")
	if err := b.d.linkRename("alpha", "devbox"); err != nil {
		t.Fatal(err)
	}
	inbox := readInbox(t, b, receiver)
	if inbox[0]["sender_name"] != fromName(sender)+"@devbox" {
		t.Fatalf("renamed sender: %v", inbox[0])
	}
	status, reply := linkSendAs(t, b, receiver, fromName(sender)+"@alpha", "wrong to", int64(inbox[0]["id"].(float64)))
	if status == 200 || reply["code"] != "link_reply_target" {
		t.Fatalf("mismatched to: %d %v", status, reply)
	}
	status, reply = linkSendAs(t, b, receiver, "", "back", int64(inbox[0]["id"].(float64)))
	m, _ := reply["message"].(map[string]any)
	if status != 200 || m["recipient"] != fromName(sender)+"@devbox" {
		t.Fatalf("reply: %d %v", status, reply)
	}
	waitLinkStateIs(t, b, receiver, m["link_id"].(string), "received")
	// reply_to of a local message, or one the caller cannot read, is refused.
	other := joinAs(t, b.d.store, "codex", "synthetic-other", linkRepo(t, "other"), "")
	if status, reply := linkSendAs(t, b, other, "", "x", int64(inbox[0]["id"].(float64))); status == 200 || reply["code"] != "link_reply_target" {
		t.Fatalf("unreadable reply_to: %d %v", status, reply)
	}
}

// The largest valid bodies cross byte for byte, plain and escape-heavy (A3).
func TestLinkMessageMaximumBodies(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	plain := strings.Repeat("a", maxBody)
	escaped := strings.Repeat("\x01\"\\<", maxBody/4)
	for _, body := range []string{plain, escaped} {
		id := mustQueue(t, a, sender, receiver.Name+"@beta", body)
		waitLinkStateIs(t, a, sender, id, "received")
	}
	got := bodies(t, b, receiver)
	if len(got) != 2 || got[0] != plain || got[1] != escaped {
		t.Fatalf("bodies differ: %d", len(got))
	}
}

// Queue limits refuse a send and drop nothing; finished entries age out after 30 days and
// unfinished ones never do.
func TestLinkMessageQueueLimitsAndRetention(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	old := linkQueuePairCount
	linkQueuePairCount = 2
	t.Cleanup(func() { linkQueuePairCount = old })
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	to := receiver.Name + "@beta"
	ids := []string{mustQueue(t, a, sender, to, "one"), mustQueue(t, a, sender, to, "two")}
	if status, reply := linkSendAs(t, a, sender, to, "three", 0); status != 409 || reply["code"] != "link_queue_full" {
		t.Fatalf("over the limit: %d %v", status, reply)
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	a.d.linkKick(a.state(t).Pairs[0].ID)
	for _, id := range ids {
		waitLinkStateIs(t, a, sender, id, "received")
	}
	unfinished := mustQueue(t, a, sender, "x@beta", "x")
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := a.d.store.db.Exec(`UPDATE link_outbox SET finished_at=finished_at-? WHERE finished_at>0`, linkOutboxKeepMS); err != nil {
		t.Fatal(err)
	}
	if _, err := a.d.store.db.Exec(`UPDATE link_outbox SET created_at=created_at-? WHERE xid=?`, 2*linkOutboxKeepMS, unfinished); err != nil {
		t.Fatal(err)
	}
	if err := a.d.store.cullLinkOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	var left []string
	rows, _ := a.d.store.db.Query(`SELECT xid FROM link_outbox`)
	for rows.Next() {
		var x string
		rows.Scan(&x)
		left = append(left, x)
	}
	rows.Close()
	if len(left) != 1 || left[0] != unfinished {
		t.Fatalf("after retention: %v", left)
	}
}

// The receiver's guards, frame by frame: the stream order, the digest, an obsolete sequence,
// the origin, an unpaired key and another pair's ID (M4, M5, A5, A6).
func TestLinkMessageReceiverGuards(t *testing.T) {
	_, a, b, _, receiver := fastLinkHosts(t)
	aid, _ := a.d.linkIdentity()
	pair := b.state(t).Pairs[0].ID
	_, gen, _ := b.d.linkLife()
	frame := func(seq int64, xid, family, body string, digestOf string) link.Frame {
		d := linkDigest(seq, xid, receiver.Name, "synthetic-sender", family, digestOf)
		f, _ := link.NewFrame("deliver", linkDeliverBody{ID: pair, Seq: seq, XID: xid, Recipient: receiver.Name, Sender: "synthetic-sender",
			Family: family, Body: []byte(body), Digest: d})
		return f
	}
	answer := func(pin string, f link.Frame) (linkDeliverReply, error) {
		reply, _, err := b.d.answerDeliver(pin, f, gen)
		var r linkDeliverReply
		if err == nil {
			reply.Decode(&r)
		}
		return r, err
	}
	x := func(i int) string { return strings.Repeat("0", 31) + string(rune('0'+i)) }
	// A gap is a protocol error and stores nothing.
	if _, err := answer(aid.Pin, frame(2, x(1), "codex", "gap", "gap")); refusalCode(err) != ErrLinkProtocol.Code {
		t.Fatalf("gap: %v", err)
	}
	// A wrong digest is a protocol error.
	if _, err := answer(aid.Pin, frame(1, x(1), "codex", "body", "other")); refusalCode(err) != ErrLinkProtocol.Code {
		t.Fatalf("digest: %v", err)
	}
	// The maintainer origin is refused, stored and advances, so the next sequence goes (A6).
	if r, err := answer(aid.Pin, frame(1, x(1), "maintainer", "claim", "claim")); err != nil || r.Result != "refused" || r.Reason != "link_origin_refused" {
		t.Fatalf("origin: %+v %v", r, err)
	}
	if r, err := answer(aid.Pin, frame(2, x(2), "codex", "ok", "ok")); err != nil || r.Result != "received" || r.Seq != 2 {
		t.Fatalf("next: %+v %v", r, err)
	}
	// The same sequence again returns its stored result and inserts nothing; with a changed
	// body it is a protocol error.
	if r, err := answer(aid.Pin, frame(2, x(2), "codex", "ok", "ok")); err != nil || r.Result != "received" {
		t.Fatalf("again: %+v %v", r, err)
	}
	if _, err := answer(aid.Pin, frame(2, x(2), "codex", "changed", "changed")); refusalCode(err) != ErrLinkProtocol.Code {
		t.Fatalf("changed body: %v", err)
	}
	// An older sequence is stale.
	if r, err := answer(aid.Pin, frame(1, x(1), "maintainer", "claim", "claim")); err != nil || r.Result != "stale" {
		t.Fatalf("stale: %+v %v", r, err)
	}
	// The same cross-node ID at a new sequence is refused while its message is held.
	if _, err := answer(aid.Pin, frame(3, x(2), "codex", "dup", "dup")); refusalCode(err) != ErrLinkProtocol.Code {
		t.Fatalf("duplicate xid: %v", err)
	}
	// An unpaired key, and another pair's ID, get nothing.
	if _, err := answer(strings.Repeat("ab", 32), frame(3, x(3), "codex", "x", "x")); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("unpaired key: %v", err)
	}
	if got := bodies(t, b, receiver); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("inbox %v", got)
	}
	// After the receiver acknowledges and its messages are purged, the same sequence still
	// gets its stored result and inserts nothing (A5).
	if _, err := b.d.store.db.Exec(`DELETE FROM messages WHERE link_pair IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if r, err := answer(aid.Pin, frame(2, x(2), "codex", "ok", "ok")); err != nil || r.Result != "received" {
		t.Fatalf("after purge: %+v %v", r, err)
	}
	if got := bodies(t, b, receiver); len(got) != 0 {
		t.Fatalf("inserted again: %v", got)
	}
}

// The sender binds one cross-node ID to one sequence: a head read twice keeps its sequence
// and digest, and a result with another sequence or digest settles nothing (A4, A5).
func TestLinkMessageHeadBinding(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "bound")
	pair := a.state(t).Pairs[0].ID
	ctx := context.Background()
	h1, ok, err := a.d.store.linkNextHead(ctx, pair)
	if err != nil || !ok || h1.XID != id || h1.Seq != 1 {
		t.Fatalf("head: %+v %v %v", h1, ok, err)
	}
	h2, _, _ := a.d.store.linkNextHead(ctx, pair)
	if h2.Seq != h1.Seq || h2.Digest != h1.Digest {
		t.Fatalf("head moved: %+v %+v", h1, h2)
	}
	for _, c := range []struct {
		seq    int64
		digest string
	}{{2, h1.Digest}, {1, strings.Repeat("0", 64)}} {
		if done, err := a.d.store.linkSettle(ctx, pair, c.seq, c.digest, "received", ""); err != nil || done {
			t.Fatalf("mismatched settle %+v: %v %v", c, done, err)
		}
	}
	if got := linkStateOf(t, a, sender, id); got != "queued" {
		t.Fatalf("state %s", got)
	}
}

// A queue survives a restart while Remote is off, with its IDs and order, and is delivered
// once Remote is on again.
func TestLinkMessageQueueSurvivesRestart(t *testing.T) {
	n, a, b, sender, receiver := fastLinkHosts(t)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	to := receiver.Name + "@beta"
	ids := []string{mustQueue(t, a, sender, to, "one"), mustQueue(t, a, sender, to, "two")}
	if err := a.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := Start(Config{StateDir: a.root, Listen: []string{"127.0.0.1:0"}, Tailscale: a.fake})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.link.dial, d.link.now, d.link.backoff = n.dial, n.now, 20*time.Millisecond
	n.mu.Lock()
	n.hosts[a.ip+":47672"] = d
	n.mu.Unlock()
	a.d = d
	for _, id := range ids {
		if got := linkStateOf(t, a, sender, id); got != "queued" {
			t.Fatalf("after restart: %s", got)
		}
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := a.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		waitLinkStateIs(t, a, sender, id, "received")
	}
	if got := strings.Join(bodies(t, b, receiver), ","); got != "one,two" {
		t.Fatalf("inbox %s", got)
	}
}

// A send that races an unpair is retired with the pair's other messages (A7).
func TestLinkMessageEnqueueRacesUnpair(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if status, reply := linkSendAs(t, a, sender, receiver.Name+"@beta", "race", 0); status == 200 {
				ids <- reply["message"].(map[string]any)["link_id"].(string)
			}
		}()
	}
	if err := a.d.linkUnpair(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(ids)
	for id := range ids {
		if got := linkStateOf(t, a, sender, id); got != "cancelled" {
			t.Fatalf("%s after unpair: %s", id, got)
		}
	}
}

// A queue longer than one connection's frame limit is delivered in order, each message once,
// with no answer lost to the limit.
func TestLinkMessageLongQueue(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ids, want []string
	for i := 0; i < linkMaxFramesConn+10; i++ {
		body := "m" + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		want = append(want, body)
		ids = append(ids, mustQueue(t, a, sender, receiver.Name+"@beta", body))
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	a.d.linkKick(a.state(t).Pairs[0].ID)
	waitLinkStateIs(t, a, sender, ids[len(ids)-1], "received")
	var unknown int
	if err := a.d.store.db.QueryRow(`SELECT COUNT(*) FROM link_outbox WHERE unknown=1 OR state<>'received'`).Scan(&unknown); err != nil || unknown != 0 {
		t.Fatalf("rows not received cleanly: %d %v", unknown, err)
	}
	if got := strings.Join(bodies(t, b, receiver), ","); got != strings.Join(want, ",") {
		t.Fatalf("order differs")
	}
}
