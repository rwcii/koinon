package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/install"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
)

// configureTerminal opens the terminal of koinon remote configure; tests replace it.
var configureTerminal = func(ctx context.Context, in io.Reader) *terminal { return openTerminal(ctx, in, os.Stderr) }

// remoteConfigure shows where the dashboard opens and changes the install answer in one step
// (#685). Every option is checked before anything is read or changed.
func remoteConfigure(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("remote configure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	prefix := flags.String("prefix", "", "Go installation prefix, for the daemon restart")
	address := addressFlag(flags)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	choice := flags.String("choice", "", "the answer: local, tailnet or network")
	httpsPort := flags.Int("https-port", 0, "Tailscale Serve HTTPS port for --choice tailnet")
	dashboardAddress := flags.String("dashboard-address", "", "dashboard HTTPS HOST:PORT for --choice network")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return usageError{"invalid_request", "invalid remote configure options; use koinon --help"}
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch *choice {
	case "", install.DashboardLocal, install.DashboardTailnet, install.DashboardNetwork:
	default:
		return usageError{"invalid_request", "--choice takes local, tailnet or network"}
	}
	if given["choice"] && *choice == "" {
		return usageError{"invalid_request", "--choice takes local, tailnet or network"}
	}
	if given["https-port"] {
		if *choice != install.DashboardTailnet {
			return usageError{"invalid_request", "--https-port applies only with --choice tailnet"}
		}
		if !remote.ValidHTTPSPort(*httpsPort) {
			return usageError{"invalid_request", "--https-port must be 1 to 65535"}
		}
	}
	if given["dashboard-address"] {
		if *choice != install.DashboardNetwork {
			return usageError{"invalid_request", "--dashboard-address applies only with --choice network; use --choice local or tailnet to remove the network address"}
		}
		host, port, err := net.SplitHostPort(*dashboardAddress)
		n, perr := strconv.Atoi(port)
		if err != nil || host == "" || perr != nil || !core.ValidPort(n) || port != strconv.Itoa(n) {
			return usageError{"dashboard_address_invalid", "--dashboard-address takes HOST:PORT, with a port from 1 to 65535"}
		}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	o := install.Options{Prefix: *prefix, StateDir: *state, Address: *address, Version: version, Tell: os.Stderr}
	current, err := install.ReadDashboardCurrent(ctx, o)
	if err != nil {
		return coded(err)
	}
	var t *terminal
	if !*asJSON {
		t = configureTerminal(ctx, in)
	}
	if *choice == "" && t == nil {
		// Without an answer and without a terminal, the command only shows the choice.
		if *asJSON {
			return printJSON(out, struct {
				OK      bool                     `json:"ok"`
				Current install.DashboardCurrent `json:"current"`
			}{true, current})
		}
		for _, line := range install.DescribeDashboard(current) {
			fmt.Fprintln(out, line)
		}
		fmt.Fprintln(out, "To change it, run koinon remote configure at a terminal, or give --choice local, tailnet or network.")
		return nil
	}
	if *choice == "" {
		return usageError{"remote_configure_unavailable", "this build asks no question; give --choice local, tailnet or network"}
	}
	if t != nil {
		o.Line, o.NewSecret = t.line, t.newSecret
		o.RemoteEnable = installRemoteEnable(t)
	}
	change, err := install.ConfigureDashboard(ctx, o, install.DashboardTarget{Choice: *choice, HTTPSPort: *httpsPort, Address: *dashboardAddress})
	if *asJSON {
		printJSON(out, struct {
			OK     bool                    `json:"ok"`
			Change install.DashboardChange `json:"change"`
		}{err == nil, change})
		return coded(err)
	}
	printDashboardChange(out, change)
	return coded(err)
}

// printDashboardChange shows each step with its effect, then the choice after the change.
func printDashboardChange(out io.Writer, c install.DashboardChange) {
	if c.Steps != nil && !c.Changed {
		fmt.Fprintln(out, "No change: the dashboard already opens this way.")
	}
	for _, s := range c.Steps {
		line := "Done: " + s.Step
		if !s.Done {
			line = "Failed: " + s.Step + ": " + s.Error + ". To finish it, run " + s.Finish
		}
		if s.Effect != "" && s.Done {
			line += ". " + s.Effect
		}
		fmt.Fprintln(out, line)
	}
	if c.After != nil {
		for _, line := range install.DescribeDashboard(*c.After) {
			fmt.Fprintln(out, line)
		}
	}
}
