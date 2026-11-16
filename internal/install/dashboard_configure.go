package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/remote"
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

// DashboardTarget is a new answer to the install question. A zero HTTPSPort takes the saved
// Serve port, else the default; an empty Address takes the saved network address.
type DashboardTarget struct {
	Choice    string
	HTTPSPort int
	Address   string
}

// DashboardStep is one step of a change, with its effect on sign-ins and, after a failure, the
// command that finishes it.
type DashboardStep struct {
	Step   string `json:"step"`
	Done   bool   `json:"done"`
	Effect string `json:"effect,omitempty"`
	Error  string `json:"error,omitempty"`
	Finish string `json:"finish,omitempty"`
}

// DashboardChange is the result of koinon remote configure with an answer.
type DashboardChange struct {
	Before    DashboardCurrent       `json:"before"`
	Choice    string                 `json:"choice"`
	HTTPSPort int                    `json:"https_port,omitempty"`
	Network   *core.DashboardAddress `json:"network,omitempty"`
	Changed   bool                   `json:"changed"`
	Steps     []DashboardStep        `json:"steps"`
	After     *DashboardCurrent      `json:"after,omitempty"`
}

// ConfigureDashboard changes where the dashboard opens to target (#685). It reads the current
// choice, resolves the target and gets the maintainer secret that the target needs before it
// changes anything; a refusal there changes nothing. Then it closes the old access before it
// opens the new one: a remote disable, the saved network setting and one daemon restart
// through the installation's backend, then a remote enable. A failed step ends the change; the
// result names the steps that were done and the command that finishes the failed one, and the
// earlier steps stay done.
func ConfigureDashboard(ctx context.Context, o Options, target DashboardTarget) (DashboardChange, error) {
	var c DashboardChange
	if err := o.defaults(); err != nil {
		return c, err
	}
	if o.Call == nil {
		o.Call = core.Call
	}
	if o.Address == "" {
		port, err := core.RecordedPort(o.StateDir)
		if err != nil {
			return c, err
		}
		o.Address = core.Listeners(port)[0]
	}
	binary := Binary(o.Prefix)
	before, err := ReadDashboardCurrent(ctx, o)
	c.Before = before
	if err != nil {
		return c, err
	}
	if before.Runtime == nil || before.Runtime.Remote == nil {
		return c, fmt.Errorf("daemon_unavailable: %s; start the daemon with %s, then repeat this command", before.RuntimeError, restartCommand(o, binary))
	}
	c.Choice = target.Choice
	switch target.Choice {
	case DashboardLocal:
	case DashboardTailnet:
		c.HTTPSPort = target.HTTPSPort
		if c.HTTPSPort == 0 {
			c.HTTPSPort = before.HTTPSPort
		}
		if c.HTTPSPort == 0 {
			c.HTTPSPort = remote.DefaultHTTPSPort
		}
	case DashboardNetwork:
		address := target.Address
		if address == "" && before.Network != nil {
			address = before.Network.Address()
		}
		if address == "" {
			return c, errors.New("dashboard_address_required: no network address is saved; give --dashboard-address HOST:PORT")
		}
		resolved, err := o.ResolveDashboard(ctx, address)
		if err != nil {
			return c, err
		}
		c.Network = &resolved
	default:
		return c, errors.New("invalid_request: the choice is local, tailnet or network")
	}
	runtime := before.Runtime
	remoteOn := before.Choice == DashboardTailnet || before.Choice == DashboardMixed
	networkRuns := runtime.Network != nil && runtime.Network.Code == "running"
	tailnetReady := runtime.Remote.Configured && runtime.Remote.Listener == "ready" && runtime.Remote.Serve == "ready" && runtime.Remote.Error == ""
	// The steps that the target needs; an equal choice that runs needs none.
	disable := remoteOn && target.Choice != DashboardTailnet
	removeNetwork := before.Network != nil && target.Choice != DashboardNetwork
	writeNetwork := c.Network != nil && (before.Network == nil || *before.Network != *c.Network)
	restart := removeNetwork || writeNetwork || c.Network != nil && !networkRuns || c.Network == nil && runtime.Network != nil
	enable := target.Choice == DashboardTailnet && (!remoteOn || before.HTTPSPort != c.HTTPSPort || !tailnetReady)
	saveLocal := target.Choice != DashboardNetwork && !before.Asked
	if !disable && !removeNetwork && !writeNetwork && !restart && !enable && !saveLocal {
		c.Steps = []DashboardStep{}
		c.After = &before
		return c, nil
	}
	if !binaryPresent(binary) {
		return c, fmt.Errorf("install_not_found: no installed koinon is at %s; give the --prefix of this installation", binary)
	}
	if target.Choice != DashboardLocal && !runtime.SecretSet {
		if o.Line == nil {
			return c, fmt.Errorf("remote_secret_required: the dashboard login needs the maintainer secret, and none is set; run %s, then repeat this command", installCommand(o, binary, "maintainer secret set"))
		}
		set, err := offerSecret(ctx, o, "The dashboard login needs the maintainer secret, and no secret is set.")
		if err != nil {
			return c, err
		}
		if !set {
			return c, errors.New("remote_secret_required: no secret was set, so nothing changed")
		}
	}
	c.Changed = true
	again := "remote configure --choice " + target.Choice
	step := func(name, effect, finish string, do func() error) error {
		s := DashboardStep{Step: name, Effect: effect}
		err := do()
		if ctx.Err() != nil && err == nil {
			err = ctx.Err()
		}
		if err != nil {
			s.Error, s.Finish = err.Error(), finish
		}
		s.Done = err == nil
		c.Steps = append(c.Steps, s)
		return err
	}
	fail := func(err error) (DashboardChange, error) {
		if after, rerr := ReadDashboardCurrent(ctx, o); rerr == nil {
			c.After = &after
		}
		return c, fmt.Errorf("dashboard_change_incomplete: %w", err)
	}
	bearer, err := core.ClientSecret(o.StateDir, o.Address)
	if err != nil {
		return c, err
	}
	if disable {
		if err := step("remote_disable", "Remote sign-ins on your tailnet end.", installCommand(o, binary, "remote disable"), func() error {
			_, err := o.Call(ctx, o.Address, bearer, "/v1/remote/disable", struct{}{})
			return err
		}); err != nil {
			return fail(err)
		}
	}
	if removeNetwork || saveLocal {
		name := "save_no_network_address"
		if !removeNetwork {
			name = "save_choice"
		}
		if err := step(name, "", installCommand(o, binary, again), func() error {
			if err := core.WriteDashboardLocal(o.StateDir); err != nil {
				return err
			}
			if before.Network == nil {
				return nil
			}
			return core.WriteDashboardAddress(o.StateDir, nil)
		}); err != nil {
			return fail(err)
		}
	}
	if writeNetwork {
		if err := step("save_network_address", "", installCommand(o, binary, again+" --dashboard-address "+c.Network.Address()), func() error {
			return core.WriteDashboardAddress(o.StateDir, c.Network)
		}); err != nil {
			return fail(err)
		}
	}
	if restart {
		effect := "The daemon restarts; open dashboard pages reconnect."
		if runtime.Network != nil && (removeNetwork || writeNetwork) {
			effect = "The daemon restarts; a page open on the old network address loses its connection."
		}
		if err := step("daemon_restart", effect, restartCommand(o, binary), func() error {
			return restartDaemon(ctx, o, binary)
		}); err != nil {
			return fail(err)
		}
		if c.Network != nil {
			if err := step("network_address_open", "", restartCommand(o, binary), func() error {
				status, err := readStatus(ctx, o)
				if err != nil {
					return err
				}
				if n := status.DashboardNetwork; n == nil || n.Code != "running" {
					code := "not configured"
					if n != nil {
						code = n.Code
						if n.Step != "" {
							code += ": " + n.Step
						}
					}
					return errors.New("the network address is not open: " + code)
				}
				return nil
			}); err != nil {
				return fail(err)
			}
		}
	}
	if enable {
		effect := ""
		if remoteOn {
			effect = "Remote sign-ins on your tailnet end."
		}
		if err := step("remote_enable", effect, remoteEnableCommand(o, binary, c.HTTPSPort), func() error {
			_, err := o.RemoteEnable(ctx, o.StateDir, o.Address, c.HTTPSPort)
			return err
		}); err != nil {
			return fail(err)
		}
	}
	after, err := ReadDashboardCurrent(ctx, o)
	if err != nil {
		return c, err
	}
	c.After = &after
	return c, nil
}

// restartDaemon starts the answering daemon again through the backend of this installation,
// as install does after a network change, and waits for a new process.
func restartDaemon(ctx context.Context, o Options, binary string) error {
	system, err := systemMode(ctx, o)
	if err != nil {
		return err
	}
	before, err := readStatus(ctx, o)
	if err != nil {
		return err
	}
	o.dashboardChanged = true
	r := Report{Binary: binary, Backend: o.Services.Backend, Artifact: o.Services.DaemonArtifact(), StateDir: o.StateDir}
	if err := startService(ctx, o, &r, system, &before); err != nil {
		return err
	}
	return waitForNewDaemon(ctx, o, before.PID)
}

// binaryPresent reports whether the installed binary is a regular file.
func binaryPresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// AskDashboardTarget puts the install question again at the terminal of o.Line, with the
// current values as the defaults (#685). It checks each answer as the first install does.
// Ended input returns ok false: no answer, so nothing changes.
func AskDashboardTarget(ctx context.Context, o Options, c DashboardCurrent) (DashboardTarget, bool, error) {
	if err := o.defaults(); err != nil {
		return DashboardTarget{}, false, err
	}
	d := dashboardDefaults{choice: map[string]string{DashboardLocal: "1", DashboardTailnet: "2", DashboardNetwork: "3"}[c.Choice],
		httpsPort: c.HTTPSPort, port: DefaultDashboardPort}
	if d.httpsPort == 0 {
		d.httpsPort = remote.DefaultHTTPSPort
	}
	if c.Network != nil {
		d.host, d.port = c.Network.Host, c.Network.Port
	}
	local, port, network, err := askDashboardWith(ctx, o, d)
	switch {
	case err != nil:
		return DashboardTarget{}, false, err
	case network != nil:
		return DashboardTarget{Choice: DashboardNetwork, Address: network.Address()}, true, nil
	case port != 0:
		return DashboardTarget{Choice: DashboardTailnet, HTTPSPort: port}, true, nil
	case local:
		return DashboardTarget{Choice: DashboardLocal}, true, nil
	}
	return DashboardTarget{}, false, nil
}
