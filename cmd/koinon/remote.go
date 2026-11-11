package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

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

func remoteCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "enable" && args[0] != "status" && args[0] != "disable" {
		return errors.New("use koinon remote enable|status|disable")
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
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid remote command options; use koinon --help")
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil && args[0] != "status" {
		return fmt.Errorf("daemon unavailable: run koinon install, then repeat this command: %w", err)
	}
	var status core.RemoteStatus
	if err == nil {
		if args[0] != "status" {
			if _, err := core.Call(ctx, *address, bearer, "/v1/remote/"+args[0], struct{}{}); err != nil {
				return fmt.Errorf("remote %s: %w", args[0], err)
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
			return fmt.Errorf("daemon unavailable or too old: install this version, then repeat koinon remote %s: %w", args[0], err)
		}
		status, err = remoteOfflineStatus(ctx, *state, nil)
		if err != nil {
			return err
		}
	}
	return printRemoteStatus(out, status, args[0], *asJSON)
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
		return printJSON(out, status)
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
