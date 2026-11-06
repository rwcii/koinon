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
)

// DefaultDashboardPort is the port that the network question offers.
const DefaultDashboardPort = 8443

// askDashboard puts the network dashboard questions at the terminal (DASHBOARD-LAN-624,
// part 2). It returns local for the answer "only on this computer", or the resolved network
// address. Ended input returns neither, so nothing is saved.
func askDashboard(ctx context.Context, o Options) (bool, *core.DashboardAddress, error) {
	fmt.Fprintln(o.Tell, "Open the dashboard only on this computer (default), or also on a network address?")
	fmt.Fprintln(o.Tell, "  1. Only on this computer")
	fmt.Fprintln(o.Tell, "  2. Also on a network address, over HTTPS, with a login by the maintainer secret")
	for network := false; !network; {
		answer, err := o.Line("Choice [1]: ")
		if errors.Is(err, io.EOF) {
			return false, nil, nil
		}
		if err != nil {
			return false, nil, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "", "1":
			return true, nil, nil
		case "2":
			network = true
		default:
			fmt.Fprintln(o.Tell, "Type 1 or 2.")
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
			return false, nil, nil
		}
		if err != nil {
			return false, nil, err
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
			return false, nil, nil
		}
		if err != nil {
			return false, nil, err
		}
		if port = strings.TrimSpace(port); port == "" {
			port = strconv.Itoa(DefaultDashboardPort)
		}
		a, err := o.ResolveDashboard(ctx, net.JoinHostPort(host, port))
		if err != nil {
			fmt.Fprintln(o.Tell, "This address is refused: "+err.Error())
			continue
		}
		return false, &a, nil
	}
}

// offerSecret asks to set the maintainer secret, which the network login needs. It reports
// whether a secret was set. A generated secret is shown once on the terminal, never in the
// report. Ended input and "not now" set nothing.
func offerSecret(ctx context.Context, o Options) (bool, error) {
	bearer, err := core.ReadSecret(o.StateDir)
	if err != nil {
		return false, err
	}
	fmt.Fprintln(o.Tell, "The network dashboard needs the maintainer secret for its login, and no secret is set.")
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
