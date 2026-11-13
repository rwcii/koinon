package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAttentionBrowserCursorsDoNotAcknowledge(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, recipient := range []string{"maintainer", "codex"} {
		_, err := s.db.Exec(`INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,created_at,delivery_updated_at) VALUES(?, 'attention-test',1,'codex','sender','sender','private text',1000,1000)`, recipient)
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.attention(ctx, "phone-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.DirectUnread != 1 || len(first.Messages) != 1 || !strings.Contains(first.Messages[0].Href, "#message-") {
		t.Fatalf("initial projection: %+v", first)
	}
	if err = s.markAttentionRead(ctx, "phone-a", first.DirectHead, first.RoomHead); err != nil {
		t.Fatal(err)
	}
	after, err := s.attention(ctx, "phone-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.attention(ctx, "phone-b")
	if err != nil {
		t.Fatal(err)
	}
	if after.DirectUnread != 0 || other.DirectUnread != 1 {
		t.Fatalf("browser isolation: %d / %d", after.DirectUnread, other.DirectUnread)
	}
	var state string
	if err = s.db.QueryRow(`SELECT delivery_state FROM messages WHERE recipient_family='codex'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" {
		t.Fatalf("agent delivery changed: %s", state)
	}
	// Reading again cannot advance the cursor or erase a later record.
	if _, err = s.db.Exec(`INSERT INTO messages(recipient_family,recipient_id,seq,sender_family,sender_id,sender_name,body,created_at,delivery_updated_at) VALUES('maintainer','attention-test',2,'codex','sender','sender','later',2000,2000)`); err != nil {
		t.Fatal(err)
	}
	if err = s.markAttentionRead(ctx, "phone-a", first.DirectHead, 0); err != nil {
		t.Fatal(err)
	}
	later, err := s.attention(ctx, "phone-a")
	if err != nil {
		t.Fatal(err)
	}
	if later.DirectUnread != 1 {
		t.Fatalf("late message lost: %+v", later)
	}
}

func TestAttentionViewAndProtectedRead(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	cookie := dashboardLogin(t, d, secret)
	page := dashboardDo(t, d, "GET", "/dashboard/attention", cookie, nil, nil)
	if page.status != 200 || !strings.Contains(page.body, "unknown or stale activity") || !strings.Contains(page.body, "Mark displayed snapshot read") {
		t.Fatalf("attention page: %d %s", page.status, page.body)
	}
	refused := dashboardDo(t, d, "POST", "/dashboard/actions/attention-read", cookie, nil, url.Values{"direct": {"0"}, "room": {"0"}})
	if refused.status != http.StatusForbidden {
		t.Fatalf("missing CSRF accepted: %d", refused.status)
	}
}

func TestAttentionCountsAndUnknownObservations(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	agent := join(t, s, "codex", "attention-observation", "")
	mustCreate(t, s, Key{"codex", agent.ID}, "ATTENTION-1", decisionFields("An actual choice", "decision"))
	mustCreate(t, s, Key{"codex", agent.ID}, "ATTENTION-2", decisionFields("Information only", "fyi"))
	if _, err := s.db.Exec(`INSERT INTO room_posts(room_id,seq,sender_family,sender_id,sender_name,origin,body,created_at) VALUES('synthetic-room',1,'codex','sender','sender','member','private text',1000)`); err != nil {
		t.Fatal(err)
	}
	data, err := s.attention(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if data.Waiting != 1 || len(data.Decisions) != 1 || data.Decisions[0].Label != "ATTENTION-1" || data.RoomUnread != 1 || data.UnknownActivities != 1 {
		t.Fatalf("projection: %+v", data)
	}
	if err = s.markAttentionRead(ctx, "phone", 0, data.RoomHead); err != nil {
		t.Fatal(err)
	}
	after, err := s.attention(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	if after.Waiting != 1 || after.RoomUnread != 0 {
		t.Fatalf("read resolved unrelated state: %+v", after)
	}
	var state string
	if err = s.db.QueryRow(`SELECT state FROM decision_items WHERE reference='ATTENTION-1'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "active" {
		t.Fatalf("read resolved decision: %s", state)
	}
}

func TestAttentionRejectsInvalidReceiptVersions(t *testing.T) {
	for _, raw := range []string{`{"version":99,"id":"unsupported"}`, `{not-json`} {
		t.Run(raw, func(t *testing.T) {
			s, _ := testStore(t)
			if _, err := s.db.Exec(`INSERT INTO settings(name,value,updated_at) VALUES('remote.v2.receipt:invalid',?,?)`, raw, s.now().UnixMilli()+10000); err != nil {
				t.Fatal(err)
			}
			if _, err := s.attention(context.Background(), "phone"); err == nil {
				t.Fatal("invalid receipt displayed as an outcome")
			}
		})
	}
	// A supported retained receipt remains visible.
	s, _ := testStore(t)
	receipt := OperationReceipt{Version: 1, ID: "valid-receipt", State: "accepted", CreatedAt: s.now().UnixMilli(), ExpiresAt: s.now().UnixMilli() + 10000}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO settings(name,value,updated_at) VALUES('remote.v2.receipt:valid',?,?)`, string(raw), receipt.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	data, err := s.attention(context.Background(), "phone")
	if err != nil || len(data.Receipts) != 1 || data.Receipts[0].ID != receipt.ID {
		t.Fatalf("valid receipt: %+v %v", data, err)
	}
}

func TestAttentionReadCursorRespectsStorageAdmission(t *testing.T) {
	s, _ := testStore(t)
	s.storage.mu.Lock()
	s.storage.blocked = "synthetic blocked store"
	s.storage.mu.Unlock()
	if err := s.markAttentionRead(context.Background(), "synthetic-browser", 0, 0); err == nil {
		t.Fatal("browser read cursor bypassed blocked storage admission")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE name='remote.v2.attention:synthetic-browser'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("blocked read cursor was persisted")
	}
}

// A browser's read position is kept after its last mark for attentionCursorLife, then
// deleted, so ended logins leave no permanent rows (#673 review M7).
func TestAttentionReadPositionsExpire(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	clock := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	if err := s.markAttentionRead(ctx, "ended-browser", 0, 0); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(attentionCursorLife - time.Minute)
	if err := s.markAttentionRead(ctx, "live-browser", 0, 0); err != nil {
		t.Fatal(err)
	}
	count := func(name string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM settings WHERE name=?`, "remote.v2.attention:"+name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("ended-browser") != 1 {
		t.Fatal("a read position inside its life was deleted")
	}
	clock = clock.Add(2 * time.Minute)
	if err := s.markAttentionRead(ctx, "live-browser", 0, 0); err != nil {
		t.Fatal(err)
	}
	if count("ended-browser") != 0 || count("live-browser") != 1 {
		t.Fatalf("expired position kept or live one lost: ended %d live %d", count("ended-browser"), count("live-browser"))
	}
}
