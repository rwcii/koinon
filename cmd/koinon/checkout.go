package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// checkoutCommand is local and read-only: no daemon, caller, state or lease is needed
// to derive the resource that participants can explicitly include in work-start.
func checkoutCommand(ctx context.Context, args []string, out io.Writer) error {
	operation := ""
	if len(args) > 0 && (args[0] == "status" || args[0] == "request") {
		operation, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("work checkout", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "directory in the Git worktree, default current directory")
	var state, address, as, note *string
	if operation != "" {
		root, err := platform.DefaultStateDir()
		if err != nil {
			return err
		}
		state = flags.String("state-dir", root, "private Go state directory")
		address = flags.String("address", "127.0.0.1:47671", "daemon loopback address")
		as = flags.String("as", "", "calling session as FAMILY:ID")
		if operation == "request" {
			note = flags.String("note", "", "reason for requesting the writer role")
		}
	}
	if err := flags.Parse(args); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	if flags.NArg() != 0 {
		return usageError{"invalid_request", "unexpected arguments"}
	}
	checkout, err := core.CheckoutResource(ctx, *directory)
	if err != nil {
		return usageError{"repo_unresolved", "cannot derive a checkout resource from this directory"}
	}
	if operation != "" {
		family, id, found := strings.Cut(*as, ":")
		if !found || family == "" || id == "" {
			return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
		}
		body := map[string]any{"caller": core.Key{Family: family, ID: id}, "directory": checkout.Directory}
		if note != nil {
			body["note"] = *note
		}
		secret, err := core.ClientSecret(*state, *address)
		if err != nil {
			return err
		}
		data, err := core.Call(ctx, *address, secret, "/v1/work/checkout-"+operation, body)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"ok": true, "result": checkout})
}
