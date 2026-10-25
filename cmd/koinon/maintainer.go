package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// maintainerCommand runs koinon maintainer secret set|generate|clear (PDR 009, Decision 1).
// A secret is never an argument: set reads it from the terminal with echo off, or from
// the first line of standard input.
func maintainerCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) < 2 || args[0] != "secret" || args[1] != "set" && args[1] != "generate" && args[1] != "clear" {
		return usageError{"invalid_request", "use koinon maintainer secret set, generate or clear"}
	}
	action := args[1]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("maintainer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command options; use koinon --help")
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	secret := ""
	if action == "set" {
		if secret, err = readNewSecret(in, os.Stderr); err != nil {
			return err
		}
		if !core.ValidMaintainerSecret(secret) {
			return usageError{"invalid_request", "a maintainer secret is valid UTF-8 with at least 12 characters and at most 1024 bytes"}
		}
	}
	data, err := core.Call(ctx, *address, bearer, "/v1/maintainer/secret", map[string]any{"action": action, "secret": secret})
	if err != nil {
		return err
	}
	var reply struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return errors.New("invalid daemon response")
	}
	switch action {
	case "generate":
		// Only the secret, so that a script can keep it; it is not shown again.
		_, err = fmt.Fprintln(out, reply.Secret)
	case "set":
		_, err = fmt.Fprintln(out, "The maintainer secret is set. Every dashboard login has ended.")
	default:
		_, err = fmt.Fprintln(out, "The maintainer secret is removed. Every dashboard login has ended; koinon dashboard gives a login link.")
	}
	return err
}

// readNewSecret reads a new secret: on a terminal twice with echo off, else the first line
// of in. The terminal gets its settings back after an error, a mismatch, SIGINT or SIGTERM.
func readNewSecret(in io.Reader, tell *os.File) (string, error) {
	file, ok := in.(*os.File)
	if !ok || !platform.IsTerminal(int(file.Fd())) || !platform.IsTerminal(int(tell.Fd())) {
		return core.ReadSecretLine(in)
	}
	restore, err := platform.NoEcho(int(file.Fd()))
	if err != nil {
		return "", err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	defer func() {
		signal.Stop(signals)
		close(done)
		restore()
	}()
	go func() {
		select {
		case s := <-signals:
			// Restore, then end with the signal's own status.
			restore()
			fmt.Fprintln(tell)
			signal.Reset(s)
			syscall.Kill(os.Getpid(), s.(syscall.Signal))
		case <-done:
		}
	}()
	var entries [2]string
	for i, prompt := range []string{"New maintainer secret: ", "Repeat it: "} {
		fmt.Fprint(tell, prompt)
		entries[i], err = core.ReadSecretLine(file)
		fmt.Fprintln(tell)
		if err != nil {
			return "", err
		}
	}
	if entries[0] != entries[1] {
		return "", usageError{"invalid_request", "the two entries differ; nothing was changed"}
	}
	return entries[0], nil
}
