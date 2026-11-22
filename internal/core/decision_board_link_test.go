package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

// Remote read and acknowledgement, chunk 03 (#746): two in-process daemons, real TLS,
// synthetic Tailscale. The card lives on alpha; the agent works on beta. All names are
// synthetic.

// boardPost calls a decision route on h as s, and returns the status, the result or the
// refusal's code.
func boardPost(t *testing.T, h *linkHost, s Session, op string, body map[string]any) (int, map[string]any, string) {
	t.Helper()
	body["caller"] = agentKey(s)
	status, reply := callerPost(t, h, "/v1/decisions/"+op, body)
	result, _ := reply["result"].(map[string]any)
	code, _ := reply["code"].(string)
	return status, result, code
}

// boardCard creates a card on h by author that names recipients, and returns it.
func boardCard(t *testing.T, h *linkHost, author Session, reference string, fields DecisionFields) {
	t.Helper()
	if _, err := h.d.store.DecisionCreate(context.Background(), agentKey(author), reference, fields, reference+"-key", h.d.store.clock()+600); err != nil {
		t.Fatal(err)
	}
}

func pendingIDs(result map[string]any) []int64 {
	var out []int64
	list, _ := result["deliveries"].([]any)
	for _, d := range list {
		out = append(out, int64(d.(map[string]any)["verdict_id"].(float64)))
	}
	return out
}

func ids(result map[string]any, key string) []int64 {
	var out []int64
	list, _ := result[key].([]any)
	for _, v := range list {
		out = append(out, int64(v.(float64)))
	}
	return out
}

// The agent reads the live card, lists the cards that name it and its waiting verdicts, and
// acknowledges them at alpha; nothing is stored on beta, and its local answers stay as they
// were.
func TestBoardRemoteReadAndAck(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "R-1", DecisionFields{Title: ptr("Remote title text"), Kind: ptr("decision"),
		Context: ptr("Remote context text."), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "R-1", 1, "approved")

	status, item, code := boardPost(t, b, agent, "get", map[string]any{"reference": "R-1@alpha"})
	origin, _ := item["origin"].(map[string]any)
	if status != 200 || item["title"] != "Remote title text" || origin["computer"] != "alpha" || len(origin["pair"].(string)) != 32 {
		t.Fatalf("remote get: %d %s %v", status, code, item)
	}
	pair := origin["pair"].(string)
	// The pair ID names the same computer, and wins over a computer name.
	if status, item, code = boardPost(t, b, agent, "get", map[string]any{"reference": "R-1", "pair": pair, "computer": "no-such"}); status != 200 || item["title"] != "Remote title text" {
		t.Fatalf("get by pair: %d %s %v", status, code, item)
	}
	if status, page, code := boardPost(t, b, agent, "list", map[string]any{"filter": "mine", "computer": "alpha"}); status != 200 ||
		len(page["items"].([]any)) != 1 || page["items"].([]any)[0].(map[string]any)["reference"] != "R-1" {
		t.Fatalf("remote list: %d %s %v", status, code, page)
	}
	if status, _, code := boardPost(t, b, agent, "list", map[string]any{"filter": "waiting", "computer": "alpha"}); status == 200 || code != "invalid_request" {
		t.Fatalf("remote list with another filter: %d %s", status, code)
	}
	status, pending, code := boardPost(t, b, agent, "pending", map[string]any{"computer": "alpha"})
	if got := pendingIDs(pending); status != 200 || len(got) != 1 || got[0] != v.ID ||
		!strings.Contains(pending["deliveries"].([]any)[0].(map[string]any)["entry"].(string), "Remote context text.") || pending["record"] == nil {
		t.Fatalf("remote pending: %d %s %v", status, code, pending)
	}
	status, ack, code := boardPost(t, b, agent, "ack", map[string]any{"verdict_ids": []int64{v.ID}, "pair": pair})
	if got := ids(ack, "acknowledged"); status != 200 || len(got) != 1 || got[0] != v.ID || len(ids(ack, "already")) != 0 {
		t.Fatalf("remote ack: %d %s %v", status, code, ack)
	}
	// A repeated acknowledgement reports already, and changes nothing.
	if status, ack, _ = boardPost(t, b, agent, "ack", map[string]any{"verdict_ids": []int64{v.ID}, "computer": "alpha"}); status != 200 ||
		len(ids(ack, "acknowledged")) != 0 || len(ids(ack, "already")) != 1 {
		t.Fatalf("repeated ack: %v", ack)
	}
	if c := count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND outcome='acknowledged' AND verdict_id=?`, v.ID); c != 1 {
		t.Fatalf("origin deliveries acknowledged: %d", c)
	}
	if _, pending, _ = boardPost(t, b, agent, "pending", map[string]any{"computer": "alpha"}); len(pendingIDs(pending)) != 0 {
		t.Fatalf("pending after the ack: %v", pending)
	}
	// Nothing is stored on beta, and its local board does not know the card.
	if c := count(t, b.d.store, `SELECT COUNT(*) FROM decision_items`); c != 0 {
		t.Fatalf("%d cards stored on the agent's computer", c)
	}
	for _, text := range []string{"Remote title text", "Remote context text"} {
		if c := count(t, b.d.store, `SELECT COUNT(*) FROM messages WHERE body LIKE ? OR decision_notice LIKE ?`, "%"+text+"%", "%"+text+"%"); c != 0 {
			t.Fatalf("card text %q on the agent's computer", text)
		}
	}
	if status, _, code = boardPost(t, b, agent, "get", map[string]any{"reference": "R-1"}); status == 200 || code != "not_found" {
		t.Fatalf("local get of a remote card: %d %s", status, code)
	}
	// A malformed remote reference is refused before any call.
	for _, ref := range []string{"R-1@", "@alpha"} {
		if status, _, code = boardPost(t, b, agent, "get", map[string]any{"reference": ref}); status != 400 || code != "invalid_request" {
			t.Fatalf("get %q: %d %s", ref, status, code)
		}
	}
	if status, _, code = boardPost(t, b, agent, "get", map[string]any{"reference": "R-1@alpha", "computer": "gamma"}); status != 400 {
		t.Fatalf("two computers: %d %s", status, code)
	}
	if status, _, code = boardPost(t, b, agent, "get", map[string]any{"reference": "R-1@gamma"}); code != "link_computer_unknown" {
		t.Fatalf("unknown computer: %d %s", status, code)
	}
}

// Only the caller's own names count: another agent on beta, a card that names nobody on beta,
// a same reference and verdict ID on beta's own board, and a recipient that an edit removed,
// which keeps its earlier verdict from its snapshot, sees no live card and cannot acknowledge
// the other recipient's delivery.
func TestBoardRemoteNamesAndRemovedRecipients(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	one := joinAs(t, b.d.store, "codex", "synthetic-board-one", linkRepo(t, "one"), "")
	two := joinAs(t, b.d.store, "opencode", "synthetic-board-two", linkRepo(t, "two"), "")
	other := joinAs(t, b.d.store, "claude", "synthetic-board-other", linkRepo(t, "other"), "")
	boardCard(t, a, author, "S-1", DecisionFields{Title: ptr("Old title"), Kind: ptr("decision"), Context: ptr("Old context."),
		Recipients: &[]string{one.Name + "@beta", two.Name + "@beta"}})
	boardCard(t, a, author, "S-2", DecisionFields{Title: ptr("Nobody on beta"), Kind: ptr("decision")})
	v, err := a.d.store.DecisionVerdict(verdictCtx(), "S-1", 1, "feedback", "Synthetic guidance.")
	if err != nil {
		t.Fatal(err)
	}
	// Beta's own board has the same reference and the same verdict number.
	boardCard(t, b, other, "S-1", DecisionFields{Title: ptr("Local card"), Kind: ptr("decision"), Recipients: &[]string{one.Name}})
	local := mustVerdict(t, b.d.store, "S-1", 1, "approved")
	if local.ID != v.ID {
		t.Fatalf("fixture: local verdict %d, remote %d", local.ID, v.ID)
	}

	if status, _, code := boardPost(t, b, other, "get", map[string]any{"reference": "S-1@alpha"}); status == 200 || code != "not_found" {
		t.Fatalf("an agent that the card does not name: %d %s", status, code)
	}
	if status, _, code := boardPost(t, b, one, "get", map[string]any{"reference": "S-2@alpha"}); status == 200 || code != "not_found" {
		t.Fatalf("a card that names nobody here: %d %s", status, code)
	}
	if _, ack, _ := boardPost(t, b, other, "ack", map[string]any{"verdict_ids": []int64{v.ID}, "computer": "alpha"}); len(ids(ack, "acknowledged"))+len(ids(ack, "already")) != 0 {
		t.Fatalf("an agent acknowledged another's delivery: %v", ack)
	}

	// An edit removes two, then changes every field.
	current, err := a.d.store.DecisionGet(context.Background(), agentKey(author), "S-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.d.store.DecisionEdit(context.Background(), agentKey(author), "S-1", current.Revision, DecisionFields{Recipients: &[]string{one.Name + "@beta"},
		Title: ptr("New title"), Where: ptr("New where"), AgentText: ptr("New agent text"), Context: ptr("New context."),
		Recommendation: ptr("New recommendation."), Commands: &[]string{"echo new"}, Order: ptr(int64(7))}); err != nil {
		t.Fatal(err)
	}
	if status, _, code := boardPost(t, b, two, "get", map[string]any{"reference": "S-1@alpha"}); status == 200 || code != "not_found" {
		t.Fatalf("a removed recipient read the live card: %d %s", status, code)
	}
	status, pending, _ := boardPost(t, b, two, "pending", map[string]any{"computer": "alpha"})
	data, _ := json.Marshal(pending)
	if got := pendingIDs(pending); status != 200 || len(got) != 1 || got[0] != v.ID || !strings.Contains(string(data), "Old context.") {
		t.Fatalf("the removed recipient's verdict: %s", data)
	}
	for _, text := range []string{"New title", "New where", "New agent text", "New context", "New recommendation", "echo new"} {
		if strings.Contains(string(data), text) {
			t.Fatalf("later card text %q reached a removed recipient: %s", text, data)
		}
	}
	// One's live card is the new revision; local beta answers are its own.
	if _, item, _ := boardPost(t, b, one, "get", map[string]any{"reference": "S-1@alpha"}); item["title"] != "New title" {
		t.Fatalf("live card: %v", item)
	}
	if _, item, _ := boardPost(t, b, one, "get", map[string]any{"reference": "S-1"}); item["title"] != "Local card" {
		t.Fatalf("local card: %v", item)
	}
	// Two acknowledges its own delivery only; one's stays queued at alpha, and beta's local
	// delivery of the same number stays queued too.
	if _, ack, _ := boardPost(t, b, two, "ack", map[string]any{"verdict_ids": []int64{v.ID}, "computer": "alpha"}); len(ids(ack, "acknowledged")) != 1 {
		t.Fatalf("removed recipient's own ack: %v", ack)
	}
	if c := count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE verdict_id=? AND outcome='queued' AND remote_name=?`, v.ID, one.Name); c != 1 {
		t.Fatalf("one's delivery at alpha: %d queued", c)
	}
	if _, pending, _ := boardPost(t, b, one, "pending", map[string]any{}); len(pendingIDs(pending)) != 1 {
		t.Fatalf("beta's local delivery: %v", pending)
	}
}

// A participant address keeps its cards across a holder change; a rename of alpha on beta
// changes the name that reaches it, never the pair; a pair that is gone is refused, also when
// a new computer takes the old name.
func TestBoardRemoteHolderRenameAndRepair(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	repo := linkRepo(t, "held")
	holder := joinAs(t, b.d.store, "codex", "synthetic-board-holder", repo, "")
	boardCard(t, a, author, "P-1", DecisionFields{Title: ptr("Held card"), Kind: ptr("decision"), Recipients: &[]string{holder.Address + "@beta"}})
	successor := joinAs(t, b.d.store, "codex", "synthetic-board-successor", repo, "")
	holderCtx, _ := withAudit(context.Background(), "participant-holder", holder.Address)
	if err := b.d.store.ChooseHolder(holderCtx, holder.Address, agentKey(successor), successor.Revision); err != nil {
		t.Fatal(err)
	}
	status, item, code := boardPost(t, b, successor, "get", map[string]any{"reference": "P-1@alpha"})
	if status != 200 || item["title"] != "Held card" {
		t.Fatalf("the successor's read: %d %s %v", status, code, item)
	}
	pair := item["origin"].(map[string]any)["pair"].(string)
	if err := b.d.linkRename(pair, "alpha-renamed"); err != nil {
		t.Fatal(err)
	}
	if status, item, _ = boardPost(t, b, successor, "get", map[string]any{"reference": "P-1@alpha-renamed"}); status != 200 || item["origin"].(map[string]any)["pair"] != pair {
		t.Fatalf("after the rename: %d %v", status, item)
	}
	if _, _, code = boardPost(t, b, successor, "get", map[string]any{"reference": "P-1@alpha"}); code != "link_computer_unknown" {
		t.Fatalf("the old name after the rename: %s", code)
	}
	// Unpair, then a new computer pairs under the old name: the old pair is refused, and the
	// new computer's card rows are its own.
	if err := b.d.linkUnpair(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	if _, _, code = boardPost(t, b, successor, "get", map[string]any{"reference": "P-1", "pair": pair}); code != "link_computer_unknown" {
		t.Fatalf("a removed pair: %s", code)
	}
	c := newLinkHost(t, n, "alpha", "100.64.0.3")
	pairHosts(t, c, b)
	if _, _, code = boardPost(t, b, successor, "get", map[string]any{"reference": "P-1@alpha"}); code != "not_found" {
		t.Fatalf("a new computer under the old name: %s", code)
	}
	// The new computer has the same reference and verdict number: an acknowledgement with the
	// old pair ID is refused and acknowledges nothing there; the new pair's works.
	cAuthor := join(t, c.d.store, "claude", "synthetic-board-author-c", "")
	boardCard(t, c, cAuthor, "P-1", DecisionFields{Title: ptr("New computer card"), Kind: ptr("decision"), Recipients: &[]string{holder.Address + "@beta"}})
	cv := mustVerdict(t, c.d.store, "P-1", 1, "approved")
	if _, _, code = boardPost(t, b, successor, "ack", map[string]any{"verdict_ids": []int64{cv.ID}, "pair": pair}); code != "link_computer_unknown" {
		t.Fatalf("ack with the old pair: %s", code)
	}
	if got := count(t, c.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND verdict_id=? AND outcome='queued'`, cv.ID); got != 1 {
		t.Fatalf("the old pair's ack reached the new computer: %d queued", got)
	}
	if _, ack, code := boardPost(t, b, successor, "ack", map[string]any{"verdict_ids": []int64{cv.ID}, "computer": "alpha"}); len(ids(ack, "acknowledged")) != 1 {
		t.Fatalf("ack at the new computer: %s %v", code, ack)
	}
}

// The card's dashboard shows each remote recipient with its computer and its notice state.
func TestBoardRemoteRecipientsOnTheDashboard(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "D-1", DecisionFields{Title: ptr("Dashboard card"), Kind: ptr("decision"),
		Recipients: &[]string{agent.Name + "@beta", "synthetic-nobody@beta"}})
	waitNotices(t, a.d.store, "D-1", 2)
	client := newActionClient(t, a.d, a.root)
	page := dashboardDo(t, a.d, "GET", "/dashboard/decisions", client.cookie, nil, nil)
	if page.status != 200 {
		t.Fatalf("board: %d", page.status)
	}
	card := cardOf(t, page.body, "D-1")
	for _, want := range []string{agent.Name + "@beta <small>remote</small> <small>notice received</small>",
		"synthetic-nobody@beta <small>remote</small> <small>notice refused: peer_not_found</small>"} {
		if !strings.Contains(card, want) {
			t.Errorf("no %q in %s", want, card)
		}
	}
}

// An earlier release answers unsupported; an offline computer answers offline before any
// send; a lost answer after the commit, a hung computer and a cancel answer uncertain, and a
// retry after the lost answer reports already.
func TestBoardRemoteFailures(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "F-1", DecisionFields{Title: ptr("Faults"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "F-1", 1, "approved")
	hook := func(f func(string) error) {
		a.d.link.mu.Lock()
		a.d.link.hook = f
		a.d.link.mu.Unlock()
	}
	ctx := context.Background()

	hook(func(point string) error {
		if point == "board-op" {
			return errLinkOldPeer
		}
		return nil
	})
	if _, err := b.d.RemoteDecisionGet(ctx, agentKey(agent), "alpha", "", "F-1"); errorCode(err) != "link_unsupported" {
		t.Fatalf("earlier release: %v", err)
	}

	// A lost answer after the acknowledgement's commit, then a retry. It runs while no other
	// board request is in flight: a request that a later step leaves behind runs to its end
	// on alpha and could take the one dropped answer (#765).
	dropped := 0
	hook(func(point string) error {
		if point == "after-board" && dropped == 0 {
			dropped++
			return errLinkDrop
		}
		return nil
	})
	if _, err := b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID}); errorCode(err) != "link_uncertain" {
		t.Fatalf("lost answer: %v", err)
	}
	if out, err := b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID}); err != nil || len(out.Already) != 1 || out.Already[0] != v.ID {
		t.Fatalf("retry after a lost answer: %+v %v", out, err)
	}

	// A hung computer, within a shorter budget, on a second card.
	boardCard(t, a, author, "F-2", DecisionFields{Title: ptr("Hung faults"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	hung := mustVerdict(t, a.d.store, "F-2", 1, "approved")
	saved := boardCallBudget
	boardCallBudget = 300 * time.Millisecond
	t.Cleanup(func() { boardCallBudget = saved })
	release := make(chan struct{})
	var opened sync.Once
	open := func() { opened.Do(func() { close(release) }) }
	t.Cleanup(open) // an early failure never leaves a request held in the hook
	hook(func(point string) error {
		if point == "before-board" {
			<-release
		}
		return nil
	})
	start := time.Now()
	if _, err := b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{hung.ID}); errorCode(err) != "link_uncertain" || time.Since(start) > 5*time.Second {
		t.Fatalf("hung computer: %v after %s", err, time.Since(start))
	}
	// A cancel of the local request ends the exchange at once.
	cancelled, cancel := context.WithCancel(ctx)
	time.AfterFunc(50*time.Millisecond, cancel)
	boardCallBudget = 5 * time.Second
	start = time.Now()
	if _, err := b.d.RemoteDecisionGet(cancelled, agentKey(agent), "alpha", "", "F-2"); errorCode(err) != "link_uncertain" || time.Since(start) > 2*time.Second {
		t.Fatalf("cancel: %v after %s", err, time.Since(start))
	}
	// Nothing commits while the request is held before the board. After the release, the sent
	// acknowledgement can still commit: its client was told uncertain.
	if c := count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND verdict_id=? AND outcome='queued'`, hung.ID); c != 1 {
		t.Fatalf("an ack held before the board committed: %d queued", c)
	}
	open()
	hook(nil)

	// Alpha turns Remote off: offline, and nothing was sent.
	if err := a.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.d.RemoteDecisionGet(ctx, agentKey(agent), "alpha", "", "F-1"); errorCode(err) != "link_computer_offline" {
		t.Fatalf("offline: %v", err)
	}
	// Beta turns Remote off: offline at once.
	if err := b.d.remoteDisable(ctx); err != nil {
		t.Fatal(err)
	}
	var work WorkRefusal
	if _, err := b.d.RemoteDecisionGet(ctx, agentKey(agent), "alpha", "", "F-1"); !errors.As(err, &work) || work.Code != "link_computer_offline" || work.Details["reason"] != "remote_off" {
		t.Fatalf("local Remote off: %v", err)
	}
}

// Alpha turns Remote off between the pair check of the request and its commit: the
// acknowledgement is not stored, and the agent gets uncertain.
func TestBoardRemoteOffBeforeCommit(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "O-1", DecisionFields{Title: ptr("Off"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "O-1", 1, "approved")
	done := make(chan error, 1)
	a.d.link.mu.Lock()
	a.d.link.hook = func(point string) error {
		if point == "before-board" {
			go func() { done <- a.d.remoteDisable(context.Background()) }()
			time.Sleep(300 * time.Millisecond)
		}
		return nil
	}
	a.d.link.mu.Unlock()
	_, err := b.d.RemoteDecisionAck(context.Background(), agentKey(agent), "alpha", "", []int64{v.ID})
	if code := errorCode(err); code != "link_uncertain" && code != "link_remote_required" {
		t.Fatalf("off before commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c := count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND verdict_id=? AND outcome='queued'`, v.ID); c != 1 {
		t.Fatalf("an ack committed after Remote off: %d queued", c)
	}
}

// A maximal card, with its verdict snapshot, 32 recipients and their deliveries, in the worst
// JSON escaping, fits one board answer.
func TestBoardRemoteMaximalCard(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	recipients := []string{agent.Name + "@beta"}
	for i := 1; i < 32; i++ {
		recipients = append(recipients, strings.Repeat("n", 200)+string(rune('a'+i%26))+strings.Repeat("x", i)+"@beta")
	}
	commands := make([]string, 8)
	for i := range commands {
		commands[i] = strings.Repeat("<", 512)
	}
	fill := func(n int) *string { return ptr(strings.Repeat("<", n)) }
	boardCard(t, a, author, "M-1", DecisionFields{Title: fill(256), Where: fill(256), AgentText: fill(8 << 10), Context: fill(16 << 10),
		Recommendation: fill(8 << 10), Commands: &commands, Kind: ptr("decision"), Recipients: &recipients})
	if _, err := a.d.store.DecisionVerdict(verdictCtx(), "M-1", 1, "feedback", strings.Repeat("<", decisionNoteLimit)); err != nil {
		t.Fatal(err)
	}
	item, err := b.d.RemoteDecisionGet(context.Background(), agentKey(agent), "alpha", "", "M-1")
	if err != nil || item.Verdict == nil || len(item.Recipients) != 32 {
		t.Fatalf("maximal card: %v", err)
	}
	reply, err := link.NewFrame("board-ok", item.DecisionItem)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(reply)
	if len(data) > link.FrameLimit("board-ok") || len(data) < 256<<10 {
		t.Fatalf("maximal answer is %d bytes", len(data))
	}
}

// A board request is bounded before any store read.
func TestBoardRequestBounds(t *testing.T) {
	id := strings.Repeat("a", 32)
	good := []boardRequest{
		{ID: id, Op: "get", Names: []string{"n"}, Reference: "R-1"},
		{ID: id, Op: "list", Names: []string{"n", "addr"}},
		{ID: id, Op: "pending", Names: []string{"n"}, Limit: decisionPageItems},
		{ID: id, Op: "ack", Names: []string{"n"}, VerdictIDs: make32()},
	}
	for _, r := range good {
		if !r.valid() {
			t.Fatalf("refused: %+v", r)
		}
	}
	bad := []boardRequest{
		{ID: "short", Op: "get", Names: []string{"n"}, Reference: "R-1"},
		{ID: id, Op: "get", Reference: "R-1"},
		{ID: id, Op: "get", Names: []string{"a", "b", "c"}, Reference: "R-1"},
		{ID: id, Op: "get", Names: []string{"n@alpha"}, Reference: "R-1"},
		{ID: id, Op: "get", Names: []string{strings.Repeat("n", link.MaxAddressBytes+1)}, Reference: "R-1"},
		{ID: id, Op: "get", Names: []string{"n"}, Reference: "R-1@alpha"},
		{ID: id, Op: "list", Names: []string{"n"}, Reference: "R-1"},
		{ID: id, Op: "list", Names: []string{"n"}, Limit: decisionPageItems + 1},
		{ID: id, Op: "list", Names: []string{"n"}, After: -1},
		{ID: id, Op: "ack", Names: []string{"n"}},
		{ID: id, Op: "ack", Names: []string{"n"}, VerdictIDs: append(make32(), 33)},
		{ID: id, Op: "ack", Names: []string{"n"}, VerdictIDs: []int64{0}},
		{ID: id, Op: "edit", Names: []string{"n"}},
		{ID: id, Op: "verdict", Names: []string{"n"}},
	}
	for _, r := range bad {
		if r.valid() {
			t.Fatalf("accepted: %+v", r)
		}
	}
}

func make32() []int64 {
	out := make([]int64, decisionAckIDs)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

type boardDialGate struct{}

// An unpair that finishes while the board call dials: the call sends nothing, and the card's
// computer records no acknowledgement (review 746-C3-F1).
func TestBoardRemoteUnpairDuringDial(t *testing.T) {
	n, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "U-1", DecisionFields{Title: ptr("Unpair"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "U-1", 1, "approved")
	for _, op := range []string{"ack", "get"} {
		entered, release := make(chan struct{}), make(chan struct{})
		b.d.link.mu.Lock()
		b.d.link.dial = func(ctx context.Context, address string) (net.Conn, error) {
			if ctx.Value(boardDialGate{}) == op {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return n.dial(ctx, address)
			}
			// The unpair's own notice to alpha fails, so alpha keeps the pair.
			return nil, errors.New("synthetic dial refused")
		}
		b.d.link.mu.Unlock()
		done := make(chan error, 1)
		ctx := context.WithValue(context.Background(), boardDialGate{}, op)
		go func() {
			var err error
			if op == "get" {
				_, err = b.d.RemoteDecisionGet(ctx, agentKey(agent), "alpha", "", "U-1")
			} else {
				_, err = b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID})
			}
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no dial", op)
		}
		if err := b.d.linkUnpair(context.Background(), "alpha"); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-done; errorCode(err) != "link_computer_unknown" {
			t.Fatalf("%s after the unpair: %v", op, err)
		}
		if op == "ack" {
			// Pair again for the get case.
			b.d.link.mu.Lock()
			b.d.link.dial = nil
			b.d.link.mu.Unlock()
			if err := a.d.linkUnpair(context.Background(), "beta"); err != nil {
				t.Fatal(err)
			}
			pairHosts(t, b, a)
		}
	}
	if c := count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND outcome='acknowledged' AND verdict_id=?`, v.ID); c != 0 {
		t.Fatalf("%d acknowledgements after the unpair", c)
	}
}

// A local deadline ends the call while it waits for the link lock, and the call sends nothing
// after it gave up (review 746-C3-F2).
func TestBoardRemoteDeadlineWhileLockHeld(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "L-1", DecisionFields{Title: ptr("Lock"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "L-1", 1, "approved")
	sent := 0
	a.d.link.mu.Lock()
	a.d.link.hook = func(point string) error {
		if point == "before-board" {
			sent++
		}
		return nil
	}
	a.d.link.mu.Unlock()
	b.d.link.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID})
		done <- err
	}()
	var err error
	returned := false
	select {
	case err = <-done:
		returned = true
	case <-time.After(250 * time.Millisecond):
	}
	elapsed := time.Since(start)
	b.d.link.mu.Unlock()
	if !returned {
		<-done
		t.Fatalf("the call waited for the lock past its deadline: %s", elapsed)
	}
	var work WorkRefusal
	if !errors.As(err, &work) || work.Code != "link_computer_offline" || work.Details["reason"] != "deadline" || elapsed > time.Second {
		t.Fatalf("deadline while the lock is held: %v after %s", err, elapsed)
	}
	// The abandoned exchange gets the lock now, and sends nothing.
	time.Sleep(500 * time.Millisecond)
	a.d.link.mu.Lock()
	got := sent
	a.d.link.mu.Unlock()
	if got != 0 || count(t, a.d.store, `SELECT COUNT(*) FROM decision_deliveries WHERE kind='remote' AND outcome='acknowledged'`) != 0 {
		t.Fatalf("an abandoned call sent %d requests", got)
	}
}

// A cancel during an acknowledgement and a failed write are uncertain; the retry reports the
// stored result once.
func TestBoardRemoteAckCancelAndWriteFailure(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "C-1", DecisionFields{Title: ptr("Cancel"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "C-1", 1, "approved")
	release := make(chan struct{})
	a.d.link.mu.Lock()
	a.d.link.hook = func(point string) error {
		if point == "after-board" {
			<-release
		}
		return nil
	}
	a.d.link.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID}); errorCode(err) != "link_uncertain" {
		t.Fatalf("cancelled ack: %v", err)
	}
	close(release)
	a.d.link.mu.Lock()
	a.d.link.hook = nil
	a.d.link.mu.Unlock()
	// The ack committed before the cancel cut the answer; the retry says already.
	if out, err := b.d.RemoteDecisionAck(context.Background(), agentKey(agent), "alpha", "", []int64{v.ID}); err != nil || len(out.Already) != 1 {
		t.Fatalf("retry after a cancel: %+v %v", out, err)
	}
	// A failure right after the write is uncertain, too.
	b.d.link.mu.Lock()
	b.d.link.hook = func(point string) error {
		if point == "after-board-write" {
			return errLinkDrop
		}
		return nil
	}
	b.d.link.mu.Unlock()
	if _, err := b.d.RemoteDecisionGet(context.Background(), agentKey(agent), "alpha", "", "C-1"); errorCode(err) != "link_uncertain" {
		t.Fatalf("write failure: %v", err)
	}
}

// A list and a pending walk over several pages: each page fits its local byte bound and the
// board answer's frame, the cursor moves forward, and every item comes once.
func TestBoardRemotePageWalk(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	const cards = 110
	for i := 0; i < cards; i++ {
		f := DecisionFields{Title: ptr(strings.Repeat("<", 256)), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}}
		if i < 12 {
			f.Context, f.Recommendation = ptr(strings.Repeat("<", 16<<10)), ptr(strings.Repeat("<", 8<<10))
		}
		reference := fmt.Sprintf("W-%03d", i)
		boardCard(t, a, author, reference, f)
		if i < 12 {
			mustVerdict(t, a.d.store, reference, 1, "approved")
		}
	}
	frame := func(v any) int {
		f, err := link.NewFrame("board-ok", v)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(f)
		return len(data)
	}
	seen, pages := map[string]bool{}, 0
	var after int64
	for {
		page, err := b.d.RemoteDecisionList(context.Background(), agentKey(agent), "alpha", "", after, 0)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		body, _ := json.Marshal(map[string]any{"ok": true, "result": page})
		if len(body) > decisionPageBytes || frame(page.DecisionPage) > link.FrameLimit("board-ok") || len(page.Items) == 0 {
			t.Fatalf("list page %d: %d bytes, %d items", pages, len(body), len(page.Items))
		}
		for _, item := range page.Items {
			if seen[item.Reference] {
				t.Fatalf("%s twice", item.Reference)
			}
			seen[item.Reference] = true
		}
		if !page.More {
			break
		}
		if *page.NextAfter <= after {
			t.Fatalf("list cursor did not move: %d", *page.NextAfter)
		}
		after = *page.NextAfter
	}
	if len(seen) != cards || pages < 2 {
		t.Fatalf("list walk: %d cards in %d pages", len(seen), pages)
	}
	verdicts, pages := map[int64]bool{}, 0
	after = 0
	for {
		page, err := b.d.RemoteDecisionPending(context.Background(), agentKey(agent), "alpha", "", after, 0)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		body, _ := json.Marshal(map[string]any{"ok": true, "result": page})
		if len(body) > decisionPendingBytes || frame(page.DecisionPendingPage) > link.FrameLimit("board-ok") || len(page.Deliveries) == 0 {
			t.Fatalf("pending page %d: %d bytes, %d deliveries", pages, len(body), len(page.Deliveries))
		}
		for _, d := range page.Deliveries {
			if verdicts[d.VerdictID] {
				t.Fatalf("verdict %d twice", d.VerdictID)
			}
			verdicts[d.VerdictID] = true
		}
		if !page.More {
			break
		}
		if *page.NextAfter <= after {
			t.Fatalf("pending cursor did not move: %d", *page.NextAfter)
		}
		after = *page.NextAfter
	}
	if len(verdicts) != 12 || pages < 2 {
		t.Fatalf("pending walk: %d verdicts in %d pages", len(verdicts), pages)
	}
}

// A cancel after the write returns uncertain at once, also while the exchange's cleanup waits
// for the link lock (review 746-C3-F2, writing phase).
func TestBoardRemoteCancelAfterWriteDuringCleanup(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	author := join(t, a.d.store, "claude", "synthetic-board-author", "")
	agent := joinAs(t, b.d.store, "codex", "synthetic-board-agent", linkRepo(t, "agent"), "")
	boardCard(t, a, author, "K-1", DecisionFields{Title: ptr("Cleanup"), Kind: ptr("decision"), Recipients: &[]string{agent.Name + "@beta"}})
	v := mustVerdict(t, a.d.store, "K-1", 1, "approved")
	wrote, resume := make(chan struct{}), make(chan struct{})
	b.d.link.mu.Lock()
	b.d.link.hook = func(point string) error {
		if point == "after-board-write" {
			close(wrote)
			<-resume
		}
		return nil
	}
	b.d.link.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out RemoteAck
	done := make(chan error, 1)
	go func() {
		var err error
		out, err = b.d.RemoteDecisionAck(ctx, agentKey(agent), "alpha", "", []int64{v.ID})
		done <- err
	}()
	select {
	case <-wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("no write")
	}
	b.d.link.mu.Lock()
	close(resume)
	cancel()
	var err error
	returned := false
	select {
	case err = <-done:
		returned = true
	case <-time.After(250 * time.Millisecond):
	}
	b.d.link.mu.Unlock()
	if !returned {
		<-done
		t.Fatal("a cancel after the write waited for the exchange's cleanup")
	}
	if errorCode(err) != "link_uncertain" || len(out.Acknowledged)+len(out.Already) != 0 {
		t.Fatalf("cancel after the write: %+v %v", out, err)
	}
	// The late exchange changes nothing that the call returned.
	time.Sleep(300 * time.Millisecond)
	if len(out.Acknowledged)+len(out.Already) != 0 {
		t.Fatalf("a late exchange changed the result: %+v", out)
	}
}
