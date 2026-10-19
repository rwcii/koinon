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

// roomCommand is the command path of room_list, room_members (#217, chunk 01), room_post and
// room_read (chunk 02).
func roomCommand(ctx context.Context, args []string, out io.Writer) error {
	ops := map[string]bool{"list": true, "members": true, "post": true, "read": true}
	if len(args) == 0 || !ops[args[0]] {
		return usageError{"invalid_request", "use koinon room list, members ROOM_ID, post ROOM_ID BODY or read ROOM_ID"}
	}
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("room "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	as := flags.String("as", "", "calling session as FAMILY:ID")
	var limit *int
	var afterRoom, key *string
	var afterSeq *int64
	var deadline *float64
	switch op {
	case "list":
		afterRoom = flags.String("after", "", "room ID from the previous page")
		limit = flags.Int("limit", 0, "page size (default and maximum 100)")
	case "members":
		afterSeq = flags.Int64("after", 0, "member ordinal from the previous page")
		limit = flags.Int("limit", 0, "page size (default and maximum 100)")
	case "read":
		afterSeq = flags.Int64("after", 0, "room sequence from the previous page")
		limit = flags.Int("limit", 0, "page size (default and maximum 100)")
	case "post":
		key = flags.String("key", "", "idempotency key, fixed before the first attempt")
		deadline = flags.Float64("deadline", 0, "retry deadline in Unix seconds, at most 24 hours ahead")
	}
	// Positional arguments (ROOM_ID, and BODY for a post) and flags can come in any order;
	// after "--" every argument is positional.
	var positional []string
	for {
		if err := flags.Parse(rest); err != nil {
			return usageError{"invalid_request", err.Error()}
		}
		left := flags.Args()
		if len(left) < len(rest) && rest[len(rest)-len(left)-1] == "--" {
			positional = append(positional, left...)
			break
		}
		if len(left) == 0 {
			break
		}
		positional, rest = append(positional, left[0]), left[1:]
	}
	want := map[string]int{"list": 0, "members": 1, "read": 1, "post": 2}[op]
	if len(positional) != want {
		return usageError{"invalid_request", fmt.Sprintf("koinon room %s takes %d arguments", op, want)}
	}
	family, id, found := strings.Cut(*as, ":")
	if !found || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
	}
	body := map[string]any{"caller": core.Key{Family: family, ID: id}}
	switch op {
	case "list":
		body["after"], body["limit"] = *afterRoom, *limit
	case "members", "read":
		body["room_id"], body["after"], body["limit"] = positional[0], *afterSeq, *limit
	case "post":
		if *key == "" || *deadline == 0 {
			return usageError{"invalid_request", "koinon room post needs --key and --deadline"}
		}
		body["room_id"], body["body"], body["key"], body["deadline"] = positional[0], positional[1], *key, *deadline
	}
	if err := resolveAddress(flags); err != nil {
		return err
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
