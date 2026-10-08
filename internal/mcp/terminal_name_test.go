package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// namingFixture is a private tmux server under a temporary directory. Its Tmux runner
// fails the test when a call names any other server, so no test reaches the user's tmux.
type namingFixture struct {
	t      *testing.T
	socket string
	mu     sync.Mutex
	agents map[int]bool // processes that the synthetic Command reports as Claude Code
}

func newNamingFixture(t *testing.T) *namingFixture {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("tmux required in CI")
		}
		t.Skip("tmux not installed")
	}
	// A short unresolved socket path also fits macOS's AF_UNIX bound.
	dir, err := os.MkdirTemp("", "km-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	f := &namingFixture{t: t, socket: filepath.Join(dir, "s"), agents: map[int]bool{}}
	t.Cleanup(func() { exec.Command(tmux, "-S", f.socket, "kill-server").Run(); os.RemoveAll(dir) })
	// The first session starts the server without reading any user configuration.
	if out, err := exec.Command(tmux, "-S", f.socket, "-f", "/dev/null", "new-session", "-d", "-s", "keeper", "sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("private tmux: %v %s", err, out)
	}
	return f
}

func (f *namingFixture) tmux(ctx context.Context, socket string, args ...string) (string, error) {
	if socket != f.socket {
		f.t.Errorf("a test reached the tmux server at %s", socket)
		return "", errors.New("not the private server")
	}
	return RunTmux(ctx, socket, args...)
}

func (f *namingFixture) run(args ...string) string {
	f.t.Helper()
	out, err := f.tmux(context.Background(), f.socket, args...)
	if err != nil {
		f.t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimRight(out, "\n")
}

// pane holds a shell whose descendant is the synthetic host process; middle is the process
// between them in a deeper pane, else 0.
type pane struct {
	id, session         string
	shell, middle, host int
}

const (
	hostCommand  = "sh -c 'sleep 300 & wait'"
	deepCommand  = `sh -c 'sh -c "sleep 300 & wait" & wait'`
	waitForChild = 5 * time.Second
)

func (f *namingFixture) newPane(args ...string) pane {
	return f.startPane(hostCommand, args...)
}

func (f *namingFixture) startPane(command string, args ...string) pane {
	f.t.Helper()
	fields := strings.Split(f.run(append(args, "-P", "-F", "#{pane_id}\t#{pane_pid}\t#{session_id}", command)...), "\t")
	shell, _ := strconv.Atoi(fields[1])
	p := pane{id: fields[0], session: fields[2], shell: shell}
	child := func(parent int) int {
		deadline := time.Now().Add(waitForChild)
		for time.Now().Before(deadline) {
			parents, err := platform.ProcessParents()
			if err != nil {
				f.t.Fatal(err)
			}
			for pid, value := range parents {
				if value == parent {
					return pid
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		f.t.Fatal("the pane's host process did not start")
		return 0
	}
	p.host = child(shell)
	if command == deepCommand {
		p.middle, p.host = p.host, child(p.host)
	}
	return p
}

func (f *namingFixture) newSession(name string) pane {
	return f.newPane("new-session", "-d", "-s", name)
}

func (f *namingFixture) agent(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[pid] = true
}

func (f *namingFixture) command(pid int) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agents[pid] {
		return "/opt/synthetic/claude/versions/1.0.0", []string{"claude"}, nil
	}
	return "/bin/sleep", []string{"sleep"}, nil
}

func (f *namingFixture) server(env map[string]string) *server {
	return &server{c: Config{
		Getenv:  func(name string) string { return env[name] },
		Command: f.command, Tmux: f.tmux, Parents: platform.ProcessParents, Now: time.Now,
	}, sessions: map[core.Key]registered{}, named: map[core.Key]string{}, naming: map[core.Key]bool{}}
}

func (f *namingFixture) env(p pane) map[string]string {
	return map[string]string{"TMUX": f.socket + ",1,0", "TMUX_PANE": p.id}
}

func (f *namingFixture) sessionName(p pane) string {
	return f.run("display-message", "-p", "-t", p.id, "#{session_name}")
}

func TestTerminalNamingRules(t *testing.T) {
	f := newNamingFixture(t)
	ctx := context.Background()

	own := f.newSession("own")
	s := f.server(f.env(own))
	if got := s.nameTerminal(ctx, own.host, "claude-koinon"); got != "renamed" || f.sessionName(own) != "claude-koinon" {
		t.Fatalf("own session: %s, named %q", got, f.sessionName(own))
	}
	if got := s.nameTerminal(ctx, own.host, "claude-koinon"); got != "unchanged" {
		t.Fatalf("same name again: %s", got)
	}

	// An inherited TMUX_PANE that names another session's pane renames nothing.
	other := f.newSession("other")
	mine := f.newSession("mine")
	s = f.server(f.env(other))
	if got := s.nameTerminal(ctx, mine.host, "claude-elsewhere"); got != "pane_not_host" {
		t.Fatalf("inherited pane: %s", got)
	}
	if f.sessionName(other) != "other" || f.sessionName(mine) != "mine" {
		t.Fatalf("a session was renamed: %q %q", f.sessionName(other), f.sessionName(mine))
	}

	// A name that another session has is refused.
	taken := f.newSession("taken")
	if got := f.server(f.env(taken)).nameTerminal(ctx, taken.host, "claude-koinon"); got != "name_taken" || f.sessionName(taken) != "taken" {
		t.Fatalf("taken name: %s, named %q", got, f.sessionName(taken))
	}

	// Another agent between the host and the pane: the pane is that agent's.
	nested := f.newSession("nested")
	f.agent(nested.shell)
	if got := f.server(f.env(nested)).nameTerminal(ctx, nested.host, "claude-nested"); got != "nested_agent" || f.sessionName(nested) != "nested" {
		t.Fatalf("nested agent: %s, named %q", got, f.sessionName(nested))
	}

	// The same with the other agent between the pane's shell and the host.
	deep := f.startPane(deepCommand, "new-session", "-d", "-s", "deep")
	f.agent(deep.middle)
	if got := f.server(f.env(deep)).nameTerminal(ctx, deep.host, "claude-deep"); got != "nested_agent" || f.sessionName(deep) != "deep" {
		t.Fatalf("agent between: %s, named %q", got, f.sessionName(deep))
	}

	// A session shared with another agent's pane: only this pane is titled.
	shared := f.newSession("shared")
	neighbour := f.newPane("split-window", "-d", "-t", shared.session)
	f.agent(neighbour.host)
	if got := f.server(f.env(shared)).nameTerminal(ctx, shared.host, "claude-shared"); got != "pane_titled" {
		t.Fatalf("shared session: %s", got)
	}
	if f.sessionName(shared) != "shared" || f.run("display-message", "-p", "-t", shared.id, "#{pane_title}") != "claude-shared" {
		t.Fatalf("shared session: named %q", f.sessionName(shared))
	}
	if title := f.run("display-message", "-p", "-t", neighbour.id, "#{pane_title}"); title == "claude-shared" {
		t.Fatal("the other agent's pane was titled")
	}

	// Outside tmux, and with a name tmux would change, nothing is run.
	if got := f.server(map[string]string{}).nameTerminal(ctx, own.host, "claude-koinon"); got != "not_in_tmux" {
		t.Fatalf("outside tmux: %s", got)
	}
	if got := f.server(f.env(own)).nameTerminal(ctx, own.host, "claude.koinon"); got != "invalid_name" {
		t.Fatalf("invalid name: %s", got)
	}
}

func TestTerminalNamedAgainWhenAliasChanges(t *testing.T) {
	f := newNamingFixture(t)
	p := f.newSession("start")
	s := f.server(f.env(p))
	s.claude = true
	caller := core.Key{Family: "claude", ID: "synthetic-claude"}
	target, _ := json.Marshal(map[string]any{"claude_pid": p.host})
	result := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			busy := s.naming[caller]
			s.mu.Unlock()
			s.obs.mu.Lock()
			got := s.obs.naming[caller]
			s.obs.mu.Unlock()
			if !busy && got == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("naming result: want %s", want)
	}
	s.nameAfterRegistration(caller, core.Session{Family: "claude", Name: "claude-koinon-0d", WakeTarget: target})
	result("renamed")
	if f.sessionName(p) != "claude-koinon-0d" {
		t.Fatalf("peer name: %q", f.sessionName(p))
	}
	// A renewal with the same published name runs no tmux command.
	f.run("rename-session", "-t", p.session, "by-hand")
	s.nameAfterRegistration(caller, core.Session{Family: "claude", Name: "claude-koinon-0d", WakeTarget: target})
	time.Sleep(200 * time.Millisecond)
	if f.sessionName(p) != "by-hand" {
		t.Fatalf("renamed without a name change: %q", f.sessionName(p))
	}
	// Taking the alias renames it again.
	s.nameAfterRegistration(caller, core.Session{Family: "claude", Name: "claude-koinon-0d", Alias: "claude-koinon", WakeTarget: target})
	result("renamed")
	if f.sessionName(p) != "claude-koinon" {
		t.Fatalf("alias: %q", f.sessionName(p))
	}

	// A session whose terminal the daemon does not attribute is never named: an unlaunched
	// Codex session, and a Claude session whose server is not the child of Claude.
	codex := core.Key{Family: "codex", ID: "synthetic-codex"}
	s.nameAfterRegistration(codex, core.Session{Family: "codex", Name: "codex-koinon-1a", WakeTarget: target})
	s.claude = false
	s.nameAfterRegistration(core.Key{Family: "claude", ID: "synthetic-other"}, core.Session{Family: "claude", Name: "claude-koinon-2b", WakeTarget: target})
	time.Sleep(200 * time.Millisecond)
	s.mu.Lock()
	attempts := len(s.named)
	s.mu.Unlock()
	if attempts != 1 || f.sessionName(p) != "claude-koinon" {
		t.Fatalf("an unattributed session was named: %d attempts, %q", attempts, f.sessionName(p))
	}
}
