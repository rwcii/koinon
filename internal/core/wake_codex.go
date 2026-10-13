package core

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The Codex pane wake (#275). After /clear, a Codex host keeps the cleared thread until the
// new thread's first turn, and `codex queue --thread` runs a hidden turn in the cleared thread.
// For a launched participant holder with a proven tmux pane, the notice is typed into the
// pane instead: the visible thread runs it, and after /clear that is the new thread's first
// turn, whose SessionStart hook moves the participant (host_cleared). The evidence is Codex
// CLI 0.162.0 on Linux (#275 T1). It runs on Linux and macOS, where platform.ProcessGroups
// reads the terminal groups; a session without a pane and a session inbox keep the queue.

// codexSubmitPause separates the typed notice from Enter, as in the T1 evidence: Codex submits
// a typed line on Enter, and the composer is read back in between.
const codexSubmitPause = 500 * time.Millisecond

// codexParticipantNotice reports whether a notice names a participant inbox. The daemon
// writes every notice (wakeNotice). A session inbox keeps the queue: after /clear the new
// thread, which a pane wake reaches, cannot read the cleared thread's own inbox.
func codexParticipantNotice(notice string) bool {
	return strings.HasPrefix(notice, `Koinon inbox "codex":"participant:`)
}

// wakeCodexPane wakes a launched Codex session through its pane. ok is false when the session
// has no recorded pane; the caller then uses the queue.
func (s *Store) wakeCodexPane(ctx context.Context, session Session, activity SessionView, notice string) (result wakeResult, ok bool) {
	var pid, start int64
	var socket, pane string
	if s.db.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family='codex' AND id=?`,
		session.ID).Scan(&pid, &start, &socket, &pane) != nil || socket == "" || pane == "" {
		return wakeResult{}, false
	}
	// Codex shows no busy marker while a reply streams (T1), so only a host observed idle is
	// woken. An unknown activity, such as a new thread's before its first tool call, waits.
	if activity.Value == nil || activity.Value.State != "idle" {
		return waiting("receiver_busy"), true
	}
	h := s.hostTools()
	if !codexForeground(ctx, h, pid, start, socket, pane) {
		return waiting("codex_target_unavailable"), true
	}
	before, err := h.tmux(ctx, socket, "capture-pane", "-p", "-e", "-J", "-t", pane)
	if err != nil {
		return waiting("codex_target_unavailable"), true
	}
	if !codexIdle(before) {
		return waiting("receiver_busy"), true
	}
	// The fixed notice as literal keys, then Enter: nothing else reaches the pane.
	if _, err := h.tmux(ctx, socket, "send-keys", "-t", pane, "-l", notice); err != nil {
		return uncertain("pane_submission_unconfirmed"), true
	}
	if !pause(ctx, codexSubmitPause) {
		return uncertain("pane_submission_unconfirmed"), true
	}
	// Enter only when the composer holds exactly the notice and the pane is still proven.
	typed, err := h.tmux(ctx, socket, "capture-pane", "-p", "-e", "-J", "-t", pane)
	if err != nil || squeeze(codexDraft(typed)) != squeeze(notice) || !codexForeground(ctx, h, pid, start, socket, pane) {
		return uncertain("pane_submission_unconfirmed"), true
	}
	if _, err := h.tmux(ctx, socket, "send-keys", "-t", pane, "Enter"); err != nil {
		return uncertain("pane_submission_unconfirmed"), true
	}
	for attempt := 0; attempt < 5; attempt++ {
		if !pause(ctx, 100*time.Millisecond) {
			break
		}
		after, err := h.tmux(ctx, socket, "capture-pane", "-p", "-e", "-J", "-t", pane)
		if err == nil && codexSubmitted(before, after, notice) {
			return accepted(), true
		}
	}
	return uncertain("pane_submission_unconfirmed"), true
}

// pause waits d, or reports false when ctx ends first.
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// codexForeground proves the pane (provePane) and that its terminal gives input to the host:
// the host shares the pane process's terminal, it is in that terminal's foreground process
// group, and every other process in that group is an ancestor of the host, such as the
// pane's shell that waits for it (T1). A child of the host in the foreground, the 7638dc8
// case, is refused.
func codexForeground(ctx context.Context, h hostTools, pid, start int64, socket, pane string) bool {
	panePID, parents, ok := provenPane(ctx, h, pid, start, socket, pane)
	if !ok || h.processGroups == nil {
		return false
	}
	groups, err := h.processGroups()
	if err != nil {
		return false
	}
	host, shell := groups[int(pid)], groups[panePID]
	if host.Terminal == 0 || host.Terminal != shell.Terminal || host.Foreground <= 0 || host.Group != host.Foreground {
		return false
	}
	ancestors := map[int]bool{}
	for current := parents[int(pid)]; current > 1 && len(ancestors) < maxGrokAncestors; current = parents[current] {
		ancestors[current] = true
	}
	for member, g := range groups {
		if g.Group == host.Foreground && member != int(pid) && !ancestors[member] {
			return false
		}
	}
	return true
}

// codexScreen is a pane capture with attributes (capture-pane -e): each line raw and without
// its escape sequences.
type codexScreen struct{ raw, plain []string }

func readCodexScreen(screen string) codexScreen {
	raw := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	plain := make([]string, len(raw))
	for i, line := range raw {
		plain[i], _ = sgrText(line)
	}
	return codexScreen{raw, plain}
}

// composer is the index of the input line: the last line that starts with the prompt mark ›.
// Submitted prompts above it start with the same mark.
func (c codexScreen) composer() int {
	for i := len(c.plain) - 1; i >= 0; i-- {
		if strings.HasPrefix(c.plain[i], "›") {
			return i
		}
	}
	return -1
}

// footer is the index of the first footer row below the composer at line i: the last run of
// non-blank rows on the screen, the status line and, while the composer is empty, the
// shortcut hint. In every T1 capture a blank row separates it from the composer. Another
// layout, such as a longer last run or none, gives -1, and the composer counts as not empty.
func (c codexScreen) footer(i int) int {
	end := len(c.plain) - 1
	for end > i && strings.TrimSpace(c.plain[end]) == "" {
		end--
	}
	start := end
	for start-1 > i && strings.TrimSpace(c.plain[start-1]) != "" {
		start--
	}
	if end <= i || start-1 <= i || end-start+1 > 2 {
		return -1
	}
	return start
}

// composerText is the composer's text: the rest of the mark's line unless it is only the dim
// placeholder ("Ask Codex to do anything"), and every non-blank row down to the footer, also
// after blank rows. ok is false when the footer is not found.
func (c codexScreen) composerText(i int) (text string, ok bool) {
	footer := c.footer(i)
	if footer < 0 {
		return "", false
	}
	var parts []string
	_, rest, _ := strings.Cut(c.raw[i], "›")
	if first, typed := sgrText(rest); typed {
		parts = append(parts, strings.TrimSpace(first))
	}
	for _, line := range c.plain[i+1 : footer] {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "\n"), true
}

// empty reports whether the composer at line i holds no text.
func (c codexScreen) empty(i int) bool {
	text, ok := c.composerText(i)
	return ok && text == ""
}

// codexIdle reports whether a Codex 0.162.0 screen shows an empty composer and no running-turn
// status: the composer holds only the placeholder, and the nearest line above it is not
// "Working (… esc to interrupt)". A reply that streams shows no status, so the caller also
// requires an idle activity.
func codexIdle(screen string) bool {
	c := readCodexScreen(screen)
	i := c.composer()
	if i < 0 || !c.empty(i) {
		return false
	}
	for j := i - 1; j >= 0; j-- {
		line := strings.TrimSpace(c.plain[j])
		if line == "" {
			continue
		}
		if strings.Contains(line, "Working (") && strings.HasSuffix(line, "esc to interrupt)") {
			return false
		}
		break
	}
	return true
}

// codexDraft is the composer's text, or "" when it shows only the placeholder or cannot be read.
func codexDraft(screen string) string {
	c := readCodexScreen(screen)
	i := c.composer()
	if i < 0 {
		return ""
	}
	text, _ := c.composerText(i)
	return text
}

// block is the text of a submitted prompt that starts at line i with the mark: the rest of
// that line and the indented lines that follow it up to a blank or unindented line.
func (c codexScreen) block(i int) string {
	parts := []string{strings.TrimSpace(strings.TrimPrefix(c.plain[i], "›"))}
	for j := i + 1; j < len(c.plain) && strings.TrimSpace(c.plain[j]) != "" && strings.HasPrefix(c.plain[j], " "); j++ {
		parts = append(parts, strings.TrimSpace(c.plain[j]))
	}
	return strings.Join(parts, "\n")
}

// lastPrompt is the text of the latest submitted prompt above the composer, if any.
func (c codexScreen) lastPrompt() (string, bool) {
	i := c.composer()
	for j := i - 1; j >= 0; j-- {
		if strings.HasPrefix(c.plain[j], "›") {
			return c.block(j), true
		}
	}
	return "", false
}

// codexSubmitted reports whether the notice left the composer as a submitted prompt: the
// composer is empty again and the latest prompt above it is the notice. When the screen
// before typing already ended with the same notice, a new prompt must have been added.
func codexSubmitted(before, after, notice string) bool {
	c := readCodexScreen(after)
	i := c.composer()
	if i < 0 || !c.empty(i) {
		return false
	}
	last, found := c.lastPrompt()
	if !found || squeeze(last) != squeeze(notice) {
		return false
	}
	previous := readCodexScreen(before)
	if earlier, found := previous.lastPrompt(); found && squeeze(earlier) == squeeze(notice) {
		return c.prompts(notice) > previous.prompts(notice)
	}
	return true
}

// prompts counts the submitted prompts above the composer that are the notice.
func (c codexScreen) prompts(notice string) int {
	count := 0
	for j := c.composer() - 1; j >= 0; j-- {
		if strings.HasPrefix(c.plain[j], "›") && squeeze(c.block(j)) == squeeze(notice) {
			count++
		}
	}
	return count
}

// squeeze removes white space, so a line that Codex wraps compares equal to the typed text.
func squeeze(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
}

// sgrText strips the escape sequences of a captured line, and reports whether any visible
// character is drawn without the dim attribute (SGR 2), the attribute of Codex's placeholder.
func sgrText(line string) (text string, undimmed bool) {
	var b strings.Builder
	dim := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			end := i + 2
			for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
				end++
			}
			if end < len(line) && line[end] == 'm' {
				for _, param := range strings.Split(line[i+2:end], ";") {
					switch param {
					case "", "0", "22":
						dim = false
					case "2":
						dim = true
					}
				}
			}
			i = end + 1
			continue
		}
		r, size := rune(line[i]), 1
		if line[i] >= 0x80 {
			r, size = utf8.DecodeRuneInString(line[i:])
		}
		b.WriteString(line[i : i+size])
		if !dim && !unicode.IsSpace(r) {
			undimmed = true
		}
		i += size
	}
	return b.String(), undimmed
}
