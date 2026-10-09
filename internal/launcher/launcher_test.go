package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

var launcherBinary string

func TestMain(m *testing.M) {
	if os.Getenv("KOINON_TEST_FAKE_CLI") == "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "koinon-launcher-binary-")
	if err != nil {
		panic(err)
	}
	launcherBinary = filepath.Join(dir, "koinon")
	cmd := exec.Command("go", "build", "-o", launcherBinary, "../../cmd/koinon")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintln(os.Stderr, string(out))
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type report struct {
	PID           int
	Directory     string
	Args          []string
	ClaudeNames   []string
	Nested        bool
	ConfigDir     *string
	Host          string
	LaunchID      string
	Session       core.Session
	Authenticated bool
}

// The configured synthetic CLI uses the daemon's real HTTP API to bind its
// native identity. It never connects to a real agent or reads user configuration.
func TestFakeCLI(t *testing.T) {
	if os.Getenv("KOINON_TEST_FAKE_CLI") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	var resultPath, family string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--result" {
			resultPath = args[i+1]
		}
		if args[i] == "--family" {
			family = args[i+1]
		}
	}
	if resultPath == "" || family == "" {
		os.Exit(10)
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(11)
	}
	_, nested := os.LookupEnv("CLAUDECODE")
	var config *string
	if value, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
		config = &value
	}
	r := report{PID: os.Getpid(), Directory: directory, Args: args, Host: os.Getenv("KOINON_CODEX_HOST"), LaunchID: os.Getenv("KOINON_LAUNCH_ID"), Nested: nested, ConfigDir: config}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "CLAUDE_") {
			r.ClaudeNames = append(r.ClaudeNames, name)
		}
	}
	if family == "opencode" {
		var host, port string
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--hostname" {
				host = args[i+1]
			}
			if args[i] == "--port" {
				port = args[i+1]
			}
		}
		if host != "127.0.0.1" || port == "" || len(os.Getenv("OPENCODE_SERVER_PASSWORD")) != 64 {
			os.Exit(12)
		}
		listener, err := net.Listen("tcp4", net.JoinHostPort(host, port))
		if err != nil {
			os.Exit(13)
		}
		password := os.Getenv("OPENCODE_SERVER_PASSWORD")
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			user, pass, ok := request.BasicAuth()
			if !ok || user != "opencode" || pass != password {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(204)
		}), ReadHeaderTimeout: time.Second}
		go server.Serve(listener)
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
		url := "http://" + listener.Addr().String() + "/session/status"
		response, err := client.Get(url)
		if err != nil || response.StatusCode != 401 {
			os.Exit(14)
		}
		response.Body.Close()
		request, _ := http.NewRequest("GET", url, nil)
		request.SetBasicAuth("opencode", password)
		response, err = client.Do(request)
		if err != nil || response.StatusCode != 204 {
			os.Exit(15)
		}
		response.Body.Close()
		r.Authenticated = true
		server.Close()
	}
	secret, err := core.ReadSecret(os.Getenv("KOINON_STATE_DIR"))
	if err != nil {
		os.Exit(16)
	}
	// The launcher's exec keeps its process ID, so this process is the launch's host, as
	// the agent CLI is; a Claude server also reports it as claude_pid.
	own := core.Registration{Family: family, ID: "synthetic-" + family, Directory: directory, LaunchID: r.LaunchID, Ancestors: []int{os.Getpid()}}
	if family == "claude" {
		own.WakeTarget = json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, os.Getpid()))
	}
	body, _ := json.Marshal(own)
	request, _ := http.NewRequest("POST", "http://"+os.Getenv("KOINON_DAEMON_ADDRESS")+"/v1/sessions/register", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second * 5}
	response, err := client.Do(request)
	if err != nil {
		os.Exit(17)
	}
	var registration struct {
		OK      bool         `json:"ok"`
		Session core.Session `json:"session"`
	}
	err = json.NewDecoder(response.Body).Decode(&registration)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !registration.OK {
		os.Exit(18)
	}
	r.Session = registration.Session
	data, _ := json.Marshal(r)
	if os.WriteFile(resultPath, data, 0600) != nil {
		os.Exit(19)
	}
	os.Exit(0)
}

func fixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	root := t.TempDir()
	d, err := core.Start(core.Config{StateDir: filepath.Join(root, "state"), Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "configured-cli")
	script := "#!/bin/sh\nKOINON_TEST_FAKE_CLI=1\nexport KOINON_TEST_FAKE_CLI\nexec " + quote(self) + " -test.run=TestFakeCLI -- \"$@\"\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "project")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "synthetic-stale-caller")
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "synthetic-token")
	t.Setenv("CLAUDE_OTHER_TEST", "synthetic-other")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude-config"))
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("KOINON_LAUNCH_ID", "synthetic-stale-launch")
	return root, d.Addresses()[0], cli, directory
}

func readReport(t *testing.T, path string) report {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var r report
			if json.Unmarshal(data, &r) == nil {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic CLI did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func checkReport(t *testing.T, r report, family, directory string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	// Only a started Claude keeps its user's configuration directory; no agent inherits
	// another Claude session's variables or nesting marker.
	kept := len(r.ClaudeNames) == 0
	if family == "claude" {
		kept = len(r.ClaudeNames) == 1 && r.ClaudeNames[0] == "CLAUDE_CONFIG_DIR"
	}
	if r.Directory != canonical || !kept || r.Nested || len(r.LaunchID) != 64 || r.Session.ID != "synthetic-"+family {
		t.Fatalf("invalid launch association or environment: %+v", r)
	}
	var target core.LaunchTarget
	if err := json.Unmarshal(r.Session.WakeTarget, &target); err != nil {
		t.Fatal(err)
	}
	if target.Family != family || target.HostPID != r.PID || target.Directory != canonical {
		t.Fatal("wrong wake target")
	}
	if family == "codex" {
		expected := fmt.Sprint(r.PID)
		// Codex runs on its own, so its MCP servers are children of this process, and
		// passes them the launch variables (#247).
		if r.Host != expected || len(r.Args) < 5 || r.Args[0] != "--no-daemon" || r.Args[1] != "-c" || r.Args[2] != "shell_environment_policy.set.KOINON_CODEX_HOST=\""+expected+"\"" ||
			r.Args[3] != "-c" || r.Args[4] != `mcp_servers.koinon.env_vars=["KOINON_LAUNCH_ID","KOINON_STATE_DIR","KOINON_DAEMON_ADDRESS","TMUX","TMUX_PANE","CODEX_HOME"]` {
			t.Fatalf("Codex command line: %q", r.Args)
		}
	}
	if family == "opencode" && !r.Authenticated {
		t.Fatal("OpenCode listener not authenticated")
	}
	found := false
	for _, arg := range r.Args {
		if arg == "literal ' $() ` ; argument" {
			found = true
		}
	}
	if !found {
		t.Fatal("CLI arguments did not survive literally")
	}
}

func arguments(root, address, cli, directory, family, result string) []string {
	return []string{family, "--state-dir", filepath.Join(root, "state"), "--address", address, "--cli", cli, "--directory", directory, "--", "--result", result, "--family", family, "literal ' $() ` ; argument"}
}

func TestCurrentTerminalAndNoTmux(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		for _, inside := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s-inside-%v", family, inside), func(t *testing.T) {
				root, address, cli, directory := fixture(t)
				if inside {
					t.Setenv("TMUX", filepath.Join(root, "unused-socket")+",1,0")
				} else {
					t.Setenv("TMUX", "")
					t.Setenv("PATH", filepath.Join(root, "no-executables"))
				}
				result := filepath.Join(root, "result.json")
				cmd := exec.Command(launcherBinary, arguments(root, address, cli, directory, family, result)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("launch failed: %v %s", err, out)
				}
				r := readReport(t, result)
				checkReport(t, r, family, directory)
				if r.PID != cmd.Process.Pid {
					t.Fatal("launcher introduced a supervisor process")
				}
			})
		}
	}
}

func TestPrivateTmuxDetachedAndOutside(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("tmux required in CI")
		}
		t.Skip("tmux not installed")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		for _, detached := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s-detached-%v", family, detached), func(t *testing.T) {
				root, address, cli, directory := fixture(t)
				// Short unresolved socket path also fits macOS's AF_UNIX bound.
				socketDir, err := os.MkdirTemp("", "kl-tmux-")
				if err != nil {
					t.Fatal(err)
				}
				socket := filepath.Join(socketDir, "s")
				t.Cleanup(func() { exec.Command(realTmux, "-S", socket, "kill-server").Run(); os.RemoveAll(socketDir) })
				// The private server deliberately inherits stale Claude variables.
				cmd := exec.Command(realTmux, "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "guard", "sleep 60")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("private tmux: %v %s", err, out)
				}
				wrapperDir := filepath.Join(root, "tools")
				os.Mkdir(wrapperDir, 0700)
				attach := filepath.Join(root, "attached")
				// New panes use real tmux. The attach boundary is recorded without
				// requiring a controlling terminal on the CI worker.
				wrapper := "#!/bin/sh\nif [ \"$1\" = attach-session ]; then printf '%s\\n' \"$@\" > " + quote(attach) + "; exit 0; fi\nexec " + quote(realTmux) + " -S " + quote(socket) + " -f /dev/null \"$@\"\n"
				os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte(wrapper), 0700)
				t.Setenv("PATH", wrapperDir)
				result := filepath.Join(root, "result.json")
				args := arguments(root, address, cli, directory, family, result)
				// The role reaches the launch record through the launcher in the new pane.
				args = append(args[:9], append([]string{"--role", "review"}, args[9:]...)...)
				if detached {
					t.Setenv("TMUX", socket+",1,0")
					args = append(args[:9], append([]string{"--tmux-session", "detached"}, args[9:]...)...)
				} else {
					t.Setenv("TMUX", "")
				}
				cmd = exec.Command(launcherBinary, args...)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("tmux launch: %v %s", err, out)
				}
				launched := readReport(t, result)
				checkReport(t, launched, family, directory)
				var target core.LaunchTarget
				if json.Unmarshal(launched.Session.WakeTarget, &target) != nil || target.Role != "review" || launched.Session.Role != "review" {
					t.Fatalf("role not recorded: %+v %s", launched.Session, launched.Session.WakeTarget)
				}
				if detached {
					var result map[string]any
					if json.Unmarshal(out, &result) != nil || result["ok"] != true || result["session"] != "detached" {
						t.Fatalf("invalid detached result %s", out)
					}
					// A name collision must not reuse an existing session. The first session
					// closes when its synthetic CLI exits, which can be after the report is
					// written; occupy the name only once that session is gone. The occupant
					// names sleep by its absolute path, since PATH holds only the wrapper.
					deadline := time.Now().Add(10 * time.Second)
					for exec.Command(realTmux, "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "detached", quote(sleep)+" 60").Run() != nil {
						if time.Now().After(deadline) {
							t.Fatal("cannot occupy the session name")
						}
						time.Sleep(20 * time.Millisecond)
					}
					if err := exec.Command(launcherBinary, args...).Run(); err == nil {
						t.Fatal("reused occupied session")
					}
				} else {
					data, err := os.ReadFile(attach)
					if err != nil || !strings.HasPrefix(string(data), "attach-session\n-t\n$") {
						t.Fatal("outside launch did not attach newly created session")
					}
				}
			})
		}
	}
}

func TestDirectoryGuardAndOptions(t *testing.T) {
	// Nested repositories never refuse a start (#252).
	for _, kind := range []string{"directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			os.Mkdir(filepath.Join(root, ".git"), 0700)
			nested := filepath.Join(root, "nested")
			os.Mkdir(nested, 0700)
			switch kind {
			case "directory":
				os.Mkdir(filepath.Join(nested, ".git"), 0700)
			case "file":
				os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: synthetic"), 0600)
			case "symlink":
				os.Symlink(filepath.Join(root, ".git"), filepath.Join(nested, ".git"))
			}
			if _, err := CheckDirectory(root); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := CheckDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing start directory accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0600)
	if _, err := CheckDirectory(file); err == nil {
		t.Fatal("file accepted as start directory")
	}
	o, err := Parse("agy", []string{"--directory", "relative", "--", "--cli", "literal"})
	if err != nil || o.Directory != "relative" || !reflect.DeepEqual(o.Args, []string{"--cli", "literal"}) {
		t.Fatal("argument delimiter changed CLI arguments")
	}
	for _, arg := range []string{"--port", "--hostname=0.0.0.0", "--mdns", "--no-hostname", "--"} {
		if validateOpenCodeArgs([]string{arg}) == nil {
			t.Fatalf("unsafe OpenCode option allowed: %s", arg)
		}
	}
}

func TestConfiguredPathAndFailures(t *testing.T) {
	root, address, cli, directory := fixture(t)
	data, _ := json.Marshal(map[string]string{"agy": cli})
	os.WriteFile(filepath.Join(root, "state", "launchers.json"), data, 0600)
	o := Options{Family: "agy", StateDir: filepath.Join(root, "state")}
	if path, err := configuredCLI(o); err != nil || path != cli {
		t.Fatal("configured path not selected")
	}
	os.Chmod(filepath.Join(root, "state", "launchers.json"), 0644)
	if _, err := configuredCLI(o); err == nil {
		t.Fatal("unsafe config accepted")
	}
	o.CLI = filepath.Join(root, "missing")
	if _, err := configuredCLI(o); err == nil {
		t.Fatal("missing configured CLI accepted")
	}
	if _, err := configuredCLI(Options{CLI: "agy"}); err == nil {
		t.Fatal("searched PATH for configured CLI")
	}
	nested := filepath.Join(directory, "nested")
	os.Mkdir(nested, 0700)
	os.WriteFile(filepath.Join(nested, ".git"), nil, 0600)
	// A name that the launch record cannot hold is left out and never refuses the start.
	os.MkdirAll(filepath.Join(directory, "line\nbreak", ".git"), 0700)
	// A start directory with a nested repository starts the agent, reports the repository
	// before the CLI starts, and records it with the launch (#252).
	// A TMUX value keeps the start in this process, never in a tmux server.
	t.Setenv("TMUX", filepath.Join(root, "unused-socket")+",1,0")
	cmd := exec.Command(launcherBinary, arguments(root, address, cli, directory, "agy", filepath.Join(root, "result"))...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("start with a nested repository: %v %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "1 nested repositories in the start directory (list incomplete)") || !strings.Contains(stderr.String(), "nested (repository)") {
		t.Fatalf("no nested notice: %q", stderr.String())
	}
	var target core.LaunchTarget
	if err := json.Unmarshal(readReport(t, filepath.Join(root, "result")).Session.WakeTarget, &target); err != nil ||
		!reflect.DeepEqual(target.Nested, []core.NestedRepository{{Path: "nested", Kind: "repository"}}) || !target.NestedIncomplete {
		t.Fatalf("launch record lacks the nested repository: %+v %v", target, err)
	}
	if err := Run(context.Background(), Options{Family: "opencode", CLI: cli, Directory: directory, StateDir: filepath.Join(root, "state"), Address: address, Args: []string{"--hostname=0.0.0.0"}}, io.Discard); err == nil {
		t.Fatal("unsafe launch succeeded")
	}
}

// An existing tmux server has its own environment: a started Claude gets the caller's
// CLAUDE_CONFIG_DIR, exactly, or none when the caller has none (#242 review).
func TestTmuxCarriesClaudeConfiguration(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("tmux required in CI")
		}
		t.Skip("tmux not installed")
	}
	for _, caller := range []*string{ptr("/synthetic/caller config $(touch x) 'q'"), nil} {
		t.Run(fmt.Sprintf("caller-set-%v", caller != nil), func(t *testing.T) {
			root, address, cli, directory := fixture(t)
			socketDir, err := os.MkdirTemp("", "kl-tmux-")
			if err != nil {
				t.Fatal(err)
			}
			socket := filepath.Join(socketDir, "s")
			t.Cleanup(func() { exec.Command(realTmux, "-S", socket, "kill-server").Run(); os.RemoveAll(socketDir) })
			// The server starts with another configuration directory than the caller's.
			server := exec.Command(realTmux, "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "guard", "sleep 60")
			server.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR=/synthetic/server-config")
			if out, err := server.CombinedOutput(); err != nil {
				t.Fatalf("private tmux: %v %s", err, out)
			}
			wrapperDir := filepath.Join(root, "tools")
			os.Mkdir(wrapperDir, 0700)
			wrapper := "#!/bin/sh\nexec " + quote(realTmux) + " -S " + quote(socket) + " -f /dev/null \"$@\"\n"
			os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte(wrapper), 0700)
			t.Setenv("PATH", wrapperDir)
			t.Setenv("TMUX", socket+",1,0")
			if caller != nil {
				t.Setenv("CLAUDE_CONFIG_DIR", *caller)
			} else {
				os.Unsetenv("CLAUDE_CONFIG_DIR")
			}
			result := filepath.Join(root, "result.json")
			args := arguments(root, address, cli, directory, "claude", result)
			args = append(args[:9], append([]string{"--tmux-session", "carried"}, args[9:]...)...)
			if out, err := exec.Command(launcherBinary, args...).CombinedOutput(); err != nil {
				t.Fatalf("tmux launch: %v %s", err, out)
			}
			r := readReport(t, result)
			switch {
			case caller == nil && r.ConfigDir != nil:
				t.Fatalf("the server's configuration reached Claude: %q", *r.ConfigDir)
			case caller != nil && (r.ConfigDir == nil || *r.ConfigDir != *caller):
				got := "none"
				if r.ConfigDir != nil {
					got = *r.ConfigDir
				}
				t.Fatalf("Claude did not get the caller's configuration: %q", got)
			}
			for _, name := range r.ClaudeNames {
				if name != "CLAUDE_CONFIG_DIR" {
					t.Fatalf("Claude variable %s reached Claude", name)
				}
			}
		})
	}
}

func ptr(value string) *string { return &value }
