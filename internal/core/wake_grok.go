package core

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	// A space separates the fields: tmux before 3.4 prints a tab in a format as "_".
	out, err := s.tmux(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_pid}")
	fields := strings.Fields(out)
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

// grokComposer reads Grok's input box: the last line that starts with the box border and the
// prompt mark, and every bordered continuation line below it up to the bottom border. It
// returns the box's text, the lines below the box, and whether a box was found.
func grokComposer(screen string) (text string, below []string, found bool) {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "│ ❯")
		if !ok {
			continue
		}
		parts := []string{strings.TrimSpace(strings.TrimSuffix(rest, "│"))}
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])
			continuation, ok := strings.CutPrefix(line, "│")
			if !ok {
				break
			}
			parts = append(parts, strings.TrimSpace(strings.TrimSuffix(continuation, "│")))
		}
		return strings.TrimSpace(strings.Join(parts, "\n")), lines[j:], true
	}
	return "", nil, false
}

// grokRunning reports whether a line is Grok's running-turn status: a spinner glyph (a
// Braille pattern) at its start and the [stop] control at its end. Conversation text that
// merely mentions [stop] is not the status.
func grokRunning(line string) bool {
	line = strings.TrimSpace(line)
	first, _ := utf8.DecodeRuneInString(line)
	return first >= 0x2800 && first <= 0x28ff && strings.HasSuffix(line, "[stop]")
}

// grokIdle reports whether a Grok screen shows an empty input box and no running turn
// (Grok Build 1.0.50, recorded on #254): a running turn shows its status line, a spinner
// ending in [stop], and a shortcut bar below the box with Ctrl+c:cancel, while the box
// itself stays empty. Every line of the box must be empty; a multi-line draft is not idle.
// A screen without the box, such as a sub-agent's full-screen view, is not idle.
func grokIdle(screen string) bool {
	text, below, found := grokComposer(screen)
	if !found || text != "" {
		return false
	}
	for _, line := range strings.Split(screen, "\n") {
		if grokRunning(line) {
			return false
		}
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
