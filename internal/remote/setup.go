package remote

import (
	"context"
	"net/url"
	"strings"
)

// Setup is a transient response for interactive setup. Its URL is a provider
// consent page, not the dashboard URL: never persist it or put it in a QR code,
// audit record, or diagnostic log.
type Setup struct {
	Complete   bool
	URL        string
	ShouldWait bool
}

// Setup reads Tailscale's feature enablement instructions without changing Serve
// mappings or accepting account consent. The installed CLI handles native LocalAPI
// authentication. Its debug interface is optional: callers can fall back to the
// official HTTPS setup instructions if it is unavailable on another version.
//
// This matches v1.102.3 client/local.Client.QueryFeature. The LocalAPI handler
// requires PermitRead despite using POST, and does not call SetServeConfig.
func (m Manager) Setup(ctx context.Context) (Setup, error) {
	if err := ctx.Err(); err != nil {
		return Setup{}, err
	}
	data, err := m.adapter().Run(ctx, "debug", "localapi", "POST", "/localapi/v0/query-feature?feature=serve")
	if ctx.Err() != nil {
		return Setup{}, ctx.Err()
	}
	if err != nil {
		return Setup{}, setupUnavailable()
	}
	// Native QueryFeatureResponse uses these exact names. Ignore Text entirely:
	// neither arbitrary provider prose nor process output belongs in diagnostics.
	var raw struct {
		Complete   bool
		URL        string
		ShouldWait bool
	}
	if decode(data, &raw) != nil {
		return Setup{}, setupUnavailable()
	}
	if raw.URL != "" && !validSetupURL(raw.URL) {
		return Setup{}, setupUnavailable()
	}
	if !raw.Complete && raw.URL == "" {
		return Setup{}, setupUnavailable()
	}
	return Setup{Complete: raw.Complete, URL: raw.URL, ShouldWait: raw.ShouldWait}, nil
}

func setupUnavailable() error {
	return diagnostic("tailscale_setup_unavailable", "Tailscale's setup link is unavailable; open Tailscale and check account permissions, then follow "+HTTPSURL+" and retry koinon remote enable")
}

// Only the installed provider's Serve activation page is eligible for display.
// The node identifier is opaque: inspect its shape, never interpret or log it.
func validSetupURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	for _, r := range raw {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "login.tailscale.com" || u.User != nil || u.Path != "/f/serve" || u.RawPath != "" || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 1 || len(q["node"]) != 1 {
		return false
	}
	node := q.Get("node")
	if len(node) < 1 || len(node) > 256 || u.RawQuery != "node="+node {
		return false
	}
	for _, c := range node {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}
