package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// Decision notices over the link, chunk 02 (#746): two in-process daemons, real TLS,
// synthetic Tailscale. All names are synthetic.

// waitNotices waits until the card's notices all have a final state, and returns them.
func waitNotices(t *testing.T, s *Store, reference string, n int) []noticeRow {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got := notices(t, s, reference)
		done := len(got) == n
		for _, x := range got {
			done = done && (x.State == "received" || x.State == "refused")
		}
		if done {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("notices of %s: %+v", reference, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func noticeMessages(t *testing.T, h *linkHost, s Session) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, m := range readInbox(t, h, s) {
		if m["system_kind"] == noticeKind {
			out = append(out, m)
		}
	}
	return out
}

// A card on alpha that names an agent on beta sends it one notice per event, with no card
// text; the agent's message is typed, comes from decision-board@alpha, names the card to read
// and cannot be answered.
func TestNoticeRoundTrip(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-notice-author", "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-notice-receiver", linkRepo(t, "recv"), "")
	ctx := context.Background()
	authorKey := Key{"claude", author.ID}
	if _, err := a.d.store.DecisionCreate(ctx, authorKey, "N-1", DecisionFields{Title: ptr("Secret title text"), Kind: ptr("decision"),
		Context: ptr("Secret context text."), Recipients: &[]string{receiver.Name + "@beta"}}, "n1", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	waitNotices(t, a.d.store, "N-1", 1)
	v, err := a.d.store.DecisionVerdict(verdictCtx(), "N-1", 1, "approved", "Secret note text.")
	if err != nil {
		t.Fatal(err)
	}
	waitNotices(t, a.d.store, "N-1", 2)
	got := noticeMessages(t, b, receiver)
	if len(got) != 2 {
		t.Fatalf("notices in the inbox: %v", got)
	}
	for _, m := range got {
		if m["sender_family"] != noticeFamily || m["sender_name"] != "decision-board@alpha" || m["origin"] != "link" {
			t.Fatalf("notice sender: %v", m)
		}
	}
	n, _ := got[1]["decision_notice"].(map[string]any)
	if n["reference"] != "N-1" || n["computer"] != "alpha" || n["get"] != "N-1@alpha" || n["event"] != "verdict" ||
		n["verdict"] != "approved" || int64(n["verdict_id"].(float64)) != v.ID || n["pair"] == "" || n["revision"].(float64) != 1 {
		t.Fatalf("typed notice: %v", n)
	}
	// No card text reaches the agent's computer.
	for _, text := range []string{"Secret title text", "Secret context text", "Secret note text"} {
		if c := count(t, b.d.store, `SELECT COUNT(*) FROM messages WHERE body LIKE ? OR decision_notice LIKE ?`, "%"+text+"%", "%"+text+"%"); c != 0 {
			t.Fatalf("card text %q on the agent's computer", text)
		}
	}
	// The origin shows the delivery's own notice as received.
	item, err := a.d.store.DecisionGet(ctx, authorKey, "N-1")
	if err != nil || item.Recipients[0].Notice != "received" || item.Verdict.Deliveries[0].Notice != "received" {
		t.Fatalf("origin states: %+v %v", item, err)
	}
	// A reply to a notice is refused, and nothing is queued.
	status, reply := linkSendAs(t, b, receiver, "", "reply to the board", int64(got[0]["id"].(float64)))
	if status == 200 || reply["code"] != "link_reply_notice" {
		t.Fatalf("reply to a notice: %d %v", status, reply)
	}
	if c := count(t, b.d.store, `SELECT COUNT(*) FROM link_outbox`); c != 0 {
		t.Fatalf("a refused reply queued %d messages", c)
	}
	// A rename of the origin computer keeps the sender and the typed name current.
	if err := b.d.store.linkShowComputer(ctx, n["pair"].(string), "alpha-renamed"); err != nil {
		t.Fatal(err)
	}
	renamed := noticeMessages(t, b, receiver)
	if r, _ := renamed[0]["decision_notice"].(map[string]any); renamed[0]["sender_name"] != "decision-board@alpha-renamed" || r["get"] != "N-1@alpha-renamed" {
		t.Fatalf("after a rename: %v", renamed[0])
	}
	// A removal gets its own notice.
	if _, err := a.d.store.DecisionEdit(ctx, authorKey, "N-1", 1, DecisionFields{Recipients: &[]string{}}); err != nil {
		t.Fatal(err)
	}
	ns := waitNotices(t, a.d.store, "N-1", 3)
	if ns[2].Event != "removed" || len(noticeMessages(t, b, receiver)) != 3 {
		t.Fatalf("removal: %+v", ns)
	}
}

// A participant address keeps the notices across a holder change; an unknown name is a
// stored refusal with its reason at the origin, never an inbox insert.
func TestNoticeRecipientsResolveAtTheReceiver(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-notice-author", "")
	repo := linkRepo(t, "held")
	holder := joinAs(t, b.d.store, "codex", "synthetic-notice-holder", repo, "")
	ctx := context.Background()
	authorKey := Key{"claude", author.ID}
	if _, err := a.d.store.DecisionCreate(ctx, authorKey, "H-1", DecisionFields{Title: ptr("Held"), Kind: ptr("decision"),
		Recipients: &[]string{holder.Address + "@beta", "synthetic-nobody@beta", "maintainer@beta"}}, "h1", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	ns := waitNotices(t, a.d.store, "H-1", 3)
	if ns[0].State != "received" || ns[1].State != "refused" || ns[1].Reason != "peer_not_found" || ns[2].State != "refused" {
		t.Fatalf("results: %+v", ns)
	}
	item, err := a.d.store.DecisionGet(ctx, authorKey, "H-1")
	if err != nil || item.Recipients[1].Notice != "refused" || item.Recipients[1].NoticeReason != "peer_not_found" {
		t.Fatalf("origin reasons: %+v %v", item.Recipients, err)
	}
	if c := count(t, b.d.store, `SELECT COUNT(*) FROM messages WHERE system_kind=? AND recipient_family NOT IN ('claude','codex','opencode','agy','grok','deepseek')`, noticeKind); c != 0 {
		t.Fatalf("%d notices in a non-agent inbox", c)
	}
	// The holder changes; the participant inbox keeps the notices, and the next one arrives.
	successor := joinAs(t, b.d.store, "codex", "synthetic-notice-successor", repo, "")
	holderCtx, _ := withAudit(ctx, "participant-holder", holder.Address)
	if err := b.d.store.ChooseHolder(holderCtx, holder.Address, Key{"codex", successor.ID}, successor.Revision); err != nil {
		t.Fatal(err)
	}
	successor.HoldsAddress, successor.Address = true, holder.Address
	if _, err := a.d.store.DecisionVerdict(verdictCtx(), "H-1", 1, "approved", ""); err != nil {
		t.Fatal(err)
	}
	waitNotices(t, a.d.store, "H-1", 6)
	if got := noticeMessages(t, b, successor); len(got) != 2 {
		t.Fatalf("the successor's notices: %v", got)
	}
}

// A computer at an earlier release answers that the operation is unknown: the notices wait as
// unsupported, direct messages still flow, and after the upgrade the notices arrive with no
// edit, each once.
func TestNoticeEarlierReleaseThenUpgrade(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	a.d.link.mu.Lock()
	a.d.link.backoff = 20 * time.Millisecond
	a.d.link.mu.Unlock()
	b.d.link.mu.Lock()
	b.d.link.hook = func(point string) error {
		if point == "notice-op" {
			return errLinkOldPeer
		}
		return nil
	}
	b.d.link.mu.Unlock()
	author := joinAs(t, a.d.store, "claude", "synthetic-notice-author", linkRepo(t, "author"), "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-notice-receiver", linkRepo(t, "recv"), "")
	ctx := context.Background()
	if _, err := a.d.store.DecisionCreate(ctx, Key{"claude", author.ID}, "O-1", DecisionFields{Title: ptr("Old"), Kind: ptr("decision"),
		Recipients: &[]string{receiver.Name + "@beta"}}, "o1", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for notices(t, a.d.store, "O-1")[0].State != "unsupported" {
		if time.Now().After(deadline) {
			t.Fatalf("not unsupported: %+v", notices(t, a.d.store, "O-1"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ns := notices(t, a.d.store, "O-1"); ns[0].Reason != "link_operation_unknown" {
		t.Fatalf("reason: %+v", ns)
	}
	// Direct messages still flow.
	id := mustQueue(t, a, author, receiver.Name+"@beta", "a direct message")
	if got := waitLinkState(t, a, author, id); got["delivery_state"] != "received" {
		t.Fatalf("direct message: %v", got)
	}
	// A second card waits too.
	if _, err := a.d.store.DecisionCreate(ctx, Key{"claude", author.ID}, "O-2", DecisionFields{Title: ptr("Old 2"), Kind: ptr("fyi"),
		Recipients: &[]string{receiver.Name + "@beta"}}, "o2", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	// The upgrade.
	b.d.link.mu.Lock()
	b.d.link.hook = nil
	b.d.link.mu.Unlock()
	waitNotices(t, a.d.store, "O-1", 1)
	waitNotices(t, a.d.store, "O-2", 1)
	if got := noticeMessages(t, b, receiver); len(got) != 2 {
		t.Fatalf("notices after the upgrade: %v", got)
	}
}

// A lost answer after the receiver committed: the sender sends the same sequence and digest
// again and gets the stored result; the agent has one message.
func TestNoticeLostAnswerAfterCommit(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	a.d.link.mu.Lock()
	a.d.link.backoff = 20 * time.Millisecond
	a.d.link.mu.Unlock()
	dropped := 0
	b.d.link.mu.Lock()
	b.d.link.hook = func(point string) error {
		if point == "after-notice-receive" && dropped == 0 {
			dropped++
			return errLinkDrop
		}
		return nil
	}
	b.d.link.mu.Unlock()
	author := join(t, a.d.store, "claude", "synthetic-notice-author", "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-notice-receiver", linkRepo(t, "recv"), "")
	if _, err := a.d.store.DecisionCreate(context.Background(), Key{"claude", author.ID}, "L-1", DecisionFields{Title: ptr("Lost"), Kind: ptr("decision"),
		Recipients: &[]string{receiver.Name + "@beta"}}, "l1", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	ns := waitNotices(t, a.d.store, "L-1", 1)
	if ns[0].State != "received" || dropped != 1 {
		t.Fatalf("after a lost answer: %+v dropped %d", ns, dropped)
	}
	if got := noticeMessages(t, b, receiver); len(got) != 1 {
		t.Fatalf("messages after a resend: %v", got)
	}
	var high int64
	if err := b.d.store.db.QueryRow(`SELECT high_water FROM link_streams WHERE stream='notice' AND direction='in'`).Scan(&high); err != nil || high != 1 {
		t.Fatalf("receiver high water: %d %v", high, err)
	}
}

// Many cards in quick succession, while the worker drains and goes idle: every notice is
// sent, and a verdict forwarded under the link lock queues and sends its notice without a
// deadlock.
func TestNoticeWorkerIdleRace(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-notice-author", "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-notice-receiver", linkRepo(t, "recv"), "")
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		if _, err := a.d.store.DecisionCreate(ctx, Key{"claude", author.ID}, fmt.Sprintf("R-%d", i), DecisionFields{Title: ptr("Race"),
			Kind: ptr("decision"), Recipients: &[]string{receiver.Name + "@beta"}}, fmt.Sprintf("r%d", i), a.d.store.clock()+600); err != nil {
			t.Fatal(err)
		}
		if i%5 == 0 {
			time.Sleep(15 * time.Millisecond)
		}
	}
	done := make(chan error, 1)
	go func() {
		a.d.link.mu.Lock()
		defer a.d.link.mu.Unlock()
		_, err := a.d.store.DecisionVerdict(context.WithValue(verdictCtx(), forwardedKey{}, "synthetic-pin"), "R-0", 1, "approved", "")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a forwarded verdict under the link lock did not finish")
	}
	waitNotices(t, a.d.store, "R-0", 2)
	for i := 1; i < 24; i++ {
		waitNotices(t, a.d.store, fmt.Sprintf("R-%d", i), 1)
	}
	if got := noticeMessages(t, b, receiver); len(got) != 25 {
		t.Fatalf("%d notices in the inbox, want 25", len(got))
	}
}

// A notice frame at every field bound, with the worst JSON escaping and the largest numbers,
// stays far under the link's frame bound; a malformed notice is refused.
func TestNoticeFrameBound(t *testing.T) {
	b := noticeBody{ID: strings.Repeat("f", 32), Seq: math.MaxInt64, XID: strings.Repeat("e", 32), Reference: strings.Repeat("Z", 32),
		Recipient: strings.Repeat("<", link.MaxLocalNameBytes), Address: strings.Repeat("&", link.MaxAddressBytes), Event: "verdict",
		Revision: math.MaxInt64, VerdictID: math.MaxInt64, Verdict: "acknowledged"}
	b.Digest = noticeDigest(b)
	f, err := link.NewFrame("notice", b)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(f)
	if len(data) > 8<<10 || len(data) > link.FrameLimit("notice") {
		t.Fatalf("frame of %d bytes", len(data))
	}
	if !b.valid() {
		t.Fatal("the bound notice is invalid")
	}
	for name, change := range map[string]func(*noticeBody){
		"recipient with @":      func(x *noticeBody) { x.Recipient = "a@b" },
		"recipient too long":    func(x *noticeBody) { x.Recipient = strings.Repeat("a", link.MaxLocalNameBytes+1) },
		"unknown event":         func(x *noticeBody) { x.Event = "deleted" },
		"verdict without an ID": func(x *noticeBody) { x.VerdictID = 0 },
		"ID without a verdict":  func(x *noticeBody) { x.Event, x.Verdict = "changed", "" },
		"zero revision":         func(x *noticeBody) { x.Revision = 0 },
		"bad reference":         func(x *noticeBody) { x.Reference = "a b" },
	} {
		x := b
		change(&x)
		if x.valid() {
			t.Fatalf("%s: accepted", name)
		}
	}
	_ = errors.New
}

// The agent's computer is offline: the notice waits, and arrives once when it is back.
func TestNoticeWaitsForADisconnectedComputer(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	a.d.link.mu.Lock()
	a.d.link.backoff = 20 * time.Millisecond
	a.d.link.mu.Unlock()
	author := join(t, a.d.store, "claude", "synthetic-notice-author", "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-notice-receiver", linkRepo(t, "recv"), "")
	ctx := context.Background()
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.d.store.DecisionCreate(ctx, Key{"claude", author.ID}, "W-1", DecisionFields{Title: ptr("Wait"), Kind: ptr("decision"),
		Recipients: &[]string{receiver.Name + "@beta"}}, "w1", a.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if ns := notices(t, a.d.store, "W-1"); ns[0].State == "received" || ns[0].State == "refused" {
		t.Fatalf("a notice finished while the computer was offline: %+v", ns)
	}
	if err := b.d.remoteEnable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	waitNotices(t, a.d.store, "W-1", 1)
	if got := noticeMessages(t, b, receiver); len(got) != 1 {
		t.Fatalf("notices after the reconnect: %v", got)
	}
}
