package core

// Tests of messages between paired computers (#670 chunk 03): two or three in-process
// daemons, real TLS, synthetic Tailscale.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// waitLinkState reads a queued message's state until it is final, or for 20 seconds.
func waitLinkState(t *testing.T, h *linkHost, caller Session, linkID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status, reply := callerPost(t, h, "/v1/messages/outcome", map[string]any{"caller": agentKey(caller), "link_id": linkID})
		m, _ := reply["message"].(map[string]any)
		if status != 200 {
			t.Fatalf("delivery %s: %d %v", linkID, status, reply)
		}
		if s := m["delivery_state"]; s != "queued" && s != "may_have_been_delivered" || time.Now().After(deadline) {
			return m
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readInbox reads a session's own and held participant messages from the start.
func readInbox(t *testing.T, h *linkHost, s Session) []map[string]any {
	t.Helper()
	request := map[string]any{"caller": agentKey(s), "after": 0, "limit": 100}
	if s.HoldsAddress {
		request["participant_after"] = 0
	}
	status, reply := callerPost(t, h, "/v1/inbox/read", request)
	if status != 200 {
		t.Fatalf("inbox: %d %v", status, reply)
	}
	inbox, _ := reply["inbox"].(map[string]any)
	var out []map[string]any
	for _, m := range inbox["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

// fromName is the name a session's messages carry: its held address, else its peer name.
func fromName(s Session) string {
	if s.HoldsAddress {
		return s.Address
	}
	return s.Name
}

func linkSendAs(t *testing.T, h *linkHost, s Session, to, body string, replyTo int64) (int, map[string]any) {
	t.Helper()
	request := map[string]any{"caller": agentKey(s), "to": to, "body": body}
	if replyTo != 0 {
		request["reply_to"] = replyTo
	}
	return callerPost(t, h, "/v1/messages/send", request)
}

func mustQueue(t *testing.T, h *linkHost, s Session, to, body string) string {
	t.Helper()
	status, reply := linkSendAs(t, h, s, to, body, 0)
	m, _ := reply["message"].(map[string]any)
	if status != 200 || m["delivery_state"] != "queued" {
		t.Fatalf("send %s: %d %v", to, status, reply)
	}
	return m["link_id"].(string)
}

// A message goes into the other computer's inbox with its origin, and a reply by reply_to
// comes back on the same pair (LINK-670-WHAT items 1 and 3).
func TestLinkMessageRoundTrip(t *testing.T) {
	_, a, b := pairedLinkHosts(t)
	sender := joinAs(t, a.d.store, "codex", "synthetic-sender", linkRepo(t, "send"), "")
	receiver := joinAs(t, b.d.store, "codex", "synthetic-receiver", linkRepo(t, "recv"), "")
	id := mustQueue(t, a, sender, receiver.Name+"@beta", "hello from alpha")
	if got := waitLinkState(t, a, sender, id); got["delivery_state"] != "received" || got["recipient"] != receiver.Name+"@beta" {
		t.Fatalf("outcome: %v", got)
	}
	inbox := readInbox(t, b, receiver)
	if len(inbox) != 1 || inbox[0]["body"] != "hello from alpha" || inbox[0]["sender_name"] != fromName(sender)+"@alpha" ||
		inbox[0]["origin"] != "link" || inbox[0]["sender_family"] != "codex" {
		t.Fatalf("inbox: %v", inbox)
	}
	// The reply goes back through the stored pair.
	status, reply := linkSendAs(t, b, receiver, "", "hello back", int64(inbox[0]["id"].(float64)))
	m, _ := reply["message"].(map[string]any)
	if status != 200 || m["recipient"] != fromName(sender)+"@alpha" {
		t.Fatalf("reply: %d %v", status, reply)
	}
	if got := waitLinkState(t, b, receiver, m["link_id"].(string)); got["delivery_state"] != "received" {
		t.Fatalf("reply outcome: %v", got)
	}
	back := readInbox(t, a, sender)
	if len(back) != 1 || back[0]["body"] != "hello back" || back[0]["sender_name"] != fromName(receiver)+"@beta" {
		t.Fatalf("reply inbox: %v", back)
	}
	// Another session cannot read the state of a message it did not send.
	other := joinAs(t, a.d.store, "codex", "synthetic-other", linkRepo(t, "other"), "")
	if status, reply := callerPost(t, a, "/v1/messages/outcome", map[string]any{"caller": agentKey(other), "link_id": id}); status == 200 ||
		reply["code"] != "link_message_not_found" {
		t.Fatalf("other session's delivery: %d %v", status, reply)
	}
	// Exactly one of message_id and link_id.
	if status, _ := callerPost(t, a, "/v1/messages/outcome", map[string]any{"caller": agentKey(sender), "link_id": id, "message_id": 1}); status == 200 {
		t.Fatal("both IDs accepted")
	}
	_ = strings.Repeat
	_ = context.Background
}
