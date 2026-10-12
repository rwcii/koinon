package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// The Grok wake (#254 chunk 03) on a private tmux server, with a synthetic program that
// draws Grok Build 1.0.50's input box and records every line it receives.

// grokScreen draws the recorded screen states. Idle: an empty input box and the idle
// shortcut bar. Busy: a running turn's spinner and cancel bar, with the box still empty.
// Draft: a box that already holds text. After each line it reads, the program draws the
// line as a submitted prompt above an empty box, as Grok does.
const grokScreen = `#!/bin/sh
state=$1 received=$2
box() {
	printf '  ╭────────────────────────────────╮\n  │ ❯ %s\n  ╰────────────────────────────────╯\n' "$1"
	if [ "$state" = busy ]; then printf '  Shift+Tab:mode  │  Ctrl+c:cancel\n'; else printf '  Shift+Tab:mode  │  Ctrl+x:shortcuts\n'; fi
	printf '\033[3A\r\033[6C'
}
printf '\033[2J\033[H'
[ "$state" = busy ] && printf '    ⠧ Waiting for response… 0.9s  [stop]\n'
draft=
[ "$state" = draft ] && draft="synthetic draft"
box "$draft"
# A deaf program never reads: typed keys stay in the box.
[ "$state" = deaf ] && exec sleep 300
while IFS= read -r line; do
	printf '%s\n' "$line" >> "$received"
	printf '\033[2J\033[H     ❯ %s\n' "$line"
	box ""
done
`

type grokFixture struct {
	t              *testing.T
	s              *Store
	dir, socket    string
	program        string
	agents         map[int]bool
	sessionCounter int
}

func newGrokFixture(t *testing.T) *grokFixture {
	t.Helper()
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("tmux required in CI")
		}
		t.Skip("tmux not installed")
	}
	// A short unresolved socket path also fits macOS's AF_UNIX bound.
	dir, err := os.MkdirTemp("", "kg-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	f := &grokFixture{t: t, dir: dir, socket: filepath.Join(dir, "s"), program: filepath.Join(dir, "grok-screen"), agents: map[int]bool{}}
	t.Cleanup(func() { exec.Command(tmux, "-S", f.socket, "kill-server").Run(); os.RemoveAll(dir) })
	if err := os.WriteFile(f.program, []byte(grokScreen), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(tmux, "-S", f.socket, "-f", "/dev/null", "new-session", "-d", "-s", "keeper", "-x", "80", "-y", "20", "sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("private tmux: %v %s", err, out)
	}
	f.s, _ = testStore(t)
	// Every tmux command must reach the private server.
	f.s.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		if socket != f.socket {
			t.Errorf("a wake reached the tmux server at %s", socket)
			return "", errors.New("not the private server")
		}
		return runTmux(ctx, socket, args...)
	}
	real := f.s.processCommand
	f.s.processCommand = func(pid int) (string, []string, error) {
		if f.agents[pid] {
			return "/synthetic/grok", []string{"grok"}, nil
		}
		return real(pid)
	}
	return f
}

func (f *grokFixture) run(args ...string) string {
	f.t.Helper()
	out, err := runTmux(context.Background(), f.socket, args...)
	if err != nil {
		f.t.Fatalf("tmux %v: %v", args, err)
	}
	return strings.TrimRight(out, "\n")
}

// grokPaneUnderTest is a pane with the synthetic program: shell is the pane's process, host
// the program's process (the shell itself unless deep), received the program's record.
type grokPaneUnderTest struct {
	id          string
	shell, host int
	received    string
	middle      int
}

func (f *grokFixture) pane(state string, depth int) grokPaneUnderTest {
	f.t.Helper()
	f.sessionCounter++
	received := filepath.Join(f.dir, "received-"+strconv.Itoa(f.sessionCounter))
	command := "sh " + f.program + " " + state + " " + received
	// A foreground child keeps the terminal; the trailing command stops each shell from
	// replacing itself with its child.
	switch depth {
	case 1:
		command = "sh -c '" + command + "; exit'"
	case 2:
		command = `sh -c 'sh -c "` + command + `; exit"; exit'`
	}
	fields := strings.Fields(f.run("new-window", "-d", "-P", "-F", "#{pane_id} #{pane_pid}", command))
	shell, _ := strconv.Atoi(fields[1])
	p := grokPaneUnderTest{id: fields[0], shell: shell, host: shell, received: received}
	// Walk down to the program: each level is the only child of the one above.
	for level := 0; level < depth; level++ {
		parent, child := p.host, 0
		deadline := time.Now().Add(5 * time.Second)
		for child == 0 && time.Now().Before(deadline) {
			parents, err := platform.ProcessParents()
			if err != nil {
				f.t.Fatal(err)
			}
			for pid, value := range parents {
				if value == parent {
					child = pid
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if child == 0 {
			f.t.Fatal("the deep pane's program did not start")
		}
		p.middle, p.host = parent, child
	}
	// Wait until the program has drawn its box.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.run("capture-pane", "-p", "-t", p.id), "│ ❯") {
		if time.Now().After(deadline) {
			f.t.Fatal("the synthetic Grok screen did not draw")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return p
}

// session registers a plain Grok peer and records host as its host in pane, as a launched
// session's registration does.
func (f *grokFixture) session(host int, pane string) Session {
	f.t.Helper()
	f.sessionCounter++
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", f.sessionCounter)
	session, err := f.s.Register(context.Background(), Registration{Family: "grok", ID: id, Directory: f.dir, TTLSeconds: 60})
	if err != nil {
		f.t.Fatal(err)
	}
	start, err := platform.ProcessStart(host)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`UPDATE sessions SET host_pid=?,host_start=?,tmux_socket=?,tmux_pane=? WHERE family='grok' AND id=?`,
		host, start, f.socket, pane, id); err != nil {
		f.t.Fatal(err)
	}
	return session
}

func (f *grokFixture) received(p grokPaneUnderTest) string {
	data, err := os.ReadFile(p.received)
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return string(data)
}

const grokTestNotice = `Koinon inbox "grok":"synthetic" has waiting sequence range 1-2. Read your Koinon inbox.`

func TestGrokWakeTypesTheNoticeIntoTheProvenIdlePane(t *testing.T) {
	f := newGrokFixture(t)
	p := f.pane("idle", 0)
	other := f.pane("idle", 0)
	session := f.session(p.host, p.id)
	if got := f.s.providerWake(context.Background(), session, grokTestNotice); got != accepted() {
		t.Fatalf("wake: %v", got)
	}
	if got := f.received(p); got != grokTestNotice+"\n" {
		t.Fatalf("received %q", got)
	}
	if got := f.received(other); got != "" {
		t.Fatalf("another pane received %q", got)
	}
}

func TestGrokWakeSendsNothingWithoutProofOrIdle(t *testing.T) {
	f := newGrokFixture(t)
	for name, c := range map[string]func() (Session, grokPaneUnderTest, string){
		"busy turn": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("busy", 0)
			return f.session(p.host, p.id), p, "receiver_busy"
		},
		"draft in the box": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("draft", 0)
			return f.session(p.host, p.id), p, "receiver_busy"
		},
		"foreign pane": func() (Session, grokPaneUnderTest, string) {
			p, foreign := f.pane("idle", 0), f.pane("idle", 0)
			// The recorded pane does not hold the host.
			return f.session(p.host, foreign.id), foreign, "grok_target_unavailable"
		},
		"reused host": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", 0)
			session := f.session(p.host, p.id)
			f.s.db.Exec(`UPDATE sessions SET host_start=host_start+1 WHERE family='grok' AND id=?`, session.ID)
			return session, p, "grok_target_unavailable"
		},
		"agent in the pane": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", 1)
			f.agents[p.middle] = true
			return f.session(p.host, p.id), p, "grok_target_unavailable"
		},
		"agent between the pane and the host": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", 2)
			f.agents[p.middle] = true
			return f.session(p.host, p.id), p, "grok_target_unavailable"
		},
		"no host record": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", 0)
			session := f.session(p.host, p.id)
			f.s.db.Exec(`UPDATE sessions SET host_pid=0,host_start=0,tmux_socket='',tmux_pane='' WHERE family='grok' AND id=?`, session.ID)
			return session, p, "grok_target_unavailable"
		},
	} {
		t.Run(name, func(t *testing.T) {
			session, p, reason := c()
			if got := f.s.providerWake(context.Background(), session, grokTestNotice); got != waiting(reason) {
				t.Fatalf("wake: %v, want waiting %s", got, reason)
			}
			time.Sleep(200 * time.Millisecond)
			if got := f.received(p); got != "" {
				t.Fatalf("received %q", got)
			}
			if screen := f.run("capture-pane", "-p", "-t", p.id); strings.Contains(screen, "Koinon inbox") {
				t.Fatalf("the notice reached the pane:\n%s", screen)
			}
		})
	}
	// Intervening processes that are not agents are allowed.
	p := f.pane("idle", 2)
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id), grokTestNotice); got != accepted() {
		t.Fatalf("wake through plain shells: %v", got)
	}
	// tmux must answer for the recorded pane itself.
	p = f.pane("idle", 0)
	session := f.session(p.host, p.id)
	real := f.s.tmux
	f.s.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		out, err := real(ctx, socket, args...)
		if len(args) > 0 && args[0] == "display-message" {
			out = strings.Replace(out, p.id, "%999", 1)
		}
		return out, err
	}
	if got := f.s.providerWake(context.Background(), session, grokTestNotice); got != waiting("grok_target_unavailable") {
		t.Fatalf("wake when tmux names another pane: %v", got)
	}
	// tmux before 3.4 prints a tab in a format as "_": an answer joined that way is refused.
	f.s.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		out, err := real(ctx, socket, args...)
		if len(args) > 0 && args[0] == "display-message" {
			out = strings.ReplaceAll(out, " ", "_")
		}
		return out, err
	}
	if got := f.s.providerWake(context.Background(), session, grokTestNotice); got != waiting("grok_target_unavailable") {
		t.Fatalf("wake with an underscore-joined answer: %v", got)
	}
	f.s.tmux = real
	if got := f.received(p); got != "" {
		t.Fatalf("received %q", got)
	}
}

// A notice that does not leave the input box is not recorded as delivered.
func TestGrokWakeUnconfirmedSubmission(t *testing.T) {
	f := newGrokFixture(t)
	p := f.pane("deaf", 0)
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id), grokTestNotice); got != uncertain("pane_submission_unconfirmed") {
		t.Fatalf("wake: %v", got)
	}
}

// The recorded Grok screens: the box stays empty during a turn, so the spinner and the
// cancel bar decide.
func TestGrokIdleScreens(t *testing.T) {
	box := "  ╭────╮\n  │ ❯ %s│\n  ╰────╯\n"
	for screen, idle := range map[string]bool{
		strings.Replace(box, "%s", "    ", 1) + "  Shift+Tab:mode  │  Ctrl+x:shortcuts\n":                                      true,
		strings.Replace(box, "%s", "    ", 1) + "                       [stable]\n":                                            true,
		"    ⠧ Waiting for response… 0.9s  [stop]\n" + strings.Replace(box, "%s", "    ", 1):                                   false,
		strings.Replace(box, "%s", "    ", 1) + "  Shift+Tab:mode  │  Ctrl+c:cancel  │  Ctrl+x:shortcuts\n":                    false,
		strings.Replace(box, "%s", "text", 1) + "  Enter:send  │  Alt+Enter:newline  │  Shift+Tab:mode  │  Ctrl+x:shortcuts\n": false,
		"  a sub-agent view without a box\n": false,
	} {
		if got := grokIdle(screen); got != idle {
			t.Errorf("idle %v, want %v:\n%s", got, idle, screen)
		}
	}
	submitted := "     ❯ " + grokTestNotice + "   8:35 PM\n" + strings.Replace(box, "%s", "    ", 1)
	if !grokSubmitted(submitted, grokTestNotice) || grokSubmitted(strings.Replace(box, "%s", grokTestNotice, 1), grokTestNotice) {
		t.Fatal("submission check")
	}
}
