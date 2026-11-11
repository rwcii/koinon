package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
)

// RemoteConfig is a private write-ahead ownership record. Owned is recorded before Serve
// is changed; Enabled=false is recorded before closing the listener or removing a mapping.
// A failed command retains this record, so a repeat or a restart can finish the same step.
type RemoteConfig struct {
	Version int            `json:"version"`
	Enabled bool           `json:"enabled"`
	Owned   bool           `json:"owned"`
	Mapping remote.Mapping `json:"mapping"`
}

type RemoteStatus struct {
	Configured bool           `json:"configured"`
	URL        string         `json:"url,omitempty"`
	Listener   string         `json:"listener"`
	Serve      string         `json:"serve"`
	Tailscale  *remote.Status `json:"tailscale,omitempty"`
	Error      string         `json:"error,omitempty"`
	Phone      string         `json:"phone"`
}

type remoteRuntime struct {
	mu       sync.Mutex
	surface  sync.RWMutex
	active   bool
	config   *RemoteConfig
	server   *http.Server
	listener net.Listener
	problem  string
	manager  remote.Manager
}

func ReadRemoteConfig(root string) (*RemoteConfig, error) {
	f, err := platform.OpenPrivate(filepath.Join(root, "remote-dashboard.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c RemoteConfig
	dec := json.NewDecoder(io.LimitReader(f, 4097))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || c.Version != 1 || remote.ValidateMapping(c.Mapping) != nil || c.Enabled && !c.Owned {
		return nil, errors.New("remote_configuration_invalid: inspect remote-dashboard.json in the private state directory")
	}
	return &c, nil
}

func writeRemoteConfig(root string, c *RemoteConfig) error {
	if c == nil || c.Version != 1 || remote.ValidateMapping(c.Mapping) != nil || c.Enabled && !c.Owned {
		return ErrInvalid
	}
	// Never replace an unsafe inode, even during an explicit repair.
	f, err := platform.OpenPrivate(filepath.Join(root, "remote-dashboard.json"), os.O_RDONLY)
	if err == nil {
		f.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return platform.WriteAtomic(filepath.Join(root, "remote-dashboard.json"), append(data, '\n'), 0600)
}

func (d *Daemon) openRemoteLocked() error { return d.openRemoteListenerLocked(nil) }

func (d *Daemon) openRemoteListenerLocked(l net.Listener) error {
	rt := &d.remote
	select {
	case <-d.stop:
		return ErrUnavailable
	default:
	}
	if rt.server != nil {
		return nil
	}
	c := rt.config
	if l == nil {
		var err error
		l, err = net.Listen("tcp4", strings.TrimPrefix(c.Mapping.Target, "http://"))
		if err != nil {
			return fmt.Errorf("remote_listener_unavailable: saved port is occupied; free it or disable then enable: %w", err)
		}
	}
	h := d.remoteDashboardHandler(c.Mapping.Origin)
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rt.surface.RLock()
		if !rt.active {
			rt.surface.RUnlock()
			http.Error(w, "remote access disabled", http.StatusServiceUnavailable)
			return
		}
		// These authenticated controls reconcile or close this very listener. They
		// must not hold its read lock while taking its write lock. All other requests
		// still drain before a disable; controls retain ordinary session/CSRF checks.
		if remoteControlRequest(r) {
			rt.surface.RUnlock()
		} else {
			defer rt.surface.RUnlock()
		}
		h.ServeHTTP(w, r)
	})
	rt.surface.Lock()
	rt.active = true
	rt.surface.Unlock()
	rt.server, rt.listener = server, l
	d.wg.Add(1)
	go func() { defer d.wg.Done(); server.Serve(l) }()
	return nil
}

func (d *Daemon) closeRemoteLocked() {
	rt := &d.remote
	rt.surface.Lock()
	defer rt.surface.Unlock()
	rt.active = false
	if rt.server != nil {
		rt.server.Close()
		rt.server = nil
	}
	// Close the socket explicitly as well: Close can precede Serve's registration of
	// the listener when a disable immediately follows enable.
	if rt.listener != nil {
		rt.listener.Close()
		rt.listener = nil
	}
	if d.auth != nil {
		d.auth.endRemote()
	}
}

// Conflicting exposure or a changed external identity ends this surface's logins.
func (d *Daemon) remoteSecurityFailureLocked(err error) {
	var diagnostic *remote.Diagnostic
	if errors.As(err, &diagnostic) && (diagnostic.Code == "tailscale_identity_changed" || diagnostic.Code == "tailscale_serve_conflict") {
		d.closeRemoteLocked()
	}
}

func (d *Daemon) remoteEnable(ctx context.Context) error {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	set, err := d.store.maintainerSecretSet(ctx)
	if err != nil {
		return err
	}
	if !set {
		return ErrSecretUnset
	}
	// Read again to surface malformed or unsafe state instead of overwriting it.
	c, err := ReadRemoteConfig(d.root)
	if err != nil {
		return err
	}
	status, err := rt.manager.Inspect(ctx)
	if err != nil {
		return err
	}
	if c != nil && c.Owned && status.Origin != "" && c.Mapping.Origin != status.Origin {
		d.closeRemoteLocked()
		return fmt.Errorf("remote_identity_changed: the saved mapping names %s; restore that Tailscale name/network temporarily, run koinon remote disable, then restore the desired name and enable; see docs/REMOTE-DASHBOARD.md#name-change-recovery", c.Mapping.Origin)
	}
	if err := status.Ready(); err != nil {
		return err
	}
	var held net.Listener
	if c == nil {
		held, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer func() {
			if held != nil {
				held.Close()
			}
		}()
		c = &RemoteConfig{Version: 1, Mapping: remote.Mapping{Origin: status.Origin, Target: "http://" + held.Addr().String()}}
	} else if !c.Owned {
		c.Mapping.Origin = status.Origin
		// An explicit disable released the old mapping; a new enable can recover from
		// an occupied old port. Enabled/restarting configurations never silently move.
		held, err = net.Listen("tcp4", strings.TrimPrefix(c.Mapping.Target, "http://"))
		if err != nil {
			held, err = net.Listen("tcp4", "127.0.0.1:0")
		}
		if err != nil {
			return err
		}
		defer func() {
			if held != nil {
				held.Close()
			}
		}()
		c.Mapping.Target = "http://" + held.Addr().String()
	}
	// Check before journaling so an existing identical but unowned mapping is not adopted.
	if err := rt.manager.Check(ctx, c.Mapping, c.Owned); err != nil {
		d.remoteSecurityFailureLocked(err)
		return err
	}
	if rt.config != nil && rt.config.Mapping != c.Mapping {
		d.closeRemoteLocked()
	}
	c.Enabled, c.Owned = true, true
	if err := writeRemoteConfig(d.root, c); err != nil {
		return err
	}
	rt.config = c
	if err := d.openRemoteListenerLocked(held); err != nil {
		err = errors.Join(err, rt.manager.Remove(ctx, c.Mapping))
		rt.problem = err.Error()
		return err
	}
	held = nil // listener ownership transferred; no release/rebind gap
	err = rt.manager.Ensure(ctx, c.Mapping, true)
	rt.problem = ""
	if err != nil {
		rt.problem = err.Error()
		d.remoteSecurityFailureLocked(err)
	}
	return err
}

func (d *Daemon) remoteDisable(ctx context.Context) error {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	c, err := ReadRemoteConfig(d.root)
	if err != nil {
		return err
	}
	if c == nil {
		d.closeRemoteLocked()
		return nil
	}
	c.Enabled = false
	if err := writeRemoteConfig(d.root, c); err != nil {
		return err
	}
	rt.config = c
	d.closeRemoteLocked()
	if c.Owned {
		if err := rt.manager.Remove(ctx, c.Mapping); err != nil {
			rt.problem = err.Error()
			return err
		}
		c.Owned = false
		if err := writeRemoteConfig(d.root, c); err != nil {
			rt.problem = err.Error()
			return err
		}
	}
	rt.problem = ""
	return nil
}

func (d *Daemon) remoteStatus(ctx context.Context) RemoteStatus {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	result := RemoteStatus{Listener: "off", Phone: "unverified", Error: rt.problem, Serve: "off"}
	if rt.config != nil {
		result.Configured = rt.config.Enabled
		result.URL = rt.config.Mapping.Origin + "/dashboard/"
		if !rt.config.Enabled && rt.config.Owned {
			result.Serve = "cleanup pending"
		}
		if rt.config.Enabled {
			result.Serve = "unknown"
			result.Listener = "unavailable"
			if rt.server != nil {
				result.Listener = "ready"
			}
		}
	}
	observed, err := rt.manager.Inspect(ctx)
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Tailscale = &observed
		if rt.config != nil && rt.config.Enabled && observed.Origin != "" && rt.config.Mapping.Origin != observed.Origin {
			d.closeRemoteLocked()
			result.Listener = "unavailable"
			result.Error = "Tailscale identity changed; see docs/REMOTE-DASHBOARD.md#name-change-recovery before disabling and enabling"
		}
		if rt.config != nil && rt.config.Enabled {
			result.Serve = "unknown"
			if err := observed.CheckAvailable(rt.config.Mapping, true); err != nil {
				result.Error = err.Error()
				d.remoteSecurityFailureLocked(err)
				if rt.server == nil {
					result.Listener = "unavailable"
				}
			} else if !observed.MappingPresent(rt.config.Mapping) {
				result.Serve = "missing"
				if result.Error == "" {
					result.Error = "Serve mapping absent; repeat koinon remote enable"
				}
			} else {
				result.Serve = "ready"
			}
		}
	}
	if rt.config != nil && rt.config.Enabled {
		set, err := d.store.maintainerSecretSet(ctx)
		if err != nil {
			result.Listener = "unknown"
			result.Error = "Maintainer secret state unavailable; check daemon health and retry status"
		} else if !set {
			result.Listener = "secret required"
			result.Error = ErrSecretUnset.Error()
		}
	}
	return result
}

// restoreRemote retries dependency startup after a host restart. A failure of this optional
// surface never stops the local agent API or LAN dashboard. No submitted action is retried.
func (d *Daemon) restoreRemote(ctx context.Context) {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	select {
	case <-d.stop:
		return
	default:
	}
	c, err := ReadRemoteConfig(d.root)
	if err != nil {
		d.closeRemoteLocked()
		rt.problem = err.Error()
		return
	}
	if rt.config != nil && (c == nil || rt.config.Mapping != c.Mapping) {
		d.closeRemoteLocked()
	}
	rt.config = c
	if c == nil {
		return
	}
	if !c.Enabled {
		d.closeRemoteLocked()
		if c.Owned {
			if err = rt.manager.Remove(ctx, c.Mapping); err == nil {
				c.Owned = false
				err = writeRemoteConfig(d.root, c)
			}
		}
	} else {
		err = rt.manager.Check(ctx, c.Mapping, true)
		var diagnostic *remote.Diagnostic
		if errors.As(err, &diagnostic) && (diagnostic.Code == "tailscale_identity_changed" || diagnostic.Code == "tailscale_serve_conflict") {
			d.closeRemoteLocked()
		} else {
			if openErr := d.openRemoteLocked(); openErr != nil {
				err = openErr
				// While this daemon is alive, never knowingly forward credentials to a foreign
				// process occupying the saved backend port. Keep the journal for restoration.
				cleanupErr := rt.manager.Remove(ctx, c.Mapping)
				if cleanupErr != nil {
					err = errors.Join(err, cleanupErr)
				}
			} else if err == nil {
				err = rt.manager.Ensure(ctx, c.Mapping, true)
			}
		}
	}
	rt.problem = ""
	if err != nil {
		rt.problem = err.Error()
		d.remoteSecurityFailureLocked(err)
	}
}

func (d *Daemon) maintainRemote() {
	defer d.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			d.restoreRemote(ctx)
			cancel()
			timer.Reset(30 * time.Second)
		}
	}
}

func (d *Daemon) remoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/remote/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		respond(w, 200, d.remoteStatus(ctx))
	})
	for _, action := range []string{"enable", "disable"} {
		mux.HandleFunc("POST /v1/remote/"+action, func(w http.ResponseWriter, r *http.Request) {
			if err := decode(w, r, &struct{}{}); err != nil {
				failure(w, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			var err error
			if action == "enable" {
				err = d.remoteEnable(ctx)
			} else {
				err = d.remoteDisable(ctx)
			}
			if err != nil {
				respond(w, 409, map[string]any{"ok": false, "code": "remote_setup_required", "error": err.Error()})
				return
			}
			respond(w, 200, map[string]any{"ok": true})
		})
	}
}

// CleanupRemote removes only a journaled Serve mapping after uninstall stopped the daemon.
// State is retained on a dependency failure; repeating uninstall can finish cleanup.
func CleanupRemote(ctx context.Context, root string, adapter remote.Adapter) error {
	c, err := ReadRemoteConfig(root)
	if err != nil || c == nil {
		return err
	}
	c.Enabled = false
	if err := writeRemoteConfig(root, c); err != nil {
		return err
	}
	if !c.Owned {
		return nil
	}
	if err := (remote.Manager{Adapter: adapter}).Remove(ctx, c.Mapping); err != nil {
		return err
	}
	c.Owned = false
	return writeRemoteConfig(root, c)
}

// An orderly stop removes forwarding before releasing the backend port. The enabled
// journal remains so startup restores the same URL and port. After an abrupt process
// death, same-user loopback ownership cannot be guaranteed until reconciliation runs.
func (d *Daemon) shutdownRemote(ctx context.Context) error {
	rt := &d.remote
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var err error
	if d.auth != nil {
		c, readErr := ReadRemoteConfig(d.root)
		if readErr != nil {
			err = readErr
		} else if c != nil && c.Enabled && c.Owned {
			err = rt.manager.Remove(ctx, c.Mapping)
		}
	}
	d.closeRemoteLocked()
	return err
}
