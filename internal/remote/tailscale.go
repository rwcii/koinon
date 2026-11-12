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
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
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
		for _, candidate := range platform.TailscaleCandidates() {
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
		return nil, commandFailure(runtime.GOOS, stderr.Bytes(), currentAccount)
	}
	if output.overflow {
		return nil, &Diagnostic{Code: "tailscale_output_invalid", Next: "Tailscale returned too much data; update Tailscale and retry"}
	}
	return output.Bytes(), nil
}

// OperatorRequired is the code of a Linux Serve write that tailscaled refused
// because this account is neither root nor the Tailscale operator (#652).
const OperatorRequired = "tailscale_operator_required"

var accountName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$`)

// currentAccount names the account that runs Koinon by its UID in /etc/passwd, so the
// recovery step never depends on $USER, which is root in a root shell. os/user is not
// used: without cgo, user.Current falls back to $USER when /etc/passwd has no entry.
// An account that only a directory service knows gets the manual id -un step.
func currentAccount() (string, error) { return accountByUID(os.Getuid(), "/etc/passwd") }

func accountByUID(uid int, passwd string) (string, error) {
	data, err := os.ReadFile(passwd)
	if err != nil {
		return "", err
	}
	want := strconv.Itoa(uid)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 3 && fields[2] == want {
			return fields[0], nil
		}
	}
	return "", errors.New("no passwd entry for the process UID")
}

// OperatorCommand is the exact Linux command that lets this account configure
// Serve, or "" when the account name cannot be read as a plain name.
func OperatorCommand() string { return operatorCommand(currentAccount) }

// OperatorAccount is the name of the account that runs Koinon, read as for
// OperatorCommand, or "" when it cannot be read as a plain name.
func OperatorAccount() string { return operatorAccount(currentAccount) }

func operatorAccount(account func() (string, error)) string {
	name, err := account()
	if err != nil || !accountName.MatchString(name) {
		return ""
	}
	return name
}

func operatorCommand(account func() (string, error)) string {
	name := operatorAccount(account)
	if name == "" {
		return ""
	}
	return "sudo tailscale set --operator=" + name
}

// commandFailure classifies a failed Tailscale command by its stderr and never
// copies that output. tailscaled refuses a Serve write from an account that is
// not root or the operator with "Access denied: serve config denied".
func commandFailure(goos string, stderr []byte, account func() (string, error)) *Diagnostic {
	if goos == "linux" && bytes.Contains(bytes.ToLower(stderr), []byte("access denied")) {
		next := "Tailscale refused this account. As the account that runs Koinon, outside a root shell, run id -un; then run sudo tailscale set --operator=NAME with that name, then run koinon remote enable"
		if command := operatorCommand(account); command != "" {
			next = "Tailscale refused this account. Run " + command + ", then run koinon remote enable"
		}
		return &Diagnostic{Code: OperatorRequired, Next: next}
	}
	return &Diagnostic{Code: "tailscale_command_failed", Next: "open Tailscale, check that it is connected and that this account can configure Serve; on Linux configure the Tailscale operator if needed, then retry"}
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

// DefaultHTTPSPort is the Serve HTTPS port of an origin that names none.
const DefaultHTTPSPort = 443

// ValidOrigin accepts the exact HTTPS .ts.net origin of this host, with an optional HTTPS
// port other than 443 (#665): https://HOST.TAILNET.ts.net or https://HOST.TAILNET.ts.net:PORT.
// Port 443 is written without the port, as browsers send it.
func ValidOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	host := u.Hostname()
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n == DefaultHTTPSPort || !ValidHTTPSPort(n) || port != strconv.Itoa(n) || u.Host != host+":"+port {
			return false
		}
	} else if u.Host != host {
		return false
	}
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
func (m Mapping) hostPort() string {
	u, _ := url.Parse(m.Origin)
	return u.Hostname() + ":" + strconv.Itoa(m.Port())
}

// ValidHTTPSPort reports whether n can be a Serve HTTPS port.
func ValidHTTPSPort(n int) bool { return n >= 1 && n <= 65535 }

// Port is the mapping's Serve HTTPS port: its origin's port, or 443.
func (m Mapping) Port() int {
	if u, err := url.Parse(m.Origin); err == nil && u.Port() != "" {
		if n, err := strconv.Atoi(u.Port()); err == nil {
			return n
		}
	}
	return DefaultHTTPSPort
}

// HostOrigin is the mapping's origin without its port: the host's Tailscale identity,
// as Inspect reports it.
func (m Mapping) HostOrigin() string {
	u, err := url.Parse(m.Origin)
	if err != nil {
		return m.Origin
	}
	return "https://" + u.Hostname()
}

// OriginAt is hostOrigin served on port: hostOrigin itself for 443, else with the port.
func OriginAt(hostOrigin string, port int) string {
	if port == DefaultHTTPSPort {
		return hostOrigin
	}
	return hostOrigin + ":" + strconv.Itoa(port)
}

func (m Mapping) portKey() string   { return strconv.Itoa(m.Port()) }
func (m Mapping) httpsFlag() string { return "--https=" + m.portKey() }

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
	BackendState string `json:"backend_state"`
	Online       bool   `json:"online"`
	DNSName      string `json:"dns_name,omitempty"`
	Origin       string `json:"origin,omitempty"`
	HTTPSReady   bool   `json:"https_ready"`
	// TailscaleIPs are the host's own Tailscale addresses, IPv4 first, for the policy
	// example (#653). Only addresses in Tailscale's ranges are kept.
	TailscaleIPs []string    `json:"tailscale_ips,omitempty"`
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
			Online       bool
			DNSName      string
			TailscaleIPs json.RawMessage
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
		s.TailscaleIPs = tailnetIPs(raw.Self.TailscaleIPs)
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

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// tailnetIPs keeps the valid Tailscale addresses of the status field, IPv4 first. Missing
// or malformed data gives none; it never makes the status unreadable (#653).
func tailnetIPs(raw json.RawMessage) []string {
	var values []any
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return nil
	}
	var v4, v6 []string
	seen := map[netip.Addr]bool{}
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		addr, err := netip.ParseAddr(text)
		if err != nil || addr.Zone() != "" || seen[addr] {
			continue
		}
		switch {
		case addr.Is4() && tailnetV4.Contains(addr):
			v4 = append(v4, addr.String())
		case addr.Is6() && !addr.Is4In6() && tailnetV6.Contains(addr):
			v6 = append(v6, addr.String())
		default:
			continue
		}
		seen[addr] = true
	}
	return append(v4, v6...)
}

// PolicyRule is an example tailnet policy entry that admits HTTPS to the given host
// addresses, or "" when there are none. autogroup:member is every member of the tailnet.
func PolicyRule(ips []string, port int) string {
	var dst []string
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		dst = append(dst, netip.AddrPortFrom(addr, uint16(port)).String())
	}
	if len(dst) == 0 {
		return ""
	}
	rule, _ := json.Marshal(struct {
		Action string   `json:"action"`
		Src    []string `json:"src"`
		Proto  string   `json:"proto"`
		Dst    []string `json:"dst"`
	}{"accept", []string{"autogroup:member"}, "tcp", dst})
	return string(rule)
}

func conflict(m Mapping) error {
	return diagnostic("tailscale_serve_conflict", "Tailscale already uses HTTPS port "+m.portKey()+" or a dashboard route; preserve that configuration and move or remove its conflicting mapping explicitly, or choose another port with koinon remote enable --https-port, before retrying")
}
func foregroundConflict(c ServeConfig, m Mapping) bool {
	for _, fg := range c.Foreground {
		if fg == nil {
			continue
		}
		if _, ok := fg.TCP[m.portKey()]; ok {
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
		return conflict(m)
	}
	if t, ok := s.Serve.TCP[m.portKey()]; ok && (!t.HTTPS || t.HTTP || t.TCPForward != "" || t.TerminateTLS != "") {
		return conflict(m)
	}
	for path, h := range s.Serve.Web[m.hostPort()].Handlers {
		if path == "/" {
			if !owned || h.Proxy != m.Target || h.Path != "" || h.Text != "" || h.extra {
				return conflict(m)
			}
		} else if path == "/dashboard" || strings.HasPrefix(path, "/dashboard/") {
			return conflict(m)
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
	if s.Origin != "" && s.Origin != mapping.HostOrigin() {
		return diagnostic("tailscale_identity_changed", "the host's Tailscale name changed; see docs/REMOTE-DASHBOARD.md#name-change-recovery, then disable the old configuration and enable for its new URL")
	}
	if err := s.Ready(); err != nil {
		return err
	}
	if s.Origin != mapping.HostOrigin() {
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
	_, err = m.adapter().Run(ctx, "serve", "--bg", mapping.httpsFlag(), "--set-path=/", mapping.Target)
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
	// A signed-out host reports no name. That is not a rename: say to connect it.
	if s.Origin == "" {
		return diagnostic("tailscale_disconnected", "the remote listener is stopped; open Tailscale and connect the host, then repeat koinon remote disable")
	}
	// The CLI addresses the current DNS name implicitly. It cannot safely remove
	// a saved old-name mapping, because that could delete an unrelated current root.
	if s.Origin != mapping.HostOrigin() {
		return diagnostic("tailscale_identity_changed", "the remote listener is stopped; the Tailscale host name changed; temporarily restore the old name with tailscale set --hostname="+strings.Split(strings.TrimPrefix(mapping.HostOrigin(), "https://"), ".")[0]+" on its original tailnet, repeat koinon remote disable, then restore the desired name; see docs/REMOTE-DASHBOARD.md#name-change-recovery")
	}
	if t, ok := s.Serve.TCP[mapping.portKey()]; ok && (!t.HTTPS || t.HTTP || t.TCPForward != "" || t.TerminateTLS != "") {
		return conflict(mapping)
	}
	if h.Proxy != mapping.Target || h.Path != "" || h.Text != "" || h.extra || foregroundConflict(s.Serve, mapping) || s.Serve.AllowFunnel[mapping.hostPort()] {
		return conflict(mapping)
	}
	_, err = m.adapter().Run(ctx, "serve", "--bg", mapping.httpsFlag(), "--set-path=/", "off")
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
