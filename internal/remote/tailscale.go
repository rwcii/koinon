// Package remote manages one private Tailscale Serve dashboard mapping. It never
// resets Serve or changes the host's Tailscale login or connection preferences.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Adapter is injectable so ordinary tests never need a Tailscale installation,
// account, network connection, or access to a person's live Serve configuration.
type Adapter interface {
	Run(context.Context, ...string) ([]byte, error)
}

// Exec uses the installed command, including the CLI bundled in the macOS app.
// Commands have a deadline and their output is bounded. Error diagnostics never
// copy raw process output, which can contain account details and login tokens.
type Exec struct{}

const InstallURL = "https://tailscale.com/download"
const HTTPSURL = "https://tailscale.com/docs/how-to/set-up-https-certificates"
const maxOutput = 1 << 20

func (Exec) Run(ctx context.Context, args ...string) ([]byte, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		candidates := []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"}
		if runtime.GOOS == "darwin" {
			candidates = []string{"/Applications/Tailscale.app/Contents/MacOS/Tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale"}
		}
		for _, candidate := range candidates {
			if st, e := os.Stat(candidate); e == nil && st.Mode().IsRegular() && st.Mode()&0111 != 0 {
				path, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		return nil, &Diagnostic{Code: "tailscale_missing", Next: "install Tailscale from " + InstallURL + " on the host and phone, then sign both into the same private network"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	var output, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &output, &stderr
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, &Diagnostic{Code: "tailscale_timeout", Next: "open Tailscale and check its connection and account prompts, then retry the command"}
		}
		return nil, &Diagnostic{Code: "tailscale_command_failed", Next: "open Tailscale, check that it is connected and that this account can configure Serve; on Linux configure the Tailscale operator if needed, then retry"}
	}
	if output.overflow {
		return nil, &Diagnostic{Code: "tailscale_output_invalid", Next: "Tailscale returned too much data; update Tailscale and retry"}
	}
	return output.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if available := maxOutput - b.Len(); len(p) > available {
		b.overflow = true
		p = p[:available]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

// Diagnostic is an actionable, content-free command failure safe to show in CLI
// and dashboard status. It intentionally contains no raw Tailscale response.
type Diagnostic struct {
	Code string
	Next string
}

func (d *Diagnostic) Error() string      { return d.Code + ": " + d.Next }
func diagnostic(code, next string) error { return &Diagnostic{Code: code, Next: next} }
func commandError(err error, code, next string) error {
	var d *Diagnostic
	if errors.As(err, &d) {
		return d
	}
	return diagnostic(code, next)
}

// Mapping identifies exactly the feature's HTTPS root and local HTTP backend.
// The caller must persist ownership intent before Ensure, and retain it after a
// failure until Remove succeeds. This allows safe recovery after interruption.
type Mapping struct {
	Origin string `json:"origin"`
	Target string `json:"target"`
}

func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host != u.Hostname() || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	host := u.Hostname()
	if host != strings.ToLower(host) || len(host) > 253 || !strings.HasSuffix(host, ".ts.net") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 4 {
		return false
	}
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func ValidateMapping(m Mapping) error {
	if !ValidOrigin(m.Origin) {
		return diagnostic("remote_origin_invalid", "use the exact HTTPS .ts.net origin reported by Tailscale for this host")
	}
	u, err := url.Parse(m.Target)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return diagnostic("remote_target_invalid", "use http://127.0.0.1:PORT for the dashboard-only listener")
	}
	host, port, err := net.SplitHostPort(u.Host)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 || port != strconv.Itoa(n) {
		return diagnostic("remote_target_invalid", "use http://127.0.0.1:PORT for the dashboard-only listener")
	}
	return nil
}
func (m Mapping) hostPort() string { return strings.TrimPrefix(m.Origin, "https://") + ":443" }

// ServeConfig models only the fields needed to inspect ownership. Unknown
// fields remain in Tailscale: mutations use its targeted CLI, never JSON rewrite.
type ServeConfig struct {
	TCP         map[string]TCPConfig    `json:"TCP"`
	Web         map[string]WebConfig    `json:"Web"`
	AllowFunnel map[string]bool         `json:"AllowFunnel"`
	Foreground  map[string]*ServeConfig `json:"Foreground"`
}
type TCPConfig struct {
	HTTPS        bool   `json:"HTTPS"`
	HTTP         bool   `json:"HTTP"`
	TCPForward   string `json:"TCPForward"`
	TerminateTLS string `json:"TerminateTLS"`
}
type WebConfig struct {
	Handlers map[string]Handler `json:"Handlers"`
}
type Handler struct {
	Proxy string `json:"Proxy"`
	Path  string `json:"Path"`
	Text  string `json:"Text"`
	extra bool
}

func (h *Handler) UnmarshalJSON(data []byte) error {
	type plain Handler
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key := range fields {
		if key != "Proxy" && key != "Path" && key != "Text" {
			value.extra = true
		}
	}
	*h = Handler(value)
	return nil
}

type Status struct {
	BackendState string      `json:"backend_state"`
	Online       bool        `json:"online"`
	DNSName      string      `json:"dns_name,omitempty"`
	Origin       string      `json:"origin,omitempty"`
	HTTPSReady   bool        `json:"https_ready"`
	Serve        ServeConfig `json:"-"`
}

func (s Status) Ready() error {
	if s.BackendState != "Running" || !s.Online {
		return diagnostic("tailscale_disconnected", "open Tailscale and connect the host; sign the phone into the same private network, then retry koinon remote enable")
	}
	if !ValidOrigin(s.Origin) {
		return diagnostic("tailscale_name_unavailable", "enable MagicDNS in the Tailscale admin console and check this host's .ts.net name, then retry")
	}
	if !s.HTTPSReady {
		return diagnostic("tailscale_https_required", "enable HTTPS certificates in the Tailscale admin console: "+HTTPSURL+"; then retry koinon remote enable")
	}
	return nil
}
func (s Status) MappingPresent(m Mapping) bool {
	h, ok := s.Serve.Web[m.hostPort()].Handlers["/"]
	return ok && h.Proxy == m.Target && h.Path == "" && h.Text == "" && !h.extra
}

// Manager serializes no application state. The daemon must serialize lifecycle
// requests and journal its mapping before calling Ensure or Remove.
type Manager struct{ Adapter Adapter }

func (m Manager) adapter() Adapter {
	if m.Adapter != nil {
		return m.Adapter
	}
	return Exec{}
}

func decode(data []byte, v any) error {
	if len(data) > maxOutput || len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

// Inspect is read-only and works when Tailscale is stopped, so status and disable
// can still inspect retained Serve ownership. Ready reports setup prerequisites.
func (m Manager) Inspect(ctx context.Context) (Status, error) {
	a := m.adapter()
	data, err := a.Run(ctx, "status", "--json")
	if err != nil {
		return Status{}, commandError(err, "tailscale_unavailable", "open Tailscale and sign in, then retry koinon remote status")
	}
	var raw struct {
		BackendState string
		Self         *struct {
			Online  bool
			DNSName string
		}
		CertDomains []string
	}
	if decode(data, &raw) != nil {
		return Status{}, diagnostic("tailscale_status_invalid", "update Tailscale; its status output could not be read")
	}
	s := Status{BackendState: raw.BackendState}
	if raw.Self != nil {
		s.Online = raw.Self.Online
		s.DNSName = strings.TrimSuffix(raw.Self.DNSName, ".")
		s.Origin = "https://" + s.DNSName
		if !ValidOrigin(s.Origin) {
			s.Origin = ""
		}
		for _, domain := range raw.CertDomains {
			if strings.TrimSuffix(domain, ".") == s.DNSName {
				s.HTTPSReady = true
			}
		}
	}
	data, err = a.Run(ctx, "serve", "status", "--json")
	if err != nil {
		return s, commandError(err, "tailscale_serve_unavailable", "check Tailscale Serve permission for this account, then retry koinon remote status")
	}
	if decode(data, &s.Serve) != nil {
		return s, diagnostic("tailscale_serve_invalid", "update Tailscale; its Serve configuration could not be read; no mappings were changed")
	}
	return s, nil
}

func conflict() error {
	return diagnostic("tailscale_serve_conflict", "Tailscale already uses HTTPS port 443 or a dashboard route; preserve that configuration and move or remove its conflicting mapping explicitly before retrying")
}
func foregroundConflict(c ServeConfig, m Mapping) bool {
	for _, fg := range c.Foreground {
		if fg == nil {
			continue
		}
		if _, ok := fg.TCP["443"]; ok {
			return true
		}
		if _, ok := fg.Web[m.hostPort()]; ok {
			return true
		}
		if foregroundConflict(*fg, m) {
			return true
		}
	}
	return false
}
func (s Status) checkMapping(m Mapping, owned bool) error {
	if foregroundConflict(s.Serve, m) || s.Serve.AllowFunnel[m.hostPort()] {
		return conflict()
	}
	if t, ok := s.Serve.TCP["443"]; ok && (!t.HTTPS || t.HTTP || t.TCPForward != "" || t.TerminateTLS != "") {
		return conflict()
	}
	for path, h := range s.Serve.Web[m.hostPort()].Handlers {
		if path == "/" {
			if !owned || h.Proxy != m.Target || h.Path != "" || h.Text != "" || h.extra {
				return conflict()
			}
		} else if path == "/dashboard" || strings.HasPrefix(path, "/dashboard/") {
			return conflict()
		}
	}
	return nil
}

// Check validates setup and mapping availability without changing Tailscale.
// Use owned=false before journaling a new mapping intent. Ensure repeats these
// checks against fresh state immediately before any mutation.
func (m Manager) Check(ctx context.Context, mapping Mapping, owned bool) error {
	if err := ValidateMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	return s.CheckAvailable(mapping, owned)
}

// CheckAvailable applies the same preflight to an already inspected snapshot.
func (s Status) CheckAvailable(mapping Mapping, owned bool) error {
	if err := ValidateMapping(mapping); err != nil {
		return err
	}
	if s.Origin != "" && s.Origin != mapping.Origin {
		return diagnostic("tailscale_identity_changed", "the host's Tailscale name changed; see docs/REMOTE-DASHBOARD.md#name-change-recovery, then disable the old configuration and enable for its new URL")
	}
	if err := s.Ready(); err != nil {
		return err
	}
	if s.Origin != mapping.Origin {
		return diagnostic("tailscale_identity_changed", "the host's Tailscale name changed; see docs/REMOTE-DASHBOARD.md#name-change-recovery, then disable the old configuration and enable for its new URL")
	}
	return s.checkMapping(mapping, owned)
}

// Ensure restores a missing owned mapping, and is idempotent for the same owned
// mapping. Pass owned=false for a new intent to refuse adopting any existing
// mapping, even one pointing at the same port. Persist that intent before this
// call: a successful command whose response is lost remains recoverable.
func (m Manager) Ensure(ctx context.Context, mapping Mapping, owned bool) error {
	if err := ValidateMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if err = s.CheckAvailable(mapping, owned); err != nil {
		return err
	}
	if s.MappingPresent(mapping) {
		return nil
	}
	_, err = m.adapter().Run(ctx, "serve", "--bg", "--https=443", "--set-path=/", mapping.Target)
	if err != nil {
		return commandError(err, "tailscale_serve_setup_failed", "check Tailscale HTTPS certificate and Serve permissions at "+HTTPSURL+"; then retry koinon remote enable")
	}
	// A command exit is not proof of the resulting mapping. Read it back before
	// reporting host readiness, including protection against concurrent edits.
	after, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if err = after.checkMapping(mapping, true); err != nil {
		return err
	}
	if !after.MappingPresent(mapping) {
		return diagnostic("tailscale_serve_not_restored", "the requested Serve mapping is missing; retry koinon remote enable")
	}
	return nil
}

// Remove only removes the journaled mapping when its exact target still matches.
// It leaves changed mappings and unrelated paths alone, including when stopped.
// Disable stops its listener first; an orderly daemon shutdown removes forwarding
// before releasing its backend port. Neither path clears ownership until confirmed.
func (m Manager) Remove(ctx context.Context, mapping Mapping) error {
	if err := ValidateMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	h, exists := s.Serve.Web[mapping.hostPort()].Handlers["/"]
	if !exists {
		return nil
	}
	// The CLI addresses the current DNS name implicitly. It cannot safely remove
	// a saved old-name mapping, because that could delete an unrelated current root.
	if s.Origin != mapping.Origin {
		return diagnostic("tailscale_identity_changed", "the remote listener is stopped; the Tailscale host name changed; temporarily restore the old name with tailscale set --hostname="+strings.Split(strings.TrimPrefix(mapping.Origin, "https://"), ".")[0]+" on its original tailnet, repeat koinon remote disable, then restore the desired name; see docs/REMOTE-DASHBOARD.md#name-change-recovery")
	}
	if t, ok := s.Serve.TCP["443"]; ok && (!t.HTTPS || t.HTTP || t.TCPForward != "" || t.TerminateTLS != "") {
		return conflict()
	}
	if h.Proxy != mapping.Target || h.Path != "" || h.Text != "" || h.extra || foregroundConflict(s.Serve, mapping) || s.Serve.AllowFunnel[mapping.hostPort()] {
		return conflict()
	}
	_, err = m.adapter().Run(ctx, "serve", "--bg", "--https=443", "--set-path=/", "off")
	if err != nil {
		return commandError(err, "tailscale_serve_remove_failed", "the remote listener is stopped; check Tailscale permissions and retry koinon remote disable to remove its retained mapping")
	}
	after, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if _, exists = after.Serve.Web[mapping.hostPort()].Handlers["/"]; exists {
		return diagnostic("tailscale_serve_remove_unverified", "Serve still has a root mapping; inspect tailscale serve status and retry koinon remote disable")
	}
	return nil
}
