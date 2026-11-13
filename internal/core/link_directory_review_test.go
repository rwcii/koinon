package core

// Regression tests for the #675 review: a disconnected pair's directory is stale (F2), and a
// name-update read in a closed generation does not commit (F1).

import (
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

func TestLinkDisconnectedDirectoryIsStale(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	_ = n
	joinAs(t, b.d.store, "codex", "synthetic-review-b", linkRepo(t, "proj"), "")
	a.d.passNow()
	if rows := a.d.remoteRows(); len(rows) != 1 || rows[0].Stale {
		t.Fatalf("initial: %+v", rows)
	}
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.d.passNow()
	p := a.state(t).Pairs[0]
	a.d.link.mu.Lock()
	o := a.d.link.observed[p.ID]
	a.d.link.mu.Unlock()
	rows := a.d.remoteRows()
	if o.State != "offline" {
		t.Fatalf("expected offline: %+v", o)
	}
	if len(rows) != 1 || !rows[0].Stale {
		t.Fatalf("offline pair still has current receivers: observation=%+v rows=%+v", o, rows)
	}
}

type linkReadGate struct {
	net.Conn
	armed            atomic.Bool
	entered, release chan struct{}
}

func (c *linkReadGate) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
	}
	return n, err
}

func TestLinkOldInboundNameUpdateCannotCommit(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	aid, _ := a.d.linkIdentity()
	bid, _ := b.d.linkIdentity()
	p := a.state(t).Pairs[0]
	old := a.d.openGen()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	gateReady := make(chan *linkReadGate, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		raw, err := l.Accept()
		if err != nil {
			return
		}
		gate := &linkReadGate{Conn: raw, entered: make(chan struct{}), release: make(chan struct{})}
		gateReady <- gate
		a.d.serveLink(tls.Server(gate, a.d.linkServerConfig(aid)), old)
	}()
	raw, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client := tls.Client(raw, linkClientConfig(bid, aid.Pin))
	defer client.Close()
	if err := client.Handshake(); err != nil {
		t.Fatal(err)
	}
	gate := <-gateReady
	waitFor(t, "server handshake tracked", func() bool {
		a.d.link.mu.Lock()
		defer a.d.link.mu.Unlock()
		for _, pin := range a.d.link.conns {
			if pin == bid.Pin {
				return true
			}
		}
		return false
	})
	gate.armed.Store(true)
	f, _ := link.NewFrame("name-update", linkNameUpdate{ID: p.ID, Name: "late-name"})
	if err := link.WriteFrame(client, f); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("name-update not paused")
	}
	if err := a.d.remoteDisable(ctx); err != nil {
		close(gate.release)
		t.Fatal(err)
	}
	if err := a.d.remoteEnable(ctx, 0); err != nil {
		close(gate.release)
		t.Fatal(err)
	}
	close(gate.release)
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("old server did not finish")
	}
	if p := a.state(t).Pairs[0]; p.Advertised == "late-name" {
		t.Fatalf("old generation %d name-update committed in new generation %d: %+v", old, a.d.openGen(), p)
	}
}
