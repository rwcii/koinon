package core

// Regression tests for the #676 review (F1 to F5), from the reviewer's probes.

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

func TestLinkMessageMaintainerInboxStaysLocal(t *testing.T) {
	_, a, b, sender, _ := fastLinkHosts(t)
	id := mustQueue(t, a, sender, "maintainer@beta", "remote maintainer inbox probe")
	got := waitLinkState(t, a, sender, id)
	var count int
	if err := b.d.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient_family='maintainer' AND link_pair IS NOT NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || got["delivery_state"] == "received" {
		t.Fatalf("remote message reached local-only maintainer inbox: rows=%d outcome=%v", count, got)
	}
}

func TestLinkMessageUnpairFencesPausedRetry(t *testing.T) {
	n, a, b, sender, receiver := fastLinkHosts(t)
	ctx := context.Background()
	firstRead := make(chan struct{})
	var once sync.Once
	setLinkHook(b, func(point string) error {
		if point == "before-receive" {
			once.Do(func() { close(firstRead) })
			return errLinkDrop
		}
		return nil
	})
	setLinkHook(a, func(point string) error {
		if point == "after-write" {
			return errLinkFault
		}
		return nil
	})
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "must not arrive after unpair")
	<-firstRead
	pair := a.state(t).Pairs[0].ID
	waitFor(t, "first worker stopped", func() bool { a.d.link.mu.Lock(); defer a.d.link.mu.Unlock(); return a.d.link.workers[pair] == nil })
	if got := bodies(t, b, receiver); len(got) != 0 {
		t.Fatalf("initial body unexpectedly committed: %v", got)
	}
	setLinkHook(a, nil)
	setLinkHook(b, nil)
	type marker struct{}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	a.d.link.mu.Lock()
	a.d.link.dial = func(call context.Context, address string) (net.Conn, error) {
		if call.Value(marker{}) == nil {
			return nil, errors.New("unmarked notification/monitor held")
		}
		close(entered)
		select {
		case <-release:
		case <-call.Done():
			return nil, call.Err()
		}
		return n.dial(call, address)
	}
	a.d.link.mu.Unlock()
	life, gen, err := a.d.linkLife()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); a.d.linkDeliverBatch(gen, context.WithValue(life, marker{}, true), pair) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("retry did not pause")
	}
	if err := a.d.linkUnpair(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if len(a.state(t).Pairs) != 0 || len(b.state(t).Pairs) != 1 {
		t.Fatal("expected local unpair with notice unavailable")
	}
	open()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("paused retry did not finish")
	}
	if got := bodies(t, b, receiver); len(got) != 0 {
		t.Fatalf("paused retry delivered AFTER unpair returned: inbox=%v state=%s", got, linkStateOf(t, a, sender, id))
	}
}

func TestLinkMessageEmptyOriginRefusalAdvances(t *testing.T) {
	_, a, b, _, receiver := fastLinkHosts(t)
	aid, _ := a.d.linkIdentity()
	pair := b.state(t).Pairs[0].ID
	frame := func(seq int64, family string) link.Frame {
		xid := strings.Repeat("a", 31) + string(rune('0'+seq))
		digest := linkDigest(seq, xid, receiver.Name, "sender", family, "body")
		f, _ := link.NewFrame("deliver", linkDeliverBody{ID: pair, Seq: seq, XID: xid, Recipient: receiver.Name, Sender: "sender", Family: family, Body: []byte("body"), Digest: digest})
		return f
	}
	answer, _, err := b.d.answerDeliver(aid.Pin, frame(1, ""), b.d.openGen())
	var reply linkDeliverReply
	if err == nil {
		err = answer.Decode(&reply)
	}
	if err != nil || reply.Result != "refused" || reply.Reason != "link_origin_refused" {
		t.Fatalf("empty origin was not terminally refused: reply=%+v err=%v", reply, err)
	}
	answer, _, err = b.d.answerDeliver(aid.Pin, frame(2, "codex"), b.d.openGen())
	if err == nil {
		err = answer.Decode(&reply)
	}
	if err != nil || reply.Result != "received" {
		t.Fatalf("next sequence stalled: %+v %v", reply, err)
	}
}

func TestLinkMessageQueueShowsBlockingHead(t *testing.T) {
	_, a, _, sender, receiver := fastLinkHosts(t)
	ctx := context.Background()
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	pair := a.state(t).Pairs[0].ID
	var first string
	for i := 0; i < linkQueueShown+1; i++ {
		o, err := a.d.store.linkEnqueue(ctx, agentKey(sender), pair, "beta", receiver.Name, "queued")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = o.LinkID
		}
	}
	h, ok, err := a.d.store.linkNextHead(ctx, pair)
	if err != nil || !ok {
		t.Fatalf("head: %+v %v", h, err)
	}
	if _, err := a.d.store.linkAttempt(ctx, pair, h.Seq, h.Digest); err != nil {
		t.Fatal(err)
	}
	queues, err := a.d.linkQueues(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range queues {
		for _, m := range q.Messages {
			if m.LinkID == first {
				return
			}
		}
	}
	t.Fatalf("blocking uncertain head %s omitted from all queue results (%d newest rows, no cursor)", first, linkQueueShown)
}

func TestLinkMessageReconcileDoesNotRetireNewPair(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	sender := joinAs(t, a.d.store, "codex", "synthetic-reconcile-sender", linkRepo(t, "sender"), "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-reconcile-receiver", linkRepo(t, "receiver"), "")
	// Only beta needs to dial for this test's pairing. Prevent any queued body from
	// racing the assertion while alpha's old reconciler is released.
	a.d.link.mu.Lock()
	a.d.link.dial = func(context.Context, string) (net.Conn, error) { return nil, errors.New("hold alpha's delivery") }
	a.d.link.mu.Unlock()
	prior := a.d.store.db.Stats().MaxOpenConnections
	a.d.store.db.SetMaxOpenConns(1)
	defer a.d.store.db.SetMaxOpenConns(prior)
	conn, err := a.d.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The connection gate pauses linkPairIDs after linkReconcile took the empty
	// link-state snapshot, while pairing itself uses the private link-state file.
	done := make(chan struct{})
	a.d.wg.Add(1)
	go func() { a.d.linkReconcile(a.d.openGen()); close(done) }()
	stacks := func() string { buf := make([]byte, 1<<20); return string(buf[:runtime.Stack(buf, true)]) }
	waitFor(t, "reconciler waiting for database after snapshot", func() bool {
		for _, stack := range strings.Split(stacks(), "\n\n") {
			if strings.Contains(stack, "(*Daemon).linkReconcile(") && strings.Contains(stack, "(*Store).linkPairIDs(") {
				return true
			}
		}
		return false
	})
	pairHosts(t, a, b)
	pair := a.state(t).Pairs[0].ID
	xid := strings.Repeat("b", 32)
	now := a.d.store.now().UnixMilli()
	// Fixture for an accepted row queued after pairing and before the old query resumes.
	_, err = conn.ExecContext(context.Background(), `INSERT INTO link_outbox(xid,pair_id,sender_family,sender_id,sender_name,recipient,computer,body,created_at,state,updated_at) VALUES (?,?,?,?,?,?,?,?,?,'queued',?)`, xid, pair, sender.Family, sender.ID, fromName(sender), receiver.Name, "beta", "keep valid new queue", now, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconciler did not finish")
	}
	waitFor(t, "all startup reconcilers finished", func() bool { return !strings.Contains(stacks(), "(*Daemon).linkReconcile(") })
	var state, reason string
	if err := a.d.store.db.QueryRow(`SELECT state,reason FROM link_outbox WHERE xid=?`, xid).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state == "cancelled" || reason == "unpaired" {
		t.Fatalf("live pair %s queue retired by stale startup snapshot: %s/%s; pairs=%+v", pair, state, reason, a.state(t).Pairs)
	}
}
