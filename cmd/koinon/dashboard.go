package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// openBrowser runs the platform's opener; tests replace it so that no browser starts.
var openBrowser = func(ctx context.Context, argv []string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	return cmd.Run()
}

// dashboardCommand prints a one-time login link for the dashboard and opens it when a
// browser is available. The link is printed first, so a failure to open loses nothing.
func dashboardCommand(ctx context.Context, args []string, out io.Writer) error {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	noOpen := flags.Bool("no-open", false, "print the link without opening a browser")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command options; use koinon --help")
	}
	secret, err := core.ReadSecret(*state)
	if err != nil {
		return errors.New("cannot read private daemon secret")
	}
	data, err := core.Call(ctx, *address, secret, "/v1/dashboard/links", map[string]any{})
	if err != nil {
		return err
	}
	var reply struct {
		Path      string `json:"path"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &reply); err != nil || reply.Path == "" {
		return errors.New("invalid daemon response")
	}
	link := "http://" + *address + reply.Path
	if _, err := fmt.Fprintf(out, "%s\nThis link works once, within %d seconds.\n", link, reply.ExpiresIn); err != nil {
		return err
	}
	if *noOpen {
		return nil
	}
	opener, ok := platform.BrowserOpener(os.Getenv)
	if !ok {
		_, err := fmt.Fprintln(out, "No browser is available here; open the link in a browser on this computer.")
		return err
	}
	if err := openBrowser(ctx, append(opener, link)); err != nil {
		_, err := fmt.Fprintf(out, "The browser did not open (%v); open the link yourself.\n", err)
		return err
	}
	return nil
}
