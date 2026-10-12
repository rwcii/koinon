package core

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// The Grok wake (#254 chunk 03). Grok's default mode has no interface that reaches a running
// session from outside (spike fact 6), so the notice is typed into the session's own tmux
// pane: the pane that koinon mcp proved at registration to hold the session's host. Before
// each wake the pane is proved again, and input is sent only while Grok is idle.

const (
	grokTmuxTimeout = time.Second
	// maxGrokAncestors bounds the walk from the host process up to the pane's process.
	maxGrokAncestors = 256
)

// runTmux runs one tmux command on the server at socket, resolved on PATH as the agent's
// own terminal would be, and never reads the user's configuration for a new server.
func runTmux(ctx context.Context, socket string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, grokTmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).Output()
	return string(out), err
}

func (s *Store) wakeGrok(ctx context.Context, session Session, notice string) wakeResult {
	var pid, start int64
	var socket, pane string
	if s.db.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`,
		session.Family, session.ID).Scan(&pid, &start, &socket, &pane) != nil || !s.grokPane(ctx, pid, start, socket, pane) {
		return waiting("grok_target_unavailable")
	}
	screen, err := s.tmux(ctx, socket, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return waiting("grok_target_unavailable")
	}
	if !grokIdle(screen) {
		return waiting("receiver_busy")
	}
	// The fixed notice as literal keys, then Enter: nothing else reaches the pane.
	if _, err := s.tmux(ctx, socket, "send-keys", "-t", pane, "-l", notice); err != nil {
		return uncertain("pane_submission_unconfirmed")
	}
	if _, err := s.tmux(ctx, socket, "send-keys", "-t", pane, "Enter"); err != nil {
		return uncertain("pane_submission_unconfirmed")
	}
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(100 * time.Millisecond)
		screen, err = s.tmux(ctx, socket, "capture-pane", "-p", "-J", "-t", pane)
		if err == nil && grokSubmitted(screen, notice) {
			return accepted()
		}
	}
	return uncertain("pane_submission_unconfirmed")
}

// grokPane proves that pane still holds the session's host: the host process has its
// recorded start value, and the pane's process is the host or an ancestor of it with no
// other agent process between them, the rule of terminal naming (#156).
func (s *Store) grokPane(ctx context.Context, pid, start int64, socket, pane string) bool {
	if pid <= 1 || start == 0 || !(&TmuxPane{Socket: socket, Pane: pane}).valid() || socket == "" {
		return false
	}
	if current, err := s.processStart(int(pid)); err != nil || current != start {
		return false
	}
	out, err := s.tmux(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_id}\t#{pane_pid}")
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if err != nil || len(fields) != 2 || fields[0] != pane {
		return false
	}
	panePID, err := strconv.Atoi(fields[1])
	if err != nil || panePID <= 1 {
		return false
	}
	parents, err := s.processParents()
	if err != nil {
		return false
	}
	agent := func(pid int) bool {
		executable, args, err := s.processCommand(pid)
		return err != nil || platform.AgentCommand(executable, args)
	}
	for current, steps := int(pid), 0; current != panePID; steps++ {
		if current != int(pid) && agent(current) {
			return false
		}
		parent, found := parents[current]
		if !found || parent <= 1 || steps >= maxGrokAncestors {
			return false
		}
		current = parent
	}
	return panePID == int(pid) || !agent(panePID)
}

// grokComposer finds Grok's input box, the last line that starts with the box border and the
// prompt mark, and returns its text and whether one was found, with the lines below it.
func grokComposer(screen string) (text string, below []string, found bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if rest, ok := strings.CutPrefix(line, "│ ❯"); ok {
			return strings.TrimSpace(strings.TrimSuffix(rest, "│")), lines[i+1:], true
		}
	}
	return "", nil, false
}

// grokIdle reports whether a Grok screen shows an empty input box and no running turn
// (Grok Build 1.0.50, recorded on #254): a running turn shows a spinner line that ends with
// [stop] and a shortcut bar below the box with Ctrl+c:cancel; the box itself stays empty.
// A screen without the box, such as a sub-agent's full-screen view, is not idle.
func grokIdle(screen string) bool {
	text, below, found := grokComposer(screen)
	if !found || text != "" || strings.Contains(screen, "[stop]") {
		return false
	}
	for _, line := range below {
		if strings.Contains(strings.ToLower(line), "cancel") {
			return false
		}
	}
	return true
}

// grokSubmitted reports whether the notice left the input box as a submitted prompt: the
// box is empty and a transcript line above it shows the prompt mark and the notice.
func grokSubmitted(screen, notice string) bool {
	text, _, found := grokComposer(screen)
	if !found || text != "" {
		return false
	}
	for _, line := range strings.Split(screen, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "│") && strings.HasPrefix(line, "❯ "+notice) {
			return true
		}
	}
	return false
}
