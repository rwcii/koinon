package core

// Regression test for the bounded #676 review: Remote disable fences a pending settlement
// of the old generation.

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pause the real sender's settlement after its generation check, as a busy database does.
func TestLinkMessageDisableFencesPendingSettlement(t *testing.T) {
	_, a, b, sender, receiver := fastLinkHosts(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	defer open()
	setLinkHook(a, func(point string) error {
		if point == "after-write" {
			close(entered)
			<-release
		}
		return nil
	})
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "settlement must respect its generation")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no frame write")
	}
	waitFor(t, "receiver committed", func() bool { return len(bodies(t, b, receiver)) == 1 })
	prior := a.d.store.db.Stats().MaxOpenConnections
	a.d.store.db.SetMaxOpenConns(1)
	defer a.d.store.db.SetMaxOpenConns(prior)
	conn, err := a.d.store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	open()
	waitFor(t, "sender settlement waiting after generation check", func() bool {
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		for _, stack := range strings.Split(stacks, "\n\n") {
			if strings.Contains(stack, "(*Daemon).linkStore(") && strings.Contains(stack, "(*Store).linkSettle(") {
				return true
			}
		}
		return false
	})
	off := make(chan error, 1)
	go func() { off <- a.d.remoteDisable(context.Background()) }()
	closedBeforeRelease := false
	select {
	case err := <-off:
		if err != nil {
			t.Fatal(err)
		}
		closedBeforeRelease = true
	case <-time.After(200 * time.Millisecond):
		// A commit held in its original generation can drain before the disable returns.
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if !closedBeforeRelease {
		select {
		case err := <-off:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("disable did not drain")
		}
	}
	waitFor(t, "sender settlement finished", func() bool {
		buf := make([]byte, 1<<20)
		stacks := string(buf[:runtime.Stack(buf, true)])
		return !strings.Contains(stacks, "(*Store).linkSettle(")
	})
	if got := linkStateOf(t, a, sender, id); closedBeforeRelease && got == "received" {
		t.Fatalf("old generation settled received after Remote disable returned; expected unfinished uncertainty")
	}
}
