package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// contextCommand reads and changes the context-cycle settings from the command line (#391):
// the family mode and threshold, a session's Enable and a session's own threshold. The
// daemon records each change in the audit log with the origin "cli".
func contextCommand(ctx context.Context, args []string, out io.Writer) error {
	operands := map[string]int{"show": 0, "set": 1, "enable": 1, "disable": 1, "threshold": 2}
	const ops = "use koinon context show, set, enable, disable or threshold"
	if len(args) == 0 {
		return usageError{"invalid_request", ops}
	}
	op, rest := args[0], args[1:]
	want, known := operands[op]
	if !known {
		return usageError{"invalid_request", ops}
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("context "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	var mode *string
	var threshold *int64
	var asJSON *bool
	switch op {
	case "show":
		asJSON = flags.Bool("json", false, "print the daemon's JSON reply")
	case "set":
		mode = flags.String("mode", "", "observe, native_compaction (codex only) or handoff_cycle")
		threshold = flags.Int64("threshold", 0, "percent of the context limit, 1 to 100")
	}
	// Flags may come before or after the operands.
	var operandList []string
	for {
		if err := flags.Parse(rest); err != nil {
			return usageError{"invalid_request", err.Error()}
		}
		if flags.NArg() == 0 {
			break
		}
		operandList, rest = append(operandList, flags.Arg(0)), flags.Args()[1:]
	}
	if len(operandList) != want {
		return usageError{"invalid_request", "koinon context " + op + " takes " + strconv.Itoa(want) + " operand(s); see koinon --help"}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	var path string
	body := map[string]any{}
	switch op {
	case "show":
	case "set":
		set := map[string]bool{}
		flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if !set["mode"] || !set["threshold"] {
			return usageError{"invalid_request", "koinon context set needs --mode and --threshold"}
		}
		path, body["family"], body["mode"], body["threshold"] = "/v1/context/set", operandList[0], *mode, *threshold
	case "enable", "disable":
		path, body["peer"], body["enabled"] = "/v1/context/enable", operandList[0], op == "enable"
	case "threshold":
		value := int64(0)
		if operandList[1] != "family" {
			n, err := strconv.ParseInt(operandList[1], 10, 64)
			if err != nil || n < 1 || n > 100 {
				return usageError{"invalid_request", "the threshold is a percent from 1 to 100, or family"}
			}
			value = n
		}
		path, body["peer"], body["threshold"] = "/v1/context/threshold", operandList[0], value
	}
	if op == "show" {
		return showContext(ctx, *address, secret, *asJSON, out)
	}
	data, err := core.Call(ctx, *address, secret, path, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// contextShow is the reply of /v1/context/show.
type contextShow struct {
	Families  []core.ContextFamilyPolicy `json:"families"`
	Sessions  []core.ContextSessionView  `json:"sessions"`
	NextAfter string                     `json:"next_after,omitempty"`
}

// showContext reads every page of the active sessions and prints them together.
func showContext(ctx context.Context, address, secret string, asJSON bool, out io.Writer) error {
	var all contextShow
	after := ""
	for {
		data, err := core.Call(ctx, address, secret, "/v1/context/show", map[string]any{"after": after})
		if err != nil {
			return err
		}
		var page contextShow
		if err := json.Unmarshal(data, &page); err != nil {
			return err
		}
		all.Families = page.Families
		all.Sessions = append(all.Sessions, page.Sessions...)
		if page.NextAfter == "" {
			break
		}
		after = page.NextAfter
	}
	if asJSON {
		data, err := json.Marshal(all)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	return printContext(out, all)
}

// printContext prints the show reply as two short tables.
func printContext(out io.Writer, reply contextShow) error {
	fmt.Fprintln(out, "FAMILY      MODE               THRESHOLD")
	for _, f := range reply.Families {
		line := fmt.Sprintf("%-11s %-18s %d%%", f.Family, f.Mode, f.Threshold)
		if f.Error != "" {
			line += " (stored value not applied: " + f.Error + ")"
		}
		fmt.Fprintln(out, line)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "SESSION                          FAMILY      HANDOFF  THRESHOLD       STATE")
	for _, s := range reply.Sessions {
		enabled := "off"
		if s.Enabled {
			enabled = "on"
		}
		state := s.Reason
		switch {
		case s.Dialog != nil:
			state = "cycle " + s.CycleState + ": a native dialog waits (Sessions page or the session's pane)"
		case s.CycleState != "":
			state = "cycle " + s.CycleState
		case !s.Available:
			state = "unavailable: " + s.Unavailable
		case s.Eligible:
			state = "threshold reached"
		}
		_, err := fmt.Fprintf(out, "%-32s %-11s %-8s %-15s %s\n", s.Peer, s.Family, enabled,
			fmt.Sprintf("%d%% (%s)", s.Threshold, s.ThresholdSource), state)
		if err != nil {
			return err
		}
	}
	return nil
}
