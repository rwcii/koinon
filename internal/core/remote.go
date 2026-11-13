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

	"github.com/rwcii/koinon/internal/link"
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
	// Link is the daemon link's raw TCP Serve mapping (#670), journaled with the same rules:
	// LinkOwned is recorded before Serve changes and cleared only after its removal.
	Link      *remote.TCPMapping `json:"link,omitempty"`
	LinkOwned bool               `json:"link_owned,omitempty"`
}

// validRemoteConfig checks the whole remote record, the link mapping included.
func validRemoteConfig(c *RemoteConfig) bool {
	if c == nil || c.Version != 1 || remote.ValidateMapping(c.Mapping) != nil || c.Enabled && !c.Owned || c.LinkOwned && c.Link == nil {
		return false
	}
	return c.Link == nil || remote.ValidateTCPMapping(*c.Link) == nil && c.Link.Port != c.Mapping.Port()
}

type RemoteStatus struct {
	Configured bool           `json:"configured"`
	URL        string         `json:"url,omitempty"`
	Listener   string         `json:"listener"`
	Serve      string         `json:"serve"`
	Tailscale  *remote.Status `json:"tailscale,omitempty"`
	Error      string         `json:"error,omitempty"`
	Phone      string         `json:"phone"`
	// PolicyHint and PolicyRule say what the tailnet access policy must allow when the
	// host is ready but the phone is unverified (#653). Koinon cannot read the policy.
	PolicyHint string `json:"policy_hint,omitempty"`
	PolicyRule string `json:"policy_rule,omitempty"`
	// The link between Koinon computers (#670), once its name and port are set: its loopback
	// listener and its raw TCP Serve mapping.
	LinkPort     int    `json:"link_port,omitempty"`
	LinkListener string `json:"link_listener,omitempty"`
	LinkServe    string `json:"link_serve,omitempty"`
}

// policyHint names the remote HTTPS port that the tailnet access policy must allow (#665).
func policyHint(port int) string {
	return fmt.Sprintf("If the phone times out, the tailnet access policy must allow TCP %d to this computer's Tailscale addresses (docs/REMOTE-DASHBOARD.md).", port)
}

const policyRuleNote = "autogroup:member is every member of the tailnet; replace it with a user, group or devices for narrower access."

type remoteRuntime struct {
	mu       sync.Mutex
	surface  sync.RWMutex
	active   bool
	config   *RemoteConfig
	server   *http.Server
	listener net.Listener
	problem  string
	manager  remote.Manager
	// offUnrecorded holds a disable whose record write failed: the surface stays
	// closed and the reconciler retries the write instead of opening it again.
	offUnrecorded bool
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
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF || !validRemoteConfig(&c) {
		return nil, errors.New("remote_configuration_invalid: inspect remote-dashboard.json in the private state directory")
	}
	return &c, nil
}

func writeRemoteConfig(root string, c *RemoteConfig) error {
	if !validRemoteConfig(c) {
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
	d.closeLinkLocked()
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

// errRemotePort refuses an HTTPS port outside 1 to 65535.
func errRemotePort() error {
	return workError("invalid_request", "https_port must be 1 to 65535, or 0 to keep the saved port")
}

// remoteEnable opens the remote surface on port, Serve's HTTPS port (#665): 0 keeps the saved
// port, or 443 for a first enable. A changed port on an owned mapping first takes the
// disable steps for the old one, so its logins end and its Serve mapping is removed.
func (d *Daemon) remoteEnable(ctx context.Context, port int) error {
	d.remote.mu.Lock()
	defer d.remote.mu.Unlock()
	return d.remoteEnableLocked(ctx, port)
}

// remoteEnableLocked takes the enable steps with the remote lock held.
func (d *Daemon) remoteEnableLocked(ctx context.Context, port int) error {
	if port != 0 && !remote.ValidHTTPSPort(port) {
		return errRemotePort()
	}
	rt := &d.remote
	rt.offUnrecorded = false
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
	if c != nil && c.Owned && status.Origin != "" && c.Mapping.HostOrigin() != status.Origin {
		d.closeRemoteLocked()
		return fmt.Errorf("remote_identity_changed: the saved mapping names %s; restore that Tailscale name/network temporarily, run koinon remote disable, then restore the desired name and enable; see docs/REMOTE-DASHBOARD.md#name-change-recovery", c.Mapping.Origin)
	}
	if err := status.Ready(); err != nil {
		return err
	}
	if port == 0 {
		port = remote.DefaultHTTPSPort
		if c != nil {
			port = c.Mapping.Port()
		}
	}
	if c != nil && c.Link != nil && c.Link.Port == port {
		return ErrLinkPortSame
	}
	if c != nil && c.Owned && c.Mapping.Port() != port {
		if err := d.remoteDisableLocked(ctx); err != nil {
			return err
		}
		if c, err = ReadRemoteConfig(d.root); err != nil {
			return err
		}
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
		c = &RemoteConfig{Version: 1, Mapping: remote.Mapping{Origin: remote.OriginAt(status.Origin, port), Target: "http://" + held.Addr().String()}}
	} else if !c.Owned {
		c.Mapping.Origin = remote.OriginAt(status.Origin, port)
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
	if err == nil {
		err = d.enableLinkLocked(ctx)
	}
	rt.problem = ""
	if err != nil {
		rt.problem = err.Error()
		d.remoteSecurityFailureLocked(err)
	}
	return err
}

// enableLinkLocked opens the link listener and its Serve mapping when the link is set up.
// The intent is journaled before Serve changes; an unowned identical mapping is not adopted.
func (d *Daemon) enableLinkLocked(ctx context.Context) error {
	rt := &d.remote
	c := rt.config
	if c == nil || c.Link == nil {
		return nil
	}
	owned := c.LinkOwned
	if !owned {
		if err := rt.manager.CheckTCP(ctx, *c.Link, false); err != nil {
			return err
		}
		c.LinkOwned = true
		if err := writeRemoteConfig(d.root, c); err != nil {
			return err
		}
	}
	if err := d.openLinkLocked(nil); err != nil {
		return errors.Join(err, rt.manager.RemoveTCP(ctx, *c.Link))
	}
	return rt.manager.EnsureTCP(ctx, *c.Link, true)
}

// clearInvites removes the invitations from link.json. The close already revoked them in
// memory, which is their authority; this keeps the file true, and its error is reported.
func (d *Daemon) clearInvites() error {
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	s, err := link.ReadState(d.root)
	if err != nil || len(s.Invites) == 0 {
		return err
	}
	s.Invites = nil
	return link.WriteState(d.root, s)
}

// disableLinkLocked removes the link's Serve mapping. Pairs stay. The link listener and its
// connections and invitations are already closed with the remote surface.
func (d *Daemon) disableLinkLocked(ctx context.Context, c *RemoteConfig) error {
	if c == nil || !c.LinkOwned {
		return nil
	}
	if err := d.remote.manager.RemoveTCP(ctx, *c.Link); err != nil {
		return err
	}
	c.LinkOwned = false
	return writeRemoteConfig(d.root, c)
}

func (d *Daemon) remoteDisable(ctx context.Context) error {
	d.remote.mu.Lock()
	defer d.remote.mu.Unlock()
	return d.remoteDisableLocked(ctx)
}

// remoteDisableLocked takes the disable steps with the remote lock held.
func (d *Daemon) remoteDisableLocked(ctx context.Context) error {
	rt := &d.remote
	// The off switch closes the surface first, also when the record cannot be read
	// or written; the error then reports the record failure. The close also ends the link
	// and revokes its invitations, before any Serve cleanup that can fail.
	d.closeRemoteLocked()
	inviteErr := d.clearInvites()
	c, err := ReadRemoteConfig(d.root)
	if err != nil {
		rt.offUnrecorded = true
		rt.problem = err.Error()
		return err
	}
	if c == nil {
		return nil
	}
	c.Enabled = false
	if err := writeRemoteConfig(d.root, c); err != nil {
		rt.offUnrecorded = true
		rt.problem = err.Error()
		return err
	}
	rt.offUnrecorded = false
	rt.config = c
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
	if err := d.disableLinkLocked(ctx, c); err != nil {
		rt.problem = err.Error()
		return err
	}
	if inviteErr != nil {
		rt.problem = "link invitations are revoked, but link.json could not be updated: " + inviteErr.Error()
		return inviteErr
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
		if rt.config != nil && rt.config.Enabled && observed.Origin != "" && rt.config.Mapping.HostOrigin() != observed.Origin {
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
				result.PolicyHint = policyHint(rt.config.Mapping.Port())
				if rule := remote.PolicyRule(observed.TailscaleIPs, rt.config.Mapping.Port()); rule != "" {
					result.PolicyHint += " Example policy entry; " + policyRuleNote
					result.PolicyRule = rule
				}
			}
		}
	}
	if c := rt.config; c != nil && c.Link != nil {
		result.LinkPort, result.LinkListener, result.LinkServe = c.Link.Port, "off", "off"
		d.link.mu.Lock()
		if d.link.listener != nil {
			result.LinkListener = "ready"
		}
		d.link.mu.Unlock()
		switch {
		case !c.Enabled && c.LinkOwned:
			result.LinkServe = "cleanup pending"
		case c.Enabled && err != nil:
			result.LinkServe = "unknown"
		case c.Enabled && observed.TCPMappingPresent(*c.Link):
			result.LinkServe = "ready"
		case c.Enabled:
			result.LinkServe = "missing"
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
	if c != nil && c.Enabled && rt.offUnrecorded {
		c.Enabled = false
		if err := writeRemoteConfig(d.root, c); err != nil {
			d.closeRemoteLocked()
			rt.problem = err.Error()
			return
		}
		rt.offUnrecorded = false
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
		if err == nil {
			err = d.disableLinkLocked(ctx, c)
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
				if err == nil {
					err = d.enableLinkLocked(ctx)
				}
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
			// enable takes an optional https_port (#665); disable takes an empty object.
			var body struct {
				HTTPSPort *int `json:"https_port,omitempty"`
			}
			var err error
			if action == "enable" {
				err = decode(w, r, &body)
			} else {
				err = decode(w, r, &struct{}{})
			}
			if err == nil && body.HTTPSPort != nil && *body.HTTPSPort != 0 && !remote.ValidHTTPSPort(*body.HTTPSPort) {
				err = errRemotePort()
			}
			if err != nil {
				failure(w, err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			if action == "enable" {
				port := 0
				if body.HTTPSPort != nil {
					port = *body.HTTPSPort
				}
				err = d.remoteEnable(ctx, port)
			} else {
				err = d.remoteDisable(ctx)
			}
			if err != nil {
				// The code names the step to take: a missing secret, a Tailscale
				// diagnostic, or remote_setup_required for another failure.
				code := "remote_setup_required"
				var diagnostic *remote.Diagnostic
				if errors.Is(err, ErrSecretUnset) {
					code = "remote_secret_required"
				} else if errors.As(err, &diagnostic) {
					code = diagnostic.Code
				}
				respond(w, 409, map[string]any{"ok": false, "code": code, "error": err.Error()})
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
	manager := remote.Manager{Adapter: adapter}
	if c.Owned {
		if err := manager.Remove(ctx, c.Mapping); err != nil {
			return err
		}
		c.Owned = false
		if err := writeRemoteConfig(root, c); err != nil {
			return err
		}
	}
	if !c.LinkOwned {
		return nil
	}
	if err := manager.RemoveTCP(ctx, *c.Link); err != nil {
		return err
	}
	c.LinkOwned = false
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
			if c.LinkOwned {
				err = errors.Join(err, rt.manager.RemoveTCP(ctx, *c.Link))
			}
		}
	}
	d.closeRemoteLocked()
	return err
}
