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
	given := false
	flags.Visit(func(f *flag.Flag) { given = given || f.Name == "address" })
	if given {
		return nil
	}
	address, err := core.DaemonAddress(flags.Lookup("state-dir").Value.String(), os.Getenv)
	if err != nil {
		return err
	}
	return flags.Set("address", address)
}
