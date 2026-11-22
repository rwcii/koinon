package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// Current is what `koinon install --plan` prints for an installation that needs nothing. The
// Homebrew caveats show it with "No action is required.", without the action line and the
// recheck (#561, #618).
const Current = "The Koinon installation is current."

// Plan returns the commands that this machine's installation still needs, in order, and
// changes nothing (#551). An empty plan means the installation is current. A check that
// cannot finish is an error, so a plan is never partial: a missing step is never reported
// as done.
func Plan(ctx context.Context, o Options) ([]string, error) {
	result, err := PlanSteps(ctx, o)
	if err != nil {
		return nil, err
	}
	commands := []string{}
	for _, step := range result.Steps {
		commands = append(commands, step.Command)
	}
	return commands, nil
}

// PlanResult is the plan with the reason for each step, the form of
// `koinon install --plan --json` (#745). Current is true exactly when Steps is empty.
type PlanResult struct {
	Current bool       `json:"current"`
	Steps   []PlanStep `json:"steps"`
}

// PlanStep is one command of the plan and why it is needed.
type PlanStep struct {
	Command string       `json:"command"`
	Reasons []PlanReason `json:"reasons"`
}

// PlanReason is one cause of a step. Code is python_install, not_installed, binary_differs,
// homebrew_link_differs, no_current_daemon, entry_needs_install (with Kind pickup_skill,
// skill or opencode_command, Family, Path and Result created or replaced),
// maintainer_secret_unset, agent_not_set_up or status_line_needed (with Family).
type PlanReason struct {
	Code   string `json:"code"`
	Family string `json:"family,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Path   string `json:"path,omitempty"`
	Result string `json:"result,omitempty"`
}

// PlanSteps is Plan with the reason for each step.
func PlanSteps(ctx context.Context, o Options) (PlanResult, error) {
	steps, err := planSteps(ctx, o)
	if err != nil {
		return PlanResult{}, err
	}
	return PlanResult{Current: len(steps) == 0, Steps: steps}, nil
}

func planSteps(ctx context.Context, o Options) ([]PlanStep, error) {
	if err := o.defaults(); err != nil {
		return nil, err
	}
	if o.PlanWait == 0 {
		o.PlanWait = 30 * time.Second
	}
	if o.StatusWait == 0 {
		o.StatusWait = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.PlanWait)
	defer cancel()
	b := planBinary(o.Source)
	binary := Binary(o.Prefix)
	// Each command acts on the installation that the plan read.
	var prefix, state string
	if o.PlanPrefix {
		prefix = " --prefix " + setup.ShellWord(o.Prefix)
	}
	if o.PlanState {
		state = " --state-dir " + setup.ShellWord(o.StateDir)
	}
	// An installation step comes alone: the install offers each agent family at a terminal,
	// and a daemon that is not current cannot answer for the secret. The recheck after it
	// finds the rest.
	if i, err := legacy.ReadInstall(o.PythonPrefix); err != nil {
		return nil, err
	} else if i != nil {
		return one(b+" upgrade --from-python"+prefix+state, PlanReason{Code: "python_install"}), nil
	}
	var system platform.SystemPlist
	if o.Services.Backend == "launchd" {
		var err error
		if system, err = o.Services.ReadSystem(ctx); err != nil {
			return nil, err
		}
	}
	agent, _, err := platform.Marked(o.Services.DaemonArtifact())
	if err != nil {
		return nil, err
	}
	install := b + " install" + prefix + state
	switch {
	case !agent && !system.Present:
		return one(install, PlanReason{Code: "not_installed"}), nil
	case system.Present:
		if differ, err := differs(o.Source, binary); err != nil {
			return nil, err
		} else if differ {
			return one(b+" install --system"+prefix+state, PlanReason{Code: "binary_differs"}), nil
		}
	case platform.HomebrewOpt(o.Source) != "":
		if differ, err := notLinked(binary, platform.HomebrewOpt(o.Source)); err != nil {
			return nil, err
		} else if differ {
			return one(install, PlanReason{Code: "homebrew_link_differs"}), nil
		}
	default:
		if differ, err := differs(o.Source, binary); err != nil {
			return nil, err
		} else if differ {
			return one(install, PlanReason{Code: "binary_differs"}), nil
		}
	}
	// Without a service manager nothing starts the daemon after a restart or after brew
	// upgrade; the install starts it again (#619).
	if !system.Present && !o.Services.Available(ctx) && !answersNow(ctx, o) {
		return one(install, PlanReason{Code: "no_current_daemon"}), nil
	}
	// A missing or earlier skill entry or OpenCode command file is the install's to write
	// (#741); the daemon also writes them at each start, so this finds a write that failed.
	if entries, err := entriesNeedInstall(ctx, o); err != nil {
		return nil, err
	} else if len(entries) > 0 {
		return []PlanStep{{Command: install, Reasons: entries}}, nil
	}
	steps := []PlanStep{}
	set, err := secretSet(ctx, o)
	if err != nil {
		return nil, err
	}
	if !set {
		steps = append(steps, PlanStep{b + " maintainer secret set" + state, []PlanReason{{Code: "maintainer_secret_unset"}}})
	}
	for _, family := range setupFamilies {
		in := o.Inspect(ctx, setup.Options{Family: family, Binary: binary, StateDir: o.StateDir, ClaudeConfig: o.ClaudeConfig,
			AgyRoot: o.AgyRoot, GrokHome: o.GrokHome})
		if ctx.Err() != nil {
			return nil, fmt.Errorf("plan_timeout: the checks did not finish in %s", o.PlanWait)
		}
		switch {
		case in.Error != "":
			return nil, fmt.Errorf("agent_unreadable: %s", in.Error)
		case in.StatusLineError != "":
			// A status line whose state is not known is an unfinished check, never current (#631).
			return nil, fmt.Errorf("agent_unreadable: %s status line: %s", family, in.StatusLineError)
		case in.CLI != "" && !in.Configured, in.Configured && in.StatusLine == setup.StatusLineNeeded:
			// Setup configures the running binary unless --binary names the installed one. A
			// configured family whose status line is missing or out of date gets it there (#631).
			code := "agent_not_set_up"
			if in.Configured {
				code = "status_line_needed"
			}
			steps = append(steps, PlanStep{b + " setup " + family + " --binary " + setup.ShellWord(binary) + state,
				[]PlanReason{{Code: code, Family: family}}})
		}
	}
	return steps, nil
}

// one is a plan of one step with one reason.
func one(command string, reason PlanReason) []PlanStep {
	return []PlanStep{{Command: command, Reasons: []PlanReason{reason}}}
}

// planBinary is the command word for the binary that runs the plan: the formula's opt path
// when it runs from a Homebrew keg, so the command stays right after the next upgrade.
func planBinary(source string) string {
	if opt := platform.HomebrewOpt(source); opt != "" {
		source = opt
	}
	return setup.ShellWord(source)
}

// differs reports whether binary is missing or has other content than source. A file that
// cannot be read is an error, not a difference.
func differs(source, binary string) (bool, error) {
	want, err := digest(source)
	if err != nil {
		return false, fmt.Errorf("binary_unreadable: %v", err)
	}
	have, err := digest(binary)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("binary_unreadable: %v", err)
	}
	return !bytes.Equal(want, have), nil
}

// notLinked reports whether binary is missing or is not the link to opt.
func notLinked(binary, opt string) (bool, error) {
	info, err := os.Lstat(binary)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("binary_unreadable: %v", err)
	case info.Mode()&fs.ModeSymlink == 0:
		return true, nil
	}
	target, err := os.Readlink(binary)
	if err != nil {
		return false, fmt.Errorf("binary_unreadable: %v", err)
	}
	return target != opt, nil
}

// answersNow reports whether a daemon answers on the state directory, with one try, and runs
// o.Version when it is given.
func answersNow(ctx context.Context, o Options) bool {
	port, err := core.RecordedPort(o.StateDir)
	if err != nil {
		return false
	}
	address := o.Address
	if address == "" {
		address = core.Listeners(port)[0]
	}
	secret, err := core.ReadSecret(o.StateDir)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := o.Status(ctx, address, secret)
	if err != nil {
		return false
	}
	var status struct {
		Version string `json:"version"`
	}
	return o.Version == "" || json.Unmarshal(data, &status) == nil && status.Version == o.Version
}

// secretSet asks the daemon whether a maintainer secret is set. The daemon holds its store
// alone, so the plan does not read it. After brew upgrade the daemon exits and its manager
// starts the new binary, so the plan asks again until the wait ends; an answer after the
// wait does not count.
func secretSet(ctx context.Context, o Options) (bool, error) {
	port, err := core.RecordedPort(o.StateDir)
	if err != nil {
		return false, err
	}
	address := o.Address
	if address == "" {
		address = core.Listeners(port)[0]
	}
	ctx, cancel := context.WithTimeout(ctx, o.StatusWait)
	defer cancel()
	last := errors.New("no answer")
	for {
		secret, err := core.ReadSecret(o.StateDir)
		if err == nil {
			var data json.RawMessage
			if data, err = o.Status(ctx, address, secret); err == nil && ctx.Err() == nil {
				var status struct {
					Set *bool `json:"maintainer_secret_set"`
				}
				if err = json.Unmarshal(data, &status); err == nil {
					if status.Set != nil {
						return *status.Set, nil
					}
					err = errors.New("daemon_outdated: the daemon does not report maintainer_secret_set")
				}
			} else if err == nil {
				err = ctx.Err()
			}
		}
		last = err
		select {
		case <-ctx.Done():
			if strings.HasPrefix(last.Error(), "daemon_outdated:") {
				return false, fmt.Errorf("%v at %s", last, address)
			}
			return false, fmt.Errorf("daemon_unavailable: the daemon did not answer at %s: %v", address, last)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// entriesNeedInstall returns each koinon-pickup entry, skill entry and OpenCode command file
// that koinon install would create or replace, as entry_needs_install reasons, reading the
// same files that the install writes (pickupSkillPaths, skillRoots, OpenCode's commands when
// its directory exists) and writing none (#741, #745). A kept file (a link, another skill at
// a shared name, an edited command) needs nothing; a file that cannot be read is an
// unfinished check.
func entriesNeedInstall(ctx context.Context, o Options) ([]PlanReason, error) {
	// Every file is read before the answer, so a missing entry never hides an unreadable one,
	// and an inspection that passes the plan's time is unfinished, even after a repair.
	var entries []PlanReason
	check := func(kind, family, path string, result string, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return fmt.Errorf("plan_timeout: the checks did not finish in %s", o.PlanWait)
		}
		if result == "created" || result == "replaced" {
			entries = append(entries, PlanReason{Code: "entry_needs_install", Kind: kind, Family: family, Path: path, Result: result})
		}
		return nil
	}
	for _, skill := range pickupSkillPaths(o) {
		result, err := core.SkillFileState(skill.Path, pickupContent(skill.Family))
		if err := check("pickup_skill", skill.Family, skill.Path, result, err); err != nil {
			return nil, err
		}
	}
	skills, err := core.TeamSkills()
	if err != nil {
		return nil, err
	}
	for _, root := range skillRoots(o) {
		for _, skill := range skills {
			path := root.Path(skill.Name)
			result, err := core.TeamSkillState(path, skill)
			if err := check("skill", root.Family, path, result, err); err != nil {
				return nil, err
			}
		}
	}
	if info, err := os.Stat(o.Opencode); err != nil || !info.IsDir() {
		return entries, check("", "", "", "", nil)
	}
	commands, err := core.OpenCodeCommands()
	if err != nil {
		return nil, err
	}
	for _, command := range commands {
		path := core.OpenCodeCommandPath(o.Opencode, command.Name)
		result, err := core.OpenCodeCommandState(path, command)
		if err := check("opencode_command", "opencode", path, result, err); err != nil {
			return nil, err
		}
	}
	return entries, check("", "", "", "", nil)
}
