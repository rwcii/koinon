// Package launcher starts configured agent CLIs in their intended terminal.
package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// Families are the agent families that the launcher starts. A started Claude session
// registers through its own MCP server with its Claude Code session ID, as one started by
// hand does; its launch record documents the start.
var Families = map[string]bool{"claude": true, "codex": true, "agy": true, "opencode": true}

// MCPVariables are the environment variables that koinon mcp reads and the launcher sets or
// inherits. Codex passes an MCP server only the variables that its env_vars lists (#247).
var MCPVariables = []string{"KOINON_LAUNCH_ID", "KOINON_STATE_DIR", "KOINON_DAEMON_ADDRESS", "TMUX", "TMUX_PANE", "CODEX_HOME"}

// codexServer is the MCP server name that koinon setup codex configures.
const codexServer = "koinon"

type Options struct {
	Family    string
	StateDir  string
	Address   string
	CLI       string
	Directory string
	Session   string
	// Background starts a Claude background job (claude --bg) instead of a terminal.
	Background bool
	Args       []string
}

// Parse consumes only leading launcher options; everything after -- is literal.
func Parse(family string, args []string) (Options, error) {
	root, err := platform.DefaultStateDir()
	if err != nil {
		return Options{}, err
	}
	o := Options{Family: family, StateDir: root, Address: "127.0.0.1:47671"}
	for len(args) > 0 {
		key := args[0]
		if key == "--" {
			args = args[1:]
			break
		}
		if key == "--bg" {
			o.Background = true
			args = args[1:]
			continue
		}
		var field *string
		switch key {
		case "--state-dir":
			field = &o.StateDir
		case "--address":
			field = &o.Address
		case "--cli":
			field = &o.CLI
		case "--directory":
			field = &o.Directory
		case "--tmux-session":
			field = &o.Session
		default:
			o.Args = args
			return o, nil
		}
		if len(args) < 2 || args[1] == "" {
			return Options{}, fmt.Errorf("%s needs a value", key)
		}
		*field = args[1]
		args = args[2:]
	}
	o.Args = args
	return o, nil
}

func configuredCLI(o Options) (string, error) {
	return ConfiguredCLI(o.StateDir, o.Family, o.CLI)
}

// ConfiguredCLI returns the absolute agent CLI path: the override when given, else the
// family's entry in private launchers.json. It never searches PATH.
func ConfiguredCLI(stateDir, family, override string) (string, error) {
	path := override
	if path == "" {
		f, err := platform.OpenPrivate(filepath.Join(stateDir, "launchers.json"), os.O_RDONLY)
		if err != nil {
			return "", errors.New("configure an absolute CLI path in private launchers.json or use --cli")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(data) > 16384 {
			return "", errors.New("invalid launcher configuration")
		}
		var config map[string]string
		if json.Unmarshal(data, &config) != nil {
			return "", errors.New("invalid launcher configuration")
		}
		path = config[family]
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("configured CLI path must be absolute")
	}
	path, err := exec.LookPath(path)
	if err != nil {
		return "", errors.New("configured agent CLI is not installed or executable")
	}
	return path, nil
}

// CheckDirectory resolves the start directory: an existing directory, with symlinks
// resolved. Nested repositories do not refuse a start; ScanNested reports them (#252).
func CheckDirectory(path string) (string, error) {
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", errors.New("start directory does not exist")
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", errors.New("start directory is not a directory")
	}
	return abs, nil
}

// scanTime bounds a nested repository scan; a scan that stops early is incomplete.
var scanTime = 3 * time.Second

// ScanNested lists the repositories inside the start directory dir that are not part of
// its repository: every nested checkout with a .git entry except a linked worktree of the
// start repository. It descends into submodules and the start repository's worktrees, but
// not into another repository, whose contents are its own; it never follows directory
// symlinks or enters a .git directory. An unreadable subtree, the time bound, more than
// core.MaxNested entries or a path longer than core.MaxNestedPath make the report
// incomplete; none of them refuses the start.
func ScanNested(dir string) core.NestedReport {
	report := core.NestedReport{}
	// One deadline bounds the whole scan, every Git query included.
	ctx, cancel := context.WithTimeout(context.Background(), scanTime)
	defer cancel()
	start, _ := gitPath(ctx, dir, "--git-common-dir")
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			report.Incomplete = true
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			report.Incomplete = true
			return filepath.SkipAll
		}
		if !entry.IsDir() || path == dir {
			return nil
		}
		if entry.Name() == ".git" {
			return filepath.SkipDir
		}
		git, err := os.Lstat(filepath.Join(path, ".git"))
		if err != nil {
			return nil
		}
		kind := nestedKind(ctx, start, path, git.Mode())
		if ctx.Err() != nil {
			// A query cut short by the deadline leaves the kind unknown.
			report.Incomplete = true
			return filepath.SkipAll
		}
		if kind == "" {
			return nil
		}
		// A path that the launch record cannot hold, such as one with a newline, is left out.
		rel, _ := filepath.Rel(dir, path)
		if len(report.List) == core.MaxNested || !core.ValidNestedPath(rel) {
			report.Incomplete = true
		} else {
			report.List = append(report.List, core.NestedRepository{Path: rel, Kind: kind})
		}
		if kind == "submodule" {
			return nil
		}
		return filepath.SkipDir
	})
	if ctx.Err() != nil {
		report.Incomplete = true
	}
	return report
}

// nestedKind classifies the checkout at dir, whose .git entry has mode: "" for a linked
// worktree of the start repository (common directory start); submodule when its enclosing
// repository records exactly dir as a gitlink, whatever the .git layout; worktree for a
// linked worktree of another repository; otherwise repository.
func nestedKind(ctx context.Context, start, dir string, mode fs.FileMode) string {
	if mode.IsRegular() {
		common, err := gitPath(ctx, dir, "--git-common-dir")
		if err == nil && start != "" && common == start {
			return ""
		}
	}
	if recordedGitlink(ctx, filepath.Dir(dir), filepath.Base(dir)) {
		return "submodule"
	}
	if mode.IsRegular() {
		common, commonErr := gitPath(ctx, dir, "--git-common-dir")
		gitDir, dirErr := gitPath(ctx, dir, "--git-dir")
		if commonErr == nil && dirErr == nil && gitDir != common {
			return "worktree"
		}
	}
	return "repository"
}

// NestedNotice is the launcher's report of a nested scan, or "" when it found nothing.
func NestedNotice(dir string, report core.NestedReport) string {
	if len(report.List) == 0 && !report.Incomplete {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "koinon: %d nested repositories in the start directory", len(report.List))
	if report.Incomplete {
		b.WriteString(" (list incomplete)")
	}
	fmt.Fprintf(&b, "; the session's Koinon repository is %s\n", dir)
	for _, n := range report.List {
		fmt.Fprintf(&b, "  %s (%s)\n", n.Path, n.Kind)
	}
	return b.String()
}

// recordedGitlink reports whether the index of the repository at dir holds an entry for
// exactly name, as a gitlink. The pathspec is literal, and an entry below name, which a
// directory pathspec also lists, does not count.
func recordedGitlink(ctx context.Context, dir, name string) bool {
	out, err := git(ctx, dir, "--literal-pathspecs", "ls-files", "--stage", "-z", "--", name)
	if err != nil {
		return false
	}
	for _, entry := range strings.Split(out, "\x00") {
		meta, path, found := strings.Cut(entry, "\t")
		if found && path == name && strings.HasPrefix(meta, "160000 ") {
			return true
		}
	}
	return false
}

// gitPath returns an absolute rev-parse path for the repository at dir, with symlinks
// resolved so that paths compare on macOS too.
func gitPath(ctx context.Context, dir, flag string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--path-format=absolute", flag)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSpace(out))
}

// git runs one read-only Git command in dir without inherited GIT_ variables and with the
// file-system monitor off, so no repository configuration runs a command.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	cmd.Env = core.CleanGitEnvironment()
	// A child that keeps the output open cannot hold the caller past the deadline.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	return string(out), err
}

// carriedConfig carries the caller's CLAUDE_CONFIG_DIR into a new tmux pane, whose
// environment otherwise comes from the tmux server; an empty value means the caller had none.
const carriedConfig = "KOINON_CLAUDE_CONFIG_DIR"

// cleanEnvironment drops the variables that would tie the started agent to another session:
// Claude Code's session variables and nesting marker, Koinon's launch variables and the
// OpenCode credential. A started Claude keeps CLAUDE_CONFIG_DIR, its user's configuration;
// in a pane that the launcher created, the carried value of the caller replaces the server's.
func cleanEnvironment(env []string, family string) []string {
	result := []string{}
	carried, isCarried := "", false
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, carriedConfig+"="); ok {
			carried, isCarried = value, true
		}
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if family == "claude" && key == "CLAUDE_CONFIG_DIR" {
			if !isCarried {
				result = append(result, entry)
			}
			continue
		}
		if strings.HasPrefix(key, "CLAUDE_") || key == "CLAUDECODE" || strings.HasPrefix(key, "KOINON_") || key == "OPENCODE_SERVER_PASSWORD" || key == "OPENCODE_SERVER_USERNAME" {
			continue
		}
		result = append(result, entry)
	}
	if family == "claude" && carried != "" {
		result = append(result, "CLAUDE_CONFIG_DIR="+carried)
	}
	return result
}

func tmuxSocket() string {
	value := os.Getenv("TMUX")
	last := strings.LastIndex(value, ",")
	if last < 0 {
		return ""
	}
	previous := strings.LastIndex(value[:last], ",")
	if previous < 0 {
		return ""
	}
	return value[:previous]
}

func tmuxArgs(socket string, args ...string) []string {
	if socket != "" {
		return append([]string{"-S", socket}, args...)
	}
	return args
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func validSession(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func directorySession(directory string) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, filepath.Base(directory))
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		name = "koinon"
	}
	return name
}

func startTmux(ctx context.Context, tmux, cli, directory string, o Options, out io.Writer) error {
	name := o.Session
	if name == "" {
		name = directorySession(directory)
	}
	if !validSession(name) {
		return errors.New("tmux session name must contain only letters, numbers, underscores and hyphens")
	}
	socket := tmuxSocket()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// tmux executes its pane command through a shell. Quote every argument; never
	// interpolate a directory or model argument as executable shell text.
	args := []string{self, o.Family, "--state-dir", o.StateDir, "--address", o.Address, "--cli", cli, "--directory", directory, "--"}
	args = append(args, o.Args...)
	quoted := make([]string, len(args))
	for i, value := range args {
		quoted[i] = quote(value)
	}
	session := []string{"new-session", "-d", "-P", "-F", "#{session_id}\t#{pane_id}", "-s", name, "-c", directory}
	if o.Family == "claude" {
		// tmux sets it in the new session only, as one literal argument, never through a shell.
		session = append(session, "-e", carriedConfig+"="+os.Getenv("CLAUDE_CONFIG_DIR"))
	}
	cmd := exec.CommandContext(ctx, tmux, tmuxArgs(socket, append(session, "exec "+strings.Join(quoted, " "))...)...)
	cmd.Env = cleanEnvironment(os.Environ(), o.Family)
	data, err := cmd.Output()
	if err != nil {
		return errors.New("tmux did not create a new session; name may be taken or server unavailable")
	}
	fields := strings.Split(strings.TrimSpace(string(data)), "\t")
	if len(fields) != 2 {
		return errors.New("invalid tmux launch result")
	}
	if o.Session != "" {
		result := map[string]any{"ok": true, "session": name, "session_id": fields[0], "pane_id": fields[1], "socket": socket}
		// The launcher in the new pane records its own scan with the launch.
		if nested := ScanNested(directory); len(nested.List) > 0 || nested.Incomplete {
			result["nested"], result["nested_incomplete"] = nested.List, nested.Incomplete
		}
		return json.NewEncoder(out).Encode(result)
	}
	return platform.Exec(tmux, append([]string{tmux}, tmuxArgs(socket, "attach-session", "-t", fields[0])...), cleanEnvironment(os.Environ(), o.Family))
}

func validateOpenCodeArgs(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			return errors.New("OpenCode argument terminator would hide required server options")
		}
		for _, option := range []string{"--port", "--hostname", "--mdns", "--mdns-domain"} {
			if arg == option || strings.HasPrefix(arg, option+"=") || arg == "--no-"+strings.TrimPrefix(option, "--") {
				return errors.New("OpenCode server options are set by Koinon")
			}
		}
	}
	return nil
}

func openCodeTarget(args []string) (string, string, error) {
	if err := validateOpenCodeArgs(args); err != nil {
		return "", "", err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", "", errors.New("cannot allocate an OpenCode loopback port")
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", "", err
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", err
	}
	return address, hex.EncodeToString(nonce[:]), nil
}

func Run(ctx context.Context, o Options, out io.Writer) error {
	if !Families[o.Family] {
		return errors.New("unsupported launcher family")
	}
	directory, err := CheckDirectory(o.Directory)
	if err != nil {
		return err
	}
	cli, err := configuredCLI(o)
	if err != nil {
		return err
	}
	// Resolve before entering tmux so its new shell cannot retarget state/config.
	o.StateDir, err = filepath.Abs(o.StateDir)
	if err != nil {
		return err
	}
	secret, err := core.ReadSecret(o.StateDir)
	if err != nil {
		return errors.New("cannot read private daemon secret; agent was not started")
	}
	if _, err := core.GetStatus(ctx, o.Address, secret); err != nil {
		return err
	}
	if o.Background {
		if o.Family != "claude" || o.Session != "" {
			return errors.New("--bg starts only a Claude background job, outside tmux")
		}
		return runBackground(ctx, o, cli, directory, secret, out)
	}
	if o.Family == "opencode" {
		// Validate before creating even a detached pane.
		if err := validateOpenCodeArgs(o.Args); err != nil {
			return err
		}
	}
	tmux, tmuxErr := exec.LookPath("tmux")
	if o.Session != "" || os.Getenv("TMUX") == "" && tmuxErr == nil {
		if tmuxErr != nil {
			return errors.New("tmux is required for --tmux-session")
		}
		return startTmux(ctx, tmux, cli, directory, o, out)
	}
	nested := ScanNested(directory)
	target := core.LaunchTarget{Family: o.Family, Directory: directory, CLI: cli, HostPID: os.Getpid(),
		Nested: nested.List, NestedIncomplete: nested.Incomplete}
	args := append([]string{cli}, o.Args...)
	env := cleanEnvironment(os.Environ(), o.Family)
	if o.Family == "opencode" {
		target.Address, target.Password, err = openCodeTarget(o.Args)
		if err != nil {
			return err
		}
		_, port, _ := net.SplitHostPort(target.Address)
		args = append(args, "--hostname", "127.0.0.1", "--port", port, "--mdns=false")
		env = append(env, "OPENCODE_SERVER_PASSWORD="+target.Password, "OPENCODE_SERVER_USERNAME=opencode")
	}
	id, err := core.CreateLaunch(ctx, o.Address, secret, target)
	if err != nil {
		return err
	}
	env = append(env, "KOINON_LAUNCH_ID="+id, "KOINON_STATE_DIR="+o.StateDir, "KOINON_DAEMON_ADDRESS="+o.Address)
	if o.Family == "codex" {
		pid := strconv.Itoa(os.Getpid())
		args = append([]string{cli}, codexArgs(pid, o.Args)...)
		env = append(env, "KOINON_CODEX_HOST="+pid)
	}
	if err := os.Chdir(directory); err != nil {
		return errors.New("cannot enter start directory")
	}
	fmt.Fprint(os.Stderr, NestedNotice(directory, nested))
	return platform.Exec(cli, args, env)
}

// codexArgs runs Codex on its own (--no-daemon), so that its MCP servers are children of
// the launched process, and passes koinon mcp the launch variables (#247).
func codexArgs(pid string, user []string) []string {
	args := []string{}
	if !slices.Contains(user, "--no-daemon") {
		args = append(args, "--no-daemon")
	}
	quoted := make([]string, len(MCPVariables))
	for i, name := range MCPVariables {
		quoted[i] = strconv.Quote(name)
	}
	args = append(args, "-c", "shell_environment_policy.set.KOINON_CODEX_HOST=\""+pid+"\"",
		"-c", "mcp_servers."+codexServer+".env_vars=["+strings.Join(quoted, ",")+"]")
	return append(args, user...)
}

// jobID reads the job ID that claude --bg prints: one full session ID, or else one short
// ID (its first eight characters). Anything else is no job ID.
func jobID(output string) string {
	var full, short []string
	for _, field := range strings.FieldsFunc(output, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r == '-')
	}) {
		switch {
		case core.FullJobID.MatchString(field) && !slices.Contains(full, field):
			full = append(full, field)
		case core.ShortJobID.MatchString(field) && !slices.Contains(short, field):
			short = append(short, field)
		}
	}
	if len(full) == 1 {
		return full[0]
	}
	if len(full) == 0 && len(short) == 1 {
		return short[0]
	}
	return ""
}

// runBackground starts a Claude background job with its own launch. A job inherits the
// Claude background service's environment, not this one, so the launch variables reach
// it only through a private --settings file; claude --bg itself runs without them, so a
// service that it starts inherits no launch ID (live-checks F5). The job's ID admits it.
func runBackground(ctx context.Context, o Options, cli, directory, secret string, out io.Writer) error {
	for _, arg := range o.Args {
		if arg == "--bg" || arg == "--background" || arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
			return errors.New("koinon sets --bg and --settings for a background job")
		}
	}
	nested := ScanNested(directory)
	target := core.LaunchTarget{Family: "claude", Directory: directory, CLI: cli, HostPID: os.Getpid(), Background: true,
		Nested: nested.List, NestedIncomplete: nested.Incomplete}
	id, err := core.CreateLaunch(ctx, o.Address, secret, target)
	if err != nil {
		return err
	}
	settings := ""
	retire := func(reason string) error {
		core.Call(ctx, o.Address, secret, "/v1/launches/retire", map[string]string{"launch_id": id})
		if settings != "" {
			os.Remove(settings)
		}
		return errors.New(reason)
	}
	settings, err = writeSettings(o.StateDir, id, map[string]string{"KOINON_LAUNCH_ID": id, "KOINON_STATE_DIR": o.StateDir, "KOINON_DAEMON_ADDRESS": o.Address})
	if err != nil {
		return retire("cannot write the private settings file; the job was not started")
	}
	cmd := exec.CommandContext(ctx, cli, append(append([]string{"--bg"}, o.Args...), "--settings", settings)...)
	cmd.Dir = directory
	cmd.Env = cleanEnvironment(os.Environ(), "claude")
	cmd.Stderr = os.Stderr
	var stdout strings.Builder
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	job := jobID(stdout.String())
	if runErr != nil || job == "" {
		return retire("claude --bg reported no job ID; the launch was retired")
	}
	if _, err := core.Call(ctx, o.Address, secret, "/v1/launches/job", map[string]string{"launch_id": id, "job_id": job}); err != nil {
		return retire("cannot record background job " + job + "; it is not a Koinon session, stop it with claude stop " + job)
	}
	_, err = io.WriteString(out, stdout.String())
	return err
}

// writeSettings writes a private Claude settings file that sets env, under the state
// directory's launches/, and returns its path.
func writeSettings(stateDir, id string, env map[string]string) (string, error) {
	dir, err := platform.PrivateDir(filepath.Join(stateDir, "launches"))
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]any{"env": env})
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	f, err := platform.OpenPrivate(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
