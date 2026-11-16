package install

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/rwcii/koinon/internal/core"
)

// The answers of the install question "Where should the dashboard open?" (#685).
const (
	DashboardLocal   = "local"
	DashboardTailnet = "tailnet"
	DashboardNetwork = "network"
	// DashboardMixed is a saved state that no single answer gives: a network address and
	// remote access through Serve, both on. Any answer replaces it.
	DashboardMixed = "network_and_tailnet"
)

// DashboardCurrent is the saved dashboard choice, read from the state directory, and what the
// daemon runs now, read from its status (#685). The two are separate: a saved choice can wait
// for a restart, and an unavailable daemon leaves the runtime unknown.
type DashboardCurrent struct {
	Choice string `json:"choice"`
	// Asked reports whether the install question has an answer: a saved local-only choice, a
	// network address or a remote record. Without one, the dashboard opens on this computer.
	Asked     bool                   `json:"asked"`
	HTTPSPort int                    `json:"https_port,omitempty"`
	Network   *core.DashboardAddress `json:"network,omitempty"`
	// Runtime is the daemon's answer; RuntimeError says why it is unknown.
	Runtime      *DashboardRuntime `json:"runtime,omitempty"`
	RuntimeError string            `json:"runtime_error,omitempty"`
}

// DashboardRuntime is what the answering daemon runs.
type DashboardRuntime struct {
	SecretSet bool                         `json:"maintainer_secret_set"`
	Network   *core.DashboardNetworkStatus `json:"network,omitempty"`
	Remote    *core.RemoteStatus           `json:"remote,omitempty"`
}

// ReadDashboardCurrent reads the saved choice of state and, when the daemon at o.Address
// answers, its runtime. A saved file that cannot be read is an error; an unavailable daemon is
// not.
func ReadDashboardCurrent(ctx context.Context, o Options) (DashboardCurrent, error) {
	var c DashboardCurrent
	local, err := core.ReadDashboardLocal(o.StateDir)
	if err != nil {
		return c, err
	}
	if c.Network, err = core.ReadDashboardAddress(o.StateDir); err != nil {
		return c, err
	}
	config, err := core.ReadRemoteConfig(o.StateDir)
	if err != nil {
		return c, fmt.Errorf("remote_configuration_invalid: %w", err)
	}
	remoteOn := config != nil && config.Enabled
	if config != nil {
		c.HTTPSPort = config.Mapping.Port()
	}
	c.Asked = local || c.Network != nil || config != nil
	switch {
	case c.Network != nil && remoteOn:
		c.Choice = DashboardMixed
	case c.Network != nil:
		c.Choice = DashboardNetwork
	case remoteOn:
		c.Choice = DashboardTailnet
	default:
		c.Choice = DashboardLocal
	}
	if o.Status == nil {
		o.Status = core.GetStatus
	}
	if o.Call == nil {
		o.Call = core.Call
	}
	status, err := readStatus(ctx, o)
	if err != nil {
		c.RuntimeError = "daemon_unavailable: " + err.Error()
		return c, nil
	}
	c.Runtime = &DashboardRuntime{SecretSet: status.SecretSet, Network: status.DashboardNetwork}
	bearer, err := core.ClientSecret(o.StateDir, o.Address)
	var data json.RawMessage
	if err == nil {
		data, err = o.Call(ctx, o.Address, bearer, "/v1/remote/status", nil)
	}
	var remoteStatus core.RemoteStatus
	if err == nil {
		err = json.Unmarshal(data, &remoteStatus)
	}
	if err != nil {
		c.RuntimeError = "remote_status_unavailable: " + err.Error()
		return c, nil
	}
	c.Runtime.Remote = &remoteStatus
	return c, nil
}

// DescribeDashboard gives the saved choice in the words of the install question, then what
// runs now, one line each.
func DescribeDashboard(c DashboardCurrent) []string {
	var lines []string
	tailnet := "2. On this computer and your tailnet, through Tailscale Serve"
	if c.HTTPSPort != 0 {
		tailnet += " (HTTPS port " + strconv.Itoa(c.HTTPSPort) + ")"
	}
	network := "3. Also on a network address, over HTTPS"
	if c.Network != nil {
		network += ", at " + c.Network.Address()
	}
	switch c.Choice {
	case DashboardLocal:
		lines = append(lines, "Saved choice: 1. Only on this computer")
		if !c.Asked {
			lines[0] += " (no answer saved yet)"
		}
	case DashboardTailnet:
		lines = append(lines, "Saved choice: "+tailnet)
	case DashboardNetwork:
		lines = append(lines, "Saved choice: "+network)
	default:
		lines = append(lines, fmt.Sprintf("Saved choice: a network address at %s and your tailnet through Tailscale Serve (HTTPS port %d), which is not one of the three answers", c.Network.Address(), c.HTTPSPort))
	}
	if c.Runtime == nil {
		return append(lines, "Running now: unknown ("+c.RuntimeError+")")
	}
	n := c.Runtime.Network
	switch {
	case n == nil:
		lines = append(lines, "Running now, network address: none")
	case n.Code == "running":
		lines = append(lines, "Running now, network address: open at "+n.URL)
	default:
		lines = append(lines, "Running now, network address: "+n.Address+" not open ("+n.Code+")")
	}
	switch r := c.Runtime.Remote; {
	case r == nil:
		lines = append(lines, "Running now, tailnet: unknown ("+c.RuntimeError+")")
	case !r.Configured:
		lines = append(lines, "Running now, tailnet: off")
	case r.Listener == "ready" && r.Serve == "ready" && r.Error == "":
		lines = append(lines, "Running now, tailnet: open at "+r.URL)
	default:
		lines = append(lines, fmt.Sprintf("Running now, tailnet: not ready (listener %s, Serve %s)", r.Listener, r.Serve))
	}
	return lines
}
