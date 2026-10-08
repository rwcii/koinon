package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// usageError is a refusal made before the daemon is contacted.
type usageError struct{ code, message string }

func (e usageError) Error() string { return e.message }

type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

var (
	workIntegers = map[string]bool{"revision": true, "if_revision": true, "claim_generation": true, "if_claim_revision": true,
		"limit": true, "lease_seconds": true, "renew_for": true}
	workNumbers = map[string]bool{"progress_deadline": true, "deadline": true}
	workBools   = map[string]bool{"stale": true, "blocked": true}
)

// workCommand runs koinon work OPERATION and koinon claim renew through the daemon's
// API. It checks the unconditional required options before it contacts the daemon.
func workCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) >= 2 && args[0] == "work" && args[1] == "checkout" {
		return checkoutCommand(ctx, args[2:], out)
	}
	if len(args) < 2 {
		return usageError{"invalid_request", "use koinon work <create|get|list|propose|edit|start|update|release|finish> or koinon claim renew"}
	}
	op := args[0] + "-" + args[1]
	if args[0] == "claim" && args[1] != "renew" || !known(op) {
		return usageError{"invalid_request", "unknown work operation " + args[1]}
	}
	rest := args[2:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	as := flags.String("as", "", "calling session as FAMILY:ID")
	consumer := flags.String("consumer", "", "stable consumer key")
	fields := core.WorkFields(op)
	if op != "work-get" && op != "work-list" {
		fields = append(fields, "author", "key", "deadline")
	}
	texts := map[string]*string{}
	var references, paths, exacts repeated
	clear := false
	for _, field := range fields {
		name := strings.ReplaceAll(field, "_", "-")
		switch {
		case field == "work_id":
		case field == "references":
			flags.Var(&references, "reference", "inert evidence reference, repeated")
		case field == "resources":
			flags.Var(&paths, "path-resource", "repository-relative path claim, repeated")
			flags.Var(&exacts, "exact-resource", "exact resource key claim, repeated")
		case workBools[field]:
			flags.Bool(name, false, field)
		case workIntegers[field]:
			flags.Int64(name, 0, field)
		case workNumbers[field]:
			flags.Float64(name, 0, field)
		default:
			texts[field] = flags.String(name, "", field)
		}
	}
	if op == "work-propose" {
		flags.BoolVar(&clear, "clear-assignee", false, "clear the proposed assignee")
	}
	// The work ID may come before or after the options.
	workID := ""
	positional := op != "work-create" && op != "work-list"
	if positional && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		workID, rest = rest[0], rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	if positional && workID == "" && flags.NArg() == 1 {
		workID = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usageError{"invalid_request", "unexpected arguments"}
	}
	body := map[string]any{}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) {
		field := strings.ReplaceAll(f.Name, "-", "_")
		given[field] = true
		switch {
		case f.Name == "reference":
			body["references"] = []string(references)
		case f.Name == "path-resource" || f.Name == "exact-resource" || f.Name == "clear-assignee":
		case workBools[field] || workIntegers[field] || workNumbers[field]:
			body[field] = f.Value.(flag.Getter).Get()
		case texts[field] != nil:
			body[field] = *texts[field]
		}
	})
	if workID != "" {
		body["work_id"], given["work_id"] = workID, true
	}
	if op == "work-start" {
		resources := [][]string{}
		for _, p := range paths {
			resources = append(resources, []string{"path", p})
		}
		for _, e := range exacts {
			resources = append(resources, []string{"exact", e})
		}
		body["resources"] = resources
	}
	if op == "work-propose" {
		if clear == given["proposed_assignee"] {
			return usageError{"invalid_request", "give --proposed-assignee or --clear-assignee"}
		}
		if clear {
			body["proposed_assignee"], given["proposed_assignee"] = nil, true
		}
	}
	var missing []string
	for _, field := range core.WorkRequired[op] {
		if !given[field] {
			if field == "work_id" {
				missing = append(missing, "WORK_ID")
			} else {
				missing = append(missing, "--"+strings.ReplaceAll(field, "_", "-"))
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return usageError{"invalid_request", "missing required options: " + strings.Join(missing, ", ")}
	}
	family, id, found := strings.Cut(*as, ":")
	if !found || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling session as FAMILY:ID"}
	}
	body["caller"] = core.Key{Family: family, ID: id}
	if *consumer != "" {
		body["consumer"] = *consumer
	}
	secret, err := core.ReadSecret(*state)
	if err != nil {
		return errors.New("cannot read private daemon secret")
	}
	result, err := core.Call(ctx, *address, secret, "/v1/work/"+op, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(result))
	return err
}

func known(op string) bool {
	for _, candidate := range core.WorkOperations() {
		if candidate == op {
			return true
		}
	}
	return false
}
