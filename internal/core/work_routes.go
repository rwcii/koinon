package core

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// workRoutes adds the work API: one route per wire operation. The store is the caller's
// recorded repository; the consumer is the caller's session key unless the request names
// a stable consumer.
func (d *Daemon) workRoutes(mux *http.ServeMux) {
	for _, op := range WorkOperations() {
		mux.HandleFunc("POST /v1/work/"+op, func(w http.ResponseWriter, r *http.Request) {
			var fields map[string]json.RawMessage
			// Escaping can grow the bounded text fields sixfold.
			if err := decodeLimit(w, r, &fields, 128<<10); err != nil {
				failure(w, err)
				return
			}
			var caller Key
			if json.Unmarshal(fields["caller"], &caller) != nil {
				failure(w, ErrInvalid)
				return
			}
			var consumer *string
			if raw, ok := fields["consumer"]; ok {
				if json.Unmarshal(raw, &consumer) != nil || consumer == nil {
					failure(w, ErrInvalid)
					return
				}
			}
			delete(fields, "caller")
			delete(fields, "consumer")
			m, err := d.store.ResolveWorkCaller(r.Context(), caller, consumer)
			if err != nil {
				failure(w, err)
				return
			}
			result, err := d.store.Work(r.Context(), m, op, fields)
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"ok": true, "result": result})
		})
	}
}

// maintainWork runs a work sweep at start, which reconciles transitions that fell due
// while the daemon was down, and then 30 seconds after each sweep ends, so sweeps never
// overlap. Timers use the monotonic clock.
func (d *Daemon) maintainWork() {
	defer d.wg.Done()
	for {
		ctx, cancel := contextUntil(d.stop, time.Minute)
		d.store.MaintainWork(ctx)
		// The audit log keeps 90 days and at most auditMax records (sprint chunk 09).
		d.store.trimAudit(ctx)
		cancel()
		select {
		case <-d.stop:
			return
		case <-time.After(workSweepInterval):
		}
	}
}

// contextUntil is a context that ends when stop closes or after limit.
func contextUntil(stop <-chan struct{}, limit time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
