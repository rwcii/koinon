package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// participantCommand lists the participants and chooses a participant's holder from the
// command line (#398), as the dashboard's Make holder does. The daemon records each choice in
// the audit log with the origin "cli".
func participantCommand(ctx context.Context, args []string, out io.Writer) error {
	operands := map[string]int{"list": 0, "holder": 2}
	const ops = "use koinon participant list or koinon participant holder ADDRESS PEER"
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
	flags := flag.NewFlagSet("participant "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	var ifRevision *int64
	if op == "holder" {
		ifRevision = flags.Int64("if-revision", 0, "the chosen session's revision; default its current one")
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
		return usageError{"invalid_request", "koinon participant " + op + " takes " + strconv.Itoa(want) + " operand(s); see koinon --help"}
	}
	if ifRevision != nil && *ifRevision < 0 {
		return usageError{"invalid_request", "--if-revision is a session revision"}
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	var data []byte
	if op == "list" {
		data, err = core.Call(ctx, *address, secret, "/v1/participants", nil)
	} else {
		data, err = core.Call(ctx, *address, secret, "/v1/participants/holder",
			map[string]any{"address": operandList[0], "peer": operandList[1], "if_revision": *ifRevision})
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
