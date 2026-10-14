package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func checkoutSession(t *testing.T, s *Store, id, directory string) (Key, MemoryCaller) {
	t.Helper()
	ctx := context.Background()
	k := Key{"codex", id}
	if _, err := s.Register(ctx, withLaunch(t, s, Registration{Family: k.Family, ID: k.ID, Repository: directory, Directory: directory, TTLSeconds: 3600})); err != nil {
		t.Fatal(err)
	}
	m, err := s.ResolveWorkCaller(ctx, k, nil)
	if err != nil {
		t.Fatal(err)
	}
	return k, m
}

func TestCheckoutRoleRequestAndAtomicHandoff(t *testing.T) {
	checkout, linked := checkoutFixture(t)
	s, _ := testStore(t)
	ctx := context.Background()
	a, ma := checkoutSession(t, s, "synthetic-writer", checkout.Directory)
	b, mb := checkoutSession(t, s, "synthetic-requester", checkout.Directory)
	_, foreign := checkoutSession(t, s, "synthetic-foreign", testRepo(t))
	retired, retiredCaller := checkoutSession(t, s, "synthetic-retired", checkout.Directory)
	var retiredRevision int64
	if err := s.db.QueryRow(`SELECT revision FROM sessions WHERE family=? AND id=?`, retired.Family, retired.ID).Scan(&retiredRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Mutate(ctx, Mutation{Family: retired.Family, ID: retired.ID, IfRevision: retiredRevision}, true); err != nil {
		t.Fatal(err)
	}
	status, err := s.CheckoutStatus(ctx, b, checkout.Directory)
	if err != nil || status.State != "unclaimed" || status.Writer != nil {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_stores`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("status created a store: %d %v", count, err)
	}
	wa, wb := create(t, s, ma, "writer task"), create(t, s, mb, "requester task")
	gen := mustStart(t, s, ma, wa, map[string]any{"resources": [][2]string{checkout.Resource}})
	status, err = s.CheckoutStatus(ctx, b, checkout.Directory)
	if err != nil || status.State != "held" || status.Writer == nil || status.Writer.Generation != gen || status.Writer.Peer != ma.Name || !status.Writer.LeaseValid {
		t.Fatalf("writer token: %+v %v", status, err)
	}
	if other, err := s.CheckoutStatus(ctx, b, linked.Directory); err != nil || other.Writer != nil {
		t.Fatalf("linked checkout leaked role: %+v %v", other, err)
	}
	request, err := s.RequestCheckout(ctx, b, checkout.Directory, "ready to drive")
	if err != nil || request["message"].(Outcome).Recipient != ma.Name {
		t.Fatalf("request: %v %v", request, err)
	}
	inbox, err := s.ReadInbox(ctx, a, 0, 10)
	if err != nil || len(inbox.Messages) != 1 || inbox.Messages[0].SenderName != mb.Name || !strings.Contains(inbox.Messages[0].Body, "checkout_writer_request") {
		t.Fatalf("request inbox: %+v %v", inbox, err)
	}
	if _, err := start(s, mb, wb, 1, map[string]any{"resources": [][2]string{checkout.Resource}}); wcode(err) != "claim_conflict" {
		t.Fatalf("request transferred ownership: %v", err)
	}
	fields := map[string]any{"work_id": wa, "if_revision": revision(t, s, ma, wa), "claim_generation": gen,
		"checkpoint": "committed checkpoint; ready for pickup", "checkout_resource": checkout.Resource[1], "handoff_to": mb.Name,
		"key": key(), "deadline": now(s) + 600}
	var alias string
	if err := s.db.QueryRow(`SELECT name FROM names WHERE kind='alias' AND repository=? AND family='codex'`, ma.Repository).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	// Invalid recipients, aliases and unrelated resources never partially release.
	for _, change := range []map[string]any{{"handoff_to": foreign.Name}, {"handoff_to": alias},
		{"handoff_to": ma.Name}, {"handoff_to": retiredCaller.Name}, {"handoff_to": "missing-synthetic-peer"}, {"checkout_resource": linked.Resource[1]}, {"claim_generation": gen + 1}} {
		bad := map[string]any{}
		for name, value := range fields {
			bad[name] = value
		}
		for name, value := range change {
			bad[name] = value
		}
		if _, err := work(s, ma, "work-release", bad); err == nil {
			t.Fatalf("accepted %v", change)
		}
		if item := get(t, s, ma, wa); item["current_claim"] == nil || item["revision"] != float64(fields["if_revision"].(int64)) {
			t.Fatalf("partial handoff: %v", item)
		}
	}
	result := mustWork(t, s, ma, "work-release", fields)
	if result["role_released"] != true || result["message"].(map[string]any)["recipient"] != mb.Name {
		t.Fatalf("handoff: %v", result)
	}
	status, err = s.CheckoutStatus(ctx, b, checkout.Directory)
	if err != nil || status.State != "released" || status.Writer.LeaseValid || status.Writer.Checkpoint != fields["checkpoint"] {
		t.Fatalf("released status: %+v %v", status, err)
	}
	if item := get(t, s, mb, wb); item["current_claim"] != nil {
		t.Fatal("notification automatically accepted the role")
	}
	newGen := mustStart(t, s, mb, wb, map[string]any{"resources": [][2]string{checkout.Resource}})
	if newGen == gen {
		t.Fatal("pickup reused the previous writer's token")
	}
	status, err = s.CheckoutStatus(ctx, a, checkout.Directory)
	if err != nil || status.Writer.Generation != newGen || status.Writer.Peer != mb.Name || status.State != "held" {
		t.Fatalf("new writer: %+v %v", status, err)
	}
	duplicate := mustWork(t, s, ma, "work-release", fields)
	if duplicate["duplicate"] != true || duplicate["message"].(map[string]any)["id"] != result["message"].(map[string]any)["id"] {
		t.Fatalf("handoff replay: %v", duplicate)
	}
	inbox, err = s.ReadInbox(ctx, b, 0, 10)
	if err != nil || len(inbox.Messages) != 1 {
		t.Fatalf("duplicate notification: %+v %v", inbox, err)
	}
	var notice map[string]any
	if json.Unmarshal([]byte(inbox.Messages[0].Body), &notice) != nil || notice["type"] != "checkout_writer_handoff" || notice["claim_generation"] != float64(gen) {
		t.Fatalf("handoff notice: %v", notice)
	}
	delete(fields, "key")
	delete(fields, "deadline")
	if _, err := work(s, ma, "work-release", fields); err == nil {
		t.Fatal("stale writer token released the new writer")
	}
}

func TestCheckoutRoleExpiryAndUnaddressableWriter(t *testing.T) {
	checkout, _ := checkoutFixture(t)
	s, _ := testStore(t)
	clock := s.now()
	s.now = func() time.Time { return clock }
	ctx := context.Background()
	a, ma := checkoutSession(t, s, "synthetic-a", checkout.Directory)
	b, _ := checkoutSession(t, s, "synthetic-b", checkout.Directory)
	id := create(t, s, ma, "expires")
	mustStart(t, s, ma, id, map[string]any{"resources": [][2]string{checkout.Resource}, "lease_seconds": 60})
	if _, err := s.RequestCheckout(ctx, a, checkout.Directory, "self"); err == nil {
		t.Fatal("self request accepted")
	}
	if _, err := s.CheckoutStatus(ctx, b, testRepo(t)); wcode(err) != "repo_unresolved" {
		t.Fatalf("foreign checkout accepted: %v", err)
	}
	clock = clock.Add(61 * time.Second)
	for _, maintain := range []bool{false, true} {
		if maintain {
			if err := s.MaintainWork(ctx); err != nil {
				t.Fatal(err)
			}
		}
		status, err := s.CheckoutStatus(ctx, b, checkout.Directory)
		if err != nil || !maintain && (status.State != "expired" || status.Writer.LeaseValid) || maintain && status.State != "unclaimed" {
			t.Fatalf("expiry status: %+v %v", status, err)
		}
		if _, err := s.RequestCheckout(ctx, b, checkout.Directory, "request"); wcode(err) != "checkout_unheld" {
			t.Fatalf("expiry request: %v", err)
		}
	}
	custom := ma
	custom.Consumer = "synthetic-stable-consumer"
	id = create(t, s, custom, "custom consumer")
	mustStart(t, s, custom, id, map[string]any{"resources": [][2]string{checkout.Resource}})
	if _, err := s.RequestCheckout(ctx, b, checkout.Directory, "request"); wcode(err) != "writer_unaddressable" {
		t.Fatalf("guessed custom consumer recipient: %v", err)
	}
}

func TestCheckoutHandoffPreservesFundedPlainRelease(t *testing.T) {
	checkout, _ := checkoutFixture(t)
	s, _ := testStore(t)
	_, ma := checkoutSession(t, s, "synthetic-writer", checkout.Directory)
	b, mb := checkoutSession(t, s, "synthetic-requester", checkout.Directory)
	id := create(t, s, ma, "bounded handoff")
	gen := mustStart(t, s, ma, id, map[string]any{"resources": [][2]string{checkout.Resource}})
	s.storage.maxPages = count(t, s, `PRAGMA page_count`) + reservePages + commitSlack + mustDebt(t, s) - 1
	fields := map[string]any{"work_id": id, "if_revision": revision(t, s, ma, id), "claim_generation": gen,
		"checkpoint": "stopped; plain release if notification cannot fit", "handoff_to": mb.Name, "checkout_resource": checkout.Resource[1]}
	if _, err := work(s, ma, "work-release", fields); wcode(err) != "capacity" {
		t.Fatalf("handoff used funded control capacity: %v", err)
	}
	if item := get(t, s, ma, id); item["current_claim"] == nil || item["revision"] != float64(fields["if_revision"].(int64)) {
		t.Fatalf("capacity refusal partially released: %v", item)
	}
	delete(fields, "handoff_to")
	delete(fields, "checkout_resource")
	mustWork(t, s, ma, "work-release", fields)
	if inbox, err := s.ReadInbox(context.Background(), b, 0, 10); err != nil || len(inbox.Messages) != 0 {
		t.Fatalf("refusal left a notification: %+v %v", inbox, err)
	}
}
