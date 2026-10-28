package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// decisionCommand is the command path of decision_create, decision_edit, decision_list and
// decision_get (#206, chunk 01) and decision_history, decision_pending, decision_ack,
// decision_close and decision_restore (chunk 02), decision_withdraw (#504) and
// decision_record (#536).
func decisionCommand(ctx context.Context, args []string, out io.Writer) error {
	ops := map[string]bool{"create": true, "edit": true, "list": true, "get": true, "history": true, "pending": true,
		"ack": true, "close": true, "withdraw": true, "restore": true, "record": true}
	if len(args) == 0 || !ops[args[0]] {
		return usageError{"invalid_request", "use koinon decision create, edit, list, get, history, pending, ack, close, withdraw, restore or record"}
	}
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("decision "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	as := flags.String("as", "", "calling session as FAMILY:ID")
	strs := map[string]*string{}
	ints := map[string]*int64{}
	var commands, recipients, verdicts repeated
	var deadline *float64
	var limit *int
	noCommands, noRecipients, withdrawals := new(bool), new(bool), new(bool)
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
	case "record":
		text("reference", "item reference")
		text("directory", "a checkout of the repository whose DECISIONS.md gets the entries (default: the current directory)")
	case "restore":
		text("reference", "item reference")
		ints["if-revision"] = flags.Int64("if-revision", 0, "the revision that was read, to reopen a withdrawn item")
	case "history":
		text("reference", "item reference")
		withdrawals = flags.Bool("withdrawals", false, "page the item's agent withdrawals instead of its verdicts")
		ints["after"] = flags.Int64("after", 0, "verdict or withdrawal ID from the previous page's next_after")
		limit = flags.Int("limit", 0, "page size (default and maximum 16)")
	case "pending":
		ints["after"] = flags.Int64("after", 0, "verdict ID from the previous page's next_after")
		limit = flags.Int("limit", 0, "page size (default and maximum 100)")
	case "ack":
		flags.Var(&verdicts, "verdict", "handled verdict ID (repeat for each, at most 32)")
	case "close":
		text("reference", "item reference")
		ints["if-revision"] = flags.Int64("if-revision", 0, "the revision that was read")
		text("note", "optional Markdown outcome note")
		text("key", "idempotency key, fixed before the first attempt")
		deadline = flags.Float64("deadline", 0, "retry deadline in Unix seconds, at most 24 hours ahead")
	case "withdraw":
		text("reference", "item reference")
		ints["if-revision"] = flags.Int64("if-revision", 0, "the revision that was read")
		text("reason", "one-line reason: done, superseded by REF, answered in chat or no longer valid")
		text("key", "idempotency key, fixed before the first attempt")
		deadline = flags.Float64("deadline", 0, "retry deadline in Unix seconds, at most 24 hours ahead")
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
	case "list", "history", "pending":
		body["limit"] = *limit
		if *withdrawals {
			body["withdrawals"] = true
		}
	case "ack":
		ids := []int64{}
		for _, v := range verdicts {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 {
				return usageError{"invalid_request", "--verdict takes a verdict ID"}
			}
			ids = append(ids, n)
		}
		body["verdict_ids"] = ids
	case "close":
		if !set["if-revision"] || !set["key"] || !set["deadline"] {
			return usageError{"invalid_request", "koinon decision close needs --if-revision, --key and --deadline"}
		}
		body["deadline"] = *deadline
	case "withdraw":
		if !set["if-revision"] || !set["reason"] || !set["key"] || !set["deadline"] {
			return usageError{"invalid_request", "koinon decision withdraw needs --if-revision, --reason, --key and --deadline"}
		}
		body["deadline"] = *deadline
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	if op == "record" {
		return decisionRecord(ctx, out, *address, secret, body)
	}
	data, err := core.Call(ctx, *address, secret, "/v1/decisions/"+op, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// decisionRecord appends the item's verdicts to DECISIONS.md at the root of the checkout that
// --directory names, or of the current directory (#536).
func decisionRecord(ctx context.Context, out io.Writer, address, secret string, body map[string]any) error {
	reference, _ := body["reference"].(string)
	directory, _ := body["directory"].(string)
	if reference == "" {
		return usageError{"invalid_request", "koinon decision record needs --reference"}
	}
	checkout, err := core.CheckoutResource(ctx, directory)
	if err != nil {
		return usageError{"repo_unresolved", "--directory or the current directory must be in a Git checkout"}
	}
	call := func(ctx context.Context, path string, request map[string]any) ([]byte, error) {
		request["caller"] = body["caller"]
		return core.Call(ctx, address, secret, path, request)
	}
	item, verdicts, err := core.CollectDecisionRecord(ctx, call, reference)
	if err != nil {
		return err
	}
	if err := core.CheckRecordRepository(item, checkout.Repository); err != nil {
		return err
	}
	recorded, err := core.RecordDecision(checkout.Directory, reference, item.Where, verdicts)
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"ok": true, "result": recorded})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
