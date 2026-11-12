package core

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/remote"
)

// linkNet stands for the tailnet: it maps a Tailscale address and link port to a daemon's
// loopback link listener, as the raw TCP Serve forward does. All names and addresses are
// synthetic.
type linkNet struct {
	mu    sync.Mutex
	hosts map[string]*Daemon
	shift atomic.Int64
}

func (n *linkNet) dial(ctx context.Context, address string) (net.Conn, error) {
	n.mu.Lock()
	d := n.hosts[address]
	n.mu.Unlock()
	if d == nil {
		return nil, errors.New("synthetic: no host at " + address)
	}
	d.remote.mu.Lock()
	var target string
	if c := d.remote.config; c != nil && c.Link != nil {
		target = c.Link.Target
	}
	d.remote.mu.Unlock()
	if target == "" {
		return nil, errors.New("synthetic: no link")
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", target)
}

func (n *linkNet) now() time.Time { return time.Now().Add(time.Duration(n.shift.Load())) }

type linkHost struct {
	d    *Daemon
	root string
	fake *syntheticTailscale
	ip   string
}

func newLinkHost(t *testing.T, n *linkNet, name, ip string) *linkHost {
	t.Helper()
	d, root := startTestDaemon(t)
	fake := &syntheticTailscale{dns: name + ".example-tailnet.ts.net.", ips: []string{ip}}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: fake}
	d.remote.mu.Unlock()
	d.link.dial, d.link.now = n.dial, n.now
	d.store.secretCost = 1000
	ctx := context.Background()
	if _, err := d.changeSecret(ctx, "set", testSecret); err != nil {
		t.Fatal(err)
	}
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.linkSetup(ctx, name, link.DefaultPort); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	if n.hosts == nil {
		n.hosts = map[string]*Daemon{}
	}
	n.hosts[ip+":47672"] = d
	n.mu.Unlock()
	return &linkHost{d: d, root: root, fake: fake, ip: ip}
}

func (h *linkHost) state(t *testing.T) link.State {
	t.Helper()
	s, err := link.ReadState(h.root)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func pairNames(s link.State) map[string]string {
	out := map[string]string{}
	for _, p := range s.Pairs {
		out[p.Effective] = p.State
	}
	return out
}

func TestLinkPairsThroughOneInvitation(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	for _, h := range []*linkHost{a, b} {
		for _, file := range []string{link.KeyFile, link.StateFile, "remote-dashboard.json"} {
			info, err := os.Stat(filepath.Join(h.root, file))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("%s: %v %v", file, err, info)
			}
		}
		c, _ := ReadRemoteConfig(h.root)
		if c.Link == nil || !c.LinkOwned || c.Link.Port == c.Mapping.Port() {
			t.Fatalf("journal %+v", c)
		}
		h.fake.mu.Lock()
		forward := h.fake.config.TCP["47672"]
		h.fake.mu.Unlock()
		if forward != (remote.TCPConfig{TCPForward: c.Link.Target}) {
			t.Fatalf("serve %+v", forward)
		}
	}
	text, err := b.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := link.ParseInvitation(text)
	if err != nil || invitation.DNSName != "beta.example-tailnet.ts.net" || invitation.IPs[0] != "100.64.0.2" || invitation.Name != "beta" {
		t.Fatalf("invitation %v %+v", err, invitation)
	}
	if strings.Contains(text, invitation.Secret) || b.state(t).Invites[0].SecretHash != link.SecretHash(invitation.Secret) {
		t.Fatal("the secret is readable in the text or is stored")
	}
	p, err := a.d.linkJoin(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	if p.Effective != "beta" || p.State != link.Paired || p.Role != link.Joiner {
		t.Fatalf("pair %+v", p)
	}
	bs := b.state(t)
	if len(bs.Invites) != 0 || len(bs.Pairs) != 1 || bs.Pairs[0].Effective != "alpha" || bs.Pairs[0].State != link.Paired || bs.Pairs[0].ID != p.ID {
		t.Fatalf("inviter %+v", bs)
	}
	aid, _ := a.d.linkIdentity()
	bid, _ := b.d.linkIdentity()
	if bs.Pairs[0].Pin != aid.Pin || a.state(t).Pairs[0].Pin != bid.Pin {
		t.Fatal("pins")
	}
	// Each side observes the other through a ping.
	a.d.linkPass(make(chan struct{}))
	b.d.linkPass(make(chan struct{}))
	for _, h := range []*linkHost{a, b} {
		st := h.d.linkStatus(ctx)
		if !st.Configured || st.Listener != "ready" || st.Serve != "ready" || len(st.Pairs) != 1 || st.Pairs[0].Observed.State != "connected" {
			t.Fatalf("status %+v", st)
		}
	}
	// The invitation is single use.
	c := newLinkHost(t, n, "gamma", "100.64.0.3")
	if _, err := c.d.linkJoin(ctx, text); refusalCode(err) != ErrLinkInvitation.Code {
		t.Fatalf("reuse: %v", err)
	}
	if len(c.state(t).Pairs) != 0 {
		t.Fatal("refused join stored a pair")
	}
	// Paired already, and the own invitation, are refused.
	text2, _ := b.d.linkInvite(ctx)
	if _, err := a.d.linkJoin(ctx, text2); refusalCode(err) != ErrLinkPaired.Code {
		t.Fatalf("again: %v", err)
	}
	if _, err := b.d.linkJoin(ctx, text2); refusalCode(err) != ErrLinkSelf.Code {
		t.Fatalf("self: %v", err)
	}
}

func TestLinkRefusalsGiveNoPair(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	invitation, _ := link.ParseInvitation(text)
	// A wrong pin fails in the handshake, before the secret is sent: the invitation stays open.
	wrong := invitation
	wrong.Pin = strings.Repeat("0", 64)
	wrongText, _ := wrong.Encode()
	if _, err := a.d.linkJoin(ctx, wrongText); refusalCode(err) != ErrLinkWrongKey.Code {
		t.Fatalf("wrong pin: %v", err)
	}
	if len(b.state(t).Invites) != 1 {
		t.Fatal("wrong pin consumed the invitation")
	}
	// A wrong secret is refused.
	bad := invitation
	bad.Secret, _ = link.NewSecret()
	badText, _ := bad.Encode()
	if _, err := a.d.linkJoin(ctx, badText); refusalCode(err) != ErrLinkInvitation.Code {
		t.Fatalf("wrong secret: %v", err)
	}
	// An expired invitation is refused.
	n.shift.Store(int64(linkInviteLife + time.Second))
	if _, err := a.d.linkJoin(ctx, text); refusalCode(err) != ErrLinkInvitation.Code {
		t.Fatalf("expired: %v", err)
	}
	n.shift.Store(0)
	if len(a.state(t).Pairs) != 0 || len(b.state(t).Pairs) != 0 {
		t.Fatal("a refusal stored a pair")
	}
	// Garbage is refused before any connection.
	if _, err := a.d.linkJoin(ctx, "hello"); refusalCode(err) != "link_invitation_invalid" {
		t.Fatalf("garbage: %v", err)
	}
	// With no open invitation an unknown key cannot finish the handshake.
	b.d.linkCancel("invitations")
	stranger := t.TempDir()
	id, err := link.LoadIdentity(stranger, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := n.dial(ctx, "100.64.0.2:47672")
	if err != nil {
		t.Fatal(err)
	}
	bid, _ := b.d.linkIdentity()
	conn := tls.Client(raw, linkClientConfig(id, bid.Pin))
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := conn.Handshake(); err == nil {
		// TLS 1.3 reports a refused client certificate at the first read.
		f, _ := link.NewFrame("ping", linkPairID{ID: strings.Repeat("1", 32)})
		link.WriteFrame(conn, f)
		if _, err := link.ReadFrame(conn); err == nil {
			t.Fatal("unknown key admitted")
		}
	}
	conn.Close()
}

func TestLinkNeedsRemoteAndSetup(t *testing.T) {
	ctx := context.Background()
	d, _, fake := remoteFixture(t)
	fake.ips = []string{"100.64.0.9"}
	if _, err := d.linkInvite(ctx); refusalCode(err) != ErrLinkRemoteOff.Code {
		t.Fatalf("remote off invite: %v", err)
	}
	if err := d.linkSetup(ctx, "delta", 0); refusalCode(err) != ErrLinkRemoteOff.Code {
		t.Fatalf("remote off setup: %v", err)
	}
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.linkInvite(ctx); refusalCode(err) != ErrLinkNotSetUp.Code {
		t.Fatalf("not set up: %v", err)
	}
	if err := d.linkSetup(ctx, "Bad Name", 0); refusalCode(err) != "link_name_invalid" {
		t.Fatalf("name: %v", err)
	}
	if err := d.linkSetup(ctx, "delta", 443); refusalCode(err) != ErrLinkPortSame.Code {
		t.Fatalf("same port: %v", err)
	}
	if err := d.remoteEnable(ctx, link.DefaultPort); err != nil {
		t.Fatal(err)
	}
	if err := d.linkSetup(ctx, "delta", link.DefaultPort); refusalCode(err) != ErrLinkPortSame.Code {
		t.Fatalf("dashboard on link port: %v", err)
	}
}

func TestLinkRemoteDisableClosesAndKeepsPairs(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	if _, err := a.d.linkJoin(ctx, text); err != nil {
		t.Fatal(err)
	}
	open, _ := b.d.linkInvite(ctx)
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	bs := b.state(t)
	if len(bs.Invites) != 0 || len(bs.Pairs) != 1 {
		t.Fatalf("after disable %+v", bs)
	}
	c, _ := ReadRemoteConfig(b.root)
	b.fake.mu.Lock()
	_, mapped := b.fake.config.TCP["47672"]
	b.fake.mu.Unlock()
	if c.Enabled || c.LinkOwned || c.Link == nil || mapped {
		t.Fatalf("journal %+v mapped %v", c, mapped)
	}
	st := b.d.linkStatus(ctx)
	if st.Listener != "off" || st.Remote || st.Pairs[0].Observed.State != "offline" {
		t.Fatalf("status %+v", st)
	}
	// A cancelled invitation and the link of a computer with Remote off are refused.
	g := newLinkHost(t, n, "gamma", "100.64.0.3")
	if _, err := g.d.linkJoin(ctx, open); refusalCode(err) != ErrLinkUnreachable.Code {
		t.Fatalf("join while off: %v", err)
	}
	a.d.linkPass(make(chan struct{}))
	if st := a.d.linkStatus(ctx); st.Pairs[0].Observed.State != "offline" {
		t.Fatalf("peer off: %+v", st.Pairs[0])
	}
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.d.linkJoin(ctx, open); refusalCode(err) != ErrLinkInvitation.Code {
		t.Fatalf("cancelled invitation: %v", err)
	}
	c, _ = ReadRemoteConfig(b.root)
	if !c.LinkOwned {
		t.Fatalf("re-enable %+v", c)
	}
	a.d.linkPass(make(chan struct{}))
	if st := a.d.linkStatus(ctx); st.Pairs[0].Observed.State != "connected" {
		t.Fatalf("after re-enable: %+v", st.Pairs[0])
	}
}

func TestLinkDisableWithFailedRecordWriteKeepsLinkClosed(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	ctx := context.Background()
	path := filepath.Join(a.root, "remote-dashboard.json")
	os.Remove(path)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	// Runs before the host's own cleanup, whose state check needs regular files.
	t.Cleanup(func() { os.Remove(path) })
	if err := a.d.remoteDisable(ctx); err == nil {
		t.Fatal("disable reported success")
	}
	if st := a.d.linkStatus(ctx); st.Listener != "off" {
		t.Fatalf("link open after a failed disable record: %+v", st)
	}
	a.d.restoreRemote(ctx)
	if st := a.d.linkStatus(ctx); st.Listener != "off" {
		t.Fatalf("reconciler reopened the link: %+v", st)
	}
}

func TestLinkRestartWhileOffOpensNothing(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	ctx := context.Background()
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	a.d.restoreRemote(ctx)
	if st := a.d.linkStatus(ctx); st.Listener != "off" || st.Serve != "off" {
		t.Fatalf("%+v", st)
	}
}

func TestLinkUnpairRemovesBothSides(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	if _, err := a.d.linkJoin(ctx, text); err != nil {
		t.Fatal(err)
	}
	if err := a.d.linkUnpair(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if len(a.state(t).Pairs) != 0 || len(b.state(t).Pairs) != 0 {
		t.Fatalf("pairs left: %+v %+v", a.state(t).Pairs, b.state(t).Pairs)
	}
	if err := a.d.linkUnpair(ctx, "beta"); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("again: %v", err)
	}
	// The old key is refused: B no longer admits A's key.
	aid, _ := a.d.linkIdentity()
	if b.d.linkAdmits(aid.Pin) {
		t.Fatal("old key admitted")
	}
}

func TestLinkUnpairWhileOtherSideOffIsLocal(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	if _, err := a.d.linkJoin(ctx, text); err != nil {
		t.Fatal(err)
	}
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.d.linkUnpair(ctx, "beta"); err != nil {
		t.Fatal(err)
	}
	if len(a.state(t).Pairs) != 0 || len(b.state(t).Pairs) != 1 {
		t.Fatal("unpair")
	}
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	// B's ping now fails at A's handshake: A refuses B's key.
	b.d.linkPass(make(chan struct{}))
	if st := b.d.linkStatus(ctx); st.Pairs[0].Observed.State == "connected" {
		t.Fatalf("unpaired key connected: %+v", st.Pairs[0])
	}
}

func TestLinkPendingPairRecoversAtThePing(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	invitation, _ := link.ParseInvitation(text)
	// Run the hello by hand, without the confirm: B keeps a pending pair.
	session, err := a.d.connectLink(ctx, invitation.IPs, invitation.Port, invitation.Pin)
	if err != nil {
		t.Fatal(err)
	}
	var ok linkHelloOK
	if err := session.call("pair-hello", linkHello{Secret: invitation.Secret, Name: "alpha", DNS: "alpha.example-tailnet.ts.net", IPs: []string{"100.64.0.1"}, Port: link.DefaultPort}, "hello-ok", &ok); err != nil {
		t.Fatal(err)
	}
	session.conn.Close()
	if s := b.state(t); s.Pairs[0].State != link.Pending {
		t.Fatalf("%+v", s.Pairs)
	}
	if st := b.d.linkStatus(ctx); st.Pairs[0].Observed.State != "pending" {
		t.Fatalf("%+v", st.Pairs[0])
	}
	// A records its side as the joiner would, then its ping confirms B's pending pair.
	a.d.link.mu.Lock()
	s, _ := link.ReadState(a.root)
	s.Pairs = append(s.Pairs, link.Pair{ID: ok.ID, Advertised: "beta", Effective: "beta", Pin: invitation.Pin, DNSName: invitation.DNSName, IPs: invitation.IPs, Port: invitation.Port, State: link.Paired, Role: link.Joiner, Created: 1})
	link.WriteState(a.root, s)
	a.d.link.mu.Unlock()
	a.d.linkPass(make(chan struct{}))
	if s := b.state(t); s.Pairs[0].State != link.Paired {
		t.Fatalf("not confirmed: %+v", s.Pairs)
	}
	// A pending pair that nobody confirms expires.
	g := newLinkHost(t, n, "gamma", "100.64.0.3")
	text, _ = b.d.linkInvite(ctx)
	invitation, _ = link.ParseInvitation(text)
	session, err = g.d.connectLink(ctx, invitation.IPs, invitation.Port, invitation.Pin)
	if err != nil {
		t.Fatal(err)
	}
	session.call("pair-hello", linkHello{Secret: invitation.Secret, Name: "gamma", DNS: "gamma.example-tailnet.ts.net", IPs: []string{"100.64.0.3"}, Port: link.DefaultPort}, "hello-ok", &ok)
	session.conn.Close()
	if len(b.state(t).Pairs) != 2 {
		t.Fatal("no pending pair")
	}
	n.shift.Store(int64(linkInviteLife + time.Second))
	b.d.linkPass(make(chan struct{}))
	n.shift.Store(0)
	if names := pairNames(b.state(t)); len(names) != 1 || names["alpha"] != link.Paired {
		t.Fatalf("expiry: %v", names)
	}
}

func TestLinkRefreshesAddressesFromTailscale(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, _ := b.d.linkInvite(ctx)
	if _, err := a.d.linkJoin(ctx, text); err != nil {
		t.Fatal(err)
	}
	// B moves to a new Tailscale address; the system resolver plays no part.
	n.mu.Lock()
	delete(n.hosts, "100.64.0.2:47672")
	n.hosts["fd7a:115c:a1e0::2:47672"] = b.d
	n.hosts["[fd7a:115c:a1e0::2]:47672"] = b.d
	n.mu.Unlock()
	a.d.linkPass(make(chan struct{}))
	if st := a.d.linkStatus(ctx); st.Pairs[0].Observed.State != "offline" {
		t.Fatalf("stale: %+v", st.Pairs[0])
	}
	// An ambiguous answer never replaces the saved addresses.
	a.fake.mu.Lock()
	a.fake.peers = map[string]any{
		"k1": map[string]any{"DNSName": "beta.example-tailnet.ts.net.", "TailscaleIPs": []string{"fd7a:115c:a1e0::2"}},
		"k2": map[string]any{"DNSName": "beta.example-tailnet.ts.net.", "TailscaleIPs": []string{"100.64.0.99"}},
	}
	a.fake.mu.Unlock()
	a.d.linkPass(make(chan struct{}))
	if got := a.state(t).Pairs[0].IPs; len(got) != 1 || got[0] != "100.64.0.2" {
		t.Fatalf("ambiguous replaced: %v", got)
	}
	// One exact peer with one address family is enough; the new address is saved after a
	// connection with the pinned key.
	a.fake.mu.Lock()
	a.fake.peers = map[string]any{"k1": map[string]any{"DNSName": "BETA.example-tailnet.ts.net.", "TailscaleIPs": []string{"fd7a:115c:a1e0::2"}}}
	a.fake.mu.Unlock()
	a.d.linkPass(make(chan struct{}))
	if got := a.state(t).Pairs[0].IPs; len(got) != 1 || got[0] != "fd7a:115c:a1e0::2" {
		t.Fatalf("refresh: %v", got)
	}
	if st := a.d.linkStatus(ctx); st.Pairs[0].Observed.State != "connected" {
		t.Fatalf("after refresh: %+v", st.Pairs[0])
	}
	// A wrong key at a new address is refused before any message, and nothing is saved.
	g := newLinkHost(t, n, "gamma", "100.64.0.3")
	n.mu.Lock()
	n.hosts["[fd7a:115c:a1e0::2]:47672"] = g.d
	n.hosts["100.64.0.7:47672"] = g.d
	n.mu.Unlock()
	a.fake.mu.Lock()
	a.fake.peers = map[string]any{"k1": map[string]any{"DNSName": "beta.example-tailnet.ts.net.", "TailscaleIPs": []string{"100.64.0.7"}}}
	a.fake.mu.Unlock()
	a.d.linkPass(make(chan struct{}))
	if st := a.d.linkStatus(ctx); st.Pairs[0].Observed.State != "rejected" {
		t.Fatalf("wrong key: %+v", st.Pairs[0])
	}
	if got := a.state(t).Pairs[0].IPs; got[0] != "fd7a:115c:a1e0::2" {
		t.Fatalf("wrong key saved: %v", got)
	}
}

func TestLinkNamesCollideAndRename(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	c := newLinkHost(t, n, "beta", "100.64.0.3")
	ctx := context.Background()
	for _, h := range []*linkHost{b, c} {
		text, _ := h.d.linkInvite(ctx)
		if _, err := a.d.linkJoin(ctx, text); err != nil {
			t.Fatal(err)
		}
	}
	names := pairNames(a.state(t))
	if names["beta"] != link.Paired || names["beta-2"] != link.Paired {
		t.Fatalf("collision: %v", names)
	}
	if err := a.d.linkRename("beta-2", "beta"); refusalCode(err) != ErrLinkNameUsed.Code {
		t.Fatalf("rename to used: %v", err)
	}
	if err := a.d.linkRename("beta-2", "spare"); err != nil {
		t.Fatal(err)
	}
	if err := a.d.linkRename("spare", "Not Valid"); refusalCode(err) != "link_name_invalid" {
		t.Fatalf("invalid: %v", err)
	}
	if err := a.d.linkRename("spare", ""); err != nil {
		t.Fatal(err)
	}
	if names := pairNames(a.state(t)); names["beta"] == "" || names["beta-2"] == "" {
		t.Fatalf("reset: %v", names)
	}
	if err := a.d.linkCancel("beta"); refusalCode(err) != ErrLinkNoPair.Code {
		t.Fatalf("cancel of a paired computer: %v", err)
	}
}
