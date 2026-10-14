package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Criterion 4: a message to an address belongs to the participant. Its holder reads and
// acknowledges it, the acknowledgement names the native session, and after a change of
// holder the successor reads the unread messages from the old acknowledgement point. A
// message to a peer name stays with that session.
func TestParticipantInbox(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	a := joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	sender := join(t, s, "claude", "synthetic-sender", repo)
	from := Key{"claude", "synthetic-sender"}
	for _, body := range []string{"one", "two", "three"} {
		if out, err := s.Send(ctx, from, "codex-koinon", body); err != nil || out.Recipient != "codex-koinon" {
			t.Fatalf("send to the address: %+v %v", out, err)
		} else if got, err := s.MessageOutcome(ctx, from, out.ID); err != nil || got.Recipient != out.Recipient {
			t.Fatalf("participant delivery recipient: %+v %v", got, err)
		}
	}
	if _, err := s.Send(ctx, from, a.Name, "to the peer name"); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ReadInboxes(ctx, Key{"codex", "synthetic-a"}, 0, nil, 10)
	if err != nil || inbox.LastSeq != 1 || inbox.Participant == nil || inbox.Participant.LastSeq != 3 || len(inbox.Messages) != 4 {
		t.Fatalf("holder inbox: %+v %v", inbox, err)
	}
	for _, m := range inbox.Messages {
		want := "participant"
		if m.Body == "to the peer name" {
			want = "session"
		}
		if m.Inbox != want || m.SenderName != sender.Alias {
			t.Fatalf("message %+v", m)
		}
	}
	if address, acked, err := s.AckParticipant(ctx, Key{"codex", "synthetic-a"}, 1); err != nil || address != "codex-koinon" || acked != 1 {
		t.Fatalf("participant ack: %s %d %v", address, acked, err)
	}
	var by string
	if err := s.db.QueryRow(`SELECT acked_by FROM sessions WHERE id='participant:codex-koinon'`).Scan(&by); err != nil || by != "codex:synthetic-a" {
		t.Fatalf("ack provenance: %q %v", by, err)
	}
	// The holder sends as its participant.
	if _, err := s.Send(ctx, Key{"codex", "synthetic-a"}, sender.Name, "reply"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ReadInbox(ctx, from, 0, 10); got.Messages[0].SenderName != "codex-koinon" {
		t.Fatalf("holder's sender name: %+v", got.Messages)
	}
	// B becomes the holder; it continues the participant's inbox from the old point.
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatal(err)
	}
	inbox, err = s.ReadInboxes(ctx, Key{"codex", "synthetic-b"}, 0, nil, 10)
	if err != nil || inbox.Participant == nil || inbox.Participant.AckedThrough != 1 || inbox.Participant.LastSeq != 3 {
		t.Fatalf("successor inbox: %+v %v", inbox, err)
	}
	unread := 0
	for _, m := range inbox.Messages {
		if m.Inbox == "participant" && !m.Acknowledged {
			unread++
		}
	}
	if unread != 2 {
		t.Fatalf("successor's unread participant messages: %d", unread)
	}
	// The message to A's peer name stayed with A.
	var own int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE recipient_family='codex' AND recipient_id='synthetic-a'`).Scan(&own); err != nil || own != 1 {
		t.Fatalf("peer-name message: %d %v", own, err)
	}
	// The internal inbox row is not a session that anyone lists.
	peers, _, err := s.Peers(ctx, from)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if strings.HasPrefix(p.Name, "participant:") || p.Name == "" {
			t.Fatalf("participant inbox listed: %+v", p)
		}
	}
	counts, err := s.Counts(ctx)
	if err != nil || counts["total"] != 3 {
		t.Fatalf("counts: %+v %v", counts, err)
	}
	if validKey("codex", "participant:codex-koinon") {
		t.Fatal("a native session ID can take a participant inbox's form")
	}
}

// Criterion 5: after a change of holder the former holder is retired and fenced. Each of its
// calls for the participant is refused and changes nothing; registered again, it gets its
// peer name only, and the fence survives a restart, repeated registration and the
// successor's expiry.
func TestFencedFormerHolder(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	join(t, s, "claude", "synthetic-sender", repo)
	if _, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, "codex-koinon", "for the participant"); err != nil {
		t.Fatal(err)
	}
	a := Key{"codex", "synthetic-a"}
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatal(err)
	}
	// Retired: every call is refused.
	if _, err := s.ReadInboxes(ctx, a, 0, nil, 10); !errors.Is(err, ErrCallerInactive) {
		t.Fatalf("retired former holder read: %v", err)
	}
	// Registered again: its own session works, the participant's inbox does not.
	again := joinAs(t, s, "codex", "synthetic-a", repo, "")
	if again.HoldsAddress || !again.Fenced {
		t.Fatalf("registered again: %+v", again)
	}
	after := int64(0)
	if _, err := s.ReadInboxes(ctx, a, 0, &after, 10); code(err) != "stale_holder" {
		t.Fatalf("participant read by the former holder: %v", err)
	}
	if inbox, err := s.ReadInboxes(ctx, a, 0, nil, 10); err != nil || inbox.Participant != nil {
		t.Fatalf("own inbox of the former holder: %+v %v", inbox, err)
	}
	if _, _, err := s.AckParticipant(ctx, a, 1); code(err) != "stale_holder" {
		t.Fatalf("participant ack by the former holder: %v", err)
	}
	var acked int64
	if err := s.db.QueryRow(`SELECT acked_through FROM sessions WHERE id='participant:codex-koinon'`).Scan(&acked); err != nil || acked != 0 {
		t.Fatalf("refused ack changed the inbox: %d %v", acked, err)
	}
	// It sends with its own peer name, never as the participant.
	if _, err := s.Send(ctx, a, "claude-koinon", "from the former holder"); err != nil {
		t.Fatal(err)
	}
	var sender string
	s.db.QueryRow(`SELECT sender_name FROM messages WHERE body='from the former holder'`).Scan(&sender)
	if sender != again.Name {
		t.Fatalf("former holder's sender name: %q", sender)
	}
	// The fence persists over a restart, repeated registration and the successor's expiry.
	s.db.Close()
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = func() time.Time { return clock }
	for range 2 {
		if again = joinAs(t, reopened, "codex", "synthetic-a", repo, ""); again.HoldsAddress || !again.Fenced {
			t.Fatalf("after a restart: %+v", again)
		}
	}
	clock = clock.Add(61 * time.Second)
	if again = joinAs(t, reopened, "codex", "synthetic-a", repo, ""); again.HoldsAddress || !again.Fenced {
		t.Fatalf("after the successor expired: %+v", again)
	}
}

// The change of holder and the former holder's retirement and fence commit together: a
// failure after the first write leaves all of them unchanged.
func TestHolderChangeIsAtomic(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_fence BEFORE INSERT ON participant_fences BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err == nil {
		t.Fatal("choice with a failing fence committed")
	}
	if a := sessionState(t, s, Key{"codex", "synthetic-a"}); a.State != "active" || !a.HoldsAddress || a.Fenced {
		t.Fatalf("former holder after a failed change: %+v", a)
	}
	if p := participantOf(t, s, "codex-koinon"); p.Holder == "" || p.LastEvent.Reason == "maintainer_choice" {
		t.Fatalf("participant after a failed change: %+v", p)
	}
}

// A participant's messages wake its current holder, and the next holder after a change; with
// no active holder they wait. An Antigravity holder is offered them at its Stop boundary.
func TestParticipantWake(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	joinAs(t, s, "codex", "synthetic-a", repo, "")
	b := joinAs(t, s, "codex", "synthetic-b", repo, "")
	join(t, s, "claude", "synthetic-sender", repo)
	var targets []string
	var notices []string
	s.wake.send = func(_ context.Context, target Session, notice string) wakeResult {
		targets, notices = append(targets, target.ID), append(notices, notice)
		return wakeResult{"notified", ""}
	}
	if _, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, "codex-koinon", "wake the holder"); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != "synthetic-a" || !strings.Contains(notices[0], `"participant:codex-koinon"`) || strings.Contains(notices[0], "wake the holder") {
		t.Fatalf("holder wake: %v %v", targets, notices)
	}
	if err := s.ChooseHolder(ctx, "codex-koinon", Key{"codex", "synthetic-b"}, b.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, "codex-koinon", "wake the successor"); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[1] != "synthetic-b" {
		t.Fatalf("successor wake: %v", targets)
	}
	// An Antigravity holder: no push; its Stop boundary offers the participant's range.
	g := joinAs(t, s, "agy", "synthetic-agy", repo, "")
	if _, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, g.Alias, "for agy"); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeStep(ctx); err != nil || len(targets) != 2 {
		t.Fatalf("agy pushed: %v %v", targets, err)
	}
	s.wake.mu.Lock()
	notice, err := s.offerWake(ctx, Key{"agy", "synthetic-agy"})
	s.wake.mu.Unlock()
	if err != nil || !strings.Contains(notice, `"participant:agy-koinon"`) {
		t.Fatalf("agy offer: %q %v", notice, err)
	}
}

// A session that moved to another repository can still be recorded on its earlier address;
// it acts for the participant it holds now.
func TestMovedHolderActsForItsCurrentParticipant(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	first := namedRepo(t, "koinon")
	second := namedRepo(t, "other")
	joinAs(t, s, "codex", "synthetic-a", first, "")
	moved := joinAs(t, s, "codex", "synthetic-a", second, "")
	if moved.Alias != "codex-other" {
		t.Fatalf("moved session: %+v", moved)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if address, err := heldAddress(ctx, tx, s.now().UnixMilli(), Key{"codex", "synthetic-a"}); err != nil || address != "codex-other" {
		t.Fatalf("held address: %q %v", address, err)
	}
}
