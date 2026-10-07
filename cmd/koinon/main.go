// Command koinon provides the Go daemon core and its message commands. Other sprint
// commands are added only after their prerequisite chunks merge.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/launcher"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		_, err := fmt.Fprintln(out, usage)
		return err
	}
	if args[0] == "codex" || args[0] == "agy" || args[0] == "opencode" {
		o, err := launcher.Parse(args[0], args[1:])
		if err != nil {
			return err
		}
		return launcher.Run(ctx, o, out)
	}
	switch args[0] {
	case "setup", "guide", "hook":
		return agentCommand(ctx, args, in, out)
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	var listen, listen6, address, as *string
	var after, limit *int64
	positional := 0
	switch args[0] {
	case "mcp":
		// An agent starts this server; a launched agent passes the launcher's state root
		// and daemon address through its environment.
		if v := os.Getenv("KOINON_STATE_DIR"); v != "" {
			flags.Set("state-dir", v)
		}
		value := os.Getenv("KOINON_DAEMON_ADDRESS")
		if value == "" {
			value = "127.0.0.1:47671"
		}
		address = flags.String("address", value, "daemon loopback address")
	case "serve":
		listen = flags.String("listen", "127.0.0.1:47671", "IPv4 loopback listener")
		listen6 = flags.String("listen-v6", "[::1]:47671", "IPv6 loopback listener")
	case "status", "peers", "send", "inbox", "ack":
		address = flags.String("address", "127.0.0.1:47671", "daemon loopback address")
		if args[0] != "status" {
			as = flags.String("as", "", "calling session as FAMILY:ID")
		}
		positional = map[string]int{"send": 2, "ack": 1}[args[0]]
		if args[0] == "inbox" {
			after = flags.Int64("after", 0, "read messages after this sequence")
			limit = flags.Int64("limit", 50, "most messages to read")
		}
	default:
		return errors.New("unknown command; use koinon --help")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return errors.New("invalid command options; use koinon --help")
	}
	if flags.NArg() != positional {
		return errors.New("wrong number of command arguments; use koinon --help")
	}
	if args[0] == "mcp" {
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		return mcp.Serve(ctx, mcp.Config{StateDir: *state, Address: *address, Getenv: os.Getenv,
			ParentPID: os.Getppid(), Command: platform.ProcessCommand, Directory: directory, Now: time.Now}, in, out)
	}
	if args[0] != "serve" {
		return call(ctx, args[0], *state, *address, as, after, limit, flags.Args(), in, out)
	}
	d, err := core.Start(core.Config{StateDir: *state, Listen: []string{*listen, *listen6}})
	if err != nil {
		return err
	}
	defer d.Close()
	if err := json.NewEncoder(out).Encode(map[string]any{"ok": true, "daemon": "running", "listeners": d.Addresses()}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return d.Close()
	case err := <-d.Errors():
		return err
	}
}

const usage = `Usage: koinon serve [--state-dir DIR] [--listen 127.0.0.1:PORT] [--listen-v6 [::1]:PORT]
       koinon status [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon mcp [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon setup <claude|codex|agy|opencode|deepseek> [--cli ABS_PATH] [--binary ABS_PATH]
       koinon guide --agent <claude|codex|agy|opencode|deepseek>
       koinon hook agy-stop
       koinon <codex|agy|opencode> [--state-dir DIR] [--address HOST:PORT] [--cli ABS_PATH] [--directory DIR] [--tmux-session NAME] [--] [CLI arguments...]
       koinon peers --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon send --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT] NAME BODY
       koinon inbox --as FAMILY:ID [--after SEQ] [--limit N] [--state-dir DIR] [--address 127.0.0.1:PORT]
       koinon ack --as FAMILY:ID [--state-dir DIR] [--address 127.0.0.1:PORT] SEQ
A BODY of - reads the message body from standard input.`

// call runs one client command against the daemon's API and prints its JSON response.
func call(ctx context.Context, command, state, address string, as *string, after, limit *int64, args []string, in io.Reader, out io.Writer) error {
	var caller core.Key
	if as != nil {
		family, id, found := strings.Cut(*as, ":")
		if !found || family == "" || id == "" {
			return errors.New("--as must name the calling session as FAMILY:ID")
		}
		caller = core.Key{Family: family, ID: id}
	}
	var path string
	var body any
	switch command {
	case "status":
		path = "/v1/status"
	case "peers":
		path, body = "/v1/peers", map[string]any{"caller": caller}
	case "send":
		text := args[1]
		if text == "-" {
			data, err := io.ReadAll(io.LimitReader(in, 65537))
			if err != nil {
				return err
			}
			text = string(data)
		}
		path, body = "/v1/messages/send", map[string]any{"caller": caller, "to": args[0], "body": text}
	case "inbox":
		path, body = "/v1/inbox/read", map[string]any{"caller": caller, "after": *after, "limit": *limit}
	case "ack":
		through, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return errors.New("SEQ must be a sequence number")
		}
		path, body = "/v1/inbox/ack", map[string]any{"caller": caller, "through": through}
	}
	secret, err := core.ReadSecret(state)
	if err != nil {
		return errors.New("cannot read private daemon secret")
	}
	result, err := core.Call(ctx, address, secret, path, body)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(result))
	return err
}

// agentCommand runs the commands that configure and guide agents.
func agentCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	switch args[0] {
	case "hook":
		if len(args) != 2 || args[1] != "agy-stop" {
			return errors.New("unknown hook; use koinon hook agy-stop")
		}
		return setup.AgyStop(in, out)
	case "guide":
		agent := flags.String("agent", "", "agent family")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *agent == "" {
			return errors.New("use koinon guide --agent FAMILY")
		}
		return setup.Guide(*agent, out)
	}
	if len(args) < 2 {
		return errors.New("use koinon setup FAMILY")
	}
	o := setup.Options{Family: args[1]}
	flags.StringVar(&o.CLI, "cli", "", "absolute agent CLI path")
	flags.StringVar(&o.Binary, "binary", "", "absolute koinon binary path")
	flags.StringVar(&o.AgyRoot, "agy-config", "", "agy global customization root")
	flags.StringVar(&o.Opencode, "opencode-config", "", "OpenCode global configuration directory")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid setup options; use koinon --help")
	}
	report, err := setup.Run(ctx, o)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		// Startup errors must never include the authentication secret.
		code := "daemon_error"
		var refused core.RefusedError
		if errors.As(err, &refused) {
			code = refused.Code
		}
		json.NewEncoder(os.Stderr).Encode(map[string]any{"ok": false, "code": code, "detail": err.Error()})
		os.Exit(1)
	}
}
