package core

import (
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

	"github.com/rwcii/koinon/internal/platform"
)

type Config struct {
	StateDir string
	Listen   []string
}

type Daemon struct {
	store     *Store
	secret    string
	lock      *os.File
	listeners []net.Listener
	servers   []*http.Server
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	errors    chan error
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
	d := &Daemon{lock: lock, errors: make(chan error, 2)}
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
	status, code := http.StatusInternalServerError, "storage_error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrMissing):
		status, code = http.StatusNotFound, "session_not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "session_conflict"
	}
	// Do not return database paths, credentials or submitted session content.
	respond(w, status, map[string]any{"ok": false, "code": code})
}

func decode(w http.ResponseWriter, r *http.Request, value any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return ErrInvalid
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
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
		respond(w, 200, map[string]any{"ok": true, "daemon": "running", "listeners": d.Addresses(), "sessions": counts, "schema": 1})
	})
	mux.HandleFunc("GET /v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		items, truncated, err := d.store.List(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		respond(w, 200, map[string]any{"ok": true, "sessions": items, "truncated": truncated})
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This API uses only bearer authentication. Browser origins are refused;
		// dashboard cookies and CSRF checks belong to the later dashboard chunk.
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

// GetStatus never uses environment proxies or follows a redirect with the secret.
func GetStatus(ctx context.Context, address, secret string) (json.RawMessage, error) {
	if !validAddress(address) {
		return nil, ErrInvalid
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/v1/status", nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	response, err := client.Do(r)
	if err != nil {
		return nil, errors.New("daemon unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("daemon refused status request")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 || !json.Valid(data) {
		return nil, errors.New("invalid daemon response")
	}
	return data, nil
}
