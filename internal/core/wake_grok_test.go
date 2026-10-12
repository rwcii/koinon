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

func (f *grokFixture) pane(state string, deep bool) grokPaneUnderTest {
	f.t.Helper()
	f.sessionCounter++
	received := filepath.Join(f.dir, "received-"+strconv.Itoa(f.sessionCounter))
	command := "sh " + f.program + " " + state + " " + received
	if deep {
		// A foreground child keeps the terminal; the trailing command stops the shell from
		// replacing itself with the program.
		command = "sh -c '" + command + "; exit'"
	}
	fields := strings.Split(f.run("new-window", "-d", "-P", "-F", "#{pane_id}\t#{pane_pid}", command), "\t")
	shell, _ := strconv.Atoi(fields[1])
	p := grokPaneUnderTest{id: fields[0], shell: shell, host: shell, received: received}
	if deep {
		deadline := time.Now().Add(5 * time.Second)
		for p.host == shell && time.Now().Before(deadline) {
			parents, err := platform.ProcessParents()
			if err != nil {
				f.t.Fatal(err)
			}
			for pid, parent := range parents {
				if parent == shell {
					p.host, p.middle = pid, shell
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if p.host == shell {
			f.t.Fatal("the deep pane's program did not start")
		}
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
	p := f.pane("idle", false)
	other := f.pane("idle", false)
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
			p := f.pane("busy", false)
			return f.session(p.host, p.id), p, "receiver_busy"
		},
		"draft in the box": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("draft", false)
			return f.session(p.host, p.id), p, "receiver_busy"
		},
		"foreign pane": func() (Session, grokPaneUnderTest, string) {
			p, foreign := f.pane("idle", false), f.pane("idle", false)
			// The recorded pane does not hold the host.
			return f.session(p.host, foreign.id), foreign, "grok_target_unavailable"
		},
		"reused host": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", false)
			session := f.session(p.host, p.id)
			f.s.db.Exec(`UPDATE sessions SET host_start=host_start+1 WHERE family='grok' AND id=?`, session.ID)
			return session, p, "grok_target_unavailable"
		},
		"intervening agent": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", true)
			f.agents[p.middle] = true
			return f.session(p.host, p.id), p, "grok_target_unavailable"
		},
		"no host record": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", false)
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
	// An intervening process that is not an agent is allowed.
	p := f.pane("idle", true)
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id), grokTestNotice); got != accepted() {
		t.Fatalf("wake through a plain shell: %v", got)
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
