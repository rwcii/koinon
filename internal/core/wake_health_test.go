package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestWakePassFaultClassificationRecoveryAndPrivacy(t *testing.T) {
	s, _ := testStore(t)
	clock := time.Unix(100, 0)
	s.now = func() time.Time { return clock }
	var events []map[string]any
	d := &Daemon{store: s, logEvent: func(event string, fields map[string]any) {
		fields["event"] = event
		events = append(events, fields)
	}}
	const sensitive = "synthetic_native_id_and_message_body"
	var ignored int
	dbErr := s.db.QueryRow(`SELECT * FROM ` + sensitive).Scan(&ignored)
	if dbErr == nil {
		t.Fatal("missing-table error not injected")
	}
	ctx := context.Background()
	d.recordWakePass(ctx, wakePassError{"list_due", dbErr})
	health, err := s.WakeHealth(ctx)
	if err != nil || health["fault"] != "sqlite_error" || len(events) != 1 || events[0]["sqlite_code"] != 1 || events[0]["fault_at"] != clock.UnixMilli() {
		t.Fatalf("fault: %v events %v error %v", health, events, err)
	}
	clock = clock.Add(time.Second)
	d.recordWakePass(ctx, wakePassError{"list_due", dbErr})
	if len(events) != 1 {
		t.Fatal("identical failure flooded the log")
	}
	d.recordWakePass(ctx, nil)
	health, err = s.WakeHealth(ctx)
	last := health["last_fault"].(*wakeFault)
	if err != nil || health["fault"] != "" || last.Class != "sqlite_error" || last.At != clock.UnixMilli() || len(events) != 2 || events[1]["event"] != "wake_recovered" {
		t.Fatalf("recovery: %v events %v error %v", health, events, err)
	}
	// An unknown error keeps diagnostic types without exposing its string.
	d.recordWakePass(ctx, fmt.Errorf("%s: %w", sensitive, errors.New(sensitive)))
	health, err = s.WakeHealth(ctx)
	if err != nil || health["fault"] != "wake_pass_error" || len(events) != 3 || len(health["last_fault"].(*wakeFault).ErrorTypes) < 2 {
		t.Fatalf("unknown error: %v %v", health, err)
	}
	data, _ := json.Marshal(map[string]any{"events": events, "health": health})
	if strings.Contains(string(data), sensitive) || strings.Contains(string(data), "no such table") {
		t.Fatalf("raw error content in diagnostics: %s", data)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	d.recordWakePass(cancelled, context.Canceled)
	if len(events) != 3 {
		t.Fatal("shutdown manufactured a fault")
	}
	for _, check := range []struct {
		err   error
		class string
	}{{ErrCapacity, "capacity"}, {ErrStorageBlocked, "storage_blocked"}, {context.DeadlineExceeded, "timeout"}} {
		fault := classifyWakeFault(wakePassError{"submit_wake", check.err}, clock.UnixMilli())
		if fault.Class != check.class || fault.Stage != "submit_wake" || !errors.Is(wakePassError{"submit_wake", check.err}, check.err) {
			t.Fatalf("classification: %+v", fault)
		}
	}
}

func TestWakeMaintenanceRecordsFailureThenSuccessfulPass(t *testing.T) {
	s, _ := testStore(t)
	events := make(chan string, 4)
	passes := 0
	d := &Daemon{store: s, stop: make(chan struct{}), logEvent: func(event string, _ map[string]any) { events <- event }}
	d.wakePass = func(ctx context.Context) error {
		passes++
		if passes == 1 {
			return wakePassError{"list_due", ErrStorageBlocked}
		}
		return s.wakeStep(ctx)
	}
	d.wg.Add(1)
	go d.maintainWake()
	t.Cleanup(func() { close(d.stop); d.wg.Wait() })
	for _, want := range []string{"wake_fault", "wake_recovered"} {
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("missing %s", want)
		}
	}
	health, err := s.WakeHealth(context.Background())
	if err != nil || health["fault"] != "" || health["last_fault"].(*wakeFault).Class != "storage_blocked" {
		t.Fatalf("maintenance health: %v %v", health, err)
	}
}
