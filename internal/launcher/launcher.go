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
	"strconv"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

type Options struct {
	Family    string
	StateDir  string
	Address   string
	CLI       string
	Directory string
	Session   string
	Args      []string
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

// CheckDirectory refuses nested repositories, including worktree .git files.
// A scan failure is a refusal, since an unreadable subtree cannot be verified.
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
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return errors.New("cannot verify start directory")
		}
		if entry.Name() == ".git" {
			if filepath.Dir(path) != abs {
				return errors.New("start directory holds another repository; start in that repository instead")
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return abs, err
}

func cleanEnvironment(env []string) []string {
	result := []string{}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "KOINON_") || key == "OPENCODE_SERVER_PASSWORD" || key == "OPENCODE_SERVER_USERNAME" {
			continue
		}
		result = append(result, entry)
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
	cmd := exec.CommandContext(ctx, tmux, tmuxArgs(socket, "new-session", "-d", "-P", "-F", "#{session_id}\t#{pane_id}", "-s", name, "-c", directory, "exec "+strings.Join(quoted, " "))...)
	cmd.Env = cleanEnvironment(os.Environ())
	data, err := cmd.Output()
	if err != nil {
		return errors.New("tmux did not create a new session; name may be taken or server unavailable")
	}
	fields := strings.Split(strings.TrimSpace(string(data)), "\t")
	if len(fields) != 2 {
		return errors.New("invalid tmux launch result")
	}
	if o.Session != "" {
		return json.NewEncoder(out).Encode(map[string]any{"ok": true, "session": name, "session_id": fields[0], "pane_id": fields[1], "socket": socket})
	}
	return platform.Exec(tmux, append([]string{tmux}, tmuxArgs(socket, "attach-session", "-t", fields[0])...), cleanEnvironment(os.Environ()))
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
	if o.Family != "codex" && o.Family != "agy" && o.Family != "opencode" {
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
	target := core.LaunchTarget{Family: o.Family, Directory: directory, CLI: cli, HostPID: os.Getpid()}
	args := append([]string{cli}, o.Args...)
	env := cleanEnvironment(os.Environ())
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
		args = append([]string{cli, "-c", "shell_environment_policy.set.KOINON_CODEX_HOST=\"" + pid + "\""}, o.Args...)
		env = append(env, "KOINON_CODEX_HOST="+pid)
	}
	if err := os.Chdir(directory); err != nil {
		return errors.New("cannot enter start directory")
	}
	return platform.Exec(cli, args, env)
}
