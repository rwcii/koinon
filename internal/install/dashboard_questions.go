package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
	"github.com/rwcii/koinon/internal/setup"
)

// DefaultDashboardPort is the port that the network question offers.
const DefaultDashboardPort = 8443

// askDashboard puts the dashboard questions at the terminal (DASHBOARD-LAN-624, part 2, and
// #665). It returns local for the answers "only on this computer" and "this computer and your
// tailnet", with the Serve HTTPS port for the second, or the resolved network address. Ended
// input returns none of them, so nothing is saved.
func askDashboard(ctx context.Context, o Options) (bool, int, *core.DashboardAddress, error) {
	fmt.Fprintln(o.Tell, "Where should the dashboard open?")
	fmt.Fprintln(o.Tell, "  1. Only on this computer")
	fmt.Fprintln(o.Tell, "  2. On this computer and your tailnet, through Tailscale Serve, with a login by the maintainer secret (recommended on a computer in a tailnet; no network address)")
	fmt.Fprintln(o.Tell, "  3. Also on a network address, over HTTPS, with a login by the maintainer secret")
	for network := false; !network; {
		answer, err := o.Line("Choice [1]: ")
		if errors.Is(err, io.EOF) {
			return false, 0, nil, nil
		}
		if err != nil {
			return false, 0, nil, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "1":
			return true, 0, nil, nil
		case "2":
			port, err := askHTTPSPort(o)
			if err != nil || port == 0 {
				return false, 0, nil, err
			}
			return true, port, nil, nil
		case "3":
			network = true
		default:
			fmt.Fprintln(o.Tell, "Type 1, 2 or 3.")
		}
	}
	addresses, err := o.HostAddresses()
	if err != nil {
		fmt.Fprintln(o.Tell, "The addresses of this host could not be listed: "+err.Error())
	}
	for {
		if len(addresses) == 0 {
			fmt.Fprintln(o.Tell, "This host has no network address to list. You can type a fully qualified name of this host.")
		} else {
			fmt.Fprintln(o.Tell, "The network addresses of this host:")
			for i, address := range addresses {
				fmt.Fprintf(o.Tell, "  %d. %s\n", i+1, address)
			}
		}
		answer, err := o.Line("Address number, or a fully qualified name of this host: ")
		if errors.Is(err, io.EOF) {
			return false, 0, nil, nil
		}
		if err != nil {
			return false, 0, nil, err
		}
		host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(answer), "["), "]")
		if host == "" {
			continue
		}
		if n, err := strconv.Atoi(host); err == nil {
			if n < 1 || n > len(addresses) {
				fmt.Fprintf(o.Tell, "There is no address %d.\n", n)
				continue
			}
			host = addresses[n-1]
		}
		port, err := o.Line(fmt.Sprintf("Port [%d]: ", DefaultDashboardPort))
		if errors.Is(err, io.EOF) {
			return false, 0, nil, nil
		}
		if err != nil {
			return false, 0, nil, err
		}
		if port = strings.TrimSpace(port); port == "" {
			port = strconv.Itoa(DefaultDashboardPort)
		}
		a, err := o.ResolveDashboard(ctx, net.JoinHostPort(host, port))
		if err != nil {
			fmt.Fprintln(o.Tell, "This address is refused: "+err.Error())
			continue
		}
		return false, 0, &a, nil
	}
}

// askHTTPSPort asks for the Serve HTTPS port of the tailnet answer; ended input returns 0.
func askHTTPSPort(o Options) (int, error) {
	for {
		answer, err := o.Line(fmt.Sprintf("Tailscale Serve HTTPS port [%d]: ", remote.DefaultHTTPSPort))
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return remote.DefaultHTTPSPort, nil
		}
		if n, err := strconv.Atoi(answer); err == nil && remote.ValidHTTPSPort(n) && answer == strconv.Itoa(n) {
			return n, nil
		}
		fmt.Fprintln(o.Tell, "Type a port from 1 to 65535.")
	}
}

// enableTailnet enables remote access for the tailnet answer after the daemon started. It
// offers the maintainer secret only when none is set. A refusal does not fail the install:
// the report and the terminal name the step. Without a start it names the steps.
func enableTailnet(ctx context.Context, o Options, r *Report, port int) error {
	step := remoteEnableCommand(o, r.Binary, port)
	r.Remote = &RemoteResult{HTTPSPort: port}
	fail := func(code, step string) {
		r.Remote.Error, r.Remote.Step = code, step
		fmt.Fprintln(o.Tell, "Remote access is not enabled: "+code+". "+step)
	}
	if o.NoStart {
		fail("daemon_not_started", "Start the daemon, then run "+step+".")
		return nil
	}
	status, err := readStatus(ctx, o)
	if err != nil {
		fail("daemon_unavailable", "When the daemon answers, run "+step+".")
		return nil
	}
	if !status.SecretSet {
		set, err := offerSecret(ctx, o, "The remote dashboard needs the maintainer secret for its login, and no secret is set.")
		if err != nil {
			return err
		}
		if !set {
			fail("remote_secret_required", "Set the maintainer secret with koinon maintainer secret set, then run "+step+".")
			return nil
		}
	}
	url, err := o.RemoteEnable(ctx, o.StateDir, o.Address, port)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		reason := err.Error()
		var refused core.RefusedError
		if errors.As(err, &refused) {
			reason = refused.Code
			if refused.Message != "" {
				reason += ": " + refused.Message
			}
		}
		fail(reason, "Then run "+step+".")
		return nil
	}
	r.Remote.URL = url
	fmt.Fprintln(o.Tell, "The dashboard is also open on your tailnet at "+url)
	return nil
}

// remoteEnableCommand is the command that enables remote access for this installation.
func remoteEnableCommand(o Options, binary string, port int) string {
	command := setup.ShellWord(binary) + " remote enable --https-port " + strconv.Itoa(port)
	if state, err := platform.DefaultStateDir(); err != nil || state != o.StateDir {
		command += " --state-dir " + setup.ShellWord(o.StateDir)
	}
	return command
}

// offerSecret asks to set the maintainer secret, which the network login needs. It reports
// whether a secret was set. A generated secret is shown once on the terminal, never in the
// report. Ended input and "not now" set nothing.
func offerSecret(ctx context.Context, o Options, why string) (bool, error) {
	bearer, err := core.ReadSecret(o.StateDir)
	if err != nil {
		return false, err
	}
	fmt.Fprintln(o.Tell, why)
	fmt.Fprintln(o.Tell, "  1. Type a new secret")
	fmt.Fprintln(o.Tell, "  2. Generate a secret")
	fmt.Fprintln(o.Tell, "  3. Not now")
	for {
		answer, err := o.Line("Choice [1]: ")
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		switch strings.TrimSpace(answer) {
		case "", "1":
			if o.NewSecret == nil {
				return false, errors.New("no terminal reads the secret")
			}
			secret, err := o.NewSecret()
			if err == nil && !core.ValidMaintainerSecret(secret) {
				err = errors.New("a maintainer secret is valid UTF-8 with at least 12 characters and at most 1024 bytes")
			}
			if err != nil {
				fmt.Fprintln(o.Tell, "The secret is not set: "+err.Error())
				continue
			}
			if _, err := o.MaintainerSecret(ctx, o.Address, bearer, "set", secret); err != nil {
				return false, fmt.Errorf("dashboard_secret_failed: %w", err)
			}
			fmt.Fprintln(o.Tell, "The maintainer secret is set.")
			return true, nil
		case "2":
			secret, err := o.MaintainerSecret(ctx, o.Address, bearer, "generate", "")
			if err != nil {
				return false, fmt.Errorf("dashboard_secret_failed: %w", err)
			}
			fmt.Fprintln(o.Tell, "The new maintainer secret, shown only now; keep it in a password manager:")
			fmt.Fprintln(o.Tell, "  "+secret)
			return true, nil
		case "3":
			return false, nil
		default:
			fmt.Fprintln(o.Tell, "Type 1, 2 or 3.")
		}
	}
}

// RemoteEnable asks the daemon of state at address to enable remote access on port, as
// koinon remote enable --https-port does without its terminal question, and returns the URL.
func RemoteEnable(ctx context.Context, state, address string, port int) (string, error) {
	bearer, err := core.ClientSecret(state, address)
	if err != nil {
		return "", err
	}
	if _, err := core.Call(ctx, address, bearer, "/v1/remote/enable", map[string]int{"https_port": port}); err != nil {
		return "", err
	}
	return RemoteURL(ctx, address, bearer)
}

// RemoteURL reads the remote URL from the daemon's remote status.
func RemoteURL(ctx context.Context, address, bearer string) (string, error) {
	data, err := core.Call(ctx, address, bearer, "/v1/remote/status", nil)
	if err != nil {
		return "", err
	}
	var status core.RemoteStatus
	if err := json.Unmarshal(data, &status); err != nil || status.URL == "" {
		return "", errors.New("the daemon reports no remote URL")
	}
	return status.URL, nil
}

// maintainerSecret calls the daemon's secret route, as koinon maintainer secret does.
func maintainerSecret(ctx context.Context, address, bearer, action, secret string) (string, error) {
	data, err := core.Call(ctx, address, bearer, "/v1/maintainer/secret", map[string]any{"action": action, "secret": secret})
	if err != nil {
		return "", err
	}
	var reply struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return "", errors.New("invalid daemon response")
	}
	if action == "generate" && reply.Secret == "" {
		return "", errors.New("the daemon returned no generated secret")
	}
	return reply.Secret, nil
}

// restart starts the daemon once more through start, so that it opens the network listener
// with the secret that is now set, and reports the status after the restart. A failed restart
// is reported with its step; the report never keeps the status from before it.
func restart(ctx context.Context, o Options, r *Report, start func(*daemonStatus) error, system **platform.SystemPlist) error {
	before, err := readStatus(ctx, o)
	if err == nil && *system != nil {
		// The system plist is installed now; the restart reconciles it, as a later install does.
		*system, err = systemMode(ctx, o)
	}
	if err == nil {
		err = start(&before)
	}
	if err == nil && before.PID > 0 {
		err = waitForNewDaemon(ctx, o, before.PID)
	}
	var status daemonStatus
	if err == nil {
		status, err = readStatus(ctx, o)
	}
	if err == nil && status.DashboardNetwork != nil {
		r.DashboardNetwork = status.DashboardNetwork
		return nil
	}
	if err == nil {
		err = errors.New("the restarted daemon reports no network dashboard")
	}
	step := "The maintainer secret is set, but the daemon restart failed. Run " + restartCommand(o, r.Binary) + " to start the network dashboard."
	failed := core.DashboardNetworkStatus{Code: "dashboard_restart_failed", Error: err.Error(), Step: step}
	if r.DashboardNetwork != nil {
		failed.Address, failed.Bind, failed.URL = r.DashboardNetwork.Address, r.DashboardNetwork.Bind, r.DashboardNetwork.URL
	}
	r.DashboardNetwork = &failed
	if _, down := readStatus(ctx, o); down != nil {
		r.Service = "restart_failed"
	}
	return fmt.Errorf("daemon_restart_failed: %v; %s", err, step)
}

// waitForNewDaemon waits until a daemon with another process ID than old answers.
func waitForNewDaemon(ctx context.Context, o Options, old int) error {
	for deadline := time.Now().Add(o.Wait); time.Now().Before(deadline); {
		if status, err := readStatus(ctx, o); err == nil && status.PID != old {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("the daemon (process %d) still answers after %s", old, o.Wait)
}

// tellDashboard shows the network dashboard's result at the terminal after the questions.
func tellDashboard(o Options, s core.DashboardNetworkStatus) {
	if s.Code == "running" {
		fmt.Fprintln(o.Tell, "The dashboard is also open at "+s.URL)
		if s.Fingerprint != "" {
			fmt.Fprintln(o.Tell, "Its certificate SHA-256 fingerprint is "+s.Fingerprint)
			fmt.Fprintln(o.Tell, "Compare it once with the fingerprint that the browser shows, then accept the certificate.")
		}
		return
	}
	line := "The network dashboard at " + s.URL + " is not open: " + s.Code
	if s.Step != "" {
		line += ". " + s.Step
	}
	fmt.Fprintln(o.Tell, line)
}
