package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/install"
	"github.com/rwcii/koinon/internal/library"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/upgrade"
)

// version and commit are set at build time by the release workflow:
// -ldflags "-X main.version=v1.2.3 -X main.commit=SHA".
var (
	version = "dev"
	commit  = ""
)

// lifecycleCode turns an error whose message starts with its fixed code ("capacity: …")
// into a refusal with that code.
var lifecycleCode = regexp.MustCompile(`^([a-z][a-z_]+): `)

func coded(err error) error {
	if err == nil {
		return nil
	}
	var refusal core.Refusal
	if errors.As(err, &refusal) {
		return usageError{refusal.Code, err.Error()}
	}
	if m := lifecycleCode.FindStringSubmatch(err.Error()); m != nil {
		return usageError{m[1], err.Error()}
	}
	return err
}

func printJSON(out io.Writer, v any) error {
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	return e.Encode(v)
}

func versionCommand(out io.Writer) error {
	revision := commit
	if revision == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					revision = s.Value
				}
			}
		}
	}
	catalog, err := library.Embedded(version)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	return printJSON(out, map[string]string{"catalog_digest": catalog.Digest, "version": version, "commit": revision, "go": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH, "mcp_resume": strconv.Itoa(mcp.ResumeVersion)})
}

// planCommand prints the plan's commands, one per line, or that the installation is current
// (#551). The whole plan is checked before anything is printed, so a failed check prints no
// step.
func planCommand(ctx context.Context, o install.Options, out io.Writer) error {
	steps, err := install.Plan(ctx, o)
	if err != nil {
		return coded(err)
	}
	if len(steps) == 0 {
		_, err = fmt.Fprintln(out, "The Koinon installation is current.")
		return err
	}
	_, err = fmt.Fprintln(out, strings.Join(steps, "\n"))
	return err
}

// repositories parses --repository KEY=PATH values.
func repositories(values []string) (map[string]string, error) {
	result := map[string]string{}
	for _, v := range values {
		key, path, ok := strings.Cut(v, "=")
		if !ok || key == "" || path == "" {
			return nil, usageError{"invalid_request", "--repository takes KEY=PATH"}
		}
		result[key] = path
	}
	return result, nil
}

// lifecycleCommand runs install, uninstall, import and upgrade (sprint chunk 11).
func lifecycleCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	prefix := flags.String("prefix", "", "Go installation prefix")
	state := flags.String("state-dir", "", "private Go state directory")
	var agents, repos repeated
	switch args[0] {
	case "install", "uninstall", "upgrade":
		flags.Var(&agents, "agent", "agent family to set up or remove; repeatable")
	}
	var noStart, system, plan, fromPython, verify, status *bool
	var from, pythonPrefix, python *string
	var port *int
	switch args[0] {
	case "install":
		port = flags.Int("port", 0, "loopback port of the daemon and dashboard; kept by later installs")
		noStart = flags.Bool("no-start", false, "write the service without starting it")
		system = flags.Bool("system", false, "macOS: run the daemon as a LaunchDaemon that survives logout; asks sudo")
		plan = flags.Bool("plan", false, "print the commands that this installation still needs; change nothing")
	case "import":
		from = flags.String("from", "", "Python-era state root")
		verify = flags.Bool("verify", false, "compare the sources with the Go state; change nothing")
	case "upgrade":
		fromPython = flags.Bool("from-python", false, "upgrade from the Python runtime")
		python = flags.String("python", "", "interpreter that runs the installed uninstall.py")
		status = flags.Bool("status", false, "print the upgrade journal; change nothing")
	}
	if args[0] == "import" || args[0] == "upgrade" {
		pythonPrefix = flags.String("python-prefix", "", "Python-era installation prefix")
		flags.Var(&repos, "repository", "KEY=PATH repository of a memory store; repeatable")
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return usageError{"invalid_request", "invalid " + args[0] + " options; use koinon --help"}
	}
	switch args[0] {
	case "install":
		given, other := false, false
		flags.Visit(func(f *flag.Flag) {
			given = given || f.Name == "port"
			other = other || (f.Name != "plan" && f.Name != "prefix" && f.Name != "state-dir")
		})
		if *plan {
			if other {
				return usageError{"invalid_request", "--plan takes only --prefix and --state-dir"}
			}
			return planCommand(ctx, install.Options{Prefix: *prefix, StateDir: *state}, out)
		}
		if given && !core.ValidPort(*port) {
			return usageError{"invalid_request", "--port takes a port from 1 to 65535"}
		}
		// The report shows what was done also when a later step failed.
		o := install.Options{Prefix: *prefix, StateDir: *state, Port: *port, Agents: agents, NoStart: *noStart, System: *system}
		// At a terminal, install reviews the families and asks what to set up (#449).
		if o.Ask = terminalAsk(in, os.Stderr); o.Ask != nil {
			o.Tell = os.Stderr
		}
		r, err := install.Install(ctx, o)
		printJSON(out, r)
		return coded(err)
	case "uninstall":
		r, err := install.Uninstall(ctx, install.Options{Prefix: *prefix, StateDir: *state, Agents: agents})
		printJSON(out, r)
		return coded(err)
	case "import":
		mapping, err := repositories(repos)
		if err != nil {
			return err
		}
		r, err := upgrade.Import(ctx, upgrade.ImportOptions{From: *from, PythonPrefix: *pythonPrefix, StateDir: *state,
			Repositories: mapping, Verify: *verify})
		if err == nil {
			err = printJSON(out, r)
		}
		return coded(err)
	}
	if *status {
		root := *state
		if root == "" {
			var err error
			if root, err = platform.DefaultStateDir(); err != nil {
				return err
			}
		}
		j, err := upgrade.Status(root)
		if err != nil {
			return coded(err)
		}
		return printJSON(out, map[string]any{"ok": true, "journal": j})
	}
	if !*fromPython {
		return usageError{"invalid_request", "use koinon upgrade --from-python"}
	}
	mapping, err := repositories(repos)
	if err != nil {
		return err
	}
	r, err := upgrade.Run(ctx, upgrade.Options{PythonPrefix: *pythonPrefix, GoPrefix: *prefix, StateDir: *state,
		Agents: agents, Repositories: mapping, Python: *python})
	printJSON(out, r)
	return coded(err)
}
