package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/remote"
	qrcode "github.com/skip2/go-qrcode"
)

func remoteQR(link string) (string, error) {
	// Only a validated, credential-free dashboard URL enters the encoder.
	const suffix = "/dashboard/"
	if len(link) <= len(suffix) || link[len(link)-len(suffix):] != suffix {
		return "", errors.New("invalid remote URL")
	}
	if err := remote.ValidateMapping(remote.Mapping{Origin: link[:len(link)-len(suffix)], Target: "http://127.0.0.1:1"}); err != nil {
		return "", err
	}
	qr, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return qr.ToSmallString(false), nil
}

func remoteCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] != "enable" && args[0] != "status" && args[0] != "disable" {
		return usageError{"invalid_request", "use koinon remote enable|status|disable"}
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("remote "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	asJSON := flags.Bool("json", false, "print status as JSON")
	httpsPort := flags.Int("https-port", 0, "Tailscale Serve HTTPS port for enable; 0 keeps the saved port, or 443")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return usageError{"invalid_request", "invalid remote command options; use koinon --help"}
	}
	portSet := false
	flags.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "https-port" })
	if portSet && args[0] != "enable" {
		return usageError{"invalid_request", "--https-port applies only to koinon remote enable"}
	}
	if *httpsPort != 0 && !remote.ValidHTTPSPort(*httpsPort) {
		return usageError{"invalid_request", "--https-port must be 1 to 65535"}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil && args[0] != "status" {
		return usageError{"daemon_unavailable", "daemon unavailable: run koinon install, then repeat this command: " + err.Error()}
	}
	var status core.RemoteStatus
	if err == nil {
		if args[0] != "status" {
			var body any = struct{}{}
			if args[0] == "enable" {
				body = struct {
					HTTPSPort int `json:"https_port,omitempty"`
				}{*httpsPort}
			}
			call := func() error {
				_, err := core.Call(ctx, *address, bearer, "/v1/remote/"+args[0], body)
				return err
			}
			err := call()
			if err != nil && args[0] == "enable" && !*asJSON {
				err = grantOperator(ctx, nil, in, err, call)
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return remoteCallError(args[0], err)
			}
		}
		var data []byte
		data, err = core.Call(ctx, *address, bearer, "/v1/remote/status", nil)
		if err == nil {
			err = json.Unmarshal(data, &status)
		}
	}
	if err != nil {
		if args[0] != "status" {
			return remoteCallError(args[0], err)
		}
		status, err = remoteOfflineStatus(ctx, *state, nil)
		if err != nil {
			return err
		}
	}
	return printRemoteStatus(out, status, args[0], *asJSON)
}

// The operator step's seams: the platform, the account, the terminal and the grant command.
var (
	operatorGOOS     = runtime.GOOS
	operatorAccount  = remote.OperatorAccount
	operatorTerminal = func(ctx context.Context, in io.Reader) *terminal { return openTerminal(ctx, in, os.Stderr) }
	operatorRun      = func(cmd *exec.Cmd) error { return cmd.Run() }
)

// operatorGrant is the one fixed command that makes name the Tailscale operator. It runs
// without a shell; sudo reads the password from the terminal, and Koinon never sees it. Its
// output goes to stderr, so stdout keeps only the command's report.
func operatorGrant(ctx context.Context, t *terminal, name string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sudo", "tailscale", "set", "--operator="+name)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = t.file, t.tell, t.tell
	return cmd
}

// grantOperator answers the daemon's tailscale_operator_required refusal of an enable at a
// Linux terminal (TAILSCALE-AUTO-656 in PDR 010): it asks once, with no as the default, runs
// the fixed sudo command on yes and then asks the daemon to enable once more. No, the end of
// input, a sudo failure, another refusal, another platform, an account without a plain name
// or no terminal keep the refusal with its manual step.
// A command that already holds the terminal, such as install (#665), passes it as t, so that
// its one reader serves every question; otherwise the terminal is opened on in.
func grantOperator(ctx context.Context, t *terminal, in io.Reader, refusal error, enable func() error) error {
	var refused core.RefusedError
	if !errors.As(refusal, &refused) || refused.Code != remote.OperatorRequired || operatorGOOS != "linux" {
		return refusal
	}
	name := operatorAccount()
	if name == "" {
		return refusal
	}
	if t == nil {
		t = operatorTerminal(ctx, in)
	}
	if t == nil {
		return refusal
	}
	answer, err := t.line("Grant " + name + " control of this computer's Tailscale settings, including more than Serve, by running sudo tailscale set --operator=" + name + "? [y/N] ")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return refusal
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return refusal
	}
	if err := operatorRun(operatorGrant(ctx, t, name)); err != nil {
		fmt.Fprintf(t.tell, "sudo tailscale set --operator=%s did not complete: %v\n", name, err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return refusal
	}
	return enable()
}

// remoteCallError keeps the daemon's refusal and its code; any other failure of a call
// means that the daemon did not answer, so it is daemon_unavailable.
func remoteCallError(action string, err error) error {
	var refused core.RefusedError
	if errors.As(err, &refused) {
		return fmt.Errorf("remote %s: %w", action, err)
	}
	return usageError{"daemon_unavailable", fmt.Sprintf("daemon unavailable or too old: install this version, then repeat koinon remote %s: %v", action, err)}
}

// An unavailable daemon makes readiness unknown, not configured=false. Status remains
// read-only and inspects saved configuration and the installed dependency separately.
func remoteOfflineStatus(ctx context.Context, root string, adapter remote.Adapter) (core.RemoteStatus, error) {
	result := core.RemoteStatus{Listener: "unknown", Serve: "unknown", Phone: "unverified", Error: "Daemon unavailable or too old; inspect koinon install --plan, install this version, then repeat koinon remote status."}
	config, err := core.ReadRemoteConfig(root)
	if err != nil {
		return result, err
	}
	if config != nil {
		result.Configured = config.Enabled
		result.URL = config.Mapping.Origin + "/dashboard/"
	}
	observed, err := (remote.Manager{Adapter: adapter}).Inspect(ctx)
	if err != nil {
		result.Error += " " + err.Error()
	} else {
		result.Tailscale = &observed
		if config != nil && !config.Enabled {
			result.Serve = "off"
			if config.Owned {
				result.Serve = "cleanup pending"
			}
		} else if config != nil {
			if observed.MappingPresent(config.Mapping) {
				result.Serve = "mapping present; listener readiness unknown"
			} else {
				result.Serve = "missing"
			}
		}
	}
	return result, nil
}

func printRemoteStatus(out io.Writer, status core.RemoteStatus, action string, asJSON bool) error {
	if asJSON {
		return printJSON(out, struct {
			OK bool `json:"ok"`
			core.RemoteStatus
		}{true, status})
	}
	fmt.Fprintf(out, "Remote configured: %t\nDashboard listener: %s\nServe: %s\nPhone access: unverified\n", status.Configured, status.Listener, status.Serve)
	if status.Tailscale != nil {
		fmt.Fprintf(out, "Tailscale: %s\n", status.Tailscale.BackendState)
	}
	if status.URL != "" {
		if status.Configured {
			fmt.Fprintln(out, status.URL)
		} else {
			fmt.Fprintln(out, "Saved URL (remote disabled): "+status.URL)
		}
	}
	if status.Error != "" {
		fmt.Fprintln(out, "Next step: "+status.Error)
	}
	if status.PolicyHint != "" {
		fmt.Fprintln(out, status.PolicyHint)
		if status.PolicyRule != "" {
			fmt.Fprintln(out, "  "+status.PolicyRule)
		}
	}
	if action == "enable" && status.Configured && status.Listener == "ready" && status.Error == "" {
		qr, err := remoteQR(status.URL)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, qr)
		fmt.Fprintln(out, "Sign in with the existing maintainer secret. The QR contains only the URL.")
		fmt.Fprintln(out, "Verify on the phone: turn Wi-Fi off, read agents, send a message and submit a decision verdict. Check receipt and the verdict on the host. Keep the host awake and Tailscale connected.")
	}
	return nil
}
