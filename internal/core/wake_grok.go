package core

import (
	"context"
	"errors"
	"os"
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

// errTmuxUnavailable is the fixed reason of a tmux that could not run: its recorded executable
// failed the check (platform.TrustedExecutable), no tmux was found, or the command did not
// start (#418). It differs from a pane that tmux ran for but did not prove.
var errTmuxUnavailable = errors.New("tmux_unavailable")

// tmuxUnavailableError is errTmuxUnavailable with its cause. Its text is only the fixed
// reason, so no path or system text reaches a recorded reason.
type tmuxUnavailableError struct{ cause error }

func (e tmuxUnavailableError) Error() string        { return errTmuxUnavailable.Error() }
func (e tmuxUnavailableError) Unwrap() error        { return e.cause }
func (e tmuxUnavailableError) Is(target error) bool { return target == errTmuxUnavailable }

// errPaneUnproven is a pane that tmux answered for but that does not hold the session's host.
var errPaneUnproven = errors.New("pane_unproven")

// runTmux runs one tmux command on the server at socket, and never reads the user's
// configuration for a new server. executable is the absolute tmux that the session's own
// terminal runs, as koinon mcp reported it; it runs only after platform.TrustedExecutable
// accepts it. Without one, tmux is looked up on the daemon's PATH (#418).
func runTmux(ctx context.Context, executable, socket string, args ...string) (string, error) {
	path := "tmux"
	if executable != "" {
		resolved, err := platform.TrustedExecutable(executable)
		if err != nil {
			return "", tmuxUnavailableError{err}
		}
		path = resolved
	}
	ctx, cancel := context.WithTimeout(ctx, grokTmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, append([]string{"-S", socket}, args...)...).Output()
	if tmuxNotStarted(err) {
		return string(out), tmuxUnavailableError{err}
	}
	return string(out), err
}

// tmuxNotStarted reports whether a tmux command failed before tmux ran: tmux was not found
// or could not be started. A tmux that ran and failed exits with an *exec.ExitError.
func tmuxNotStarted(err error) bool {
	var pathErr *os.PathError
	var execErr *exec.Error
	return errors.Is(err, exec.ErrNotFound) || errors.As(err, &pathErr) || errors.As(err, &execErr)
}

// tmuxReason is the fixed reason of a failed pane proof or tmux read: tmux_unavailable when
// tmux could not run, else other.
func tmuxReason(err error, other string) string {
	if errors.Is(err, errTmuxUnavailable) {
		return errTmuxUnavailable.Error()
	}
	return other
}

func (s *Store) wakeGrok(ctx context.Context, session Session, notice string) wakeResult {
	var pid, start int64
	var socket, pane, executable string
	if s.db.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane,tmux_path FROM sessions WHERE family=? AND id=?`,
		session.Family, session.ID).Scan(&pid, &start, &socket, &pane, &executable) != nil {
		return waiting("grok_target_unavailable")
	}
	h := s.hostTools(executable)
	if err := provePane(ctx, h, pid, start, socket, pane); err != nil {
		return waiting(tmuxReason(err, "grok_target_unavailable"))
	}
	screen, err := h.tmux(ctx, socket, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return waiting(tmuxReason(err, "grok_target_unavailable"))
	}
	if !grokIdle(screen) {
		return waiting("receiver_busy")
	}
	// The fixed notice as literal keys, then Enter: nothing else reaches the pane.
	if _, err := h.tmux(ctx, socket, "send-keys", "-t", pane, "-l", notice); err != nil {
		return uncertain("pane_submission_unconfirmed")
	}
	if _, err := h.tmux(ctx, socket, "send-keys", "-t", pane, "Enter"); err != nil {
		return uncertain("pane_submission_unconfirmed")
	}
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(100 * time.Millisecond)
		screen, err = h.tmux(ctx, socket, "capture-pane", "-p", "-J", "-t", pane)
		if err == nil && grokSubmitted(screen, notice) {
			return accepted()
		}
	}
	return uncertain("pane_submission_unconfirmed")
}

// hostTools are the store's process and tmux readers, for pane proof and cycle adapters. Its
// tmux runs executable, the session's recorded tmux, or tmux on PATH for "" (#418).
func (s *Store) hostTools(executable string) hostTools {
	tmux := s.tmux
	return hostTools{tmux: func(ctx context.Context, socket string, args ...string) (string, error) {
		return tmux(ctx, executable, socket, args...)
	}, processStart: s.processStart, processParents: s.processParents,
		processCommand: s.processCommand, processEnvironment: s.processEnvironment, processGroups: s.processGroups}
}

// hostTools reach the host process and its tmux pane; tests replace each one.
type hostTools struct {
	tmux               func(ctx context.Context, socket string, args ...string) (string, error)
	processStart       func(int) (int64, error)
	processParents     func() (map[int]int, error)
	processCommand     func(int) (string, []string, error)
	processEnvironment func(pid int, name string) (string, error)
	processGroups      func() (map[int]platform.ProcessGroup, error)
}

// provePane proves that pane still holds the session's host: the host process has its
// recorded start value, and the pane's process is the host or an ancestor of it with no
// other agent process between them, the rule of terminal naming (#156). It returns nil for a
// proven pane, an errTmuxUnavailable error when tmux could not run, else errPaneUnproven.
func provePane(ctx context.Context, h hostTools, pid, start int64, socket, pane string) error {
	_, _, err := provenPane(ctx, h, pid, start, socket, pane)
	return err
}

// provenPane is provePane that also returns the pane's process and the process table it
// proved the host's ancestry with.
func provenPane(ctx context.Context, h hostTools, pid, start int64, socket, pane string) (int, map[int]int, error) {
	if pid <= 1 || start == 0 || !(&TmuxPane{Socket: socket, Pane: pane}).valid() || socket == "" {
		return 0, nil, errPaneUnproven
	}
	if current, err := h.processStart(int(pid)); err != nil || current != start {
		return 0, nil, errPaneUnproven
	}
	// A space separates the fields: tmux before 3.4 prints a tab in a format as "_".
	out, err := h.tmux(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_pid}")
	if errors.Is(err, errTmuxUnavailable) {
		return 0, nil, err
	}
	fields := strings.Fields(out)
	if err != nil || len(fields) != 2 || fields[0] != pane {
		return 0, nil, errPaneUnproven
	}
	panePID, err := strconv.Atoi(fields[1])
	if err != nil || panePID <= 1 {
		return 0, nil, errPaneUnproven
	}
	parents, err := h.processParents()
	if err != nil {
		return 0, nil, errPaneUnproven
	}
	agent := func(pid int) bool {
		executable, args, err := h.processCommand(pid)
		return err != nil || platform.AgentCommand(executable, args)
	}
	for current, steps := int(pid), 0; current != panePID; steps++ {
		if current != int(pid) && agent(current) {
			return 0, nil, errPaneUnproven
		}
		parent, found := parents[current]
		if !found || parent <= 1 || steps >= maxGrokAncestors {
			return 0, nil, errPaneUnproven
		}
		current = parent
	}
	if panePID != int(pid) && agent(panePID) {
		return 0, nil, errPaneUnproven
	}
	return panePID, parents, nil
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
