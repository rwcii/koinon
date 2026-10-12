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

// roomCommand is the command path of room_list and room_members (#217, chunk 01).
func roomCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "list" && args[0] != "members" {
		return usageError{"invalid_request", "use koinon room list or koinon room members ROOM_ID"}
	}
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("room "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	as := flags.String("as", "", "calling session as FAMILY:ID")
	limit := flags.Int("limit", 0, "page size (default and maximum 100)")
	var afterRoom *string
	var afterMember *int64
	if op == "list" {
		afterRoom = flags.String("after", "", "room ID from the previous page")
	} else {
		afterMember = flags.Int64("after", 0, "member ordinal from the previous page")
	}
	roomID := ""
	if op == "members" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		roomID, rest = rest[0], rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	if op == "members" && roomID == "" && flags.NArg() == 1 {
		roomID = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError{"invalid_request", "unexpected room arguments"}
	}
	if op == "members" && roomID == "" {
		return usageError{"invalid_request", "missing ROOM_ID"}
	}
	family, id, found := strings.Cut(*as, ":")
	if !found || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
	}
	body := map[string]any{"caller": core.Key{Family: family, ID: id}, "limit": *limit}
	if op == "list" {
		body["after"] = *afterRoom
	} else {
		body["room_id"], body["after"] = roomID, *afterMember
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	data, err := core.Call(ctx, *address, secret, "/v1/rooms/"+op, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
