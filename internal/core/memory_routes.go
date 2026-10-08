package core

import (
	"context"
	"net/http"
)

type memoryRequest struct {
	Caller   Key     `json:"caller"`
	Consumer *string `json:"consumer"`
}

// memoryRoutes adds the memory API. The store is the caller's recorded repository, and
// provenance is the caller's family and peer name.
func (d *Daemon) memoryRoutes(mux *http.ServeMux) {
	handle := func(path string, limit int64, request func() (any, *memoryRequest),
		run func(context.Context, MemoryCaller, any) (any, error)) {
		mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
			value, common := request()
			if err := decodeLimit(w, r, value, limit); err != nil {
				failure(w, err)
				return
			}
			caller, err := d.store.ResolveMemoryCaller(r.Context(), common.Caller, common.Consumer)
			if err != nil {
				failure(w, err)
				return
			}
			result, err := run(r.Context(), caller, value)
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"ok": true, "result": result})
		})
	}
	type record struct {
		memoryRequest
		MemoryRecordRequest
	}
	handle("/v1/memory/record", 6*8192+16384, func() (any, *memoryRequest) { v := &record{}; return v, &v.memoryRequest },
		func(ctx context.Context, m MemoryCaller, v any) (any, error) {
			return d.store.MemoryRecord(ctx, m, v.(*record).MemoryRecordRequest)
		})
	type sync struct {
		memoryRequest
		MemorySyncRequest
	}
	handle("/v1/memory/sync", 16384, func() (any, *memoryRequest) { v := &sync{}; return v, &v.memoryRequest },
		func(ctx context.Context, m MemoryCaller, v any) (any, error) {
			return d.store.MemorySync(ctx, m, v.(*sync).MemorySyncRequest)
		})
	type ack struct {
		memoryRequest
		MemoryAckRequest
	}
	handle("/v1/memory/ack", 16384, func() (any, *memoryRequest) { v := &ack{}; return v, &v.memoryRequest },
		func(ctx context.Context, m MemoryCaller, v any) (any, error) {
			return d.store.MemoryAck(ctx, m, v.(*ack).MemoryAckRequest)
		})
	type recall struct {
		memoryRequest
		Query  string `json:"query"`
		Before int64  `json:"before"`
	}
	handle("/v1/memory/recall", 16384, func() (any, *memoryRequest) { v := &recall{}; return v, &v.memoryRequest },
		func(ctx context.Context, m MemoryCaller, v any) (any, error) {
			return d.store.MemoryRecall(ctx, m, v.(*recall).Query, v.(*recall).Before)
		})
	type status struct {
		memoryRequest
		After string `json:"after"`
	}
	handle("/v1/memory/status", 16384, func() (any, *memoryRequest) { v := &status{}; return v, &v.memoryRequest },
		func(ctx context.Context, m MemoryCaller, v any) (any, error) {
			return d.store.MemoryStatus(ctx, m, v.(*status).After)
		})
	mux.HandleFunc("POST /v1/storage/recover", func(w http.ResponseWriter, r *http.Request) {
		var request struct{}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		result, err := d.store.Recover(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "storage": result})
	})
}
