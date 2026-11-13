package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func receiptTestContext(s *Store, id, session, kind, target, body string) context.Context {
	sum := sha256.Sum256([]byte(kind + "\x00" + target + "\x00" + body))
	ctx, _ := withAudit(context.Background(), kind, target)
	return context.WithValue(ctx, operationContextKey{}, &OperationReceipt{Version: 1, ID: id, SessionID: session, Origin: "https://synthetic.example.ts.net", Type: kind, Target: target, Digest: hex.EncodeToString(sum[:]), CreatedAt: s.now().UnixMilli(), ExpiresAt: s.now().Add(operationReceiptWindow).UnixMilli(), State: "accepted", Code: "sent"})
}

func TestOperationReceiptMessageConcurrencyRestartAndConflict(t *testing.T) {
	s, root := testStore(t)
	ctx := context.Background()
	peer := join(t, s, "codex", "synthetic-receipt-recipient", "")
	const id = "synthetic-operation-one"
	var wg sync.WaitGroup
	results := make(chan Outcome, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := s.send(receiptTestContext(s, id, "browser-one", "send", peer.Name, "private synthetic text"), maintainerKey, peer.Name, "private synthetic text", true)
			results <- r
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first int64
	for r := range results {
		if first == 0 {
			first = r.ID
		}
		if r.ID != first {
			t.Fatalf("different mutation IDs %d and %d", first, r.ID)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, peer.ID); n != 1 {
		t.Fatalf("%d mutations", n)
	}
	p, e := operationReceipt(ctx, s.db, id)
	if e != nil || p == nil || p.SessionID != "browser-one" || !strings.Contains(p.ResultTarget, "message ") {
		t.Fatalf("receipt: %+v %v", p, e)
	}
	if strings.Contains(string(p.Result), "private synthetic text") {
		t.Fatal("receipt contains message body")
	}
	if e = s.db.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := openStore(root)
	if e != nil {
		t.Fatal("unchanged schema could not reopen", e)
	}
	defer reopened.db.Close()
	got, e := reopened.send(receiptTestContext(reopened, id, "browser-after-reauth", "send", peer.Name, "private synthetic text"), maintainerKey, peer.Name, "private synthetic text", true)
	if e != nil || got.ID != first {
		t.Fatalf("lost-response reconciliation after restart/reauth: %+v %v", got, e)
	}
	_, e = reopened.send(receiptTestContext(reopened, id, "browser-after-reauth", "send", peer.Name, "changed"), maintainerKey, peer.Name, "changed", true)
	if errorCode(e) != "idempotency_conflict" {
		t.Fatal("changed payload", e)
	}
	reopened.storage.mu.Lock()
	reopened.storage.blocked = "synthetic storage block"
	reopened.storage.mu.Unlock()
	if replay, e := reopened.send(receiptTestContext(reopened, id, "browser", "send", peer.Name, "private synthetic text"), maintainerKey, peer.Name, "private synthetic text", true); e != nil || replay.ID != first {
		t.Fatalf("retained result behind write block: %+v %v", replay, e)
	}
	reopened.storage.mu.Lock()
	reopened.storage.blocked = ""
	reopened.storage.mu.Unlock()
	if e = checkCatalog(reopened.db, schemaVersion); e != nil {
		t.Fatal("catalog compatibility", e)
	}
	if e = reopened.loadSettings(ctx); e != nil {
		t.Fatal("ordinary settings reader", e)
	}
	if at, e := reopened.settingsUpdatedAt(ctx); e != nil || at != 0 {
		t.Fatalf("receipt changed timing settings observation %d %v", at, e)
	}
}

func TestOperationReceiptRollbackIsAtomic(t *testing.T) {
	s, _ := testStore(t)
	peer := join(t, s, "codex", "synthetic-atomic-recipient", "")
	// Failure at receipt persistence occurs after the message insert, inside its transaction.
	if _, e := s.db.Exec(`CREATE TRIGGER synthetic_receipt_failure BEFORE INSERT ON settings WHEN NEW.name GLOB 'remote.v2.receipt:*' BEGIN SELECT RAISE(ABORT,'synthetic persistence failure'); END`); e != nil {
		t.Fatal(e)
	}
	_, e := s.send(receiptTestContext(s, "synthetic-atomic-one", "browser", "send", peer.Name, "synthetic body"), maintainerKey, peer.Name, "synthetic body", true)
	if e == nil {
		t.Fatal("storage failure reported success")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, peer.ID); n != 0 {
		t.Fatal("mutation survived receipt failure", n)
	}
	if p, e := operationReceipt(context.Background(), s.db, "synthetic-atomic-one"); e != nil || p != nil {
		t.Fatalf("partial receipt %+v %v", p, e)
	}
	if _, e = s.db.Exec(`DROP TRIGGER synthetic_receipt_failure`); e != nil {
		t.Fatal(e)
	}
}

func TestOperationReceiptRoomAndVerdict(t *testing.T) {
	s, _ := testStore(t)
	room := mustRoom(t, s, "synthetic receipt room")
	rctx := func() context.Context {
		return receiptTestContext(s, "synthetic-room-operation", "browser", "room-post", room, "synthetic post")
	}
	first, e := s.DashboardRoomPost(rctx(), room, "synthetic post", "synthetic-room-operation", s.clock()+operationReceiptWindow.Seconds())
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.DashboardRoomPost(rctx(), room, "synthetic post", "synthetic-room-operation", s.clock()+operationReceiptWindow.Seconds())
	if e != nil || again.Seq != first.Seq {
		t.Fatalf("room replay %+v %v", again, e)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM room_posts WHERE room_id=?`, room); n != 1 {
		t.Fatal("duplicate post", n)
	}
	peer := join(t, s, "claude", "synthetic-receipt-author", "")
	k := Key{peer.Family, peer.ID}
	mustCreate(t, s, k, "RECEIPT-1", decisionFields("Synthetic receipt decision", "decision"))
	vctx := func() context.Context {
		return receiptTestContext(s, "synthetic-verdict-operation", "browser", "decision-approve", "RECEIPT-1", "1")
	}
	verdict, e := s.DecisionVerdict(vctx(), "RECEIPT-1", 1, "approved", "")
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.DecisionVerdict(vctx(), "RECEIPT-1", 1, "approved", "")
	if e != nil || duplicate.ID != verdict.ID {
		t.Fatalf("verdict replay %+v %v", duplicate, e)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM decision_verdicts`); n != 1 {
		t.Fatal("duplicate verdict", n)
	}
	mustCreate(t, s, k, "RECEIPT-2", decisionFields("Synthetic conflicting decision", "decision"))
	cctx := func() context.Context {
		return receiptTestContext(s, "synthetic-conflict-operation", "browser", "decision-approve", "RECEIPT-2", "2")
	}
	_, e = s.DecisionVerdict(cctx(), "RECEIPT-2", 2, "approved", "")
	if errorCode(e) != "stale_revision" {
		t.Fatal(e)
	}
	p, e := operationReceipt(context.Background(), s.db, "synthetic-conflict-operation")
	if e != nil || p == nil || p.State != "conflict" || p.Code != "stale_revision" {
		t.Fatalf("conflict receipt %+v %v", p, e)
	}
	_, e = s.DecisionVerdict(cctx(), "RECEIPT-2", 2, "approved", "")
	if errorCode(e) != "stale_revision" {
		t.Fatal("conflict replay", e)
	}
}

func TestOperationReceiptExpiryAndLookupAuthentication(t *testing.T) {
	s, _ := testStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	peer := join(t, s, "codex", "synthetic-expiry-recipient", "")
	ctx := receiptTestContext(s, "synthetic-expiry-operation", "browser", "send", peer.Name, "synthetic")
	if _, e := s.send(ctx, maintainerKey, peer.Name, "synthetic", true); e != nil {
		t.Fatal(e)
	}
	now = now.Add(operationReceiptWindow)
	if _, e := s.send(ctx, maintainerKey, peer.Name, "synthetic", true); errorCode(e) != "retry_deadline_expired" {
		t.Fatal("expired retry", e)
	}
	d, _ := startTestDaemon(t)
	h, e := d.dashboardHandler()
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:47671/dashboard/receipts?id=synthetic-unknown-operation", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated receipt lookup", w.Code)
	}
	id, e := d.auth.start("dashboard")
	if e != nil {
		t.Fatal(e)
	}
	got := dashboardDo(t, d, "GET", "/dashboard/receipts?id=synthetic-unknown-operation", id, nil, nil)
	if got.status != http.StatusOK || !strings.Contains(got.body, `"authoritative":true`) || !strings.Contains(got.body, `"found":false`) {
		t.Fatalf("lookup %+v", got)
	}
}

func TestOperationReceiptSemanticDigestAndWindow(t *testing.T) {
	s, _ := testStore(t)
	d := &Daemon{store: s}
	now := s.now().UnixMilli()
	makeRequest := func(body string) *http.Request {
		form := url.Values{"operation_id": {"synthetic-form-operation"}, "operation_created_at": {strconv.FormatInt(now, 10)}, "body": {body}, "to": {"synthetic-target"}}
		form.Set("operation_created_at", strconv.FormatInt(now, 10))
		r := httptest.NewRequest("POST", "http://127.0.0.1/dashboard/actions/send", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.ParseForm()
		return r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, "https://synthetic.example.ts.net"))
	}
	a, e := d.operationContext(makeRequest("one"), context.Background(), "device-one", "send", "synthetic-target", "sent")
	if e != nil {
		t.Fatal(e)
	}
	b, e := d.operationContext(makeRequest("one"), context.Background(), "device-two", "send", "synthetic-target", "sent")
	if e != nil || operationOf(a).Digest != operationOf(b).Digest {
		t.Fatal("reauth changed digest", e)
	}
	c, e := d.operationContext(makeRequest("two"), context.Background(), "device-two", "send", "synthetic-target", "sent")
	if e != nil || operationOf(c).Digest == operationOf(a).Digest {
		t.Fatal("changed payload did not change digest", e)
	}
}

func TestOperationReceiptRemoteActionAndReauthentication(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.store.secretCost = 1000
	if _, e := d.changeSecret(context.Background(), "set", testSecret); e != nil {
		t.Fatal(e)
	}
	const origin = "https://synthetic.example.ts.net"
	h := d.remoteDashboardHandler(origin)
	call := func(method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:10001"+path, strings.NewReader(form.Encode()))
		r.Host = "synthetic.example.ts.net"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if method == "POST" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := func() *http.Cookie {
		w := call("POST", "/dashboard/login", url.Values{"secret": {testSecret}}, nil)
		if w.Code != 303 || len(w.Result().Cookies()) != 1 {
			t.Fatalf("login %d %s", w.Code, w.Body.String())
		}
		return w.Result().Cookies()[0]
	}
	cookie := login()
	peer := join(t, d.store, "codex", "synthetic-http-receipt", "")
	form := url.Values{"to": {peer.Name}, "body": {"synthetic HTTP operation"}, "operation_id": {"synthetic-http-operation"}, "operation_created_at": {strconv.FormatInt(d.store.now().UnixMilli(), 10)}, "csrf": {d.auth.csrf(cookie.Value)}}
	w := call("POST", "/dashboard/actions/send", form, cookie)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "notice=sent") {
		t.Fatalf("send %d %s", w.Code, w.Body.String())
	}
	original, e := operationReceipt(context.Background(), d.store.db, "synthetic-http-operation")
	if e != nil || original == nil || original.SessionID == "" || original.SessionID == cookie.Value {
		t.Fatalf("safe attribution %+v %v", original, e)
	}
	lookup := call("GET", "/dashboard/receipts?id=synthetic-http-operation", nil, cookie)
	if lookup.Code != 200 || !strings.Contains(lookup.Body.String(), `"found":true`) {
		t.Fatalf("lookup %d %s", lookup.Code, lookup.Body.String())
	}
	if e = d.auth.logout(cookie.Value); e != nil {
		t.Fatal(e)
	}
	cookie = login()
	form.Set("csrf", d.auth.csrf(cookie.Value))
	w = call("POST", "/dashboard/actions/send", form, cookie)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "notice=sent") {
		t.Fatalf("retry %d %s", w.Code, w.Body.String())
	}
	if n := count(t, d.store, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, peer.ID); n != 1 {
		t.Fatal("reauthentication duplicated mutation", n)
	}
	form.Set("body", "changed synthetic text")
	w = call("POST", "/dashboard/actions/send", form, cookie)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "notice=idempotency_conflict") {
		t.Fatalf("changed content %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestOperationReceiptHostAcceptanceWindowAndReclaimedID(t *testing.T) {
	s, _ := testStore(t)
	now := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	peer := join(t, s, "codex", "synthetic-host-clock-recipient", "")
	d := &Daemon{store: s}
	originalAt := now.Add(-time.Hour).UnixMilli()
	const id = "synthetic-host-clock-operation"
	makeContext := func(at int64) (context.Context, error) {
		t.Helper()
		form := url.Values{"operation_id": {id}, "operation_created_at": {strconv.FormatInt(at, 10)}, "to": {peer.Name}, "body": {"synthetic host-clock message"}}
		r := httptest.NewRequest("POST", "http://127.0.0.1/dashboard/actions/send", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		r = r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, "https://synthetic.example.ts.net"))
		ctx, _ := withAudit(context.Background(), "send", peer.Name)
		return d.operationContext(r, ctx, "browser", "send", peer.Name, "sent")
	}
	ctx, err := makeContext(originalAt)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.send(ctx, maintainerKey, peer.Name, "synthetic host-clock message", true)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := operationReceipt(context.Background(), s.db, id)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CreatedAt != now.UnixMilli() || receipt.ExpiresAt != now.Add(operationReceiptWindow).UnixMilli() {
		t.Fatalf("receipt lifetime did not use host acceptance: %+v", receipt)
	}
	// The original browser attempt is now seven days old, but the host's retained
	// receipt still has its last hour. Lookup must use the stored receipt first.
	now = now.Add(operationReceiptWindow - time.Hour)
	ctx, err = makeContext(originalAt)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.send(ctx, maintainerKey, peer.Name, "synthetic host-clock message", true)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("retained receipt window shortened: %+v %v", replay, err)
	}
	now = now.Add(time.Hour)
	ctx, err = makeContext(originalAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.send(ctx, maintainerKey, peer.Name, "synthetic host-clock message", true); errorCode(err) != "retry_deadline_expired" {
		t.Fatalf("accepted expired retained receipt: %v", err)
	}
	// Model normal reclamation. Keeping the original host timestamp prevents a
	// lost old result from becoming a second mutation after its row is gone.
	if _, err = s.db.Exec(`DELETE FROM settings WHERE name=?`, operationReceiptPrefix+id); err != nil {
		t.Fatal(err)
	}
	ctx, err = makeContext(originalAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.send(ctx, maintainerKey, peer.Name, "synthetic host-clock message", true); errorCode(err) != "retry_deadline_expired" {
		t.Fatalf("reclaimed ID revived: %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, peer.ID); n != 1 {
		t.Fatalf("old operation duplicated: %d", n)
	}
	if _, err = makeContext(now.Add(2 * time.Minute).UnixMilli()); err != ErrInvalid {
		t.Fatalf("unbounded future host timestamp: %v", err)
	}
}

func TestOperationReceiptFutureToleranceCannotReviveReclaimedID(t *testing.T) {
	s, _ := testStore(t)
	now := time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	peer := join(t, s, "codex", "synthetic-future-clock-recipient", "")
	originalAt := now.Add(30 * time.Second).UnixMilli()
	makeContext := func(id string, at int64) context.Context {
		t.Helper()
		d := &Daemon{store: s}
		form := url.Values{"operation_id": {id}, "operation_created_at": {strconv.FormatInt(at, 10)}, "to": {peer.Name}, "body": {"synthetic future-tolerance message"}}
		r := httptest.NewRequest("POST", "http://127.0.0.1/dashboard/actions/send", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		r = r.WithContext(context.WithValue(r.Context(), remoteOriginKey{}, "https://synthetic.example.ts.net"))
		ctx, _ := withAudit(context.Background(), "send", peer.Name)
		ctx, err := d.operationContext(r, ctx, "browser", "send", peer.Name, "sent")
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	const id = "synthetic-future-original-id"
	if _, err := s.send(makeContext(id, originalAt), maintainerKey, peer.Name, "synthetic future-tolerance message", true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(operationReceiptWindow)
	peer = join(t, s, "codex", "synthetic-future-clock-recipient", "")
	// A new action triggers ordinary reclamation exactly at visible receipt expiry.
	if _, err := s.send(makeContext("synthetic-future-cleanup-one", now.UnixMilli()), maintainerKey, peer.Name, "synthetic future-tolerance message", true); err != nil {
		t.Fatal(err)
	}
	if old, err := operationReceipt(context.Background(), s.db, id); err != nil || old == nil {
		t.Fatalf("future-tolerance tombstone reclaimed early: %+v %v", old, err)
	}
	if _, err := s.send(makeContext(id, originalAt), maintainerKey, peer.Name, "synthetic future-tolerance message", true); errorCode(err) != "retry_deadline_expired" {
		t.Fatalf("expired receipt replayed during future tolerance: %v", err)
	}
	recent, err := recentReceipts(context.Background(), s.db, s.now().UnixMilli(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range recent {
		if receipt.ID == id {
			t.Fatal("expired tombstone exposed as recent receipt")
		}
	}
	now = now.Add(30 * time.Second)
	if _, err := s.send(makeContext("synthetic-future-cleanup-two", now.UnixMilli()), maintainerKey, peer.Name, "synthetic future-tolerance message", true); err != nil {
		t.Fatal(err)
	}
	if old, err := operationReceipt(context.Background(), s.db, id); err != nil || old != nil {
		t.Fatalf("tombstone not reclaimed after original deadline: %+v %v", old, err)
	}
	if _, err := s.send(makeContext(id, originalAt), maintainerKey, peer.Name, "synthetic future-tolerance message", true); errorCode(err) != "retry_deadline_expired" {
		t.Fatalf("reclaimed ID replayed: %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, peer.ID); n != 3 {
		t.Fatalf("original ID duplicated mutation: %d", n)
	}
}
