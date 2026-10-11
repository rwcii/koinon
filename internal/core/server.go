package core

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/platform"
)

type Config struct {
	StateDir string
	Listen   []string
}

type Daemon struct {
	store     *Store
	secret    string
	auth      *dashboardAuth
	dashboard http.Handler
	started   time.Time
	lock      *os.File
	listeners []net.Listener
	servers   []*http.Server
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	errors    chan error
	stop      chan struct{}
	// root is the private state directory; launch runs the launcher for a dashboard start
	// (sprint chunk 09), replaced in tests.
	root   string
	launch func(ctx context.Context, args []string) ([]byte, error)
	// unfinished holds launch results whose audit update failed, by record ID; the
	// maintenance loop writes them when storage allows. A launch never runs again.
	launchMu   sync.Mutex
	unfinished map[int64][2]string
}

func validAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	// Require literal loopback, so DNS, wildcard binds and URL authorities never
	// cause a connection to a different host.
	return ip != nil && ip.IsLoopback()
}

func Start(c Config) (*Daemon, error) {
	if len(c.Listen) == 0 {
		c.Listen = []string{"127.0.0.1:47671", "[::1]:47671"}
	}
	if len(c.Listen) > 2 {
		return nil, errors.New("at most two loopback listeners")
	}
	for _, address := range c.Listen {
		if !validAddress(address) {
			return nil, errors.New("listener must be a literal loopback address")
		}
	}
	root, err := platform.PrivateDir(c.StateDir)
	if err != nil {
		return nil, err
	}
	lock, err := platform.Lock(filepath.Join(root, "daemon.lock"))
	if err != nil {
		return nil, err
	}
	d := &Daemon{lock: lock, errors: make(chan error, 2), stop: make(chan struct{}), root: root, launch: runLauncher}
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	d.secret, err = loadSecret(root)
	if err != nil {
		return nil, err
	}
	d.store, err = openStore(root)
	if err != nil {
		return nil, err
	}
	d.started = d.store.now()
	// No tool call before this start is known, so the succession guard counts the start as one.
	d.store.calls.since = d.started.UnixMilli()
	d.store.observed.extras = d.store.openCodeActivity
	d.unfinished = map[int64][2]string{}
	// A launch whose result a previous daemon never recorded stays visibly open no longer.
	d.store.closeStartedLaunches(context.Background())
	d.store.wake.send = d.store.providerWake
	if d.auth, err = newDashboardAuth(func() time.Time { return d.store.now() }); err != nil {
		return nil, err
	}
	if d.dashboard, err = d.dashboardHandler(); err != nil {
		return nil, err
	}
	for _, address := range c.Listen {
		network := "tcp4"
		host, _, _ := net.SplitHostPort(address)
		if net.ParseIP(host).To4() == nil {
			network = "tcp6"
		}
		var listener net.Listener
		listener, err = net.Listen(network, address)
		if err != nil {
			return nil, err
		}
		d.listeners = append(d.listeners, listener)
	}
	d.wg.Add(1)
	go d.maintainWork()
	d.wg.Add(1)
	go d.maintainWake()
	for _, listener := range d.listeners {
		server := &http.Server{Handler: d.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
		d.servers = append(d.servers, server)
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				d.errors <- err
			}
		}()
	}
	return d, nil
}

func (d *Daemon) Addresses() []string {
	result := []string{}
	for _, l := range d.listeners {
		result = append(result, l.Addr().String())
	}
	return result
}

func (d *Daemon) Errors() <-chan error { return d.errors }

func (d *Daemon) Close() error {
	d.closeOnce.Do(func() {
		close(d.stop)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, s := range d.servers {
			if err := s.Shutdown(ctx); err != nil {
				d.closeErr = errors.Join(d.closeErr, err, s.Close())
			}
		}
		for _, l := range d.listeners {
			l.Close()
		}
		d.wg.Wait()
		if d.store != nil {
			d.store.closeWake()
			d.closeErr = errors.Join(d.closeErr, d.store.db.Close())
		}
		if d.lock != nil {
			d.closeErr = errors.Join(d.closeErr, platform.Unlock(d.lock))
		}
	})
	return d.closeErr
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func failure(w http.ResponseWriter, err error) {
	var work WorkRefusal
	if errors.As(err, &work) {
		status := http.StatusConflict
		if work.Code == "invalid_request" {
			status = http.StatusBadRequest
		}
		body := map[string]any{"ok": false, "code": work.Code, "error": work.Message}
		if work.Details != nil {
			body["details"] = work.Details
		}
		respond(w, status, body)
		return
	}
	var refusal Refusal
	if errors.As(err, &refusal) {
		status := http.StatusConflict
		if refusal.Code == "invalid_request" {
			status = http.StatusBadRequest
		}
		respond(w, status, map[string]any{"ok": false, "code": refusal.Code, "error": refusal.Message})
		return
	}
	status, code := plainError(err)
	// Do not return database paths, credentials or submitted session content.
	respond(w, status, map[string]any{"ok": false, "code": code})
}

// errorCode is the stable code of any store error, as failure reports it.
func errorCode(err error) string {
	var work WorkRefusal
	if errors.As(err, &work) {
		return work.Code
	}
	var refusal Refusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	_, code := plainError(err)
	return code
}

func plainError(err error) (int, string) {
	status, code := http.StatusInternalServerError, "storage_error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrMissing):
		status, code = http.StatusNotFound, "session_not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "session_conflict"
	case errors.Is(err, ErrPeerNotFound):
		status, code = http.StatusNotFound, "peer_not_found"
	case errors.Is(err, ErrAliasUnheld):
		status, code = http.StatusConflict, "alias_unheld"
	case errors.Is(err, ErrRecipientInactive):
		status, code = http.StatusConflict, "recipient_inactive"
	case errors.Is(err, ErrCallerInactive):
		status, code = http.StatusForbidden, "caller_inactive"
	case errors.Is(err, ErrAckBeyondLast):
		status, code = http.StatusConflict, "ack_beyond_last"
	case errors.Is(err, ErrMessageNotFound):
		status, code = http.StatusNotFound, "message_not_found"
	}
	return status, code
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	return decodeLimit(w, r, value, 16384)
}

// decodeLimit bounds one route's request. A send carries a body of up to maxBody bytes,
// which JSON escaping can grow to six times its size.
func decodeLimit(w http.ResponseWriter, r *http.Request, value any, limit int64) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return ErrInvalid
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return ErrInvalid
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

func (d *Daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		counts, err := d.store.Counts(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		storage, err := d.store.StorageStatus(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		wake, err := d.store.WakeHealth(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		retention, err := d.store.RetentionStatus(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "daemon": "running", "listeners": d.Addresses(), "sessions": counts,
			"schema": schemaVersion, "storage": storage, "wake": wake, "retention": retention})
	})
	mux.HandleFunc("GET /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		items, truncated, err := d.store.List(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "sessions": items, "truncated": truncated})
	})
	mux.HandleFunc("POST /v1/launches", func(w http.ResponseWriter, r *http.Request) {
		var target LaunchTarget
		// The nested list (MaxNested paths, each at most six times longer when escaped) may
		// exceed the ordinary limit.
		if err := decodeLimit(w, r, &target, 131072); err != nil {
			failure(w, err)
			return
		}
		id, err := d.store.CreateLaunch(r.Context(), target)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "launch_id": id})
	})
	mux.HandleFunc("POST /v1/launches/job", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			LaunchID string `json:"launch_id"`
			JobID    string `json:"job_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.SetLaunchJob(r.Context(), request.LaunchID, request.JobID); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /v1/launches/retire", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			LaunchID string `json:"launch_id"`
		}
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		if err := d.store.RetireLaunch(r.Context(), request.LaunchID); err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /v1/sessions/register", func(w http.ResponseWriter, r *http.Request) {
		var request Registration
		if err := decode(w, r, &request); err != nil {
			failure(w, err)
			return
		}
		result, err := d.store.Register(r.Context(), request)
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "session": result})
	})
	for _, action := range []string{"renew", "retire"} {
		mux.HandleFunc("POST /v1/sessions/"+action, func(w http.ResponseWriter, r *http.Request) {
			var request Mutation
			if err := decode(w, r, &request); err != nil {
				failure(w, err)
				return
			}
			result, err := d.store.Mutate(r.Context(), request, action == "retire")
			if err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"ok": true, "session": result})
		})
	}
	d.messageRoutes(mux)
	d.memoryRoutes(mux)
	d.workRoutes(mux)
	d.observationRoutes(mux)
	mux.HandleFunc("POST /v1/dashboard/links", func(w http.ResponseWriter, r *http.Request) {
		if err := decode(w, r, &struct{}{}); err != nil {
			failure(w, err)
			return
		}
		token, err := d.auth.link()
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "path": "/dashboard/login?token=" + token, "expires_in": int(linkLifetime.Seconds())})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The dashboard has its own login, host, origin and CSRF checks; a dashboard cookie
		// never reaches this API, which uses only bearer authentication and refuses browsers.
		if r.URL.Path == "/dashboard" || strings.HasPrefix(r.URL.Path, "/dashboard/") {
			d.dashboard.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Origin") != "" || !validAddress(r.Host) {
			respond(w, 403, map[string]any{"ok": false, "code": "foreign_origin"})
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(d.secret)) != 1 {
			respond(w, 401, map[string]any{"ok": false, "code": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// GetStatus reads the daemon status.
func GetStatus(ctx context.Context, address, secret string) (json.RawMessage, error) {
	return Call(ctx, address, secret, "/v1/status", nil)
}

// ErrUnavailable reports that no daemon answered at the address.
var ErrUnavailable = errors.New("daemon unavailable")

// RefusedError carries the daemon's typed code for a refused request, and the bounded
// details of a work refusal, such as the holder of a conflicting claim.
type RefusedError struct {
	Code    string
	Details json.RawMessage
	// Message is the daemon's own text for the refusal, such as the field and rule that
	// an invalid request broke; empty when the daemon sent none.
	Message string
}

func (e RefusedError) Error() string {
	if e.Message != "" {
		return "daemon refused request: " + e.Code + ": " + e.Message
	}
	return "daemon refused request: " + e.Code
}

// Call sends one authenticated request: GET without a body, POST with one. It never uses
// environment proxies or follows a redirect with the secret.
func Call(ctx context.Context, address, secret, path string, body any) (json.RawMessage, error) {
	if !validAddress(address) {
		return nil, ErrInvalid
	}
	method, reader := http.MethodGet, io.Reader(nil)
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		method, reader = http.MethodPost, bytes.NewReader(data)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := http.NewRequestWithContext(ctx, method, "http://"+address+path, reader)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer response.Body.Close()
	// An inbox page holds about one MiB of bodies, which JSON escaping can grow sixfold.
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponse || !json.Valid(data) {
		return nil, errors.New("invalid daemon response")
	}
	if response.StatusCode != 200 {
		var refusal struct {
			Code    string          `json:"code"`
			Details json.RawMessage `json:"details"`
			Message string          `json:"error"`
		}
		if json.Unmarshal(data, &refusal) != nil || refusal.Code == "" || len(refusal.Code) > 64 {
			return nil, errors.New("daemon refused request")
		}
		if len(refusal.Message) > MaxRefusalMessage || !utf8.ValidString(refusal.Message) {
			refusal.Message = ""
		}
		return nil, RefusedError{Code: refusal.Code, Details: refusal.Details, Message: refusal.Message}
	}
	return data, nil
}

const maxResponse = 8 << 20

// MaxRefusalMessage bounds the refusal text that a client passes on.
const MaxRefusalMessage = 1024
