package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/install"
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
	return printJSON(out, map[string]string{"version": version, "commit": revision, "go": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH})
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
func lifecycleCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	prefix := flags.String("prefix", "", "Go installation prefix")
	state := flags.String("state-dir", "", "private Go state directory")
	var agents, repos repeated
	switch args[0] {
	case "install", "uninstall", "upgrade":
		flags.Var(&agents, "agent", "agent family to set up or remove; repeatable")
	}
	var noStart, fromPython, verify, status *bool
	var from, pythonPrefix, python *string
	switch args[0] {
	case "install":
		noStart = flags.Bool("no-start", false, "write the service without starting it")
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
		// The report shows what was done also when a later step failed.
		r, err := install.Install(ctx, install.Options{Prefix: *prefix, StateDir: *state, Agents: agents, NoStart: *noStart})
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
