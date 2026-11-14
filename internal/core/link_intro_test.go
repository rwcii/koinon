package core

// Tests of introductions (LINK-GROUP-JOIN, #683): three and four in-process daemons, real
// TLS, synthetic Tailscale. All names and addresses are synthetic.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/remote"
)

// joinThrough makes an invitation on inviter and joins it on joiner.
func joinThrough(t *testing.T, inviter, joiner *linkHost) (link.Pair, []string) {
	t.Helper()
	ctx := context.Background()
	text, err := inviter.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, introduced, err := joiner.d.linkJoinIntroduced(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	return p, introduced
}

func pinOf(t *testing.T, h *linkHost) string {
	t.Helper()
	id, err := h.d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Pin
}

// pairWith returns h's pair with the computer of other, by its pin.
func pairWith(t *testing.T, h, other *linkHost) (link.Pair, bool) {
	t.Helper()
	return lockedState(t, h).PairByPin(pinOf(t, other))
}

// lockedState reads h's link state under its link lock, as the daemon does: a read during
// the daemon's atomic replace can hold the replaced inode, which the private file check
// refuses (seen on macOS).
func lockedState(t *testing.T, h *linkHost) link.State {
	t.Helper()
	h.d.link.mu.Lock()
	s, err := link.ReadState(h.root)
	h.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// requireMesh checks that every two hosts hold one paired pair with each other, with the
// same ID on both sides.
func requireMesh(t *testing.T, hosts ...*linkHost) {
	t.Helper()
	for i, a := range hosts {
		if got := len(lockedState(t, a).Pairs); got != len(hosts)-1 {
			t.Fatalf("%s has %d pairs, want %d: %+v", a.root, got, len(hosts)-1, lockedState(t, a).Pairs)
		}
		for _, b := range hosts[i+1:] {
			pa, okA := pairWith(t, a, b)
			pb, okB := pairWith(t, b, a)
			if !okA || !okB || pa.ID != pb.ID || pa.State != link.Paired || pb.State != link.Paired {
				t.Fatalf("pair %+v %v / %+v %v", pa, okA, pb, okB)
			}
		}
	}
}

func introsOf(t *testing.T, h *linkHost) []link.Intro {
	t.Helper()
	return lockedState(t, h).Intros
}

// Two joins pair four computers with each other: each join's inviter introduces the new
// computer to its other paired computers, and messages go between introduced computers.
func TestLinkIntroductionsMakeTheGroup(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	d := newLinkHost(t, n, "delta", "100.64.0.4")
	if _, introduced := joinThrough(t, b, a); len(introduced) != 0 {
		t.Fatalf("first join introduced %v", introduced)
	}
	_, introduced := joinThrough(t, b, c)
	if !slices.Equal(introduced, []string{"alpha"}) {
		t.Fatalf("introduced %v", introduced)
	}
	// The new computer holds the introduced pair at once, from the confirm reply.
	pc, ok := pairWith(t, c, a)
	if !ok || pc.Via == "" || pc.Effective != "alpha" {
		t.Fatalf("new computer's pair %+v %v", pc, ok)
	}
	waitFor(t, "alpha never got its introduction to gamma", func() bool {
		_, ok := pairWith(t, a, c)
		return ok && len(introsOf(t, b)) == 0
	})
	requireMesh(t, a, b, c)
	// A join through a computer that was introduced reaches the whole group too.
	_, introduced = joinThrough(t, c, d)
	slices.Sort(introduced)
	if !slices.Equal(introduced, []string{"alpha", "beta"}) {
		t.Fatalf("introduced %v", introduced)
	}
	waitFor(t, "the group never reached delta", func() bool {
		_, okA := pairWith(t, a, d)
		_, okB := pairWith(t, b, d)
		return okA && okB && len(introsOf(t, c)) == 0
	})
	requireMesh(t, a, b, c, d)
	// Status names the introducer, and each introduced pair connects.
	for _, h := range []*linkHost{a, b, c, d} {
		h.d.passNow()
	}
	st := a.d.linkStatus(context.Background())
	for _, p := range st.Pairs {
		if p.Observed.State != "connected" {
			t.Fatalf("alpha's pair %s: %+v", p.Name, p.Observed)
		}
		if want := map[string]string{"beta": "", "gamma": "beta", "delta": "gamma"}[p.Name]; p.IntroducedBy != want {
			t.Fatalf("%s introduced by %q, want %q", p.Name, p.IntroducedBy, want)
		}
	}
	// A message goes between two computers that only an introduction paired.
	sender := joinAs(t, a.d.store, "codex", "synthetic-sender", linkRepo(t, "send"), "")
	receiver := joinAs(t, d.d.store, "codex", "synthetic-receiver", linkRepo(t, "recv"), "")
	d.d.passNow()
	a.d.passNow()
	id := mustQueue(t, a, sender, receiver.Name+"@delta", "through an introduction")
	waitLinkStateIs(t, a, sender, id, "received")
	if got := strings.Join(bodies(t, d, receiver), ","); got != "through an introduction" {
		t.Fatalf("inbox %s", got)
	}
}

// A computer that is off gets its introduction when it answers again, also after the
// introducing daemon restarts; until then the introduction waits in link.json.
func TestLinkIntroductionWaitsForAnOfflineComputer(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	n.mu.Lock()
	delete(n.hosts, a.ip+":47672")
	n.mu.Unlock()
	if _, introduced := joinThrough(t, b, c); !slices.Equal(introduced, []string{"alpha"}) {
		t.Fatalf("introduced %v", introduced)
	}
	b.d.passNow()
	waitFor(t, "the introduction was not kept", func() bool {
		in := introsOf(t, b)
		return len(in) == 1 && in[0].NewSent && !b.d.introBusy()
	})
	if st := b.d.linkStatus(context.Background()); st.Introductions != 1 {
		t.Fatalf("status introductions %d", st.Introductions)
	}
	// The introducing daemon restarts with the introduction in its state.
	if err := b.d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(Config{StateDir: b.root, Listen: []string{"127.0.0.1:0"}, Tailscale: b.fake})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	restarted.link.dial, restarted.link.now = n.dial, n.now
	n.mu.Lock()
	n.hosts[b.ip+":47672"] = restarted
	n.hosts[a.ip+":47672"] = a.d
	n.mu.Unlock()
	b.d = restarted
	if _, _, err := b.d.linkLife(); err != nil {
		if err := b.d.remoteEnable(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := introsOf(t, b); len(got) != 1 || !got[0].NewSent {
		t.Fatalf("after the restart: %+v", got)
	}
	b.d.passNow()
	waitFor(t, "alpha never got its introduction after the restart", func() bool {
		_, ok := pairWith(t, a, c)
		return ok && len(introsOf(t, b)) == 0
	})
	requireMesh(t, a, b, c)
}

// loseAnswer puts joiner and inviter in the state that a lost answer to the joiner's ask
// leaves: the joiner still asks and holds none of the introduced pairs, and the inviter's
// introductions are not acknowledged.
func loseAnswer(t *testing.T, joiner, inviter *linkHost, via string) {
	t.Helper()
	joiner.d.link.mu.Lock()
	s := joiner.state(t)
	s.Pairs = slices.DeleteFunc(s.Pairs, func(p link.Pair) bool { return p.ID != via })
	s.Pairs[0].Asking, s.Pairs[0].Seen = true, nil
	err := link.WriteState(joiner.root, s)
	joiner.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	inviter.d.link.mu.Lock()
	bs := inviter.state(t)
	for i := range bs.Intros {
		bs.Intros[i].NewSent = false
	}
	err = link.WriteState(inviter.root, bs)
	inviter.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func setHost(n *linkNet, h *linkHost, on bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if on {
		n.hosts[h.ip+":47672"] = h.d
	} else {
		delete(n.hosts, h.ip+":47672")
	}
}

// The answer to the new computer's ask can be lost: the new computer asks again at its
// passes, with no time limit, and a repeated answer adds no second pair.
func TestLinkIntroductionLostAnswer(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	setHost(n, a, false) // alpha is off, so beta cannot deliver yet
	pb, _ := joinThrough(t, b, c)
	if p, _ := pairWith(t, c, b); p.Asking || p.Seen != nil {
		t.Fatalf("a finished join still asks: %+v", p)
	}
	waitFor(t, "delivery still runs", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	loseAnswer(t, c, b, pb.ID)
	// Unacknowledged, it is not delivered.
	setHost(n, a, true)
	b.d.passNow()
	waitFor(t, "delivery still runs", func() bool { return !b.d.introBusy() })
	if _, ok := pairWith(t, a, c); ok {
		t.Fatal("an unacknowledged introduction was delivered")
	}
	// The new computer is off for more than an hour; its next pass still asks.
	n.shift.Add(int64(61 * time.Minute))
	b.d.passNow()
	if got := introsOf(t, b); len(got) != 1 {
		t.Fatalf("the waiting introduction was dropped: %+v", got)
	}
	c.d.passNow()
	c.d.passNow()
	waitFor(t, "alpha never got its introduction", func() bool {
		_, ok := pairWith(t, a, c)
		return ok && len(introsOf(t, b)) == 0
	})
	requireMesh(t, a, b, c)
	if p, _ := pairWith(t, c, b); p.Asking || p.Seen != nil {
		t.Fatalf("still asks after its introductions: %+v", p)
	}
	// A repeated introduce adds no second pair.
	pa, _ := pairWith(t, a, c)
	_, gen, _ := a.d.linkLife()
	again := linkIntroduce{ID: pa.ID, Name: "gamma", DNS: pa.DNSName, IPs: pa.IPs, Port: pa.Port, Pin: pa.Pin}
	ok, err := a.d.acceptIntroduce(pinOf(t, b), again, gen)
	if err != nil || ok.Added {
		t.Fatalf("replay %+v %v", ok, err)
	}
	requireMesh(t, a, b, c)
}

// A lost acknowledgement must not let the inviter's repeated list undo an explicit unpair
// (review of #684, F1).
func TestLinkIntroductionRetryDoesNotUndoUnpair(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	setHost(n, a, false)
	pb, _ := joinThrough(t, b, c)
	waitFor(t, "waiting intro", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	// gamma took alpha, but its acknowledgement did not commit on beta.
	intro := introsOf(t, b)[0].ID
	b.d.link.mu.Lock()
	bs := b.state(t)
	bs.Intros[0].NewSent = false
	err := link.WriteState(b.root, bs)
	b.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	c.d.link.mu.Lock()
	cs := c.state(t)
	i := slices.IndexFunc(cs.Pairs, func(p link.Pair) bool { return p.ID == pb.ID })
	cs.Pairs[i].Asking, cs.Pairs[i].Seen = true, []string{intro}
	err = link.WriteState(c.root, cs)
	c.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.d.linkUnpair(context.Background(), "alpha"); err != nil {
		t.Fatal(err)
	}
	c.d.passNow()
	if p, ok := pairWith(t, c, a); ok {
		t.Fatalf("retry resurrected explicitly unpaired alpha: %+v", p)
	}
	if got := introsOf(t, b); len(got) != 0 {
		t.Fatalf("beta kept the refused introduction: %+v", got)
	}
	if p, _ := pairWith(t, c, b); p.Asking {
		t.Fatalf("still asks: %+v", p)
	}
}

// Removing either pair stops an introduction that waits before its connection (review of
// #684, F2).
func TestLinkIntroductionUnpairFencesPendingDelivery(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	setHost(n, a, false)
	joinThrough(t, b, c)
	waitFor(t, "waiting intro", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	entered, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	b.d.link.mu.Lock()
	b.d.link.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		if addr == a.ip+":47672" && blocked.CompareAndSwap(false, true) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return n.dial(ctx, addr)
	}
	b.d.link.mu.Unlock()
	setHost(n, a, true)
	_, gen, _ := b.d.linkLife()
	done := make(chan struct{})
	go func() { b.d.deliverIntros(gen); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not pause")
	}
	if err := b.d.linkUnpair(context.Background(), "gamma"); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("delivery did not finish")
	}
	if p, ok := pairWith(t, a, c); ok {
		t.Fatalf("delivery created gamma on alpha after beta unpaired gamma: %+v", p)
	}
}

// A computer whose waiting introductions leave no room for all of a new computer's makes
// none and refuses the ask; the new computer keeps asking (review of #684, F3).
func TestLinkIntroductionCapacityIsAllOrNone(t *testing.T) {
	s := link.State{Version: 1, Name: "beta", Port: link.DefaultPort}
	for i := 0; i < 24; i++ {
		s.Pairs = append(s.Pairs, link.Pair{ID: fmt.Sprintf("%032x", i+1), Advertised: fmt.Sprintf("node-%d", i), Effective: fmt.Sprintf("node-%d", i),
			Pin: fmt.Sprintf("%064x", i+1), DNSName: fmt.Sprintf("node-%d.example-tailnet.ts.net", i), IPs: []string{"100.64.0.1"}, Port: link.DefaultPort,
			State: link.Paired, Role: link.Inviter, Created: 1})
	}
	for i := 0; i < 23; i++ {
		for j := 0; j < i; j++ {
			s.Intros = append(s.Intros, link.Intro{ID: fmt.Sprintf("%032x", 4096+len(s.Intros)), New: s.Pairs[i].ID, Old: s.Pairs[j].ID, NewSent: true, Created: 1})
		}
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	before := len(s.Intros)
	if err := addIntrosLocked(&s, 23, 2); !errors.Is(err, ErrLinkIntrosFull) || len(s.Intros) != before || s.Pairs[23].Introduced {
		t.Fatalf("at capacity: %v, %d new", err, len(s.Intros)-before)
	}
	s.Intros = s.Intros[:200]
	if err := addIntrosLocked(&s, 23, 2); err != nil || len(s.Intros) != 223 || !s.Pairs[23].Introduced {
		t.Fatalf("with room: %v, %d", err, len(s.Intros))
	}
	// Over the link: the refusal keeps the new computer asking, and the join says so.
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	b.d.link.mu.Lock()
	bs := b.state(t)
	for len(bs.Intros) < link.MaxIntros {
		id, _ := link.NewID()
		bs.Intros = append(bs.Intros, link.Intro{ID: id, New: strings.Repeat("e", 32), Old: strings.Repeat("f", 32), NewSent: true, Created: 1})
	}
	err := link.WriteState(b.root, bs)
	b.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	text, _ := b.d.linkInvite(context.Background())
	j, err := c.d.linkJoinPair(context.Background(), text)
	if err != nil || !j.waiting || len(j.introduced) != 0 {
		t.Fatalf("join at capacity: %+v %v", j, err)
	}
	if p, _ := pairWith(t, c, b); !p.Asking {
		t.Fatalf("stopped asking: %+v", p)
	}
	// The stale introductions go (their pairs do not exist); the next ask gets alpha.
	b.d.passNow()
	waitFor(t, "beta kept stale introductions", func() bool { return len(introsOf(t, b)) == 0 && !b.d.introBusy() })
	c.d.passNow()
	waitFor(t, "alpha never got gamma", func() bool { _, ok := pairWith(t, a, c); return ok && !b.d.introBusy() })
	requireMesh(t, a, b, c)
}

// An introduction comes only from a paired key, only in the open generation, and never
// gives a second pair with a computer that is already paired.
func TestLinkIntroductionRefusals(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	pab, _ := joinThrough(t, b, a)
	_, gen, _ := a.d.linkLife()
	cid, _ := link.NewID()
	in := linkIntroduce{ID: cid, Name: "gamma", DNS: "gamma.example-tailnet.ts.net", IPs: []string{c.ip}, Port: link.DefaultPort, Pin: pinOf(t, c)}
	// An unknown key.
	if _, err := a.d.acceptIntroduce(strings.Repeat("1", 64), in, gen); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("unknown key: %v", err)
	}
	// A closed generation.
	if _, err := a.d.acceptIntroduce(pinOf(t, b), in, gen-1); refusalCode(err) != ErrLinkRemoteOff.Code {
		t.Fatalf("old generation: %v", err)
	}
	// The receiver's own key, and the sender's key.
	for _, pin := range []string{pinOf(t, a), pinOf(t, b)} {
		bad := in
		bad.Pin = pin
		if _, err := a.d.acceptIntroduce(pinOf(t, b), bad, gen); !errors.Is(err, ErrInvalid) {
			t.Fatalf("pin %s: %v", pin, err)
		}
	}
	// A pending key: gamma holds an invitation exchange with alpha that is not confirmed.
	text, _ := a.d.linkInvite(context.Background())
	invitation, _ := link.ParseInvitation(text)
	_, cgen, _ := a.d.linkLife()
	if _, err := a.d.acceptInvitation(pinOf(t, c), linkHello{Secret: invitation.Secret, Name: "gamma", DNS: "gamma.example-tailnet.ts.net", IPs: []string{c.ip}, Port: link.DefaultPort}, cgen); err != nil {
		t.Fatal(err)
	}
	other, _ := link.NewID()
	fromPending := linkIntroduce{ID: other, Name: "beta", DNS: "beta.example-tailnet.ts.net", IPs: []string{b.ip}, Port: link.DefaultPort, Pin: strings.Repeat("2", 64)}
	if _, err := a.d.acceptIntroduce(pinOf(t, c), fromPending, gen); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("pending key: %v", err)
	}
	if err := a.d.linkCancel("gamma"); err != nil {
		t.Fatal(err)
	}
	// alpha joins gamma's invitation (gamma has no other pair to introduce); then gamma joins
	// beta. beta's introduction of alpha to gamma meets an existing pair with another ID:
	// gamma does not acknowledge it, beta drops it, and alpha gets no second pair.
	pac, _ := joinThrough(t, c, a)
	if _, introduced := joinThrough(t, b, c); len(introduced) != 0 {
		t.Fatalf("introduced %v", introduced)
	}
	waitFor(t, "beta kept the refused introduction", func() bool { return len(introsOf(t, b)) == 0 && !b.d.introBusy() })
	requireMesh(t, a, b, c)
	if p, _ := pairWith(t, a, c); p.ID != pac.ID || p.Via != "" {
		t.Fatalf("alpha's pair with gamma changed: %+v", p)
	}
	if p, _ := pairWith(t, a, b); p.ID != pab.ID {
		t.Fatalf("alpha's pair with beta changed: %+v", p)
	}
}

// Unpair and leave drop the introductions of the removed pairs, and a leave is one write:
// an introduction that arrives later finds no paired key.
func TestLinkIntroductionUnpairAndLeave(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	n.mu.Lock()
	delete(n.hosts, a.ip+":47672")
	n.mu.Unlock()
	joinThrough(t, b, c)
	waitFor(t, "no waiting introduction", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	if err := b.d.linkUnpair(context.Background(), "gamma"); err != nil {
		t.Fatal(err)
	}
	if got := introsOf(t, b); len(got) != 0 {
		t.Fatalf("unpair kept %+v", got)
	}
	n.mu.Lock()
	n.hosts[a.ip+":47672"] = a.d
	n.mu.Unlock()
	b.d.passNow()
	waitFor(t, "delivery still runs", func() bool { return !b.d.introBusy() })
	if _, ok := pairWith(t, a, c); ok {
		t.Fatal("an unpaired computer's introduction was delivered")
	}
	// gamma still holds alpha from the confirm reply, but alpha never got gamma: gamma
	// removes it before it joins again.
	if _, err := c.d.linkLeave(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Leave removes every pair here at once, and the others remove theirs when they answer.
	joinThrough(t, b, c)
	waitFor(t, "alpha never got gamma", func() bool { _, ok := pairWith(t, a, c); return ok && !b.d.introBusy() })
	result, err := c.d.linkLeave(context.Background())
	if err != nil || result.Unpaired != 2 {
		t.Fatalf("leave %+v %v", result, err)
	}
	if s := lockedState(t, c); len(s.Pairs) != 0 || len(s.Intros) != 0 {
		t.Fatalf("after leave %+v", s)
	}
	for _, h := range []*linkHost{a, b} {
		if _, ok := pairWith(t, h, c); ok {
			t.Fatalf("%s kept its pair with gamma", h.root)
		}
	}
	// After the leave, beta's introduction finds no paired key on gamma.
	_, gen, _ := c.d.linkLife()
	nid, _ := link.NewID()
	if _, err := c.d.acceptIntroduce(pinOf(t, b), linkIntroduce{ID: nid, Name: "alpha", DNS: "alpha.example-tailnet.ts.net", IPs: []string{a.ip}, Port: link.DefaultPort, Pin: pinOf(t, a)}, gen); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("after leave: %v", err)
	}
}

// With Remote off, nothing is delivered and the introduction stays; Remote on delivers it.
func TestLinkIntroductionWaitsWhileRemoteIsOff(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	if err := a.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	joinThrough(t, b, c)
	waitFor(t, "no waiting introduction", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	_, old, _ := b.d.linkLife()
	if err := b.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	b.d.deliverIntros(old) // the closed generation delivers nothing
	if got := introsOf(t, b); len(got) != 1 {
		t.Fatalf("intros %+v", got)
	}
	if _, ok := pairWith(t, a, c); ok {
		t.Fatal("delivered with Remote off")
	}
	if err := b.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alpha never got gamma after Remote on", func() bool {
		_, ok := pairWith(t, a, c)
		return ok && len(introsOf(t, b)) == 0
	})
	requireMesh(t, a, b, c)
}

// introBusy reports whether a delivery of introductions runs.
func (d *Daemon) introBusy() bool {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	return d.link.introducing
}

// A host whose Tailscale uses no TUN interface has no route to tailnet addresses; its link
// connections go through tailscaled (tailscale nc), so it joins, pings and gets the group
// like any other computer (#688).
func TestLinkUserspaceTailscaleDialsThroughTailscaled(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	// gamma runs userspace Tailscale: no direct dial, only the stream through tailscaled.
	c.fake.mu.Lock()
	c.fake.userspace, c.fake.stream = true, n.dial
	c.fake.mu.Unlock()
	c.d.link.mu.Lock()
	c.d.link.dial = nil
	c.d.link.mu.Unlock()
	if _, introduced := joinThrough(t, b, c); !slices.Equal(introduced, []string{"alpha"}) {
		t.Fatalf("introduced %v", introduced)
	}
	waitFor(t, "alpha never got gamma", func() bool { _, ok := pairWith(t, a, c); return ok && !b.d.introBusy() })
	requireMesh(t, a, b, c)
	c.d.passNow()
	st := c.d.linkStatus(context.Background())
	if st.Transport != "tailscaled" {
		t.Fatalf("transport %q", st.Transport)
	}
	for _, p := range st.Pairs {
		if p.Observed.State != "connected" {
			t.Fatalf("gamma's pair %s: %+v", p.Name, p.Observed)
		}
	}
	c.fake.mu.Lock()
	streams := c.fake.streams
	c.fake.mu.Unlock()
	if streams == 0 {
		t.Fatal("no connection went through tailscaled")
	}
	if a.d.linkStatus(context.Background()).Transport != "" {
		t.Fatal("a TUN host reports the tailscaled transport")
	}
	// The mode is read at most once a minute: a change shows after that.
	c.fake.mu.Lock()
	c.fake.userspace = false
	c.fake.mu.Unlock()
	if !c.d.linkUserspace(context.Background()) {
		t.Fatal("the mode was read again within the minute")
	}
	n.shift.Add(int64(linkUserspaceFor + time.Second))
	if c.d.linkUserspace(context.Background()) {
		t.Fatal("the mode was not read again after the minute")
	}
}

// modeReads wraps the synthetic Tailscale and holds each status read until released; a
// read can fail (#689 review, F1).
type modeReads struct {
	base    *syntheticTailscale
	reads   atomic.Int32
	entered chan int32
	release chan bool // true: the read succeeds; false: it fails
}

// modeReadKey marks the test's own reads; the daemon's other reads pass through.
type modeReadKey struct{}

func (r *modeReads) Run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 0 && args[0] == "status" && ctx.Value(modeReadKey{}) != nil {
		r.entered <- r.reads.Add(1)
		select {
		case ok := <-r.release:
			if !ok {
				return nil, errors.New("synthetic status failure")
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.base.Run(ctx, args...)
}

// Reads of the Tailscale mode never overlap: a caller that comes during a read uses its
// result, and a failed read keeps the last good mode (#689 review, F1).
func TestLinkUserspaceReadsDoNotOverlap(t *testing.T) {
	n := &linkNet{}
	h := newLinkHost(t, n, "alpha", "100.64.0.1")
	h.fake.mu.Lock()
	h.fake.userspace = true
	h.fake.mu.Unlock()
	r := &modeReads{base: h.fake, entered: make(chan int32, 4), release: make(chan bool, 4)}
	h.d.remote.mu.Lock()
	h.d.remote.manager = remote.Manager{Adapter: r}
	h.d.remote.mu.Unlock()
	defer func() {
		h.d.remote.mu.Lock()
		h.d.remote.manager = remote.Manager{Adapter: h.fake}
		h.d.remote.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), modeReadKey{}, true), 10*time.Second)
	defer cancel()
	first, second := make(chan bool, 1), make(chan bool, 1)
	go func() { first <- h.d.linkUserspace(ctx) }()
	<-r.entered
	go func() { second <- h.d.linkUserspace(ctx) }()
	select {
	case n := <-r.entered:
		t.Fatalf("read %d overlapped the first", n)
	case <-time.After(200 * time.Millisecond):
	}
	r.release <- true
	if !<-first || !<-second {
		t.Fatal("the userspace mode was lost")
	}
	if got := r.reads.Load(); got != 1 {
		t.Fatalf("%d reads, want 1", got)
	}
	// After the minute, a failed read keeps the last good mode, and the next minute uses it.
	n.shift.Add(int64(linkUserspaceFor + time.Second))
	r.release <- false
	if !h.d.linkUserspace(ctx) {
		t.Fatal("a failed read replaced the good mode")
	}
	<-r.entered
	if !h.d.linkUserspace(ctx) || r.reads.Load() != 2 {
		t.Fatalf("after the failed read: %d reads", r.reads.Load())
	}
}
