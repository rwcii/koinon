package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

func libraryCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "state" || args[0] == "decision") {
		return libraryStateCommand(ctx, args, out)
	}
	if len(args) == 0 || args[0] != "list" && args[0] != "read" {
		return usageError{"invalid_request", "use koinon library list or koinon library read ITEM_ID"}
	}
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("library "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	as := flags.String("as", "", "calling session as FAMILY:ID")
	var after *string
	var limit *int
	if op == "list" {
		after = flags.String("after", "", "catalog ID from the previous page")
		limit = flags.Int("limit", 0, "page size (default 16; maximum 32)")
	}
	itemID := ""
	if op == "read" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		itemID, rest = rest[0], rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	if op == "read" && itemID == "" && flags.NArg() == 1 {
		itemID = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError{"invalid_request", "unexpected library arguments"}
	}
	if op == "read" && itemID == "" {
		return usageError{"invalid_request", "missing catalog ITEM_ID"}
	}
	family, id, found := strings.Cut(*as, ":")
	if !found || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
	}
	body := map[string]any{"caller": core.Key{Family: family, ID: id}}
	if op == "list" {
		body["after"], body["limit"] = *after, *limit
	} else {
		body["item_id"] = itemID
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	data, err := core.Call(ctx, *address, secret, "/v1/library/"+op, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
