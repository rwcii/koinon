package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// contextCommand reads and changes the context-cycle settings from the command line (#391):
// the family mode and threshold, a session's Enable and a session's own threshold, and it
// answers the native dialog that a session's managed cycle waits on. The daemon records each
// change in the audit log with the origin "cli".
func contextCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	operands := map[string]int{"show": 0, "set": 1, "enable": 1, "disable": 1, "threshold": 2, "answer": 2}
	const ops = "use koinon context show, set, enable, disable, threshold or answer"
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
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	var mode *string
	var threshold *int64
	var asJSON, yes *bool
	switch op {
	case "answer":
		yes = flags.Bool("yes", false, "answer without asking for confirmation")
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
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	if op == "answer" {
		return answerDialog(ctx, *address, secret, operandList[0], operandList[1], *yes, in, out)
	}
	var path string
	body := map[string]any{}
	switch op {
	case "show":
		path = "/v1/context/show"
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
	data, err := core.Call(ctx, *address, secret, path, body)
	if err != nil {
		return err
	}
	if op != "show" || *asJSON {
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	return printContext(out, data)
}

// answerDialog shows the native dialog that a session's managed cycle waits on and chooses
// one option, as "Choose N" on the Sessions page does. The answer carries the cycle, its
// revision and the digest of the dialog that was shown, so a dialog that changed meanwhile
// is refused.
func answerDialog(ctx context.Context, address, secret, peer, option string, yes bool, in io.Reader, out io.Writer) error {
	data, err := core.Call(ctx, address, secret, "/v1/context/show", map[string]any{})
	if err != nil {
		return err
	}
	var reply struct {
		Sessions []core.ContextSessionView `json:"sessions"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
	var found *core.ContextSessionView
	for i := range reply.Sessions {
		if reply.Sessions[i].Peer == peer {
			found = &reply.Sessions[i]
		}
	}
	if found == nil {
		return usageError{"peer_not_found", "no active session is named " + peer}
	}
	if found.Dialog == nil {
		return usageError{"no_dialog", peer + " has no managed cycle that waits on a native dialog"}
	}
	fmt.Fprintln(out, "Native dialog of "+peer+", cycle "+found.CycleID+":")
	for _, line := range found.Dialog.Lines {
		fmt.Fprintln(out, "  "+line)
	}
	if !slices.Contains(found.Dialog.Options, option) {
		return usageError{"invalid_request", "the dialog's options are " + strings.Join(found.Dialog.Options, ", ")}
	}
	if !yes {
		fmt.Fprintf(out, "Choose %s? Type y and press Enter: ", option)
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if strings.TrimSpace(answer) != "y" {
			_, err := fmt.Fprintln(out, "Nothing was chosen.")
			return err
		}
	}
	data, err = core.Call(ctx, address, secret, "/v1/context/answer", map[string]any{"cycle_id": found.CycleID,
		"revision": found.CycleRevision, "digest": found.Dialog.Digest, "option": option})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// printContext prints the show reply as two short tables.
func printContext(out io.Writer, data []byte) error {
	var reply struct {
		Families []core.ContextFamilyPolicy `json:"families"`
		Sessions []core.ContextSessionView  `json:"sessions"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
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
			state = "cycle " + s.CycleState + ": a native dialog waits; see koinon context answer " + s.Peer + " OPTION"
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
