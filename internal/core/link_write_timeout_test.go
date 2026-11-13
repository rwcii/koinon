package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// A link action that waits for the other computer longer than the server's write timeout
// still answers with its refusal, not with a closed connection (#677). The daemon's handler
// runs behind a server with a 200 ms write timeout, as its own servers have 10 seconds, and
// the dial to the other computer fails only after 600 ms.

const shortWrite = 200 * time.Millisecond

// shortWriteServer serves d's handler as the daemon does, with a short write timeout.
func shortWriteServer(t *testing.T, d *Daemon) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: shortWrite, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return listener.Addr().String()
}

// slowUnreachable makes every link dial of d fail after three write timeouts.
func slowUnreachable(d *Daemon) {
	d.link.mu.Lock()
	d.link.dial = func(ctx context.Context, address string) (net.Conn, error) {
		select {
		case <-time.After(3 * shortWrite):
			return nil, errors.New("synthetic: no answer from " + address)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d.link.mu.Unlock()
}

// slowJoinPair gives a valid invitation of beta and makes alpha's dials slow and failing.
func slowJoinPair(t *testing.T) (*linkHost, string) {
	t.Helper()
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	bearerB := linkBearer(t, b)
	invite := mustMaintainer(t, b.d, bearerB, newMaintainerToken(t, b.d, bearerB, "link-test"), "link-invite", nil)
	text, _ := invite["invitation"].(string)
	if !invitationText.MatchString(text) {
		t.Fatalf("invite: %v", invite)
	}
	slowUnreachable(a.d)
	return a, text
}

func TestLinkJoinAnswersAfterTheWriteTimeout(t *testing.T) {
	a, text := slowJoinPair(t)
	bearer := linkBearer(t, a)
	token := newMaintainerToken(t, a.d, bearer, "link-test")
	address := shortWriteServer(t, a.d)

	// The path of koinon link join: CallTimeout with the CLI's 45 seconds.
	start := time.Now()
	_, err := CallTimeout(context.Background(), address, bearer, "/v1/maintainer/link-join",
		map[string]any{"token": token, "invitation": text}, 45*time.Second)
	var refused RefusedError
	if !errors.As(err, &refused) || refused.Code != ErrLinkUnreachable.Code {
		t.Fatalf("join: %v, want the refusal %s", err, ErrLinkUnreachable.Code)
	}
	if elapsed := time.Since(start); elapsed < 3*shortWrite {
		t.Fatalf("the join answered after %v, before its dial failed", elapsed)
	}
	if len(a.state(t).Pairs) != 0 {
		t.Fatal("a failed join made a pair")
	}
}

func TestLinkDashboardJoinAnswersAfterTheWriteTimeout(t *testing.T) {
	a, text := slowJoinPair(t)
	client := newActionClient(t, a.d, a.root)
	address := shortWriteServer(t, a.d)

	form := url.Values{"csrf": {client.csrf}, "invitation": {text}}
	r, err := http.NewRequest("POST", "http://"+address+"/dashboard/actions/link-join", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://"+address)
	r.AddCookie(&http.Cookie{Name: cookieName(r), Value: client.cookie})
	http1 := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer http1.CloseIdleConnections()
	response, err := http1.Do(r)
	if err != nil {
		t.Fatalf("dashboard join: %v, want its 303 answer", err)
	}
	response.Body.Close()
	location, err := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != http.StatusSeeOther || err != nil || location.Query().Get("notice") != ErrLinkUnreachable.Code ||
		location.Path != "/dashboard/settings" {
		t.Fatalf("dashboard join: %d %q", response.StatusCode, response.Header.Get("Location"))
	}
}
