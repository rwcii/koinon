// Command koinon provides the Go daemon core. Other sprint commands are added
// only after their prerequisite chunks merge.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(out, "Usage: koinon serve [--state-dir DIR] [--listen 127.0.0.1:PORT] [--listen-v6 [::1]:PORT]\n       koinon status [--state-dir DIR] [--address 127.0.0.1:PORT]")
		return err
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	var listen, listen6, address *string
	switch args[0] {
	case "serve":
		listen = flags.String("listen", "127.0.0.1:47671", "IPv4 loopback listener")
		listen6 = flags.String("listen-v6", "[::1]:47671", "IPv6 loopback listener")
	case "status":
		address = flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	default:
		return errors.New("unknown command; use koinon --help")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("invalid command options; use koinon --help")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected command arguments")
	}
	if args[0] == "status" {
		secret, err := core.ReadSecret(*state)
		if err != nil {
			return errors.New("cannot read private daemon secret")
		}
		status, err := core.GetStatus(ctx, *address, secret)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(status))
		return err
	}
	d, err := core.Start(core.Config{StateDir: *state, Listen: []string{*listen, *listen6}})
	if err != nil {
		return err
	}
	defer d.Close()
	if err := json.NewEncoder(out).Encode(map[string]any{"ok": true, "daemon": "running", "listeners": d.Addresses()}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return d.Close()
	case err := <-d.Errors():
		return err
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		// Startup errors must never include the authentication secret.
		json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "code": "daemon_error", "detail": err.Error()})
		os.Exit(1)
	}
}
