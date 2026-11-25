package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/rwcii/koinon/internal/library"
)

// preflightCommand prints a standard lens brief from the preflight skill in this binary, for
// a preflight runner whose lens policy turns the lens on with a section that has no text
// (WDR 004, Decision 8; #814). It reads nothing else and needs no daemon.
func preflightCommand(args []string, out io.Writer) error {
	if len(args) != 2 || args[0] != "brief" {
		return usageError{"invalid_request", "use koinon preflight brief NAME"}
	}
	brief, err := library.EmbeddedStandardLens(args[1])
	if errors.Is(err, library.ErrNoStandardLens) {
		return usageError{"not_found", "the preflight skill has no standard lens " + args[1]}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(out, brief)
	return err
}
