package core

// Tests of introductions (LINK-GROUP-JOIN, #683): three and four in-process daemons, real
// TLS, synthetic Tailscale. All names and addresses are synthetic.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
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
	return h.state(t).PairByPin(pinOf(t, other))
}

// requireMesh checks that every two hosts hold one paired pair with each other, with the
// same ID on both sides.
func requireMesh(t *testing.T, hosts ...*linkHost) {
	t.Helper()
	for i, a := range hosts {
		if got := len(a.state(t).Pairs); got != len(hosts)-1 {
			t.Fatalf("%s has %d pairs, want %d: %+v", a.root, got, len(hosts)-1, a.state(t).Pairs)
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
	return h.state(t).Intros
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

// The confirm reply, or the acknowledgement, can be lost: the new computer asks again at its
// next pass, and a repeated answer adds no second pair.
func TestLinkIntroductionLostConfirmReply(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	joinThrough(t, b, a)
	// alpha is off, so beta cannot deliver yet.
	n.mu.Lock()
	delete(n.hosts, a.ip+":47672")
	n.mu.Unlock()
	// gamma pairs with beta, but its confirm reply is lost: the pair is confirmed on beta,
	// with an introduction that gamma did not acknowledge, and gamma holds no alpha pair.
	pb, _ := joinThrough(t, b, c)
	waitFor(t, "delivery still runs", func() bool { return len(introsOf(t, b)) == 1 && !b.d.introBusy() })
	cs := c.state(t)
	cs.Pairs = slices.DeleteFunc(cs.Pairs, func(p link.Pair) bool { return p.ID != pb.ID })
	if err := link.WriteState(c.root, cs); err != nil {
		t.Fatal(err)
	}
	bs := b.state(t)
	bs.Intros[0].NewSent = false
	if err := link.WriteState(b.root, bs); err != nil {
		t.Fatal(err)
	}
	// Unacknowledged, it is not delivered.
	n.mu.Lock()
	n.hosts[a.ip+":47672"] = a.d
	n.mu.Unlock()
	b.d.passNow()
	waitFor(t, "delivery still runs", func() bool { return !b.d.introBusy() })
	if _, ok := pairWith(t, a, c); ok {
		t.Fatal("an unacknowledged introduction was delivered")
	}
	// gamma's pass asks beta again; a second pass repeats it with no change.
	c.d.passNow()
	c.d.passNow()
	waitFor(t, "alpha never got its introduction", func() bool {
		_, ok := pairWith(t, a, c)
		return ok && len(introsOf(t, b)) == 0
	})
	requireMesh(t, a, b, c)
	// A repeated introduce adds no second pair.
	pa, _ := pairWith(t, a, c)
	_, gen, _ := a.d.linkLife()
	again := linkIntroduce{ID: pa.ID, Name: "gamma", DNS: pa.DNSName, IPs: pa.IPs, Port: pa.Port, Pin: pa.Pin}
	ok, err := a.d.acceptIntroduce(pinOf(t, b), again, gen)
	if err != nil || ok.Added {
		t.Fatalf("replay %+v %v", ok, err)
	}
	requireMesh(t, a, b, c)
	// An introduction that the new computer never acknowledges is dropped after its window.
	d := newLinkHost(t, n, "delta", "100.64.0.4")
	pd, _ := joinThrough(t, b, d)
	ds := d.state(t)
	ds.Pairs = slices.DeleteFunc(ds.Pairs, func(p link.Pair) bool { return p.ID != pd.ID })
	link.WriteState(d.root, ds)
	bs = b.state(t)
	for i := range bs.Intros {
		bs.Intros[i].NewSent = false
	}
	link.WriteState(b.root, bs)
	n.shift.Add(int64(introWindow + time.Minute))
	b.d.passNow()
	if got := introsOf(t, b); len(got) != 0 {
		t.Fatalf("expired introductions kept: %+v", got)
	}
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
	if s := c.state(t); len(s.Pairs) != 0 || len(s.Intros) != 0 {
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
