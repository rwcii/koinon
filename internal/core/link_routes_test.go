package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/remote"
)

// The link's agent API, maintainer and dashboard routes (#670): two in-process daemons with
// real TLS between them and the synthetic Tailscale adapter.

var invitationText = regexp.MustCompile(`koinon-link-1:[A-Za-z0-9_-]+`)

func linkBearer(t *testing.T, h *linkHost) string {
	t.Helper()
	bearer, err := ReadSecret(h.root)
	if err != nil {
		t.Fatal(err)
	}
	return bearer
}

func linkStatusRoute(t *testing.T, h *linkHost) LinkStatus {
	t.Helper()
	data, err := Call(context.Background(), h.d.Addresses()[0], linkBearer(t, h), "/v1/link/status", nil)
	var reply struct {
		OK     bool       `json:"ok"`
		Result LinkStatus `json:"result"`
	}
	if err != nil || json.Unmarshal(data, &reply) != nil || !reply.OK {
		t.Fatalf("status: %v %s", err, data)
	}
	return reply.Result
}

func TestLinkMaintainerRoutes(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	bearerA, bearerB := linkBearer(t, a), linkBearer(t, b)

	// Every change needs a maintainer token; the bearer alone is not enough.
	if status, reply := maintainerCall(t, b.d, bearerB, strings.Repeat("0", 64), "link-invite", nil); status == 200 {
		t.Fatalf("invite without a token: %v", reply)
	}
	if b.state(t).Invites != nil {
		t.Fatal("a refused invite left an invitation")
	}
	tokenA, tokenB := newMaintainerToken(t, a.d, bearerA, "link-test"), newMaintainerToken(t, b.d, bearerB, "link-test")
	invite := mustMaintainer(t, b.d, bearerB, tokenB, "link-invite", nil)
	text, _ := invite["invitation"].(string)
	if !invitationText.MatchString(text) || invite["expires_seconds"] != float64(600) {
		t.Fatalf("invite: %v", invite)
	}
	refusedMaintainer(t, a.d, bearerA, tokenA, "link-join", map[string]any{"invitation": "koinon-link-1:short"}, "link_invitation_invalid")
	joined := mustMaintainer(t, a.d, bearerA, tokenA, "link-join", map[string]any{"invitation": text})
	if joined["name"] != "beta" || joined["advertised"] != "beta" || len(joined["id"].(string)) != 32 {
		t.Fatalf("join: %v", joined)
	}
	id := joined["id"].(string)

	st := linkStatusRoute(t, a)
	if !st.Configured || st.Name != "alpha" || len(st.Pairs) != 1 || st.Pairs[0].Name != "beta" || st.Pairs[0].Observed.State != "connected" {
		t.Fatalf("status: %+v", st)
	}
	rs := a.d.remoteStatus(context.Background())
	if rs.LinkPort != 47672 || rs.LinkListener != "ready" || rs.LinkServe != "ready" {
		t.Fatalf("remote status link: %+v", rs)
	}

	if status, reply := maintainerCall(t, a.d, bearerA, tokenA, "link-rename", map[string]any{"pair": id, "name": "Bad Name"}); status == 200 || reply["code"] != "link_name_invalid" {
		t.Fatalf("rename refused: %d %v", status, reply)
	}
	if status, reply := maintainerCall(t, a.d, bearerA, tokenA, "link-rename", map[string]any{"pair": id, "name": "laptop"}); status != 200 {
		t.Fatalf("rename: %d %v", status, reply)
	}
	if got := linkStatusRoute(t, a).Pairs[0]; got.Name != "laptop" || got.Advertised != "beta" || !got.Override {
		t.Fatalf("renamed: %+v", got)
	}
	if status, reply := maintainerCall(t, a.d, bearerA, tokenA, "link-unpair", map[string]any{"pair": "laptop"}); status != 200 {
		t.Fatalf("unpair: %d %v", status, reply)
	}
	if len(a.state(t).Pairs) != 0 {
		t.Fatal("unpair kept the pair")
	}
	refusedMaintainer(t, a.d, bearerA, tokenA, "link-unpair", map[string]any{"pair": "laptop"}, "link_pair_not_found")

	// The audit records name each action; none holds the invitation or its secret.
	rowsA, rowsB := auditRows(t, a.d), auditRows(t, b.d)
	for _, want := range []string{"link-join link invitation", "link-rename pair " + id + " name laptop", "link-unpair pair laptop"} {
		if !strings.Contains(rowsA, want) {
			t.Fatalf("audit of alpha lacks %q:\n%s", want, rowsA)
		}
	}
	if !strings.Contains(rowsB, "link-invite link invitation") {
		t.Fatalf("audit of beta:\n%s", rowsB)
	}
	secret := strings.TrimPrefix(text, "koinon-link-1:")
	for _, rows := range []string{rowsA, rowsB} {
		if strings.Contains(rows, secret) || strings.Contains(rows, "koinon-link-1:") {
			t.Fatal("an audit record holds the invitation")
		}
	}
}

func TestLinkDashboardComputers(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ca, cb := newActionClient(t, a.d, a.root), newActionClient(t, b.d, b.root)

	page := dashboardDo(t, b.d, "GET", "/dashboard/settings", cb.cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, `id="computers"`) || !strings.Contains(page.body, "AGENT@beta") ||
		!strings.Contains(page.body, "Copy invitation") {
		t.Fatalf("settings: %d %s", page.status, page.body)
	}
	// Without the CSRF value the action is refused and makes no invitation.
	if r := dashboardDo(t, b.d, "POST", "/dashboard/actions/link-invite", cb.cookie, cb.sameOrigin, url.Values{}); r.status != http.StatusForbidden {
		t.Fatalf("invite without CSRF: %d", r.status)
	}
	if b.state(t).Invites != nil {
		t.Fatal("a refused invite left an invitation")
	}
	r := dashboardDo(t, b.d, "POST", "/dashboard/actions/link-invite", cb.cookie, cb.sameOrigin, url.Values{"csrf": {cb.csrf}})
	text := invitationText.FindString(r.body)
	if r.status != 200 || text == "" || !strings.Contains(r.body, "<svg") || r.header.Get("Location") != "" {
		t.Fatalf("invite page: %d %s", r.status, r.body)
	}
	if code := ca.do("link-join", url.Values{"invitation": {text}}); code != "link_joined" {
		t.Fatal(code)
	}
	if code := ca.do("link-join", url.Values{"invitation": {text}}); code != "link_already_paired" {
		t.Fatal(code)
	}
	page = dashboardDo(t, a.d, "GET", "/dashboard/settings?notice=link_already_paired", ca.cookie, nil, nil)
	if !strings.Contains(page.body, "<code>beta</code>") || !strings.Contains(page.body, "connected") ||
		!strings.Contains(page.body, ErrLinkPaired.Message) {
		t.Fatalf("paired list: %s", page.body)
	}
	id := a.state(t).Pairs[0].ID
	if code := ca.do("link-rename", url.Values{"pair": {id}, "name": {"laptop"}}); code != "link_renamed" {
		t.Fatal(code)
	}
	if code := ca.do("link-unpair", url.Values{"pair": {id}}); code != "link_unpaired" {
		t.Fatal(code)
	}
	if code := ca.do("link-cancel", url.Values{"pair": {id}}); code != "link_pair_not_found" {
		t.Fatal(code)
	}
	if strings.Contains(auditRows(t, b.d), text) || strings.Contains(auditRows(t, a.d), text) {
		t.Fatal("an audit record holds the invitation")
	}
}

func TestLinkDashboardAsksForTheName(t *testing.T) {
	d, root := startTestDaemon(t)
	fake := &syntheticTailscale{dns: "gamma.example-tailnet.ts.net.", ips: []string{"100.64.0.3"}}
	d.remote.mu.Lock()
	d.remote.manager = remote.Manager{Adapter: fake}
	d.remote.mu.Unlock()
	d.store.secretCost = 1000
	ctx := context.Background()
	if _, err := d.changeSecret(ctx, "set", testSecret); err != nil {
		t.Fatal(err)
	}
	c := newActionClient(t, d, root)
	page := dashboardDo(t, d, "GET", "/dashboard/settings", c.cookie, nil, nil)
	if !strings.Contains(page.body, "Turn on remote access above first.") || strings.Contains(page.body, "link-invite") {
		t.Fatalf("remote off: %s", page.body)
	}
	if code := c.do("link-invite", url.Values{}); code != ErrLinkRemoteOff.Code {
		t.Fatal(code)
	}
	if err := d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	page = dashboardDo(t, d, "GET", "/dashboard/settings", c.cookie, nil, nil)
	if !strings.Contains(page.body, "Name this computer") || strings.Contains(page.body, "link-invite") {
		t.Fatalf("not named: %s", page.body)
	}
	if code := c.do("link-name", url.Values{"name": {"Bad Name"}}); code != "link_name_invalid" {
		t.Fatal(code)
	}
	if code := c.do("link-name", url.Values{"name": {"gamma"}, "port": {"47673"}}); code != "link_named" {
		t.Fatal(code)
	}
	if st := d.linkStatus(ctx); !st.Configured || st.Name != "gamma" || st.Port != 47673 || st.Listener != "ready" {
		t.Fatalf("%+v", st)
	}
}

func TestLinkRefusedKeyIsShown(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	idB, err := b.d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	idA, err := a.d.linkIdentity()
	if err != nil {
		t.Fatal(err)
	}
	// alpha is not paired with beta and beta has no open invitation.
	session, err := a.d.connectLink(ctx, a.d.openGen(), []string{b.ip}, 47672, idB.Pin)
	if err == nil {
		err = session.call("ping", linkPairID{ID: strings.Repeat("0", 32)}, "pong", nil)
		session.conn.Close()
	}
	if !linkKeyRefused(err) {
		t.Fatalf("an unpaired key was not refused in the handshake: %v", err)
	}
	st := b.d.linkStatus(ctx)
	if st.Rejected == nil || st.Rejected.Fingerprint != idA.Pin || st.Rejected.At == 0 {
		t.Fatalf("rejected key not shown: %+v", st.Rejected)
	}
}

// A live connection ends at Remote disable, and a client that kept a TLS session cache cannot
// resume into a pair that was removed: the server issues no tickets, so each connection runs
// the full handshake with its pin check.
func TestLinkDisableEndsConnectionAndUnpairRefusesResumption(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	ctx := context.Background()
	text, err := b.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.d.linkJoin(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	idA, _ := a.d.linkIdentity()
	cache := tls.NewLRUClientSessionCache(4)
	dial := func() (*tls.Conn, error) {
		raw, err := n.dial(ctx, b.ip+":47672")
		if err != nil {
			return nil, err
		}
		config := linkClientConfig(idA, p.Pin)
		config.ClientSessionCache = cache
		conn := tls.Client(raw, config)
		if err := conn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	conn, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	session := linkSession{conn: conn}
	if err := session.call("ping", linkPairID{ID: p.ID}, "pong", nil); err != nil {
		t.Fatalf("first ping: %v", err)
	}
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.call("ping", linkPairID{ID: p.ID}, "pong", nil); err == nil {
		t.Fatal("a connection stayed open after Remote disable")
	}
	conn.Close()
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	conn, err = dial()
	if err != nil {
		t.Fatal(err)
	}
	// The ping reads the stream, where a server that issues tickets would send one.
	if err := (linkSession{conn: conn}).call("ping", linkPairID{ID: p.ID}, "pong", nil); err != nil {
		t.Fatalf("ping after re-enable: %v", err)
	}
	conn.Close()
	// beta removes the pair; alpha's cached session gives it nothing.
	if err := b.d.removeLinkPair(idA.Pin, b.state(t).Pairs[0].ID, b.d.openGen()); err != nil {
		t.Fatal(err)
	}
	conn, err = dial()
	if err == nil {
		err = linkSession{conn: conn}.call("ping", linkPairID{ID: p.ID}, "pong", nil)
		if conn.ConnectionState().DidResume {
			t.Fatal("the server resumed a TLS session after unpair")
		}
		conn.Close()
	}
	if !linkKeyRefused(err) {
		t.Fatalf("the removed key was not refused: %v", err)
	}
}
