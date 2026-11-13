package core

// Regression tests for the #674 review (F2 to F4): the link generation fences outbound
// work, invitations and joins across Remote disable, and setup keeps the saved port.

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/remote"
)

func pairedLinkHosts(t *testing.T) (*linkNet, *linkHost, *linkHost) {
	t.Helper()
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	text, err := b.d.linkInvite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.d.linkJoin(context.Background(), text); err != nil {
		t.Fatal(err)
	}
	return n, a, b
}

func TestLinkUnpairWhileOwnRemoteOffStaysLocal(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	ctx := context.Background()
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.d.linkUnpair(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if len(b.state(t).Pairs) == 0 {
		t.Fatal("unpair sent authenticated link traffic and deleted the peer's pair while own Remote was off")
	}
}

func TestLinkMonitorDialEndsAtDisable(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	_ = b
	entered, release := make(chan struct{}), make(chan struct{})
	a.d.link.mu.Lock()
	a.d.link.dial = func(ctx context.Context, address string) (net.Conn, error) {
		close(entered)
		<-release
		return n.dial(ctx, address)
	}
	a.d.link.mu.Unlock()
	finished := make(chan struct{})
	go func() { a.d.passNow(); close(finished) }()
	<-entered
	if err := a.d.remoteDisable(context.Background()); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	<-finished
	a.d.link.mu.Lock()
	observation := a.d.link.observed[a.state(t).Pairs[0].ID]
	a.d.link.mu.Unlock()
	if observation.State == "connected" {
		t.Fatal("monitor completed a new authenticated TLS ping after Remote disable returned")
	}
}

func TestLinkDisableWithFailedCleanupRevokesInvitations(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, err := b.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b.fake.mu.Lock()
	b.fake.fail = true
	b.fake.mu.Unlock()
	if err := b.d.remoteDisable(ctx); err == nil {
		t.Fatal("expected synthetic mapping cleanup failure")
	}
	b.fake.mu.Lock()
	b.fake.fail = false
	b.fake.mu.Unlock()
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.d.linkJoin(ctx, text); err == nil {
		t.Fatal("invitation from before disable still pairs after enable when dashboard cleanup had failed")
	}
}

type statusBarrier struct {
	inner            remote.Adapter
	entered, release chan struct{}
	used             atomic.Bool
}

func (a *statusBarrier) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) == 2 && args[0] == "status" && a.used.CompareAndSwap(false, true) {
		close(a.entered)
		<-a.release
	}
	return a.inner.Run(ctx, args...)
}

func TestLinkInviteInFlightAtDisableIsNotValid(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	barrier := &statusBarrier{inner: b.fake, entered: make(chan struct{}), release: make(chan struct{})}
	b.d.remote.mu.Lock()
	b.d.remote.manager = remote.Manager{Adapter: barrier}
	b.d.remote.mu.Unlock()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() { text, err := b.d.linkInvite(context.Background()); done <- result{text, err} }()
	<-barrier.entered
	if err := b.d.remoteDisable(context.Background()); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	r := <-done
	if r.err != nil && !errors.Is(r.err, ErrLinkRemoteOff) {
		t.Fatal(r.err)
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if r.err == nil {
		if _, err := a.d.linkJoin(context.Background(), r.text); err == nil {
			t.Fatal("in-flight invitation committed after disable and paired after re-enable")
		}
	}
}

func TestLinkNameOnlySetupKeepsSavedPort(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	if err := a.d.linkSetup(context.Background(), "alpha", 47673); err != nil {
		t.Fatal(err)
	}
	if err := a.d.linkSetup(context.Background(), "new-alpha", 0); err != nil {
		t.Fatal(err)
	}
	if got := a.state(t).Port; got != 47673 {
		t.Fatalf("name-only setup reset saved port 47673 to %d", got)
	}
	if link.DefaultPort != 47672 {
		t.Fatal("fixture assumption")
	}
}

// An invitation hash that link.json still holds after the link closed, as after a failed
// cleanup write or a restart, is not valid in the next generation.
func TestLinkInvitationInFileOnlyIsRefused(t *testing.T) {
	n := &linkNet{}
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, err := b.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := link.ParseInvitation(text)
	if err != nil {
		t.Fatal(err)
	}
	saved := b.state(t).Invites
	b.d.remote.mu.Lock()
	b.d.closeLinkLocked()
	err = b.d.openLinkLocked(nil)
	b.d.remote.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// The new generation's first monitor pass removes the file's stale hash; then the hash is
	// written back, as a failed cleanup or a restart leaves it.
	deadline := time.Now().Add(5 * time.Second)
	for len(b.state(t).Invites) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the monitor kept a revoked invitation in link.json")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.d.link.mu.Lock()
	s, err := link.ReadState(b.root)
	if err == nil {
		s.Invites = saved
		err = link.WriteState(b.root, s)
	}
	b.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Repeat("ab", 32)
	if b.d.linkAdmits(unknown) {
		t.Fatal("a revoked invitation admits an unknown key")
	}
	if st := b.d.linkStatus(ctx); st.Invitations != 0 {
		t.Fatalf("status counts a revoked invitation: %d", st.Invitations)
	}
	hello := linkHello{Secret: invitation.Secret, Name: "alpha", DNS: "alpha.example-tailnet.ts.net", IPs: []string{"100.64.0.1"}, Port: 47672}
	if _, err := b.d.acceptInvitation(unknown, hello); !errors.Is(err, ErrLinkInvitation) {
		t.Fatalf("a revoked invitation was accepted: %v", err)
	}
	if len(b.state(t).Pairs) != 0 {
		t.Fatal("a revoked invitation made a pair")
	}
}

// A connection of an earlier generation never attaches to a later one.
func TestLinkOldGenerationDoesNotAttach(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	_, old, err := a.d.linkLife()
	if err != nil {
		t.Fatal(err)
	}
	a.d.remote.mu.Lock()
	a.d.closeLinkLocked()
	err = a.d.openLinkLocked(nil)
	a.d.remote.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	if a.d.trackLink(c1, "", old) {
		t.Fatal("a connection of the closed generation attached")
	}
	if _, now, _ := a.d.linkLife(); !a.d.trackLink(c1, "", now) {
		t.Fatal("a connection of the open generation did not attach")
	}
}

// openGen is the open link's generation.
func (d *Daemon) openGen() uint64 {
	_, gen, _ := d.linkLife()
	return gen
}

// passNow runs one monitor pass in the open generation.
func (d *Daemon) passNow() {
	_, gen, _ := d.linkLife()
	d.linkPass(make(chan struct{}), gen)
}

// A join begun in one lifecycle must not perform its network exchange
// under a new lifecycle after a provider read crosses disable and enable.
func TestLinkOldJoinDoesNotSendInNewGeneration(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, err := b.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	barrier := &statusBarrier{inner: a.fake, entered: make(chan struct{}), release: make(chan struct{})}
	a.d.remote.mu.Lock()
	a.d.remote.manager = remote.Manager{Adapter: barrier}
	a.d.remote.mu.Unlock()
	finished := make(chan error, 1)
	go func() { _, err := a.d.linkJoin(ctx, text); finished <- err }()
	<-barrier.entered
	if err := a.d.remoteDisable(ctx); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	if err := a.d.remoteEnable(ctx, 0); err != nil {
		close(barrier.release)
		t.Fatal(err)
	}
	close(barrier.release)
	result := <-finished
	state := b.state(t)
	if len(state.Pairs) != 0 || len(state.Invites) != 1 {
		t.Fatalf("old join sent pair-hello in new generation: local error=%v, peer pairs=%d, peer invitations=%d", result, len(state.Pairs), len(state.Invites))
	}
}
