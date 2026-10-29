package main

import (
	"flag"
	"os"

	"github.com/rwcii/koinon/internal/core"
)

// addressFlag adds --address to a command's flags; resolveAddress gives it its value when
// the command line names none.
func addressFlag(flags *flag.FlagSet) *string {
	return flags.String("address", "", "daemon loopback address")
}

// resolveAddress runs after the parse, since the address depends on --state-dir (#405): an
// --address that was not given is KOINON_DAEMON_ADDRESS, or else 127.0.0.1 at the port that
// koinon install recorded in the state directory, 47671 by default.
func resolveAddress(flags *flag.FlagSet) error {
	if addressGiven(flags) {
		return nil
	}
	address, err := core.DaemonAddress(flags.Lookup("state-dir").Value.String(), os.Getenv)
	if err != nil {
		return err
	}
	return flags.Set("address", address)
}

// addressGiven reports whether the command line named --address.
func addressGiven(flags *flag.FlagSet) bool {
	given := false
	flags.Visit(func(f *flag.Flag) { given = given || f.Name == "address" })
	return given
}

// followAddress gives koinon mcp its daemon address for each call when given is false: the
// address that resolveAddress chose, read again each time, so a long-running server follows
// a daemon that restarts on another recorded port (#549). A given --address stays: nil.
func followAddress(state string, given bool) func() (string, error) {
	if given {
		return nil
	}
	return func() (string, error) { return core.DaemonAddress(state, os.Getenv) }
}
