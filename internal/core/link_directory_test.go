package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// The receiver directory and NAME@COMPUTER addresses (#670 chunk 02): two or three
// in-process daemons with real TLS and the synthetic Tailscale adapter.

func pairHosts(t *testing.T, inviter, joiner *linkHost) {
	t.Helper()
	text, err := inviter.d.linkInvite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := joiner.d.linkJoin(context.Background(), text); err != nil {
		t.Fatal(err)
	}
}

// namedRepo makes a Git repository whose directory has this base name.
func linkRepo(t *testing.T, name string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func agentKey(s Session) Key { return Key{s.Family, s.ID} }

func callerPost(t *testing.T, h *linkHost, path string, body map[string]any) (int, map[string]any) {
	t.Helper()
	return post(t, h.d.Addresses()[0], linkBearer(t, h), path, body)
}

func remoteRowsOf(rows []any) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok && m["remote"] == true {
			out = append(out, m)
		}
	}
	return out
}

func TestLinkDirectoryPeersStatusAndSend(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	pairHosts(t, b, a)
	repoA, repoB := linkRepo(t, "proj"), linkRepo(t, "proj")
	local := joinAs(t, a.d.store, "codex", "synthetic-a-review", repoA, "review")
	reviewer := joinAs(t, b.d.store, "codex", "synthetic-b-review", repoB, "review")
	plain := joinAs(t, b.d.store, "codex", "synthetic-b-plain", repoB, "")
	if local.Address != reviewer.Address || !reviewer.HoldsAddress {
		t.Fatalf("fixture: %+v %+v", local, reviewer)
	}
	a.d.passNow()

	status, reply := callerPost(t, a, "/v1/peers", map[string]any{"caller": agentKey(local)})
	rows := remoteRowsOf(reply["peers"].([]any))
	if status != 200 || len(rows) != 2 {
		t.Fatalf("peers: %d %v", status, reply)
	}
	byAddress := map[string]map[string]any{}
	for _, r := range rows {
		byAddress[r["address"].(string)] = r
	}
	rr := byAddress[reviewer.Address+"@beta"]
	if rr == nil || rr["name"] != reviewer.Name+"@beta" || rr["computer"] != "beta" || rr["repository"] != "proj" || rr["role"] != "review" ||
		rr["family"] != "codex" || rr["seen_at"] == nil || rr["stale"] != nil {
		t.Fatalf("reviewer row: %v", rows)
	}
	plainAddress := plain.Name
	if plain.HoldsAddress {
		plainAddress = plain.Address
	}
	if byAddress[plainAddress+"@beta"] == nil {
		t.Fatalf("plain row: %v", rows)
	}
	data, _ := json.Marshal(reply)
	for _, private := range []string{repoB, "synthetic-b-review", "synthetic-b-plain"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("peers carry %q", private)
		}
	}

	// peer_status: by address and by name, from the cache; the bare address is local.
	for _, name := range []string{reviewer.Address + "@beta", reviewer.Name + "@beta"} {
		status, reply = callerPost(t, a, "/v1/peers/status", map[string]any{"caller": agentKey(local), "peer": name})
		peer, _ := reply["peer"].(map[string]any)
		if status != 200 || peer["remote"] != true || peer["computer"] != "beta" {
			t.Fatalf("status %s: %d %v", name, status, reply)
		}
	}
	status, reply = callerPost(t, a, "/v1/peers/status", map[string]any{"caller": agentKey(local), "peer": reviewer.Address})
	if peer, _ := reply["peer"].(map[string]any); status != 200 || peer["remote"] != nil || peer["name"] != local.Name {
		t.Fatalf("bare address: %d %v", status, reply)
	}
	for name, code := range map[string]string{"x@gamma": "link_computer_unknown", "nobody@beta": "peer_not_found", "a@b@beta": "link_address_invalid"} {
		if status, reply = callerPost(t, a, "/v1/peers/status", map[string]any{"caller": agentKey(local), "peer": name}); status == 200 || reply["code"] != code {
			t.Fatalf("status %s: %d %v", name, status, reply)
		}
	}

	// send: the chunk 02 boundary; nothing is stored or sent.
	for to, code := range map[string]string{"x@gamma": "link_computer_unknown", reviewer.Address + "@beta": "link_messages_pending",
		"nobody@beta": "link_messages_pending", "a@b@beta": "link_address_invalid"} {
		if status, reply = callerPost(t, a, "/v1/messages/send", map[string]any{"caller": agentKey(local), "to": to, "body": "hello"}); status == 200 || reply["code"] != code {
			t.Fatalf("send %s: %d %v", to, status, reply)
		}
	}
	if status, reply = callerPost(t, a, "/v1/messages/send", map[string]any{"caller": Key{"codex", "synthetic-none"}, "to": "x@beta", "body": "hello"}); status == 200 {
		t.Fatalf("send from an unknown caller: %v", reply)
	}
	token := newMaintainerToken(t, a.d, linkBearer(t, a), "link-test")
	refusedMaintainer(t, a.d, linkBearer(t, a), token, "send", map[string]any{"to": reviewer.Address + "@beta", "body": "hello"}, "link_maintainer_remote")
	result := mustMaintainer(t, a.d, linkBearer(t, a), token, "peers", nil)
	if len(remoteRowsOf(result["peers"].([]any))) != 2 {
		t.Fatalf("maintainer peers: %v", result)
	}

	// The dashboard shows a group for the computer.
	c := newActionClient(t, a.d, a.root)
	page := dashboardDo(t, a.d, "GET", "/dashboard/sessions", c.cookie, nil, nil)
	if !strings.Contains(page.body, "On beta") || !strings.Contains(page.body, reviewer.Address+"@beta") {
		t.Fatalf("sessions page lacks the remote group")
	}
	if code := c.do("send", url.Values{"to": {reviewer.Address + "@beta"}, "body": {"hello"}}); code != "link_maintainer_remote" {
		t.Fatal(code)
	}

	// Stale after linkConnectedFor without a directory, and while the link is closed.
	n.shift.Store(int64(linkConnectedFor + time.Second))
	if rows := a.d.remoteRows(); len(rows) != 2 || !rows[0].Stale {
		t.Fatalf("not stale: %+v", rows)
	}
	n.shift.Store(0)
	if err := a.d.remoteDisable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows := a.d.remoteRows(); len(rows) != 2 || !rows[0].Stale {
		t.Fatalf("closed link: %+v", rows)
	}
	if err := a.d.remoteEnable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}

	// Unpair drops the directory; a new pair starts without one.
	if err := a.d.linkUnpair(context.Background(), "beta"); err != nil {
		t.Fatal(err)
	}
	if rows := a.d.remoteRows(); len(rows) != 0 {
		t.Fatalf("after unpair: %+v", rows)
	}
	if len(b.state(t).Pairs) != 0 {
		t.Fatal("beta kept the pair")
	}
	pairHosts(t, b, a)
	if rows := a.d.remoteRows(); len(rows) != 0 {
		t.Fatalf("re-pair revived the directory: %+v", rows)
	}
	a.d.passNow()
	if rows := a.d.remoteRows(); len(rows) != 2 {
		t.Fatalf("after re-pair and a pass: %+v", rows)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pairByAdvertised(t *testing.T, h *linkHost, advertised string) link.Pair {
	t.Helper()
	for _, p := range h.state(t).Pairs {
		if p.Advertised == advertised {
			return p
		}
	}
	return link.Pair{}
}

func TestLinkNamesFollowTheOtherComputer(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	g := newLinkHost(t, n, "gamma", "100.64.0.3")
	pairHosts(t, b, a)
	pairHosts(t, g, a)
	ctx := context.Background()
	// Asymmetric: beta calls alpha "alpha", alpha calls beta "laptop" (an override).
	idB := pairByAdvertised(t, a, "beta").ID
	if err := a.d.linkRename(idB, "laptop"); err != nil {
		t.Fatal(err)
	}
	// gamma renames itself to beta: name-update; alpha gives it the free name beta.
	if err := g.d.linkSetup(ctx, "beta", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "name-update from gamma", func() bool {
		for _, p := range a.state(t).Pairs {
			if p.Advertised == "beta" && p.ID != idB {
				return true
			}
		}
		return false
	})
	if p := a.state(t).Pairs; findPair(link.State{Pairs: p}, "beta") < 0 {
		t.Fatalf("gamma did not take the free name beta: %+v", p)
	}
	// beta renames itself to beta-x: its override stays; the advertised name changes.
	if err := b.d.linkSetup(ctx, "beta-x", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "name-update from beta", func() bool { return pairByAdvertised(t, a, "beta-x").ID == idB })
	if p := pairByAdvertised(t, a, "beta-x"); p.Effective != "laptop" || p.Override != "laptop" {
		t.Fatalf("override lost: %+v", p)
	}
	// Collision with a rename: gamma renames itself to laptop; alpha's override keeps laptop.
	if err := g.d.linkSetup(ctx, "laptop", 0); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "name-update laptop", func() bool {
		for _, p := range a.state(t).Pairs {
			if p.Advertised == "laptop" {
				return true
			}
		}
		return false
	})
	if p := pairByAdvertised(t, a, "laptop"); p.Effective != "laptop-2" {
		t.Fatalf("collision with a rename: %+v", p)
	}
	// A missed name-update arrives with the next directory: change the name with no link to
	// alpha, then let alpha's pass read it.
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.d.linkSetupName(ctx, "beta-y", 0); err != ErrLinkRemoteOff {
		t.Fatalf("setup while off: %v", err)
	}
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	b.d.link.mu.Lock()
	s, err := link.ReadState(b.root)
	if err == nil {
		s.Name = "beta-y"
		err = link.WriteState(b.root, s)
	}
	b.d.link.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.d.passNow()
	if p := pairByAdvertised(t, a, "beta-y"); p.ID != idB || p.Effective != "laptop" {
		t.Fatalf("directory name: %+v", p)
	}
	// The other computer's name for alpha never changed.
	if p := b.state(t).Pairs[0]; p.Effective != "alpha" {
		t.Fatalf("beta's name for alpha: %+v", p)
	}
}

func TestLinkDirectoryBoundsAndAccess(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	pairHosts(t, b, a)
	ctx := context.Background()
	repo := linkRepo(t, "bounds")
	for i := 0; i < linkDirectoryMaxEntries+20; i++ {
		if _, err := b.d.store.Register(ctx, Registration{Family: "codex", ID: fmt.Sprintf("synthetic-%03d", i), Repository: repo, Directory: repo, TTLSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := b.d.linkDirectoryOf(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(dir)
	if len(dir.Entries) != linkDirectoryMaxEntries || !dir.Truncated || len(data) > link.MaxFrameBytes {
		t.Fatalf("bounds: %d entries, truncated %t, %d bytes", len(dir.Entries), dir.Truncated, len(data))
	}
	for i := 1; i < len(dir.Entries); i++ {
		if dir.Entries[i-1].Name > dir.Entries[i].Name {
			t.Fatal("entries out of order")
		}
	}
	a.d.passNow()
	rows := a.d.remoteRows()
	if len(rows) != linkDirectoryMaxEntries || !rows[0].DirectoryTruncated {
		t.Fatalf("remote rows: %d", len(rows))
	}
	// Only a paired key gets the directory: an unknown key, or a pending pair.
	if _, err := b.d.answerDirectory(strings.Repeat("ab", 32), b.state(t).Pairs[0].ID); !errors.Is(err, ErrLinkNoPair) {
		t.Fatalf("unknown key: %v", err)
	}
	g := newLinkHost(t, n, "gamma", "100.64.0.3")
	text, err := g.d.linkInvite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := link.ParseInvitation(text)
	idA, _ := a.d.linkIdentity()
	ok, err := g.d.acceptInvitation(idA.Pin, linkHello{Secret: inv.Secret, Name: "alpha", DNS: "alpha.example-tailnet.ts.net", IPs: []string{"100.64.0.1"}, Port: 47672}, g.d.openGen())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.d.answerDirectory(idA.Pin, ok.ID); !errors.Is(err, ErrLinkNoPair) {
		t.Fatalf("pending pair: %v", err)
	}
}

func TestLinkDirectoryCommitsOnlyInItsGeneration(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	pairHosts(t, b, a)
	joinAs(t, b.d.store, "codex", "synthetic-b", linkRepo(t, "gen"), "")
	ctx := context.Background()
	// Only a dial with the test's marker connects: the monitor of the new generation waits
	// until the test ends, so the only directory read is the test's own.
	type marker struct{}
	held := make(chan struct{})
	t.Cleanup(func() { close(held) })
	a.d.link.mu.Lock()
	a.d.link.dial = func(dialCtx context.Context, address string) (net.Conn, error) {
		if dialCtx.Value(marker{}) == nil {
			select {
			case <-held:
			case <-dialCtx.Done():
			}
			return nil, errors.New("held for the test")
		}
		return n.dial(dialCtx, address)
	}
	a.d.link.mu.Unlock()
	old := a.d.openGen()
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	p := a.state(t).Pairs[0]
	marked := context.WithValue(ctx, marker{}, true)
	session, err := a.d.connectPair(marked, a.d.openGen(), p)
	if err != nil {
		t.Fatal(err)
	}
	a.d.pollDirectory(session, old, p)
	a.d.link.mu.Lock()
	_, cached := a.d.link.directories[p.ID]
	a.d.link.mu.Unlock()
	if cached {
		t.Fatal("a poll of a closed generation committed")
	}
	// The same read in the open generation commits.
	a.d.pollDirectory(session, a.d.openGen(), p)
	session.close()
	a.d.link.mu.Lock()
	c, cached := a.d.link.directories[p.ID]
	a.d.link.mu.Unlock()
	if !cached || c.gen != a.d.openGen() {
		t.Fatalf("a poll of the open generation did not commit: %+v", c)
	}
}

// The directory follows the holder of a participant address on the other computer: an
// unheld address is not advertised or resolved, a new holder takes it, and the earlier
// peer name stays distinct.
func TestLinkDirectoryFollowsTheAddressHolder(t *testing.T) {
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	pairHosts(t, b, a)
	ctx := context.Background()
	repo := linkRepo(t, "holder")
	first := joinAs(t, b.d.store, "codex", "synthetic-first", repo, "review")
	if !first.HoldsAddress {
		t.Fatalf("fixture: %+v", first)
	}
	address := first.Address + "@beta"
	a.d.passNow()
	status, err := a.d.remotePeerStatus(address)
	if err != nil || status.Name != first.Name+"@beta" {
		t.Fatalf("first holder: %+v %v", status, err)
	}
	if _, err := b.d.store.Mutate(ctx, Mutation{Family: first.Family, ID: first.ID, IfRevision: first.Revision}, true); err != nil {
		t.Fatal(err)
	}
	a.d.passNow()
	if _, err := a.d.remotePeerStatus(address); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("unheld address resolved: %v", err)
	}
	for _, r := range a.d.remoteRows() {
		if r.Address == address || r.Name == first.Name+"@beta" {
			t.Fatalf("unheld address or retired session advertised: %+v", r)
		}
	}
	second := joinAs(t, b.d.store, "codex", "synthetic-second", repo, "review")
	if !second.HoldsAddress || second.Address != first.Address || second.Name == first.Name {
		t.Fatalf("fixture: %+v", second)
	}
	a.d.passNow()
	status, err = a.d.remotePeerStatus(address)
	if err != nil || status.Name != second.Name+"@beta" || status.Address != address {
		t.Fatalf("new holder: %+v %v", status, err)
	}
	if _, err := a.d.remotePeerStatus(first.Name + "@beta"); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("the earlier peer name resolves: %v", err)
	}
}

// Full addresses up to 510 bytes round trip without truncation: a participant address
// longer than 64 bytes and a 256-byte NAME, each with a 253-byte COMPUTER.
func TestLinkLongAddresses(t *testing.T) {
	long := strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 61)
	n := &linkNet{}
	a := newLinkHost(t, n, "alpha", "100.64.0.1")
	b := newLinkHost(t, n, "beta", "100.64.0.2")
	if err := b.d.linkSetup(context.Background(), long, 0); err != nil {
		t.Fatal(err)
	}
	pairHosts(t, b, a)
	repo := linkRepo(t, strings.Repeat("r", 60))
	reviewer := joinAs(t, b.d.store, "opencode", "ses_synthetic-long", repo, strings.Repeat("v", 24))
	if len(reviewer.Address) <= 64 || !reviewer.HoldsAddress {
		t.Fatalf("fixture address %q", reviewer.Address)
	}
	local := joinAs(t, a.d.store, "codex", "synthetic-local", linkRepo(t, "short"), "")
	a.d.passNow()
	full := reviewer.Address + "@" + long
	status, reply := callerPost(t, a, "/v1/peers/status", map[string]any{"caller": agentKey(local), "peer": full})
	if peer, _ := reply["peer"].(map[string]any); status != 200 || peer["address"] != full {
		t.Fatalf("long status: %d %v", status, reply)
	}
	widest := strings.Repeat("n", 256) + "@" + long
	if len(widest) != 510 {
		t.Fatal("fixture")
	}
	for to, code := range map[string]string{full: "link_messages_pending", widest: "link_messages_pending"} {
		if status, reply := callerPost(t, a, "/v1/messages/send", map[string]any{"caller": agentKey(local), "to": to, "body": "hello"}); status == 200 || reply["code"] != code {
			t.Fatalf("send %d bytes: %d %v", len(to), status, reply)
		}
	}
	if status, reply := callerPost(t, a, "/v1/peers/status", map[string]any{"caller": agentKey(local), "peer": widest}); status == 200 || reply["code"] != "peer_not_found" {
		t.Fatalf("510-byte status: %d %v", status, reply)
	}
	if status, reply := callerPost(t, a, "/v1/messages/send", map[string]any{"caller": agentKey(local), "to": widest + "x", "body": "hello"}); status == 200 || reply["code"] != "link_address_invalid" {
		t.Fatalf("511 bytes: %d %v", status, reply)
	}
}
