package core

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Handled wakes (#250): synthetic sessions, a patched wake adapter and a test clock.

// later moves the test clock and renews the sessions, whose test lifetime is 60 seconds.
func later(t *testing.T, s *Store, clock *time.Time, d time.Duration, sessions ...Session) {
	t.Helper()
	*clock = clock.Add(d)
	for _, x := range sessions {
		join(t, s, x.Family, x.ID, x.Directory)
	}
}

// countingWake replaces the adapter with one that returns result and counts its calls.
func countingWake(s *Store, result wakeResult) *int {
	calls := 0
	s.wake.send = func(context.Context, Session, string) wakeResult { calls++; return result }
	return &calls
}

type wakeRow struct {
	state, reason, wake string
	notices, next       int64
}

func messageRow(t *testing.T, s *Store, id int64) wakeRow {
	t.Helper()
	var r wakeRow
	if err := s.db.QueryRow(`SELECT delivery_state,delivery_reason,wake_reason,wake_notices,wake_next_at FROM messages WHERE id=?`, id).
		Scan(&r.state, &r.reason, &r.wake, &r.notices, &r.next); err != nil {
		t.Fatal(err)
	}
	return r
}

func step(t *testing.T, s *Store) {
	t.Helper()
	if err := s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWakeReadStopsNotices(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "codex")
	calls := countingWake(s, accepted())
	step(t, s)
	if *calls != 1 || messageRow(t, s, message.ID).notices != 1 {
		t.Fatalf("first notice: %d calls, %+v", *calls, messageRow(t, s, message.ID))
	}
	if _, err := s.ReadInbox(context.Background(), Key{receiver.Family, receiver.ID}, 0, 50); err != nil {
		t.Fatal(err)
	}
	later(t, s, clock, wakeReadWaitMax, sender, receiver)
	step(t, s)
	if *calls != 1 {
		t.Fatalf("a read message was noticed again: %d calls", *calls)
	}
	health, err := s.WakeHealth(context.Background())
	if err != nil || health["notified"] != int64(0) || health["failed"] != int64(0) {
		t.Fatalf("health of a read message: %v %v", health, err)
	}
}

func TestWakeUnreadRetriesThenFails(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	calls := countingWake(s, accepted())
	step(t, s)
	for n := int64(1); n <= wakeNoticeLimit; n++ {
		row := messageRow(t, s, message.ID)
		wait := readWait(n)
		if *calls != int(n) || row.state != "notified" || row.notices != n || row.next != clock.UnixMilli()+wait.Milliseconds() {
			t.Fatalf("notice %d: %d calls, %+v", n, *calls, row)
		}
		// Before its wait ends, the message gets no notice.
		later(t, s, clock, wait-time.Second, sender, receiver)
		step(t, s)
		if *calls != int(n) {
			t.Fatalf("notice %d repeated early", n)
		}
		later(t, s, clock, time.Second, sender, receiver)
		step(t, s)
	}
	row := messageRow(t, s, message.ID)
	if *calls != wakeNoticeLimit || row.state != "failed" || row.reason != "not_read" {
		t.Fatalf("after the limit: %d calls, %+v", *calls, row)
	}
	outcome, err := s.MessageOutcome(ctx, Key{sender.Family, sender.ID}, message.ID)
	if err != nil || outcome.DeliveryState != "failed" || outcome.DeliveryReason != "not_read" || outcome.Notices != wakeNoticeLimit {
		t.Fatalf("sender's outcome: %+v %v", outcome, err)
	}
	// One content-free notice reaches the maintainer.
	var count int
	var from, body string
	if err := s.db.QueryRow(`SELECT COUNT(*),MAX(sender_name),MAX(body) FROM messages WHERE recipient_family='maintainer'`).
		Scan(&count, &from, &body); err != nil || count != 1 || from != "koinon" ||
		!strings.Contains(body, "Message "+strconv.FormatInt(message.ID, 10)+" to "+receiver.Name) ||
		!strings.Contains(body, "not_read") || strings.Contains(body, "PRIVATE") {
		t.Fatalf("maintainer notice: %d %q %q %v", count, from, body, err)
	}
	later(t, s, clock, wakeReadWaitMax, sender, receiver)
	step(t, s)
	if *calls != wakeNoticeLimit {
		t.Fatalf("a failed message was noticed again: %d calls", *calls)
	}
	if health, err := s.WakeHealth(ctx); err != nil || health["failed"] != int64(1) {
		t.Fatalf("health: %v %v", health, err)
	}
	// The recipient still finds it unread.
	inbox, err := s.ReadInbox(ctx, Key{receiver.Family, receiver.ID}, 0, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].Acknowledged {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
}

func TestWakeBusyAndTransportResultsDoNotCount(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "codex")
	calls := countingWake(s, waiting("receiver_busy"))
	step(t, s)
	later(t, s, clock, wakeBusyPoll, sender, receiver)
	step(t, s)
	countingWake(s, waiting("cli_unavailable"))
	later(t, s, clock, wakeBusyPoll, sender, receiver)
	step(t, s)
	if row := messageRow(t, s, message.ID); *calls != 2 || row.notices != 0 || row.state != "waiting" {
		t.Fatalf("busy and transport results: %d calls, %+v", *calls, row)
	}
	// A transport that cannot confirm its write may have reached the agent: it counts and
	// waits for a read, as an accepted notice does.
	calls = countingWake(s, uncertain("claude_transport_only"))
	later(t, s, clock, wakeBackoffMax, sender, receiver)
	step(t, s)
	later(t, s, clock, wakeReadWait-time.Second, sender, receiver)
	step(t, s)
	if row := messageRow(t, s, message.ID); *calls != 1 || row.notices != 1 || row.state != "uncertain" {
		t.Fatalf("uncertain transport: %d calls, %+v", *calls, row)
	}
}

func TestWakeSkippedReadKeepsNotices(t *testing.T) {
	s, _, sender, receiver, first, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	second, err := s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Name, "second")
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	s.wake.send = func(_ context.Context, _ Session, notice string) wakeResult {
		notices = append(notices, notice)
		return accepted()
	}
	step(t, s)
	// A read after the first message returns only the second: the first stays unread.
	inbox, err := s.ReadInbox(ctx, Key{receiver.Family, receiver.ID}, first.Seq, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].Seq != second.Seq {
		t.Fatalf("skipping read: %+v %v", inbox, err)
	}
	later(t, s, clock, wakeReadWait, sender, receiver)
	step(t, s)
	want := strconv.FormatInt(first.Seq, 10) + "-" + strconv.FormatInt(second.Seq, 10)
	if len(notices) != 2 || !strings.Contains(notices[1], want) || messageRow(t, s, first.ID).reason != "" {
		t.Fatalf("notices after a skipping read: %q, %+v", notices, messageRow(t, s, first.ID))
	}
	// A read from the start covers both.
	if _, err := s.ReadInbox(ctx, Key{receiver.Family, receiver.ID}, 0, 50); err != nil {
		t.Fatal(err)
	}
	later(t, s, clock, wakeReadWaitMax, sender, receiver)
	step(t, s)
	if len(notices) != 2 {
		t.Fatalf("notice after a full read: %q", notices)
	}
}

func TestWakeNoticeCountsPerMessageAcrossRestart(t *testing.T) {
	s, root, sender, receiver, first, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	countingWake(s, accepted())
	step(t, s)
	later(t, s, clock, wakeReadWait, sender, receiver)
	second, err := s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Name, "second")
	if err != nil {
		t.Fatal(err)
	}
	step(t, s)
	a, b := messageRow(t, s, first.ID), messageRow(t, s, second.ID)
	now := clock.UnixMilli()
	if a.notices != 2 || b.notices != 1 || a.reason != "" || a.next != now+readWait(2).Milliseconds() || b.next != now+readWait(1).Milliseconds() {
		t.Fatalf("staggered counts: %+v %+v", a, b)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	restored.now = func() time.Time { return *clock }
	if a, b := messageRow(t, restored, first.ID), messageRow(t, restored, second.ID); a.notices != 2 || b.notices != 1 {
		t.Fatalf("counts after a restart: %+v %+v", a, b)
	}
	// The second message's wait ends first. The notice covers the due message only; the
	// first keeps its own count and wait.
	calls := countingWake(restored, accepted())
	later(t, restored, clock, readWait(1), sender, receiver)
	step(t, restored)
	if a, b := messageRow(t, restored, first.ID), messageRow(t, restored, second.ID); *calls != 1 || a.notices != 2 || b.notices != 2 {
		t.Fatalf("after the restart: %d calls, %+v %+v", *calls, a, b)
	}
}

func TestWakeTurnFailure(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	calls := countingWake(s, accepted())
	step(t, s)
	failed := func(code string) {
		t.Helper()
		*clock = clock.Add(time.Second)
		if err := s.Observe(ctx, Observation{Caller: Key{receiver.Family, receiver.ID},
			Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: "idle", TurnError: code}}); err != nil {
			t.Fatal(err)
		}
		*clock = clock.Add(time.Second)
		step(t, s)
	}
	failed("server_overloaded")
	row := messageRow(t, s, message.ID)
	if *calls != 1 || row.reason != "turn_failed:server_overloaded" || row.state != "notified" {
		t.Fatalf("observed failure: %d calls, %+v", *calls, row)
	}
	outcome, err := s.MessageOutcome(ctx, Key{sender.Family, sender.ID}, message.ID)
	if err != nil || outcome.DeliveryReason != "turn_failed:server_overloaded" || outcome.WakeReason != "accepted" || outcome.Notices != 1 {
		t.Fatalf("sender's outcome: %+v %v", outcome, err)
	}
	// The next notice keeps the wait, then starts without a reason.
	later(t, s, clock, readWait(1), sender, receiver)
	step(t, s)
	if row := messageRow(t, s, message.ID); *calls != 2 || row.reason != "" || row.notices != 2 {
		t.Fatalf("notice after a failed turn: %d calls, %+v", *calls, row)
	}
	// An earlier failure does not count against a later notice.
	if row := messageRow(t, s, message.ID); row.reason != "" {
		t.Fatalf("an old failure applied to a new notice: %+v", row)
	}
	for n := int64(2); n < wakeNoticeLimit; n++ {
		later(t, s, clock, readWait(n), sender, receiver)
		step(t, s)
	}
	// At the limit, a failed turn fails the message at once.
	failed("usage_limit_exceeded")
	row = messageRow(t, s, message.ID)
	if *calls != wakeNoticeLimit || row.state != "failed" || row.reason != "turn_failed:usage_limit_exceeded" {
		t.Fatalf("failure at the limit: %d calls, %+v", *calls, row)
	}
}

func TestTurnErrorObservationRules(t *testing.T) {
	s, _, _, receiver, _, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	key := Key{receiver.Family, receiver.ID}
	at := clock.UnixMilli()
	for _, o := range []Observation{
		{Caller: key, Activity: &ObservedValue{Source: "codex_rollout", At: at, State: "busy", TurnError: "server_overloaded"}},
		{Caller: key, Activity: &ObservedValue{Source: "codex_rollout", At: at, State: "idle", TurnError: "Selected model is at capacity"}},
		{Caller: key, Activity: &ObservedValue{Source: "mcp_call", At: at, State: "idle", TurnError: "server_overloaded"}},
		{Caller: key, Model: &ObservedValue{Source: "codex_rollout", At: at, ID: "gpt", TurnError: "server_overloaded"}},
	} {
		if err := s.Observe(ctx, o); err != ErrInvalid {
			t.Fatalf("accepted %+v: %v", o.Activity, err)
		}
	}
	for info, want := range map[string]string{`"server_overloaded"`: "server_overloaded", `{"response_too_many_failed_attempts":{"http_status_code":429}}`: "response_too_many_failed_attempts",
		`"something new"`: "other", `{"a":1,"b":2}`: "other", `null`: "other", ``: "other", `7`: "other"} {
		if got := TurnErrorCode([]byte(info)); got != want {
			t.Fatalf("TurnErrorCode(%s) = %q, want %q", info, got, want)
		}
	}
}

func TestAcknowledgementRecordsSessionAndOperation(t *testing.T) {
	s, _, sender, receiver, message, _ := wakeFixture(t, "codex")
	ctx := context.Background()
	key := Key{receiver.Family, receiver.ID}
	second, err := s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Name, "second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(ctx, key, message.Seq); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ack(ctx, key, -1, true); err != nil {
		t.Fatal(err)
	}
	inbox, err := s.ReadInbox(ctx, key, 0, 50)
	if err != nil || len(inbox.Messages) != 2 {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	if m := inbox.Messages[0]; m.AcknowledgedSession != receiver.Name || m.AcknowledgedVia != "ack" || m.AcknowledgedBy != "recipient" {
		t.Fatalf("own acknowledgement: %+v", m)
	}
	if m := inbox.Messages[1]; m.AcknowledgedSession != "maintainer" || m.AcknowledgedVia != "dashboard" || m.AcknowledgedBy != "maintainer" {
		t.Fatalf("dashboard acknowledgement: %+v", m)
	}
	outcome, err := s.MessageOutcome(ctx, Key{sender.Family, sender.ID}, second.ID)
	if err != nil || outcome.AcknowledgedSession != "maintainer" || outcome.AcknowledgedVia != "dashboard" {
		t.Fatalf("outcome: %+v %v", outcome, err)
	}
}

// A participant's notice budget belongs to its holder: a holder change returns its
// unacknowledged notified, uncertain and failed messages to waiting with no notices, and
// what the former holder read the new one has not.
func TestHolderChangeResetsReadAndNoticeBudget(t *testing.T) {
	s, h, advance := successionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	arrive(t, s, arrival{family: "claude", id: "synthetic-sender", repo: namedRepo(t, "other"), pid: 200, pane: "%2", toolCall: true})
	var ids []int64
	for _, body := range []string{"acknowledged", "read", "exhausted"} {
		sent, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, held.Address, body)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sent.ID)
	}
	if _, _, err := s.AckParticipant(ctx, Key{"codex", "thread-1"}, 1); err != nil {
		t.Fatal(err)
	}
	inbox := participantPrefix + held.Address
	if _, err := s.db.Exec(`UPDATE sessions SET read_through=2 WHERE family='codex' AND id=?`, inbox); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET delivery_state='failed',delivery_reason='not_read',wake_notices=4 WHERE id=?`, ids[2]); err != nil {
		t.Fatal(err)
	}
	advance(31 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, pane: "%1", toolCall: true})
	wantSuccession(t, next, "succeeded", "same_host", held.Name)
	var read int64
	if err := s.db.QueryRow(`SELECT read_through FROM sessions WHERE family='codex' AND id=?`, inbox).Scan(&read); err != nil || read != 1 {
		t.Fatalf("read_through after the holder change: %d %v", read, err)
	}
	if row := messageRow(t, s, ids[2]); row.state != "waiting" || row.notices != 0 || row.reason != "" {
		t.Fatalf("exhausted message after the holder change: %+v", row)
	}
	var woken []string
	s.wake.send = func(_ context.Context, target Session, notice string) wakeResult {
		woken = append(woken, target.ID+" "+notice)
		return accepted()
	}
	s.wake.mu.Lock()
	_, err := s.submitWake(ctx, Key{"codex", inbox}, false)
	s.wake.mu.Unlock()
	if err != nil || len(woken) != 1 || !strings.HasPrefix(woken[0], "thread-2 ") || !strings.Contains(woken[0], "2-3") {
		t.Fatalf("wake of the new holder: %q %v", woken, err)
	}
}

// A read that a holder change overtakes does not advance the new holder's read (#250 review).
func TestFormerHolderReadDoesNotAdvanceSuccessor(t *testing.T) {
	s, h, advance := successionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	arrive(t, s, arrival{family: "claude", id: "synthetic-sender", repo: namedRepo(t, "other"), pid: 200, pane: "%2", toolCall: true})
	sent, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, held.Address, "pending")
	if err != nil {
		t.Fatal(err)
	}
	advance(31 * time.Second)
	arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, pane: "%1", toolCall: true})
	// The former holder's read returned the message before the holder change committed.
	zero := int64(0)
	s.markRead(ctx, Key{"codex", "thread-1"}, held.Address, 0, &zero, []Message{{Seq: sent.Seq, Inbox: "participant"}})
	var read int64
	if err := s.db.QueryRow(`SELECT read_through FROM sessions WHERE family='codex' AND id=?`, participantPrefix+held.Address).Scan(&read); err != nil || read != 0 {
		t.Fatalf("read_through after the former holder's read: %d %v", read, err)
	}
	// The holder's own read still counts.
	s.markRead(ctx, Key{"codex", "thread-2"}, held.Address, 0, &zero, []Message{{Seq: sent.Seq, Inbox: "participant"}})
	if err := s.db.QueryRow(`SELECT read_through FROM sessions WHERE family='codex' AND id=?`, participantPrefix+held.Address).Scan(&read); err != nil || read != sent.Seq {
		t.Fatalf("read_through after the holder's read: %d %v", read, err)
	}
}

// An Antigravity Stop offer counts as a notice, and its messages fail at the limit (#250 review).
func TestAntigravityOffersCountAndFail(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "agy")
	ctx := context.Background()
	offer := func() string {
		t.Helper()
		s.wake.mu.Lock()
		defer s.wake.mu.Unlock()
		notice, err := s.offerWake(ctx, Key{receiver.Family, receiver.ID})
		if err != nil {
			t.Fatal(err)
		}
		return notice
	}
	for n := int64(1); n <= wakeNoticeLimit; n++ {
		if notice := offer(); notice == "" {
			t.Fatalf("offer %d: no notice", n)
		}
		if row := messageRow(t, s, message.ID); row.notices != n || row.wake != "stop_offered" || row.state != "uncertain" {
			t.Fatalf("offer %d: %+v", n, row)
		}
		if notice := offer(); notice != "" {
			t.Fatalf("offer %d repeated within its wait", n)
		}
		later(t, s, clock, readWait(n), sender, receiver)
	}
	step(t, s)
	if row := messageRow(t, s, message.ID); row.state != "failed" || row.reason != "not_read" {
		t.Fatalf("after the limit: %+v", row)
	}
	if notice := offer(); notice != "" {
		t.Fatalf("a failed message was offered: %q", notice)
	}
}

// A notice range that spans a message whose wait has not ended leaves its count and wait
// (#250 review).
func TestWakeRangeKeepsAMessageThatIsNotDue(t *testing.T) {
	s, _, sender, receiver, first, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	calls := countingWake(s, accepted())
	step(t, s)
	later(t, s, clock, 30*time.Second, sender, receiver)
	second, err := s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Name, "second")
	if err != nil {
		t.Fatal(err)
	}
	step(t, s)
	before := messageRow(t, s, second.ID)
	later(t, s, clock, 90*time.Second, sender, receiver)
	third, err := s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Name, "third")
	if err != nil {
		t.Fatal(err)
	}
	step(t, s)
	if *calls != 3 {
		t.Fatalf("calls: %d", *calls)
	}
	if a, b, c := messageRow(t, s, first.ID), messageRow(t, s, second.ID), messageRow(t, s, third.ID); a.notices != 2 || c.notices != 1 ||
		b.notices != 1 || b.next != before.next || b.state != "notified" {
		t.Fatalf("rows: %+v %+v (was %+v) %+v", a, b, before, c)
	}
}

// A failed turn is recorded for every Codex recipient, also beyond the first 16 inboxes with
// pending notices (#250 review).
func TestTurnFailureBeyondSixteenInboxes(t *testing.T) {
	s, _, sender, receiver, message, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	countingWake(s, accepted())
	for i := 0; i < 17; i++ {
		other := join(t, s, "codex", "aaa-synthetic-"+strconv.Itoa(i), receiver.Directory)
		if _, err := s.Send(ctx, Key{sender.Family, sender.ID}, other.Name, "other"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		step(t, s)
	}
	if row := messageRow(t, s, message.ID); row.notices != 1 {
		t.Fatalf("target notice: %+v", row)
	}
	*clock = clock.Add(time.Second)
	if err := s.Observe(ctx, Observation{Caller: Key{receiver.Family, receiver.ID},
		Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: "idle", TurnError: "server_overloaded"}}); err != nil {
		t.Fatal(err)
	}
	step(t, s)
	if row := messageRow(t, s, message.ID); row.reason != "turn_failed:server_overloaded" {
		t.Fatalf("failure beyond 16 inboxes: %+v", row)
	}
}
