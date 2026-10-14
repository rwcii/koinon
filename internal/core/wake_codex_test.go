package core

import (
	"context"
	"encoding/json"
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

// The Codex pane wake (#275) on a private tmux server, with a synthetic program that draws
// Codex CLI 0.162.0's composer as the T1 captures show it, and records every line it reads.

// codexScreen draws the composer: the bold mark, the dim placeholder or a draft, a blank line,
// the status footer and, only while the composer is empty, the shortcut hint. The cursor
// returns to the composer after the mark, where the terminal echoes typed keys. After each
// line it reads, the program draws it as a submitted prompt above an empty composer.
const codexScreenProgram = `#!/bin/sh
state=$1 received=$2
composer() {
	if [ -n "$1" ]; then
		printf '\033[1m›\033[0m %s\n\n  synthetic-model · /tmp/synthetic/repo · Context 0%% used\n' "$1"
		printf '\033[3A\r\033[2C'
	else
		printf '\033[1m›\033[0m \033[2mAsk Codex to do anything\033[0m\n\n  synthetic-model · /tmp/synthetic/repo · Context 0%% used\n  \033[1m?\033[0m for shortcuts\n'
		printf '\033[4A\r\033[2C'
	fi
}
printf '\033[2J\033[H'
[ "$state" = working ] && printf '\033[2m•\033[0m \033[2mWorking\033[0m \033[2m(1s • \033[0;1mesc\033[0;2m to interrupt)\033[0m\n\n\n'
draft=
[ "$state" = draft ] && draft="synthetic draft"
composer "$draft"
# A deaf program never reads: typed keys stay in the composer.
[ "$state" = deaf ] && exec sleep 300
# Without echo the typed notice never shows in the composer.
[ "$state" = noecho ] && stty -echo
while IFS= read -r line; do
	printf '%s\n' "$line" >> "$received"
	printf '\033[2J\033[H\033[1;2m›\033[0m %s\n\n' "$line"
	composer ""
done
`

const codexTestNotice = `Koinon inbox "codex":"participant:codex-synthetic" has waiting sequence range 1-2. Read your Koinon inbox.`

type codexFixture struct {
	*grokFixture
}

func newCodexFixture(t *testing.T) *codexFixture {
	t.Helper()
	f := &codexFixture{newGrokFixture(t)}
	if err := os.WriteFile(f.program, []byte(codexScreenProgram), 0700); err != nil {
		t.Fatal(err)
	}
	// Wide windows, as in the T1 capture, so the typed notice fits on the composer line.
	f.run("set-option", "-t", "keeper", "default-size", "200x50")
	f.s.codexPane = true
	return f
}

// pane starts the synthetic program in a new window. With wrapper, the pane's process is a
// shell that runs the program as its child, as `koinon codex` runs under a pane's shell.
func (f *codexFixture) pane(state string, command string) grokPaneUnderTest {
	f.t.Helper()
	f.sessionCounter++
	received := filepath.Join(f.dir, "received-"+strconv.Itoa(f.sessionCounter))
	program := "sh " + f.program + " " + state + " " + received
	if command == "" {
		command = program
	} else {
		command = strings.ReplaceAll(command, "PROGRAM", program)
	}
	fields := strings.Fields(f.run("new-window", "-d", "-P", "-F", "#{pane_id} #{pane_pid}", command))
	shell, _ := strconv.Atoi(fields[1])
	p := grokPaneUnderTest{id: fields[0], shell: shell, host: shell, received: received}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(f.run("capture-pane", "-p", "-t", p.id), "›") {
		if time.Now().After(deadline) {
			f.t.Fatal("the synthetic Codex screen did not draw")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return p
}

// child is the only child process of pid.
func (f *codexFixture) child(pid int) int {
	f.t.Helper()
	parents, err := platform.ProcessParents()
	if err != nil {
		f.t.Fatal(err)
	}
	for child, parent := range parents {
		if parent == pid {
			return child
		}
	}
	f.t.Fatalf("no child of %d", pid)
	return 0
}

// session registers a launched Codex session with host in pane, observed with activity.
func (f *codexFixture) session(host int, pane, activity string) Session {
	f.t.Helper()
	f.sessionCounter++
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", f.sessionCounter)
	session, err := f.s.Register(context.Background(), Registration{Family: "codex", ID: id, Directory: f.dir, TTLSeconds: 60})
	if err != nil {
		f.t.Fatal(err)
	}
	start, err := platform.ProcessStart(host)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`UPDATE sessions SET host_pid=?,host_start=?,tmux_socket=?,tmux_pane=? WHERE family='codex' AND id=?`,
		host, start, f.socket, pane, id); err != nil {
		f.t.Fatal(err)
	}
	session.WakeTarget = json.RawMessage(`{"launch_id":"synthetic-launch","family":"codex","cli":"/synthetic/codex"}`)
	if activity != "" {
		at := time.Now().UnixMilli()
		if err := f.s.Observe(context.Background(), Observation{Caller: Key{"codex", session.ID},
			Activity: &ObservedValue{Source: "codex_rollout", At: at, State: activity}}); err != nil {
			f.t.Fatal(err)
		}
	}
	return session
}

func (f *codexFixture) untouched(p grokPaneUnderTest) {
	f.t.Helper()
	time.Sleep(200 * time.Millisecond)
	if got := f.received(p); got != "" {
		f.t.Fatalf("received %q", got)
	}
	if screen := f.run("capture-pane", "-p", "-t", p.id); strings.Contains(screen, "Koinon inbox") {
		f.t.Fatalf("the notice reached the pane:\n%s", screen)
	}
}

func TestCodexWakeTypesTheNoticeIntoTheProvenIdlePane(t *testing.T) {
	f := newCodexFixture(t)
	other := f.pane("idle", "")
	// The pane's shell waits for the host, which shares its foreground group (T1).
	p := f.pane("idle", "sh -c 'PROGRAM; exit'")
	host := f.child(p.shell)
	session := f.session(host, p.id, "idle")
	if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != accepted() {
		t.Fatalf("wake: %v\n%s", got, f.run("capture-pane", "-p", "-t", p.id))
	}
	if got := f.received(p); got != codexTestNotice+"\n" {
		t.Fatalf("received %q", got)
	}
	if got := f.received(other); got != "" {
		t.Fatalf("another pane received %q", got)
	}
}

func TestCodexWakeSendsNothingWithoutProofOrIdle(t *testing.T) {
	f := newCodexFixture(t)
	for name, c := range map[string]func() (Session, grokPaneUnderTest, wakeResult){
		"activity unknown": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("idle", "")
			return f.session(p.host, p.id, ""), p, waiting("receiver_busy")
		},
		"activity busy": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("idle", "")
			return f.session(p.host, p.id, "busy"), p, waiting("receiver_busy")
		},
		"working status": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("working", "")
			return f.session(p.host, p.id, "idle"), p, waiting("receiver_busy")
		},
		"draft in the composer": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("draft", "")
			return f.session(p.host, p.id, "idle"), p, waiting("receiver_busy")
		},
		"foreign pane": func() (Session, grokPaneUnderTest, wakeResult) {
			p, foreign := f.pane("idle", ""), f.pane("idle", "")
			return f.session(p.host, foreign.id, "idle"), foreign, waiting("codex_target_unavailable")
		},
		"reused host": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("idle", "")
			session := f.session(p.host, p.id, "idle")
			f.s.db.Exec(`UPDATE sessions SET host_start=host_start+1 WHERE family='codex' AND id=?`, session.ID)
			return session, p, waiting("codex_target_unavailable")
		},
		// The 7638dc8 case: the recorded host is proven in the pane, but its child has its
		// own process group and holds the terminal's foreground, so it would read the keys.
		"child of the host in the foreground": func() (Session, grokPaneUnderTest, wakeResult) {
			p := f.pane("idle", "sh -c 'set -m; PROGRAM; exit'")
			return f.session(p.shell, p.id, "idle"), p, waiting("codex_target_unavailable")
		},
	} {
		t.Run(name, func(t *testing.T) {
			session, p, want := c()
			if got := f.s.providerWake(context.Background(), session, codexTestNotice); got != want {
				t.Fatalf("wake: %v, want %v", got, want)
			}
			f.untouched(p)
		})
	}
}

// A multi-line draft whose first rows are blank still holds text: nothing is typed.
func TestCodexWakeLeavesADraftBelowBlankRows(t *testing.T) {
	f := newCodexFixture(t)
	program := strings.Replace(codexScreenProgram, `composer "$draft"`, `if [ "$state" = blankdraft ]; then
	printf '\033[1m›\033[0m \n\n  synthetic draft\n\n  synthetic-model · /tmp/synthetic/repo · Context 0%% used\n'
	printf '\033[5A\r\033[2C'
else
	composer "$draft"
fi`, 1)
	if err := os.WriteFile(f.program, []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	p := f.pane("blankdraft", "")
	if !strings.Contains(f.run("capture-pane", "-p", "-t", p.id), "synthetic draft") {
		t.Fatal("the synthetic draft did not draw")
	}
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id, "idle"), codexTestNotice); got != waiting("receiver_busy") {
		t.Fatalf("wake: %v", got)
	}
	f.untouched(p)
}

// Through the scheduler, each attempt types the notice once and presses Enter once. An
// unconfirmed submission is uncertain, and the existing read wait (#250) offers the unread
// message again after it, as for every provider.
func TestCodexWakeAttemptsThroughTheScheduler(t *testing.T) {
	f := newCodexFixture(t)
	ctx := context.Background()
	p := f.pane("idle", "")
	if out, err := exec.Command("git", "init", "-q", f.dir).CombinedOutput(); err != nil {
		t.Fatalf("repository: %v %s", err, out)
	}
	clock := time.Now()
	f.s.now = func() time.Time { return clock }
	launch, err := f.s.CreateLaunch(ctx, LaunchTarget{Family: "codex", Directory: f.dir, CLI: "/synthetic/codex", HostPID: p.host})
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := f.s.Register(ctx, Registration{Family: "codex", ID: "00000000-0000-4000-8000-000000000001",
		Repository: f.dir, Directory: f.dir, TTLSeconds: 3600, LaunchID: launch, Ancestors: []int{p.host},
		Tmux: &TmuxPane{Socket: f.socket, Pane: p.id}})
	if err != nil || !receiver.HoldsAddress {
		t.Fatalf("launched receiver: %v %+v", err, receiver)
	}
	sender, err := f.s.Register(ctx, Registration{Family: "deepseek", ID: "synthetic-sender", Directory: f.dir, TTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	idle := func() {
		t.Helper()
		if err := f.s.Observe(ctx, Observation{Caller: Key{"codex", receiver.ID},
			Activity: &ObservedValue{Source: "codex_rollout", At: clock.UnixMilli(), State: "idle"}}); err != nil {
			t.Fatal(err)
		}
	}
	idle()
	message, err := f.s.Send(ctx, Key{sender.Family, sender.ID}, receiver.Address, "synthetic payload")
	if err != nil {
		t.Fatal(err)
	}
	real := f.s.tmux
	captures, typed, enters := 0, 0, 0
	f.s.tmux = func(ctx context.Context, socket string, args ...string) (string, error) {
		switch {
		case args[0] == "capture-pane":
			captures++
			// The reads after Enter fail: the submission stays unconfirmed.
			if captures >= 3 && captures <= 7 {
				return "", errors.New("synthetic capture failure")
			}
		case args[0] == "send-keys" && len(args) > 3 && args[3] == "-l":
			typed++
		case args[0] == "send-keys" && args[len(args)-1] == "Enter":
			enters++
		}
		return real(ctx, socket, args...)
	}
	f.s.wake.send = f.s.providerWake
	step(t, f.s)
	if row := messageRow(t, f.s, message.ID); typed != 1 || enters != 1 || row.state != "uncertain" || row.wake != "pane_submission_unconfirmed" {
		t.Fatalf("first attempt: typed %d, enters %d, %+v", typed, enters, row)
	}
	step(t, f.s)
	if typed != 1 {
		t.Fatalf("typed again within the read wait: %d", typed)
	}
	clock = clock.Add(wakeReadWait + time.Millisecond)
	idle()
	step(t, f.s)
	if typed != 2 || enters != 2 {
		t.Fatalf("after the read wait: typed %d, enters %d", typed, enters)
	}
}

// Enter is sent only after the composer shows exactly the notice.
func TestCodexWakeReadsTheComposerBackBeforeEnter(t *testing.T) {
	f := newCodexFixture(t)
	p := f.pane("noecho", "")
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id, "idle"), codexTestNotice); got != uncertain("pane_submission_unconfirmed") {
		t.Fatalf("wake: %v", got)
	}
	time.Sleep(200 * time.Millisecond)
	if got := f.received(p); got != "" {
		t.Fatalf("Enter was sent without a read-back: received %q", got)
	}
}

// A notice that does not leave the composer is not recorded as delivered.
func TestCodexWakeUnconfirmedSubmission(t *testing.T) {
	f := newCodexFixture(t)
	p := f.pane("deaf", "")
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id, "idle"), codexTestNotice); got != uncertain("pane_submission_unconfirmed") {
		t.Fatalf("wake: %v", got)
	}
}

// The queue stays for a session inbox, a session without a pane, and a platform without the
// pane evidence. The synthetic launch has no CLI, so the queue path waits without input.
func TestCodexWakeKeepsTheQueue(t *testing.T) {
	f := newCodexFixture(t)
	sessionNotice := `Koinon inbox "codex":"00000000-0000-4000-8000-000000000001" has waiting sequence range 1-1. Read your Koinon inbox.`
	for name, c := range map[string]func() (Session, grokPaneUnderTest, string){
		"session inbox": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", "")
			return f.session(p.host, p.id, "idle"), p, sessionNotice
		},
		"no pane": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", "")
			session := f.session(p.host, p.id, "idle")
			f.s.db.Exec(`UPDATE sessions SET tmux_socket='',tmux_pane='' WHERE family='codex' AND id=?`, session.ID)
			return session, p, codexTestNotice
		},
		"direct start": func() (Session, grokPaneUnderTest, string) {
			p := f.pane("idle", "")
			session := f.session(p.host, p.id, "idle")
			session.WakeTarget = json.RawMessage(`{"family":"codex","cli":"/synthetic/codex"}`)
			return session, p, codexTestNotice
		},
	} {
		t.Run(name, func(t *testing.T) {
			session, p, notice := c()
			if got := f.s.providerWake(context.Background(), session, notice); got != waiting("launch_unavailable") && got != waiting("cli_unavailable") {
				t.Fatalf("wake: %v, want the queue path", got)
			}
			f.untouched(p)
		})
	}
	f.s.codexPane = false
	p := f.pane("idle", "")
	if got := f.s.providerWake(context.Background(), f.session(p.host, p.id, "idle"), codexTestNotice); got != waiting("launch_unavailable") {
		t.Fatalf("wake without the pane evidence: %v", got)
	}
	f.untouched(p)
}

func readCodexFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex-screens", name+".ansi"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The Codex CLI 0.162.0 captures from the T1 evidence (capture-pane -e).
func TestCodexIdleScreens(t *testing.T) {
	for name, idle := range map[string]bool{
		"idle":               true,
		"draft":              false,
		"multiline-draft":    false,
		"escape-enter-draft": false,
		"working":            false,
		"slash-popup":        false,
		"notice-typed":       false,
		"notice-submitted":   false,
		"notice-turn-done":   true,
		// A streaming reply shows no busy marker: the screen looks idle, and only the
		// observed activity tells it apart.
		"streaming": true,
	} {
		if got := codexIdle(readCodexFixture(t, name)); got != idle {
			t.Errorf("%s: idle %v, want %v", name, got, idle)
		}
	}
	notice := `Koinon inbox "codex":"participant:codex-repo" has waiting sequence range 1-1. Read your Koinon inbox.`
	if got := codexDraft(readCodexFixture(t, "notice-typed")); got != notice {
		t.Errorf("typed draft %q", got)
	}
	if got := codexDraft(readCodexFixture(t, "idle")); got != "" {
		t.Errorf("placeholder read as draft %q", got)
	}
	if got := codexDraft(readCodexFixture(t, "multiline-draft")); squeeze(got) != "lineonelinetwo" {
		t.Errorf("multi-line draft %q", got)
	}
	idle, typed := readCodexFixture(t, "idle"), readCodexFixture(t, "notice-typed")
	for _, after := range []string{"notice-submitted", "notice-turn-done"} {
		if !codexSubmitted(idle, readCodexFixture(t, after), notice) {
			t.Errorf("%s: submission not read back", after)
		}
	}
	if codexSubmitted(idle, typed, notice) || codexSubmitted(idle, idle, notice) {
		t.Error("an unsubmitted notice read back as submitted")
	}
	// The same notice already on the screen needs a new prompt.
	done := readCodexFixture(t, "notice-turn-done")
	if codexSubmitted(done, done, notice) {
		t.Error("an earlier identical notice read back as a new submission")
	}
	empty := "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m\n\n  footer\n"
	earlier := "\x1b[1;2m›\x1b[0m " + notice + "\n\n\x1b[2m•\x1b[0m ok\n\n"
	again := earlier + "\x1b[1;2m›\x1b[0m " + notice + "\n\n"
	if !codexSubmitted(earlier+empty, again+empty, notice) {
		t.Error("a repeated notice that was submitted again not read back")
	}
}
