package setup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/rwcii/koinon/internal/platform"
)

// Status-line states for an install that did not set the family up (#631; PDR 003, Decision
// 4). They describe automatic completion only: an explicit `koinon setup FAMILY` sets the
// status line up again also after a removal.
const (
	StatusLineCurrent  = "current"  // nothing to change
	StatusLineNeeded   = "needed"   // completion would set up or bring current Koinon's entry
	StatusLineDeclined = "declined" // the person removed it with --remove-status-line
	StatusLineChanged  = "changed"  // the entry is an edited Koinon wrapper
	StatusLineSkipped  = "skipped"  // the family's settings directory is absent
)

// statusLineFamilies are the families whose setup wraps a status line.
var statusLineFamilies = map[string]bool{"claude": true, "agy": true, "grok": true}

// statusSetter is one family's status line: its record and its set-up.
type statusSetter interface {
	load() (statusRecord, error)
	setUp() (StatusLineResult, error)
}

// familyStatusLine is the status line of o's family with its record in state, or false for a
// family without one. With dry, its set-up writes nothing.
func familyStatusLine(o Options, state string, dry bool) (statusSetter, bool) {
	switch o.Family {
	case "claude":
		dir := o.ClaudeConfig
		if dir == "" {
			dir = claudeConfigDir(os.Getenv)
		}
		return statusLine{binary: o.Binary, state: state, settings: filepath.Join(dir, "settings.json"), refresh: claudeRefresh, dry: dry}, true
	case "agy":
		l := agyStatusLine(o.Binary, agySettingsFile(o))
		l.state, l.dry = state, dry
		return l, true
	case "grok":
		g := newGrokStatusFile(o.Binary, state, grokConfigFile(o))
		g.dry = dry
		return g, true
	}
	return nil, false
}

// StatusLineState gives the state of o's status line for automatic completion: current,
// needed, declined, changed or skipped. It changes nothing, and it does not create the state
// directory. A family without a status line gives "".
func StatusLineState(o Options) (string, error) {
	s, ok := familyStatusLine(o, o.StateDir, true)
	if !ok {
		return "", nil
	}
	record, err := s.load()
	if err != nil {
		return "", err
	}
	if record.State == "declined" {
		return StatusLineDeclined, nil
	}
	result, err := s.setUp()
	if err != nil {
		return "", err
	}
	switch result.Outcome {
	case "set_up":
		return StatusLineNeeded, nil
	case "changed":
		return StatusLineChanged, nil
	case "skipped":
		return StatusLineSkipped, nil
	}
	return StatusLineCurrent, nil
}

// CompleteStatusLine sets up the status line of a family that an earlier setup configured,
// when its state is needed: a setup from before the family's status-line hook existed, or a
// Koinon wrapper that a later release writes differently (#631). It changes only the status
// line, and only for a family whose CLI is installed and whose koinon MCP server runs
// o.Binary. It reports whether it ran.
func CompleteStatusLine(ctx context.Context, o Options) (Report, bool, error) {
	report := Report{OK: true, Family: o.Family, Changed: []string{}, Unchanged: []string{}}
	if !statusLineFamilies[o.Family] || !filepath.IsAbs(o.Binary) || o.StateDir == "" {
		return report, false, nil
	}
	cli := o.CLI
	if cli == "" {
		cli, _ = exec.LookPath(o.Family)
	}
	if cli == "" || !filepath.IsAbs(cli) {
		return report, false, nil
	}
	if current, err := runCLI(ctx, cli, getCommand(o.Family)...); err != nil || !configured(o.Family, current, o.Binary) {
		return report, false, nil
	}
	state, err := StatusLineState(o)
	if err != nil || state != StatusLineNeeded {
		return report, false, err
	}
	root, err := platform.PrivateDir(o.StateDir)
	if err != nil {
		return report, false, err
	}
	s, _ := familyStatusLine(o, root, false)
	result, err := s.setUp()
	report.CLI, report.StatusLine = cli, &result
	if err != nil {
		return report, true, errors.Join(errors.New("status line not completed"), err)
	}
	report.Changed = append(report.Changed, o.Family+" status line in "+result.SettingsFile)
	report.Note = "An earlier setup configured this family without its current status line; only the status line was changed."
	return report, true, nil
}
