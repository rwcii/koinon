package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// Plan returns the commands that this machine's installation still needs, in order, and
// changes nothing (#551). An empty plan means the installation is current. A check that
// cannot finish is an error, so a plan is never partial: a missing step is never reported
// as done.
func Plan(ctx context.Context, o Options) ([]string, error) {
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
	// An installation step comes alone: the install offers each agent family at a terminal,
	// and a daemon that is not current cannot answer for the secret. The recheck after it
	// finds the rest.
	if i, err := legacy.ReadInstall(o.PythonPrefix); err != nil {
		return nil, err
	} else if i != nil {
		return []string{b + " upgrade --from-python"}, nil
	}
	binary := Binary(o.Prefix)
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
	switch {
	case !agent && !system.Present:
		return []string{b + " install"}, nil
	case system.Present:
		if !sameContent(o.Source, binary) {
			return []string{b + " install --system"}, nil
		}
	case homebrewOpt(o.Source) != "":
		if target, err := os.Readlink(binary); err != nil || target != homebrewOpt(o.Source) {
			return []string{b + " install"}, nil
		}
	case !sameContent(o.Source, binary):
		return []string{b + " install"}, nil
	}
	var steps []string
	set, err := secretSet(ctx, o)
	if err != nil {
		return nil, err
	}
	if !set {
		steps = append(steps, b+" maintainer secret set")
	}
	for _, family := range setupFamilies {
		in := o.Inspect(ctx, setup.Options{Family: family, Binary: binary, StateDir: o.StateDir, ClaudeConfig: o.ClaudeConfig})
		if ctx.Err() != nil {
			return nil, fmt.Errorf("plan_timeout: the checks did not finish in %s", o.PlanWait)
		}
		switch {
		case in.Error != "":
			return nil, fmt.Errorf("agent_unreadable: %s", in.Error)
		case in.CLI != "" && !in.Configured:
			steps = append(steps, b+" setup "+family)
		}
	}
	return steps, nil
}

// planBinary is the command word for the binary that runs the plan: the formula's opt path
// when it runs from a Homebrew keg, so the command stays right after the next upgrade.
func planBinary(source string) string {
	if opt := homebrewOpt(source); opt != "" {
		source = opt
	}
	return setup.ShellWord(source)
}

func sameContent(a, b string) bool {
	x, err := digest(a)
	if err != nil {
		return false
	}
	y, err := digest(b)
	return err == nil && bytes.Equal(x, y)
}

// secretSet asks the daemon whether a maintainer secret is set. The daemon holds its store
// alone, so the plan does not read it. After brew upgrade the daemon exits and its manager
// starts the new binary, so the plan asks again until the wait ends.
func secretSet(ctx context.Context, o Options) (bool, error) {
	port, err := core.RecordedPort(o.StateDir)
	if err != nil {
		return false, err
	}
	address := o.Address
	if address == "" {
		address = core.Listeners(port)[0]
	}
	deadline := time.Now().Add(o.StatusWait)
	last := errors.New("no answer")
	for {
		secret, err := core.ReadSecret(o.StateDir)
		if err == nil {
			var data json.RawMessage
			if data, err = o.Status(ctx, address, secret); err == nil {
				var status struct {
					Set *bool `json:"maintainer_secret_set"`
				}
				if err = json.Unmarshal(data, &status); err == nil {
					if status.Set != nil {
						return *status.Set, nil
					}
					err = errors.New("daemon_outdated: the daemon does not report maintainer_secret_set")
				}
			}
		}
		last = err
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	if strings.HasPrefix(last.Error(), "daemon_outdated:") {
		return false, fmt.Errorf("%v at %s", last, address)
	}
	return false, fmt.Errorf("daemon_unavailable: the daemon did not answer at %s: %v", address, last)
}
