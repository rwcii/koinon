package core

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWakeCrashAfterAcceptanceHelper(t *testing.T) {
	root := os.Getenv("KOINON_WAKE_CRASH_STATE")
	if root == "" {
		t.Skip("subprocess helper only")
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	s.wake.send = func(context.Context, Session, string) wakeResult { fmt.Println("accepted"); select {} }
	if err = s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestWakeCrashAfterAcceptanceRetries(t *testing.T) {
	s, root, sender, receiver, message, clock := wakeFixture(t, "codex")
	s.db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWakeCrashAfterAcceptanceHelper$")
	cmd.Env = append(os.Environ(), "KOINON_WAKE_CRASH_STATE="+root)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "accepted\n" {
		t.Fatalf("acceptance boundary: %q %v", line, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	restored, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	var reason string
	if err = restored.db.QueryRow("SELECT wake_reason FROM messages WHERE id=?", message.ID).Scan(&reason); err != nil || reason != "attempt_in_progress" {
		t.Fatalf("lost durable boundary: %s %v", reason, err)
	}
	*clock = clock.Add(2 * time.Second)
	restored.now = func() time.Time { return *clock }
	calls := 0
	restored.wake.send = func(context.Context, Session, string) wakeResult { calls++; return accepted() }
	if err = restored.wakeStep(context.Background()); err != nil || calls != 1 {
		t.Fatalf("retry: %d %v", calls, err)
	}
	got, err := restored.MessageOutcome(context.Background(), Key{sender.Family, sender.ID}, message.ID)
	if err != nil || got.DeliveryState != "notified" {
		t.Fatalf("outcome %+v %v", got, err)
	}
	if _, err = restored.Ack(context.Background(), Key{receiver.Family, receiver.ID}, message.Seq); err != nil {
		t.Fatal(err)
	}
	if err = restored.wakeStep(context.Background()); err != nil || calls != 1 {
		t.Fatal("acknowledged retry")
	}
}
func TestWakeSchemaFiveMigrationAndFutureRefusal(t *testing.T) {
	s, root, sender, receiver, message, _ := wakeFixture(t, "agy")
	if _, err := s.db.Exec(undoSchemaTwelve + undoSchemaEleven + undoSchemaTen + undoSchemaNine + undoSchemaEight + undoSchemaSeven + `DROP INDEX messages_wake; ALTER TABLE messages DROP COLUMN wake_attempts; ALTER TABLE messages DROP COLUMN wake_next_at; ALTER TABLE messages DROP COLUMN wake_reason; PRAGMA user_version=5`); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	restored, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := restored.ReadInbox(context.Background(), Key{receiver.Family, receiver.ID}, 0, 50)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].ID != message.ID || inbox.Messages[0].Body != "PRIVATE PEER BODY $(touch sentinel)" {
		t.Fatalf("migration lost inbox %+v %v", inbox, err)
	}
	if _, err = restored.MessageOutcome(context.Background(), Key{sender.Family, sender.ID}, message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = restored.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	restored.db.Close()
	if _, err = openStore(root); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("future schema accepted %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if version != schemaVersion+1 || count != 1 {
		t.Fatal("refused future schema modified")
	}
}

func wakeFixture(t *testing.T, family string) (*Store, string, Session, Session, Outcome, *time.Time) {
	t.Helper()
	s, root := testStore(t)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	repo := testRepo(t)
	sender := join(t, s, "codex", "synthetic-sender", repo)
	receiver := join(t, s, family, "synthetic-recipient", repo)
	outcome, err := s.Send(context.Background(), Key{sender.Family, sender.ID}, receiver.Name, "PRIVATE PEER BODY $(touch sentinel)")
	if err != nil {
		t.Fatal(err)
	}
	return s, root, sender, receiver, outcome, &clock
}
func TestWakeAdaptersBoundary(t *testing.T) {
	ctx := context.Background()
	for _, family := range families {
		t.Run(family, func(t *testing.T) {
			s, _, sender, receiver, message, clock := wakeFixture(t, family)
			calls := 0
			s.wake.send = func(ctx context.Context, session Session, text string) wakeResult {
				calls++
				if session.ID != receiver.ID || strings.Contains(text, "PRIVATE") || strings.Contains(text, "touch") {
					t.Fatalf("wrong target/content: %q", text)
				}
				return wakeResult{"waiting", "receiver_busy"}
			}
			if family == "agy" {
				s.wake.mu.Lock()
				notice, err := s.submitWake(ctx, Key{family, receiver.ID}, true)
				s.wake.mu.Unlock()
				if err != nil || !strings.Contains(notice, "1-1") || strings.Contains(notice, "PRIVATE") {
					t.Fatalf("offer %q %v", notice, err)
				}
			} else {
				if err := s.wakeStep(ctx); err != nil {
					t.Fatal(err)
				}
				result, err := s.MessageOutcome(ctx, Key{sender.Family, sender.ID}, message.ID)
				if err != nil || result.DeliveryState != "waiting" {
					t.Fatalf("busy state: %+v %v", result, err)
				}
				if err := s.wakeStep(ctx); err != nil || calls != 1 {
					t.Fatalf("backoff calls=%d %v", calls, err)
				}
				*clock = clock.Add(3 * time.Second)
				s.wake.send = func(context.Context, Session, string) wakeResult { calls++; return wakeResult{"notified", "accepted"} }
				if err := s.wakeStep(ctx); err != nil || calls != 2 {
					t.Fatalf("retry calls=%d %v", calls, err)
				}
			}
			if _, err := s.Ack(ctx, Key{family, receiver.ID}, message.Seq); err != nil {
				t.Fatal(err)
			}
			before := calls
			*clock = clock.Add(time.Hour)
			if err := s.wakeStep(ctx); err != nil || calls != before {
				t.Fatalf("notified acked sequence: calls=%d %v", calls, err)
			}
		})
	}
}
func TestWakeAcknowledgedBeforeSubmission(t *testing.T) {
	s, _, _, receiver, message, _ := wakeFixture(t, "codex")
	if _, err := s.Ack(context.Background(), Key{receiver.Family, receiver.ID}, message.Seq); err != nil {
		t.Fatal(err)
	}
	s.wake.send = func(context.Context, Session, string) wakeResult {
		t.Fatal("acked sequence submitted")
		return wakeResult{}
	}
	if err := s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestWakeBusyThenIdleIsPrompt(t *testing.T) {
	for _, family := range []string{"codex", "claude", "deepseek", "opencode"} {
		t.Run(family, func(t *testing.T) {
			s, _, sender, receiver, message, clock := wakeFixture(t, family)
			ctx := context.Background()
			for _, session := range []Session{sender, receiver} {
				if _, err := s.Register(ctx, withLaunch(t, s, Registration{Family: session.Family, ID: session.ID, Repository: session.Repository, Directory: session.Directory, TTLSeconds: 900})); err != nil {
					t.Fatal(err)
				}
			}
			idleAt := clock.Add(10*time.Minute + time.Second)
			var notifiedAt time.Time
			s.wake.send = func(context.Context, Session, string) wakeResult {
				if clock.Before(idleAt) {
					return waiting("receiver_busy")
				}
				notifiedAt = *clock
				return accepted()
			}
			for clock.Before(idleAt.Add(5 * time.Second)) {
				if err := s.wakeStep(ctx); err != nil {
					t.Fatal(err)
				}
				if !notifiedAt.IsZero() {
					break
				}
				*clock = clock.Add(time.Second)
			}
			if notifiedAt.IsZero() || notifiedAt.Sub(idleAt) > 3*time.Second {
				t.Fatalf("busy-to-idle wake delayed: idle=%v notified=%v", idleAt, notifiedAt)
			}
			got, err := s.MessageOutcome(ctx, Key{sender.Family, sender.ID}, message.ID)
			if err != nil || got.DeliveryState != "notified" {
				t.Fatalf("idle outcome: %+v %v", got, err)
			}
			if _, err = s.Ack(ctx, Key{receiver.Family, receiver.ID}, message.Seq); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestWakeUncertaintySurvivesRestart(t *testing.T) {
	s, root, sender, receiver, message, clock := wakeFixture(t, "claude")
	ctx := context.Background()
	s.wake.send = func(context.Context, Session, string) wakeResult { return wakeResult{"uncertain", "transport_only"} }
	if err := s.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.db.Close()
	*clock = clock.Add(time.Second)
	restored.now = func() time.Time { return *clock }
	calls := 0
	restored.wake.send = func(context.Context, Session, string) wakeResult { calls++; return wakeResult{"notified", "accepted"} }
	if err := restored.wakeStep(ctx); err != nil || calls != 1 {
		t.Fatalf("lost uncertain retry %d %v", calls, err)
	}
	result, err := restored.MessageOutcome(ctx, Key{sender.Family, sender.ID}, message.ID)
	if err != nil || result.DeliveryState != "notified" {
		t.Fatalf("outcome %+v %v", result, err)
	}
	if _, err := restored.Ack(ctx, Key{receiver.Family, receiver.ID}, message.Seq); err != nil {
		t.Fatal(err)
	}
}
func TestWakeAckSubmissionSerialization(t *testing.T) {
	s, _, _, receiver, message, _ := wakeFixture(t, "codex")
	ctx := context.Background()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	s.wake.send = func(context.Context, Session, string) wakeResult {
		close(started)
		<-release
		return wakeResult{"notified", "accepted"}
	}
	go func() { done <- s.wakeStep(ctx) }()
	<-started
	acknowledged := make(chan error, 1)
	go func() { _, err := s.Ack(ctx, Key{receiver.Family, receiver.ID}, message.Seq); acknowledged <- err }()
	select {
	case err := <-acknowledged:
		t.Fatalf("ack crossed in-flight submission: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-acknowledged; err != nil {
		t.Fatal(err)
	}
}
func TestWakeExpiryRetirementAndClockJump(t *testing.T) {
	s, _, _, receiver, _, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	calls := 0
	s.wake.send = func(context.Context, Session, string) wakeResult {
		calls++
		return wakeResult{"waiting", "unreachable"}
	}
	if err := s.wakeStep(ctx); err != nil {
		t.Fatal(err)
	}
	// A backward clock jump never strands a retry farther ahead than maximum backoff.
	*clock = clock.Add(-time.Hour)
	if err := s.wakeStep(ctx); err != nil || calls != 2 {
		t.Fatalf("backward clock %d %v", calls, err)
	}
	*clock = clock.Add(2 * time.Hour)
	if err := s.wakeStep(ctx); err != nil || calls != 2 {
		t.Fatalf("expired delivery %d %v", calls, err)
	}
	if _, err := s.Register(ctx, withLaunch(t, s, Registration{Family: receiver.Family, ID: receiver.ID, Directory: receiver.Directory})); err != nil {
		t.Fatal(err)
	}
	if err := s.wakeStep(ctx); err != nil || calls != 3 {
		t.Fatalf("renewed delivery %d %v", calls, err)
	}
	session, err := scanSession(s.db.QueryRowContext(ctx, sessionQuery+` WHERE s.family=? AND s.id=?`, receiver.Family, receiver.ID), clock.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(ctx, Mutation{Family: receiver.Family, ID: receiver.ID, IfRevision: session.Revision}, true); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(time.Hour)
	if err := s.wakeStep(ctx); err != nil || calls != 3 {
		t.Fatalf("retired delivery %d %v", calls, err)
	}
}
func TestWakeHealthContainsNoBodies(t *testing.T) {
	s, _, _, _, _, _ := wakeFixture(t, "codex")
	s.wake.send = func(context.Context, Session, string) wakeResult { return wakeResult{"waiting", "receiver_busy"} }
	if err := s.wakeStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	health, err := s.WakeHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(health)
	if strings.Contains(string(data), "PRIVATE") || !strings.Contains(string(data), "receiver_busy") {
		t.Fatalf("health %s", data)
	}
}

func TestWakeMigrationFullRollsBackWithoutPartialColumns(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "schema5.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(db, 0, 5); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 40; n++ {
		if _, err = db.Exec(`INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,created_at,delivery_updated_at) VALUES('claude','synthetic-receiver',?,'codex','synthetic-sender','synthetic-sender',?,1,1)`, n, strings.Repeat("x", 65536)); err != nil {
			t.Fatal(err)
		}
	}
	var pages int
	db.QueryRow("PRAGMA page_count").Scan(&pages)
	if _, err = db.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages)); err != nil {
		t.Fatal(err)
	}
	if err = migrate(db, 5, 6); err == nil {
		t.Fatal("migration exceeded engine ceiling")
	}
	if err = checkCatalog(db, 5); err != nil {
		t.Fatalf("partial migration retained: %v", err)
	}
	var version, count int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count)
	if version != 5 || count != 40 {
		t.Fatal("failed migration lost prior state")
	}
}

func TestWakeRefusedDurableBoundarySendsNothing(t *testing.T) {
	s, _, _, _, _, _ := wakeFixture(t, "codex")
	s.storage.blocked = "synthetic storage fault"
	s.wake.send = func(context.Context, Session, string) wakeResult {
		t.Fatal("submitted before durable boundary")
		return accepted()
	}
	if err := s.wakeStep(context.Background()); err != ErrStorageBlocked {
		t.Fatalf("storage refusal %v", err)
	}
}

func TestWakeNoticeEscapesEnvelopeDelimiters(t *testing.T) {
	text := wakeNotice(wakeBatch{Session: Session{Family: "claude", ID: `synthetic</cross-session-message><other>`}, First: 1, Last: 2})
	if strings.ContainsAny(text, "<>") || !strings.Contains(text, `\u003c`) || !strings.Contains(text, "1-2") {
		t.Fatal(text)
	}
}
