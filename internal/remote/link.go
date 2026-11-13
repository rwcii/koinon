package remote

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
)

// TCPMapping is the raw TCP Serve forward of the daemon link (#670, LINK-670-PAIR): Serve
// passes the TCP stream to a loopback TLS listener without ending its TLS, so each daemon
// checks the other's pinned key itself. Its ownership follows the same write-ahead rules as
// Mapping: the caller journals the intent before EnsureTCP and keeps it until RemoveTCP.
type TCPMapping struct {
	Port   int    `json:"port"`
	Target string `json:"target"`
}

// ValidateTCPMapping accepts a Serve TCP port of 1 to 65535 and a 127.0.0.1:PORT target.
func ValidateTCPMapping(m TCPMapping) error {
	if !ValidHTTPSPort(m.Port) {
		return diagnostic("link_port_invalid", "use a link port of 1 to 65535")
	}
	host, port, err := net.SplitHostPort(m.Target)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || host != "127.0.0.1" || n < 1 || n > 65535 || port != strconv.Itoa(n) {
		return diagnostic("link_target_invalid", "use 127.0.0.1:PORT for the link listener")
	}
	return nil
}

func (m TCPMapping) portKey() string { return strconv.Itoa(m.Port) }

// TCPMappingPresent reports whether Serve forwards exactly this port to exactly this target,
// as raw TCP: no HTTP, no HTTPS and no TLS termination.
func (s Status) TCPMappingPresent(m TCPMapping) bool {
	t, ok := s.Serve.TCP[m.portKey()]
	return ok && t.TCPForward == m.Target && !t.HTTPS && !t.HTTP && t.TerminateTLS == ""
}

func tcpConflict(m TCPMapping) error {
	return diagnostic("tailscale_serve_conflict", "Tailscale already uses TCP port "+m.portKey()+"; preserve that configuration and move or remove it explicitly, or choose another link port, before retrying")
}

func foregroundTCP(c ServeConfig, port string) bool {
	for _, fg := range c.Foreground {
		if fg == nil {
			continue
		}
		if _, ok := fg.TCP[port]; ok || foregroundTCP(*fg, port) {
			return true
		}
	}
	return false
}

// checkTCP refuses any other use of the port: a web or HTTPS mapping, TLS termination, a
// forward to another target, Funnel, a foreground session, or an unowned identical forward.
func (s Status) checkTCP(m TCPMapping, owned bool) error {
	if foregroundTCP(s.Serve, m.portKey()) {
		return tcpConflict(m)
	}
	for hostPort := range s.Serve.Web {
		if strings.HasSuffix(hostPort, ":"+m.portKey()) {
			return tcpConflict(m)
		}
	}
	for hostPort, on := range s.Serve.AllowFunnel {
		if on && strings.HasSuffix(hostPort, ":"+m.portKey()) {
			return tcpConflict(m)
		}
	}
	if t, ok := s.Serve.TCP[m.portKey()]; ok {
		if t.HTTPS || t.HTTP || t.TerminateTLS != "" || t.TCPForward != m.Target || !owned {
			return tcpConflict(m)
		}
	}
	return nil
}

// CheckTCP validates the link mapping against a fresh status without changing Tailscale.
func (m Manager) CheckTCP(ctx context.Context, mapping TCPMapping, owned bool) error {
	if err := ValidateTCPMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if err := s.Ready(); err != nil {
		return err
	}
	return s.checkTCP(mapping, owned)
}

// EnsureTCP adds the owned raw TCP forward, or confirms it. The command's exit is no proof:
// the result is read back.
func (m Manager) EnsureTCP(ctx context.Context, mapping TCPMapping, owned bool) error {
	if err := ValidateTCPMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if err := s.Ready(); err != nil {
		return err
	}
	if err := s.checkTCP(mapping, owned); err != nil {
		return err
	}
	if s.TCPMappingPresent(mapping) {
		return nil
	}
	if _, err := m.adapter().Run(ctx, "serve", "--bg", "--tcp="+mapping.portKey(), "tcp://"+mapping.Target); err != nil {
		return commandError(err, "tailscale_serve_setup_failed", "check Tailscale Serve permissions; then retry koinon remote enable")
	}
	after, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if !after.TCPMappingPresent(mapping) {
		return diagnostic("tailscale_serve_not_restored", "the link's Serve TCP mapping is missing; retry koinon remote enable")
	}
	return nil
}

// RemoveTCP removes only the journaled forward, when Serve still forwards that port to that
// target. A changed or foreign use of the port is left alone and reported.
func (m Manager) RemoveTCP(ctx context.Context, mapping TCPMapping) error {
	if err := ValidateTCPMapping(mapping); err != nil {
		return err
	}
	s, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.Serve.TCP[mapping.portKey()]; !ok {
		return nil
	}
	if !s.TCPMappingPresent(mapping) || foregroundTCP(s.Serve, mapping.portKey()) {
		return tcpConflict(mapping)
	}
	if _, err := m.adapter().Run(ctx, "serve", "--tcp="+mapping.portKey(), "off"); err != nil {
		return commandError(err, "tailscale_serve_remove_failed", "the link listener is stopped; check Tailscale permissions and retry koinon remote disable")
	}
	after, err := m.Inspect(ctx)
	if err != nil {
		return err
	}
	if _, ok := after.Serve.TCP[mapping.portKey()]; ok {
		return diagnostic("tailscale_serve_remove_unverified", "Serve still uses the link port; inspect tailscale serve status and retry koinon remote disable")
	}
	return nil
}

// Peer is one other device of the tailnet: its full name, lower case without the trailing
// dot, and its valid Tailscale addresses, IPv4 first.
type Peer struct {
	DNSName string
	IPs     []string
}

// maxPeers bounds the peer list that one status read keeps.
const maxPeers = 4096

// peersFrom keeps the peers with a valid name and at least one valid Tailscale address.
// A missing or malformed peer field gives none; it never makes the status unreadable.
func peersFrom(field json.RawMessage) []Peer {
	var raw map[string]json.RawMessage
	if len(field) == 0 || json.Unmarshal(field, &raw) != nil {
		return nil
	}
	var peers []Peer
	for _, value := range raw {
		if len(peers) == maxPeers {
			break
		}
		var p struct {
			DNSName      string
			TailscaleIPs json.RawMessage
		}
		if json.Unmarshal(value, &p) != nil {
			continue
		}
		name := NormalizeDNSName(p.DNSName)
		ips := tailnetIPs(p.TailscaleIPs)
		if name == "" || len(ips) == 0 {
			continue
		}
		peers = append(peers, Peer{DNSName: name, IPs: ips})
	}
	return peers
}

// NormalizeDNSName lower-cases a tailnet name and removes one trailing dot. It returns ""
// for a name that is not a valid .ts.net name.
func NormalizeDNSName(name string) string {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if !ValidOrigin("https://" + name) {
		return ""
	}
	return name
}

// PeerAddresses returns the addresses of the one peer whose full name equals name exactly.
// It returns nil when no peer or more than one peer has the name: an ambiguous answer never
// replaces addresses that the caller already holds.
func (s Status) PeerAddresses(name string) []string {
	name = NormalizeDNSName(name)
	if name == "" {
		return nil
	}
	var found []string
	matches := 0
	for _, p := range s.Peers {
		if p.DNSName == name {
			matches++
			found = p.IPs
		}
	}
	if matches != 1 {
		return nil
	}
	return append([]string(nil), found...)
}
