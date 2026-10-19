package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// cleanupCommand runs koinon sessions list|purge|unpurge|retire, koinon inbox clear and
// koinon maintenance sweep (#388). A change without --yes prints the selection and changes
// nothing; unpurge and the sweep need no --yes. The daemon records each changed session in
// the audit log with the origin "cli".
func cleanupCommand(ctx context.Context, command, op string, args []string, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(command+" "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	var filter core.CleanupFilter
	var inactive string
	var yes, asJSON bool
	if command != "maintenance" {
		flags.StringVar(&filter.State, "state", "", "active, expired, retired or purge-marked")
		flags.StringVar(&filter.Family, "family", "", "agent family")
		flags.StringVar(&inactive, "inactive-for", "", "inactive for at least this long, such as 72h or 30d")
		if op == "list" {
			flags.BoolVar(&asJSON, "json", false, "print the daemon's JSON reply")
		} else if op != "unpurge" {
			flags.BoolVar(&yes, "yes", false, "make the change; without it, only show the selection")
		}
	}
	// Flags may come before or after the peer names.
	for {
		if err := flags.Parse(args); err != nil {
			return usageError{"invalid_request", err.Error()}
		}
		if flags.NArg() == 0 {
			break
		}
		filter.Names, args = append(filter.Names, flags.Arg(0)), flags.Args()[1:]
	}
	if command == "maintenance" && len(filter.Names) > 0 {
		return usageError{"invalid_request", "koinon maintenance sweep takes no operands"}
	}
	if inactive != "" {
		seconds, ok := parseInactivity(inactive)
		if !ok {
			return usageError{"invalid_request", "--inactive-for is a duration such as 72h or 30d"}
		}
		filter.InactiveForSeconds = seconds
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	if command == "maintenance" {
		data, err := core.Call(ctx, *address, secret, "/v1/maintenance/sweep", map[string]any{})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	action := op
	if command == "inbox" {
		action = "clear"
	}
	data, err := core.Call(ctx, *address, secret, "/v1/cleanup/sessions", map[string]any{"action": action, "apply": yes || op == "unpurge",
		"names": filter.Names, "state": filter.State, "family": filter.Family, "inactive_for_seconds": filter.InactiveForSeconds})
	if err != nil {
		return err
	}
	if op == "list" && !asJSON {
		return printCleanup(out, data)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// parseInactivity reads a Go duration or a whole number of days such as 30d, in seconds.
func parseInactivity(value string) (int64, bool) {
	if days, ok := strings.CutSuffix(value, "d"); ok {
		n, err := strconv.ParseInt(days, 10, 64)
		if err != nil || n < 1 || n > 36500 {
			return 0, false
		}
		return n * 86400, true
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < time.Second {
		return 0, false
	}
	return int64(d / time.Second), true
}

// printCleanup prints koinon sessions list as a table.
func printCleanup(out io.Writer, data []byte) error {
	var reply struct {
		Sessions  []core.CleanupSession `json:"sessions"`
		Truncated bool                  `json:"truncated"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
	fmt.Fprintln(out, "SESSION                          FAMILY      STATE     INACTIVE SINCE        UNACKED  CLAIM  PURGE")
	for _, c := range reply.Sessions {
		since := "-"
		if c.InactiveSince != 0 {
			since = time.UnixMilli(c.InactiveSince).UTC().Format("2006-01-02 15:04 UTC")
		}
		fmt.Fprintf(out, "%-32s %-11s %-9s %-21s %7d  %-5s  %s\n", c.Name, c.Family, c.State, since, c.Unacknowledged,
			map[bool]string{true: "yes", false: "-"}[c.LiveClaim], map[bool]string{true: "marked", false: "-"}[c.PurgeMarked])
	}
	if reply.Truncated {
		fmt.Fprintln(out, "(more sessions match; narrow the selection)")
	}
	return nil
}

// claimsRoomsCommand runs koinon claims release, koinon rooms prune and koinon rooms delete
// (#388). Each needs --yes to change anything; without it, it prints what it would change.
func claimsRoomsCommand(ctx context.Context, command, op string, args []string, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(command+" "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	yes := flags.Bool("yes", false, "make the change; without it, only show what would change")
	var heldBy repeated
	if command == "claims" {
		flags.Var(&heldBy, "held-by", "peer name of an expired or retired session (repeat for each)")
	}
	var operands []string
	for {
		if err := flags.Parse(args); err != nil {
			return usageError{"invalid_request", err.Error()}
		}
		if flags.NArg() == 0 {
			break
		}
		operands, args = append(operands, flags.Arg(0)), flags.Args()[1:]
	}
	switch {
	case command == "claims" && len(operands) > 0:
		return usageError{"invalid_request", "koinon claims release takes no operands; name holders with --held-by"}
	case op == "delete" && len(operands) != 1:
		return usageError{"invalid_request", "koinon rooms delete takes one ROOM_ID"}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	var path string
	var body map[string]any
	switch {
	case command == "claims":
		path, body = "/v1/cleanup/claims", map[string]any{"held_by": []string(heldBy), "apply": *yes}
	case op == "prune":
		path, body = "/v1/cleanup/rooms", map[string]any{"room_ids": operands, "apply": *yes}
	case !*yes:
		data, err := core.Call(ctx, *address, secret, "/v1/cleanup/rooms", map[string]any{"room_ids": operands})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "{\"ok\":true,\"applied\":false,\"room_id\":%q,\"note\":\"add --yes to delete the room with its members and posts\",\"inactive_members\":%s}\n",
			operands[0], inactiveMembers(data))
		return err
	default:
		path, body = "/v1/rooms/delete", map[string]any{"room_id": operands[0]}
	}
	data, err := core.Call(ctx, *address, secret, path, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// inactiveMembers is the members list of a /v1/cleanup/rooms reply, or [].
func inactiveMembers(data []byte) string {
	var reply struct {
		Members json.RawMessage `json:"members"`
	}
	if json.Unmarshal(data, &reply) != nil || len(reply.Members) == 0 {
		return "[]"
	}
	return string(reply.Members)
}
