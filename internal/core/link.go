package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/remote"
)

// The daemon link between two of the maintainer's computers (#670, chunk 01: pairing). It is
// part of Remote (LINK-670-PAIR): Remote owns its raw TCP Serve mapping, Remote off closes
// it, and pairing needs Remote on. Lock order: remote.mu, then link.mu. No network call holds
// link.mu.

// Times of the link.
const (
	linkInviteLife    = 10 * time.Minute
	linkPingInterval  = 30 * time.Second
	linkConnectedFor  = 3 * linkPingInterval
	linkIOTimeout     = 15 * time.Second
	linkMaxFramesConn = 64
)

// Link refusals.
var (
	ErrLinkRemoteOff   = Refusal{"link_remote_required", "turn Remote on with koinon remote enable on this computer first"}
	ErrLinkNotSetUp    = Refusal{"link_setup_required", "set this computer's name first: Add a computer in the Remote settings, or koinon link setup"}
	ErrLinkNoPair      = Refusal{"link_pair_not_found", "no paired computer has that name or ID; see koinon link status"}
	ErrLinkSelf        = Refusal{"link_invitation_self", "this invitation is from this computer; paste it on the other computer"}
	ErrLinkPaired      = Refusal{"link_already_paired", "this computer is already paired with that computer; unpair it first to pair it again"}
	ErrLinkNameUsed    = Refusal{"link_name_used", "another paired computer uses that name; choose another"}
	ErrLinkPortSame    = Refusal{"link_port_invalid", "the link port must differ from the Remote dashboard port"}
	ErrLinkUnreachable = Refusal{"link_unreachable", "the other computer did not answer at any of its Tailscale addresses; check that it is on, connected to the tailnet and has Remote on, then try again"}
	ErrLinkWrongKey    = Refusal{"link_key_mismatch", "the other computer's key does not match the invitation; make a new invitation on that computer"}
	ErrLinkInvitation  = Refusal{"link_invitation_refused", "the other computer refused the invitation: it expired, was used or was cancelled; make a new one"}
	ErrLinkLimit       = Refusal{"link_limit", "this computer has the most pairs or open invitations that it can hold"}
	ErrLinkKeyRefused  = Refusal{"link_key_refused", "the other computer refused this computer's key: it has no pair and no open invitation for it"}
)

type linkObservation struct {
	State string `json:"state"`
	At    int64  `json:"at,omitempty"`
	Error string `json:"error,omitempty"`
}

type linkRuntime struct {
	mu       sync.Mutex
	identity *link.Identity
	listener net.Listener
	conns    map[net.Conn]string
	observed map[string]linkObservation
	stop     chan struct{}
	// dial connects to a paired computer, and now is the link's clock; tests set them
	// before Remote opens the link.
	dial func(ctx context.Context, address string) (net.Conn, error)
	now  func() time.Time
}

func (d *Daemon) linkNow() time.Time {
	if d.link.now != nil {
		return d.link.now()
	}
	return d.store.now()
}

func (d *Daemon) linkDial(ctx context.Context, address string) (net.Conn, error) {
	d.link.mu.Lock()
	dial := d.link.dial
	d.link.mu.Unlock()
	if dial != nil {
		return dial(ctx, address)
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", address)
}

// linkIdentity loads the link key once.
func (d *Daemon) linkIdentity() (link.Identity, error) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	return d.linkIdentityLocked()
}

func (d *Daemon) linkIdentityLocked() (link.Identity, error) {
	if d.link.identity == nil {
		id, err := link.LoadIdentity(d.root, d.linkNow())
		if err != nil {
			return link.Identity{}, err
		}
		d.link.identity = &id
	}
	return *d.link.identity, nil
}

func peerPin(cs tls.ConnectionState) string {
	if len(cs.PeerCertificates) == 0 {
		return ""
	}
	return link.Pin(cs.PeerCertificates[0])
}

var errLinkPin = errors.New("link_key_not_admitted")

// linkKeyRefused reports whether the other computer refused this computer's key in the
// handshake: its pin check sends the bad_certificate alert. In TLS 1.3 the alert can arrive
// after the client's handshake ends, at the first read.
func linkKeyRefused(err error) bool {
	if errors.Is(err, ErrLinkKeyRefused) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error" && op.Err != nil && op.Err.Error() == "tls: bad certificate"
}

// linkAdmits reports whether a client key may finish the handshake: a paired or pending key,
// or any key while an invitation is open (then only for the pairing exchange).
func (d *Daemon) linkAdmits(pin string) bool {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return false
	}
	if _, ok := s.PairByPin(pin); ok {
		return true
	}
	now := d.linkNow().Unix()
	for _, i := range s.Invites {
		if i.Expires > now {
			return true
		}
	}
	return false
}

func (d *Daemon) linkServerConfig(id link.Identity) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{id.Certificate}, ClientAuth: tls.RequireAnyClientCert,
		// Resumption is off, so the pin check of VerifyConnection runs on every connection.
		SessionTicketsDisabled: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if pin := peerPin(cs); pin == "" || !d.linkAdmits(pin) {
				return errLinkPin
			}
			return nil
		}}
}

func linkClientConfig(id link.Identity, want string) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{id.Certificate},
		// The server's certificate is self-signed: the pin, checked below, is its identity.
		InsecureSkipVerify: true, ClientSessionCache: nil,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if peerPin(cs) != want {
				return ErrLinkWrongKey
			}
			return nil
		}}
}

// openLinkLocked starts the link listener on the journaled target. The remote lock is held.
func (d *Daemon) openLinkLocked(held net.Listener) error {
	c := d.remote.config
	if c == nil || c.Link == nil {
		if held != nil {
			held.Close()
		}
		return nil
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.listener != nil {
		if held != nil {
			held.Close()
		}
		return nil
	}
	id, err := d.linkIdentityLocked()
	if err != nil {
		if held != nil {
			held.Close()
		}
		return err
	}
	if held == nil {
		if held, err = net.Listen("tcp4", c.Link.Target); err != nil {
			return fmt.Errorf("link_listener_unavailable: the saved link port is occupied; free it or disable then enable Remote: %w", err)
		}
	}
	listener := tls.NewListener(held, d.linkServerConfig(id))
	d.link.listener = listener
	d.link.conns = map[net.Conn]string{}
	if d.link.observed == nil {
		d.link.observed = map[string]linkObservation{}
	}
	stop := make(chan struct{})
	d.link.stop = stop
	d.wg.Add(2)
	go d.acceptLink(listener)
	go d.monitorLink(stop)
	return nil
}

// closeLinkLocked closes the link listener, every link connection and the monitor.
func (d *Daemon) closeLinkLocked() {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.listener != nil {
		d.link.listener.Close()
		d.link.listener = nil
	}
	for conn := range d.link.conns {
		conn.Close()
	}
	d.link.conns = nil
	if d.link.stop != nil {
		close(d.link.stop)
		d.link.stop = nil
	}
}

func (d *Daemon) trackLink(conn net.Conn, pin string) bool {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.conns == nil {
		return false
	}
	d.link.conns[conn] = pin
	return true
}

func (d *Daemon) untrackLink(conn net.Conn) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	delete(d.link.conns, conn)
}

// closeLinkPin closes the live connections of one key, after an unpair.
func (d *Daemon) closeLinkPin(pin string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	for conn, p := range d.link.conns {
		if p == pin {
			conn.Close()
		}
	}
}

func (d *Daemon) acceptLink(l net.Listener) {
	defer d.wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.serveLink(conn)
		}()
	}
}

func (d *Daemon) observe(id, state, problem string) {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	if d.link.observed == nil {
		d.link.observed = map[string]linkObservation{}
	}
	d.link.observed[id] = linkObservation{State: state, At: d.linkNow().Unix(), Error: problem}
}

func linkRefuse(conn net.Conn, err error) {
	code, detail := "link_refused", ""
	var refusal Refusal
	if errors.As(err, &refusal) {
		code, detail = refusal.Code, refusal.Message
	}
	link.WriteFrame(conn, link.Frame{Op: "error", Error: code, Detail: detail})
}

// serveLink answers one link connection. The handshake has admitted the key; each operation
// checks again what this key may do.
func (d *Daemon) serveLink(raw net.Conn) {
	defer raw.Close()
	conn, ok := raw.(*tls.Conn)
	if !ok {
		return
	}
	conn.SetDeadline(time.Now().Add(linkIOTimeout))
	if err := conn.Handshake(); err != nil {
		return
	}
	pin := peerPin(conn.ConnectionState())
	if !d.trackLink(conn, pin) {
		return
	}
	defer d.untrackLink(conn)
	for n := 0; n < linkMaxFramesConn; n++ {
		conn.SetDeadline(time.Now().Add(linkIOTimeout))
		f, err := link.ReadFrame(conn)
		if err != nil {
			return
		}
		reply, closeAfter, err := d.linkOperation(pin, f)
		if err != nil {
			linkRefuse(conn, err)
			return
		}
		if link.WriteFrame(conn, reply) != nil || closeAfter {
			return
		}
	}
}

type linkHello struct {
	Secret string   `json:"secret"`
	Name   string   `json:"name"`
	DNS    string   `json:"dns"`
	IPs    []string `json:"ips"`
	Port   int      `json:"port"`
}

type linkPairID struct {
	ID string `json:"id"`
}

type linkHelloOK struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// linkOperation runs one request of a connection whose client key is pin.
func (d *Daemon) linkOperation(pin string, f link.Frame) (link.Frame, bool, error) {
	switch f.Op {
	case "pair-hello":
		var hello linkHello
		if f.Decode(&hello) != nil {
			return link.Frame{}, true, ErrInvalid
		}
		ok, err := d.acceptInvitation(pin, hello)
		if err != nil {
			return link.Frame{}, true, err
		}
		reply, err := link.NewFrame("hello-ok", ok)
		return reply, false, err
	case "pair-confirm", "ping":
		var body linkPairID
		if f.Decode(&body) != nil {
			return link.Frame{}, true, ErrInvalid
		}
		if err := d.confirmLinkPair(pin, body.ID); err != nil {
			return link.Frame{}, true, err
		}
		d.observe(body.ID, "connected", "")
		reply, err := link.NewFrame(map[string]string{"pair-confirm": "confirm-ok", "ping": "pong"}[f.Op], struct{}{})
		return reply, false, err
	case "unpair":
		var body linkPairID
		if f.Decode(&body) != nil {
			return link.Frame{}, true, ErrInvalid
		}
		if err := d.removeLinkPair(pin, body.ID); err != nil {
			return link.Frame{}, true, err
		}
		reply, err := link.NewFrame("unpair-ok", struct{}{})
		return reply, true, err
	}
	return link.Frame{}, true, Refusal{"link_operation_unknown", "this computer does not know that link operation; update Koinon on both computers"}
}

// acceptInvitation consumes one open invitation for a new key and records the pair as
// pending until the joining computer confirms it.
func (d *Daemon) acceptInvitation(pin string, hello linkHello) (linkHelloOK, error) {
	name := link.NormalizeName(hello.Name)
	if link.ValidateName(name) != nil || remote.NormalizeDNSName(hello.DNS) != hello.DNS || hello.DNS == "" || !remote.ValidHTTPSPort(hello.Port) {
		return linkHelloOK{}, ErrInvalid
	}
	if _, ok := link.ValidAddresses(hello.IPs); !ok {
		return linkHelloOK{}, ErrInvalid
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return linkHelloOK{}, err
	}
	if _, ok := s.PairByPin(pin); ok {
		return linkHelloOK{}, ErrLinkPaired
	}
	now := d.linkNow()
	hash, found := link.SecretHash(hello.Secret), -1
	for i, invite := range s.Invites {
		if invite.SecretHash == hash && invite.Expires > now.Unix() {
			found = i
		}
	}
	if found < 0 {
		return linkHelloOK{}, ErrLinkInvitation
	}
	if len(s.Pairs) >= link.MaxPairs {
		return linkHelloOK{}, ErrLinkLimit
	}
	id, err := link.NewID()
	if err != nil {
		return linkHelloOK{}, err
	}
	s.Invites = slices.Delete(s.Invites, found, found+1)
	s.Pairs = append(s.Pairs, link.Pair{ID: id, Advertised: name, Effective: s.FreeName(name, ""), Pin: pin, DNSName: hello.DNS, IPs: hello.IPs, Port: hello.Port,
		State: link.Pending, Role: link.Inviter, Created: now.Unix(), Expires: now.Add(linkInviteLife).Unix()})
	if err := link.WriteState(d.root, s); err != nil {
		return linkHelloOK{}, err
	}
	return linkHelloOK{ID: id, Name: s.Name}, nil
}

// confirmLinkPair checks that pin holds pair id; a pending pair of this key is confirmed.
func (d *Daemon) confirmLinkPair(pin, id string) error {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return err
	}
	for i, p := range s.Pairs {
		if p.Pin != pin || p.ID != id {
			continue
		}
		if p.State == link.Pending {
			s.Pairs[i].State, s.Pairs[i].Expires = link.Paired, 0
			return link.WriteState(d.root, s)
		}
		return nil
	}
	return ErrLinkNoPair
}

// removeLinkPair removes pair id of key pin, at the other computer's unpair.
func (d *Daemon) removeLinkPair(pin, id string) error {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return err
	}
	i := slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.Pin == pin && p.ID == id })
	if i < 0 {
		d.link.mu.Unlock()
		return ErrLinkNoPair
	}
	s.Pairs = slices.Delete(s.Pairs, i, i+1)
	delete(d.link.observed, id)
	err = link.WriteState(d.root, s)
	d.link.mu.Unlock()
	if err == nil {
		d.closeLinkPin(pin)
	}
	return err
}

// linkSession is one client connection to a paired computer.
type linkSession struct{ conn *tls.Conn }

func (s linkSession) call(op string, body any, wantOp string, out any) error {
	f, err := link.NewFrame(op, body)
	if err != nil {
		return err
	}
	s.conn.SetDeadline(time.Now().Add(linkIOTimeout))
	if err := link.WriteFrame(s.conn, f); err != nil {
		return err
	}
	reply, err := link.ReadFrame(s.conn)
	if err != nil {
		return err
	}
	if reply.Op == "error" {
		return Refusal{reply.Error, reply.Detail}
	}
	if reply.Op != wantOp {
		return errors.New("link_reply_invalid")
	}
	if out != nil {
		return reply.Decode(out)
	}
	return nil
}

// connectLink dials the addresses in order and finishes a TLS handshake that checks the
// server's pin. A wrong key, or a refusal of this computer's key, is reported at once: it is
// never retried at another address.
func (d *Daemon) connectLink(ctx context.Context, ips []string, port int, pin string) (linkSession, error) {
	id, err := d.linkIdentity()
	if err != nil {
		return linkSession{}, err
	}
	var last error = ErrLinkUnreachable
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, linkIOTimeout)
		raw, err := d.linkDial(dialCtx, netip.AddrPortFrom(addr, uint16(port)).String())
		cancel()
		if err != nil {
			continue
		}
		conn := tls.Client(raw, linkClientConfig(id, pin))
		conn.SetDeadline(time.Now().Add(linkIOTimeout))
		if err := conn.HandshakeContext(ctx); err != nil {
			conn.Close()
			if errors.Is(err, ErrLinkWrongKey) {
				return linkSession{}, ErrLinkWrongKey
			}
			if linkKeyRefused(err) {
				return linkSession{}, ErrLinkKeyRefused
			}
			last = ErrLinkUnreachable
			continue
		}
		return linkSession{conn: conn}, nil
	}
	return linkSession{}, last
}

// connectPair dials a paired computer at its saved addresses, then at the addresses that
// Tailscale reports for its full name. New addresses are saved only after a connection with
// the pinned key.
func (d *Daemon) connectPair(ctx context.Context, p link.Pair) (linkSession, error) {
	session, err := d.connectLink(ctx, p.IPs, p.Port, p.Pin)
	if err == nil || errors.Is(err, ErrLinkWrongKey) || errors.Is(err, ErrLinkKeyRefused) {
		return session, err
	}
	d.remote.mu.Lock()
	manager := d.remote.manager
	d.remote.mu.Unlock()
	status, inspectErr := manager.Inspect(ctx)
	if inspectErr != nil {
		return linkSession{}, err
	}
	fresh := status.PeerAddresses(p.DNSName)
	if len(fresh) > 4 {
		fresh = fresh[:4]
	}
	if _, ok := link.ValidAddresses(fresh); !ok || slices.Equal(fresh, p.IPs) {
		return linkSession{}, err
	}
	session, err = d.connectLink(ctx, fresh, p.Port, p.Pin)
	if err != nil {
		return session, err
	}
	d.link.mu.Lock()
	if s, readErr := link.ReadState(d.root); readErr == nil {
		if i := slices.IndexFunc(s.Pairs, func(q link.Pair) bool { return q.ID == p.ID && q.Pin == p.Pin }); i >= 0 {
			s.Pairs[i].IPs = fresh
			link.WriteState(d.root, s)
		}
	}
	d.link.mu.Unlock()
	return session, nil
}

// monitorLink pings each paired computer while the link listener is open, removes expired
// pending pairs and invitations, and records what it observes.
func (d *Daemon) monitorLink(stop chan struct{}) {
	defer d.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-stop:
			return
		case <-timer.C:
			d.linkPass(stop)
			timer.Reset(linkPingInterval)
		}
	}
}

func (d *Daemon) linkPass(stop chan struct{}) {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	if err == nil {
		now := d.linkNow().Unix()
		changed := false
		s.Pairs = slices.DeleteFunc(s.Pairs, func(p link.Pair) bool {
			expired := p.State == link.Pending && p.Expires <= now
			changed = changed || expired
			return expired
		})
		s.Invites = slices.DeleteFunc(s.Invites, func(i link.Invite) bool {
			expired := i.Expires <= now
			changed = changed || expired
			return expired
		})
		if changed {
			link.WriteState(d.root, s)
		}
	}
	d.link.mu.Unlock()
	if err != nil {
		return
	}
	for _, p := range s.Pairs {
		select {
		case <-stop:
			return
		default:
		}
		if p.State != link.Paired && p.Role == link.Inviter {
			continue // the joining computer confirms its pending pair
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*linkIOTimeout)
		session, err := d.connectPair(ctx, p)
		if err == nil {
			err = session.call("ping", linkPairID{ID: p.ID}, "pong", nil)
			session.conn.Close()
		}
		cancel()
		switch {
		case err == nil:
			d.observe(p.ID, "connected", "")
		case errors.Is(err, ErrLinkWrongKey):
			d.observe(p.ID, "rejected", ErrLinkWrongKey.Code)
		case linkKeyRefused(err):
			d.observe(p.ID, "rejected", ErrLinkKeyRefused.Code)
		default:
			d.observe(p.ID, "offline", errorCode(err))
		}
	}
}

// linkReady reports whether Remote is on and the link listener is open. The remote lock
// is held.
func (d *Daemon) linkReadyLocked() error {
	rt := &d.remote
	if rt.config == nil || !rt.config.Enabled || rt.server == nil {
		return ErrLinkRemoteOff
	}
	if rt.config.Link == nil {
		return ErrLinkNotSetUp
	}
	d.link.mu.Lock()
	open := d.link.listener != nil
	d.link.mu.Unlock()
	if !open {
		return ErrLinkRemoteOff
	}
	return nil
}

// ownTailnet reads this computer's tailnet name and Tailscale addresses.
func (d *Daemon) ownTailnet(ctx context.Context) (string, []string, error) {
	d.remote.mu.Lock()
	manager := d.remote.manager
	d.remote.mu.Unlock()
	status, err := manager.Inspect(ctx)
	if err != nil {
		return "", nil, err
	}
	name := remote.NormalizeDNSName(status.DNSName)
	ips := status.TailscaleIPs
	if len(ips) > 4 {
		ips = ips[:4]
	}
	if name == "" || len(ips) == 0 {
		return "", nil, remote.Status{}.Ready()
	}
	return name, ips, nil
}

// linkInvite opens an invitation and returns its text.
func (d *Daemon) linkInvite(ctx context.Context) (string, error) {
	d.remote.mu.Lock()
	err := d.linkReadyLocked()
	port := 0
	if err == nil {
		port = d.remote.config.Link.Port
	}
	d.remote.mu.Unlock()
	if err != nil {
		return "", err
	}
	dns, ips, err := d.ownTailnet(ctx)
	if err != nil {
		return "", err
	}
	secret, err := link.NewSecret()
	if err != nil {
		return "", err
	}
	id, err := d.linkIdentity()
	if err != nil {
		return "", err
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return "", err
	}
	now := d.linkNow()
	s.Invites = slices.DeleteFunc(s.Invites, func(i link.Invite) bool { return i.Expires <= now.Unix() })
	if len(s.Invites) >= link.MaxInvites {
		return "", ErrLinkLimit
	}
	s.Invites = append(s.Invites, link.Invite{SecretHash: link.SecretHash(secret), Created: now.Unix(), Expires: now.Add(linkInviteLife).Unix()})
	text, err := link.Invitation{Version: 1, DNSName: dns, IPs: ips, Port: port, Pin: id.Pin, Name: s.Name, Secret: secret}.Encode()
	if err != nil {
		return "", err
	}
	if err := link.WriteState(d.root, s); err != nil {
		return "", err
	}
	return text, nil
}

// linkJoin pairs with the computer that made the invitation.
func (d *Daemon) linkJoin(ctx context.Context, text string) (link.Pair, error) {
	invitation, err := link.ParseInvitation(text)
	if err != nil {
		return link.Pair{}, Refusal{"link_invitation_invalid", "paste the whole invitation from the other computer's Add a computer step"}
	}
	d.remote.mu.Lock()
	err = d.linkReadyLocked()
	port := 0
	if err == nil {
		port = d.remote.config.Link.Port
	}
	d.remote.mu.Unlock()
	if err != nil {
		return link.Pair{}, err
	}
	id, err := d.linkIdentity()
	if err != nil {
		return link.Pair{}, err
	}
	if invitation.Pin == id.Pin {
		return link.Pair{}, ErrLinkSelf
	}
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return link.Pair{}, err
	}
	if _, ok := s.PairByPin(invitation.Pin); ok {
		return link.Pair{}, ErrLinkPaired
	}
	if len(s.Pairs) >= link.MaxPairs {
		return link.Pair{}, ErrLinkLimit
	}
	dns, ips, err := d.ownTailnet(ctx)
	if err != nil {
		return link.Pair{}, err
	}
	// The handshake checks the invitation's pin before the secret is sent.
	session, err := d.connectLink(ctx, invitation.IPs, invitation.Port, invitation.Pin)
	if linkKeyRefused(err) {
		// The other computer admits an unknown key only while an invitation is open.
		return link.Pair{}, ErrLinkInvitation
	}
	if err != nil {
		return link.Pair{}, err
	}
	defer session.conn.Close()
	var ok linkHelloOK
	if err := session.call("pair-hello", linkHello{Secret: invitation.Secret, Name: s.Name, DNS: dns, IPs: ips, Port: port}, "hello-ok", &ok); err != nil {
		if linkKeyRefused(err) {
			return link.Pair{}, ErrLinkInvitation
		}
		var refusal Refusal
		if errors.As(err, &refusal) {
			return link.Pair{}, refusal
		}
		return link.Pair{}, ErrLinkUnreachable
	}
	if len(ok.ID) != 32 {
		return link.Pair{}, errors.New("link_reply_invalid")
	}
	d.link.mu.Lock()
	s, err = link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return link.Pair{}, err
	}
	if _, exists := s.PairByPin(invitation.Pin); exists {
		d.link.mu.Unlock()
		return link.Pair{}, ErrLinkPaired
	}
	p := link.Pair{ID: ok.ID, Advertised: invitation.Name, Effective: s.FreeName(invitation.Name, ""), Pin: invitation.Pin, DNSName: invitation.DNSName,
		IPs: invitation.IPs, Port: invitation.Port, State: link.Paired, Role: link.Joiner, Created: d.linkNow().Unix()}
	s.Pairs = append(s.Pairs, p)
	err = link.WriteState(d.root, s)
	d.link.mu.Unlock()
	if err != nil {
		return link.Pair{}, err
	}
	// The other computer's pair is pending until this confirm, or until the next ping.
	if session.call("pair-confirm", linkPairID{ID: p.ID}, "confirm-ok", nil) == nil {
		d.observe(p.ID, "connected", "")
	}
	return p, nil
}

// findPair finds a pair by ID or effective name.
func findPair(s link.State, ref string) int {
	return slices.IndexFunc(s.Pairs, func(p link.Pair) bool { return p.ID == ref || p.Effective == ref })
}

// linkUnpair removes a pair here at once and tells the other computer when it answers.
func (d *Daemon) linkUnpair(ctx context.Context, ref string) error {
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	if err != nil {
		d.link.mu.Unlock()
		return err
	}
	i := findPair(s, ref)
	if i < 0 {
		d.link.mu.Unlock()
		return ErrLinkNoPair
	}
	p := s.Pairs[i]
	s.Pairs = slices.Delete(s.Pairs, i, i+1)
	delete(d.link.observed, p.ID)
	err = link.WriteState(d.root, s)
	d.link.mu.Unlock()
	if err != nil {
		return err
	}
	d.closeLinkPin(p.Pin)
	// Best effort: the other computer also refuses this key at its next connection.
	notify, cancel := context.WithTimeout(ctx, 2*linkIOTimeout)
	defer cancel()
	if session, err := d.connectLink(notify, p.IPs, p.Port, p.Pin); err == nil {
		session.call("unpair", linkPairID{ID: p.ID}, "unpair-ok", nil)
		session.conn.Close()
	}
	return nil
}

// linkCancel removes a pending pair, or with ref "invitations" every open invitation.
func (d *Daemon) linkCancel(ref string) error {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return err
	}
	if ref == "invitations" {
		s.Invites = nil
		return link.WriteState(d.root, s)
	}
	i := findPair(s, ref)
	if i < 0 || s.Pairs[i].State != link.Pending {
		return ErrLinkNoPair
	}
	delete(d.link.observed, s.Pairs[i].ID)
	s.Pairs = slices.Delete(s.Pairs, i, i+1)
	return link.WriteState(d.root, s)
}

// linkRename sets the local name of a pair; an empty name returns to the advertised name.
// A name that another pair uses is refused, never moved.
func (d *Daemon) linkRename(ref, name string) error {
	name = link.NormalizeName(name)
	if name != "" && link.ValidateName(name) != nil {
		return Refusal{"link_name_invalid", "use lower-case letters, digits, hyphens and dots, at most 253 characters"}
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil {
		return err
	}
	i := findPair(s, ref)
	if i < 0 {
		return ErrLinkNoPair
	}
	if name == "" {
		s.Pairs[i].Override = ""
		s.Pairs[i].Effective = s.FreeName(s.Pairs[i].Advertised, s.Pairs[i].ID)
	} else {
		if s.FreeName(name, s.Pairs[i].ID) != name {
			return ErrLinkNameUsed
		}
		s.Pairs[i].Override, s.Pairs[i].Effective = name, name
	}
	return link.WriteState(d.root, s)
}

// linkSetup sets this computer's advertised name and link port, journals the link's Serve
// mapping and opens it. Remote must be on.
func (d *Daemon) linkSetup(ctx context.Context, name string, port int) error {
	name = link.NormalizeName(name)
	if err := link.ValidateName(name); err != nil {
		return Refusal{"link_name_invalid", "use lower-case letters, digits, hyphens and dots, at most 253 characters"}
	}
	if port == 0 {
		port = link.DefaultPort
	}
	if !remote.ValidHTTPSPort(port) {
		return Refusal{"link_port_invalid", "use a link port of 1 to 65535"}
	}
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.config == nil || !rt.config.Enabled || rt.server == nil {
		return ErrLinkRemoteOff
	}
	if port == rt.config.Mapping.Port() {
		return ErrLinkPortSame
	}
	d.link.mu.Lock()
	s, err := link.ReadState(d.root)
	if err == nil {
		s.Name, s.Port = name, port
		err = link.WriteState(d.root, s)
	}
	d.link.mu.Unlock()
	if err != nil {
		return err
	}
	c, err := ReadRemoteConfig(d.root)
	if err != nil {
		return err
	}
	if c == nil || !c.Enabled {
		return ErrLinkRemoteOff
	}
	if c.Link != nil && c.Link.Port == port {
		owned := c.LinkOwned
		if !owned {
			c.LinkOwned = true
			if err := writeRemoteConfig(d.root, c); err != nil {
				return err
			}
		}
		rt.config = c
		if err := d.openLinkLocked(nil); err != nil {
			return err
		}
		return rt.manager.EnsureTCP(ctx, *c.Link, owned)
	}
	if c.Link != nil && c.LinkOwned {
		// A changed port: the old mapping goes first.
		d.closeLinkLocked()
		if err := rt.manager.RemoveTCP(ctx, *c.Link); err != nil {
			return err
		}
		c.LinkOwned = false
		if err := writeRemoteConfig(d.root, c); err != nil {
			return err
		}
	}
	held, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	mapping := remote.TCPMapping{Port: port, Target: held.Addr().String()}
	if err := rt.manager.CheckTCP(ctx, mapping, false); err != nil {
		held.Close()
		return err
	}
	c.Link, c.LinkOwned = &mapping, true
	if err := writeRemoteConfig(d.root, c); err != nil {
		held.Close()
		return err
	}
	rt.config = c
	d.closeLinkLocked()
	if err := d.openLinkLocked(held); err != nil {
		return errors.Join(err, rt.manager.RemoveTCP(ctx, mapping))
	}
	return rt.manager.EnsureTCP(ctx, mapping, true)
}

// LinkPairStatus is one paired computer in the link status.
type LinkPairStatus struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Advertised string          `json:"advertised"`
	Override   bool            `json:"override,omitempty"`
	State      string          `json:"state"`
	Role       string          `json:"role"`
	Observed   linkObservation `json:"observed"`
	Created    int64           `json:"created"`
	Expires    int64           `json:"expires,omitempty"`
}

// LinkStatus is this computer's link setup, its listener and Serve mapping, and its pairs.
type LinkStatus struct {
	Configured   bool             `json:"configured"`
	Name         string           `json:"name,omitempty"`
	SuggestShort string           `json:"suggest_short,omitempty"`
	SuggestLong  string           `json:"suggest_long,omitempty"`
	Port         int              `json:"port,omitempty"`
	Fingerprint  string           `json:"fingerprint,omitempty"`
	Remote       bool             `json:"remote"`
	Listener     string           `json:"listener"`
	Serve        string           `json:"serve"`
	Invitations  int              `json:"invitations"`
	Pairs        []LinkPairStatus `json:"pairs"`
	Error        string           `json:"error,omitempty"`
}

func (d *Daemon) linkStatus(ctx context.Context) LinkStatus {
	result := LinkStatus{Listener: "off", Serve: "off", Pairs: []LinkPairStatus{}}
	d.remote.mu.Lock()
	c := d.remote.config
	result.Remote = c != nil && c.Enabled && d.remote.server != nil
	var mapping *remote.TCPMapping
	if c != nil && c.Link != nil {
		copied := *c.Link
		mapping = &copied
	}
	manager := d.remote.manager
	d.remote.mu.Unlock()
	d.link.mu.Lock()
	open := d.link.listener != nil
	observed := map[string]linkObservation{}
	for k, v := range d.link.observed {
		observed[k] = v
	}
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Configured = s.Name != "" && mapping != nil
	result.Name, result.Port = s.Name, s.Port
	if s.Name == "" {
		result.SuggestShort = link.ShortName()
		result.SuggestLong = link.LongName(ctx)
	}
	if open {
		result.Listener = "ready"
	}
	if id, err := d.linkIdentity(); err == nil {
		result.Fingerprint = id.Pin
	}
	if mapping != nil && result.Remote {
		result.Serve = "unknown"
		if status, err := manager.Inspect(ctx); err == nil {
			result.Serve = "missing"
			if status.TCPMappingPresent(*mapping) {
				result.Serve = "ready"
			}
		}
	}
	now := d.linkNow().Unix()
	for _, i := range s.Invites {
		if i.Expires > now {
			result.Invitations++
		}
	}
	for _, p := range s.Pairs {
		o := observed[p.ID]
		switch {
		case p.State == link.Pending:
			o = linkObservation{State: "pending", At: p.Created}
		case !open:
			o = linkObservation{State: "offline", At: o.At, Error: ErrLinkRemoteOff.Code}
		case o.State == "connected" && now-o.At > int64(linkConnectedFor/time.Second):
			o.State = "offline"
		case o.State == "":
			o.State = "offline"
		}
		result.Pairs = append(result.Pairs, LinkPairStatus{ID: p.ID, Name: p.Effective, Advertised: p.Advertised, Override: p.Override != "", State: p.State,
			Role: p.Role, Observed: o, Created: p.Created, Expires: p.Expires})
	}
	return result
}
