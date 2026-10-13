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

// decisionCommand is the command path of decision_create, decision_edit, decision_list and
// decision_get (#206, chunk 01).
func decisionCommand(ctx context.Context, args []string, out io.Writer) error {
	ops := map[string]bool{"create": true, "edit": true, "list": true, "get": true}
	if len(args) == 0 || !ops[args[0]] {
		return usageError{"invalid_request", "use koinon decision create, edit, list or get"}
	}
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("decision "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	as := flags.String("as", "", "calling session as FAMILY:ID")
	strs := map[string]*string{}
	ints := map[string]*int64{}
	var commands, recipients repeated
	var deadline *float64
	var limit *int
	noCommands, noRecipients := new(bool), new(bool)
	text := func(name, usage string) { strs[name] = flags.String(name, "", usage) }
	switch op {
	case "create", "edit":
		text("reference", "item reference: 1 to 32 characters of A-Z, a-z, 0-9 and -")
		ints["order"] = flags.Int64("order", 0, "item order")
		for _, name := range []string{"title", "where", "agent-text", "context", "recommendation", "kind"} {
			text(name, name)
		}
		flags.Var(&commands, "command", "terminal-copy command (repeat for each)")
		flags.Var(&recipients, "recipient", "participant address or peer name (repeat for each)")
		noCommands = flags.Bool("no-commands", false, "store an empty command list")
		noRecipients = flags.Bool("no-recipients", false, "store an empty recipient list")
		if op == "create" {
			text("key", "idempotency key, fixed before the first attempt")
			deadline = flags.Float64("deadline", 0, "retry deadline in Unix seconds, at most 24 hours ahead")
		} else {
			ints["if-revision"] = flags.Int64("if-revision", 0, "the revision that was read")
		}
	case "list":
		text("filter", "waiting, active, in_progress, archived or mine")
		ints["after"] = flags.Int64("after", 0, "item ID from the previous page's next_after")
		limit = flags.Int("limit", 0, "page size (default and maximum 100)")
	case "get":
		text("reference", "item reference")
	}
	if err := flags.Parse(rest); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	if flags.NArg() != 0 {
		return usageError{"invalid_request", "koinon decision " + op + " takes flags only"}
	}
	family, id, found := strings.Cut(*as, ":")
	if !found || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	body := map[string]any{"caller": core.Key{Family: family, ID: id}}
	for name, value := range strs {
		if set[name] {
			body[strings.ReplaceAll(name, "-", "_")] = *value
		}
	}
	for name, value := range ints {
		if set[name] {
			body[strings.ReplaceAll(name, "-", "_")] = *value
		}
	}
	if set["command"] && *noCommands || set["recipient"] && *noRecipients {
		return usageError{"invalid_request", "use --command or --no-commands, and --recipient or --no-recipients, not both"}
	}
	if set["command"] || *noCommands {
		body["commands"] = append([]string{}, commands...)
	}
	if set["recipient"] || *noRecipients {
		body["recipients"] = append([]string{}, recipients...)
	}
	switch op {
	case "create":
		if !set["key"] || !set["deadline"] {
			return usageError{"invalid_request", "koinon decision create needs --key and --deadline"}
		}
		body["deadline"] = *deadline
	case "edit":
		if !set["if-revision"] {
			return usageError{"invalid_request", "koinon decision edit needs --if-revision"}
		}
	case "list":
		body["limit"] = *limit
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	data, err := core.Call(ctx, *address, secret, "/v1/decisions/"+op, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
