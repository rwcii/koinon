// Package install places the koinon binary, writes and starts the daemon's user service,
// and configures the agents the user names; it also removes them again, keeping the
// state (sprint chunk 11, docs/INSTALL.md).
package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/setup"
)

// Families are the agent families setup and removal know.
var Families = []string{"claude", "codex", "agy", "opencode", "grok", "deepseek"}

// Options select the installation. Zero values take the documented defaults.
type Options struct {
	Prefix       string   // default $XDG_DATA_HOME/koinon/go
	StateDir     string   // the Go state root; default platform.DefaultStateDir
	Port         int      // the daemon's loopback port to record (#405); zero keeps the recorded one
	PythonPrefix string   // the Python-era prefix that refuses a plain install
	Agents       []string // families to set up, or to remove (default: all for removal)
	NoStart      bool
	// System installs the daemon as a macOS LaunchDaemon (#283); a later install or
	// uninstall finds system mode from its plist.
	System bool
	// Upgrade is set by `koinon upgrade`, which removed the Python installation itself.
	Upgrade bool
	Source  string // the binary to install; default this executable
	LinkDir string // the directory of the koinon link; default ~/.local/bin
	// ClaudeConfig is the Claude Code configuration root that receives the koinon-pickup
	// skill; default $CLAUDE_CONFIG_DIR, else ~/.claude.
	ClaudeConfig string
	// Opencode and AgyRoot are OpenCode's and agy's configuration directories, which receive
	// the skill entries when they exist (#461); default $XDG_CONFIG_HOME/opencode, else
	// ~/.config/opencode, and ~/.gemini/config.
	Opencode string
	AgyRoot  string
	Path     string // the search path that the report resolves koinon on; default $PATH
	Address  string // the daemon address to wait for; default 127.0.0.1 at the port
	Services platform.Services
	Setup    func(context.Context, setup.Options) (setup.Report, error)
	// Adopt records the launcher CLI of a family that an earlier setup configured.
	Adopt  func(context.Context, setup.Options) (setup.Report, bool, error)
	Remove func(context.Context, setup.Options) (setup.Report, error)
	// Ask puts a yes-or-no question to the person at the terminal; nil means no terminal,
	// and install asks nothing (#449). Tell receives the review that the questions follow.
	Ask         func(question string) (bool, error)
	Tell        io.Writer
	Inspect     func(context.Context, setup.Options) setup.Inspection
	Permissions func(setup.Options, bool) (setup.PermissionsResult, error)
	Status      func(ctx context.Context, address, secret string) (json.RawMessage, error)
	Wait        time.Duration
	// SystemWait bounds the wait for the system daemon; zero is 90 seconds.
	SystemWait time.Duration
}

// Report is the JSON result of an install or an uninstall.
type Report struct {
	OK           bool           `json:"ok"`
	Binary       string         `json:"binary"`
	BinaryResult string         `json:"binary_result"`
	Backend      string         `json:"backend"`
	Artifact     string         `json:"artifact"`
	Service      string         `json:"service"`
	StartCommand string         `json:"start_command,omitempty"`
	StateDir     string         `json:"state_dir"`
	Port         int            `json:"port,omitempty"`
	Link         string         `json:"link"`
	LinkResult   string         `json:"link_result"`
	OnPath       string         `json:"on_path,omitempty"`
	PathStep     string         `json:"path_step,omitempty"`
	OtherBinary  string         `json:"other_binary,omitempty"`
	SourceStep   string         `json:"source_step,omitempty"`
	Agents       []setup.Report `json:"agents"`
	// Review is each family's state before an install at a terminal, with the permission rules
	// after the question for a family that got it (#462); Permissions reports the permission
	// rules of each set-up family after its question (#449).
	Review      []setup.Inspection        `json:"review,omitempty"`
	Permissions []setup.PermissionsResult `json:"permissions,omitempty"`
	// PickupSkills reports the managed cycle's koinon-pickup skill for each family (#406).
	PickupSkills []PickupSkill `json:"pickup_skills"`
	// TeamSkills reports the koinon-NAME team skill entries for each family (#453).
	TeamSkills []PickupSkill `json:"team_skills"`
}

// PickupSkill is one family's koinon-pickup skill file and what the install or uninstall did
// with it: created, replaced or unchanged; removed, kept (other content) or absent.
type PickupSkill struct {
	Family string `json:"family"`
	Name   string `json:"name,omitempty"`
	Path   string `json:"path"`
	Result string `json:"result"`
}

// DefaultPrefix is the Go installation prefix.
func DefaultPrefix() (string, error) {
	python, err := legacy.DefaultPrefix()
	if err != nil {
		return "", err
	}
	return filepath.Join(python, "go"), nil
}

func (o *Options) defaults() error {
	var err error
	if o.Prefix == "" {
		if o.Prefix, err = DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.StateDir == "" {
		if o.StateDir, err = platform.DefaultStateDir(); err != nil {
			return err
		}
	}
	if o.PythonPrefix == "" {
		if o.PythonPrefix, err = legacy.DefaultPrefix(); err != nil {
			return err
		}
	}
	if o.Source == "" {
		if o.Source, err = os.Executable(); err != nil {
			return err
		}
	}
	if o.Port != 0 && !core.ValidPort(o.Port) {
		return errors.New("invalid_request: the port must be from 1 to 65535")
	}
	if o.Services.Run == nil {
		if o.Services, err = platform.NewServices(); err != nil {
			return err
		}
	}
	if o.LinkDir == "" {
		o.LinkDir = filepath.Join(o.Services.Home, ".local", "bin")
	}
	if o.ClaudeConfig == "" {
		o.ClaudeConfig = core.ClaudeConfigRoot(os.Getenv("CLAUDE_CONFIG_DIR"), o.Services.Home)
	}
	if o.Opencode == "" {
		o.Opencode = core.OpenCodeConfigRoot(os.Getenv("XDG_CONFIG_HOME"), o.Services.Home)
	}
	if o.AgyRoot == "" {
		o.AgyRoot = core.AgyConfigRoot(o.Services.Home)
	}
	if o.Path == "" {
		o.Path = os.Getenv("PATH")
	}
	if o.Setup == nil {
		o.Setup = setup.Run
	}
	if o.Adopt == nil {
		o.Adopt = setup.Adopt
	}
	if o.Remove == nil {
		o.Remove = setup.Remove
	}
	if o.Inspect == nil {
		o.Inspect = setup.Inspect
	}
	if o.Permissions == nil {
		o.Permissions = setup.Permissions
	}
	if o.Tell == nil {
		o.Tell = io.Discard
	}
	if o.Status == nil {
		o.Status = core.GetStatus
	}
	if o.Wait == 0 {
		o.Wait = 20 * time.Second
	}
	for _, path := range []string{o.Prefix, o.StateDir, o.PythonPrefix, o.Source, o.LinkDir, o.ClaudeConfig} {
		if !filepath.IsAbs(path) {
			return errors.New("installation paths must be absolute")
		}
	}
	for _, agent := range o.Agents {
		known := false
		for _, f := range Families {
			known = known || f == agent
		}
		if !known {
			return fmt.Errorf("unknown agent family %q", agent)
		}
	}
	return nil
}

// Binary is the installed binary's path under prefix.
func Binary(prefix string) string { return filepath.Join(prefix, "bin", "koinon") }

// linkMarker records, in the prefix, the link that the install created.
func linkMarker(prefix string) string { return filepath.Join(prefix, "link") }

// link puts a koinon link to binary in dir, so that koinon on PATH runs the installed
// copy. Another file or link there is never replaced: the result is then "occupied".
func link(binary, dir, marker string) (string, string, error) {
	path := filepath.Join(dir, "koinon")
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(dir, 0755); err != nil {
			return path, "", err
		}
		// The marker comes first, so a link is never left unrecorded.
		if err := writeMarker(marker, path); err != nil {
			return path, "", err
		}
		if err := os.Symlink(binary, path); err != nil {
			os.Remove(marker)
			return path, "", err
		}
		return path, "created", nil
	case err != nil:
		return path, "", err
	case info.Mode()&fs.ModeSymlink != 0:
		if target, err := os.Readlink(path); err == nil && target == binary {
			return path, "unchanged", nil
		}
	}
	return path, "occupied", nil
}

// readMarker returns the link that the marker records, or "" without a marker. A marker
// that is not a regular file of this user (a symlink included), or that names no koinon
// link, is refused, so an unrelated file there is never read as one or overwritten.
func readMarker(marker string) (string, error) {
	invalid := errors.New("link_marker_invalid: " + marker + " is not a koinon link marker")
	f, err := platform.OpenOwned(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", invalid
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	recorded := strings.TrimSpace(string(data))
	if err != nil || len(data) > 4096 || !filepath.IsAbs(recorded) || filepath.Base(recorded) != "koinon" {
		return "", invalid
	}
	return recorded, nil
}

// writeMarker records path in the marker. It replaces only a valid marker, by renaming a
// new file over it, which never follows a symlink.
func writeMarker(marker, path string) error {
	if _, err := readMarker(marker); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(marker), ".link-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(path + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), marker)
}

// searchDirs resolves the search path's directories as a shell in the current directory
// would: an empty entry is the current directory, and a relative entry is under it.
func searchDirs(path string) []string {
	wd, _ := os.Getwd()
	var dirs []string
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			if wd == "" {
				continue
			}
			dir = filepath.Join(wd, dir)
		}
		dirs = append(dirs, filepath.Clean(dir))
	}
	return dirs
}

// lookPath returns the koinon that the search path runs, or "".
func lookPath(path string) string {
	if all := lookPathAll(path); len(all) > 0 {
		return all[0]
	}
	return ""
}

// lookPathAll returns every koinon executable on the search path, in its order.
func lookPathAll(path string) []string {
	var found []string
	for _, dir := range searchDirs(path) {
		candidate := filepath.Join(dir, "koinon")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			found = append(found, candidate)
		}
	}
	return found
}

// shellQuote quotes a word for a POSIX shell: in single quotes, where each embedded single
// quote closes the quoting, is escaped with a backslash and reopens it.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// otherBinary returns the first koinon on the search path with other content than the
// installed binary, when the install ran the installed binary itself; otherwise "".
// That install copied nothing, and the other one, such as a Homebrew copy, may be the
// binary that was meant.
func otherBinary(source, binary, path string) string {
	if !sameFile(source, binary) {
		return ""
	}
	installed, err := digest(binary)
	if err != nil {
		return ""
	}
	for _, candidate := range lookPathAll(path) {
		if have, err := digest(candidate); err == nil && !bytes.Equal(have, installed) {
			return candidate
		}
	}
	return ""
}

func sameFile(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// pathStep names the step that makes koinon on PATH run the installed binary, or "" when
// it already does. The install never edits shell startup files.
func pathStep(r Report, dir, path string) string {
	if r.OnPath != "" && sameFile(r.OnPath, r.Binary) {
		return ""
	}
	listed := false
	for _, entry := range searchDirs(path) {
		listed = listed || entry == filepath.Clean(dir)
	}
	switch {
	case r.LinkResult == "occupied":
		return fmt.Sprintf("%s is not a link to the installed binary; replace it with ln -sf %s %s, or run %s", r.Link, r.Binary, r.Link, r.Binary)
	case !listed:
		return fmt.Sprintf("add %s to PATH in your shell's startup file: export PATH=\"%s:$PATH\"", dir, dir)
	case r.OnPath != "":
		return fmt.Sprintf("PATH runs %s first; put %s before its directory in PATH, or run %s", r.OnPath, dir, r.Binary)
	}
	return "run " + r.Binary
}

func digest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// homebrewOpt returns the formula's stable opt path when source is a koinon binary in a
// Homebrew keg, <P>/Cellar/koinon/<version>/bin/koinon with the keg's INSTALL_RECEIPT.json,
// and <P>/opt/koinon resolves into <P>/Cellar/koinon; otherwise "". Each brew upgrade moves
// the opt link to the new keg (#397).
func homebrewOpt(source string) string {
	file, err := filepath.EvalSymlinks(source)
	if err != nil {
		return ""
	}
	bin := filepath.Dir(file)
	keg := filepath.Dir(bin)
	kegs := filepath.Dir(keg)
	cellar := filepath.Dir(kegs)
	if filepath.Base(file) != "koinon" || filepath.Base(bin) != "bin" || filepath.Base(kegs) != "koinon" || filepath.Base(cellar) != "Cellar" {
		return ""
	}
	if info, err := os.Stat(filepath.Join(keg, "INSTALL_RECEIPT.json")); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	opt := filepath.Join(filepath.Dir(cellar), "opt", "koinon", "bin", "koinon")
	resolved, err := filepath.EvalSymlinks(opt)
	if err != nil || !strings.HasPrefix(resolved, kegs+string(filepath.Separator)) {
		return ""
	}
	if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return opt
}

// placeLink makes target a symbolic link to opt, atomically: a new link is renamed over
// whatever is there.
func placeLink(opt, target string) (string, error) {
	if info, err := os.Lstat(target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		if have, err := os.Readlink(target); err == nil && have == opt {
			return "unchanged", nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".koinon-link-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	link := filepath.Join(tmp, "koinon")
	if err := os.Symlink(opt, link); err != nil {
		return "", err
	}
	if err := os.Rename(link, target); err != nil {
		return "", err
	}
	return "linked", platform.SyncDir(filepath.Dir(target))
}

// place copies source to target atomically with mode 0755 and checks the copy's digest. A
// link at target, such as a Homebrew link, is replaced by the copy.
func place(source, target string) (string, error) {
	want, err := digest(source)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
		if have, err := digest(target); err == nil && bytes.Equal(have, want) {
			return "unchanged", nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".koinon-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if have, err := digest(tmp.Name()); err != nil || !bytes.Equal(have, want) {
		return "", errors.New("binary_copy_mismatch: the copied binary differs from its source")
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	return "replaced", platform.SyncDir(filepath.Dir(target))
}

// skillRoots are the families' user skill roots that get the entries: under the Claude Code
// configuration root, in the home's ~/.agents/skills, which Codex lists (#406), and in
// OpenCode's and agy's configuration directories when they exist (#461).
func skillRoots(o Options) []core.SkillRoot {
	var roots []core.SkillRoot
	for _, root := range core.SkillRoots(o.ClaudeConfig, o.Services.Home, o.Opencode, o.AgyRoot) {
		if root.Present() {
			roots = append(roots, root)
		}
	}
	return roots
}

// pickupSkillPaths are the koinon-pickup skill files by family.
func pickupSkillPaths(o Options) []PickupSkill {
	var skills []PickupSkill
	for _, root := range skillRoots(o) {
		skills = append(skills, PickupSkill{Family: root.Family, Path: root.Path(core.PickupSkillName)})
	}
	return skills
}

// installPickupSkills writes the managed cycle's koinon-pickup skill for each family, for the
// person who runs the daemon and never into a repository (#406), and the other skill entries
// at the same places (#453, #461).
func installPickupSkills(o Options, r *Report) error {
	for _, skill := range pickupSkillPaths(o) {
		result, err := core.InstallPickupSkill(skill.Path)
		if err != nil {
			return fmt.Errorf("pickup_skill_failed: %s: %v", skill.Path, err)
		}
		skill.Result = result
		r.PickupSkills = append(r.PickupSkills, skill)
	}
	return eachTeamSkill(o, r, core.InstallSkillFile)
}

// eachTeamSkill applies change to each family's skill entry files and reports them.
func eachTeamSkill(o Options, r *Report, change func(path string, content []byte) (string, error)) error {
	skills, err := core.TeamSkills()
	if err != nil {
		return fmt.Errorf("team_skill_failed: %v", err)
	}
	roots := skillRoots(o)
	for _, skill := range skills {
		for _, root := range roots {
			target := PickupSkill{Family: root.Family, Name: skill.Name, Path: root.Path(skill.Name)}
			result, err := change(target.Path, skill.Content)
			if err != nil {
				return fmt.Errorf("team_skill_failed: %s: %v", target.Path, err)
			}
			target.Result = result
			r.TeamSkills = append(r.TeamSkills, target)
		}
	}
	return nil
}

// Install places the binary, writes and starts the service, waits until the daemon
// answers, runs setup for each named agent and installs the koinon-pickup skill. A
// Python-era installation refuses it.
func Install(ctx context.Context, o Options) (Report, error) {
	if err := o.defaults(); err != nil {
		return Report{}, err
	}
	r := Report{Binary: Binary(o.Prefix), Backend: o.Services.Backend, Artifact: o.Services.DaemonArtifact(),
		StateDir: o.StateDir, Agents: []setup.Report{}, PickupSkills: []PickupSkill{}, TeamSkills: []PickupSkill{}}
	if !o.Upgrade {
		i, err := legacy.ReadInstall(o.PythonPrefix)
		if err != nil {
			return r, err
		}
		if i != nil {
			return r, fmt.Errorf("python_install_present: a Python-era installation is at %s; run koinon upgrade --from-python", o.PythonPrefix)
		}
	}
	system, err := systemMode(ctx, o)
	if err != nil {
		return r, err
	}
	// The service runs serve --state-dir alone, so the daemon reads its port from the state
	// directory, as every client of that directory does; the service file stays the same.
	if o.Port != 0 {
		if err := core.RecordPort(o.StateDir, o.Port); err != nil {
			return r, err
		}
	}
	if r.Port, err = core.RecordedPort(o.StateDir); err != nil {
		return r, err
	}
	if o.Address == "" {
		o.Address = core.Listeners(r.Port)[0]
	}
	// In user mode, a Homebrew binary is linked, so that brew upgrade alone upgrades the
	// daemon (#397). A system-mode daemon keeps a copy that brew cannot replace.
	if opt := homebrewOpt(o.Source); opt != "" && system == nil {
		r.BinaryResult, err = placeLink(opt, r.Binary)
	} else {
		r.BinaryResult, err = place(o.Source, r.Binary)
	}
	if err != nil {
		return r, err
	}
	if r.Link, r.LinkResult, err = link(r.Binary, o.LinkDir, linkMarker(o.Prefix)); err != nil {
		return r, err
	}
	r.OnPath = lookPath(o.Path)
	r.PathStep = pathStep(r, o.LinkDir, o.Path)
	if r.OtherBinary = otherBinary(o.Source, r.Binary, o.Path); r.OtherBinary != "" {
		r.SourceStep = fmt.Sprintf("this ran the installed binary, so nothing was copied; to install %s, run %s install", r.OtherBinary, shellQuote(r.OtherBinary))
	}
	if system != nil {
		r.Artifact = o.Services.SystemArtifact()
		if err := o.Services.InstallSystem(ctx, platform.SystemInstall{Binary: r.Binary, State: o.StateDir, Plist: *system,
			StatusPID: func(ctx context.Context) (int, error) { return statusPID(ctx, o) }, Wait: o.SystemWait}); err != nil {
			return r, err
		}
		r.Service = "system"
	} else if err := startAgent(ctx, o, &r); err != nil {
		return r, err
	}
	agents, err := reviewFamilies(ctx, o, &r)
	if err != nil {
		return r, err
	}
	for _, agent := range agents {
		report, err := o.Setup(ctx, setup.Options{Family: agent, Binary: r.Binary, StateDir: o.StateDir, ClaudeConfig: o.ClaudeConfig})
		r.Agents = append(r.Agents, report)
		if err != nil {
			return r, fmt.Errorf("setup %s: %w", agent, err)
		}
	}
	// A family that an earlier setup configured keeps starting through its launcher,
	// though this install did not name it (#273). Its configuration is only read.
	for _, family := range setupFamilies {
		if slices.Contains(agents, family) {
			continue
		}
		report, adopted, err := o.Adopt(ctx, setup.Options{Family: family, Binary: r.Binary, StateDir: o.StateDir})
		if err != nil {
			return r, fmt.Errorf("record %s launcher: %w", family, err)
		}
		if adopted {
			r.Agents = append(r.Agents, report)
		}
	}
	if err := offerPermissions(o, agents, &r); err != nil {
		return r, err
	}
	if err := installPickupSkills(o, &r); err != nil {
		return r, err
	}
	r.OK = true
	return r, nil
}

// startAgent writes the user service and, unless NoStart, starts it and waits for the daemon.
func startAgent(ctx context.Context, o Options, r *Report) error {
	if _, err := o.Services.WriteDaemon(r.Binary, o.StateDir); err != nil {
		return err
	}
	r.Service = "staged"
	if o.NoStart {
		return nil
	}
	switch err := o.Services.StartDaemon(ctx); {
	case errors.Is(err, platform.ErrManualRequired):
		r.Service, r.StartCommand = "manual_required", r.Binary+" serve --state-dir "+o.StateDir
	case err != nil:
		return err
	default:
		if err := waitForDaemon(ctx, o); err != nil {
			return err
		}
		r.Service = "running"
	}
	return nil
}

// systemMode returns the system plist when this install runs in system mode, or nil. It
// refuses before anything changes: --system off macOS, as root or without a start, and a
// hand-written LaunchDaemon that does not run this binary, state directory and user.
func systemMode(ctx context.Context, o Options) (*platform.SystemPlist, error) {
	if o.Services.Backend != "launchd" {
		if o.System {
			return nil, errors.New("system_mode_unsupported: --system installs a macOS LaunchDaemon; Linux keeps the systemd user unit")
		}
		return nil, nil
	}
	p, err := o.Services.ReadSystem(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case !o.System && !p.Present:
		if err := o.Services.CheckLogDir(); err != nil {
			return nil, err
		}
		return nil, nil
	case o.Services.UID == 0:
		return nil, errors.New("system_mode_root: run koinon install --system as the user the daemon runs as; it asks sudo for each privileged step")
	case o.NoStart:
		return nil, errors.New("system_mode_no_start: a system-mode install always starts and verifies the LaunchDaemon")
	case p.Present:
		if err := o.Services.CheckSystem(p, Binary(o.Prefix), o.StateDir); err != nil {
			return nil, err
		}
	}
	if err := o.Services.CheckLogDir(); err != nil {
		return nil, err
	}
	return &p, nil
}

// statusPID is the process ID that the answering daemon reports.
func statusPID(ctx context.Context, o Options) (int, error) {
	secret, err := core.ReadSecret(o.StateDir)
	if err != nil {
		return 0, err
	}
	data, err := o.Status(ctx, o.Address, secret)
	if err != nil {
		return 0, err
	}
	var status struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return 0, err
	}
	return status.PID, nil
}

// waitForDaemon polls the daemon's status until it answers or the wait ends.
func waitForDaemon(ctx context.Context, o Options) error {
	deadline := time.Now().Add(o.Wait)
	var last error
	for time.Now().Before(deadline) {
		secret, err := core.ReadSecret(o.StateDir)
		if err == nil {
			if _, err = o.Status(ctx, o.Address, secret); err == nil {
				return nil
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("daemon_unavailable: the daemon did not answer at %s: %v", o.Address, last)
}

// Uninstall removes what setup added for each named agent (every family by default),
// stops and removes the service artifact that carries the marker, removes the koinon-pickup
// skill files that still hold this binary's skill, and removes the binary and the link that
// the install created while it still points at the binary. It keeps the state directory. A
// repeated uninstall changes nothing.
func Uninstall(ctx context.Context, o Options) (Report, error) {
	if err := o.defaults(); err != nil {
		return Report{}, err
	}
	r := Report{Binary: Binary(o.Prefix), Backend: o.Services.Backend, Artifact: o.Services.DaemonArtifact(),
		StateDir: o.StateDir, Agents: []setup.Report{}, PickupSkills: []PickupSkill{}, TeamSkills: []PickupSkill{}}
	agents := o.Agents
	if len(agents) == 0 {
		agents = Families
	}
	for _, agent := range agents {
		report, err := o.Remove(ctx, setup.Options{Family: agent, Binary: r.Binary, StateDir: o.StateDir})
		r.Agents = append(r.Agents, report)
		if err != nil {
			return r, fmt.Errorf("remove %s: %w", agent, err)
		}
	}
	removed, err := o.Services.RemoveDaemon(ctx)
	if err != nil {
		return r, err
	}
	if o.Services.Backend == "launchd" {
		// System mode; the user agent stays disabled until a later plain install.
		system, err := o.Services.RemoveSystem(ctx)
		if err != nil {
			r.Artifact = o.Services.SystemArtifact()
			return r, err
		}
		if system {
			r.Artifact = o.Services.SystemArtifact()
		}
		removed = removed || system
	}
	r.Service = map[bool]string{true: "removed", false: "absent"}[removed]
	for _, skill := range pickupSkillPaths(o) {
		result, err := core.RemovePickupSkill(skill.Path)
		if err != nil {
			return r, fmt.Errorf("pickup_skill_failed: %s: %v", skill.Path, err)
		}
		skill.Result = result
		r.PickupSkills = append(r.PickupSkills, skill)
	}
	if err := eachTeamSkill(o, &r, core.RemoveSkillFile); err != nil {
		return r, err
	}
	r.BinaryResult = "absent"
	if info, err := os.Lstat(r.Binary); err == nil {
		// A Homebrew link goes; the keg it points at stays.
		if !info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return r, fmt.Errorf("refusing to remove %s: not a regular file or link", r.Binary)
		}
		if err := os.Remove(r.Binary); err != nil {
			return r, err
		}
		r.BinaryResult = "removed"
	}
	if r.Link, r.LinkResult, err = unlink(r.Binary, o.LinkDir, linkMarker(o.Prefix)); err != nil {
		return r, err
	}
	// Empty directories of the prefix go too; anything else in them stays.
	os.Remove(filepath.Dir(r.Binary))
	os.Remove(o.Prefix)
	r.OK = true
	return r, nil
}

// unlink removes the link that the marker records while it still points at binary, then
// the marker. A link the install did not create, or one that now points elsewhere, is
// kept.
func unlink(binary, dir, marker string) (string, string, error) {
	path := filepath.Join(dir, "koinon")
	recorded, err := readMarker(marker)
	switch {
	case err != nil:
		return path, "", err
	case recorded != "":
		path = recorded
		if target, err := os.Readlink(path); err == nil && target == binary {
			if err := os.Remove(path); err != nil {
				return path, "", err
			}
			if err := os.Remove(marker); err != nil {
				return path, "", err
			}
			return path, "removed", nil
		}
		if err := os.Remove(marker); err != nil {
			return path, "", err
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return path, "kept", nil
	}
	return path, "absent", nil
}

// setupFamilies are the families that setup configures through a native CLI, in the order
// of the review.
var setupFamilies = []string{"claude", "codex", "agy", "opencode", "grok"}

// reviewFamilies returns the families to set up: the --agent families and, at a terminal,
// each installed family that is not configured and that the person accepts (#449).
func reviewFamilies(ctx context.Context, o Options, r *Report) ([]string, error) {
	agents := slices.Clone(o.Agents)
	if o.Ask == nil {
		return agents, nil
	}
	fmt.Fprintln(o.Tell, "Agent families on this computer:")
	for _, family := range setupFamilies {
		in := o.Inspect(ctx, setup.Options{Family: family, Binary: r.Binary, StateDir: o.StateDir, ClaudeConfig: o.ClaudeConfig})
		r.Review = append(r.Review, in)
		state := "not installed"
		switch {
		case in.CLI != "" && in.Configured:
			state = "set up (" + in.CLI + ")"
		case in.CLI != "":
			state = "not set up (" + in.CLI + ")"
		}
		if p := in.Permissions; p != nil {
			state += "; permission rules " + p.Outcome
			if p.Reason != "" {
				state += " (" + p.Reason + ")"
			}
		}
		fmt.Fprintf(o.Tell, "  %-9s %s\n", family, state)
	}
	for _, in := range r.Review {
		if in.CLI == "" || in.Configured || slices.Contains(agents, in.Family) {
			continue
		}
		yes, err := o.Ask("Set up Koinon for " + in.Family + "?")
		if err != nil {
			return agents, err
		}
		if yes {
			agents = append(agents, in.Family)
		}
	}
	return agents, nil
}

// offerPermissions shows, at a terminal, the missing permission rules of each family that is
// set up and adds them when the person accepts. Without a terminal it changes no rule.
func offerPermissions(o Options, agents []string, r *Report) error {
	if o.Ask == nil {
		return nil
	}
	for _, family := range setupFamilies {
		configured := slices.Contains(agents, family)
		for _, in := range r.Review {
			configured = configured || (in.Family == family && in.Configured)
		}
		if !configured {
			continue
		}
		so := setup.Options{Family: family, Binary: r.Binary, StateDir: o.StateDir, ClaudeConfig: o.ClaudeConfig}
		result, err := setup.Offer(so, o.Permissions, o.Ask, o.Tell)
		if err != nil {
			return err
		}
		r.Permissions = append(r.Permissions, result)
		// The family's review and own entries show the rules after the question, as koinon setup
		// does (#462).
		for i := range r.Review {
			if r.Review[i].Family == family {
				r.Review[i].Permissions = &result
			}
		}
		for i := range r.Agents {
			if r.Agents[i].Family != family {
				continue
			}
			r.Agents[i].Permissions = &result
			if result.Outcome == "added" {
				r.Agents[i].Changed = append(r.Agents[i].Changed, family+" permission rules in "+result.SettingsFile)
			}
		}
	}
	return nil
}
