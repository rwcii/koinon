package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

// The Codex cycle adapter (#196). It acts only through the session's own tmux pane, after
// proving that the pane's terminal gives input to the pinned host (codexForeground), and reads
// the threads' own rollouts for their version and activity. Codex writes no file for a new
// thread before its first turn, so the clear step also enters /status and the new thread ID is
// read from the pane. The facts it relies on are recorded in the sprint's native evidence
// (Codex CLI 0.162.0 on Linux) and in chunks/04-codex-cycle.md.

// codexCycleVersion is the adapter's version, stored with each cycle.
const codexCycleVersion = "codex-tmux-1"

// codexEvidencedPlatforms are the operating systems with native evidence for this adapter.
var codexEvidencedPlatforms = map[string]bool{"linux": true, "darwin": true}

// codexKeyWait bounds the read-back after each key.
var codexKeyWait = 3 * time.Second

// codexEscapeSettle separates Escape from the next key, as for Claude Code.
var codexEscapeSettle = 400 * time.Millisecond

// codexRolloutTail bounds the end of a rollout that the activity check reads; a rollout record
// is at most codexRolloutLine bytes for the first-record read.
const (
	codexRolloutTail = 256 << 10
	codexRolloutLine = 1 << 20
)

// Fixed reasons of a Codex input step that did not complete; the cycle records them.
var (
	errCodexTyped     = errors.New("typed_not_read_back")
	errCodexEscape    = errors.New("escape_changed_input")
	errCodexSubmit    = errors.New("submit_not_read_back")
	errCodexErase     = errors.New("erase_not_read_back")
	errCodexUnproven  = errors.New("pane_unproven")
	errCodexMenuEntry = errors.New("pickup_entry_unverified")
	errCodexNotReady  = errors.New("pane_not_ready")
)

type codexCycle struct{}

func (codexCycle) Version() string { return codexCycleVersion }

// codexHome is the host's Codex home: its CODEX_HOME, else ~/.codex in its HOME, with that HOME.
func codexHome(t CycleTarget) (codex, home string, err error) {
	home, err = t.tools.processEnvironment(int(t.HostPID), "HOME")
	if err != nil || home == "" {
		return "", "", errors.New("host home unknown")
	}
	codex, err = t.tools.processEnvironment(int(t.HostPID), "CODEX_HOME")
	if err != nil {
		return "", "", err
	}
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return codex, home, nil
}

// codexRolloutPath finds the one rollout of a thread under the Codex home's sessions tree
// (sessions/YYYY/MM/DD/rollout-…-THREAD.jsonl). It reports "" when none exists yet.
func codexRolloutPath(codex, thread string) (string, error) {
	if !nativeEpoch.MatchString(thread) || strings.ContainsAny(thread, "*?[\\") {
		return "", errors.New("thread ID invalid")
	}
	matches, err := filepath.Glob(filepath.Join(codex, "sessions", "*", "*", "*", "rollout-*-"+thread+".jsonl"))
	if err != nil || len(matches) > 1 {
		return "", errors.New("rollout ambiguous")
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

// codexMeta is the part of a rollout's first record (session_meta) that the adapter reads.
type codexMeta struct {
	ID         string `json:"id"`
	Originator string `json:"originator"`
	CLIVersion string `json:"cli_version"`
}

// readCodexMeta reads the first record of a thread's rollout; it must be the session_meta of
// that thread, written by the interactive CLI.
func readCodexMeta(path, thread string) (codexMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return codexMeta{}, err
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(f, codexRolloutLine), codexRolloutLine).ReadBytes('\n')
	if err != nil {
		return codexMeta{}, errors.New("rollout has no complete first record")
	}
	var record struct {
		Type    string    `json:"type"`
		Payload codexMeta `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "session_meta" || record.Payload.ID != thread || record.Payload.Originator != "codex-tui" {
		return codexMeta{}, errors.New("rollout of another thread")
	}
	return record.Payload, nil
}

// codexTurnEnded reads the end of a rollout directly and reports whether its last turn ended:
// the last task_started has a later task_complete or turn_aborted of the same turn, or the tail
// holds only an ending. A turn that starts later, or a tail with no turn event, is not ended.
func codexTurnEnded(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	offset := max(0, info.Size()-codexRolloutTail)
	data := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(data, offset); err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	lines := bytes.Split(data, []byte("\n"))
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // a partial first record
	}
	state, turn := "", ""
	for _, line := range lines {
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type   string `json:"type"`
				TurnID string `json:"turn_id"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.TurnID == "" {
			continue
		}
		switch record.Payload.Type {
		case "task_started":
			state, turn = "busy", record.Payload.TurnID
		case "task_complete", "turn_aborted":
			if state == "" || record.Payload.TurnID == turn {
				state = "ended"
			}
		}
	}
	return state == "ended"
}

// Probe decides whether the session's cycle can run. The pickup entry is $koinon-pickup: the
// koinon-pickup skill in the host's ~/.agents/skills must be the binary's skill byte for byte,
// and no configuration entry may turn it off (codexPickupEntry). Other skills, a project's own
// pickup included, are not read (#406).
func (codexCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !codexEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	codex, _, err := codexHome(t)
	if err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	// A Codex session's ID is its thread ID; a session without a cycle has no epoch yet.
	path, err := codexRolloutPath(codex, t.SessionID)
	if err != nil || path == "" {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	// The thread must have its own rollout record; its CLI version is not checked (PDR 006, Decision 5).
	if _, err := readCodexMeta(path, t.SessionID); err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	// The thread after /clear is a new session, which reports the pickup only as the holder
	// of this session's participant address (successorOf): without it, the clear would leave
	// a cycle that cannot complete.
	if !t.Holds {
		return CycleProbe{Reason: "participant_unheld"}
	}
	if probe := codexPickupEntry(t); probe.Reason != "" {
		return probe
	}
	return CycleProbe{Entry: PickupSkillName}
}

// codexCompactCommand is Codex's own compaction command (#509).
const codexCompactCommand = "/compact"

// CompactProbe decides whether the session's compaction can run: an evidenced platform, the
// thread's own rollout and the pane proof of Probe. A compaction keeps the thread, so it needs
// neither the pickup skill nor the participant address.
func (codexCycle) CompactProbe(ctx context.Context, t CycleTarget) CycleProbe {
	if !codexEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	codex, _, err := codexHome(t)
	if err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	path, err := codexRolloutPath(codex, t.SessionID)
	if err != nil || path == "" {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if _, err := readCodexMeta(path, t.SessionID); err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	return CycleProbe{Entry: codexCompactCommand}
}

// Compact types /compact while a fresh read shows the thread idle, and submits it with one
// Enter while the composer holds exactly it; the command popup runs its first entry, the typed
// command. The receipt is the empty composer after the Enter. Codex 0.164.0 then runs the
// compaction as a turn: it shows "Compacting context", and its rollout holds a compacted
// record and a token_count of 0 input tokens.
func (a codexCycle) Compact(ctx context.Context, t CycleTarget, fence CycleFence) (submitted, received bool, err error) {
	const text = codexCompactCommand
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { return codexReady(t, s) }, errCodexNotReady, "-l", text); err != nil {
		return false, false, err
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool { return codexText(s) == text }) {
		a.erase(ctx, t, fence, text)
		return false, false, errCodexTyped
	}
	// Each check before the Enter key leaves the command unsubmitted; only the key itself may
	// have been sent.
	unsent := func(err error) (bool, bool, error) {
		a.erase(ctx, t, fence, text)
		return false, false, err
	}
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return unsent(proofError(err, errCodexUnproven))
	}
	if err := fence(ctx); err != nil {
		return unsent(err)
	}
	if screen, err := readCodex(ctx, t); err != nil {
		return unsent(err)
	} else if codexText(screen) != text {
		return unsent(errCodexTyped)
	}
	if _, err := t.tools.tmux(ctx, t.Socket, "send-keys", "-t", t.Pane, "Enter"); err != nil {
		return true, false, err
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool { i := s.composer(); return i >= 0 && s.empty(i) }) {
		return true, false, errCodexSubmit
	}
	return true, true, nil
}

// codexPickupCommand is the mention that runs the koinon-pickup skill.
const codexPickupCommand = "$" + PickupSkillName

// codexPickupEntry checks the koinon-pickup skill in the host's $HOME/.agents/skills, a root
// that Codex 0.162.0 lists for every session (P7): it must be byte-equal to the binary's skill
// (pickup_entry_missing, pickup_entry_unreadable, pickup_entry_differs, each with the install
// step), and no configuration entry may turn it off (codexActivation). An unknown home is
// pickup_entry_unverified.
func codexPickupEntry(t CycleTarget) CycleProbe {
	codex, home, err := codexHome(t)
	if err != nil {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	path := CodexPickupSkillPath(home)
	if reason := pickupSkillReason(path, t.Pickup.Content); reason != "" {
		return CycleProbe{Reason: reason, Step: pickupSkillStep(path)}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return CycleProbe{Reason: "pickup_entry_unreadable", Step: pickupSkillStep(path)}
	}
	if err := codexActivation(t, codex, []string{real}); err != nil {
		return CycleProbe{Reason: err.Error()}
	}
	return CycleProbe{}
}

// codexOption is a numbered dialog option, the selected one marked with the prompt mark.
var codexOption = regexp.MustCompile(`^\s*(?:›\s*)?([1-9])\.\s+\S`)

// codexDialogFooters end the two dialogs of the evidence (P5): an MCP tool approval and a
// command approval.
var codexDialogFooters = []string{"enter to submit | esc to cancel", "Press enter to confirm or esc to cancel"}

// codexDialogLines bounds the dialog text read above its first option.
const codexDialogLines = 8

// codexDialog is the dialog a screen shows: its last non-blank line is a dialog footer and
// numbered options stand above it, past blank rows. Without one, it is the zero dialog.
func codexDialog(c codexScreen) CycleDialog {
	last := len(c.plain) - 1
	for last >= 0 && strings.TrimSpace(c.plain[last]) == "" {
		last--
	}
	if last < 0 || !slices.Contains(codexDialogFooters, strings.TrimSpace(c.plain[last])) {
		return CycleDialog{}
	}
	first, i := -1, last-1
	for i >= 0 && strings.TrimSpace(c.plain[i]) == "" {
		i-- // the command dialog has a blank row above its footer
	}
	var options []string
	for ; i >= 0; i-- {
		m := codexOption.FindStringSubmatch(c.plain[i])
		if m == nil {
			break
		}
		first = i
		options = append([]string{m[1]}, options...)
	}
	if first < 0 {
		return CycleDialog{}
	}
	var lines []string
	for _, line := range c.plain[max(0, first-codexDialogLines) : last+1] {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return CycleDialog{Lines: lines, Options: options, Digest: hex.EncodeToString(sum[:])}
}

// codexMenuHint ends the skill menu that a typed $ mention opens.
const codexMenuHint = "enter insert · esc close"

// codexMenu returns the rows of the open skill menu: the run of non-blank lines above its hint,
// past the blank row between them. open is false without the hint; complete is false when a
// scroll marker shows.
func codexMenu(c codexScreen) (rows []string, open, complete bool) {
	hint := -1
	for i := len(c.plain) - 1; i >= 0; i-- {
		if strings.TrimSpace(c.plain[i]) == codexMenuHint {
			hint = i
			break
		}
	}
	if hint < 0 {
		return nil, false, false
	}
	complete = true
	i := hint - 1
	for i >= 0 && strings.TrimSpace(c.plain[i]) == "" {
		i--
	}
	for ; i >= 0 && strings.TrimSpace(c.plain[i]) != ""; i-- {
		row := strings.TrimSpace(c.plain[i])
		if row == "↑" || row == "↓" {
			complete = false
			continue
		}
		rows = append([]string{row}, rows...)
	}
	return rows, true, complete
}

// skillRows counts the menu rows of skills named exactly name.
func skillRows(rows []string, name string) int {
	count := 0
	for _, row := range rows {
		fields := strings.Fields(strings.TrimPrefix(row, "›"))
		if len(fields) >= 2 && fields[0] == name && fields[1] == "[Skill]" {
			count++
		}
	}
	return count
}

// codexBanner reports whether line i starts Codex's session banner: the version line, followed
// by the directory line. The /status card has the same version line, followed by a blank one.
func codexBanner(c codexScreen, i int) bool {
	return strings.HasPrefix(strings.TrimSpace(c.plain[i]), ">_ OpenAI Codex (v") && i+1 < len(c.plain) &&
		strings.TrimSpace(c.plain[i+1]) != "" && strings.HasPrefix(c.plain[i+1], "     ")
}

// codexTranscript reports whether a line is part of a turn's transcript: a submitted prompt or a
// line that Codex marks as agent output, a tool call, an approval or an interruption.
func codexTranscript(line string) bool {
	line = strings.TrimSpace(line)
	for _, mark := range []string{"›", "•", "◦", "└", "✔", "✗", "■", "⚠"} {
		if strings.HasPrefix(line, mark) {
			return true
		}
	}
	return false
}

// codexSession matches the thread ID line of the /status card.
var codexSession = regexp.MustCompile(`^\s+Session:\s+(\S+)\s*$`)

// codexStatusThread reads the thread that a cleared screen's /status card names: below the
// latest banner, nothing but the /status echo and its card come before the card's Session
// line, so the card is the first output of the thread that the clear started. Later prompts of
// that thread may follow. untouched reports that nothing of a turn follows the Session line.
func codexStatusThread(c codexScreen) (thread string, untouched bool) {
	composer := c.composer()
	if composer < 0 {
		return "", false
	}
	banner := -1
	for i := composer - 1; i >= 0; i-- {
		if codexBanner(c, i) {
			banner = i
			break
		}
	}
	if banner < 0 {
		return "", false
	}
	echo, session := -1, -1
	for i := banner + 2; i < composer; i++ {
		switch {
		case echo < 0 && c.plain[i] == "/status":
			echo = i
		case echo < 0 && codexTranscript(c.plain[i]):
			return "", false
		case echo >= 0 && session < 0 && codexTranscript(c.plain[i]):
			return "", false
		case echo >= 0 && session < 0:
			if m := codexSession.FindStringSubmatch(c.plain[i]); m != nil {
				session, thread = i, m[1]
			}
		}
	}
	if session < 0 {
		return "", false
	}
	untouched = true
	for _, line := range c.plain[session+1 : composer] {
		if codexTranscript(line) {
			untouched = false
		}
	}
	return thread, untouched
}

// codexCleared reports a screen right after /clear: an empty composer below the banner, with
// nothing of a turn between them.
func codexCleared(c codexScreen) bool {
	composer := c.composer()
	if composer < 0 || !c.empty(composer) {
		return false
	}
	for i := composer - 1; i >= 0; i-- {
		if codexBanner(c, i) {
			return true
		}
		if codexTranscript(c.plain[i]) {
			return false
		}
	}
	return false
}

// readCodex captures the pane with attributes and its history, so a card that scrolled out of
// a small pane is still read.
func readCodex(ctx context.Context, t CycleTarget) (codexScreen, error) {
	out, err := t.tools.tmux(ctx, t.Socket, "capture-pane", "-p", "-e", "-J", "-S", "-", "-t", t.Pane)
	if err != nil {
		return codexScreen{}, err
	}
	return readCodexScreen(out), nil
}

// codexText is the composer's text, or "" when the screen shows no composer.
func codexText(c codexScreen) string {
	i := c.composer()
	if i < 0 {
		return ""
	}
	text, _ := c.composerText(i)
	return text
}

// Inspect reports whether the pane is ready for input. Before the clear the requester's thread
// must have ended its last turn (its rollout, read now); after it the successor thread must be
// the one that the pane shows, with no turn yet or its last turn ended. The screen must show an
// empty composer and no running turn in both cases.
func (codexCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return PaneState{}, proofError(err, errCodexUnproven)
	}
	if mode, err := t.tools.tmux(ctx, t.Socket, "display-message", "-p", "-t", t.Pane, "#{pane_in_mode}"); err != nil {
		return PaneState{}, err
	} else if strings.TrimSpace(mode) == "1" {
		if err := fence(ctx); err != nil {
			return PaneState{}, err
		}
		if _, err := t.tools.tmux(ctx, t.Socket, "send-keys", "-t", t.Pane, "-X", "cancel"); err != nil {
			return PaneState{}, err
		}
	}
	screen, err := readCodex(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	if codexDialog(screen).Digest != "" {
		return PaneState{Prompt: "native_dialog"}, nil
	}
	if seen := codexNotReady(t, screen); seen != "" {
		return PaneState{Seen: seen}, nil
	}
	return PaneState{Idle: true}, nil
}

// codexReady reports whether a screen is ready for the cycle's next input in t's phase: no
// dialog, an empty composer with no running turn, and the current thread's direct proof.
// Before the clear the requester's rollout, read now, shows its last turn ended. After it the
// /status card names the cycle's successor, which has no rollout and nothing of a turn after
// the card, or whose own last turn ended; the old thread's idle never admits input then.
func codexReady(t CycleTarget, screen codexScreen) bool { return codexNotReady(t, screen) == "" }

// codexNotReady is the fixed code of what keeps a screen from codexReady, or "" (#417).
func codexNotReady(t CycleTarget, screen codexScreen) string {
	switch {
	case codexDialog(screen).Digest != "":
		return "native_dialog"
	case screen.composer() < 0:
		return "input_line_not_found"
	case !screen.empty(screen.composer()):
		return "input_not_empty"
	case !codexIdle(strings.Join(screen.raw, "\n")):
		return "turn_running"
	}
	codex, _, err := codexHome(t)
	if err != nil {
		return "home_unknown"
	}
	if t.SuccessorEpoch == "" {
		path, err := codexRolloutPath(codex, t.SessionID)
		if err != nil || path == "" {
			return "rollout_unreadable"
		}
		if !codexTurnEnded(path) {
			return "turn_not_ended"
		}
		return ""
	}
	thread, untouched := codexStatusThread(screen)
	if thread != t.SuccessorEpoch {
		return "status_thread_differs"
	}
	path, err := codexRolloutPath(codex, t.SuccessorEpoch)
	if err != nil {
		return "rollout_unreadable"
	}
	if path == "" && untouched || path != "" && codexTurnEnded(path) {
		return ""
	}
	return "turn_not_ended"
}

// codexClearedReady reports a cleared screen that is ready for /status: no dialog, and the
// cleared screen with an empty composer and nothing of the new thread's turn.
func codexClearedReady(screen codexScreen) bool {
	return codexDialog(screen).Digest == "" && codexCleared(screen) && codexIdle(strings.Join(screen.raw, "\n"))
}

// key proves the pane, calls the fence and sends one key or literal text.
func (codexCycle) key(ctx context.Context, t CycleTarget, fence CycleFence, args ...string) error {
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errCodexUnproven)
	}
	if err := fence(ctx); err != nil {
		return err
	}
	_, err := t.tools.tmux(ctx, t.Socket, append([]string{"send-keys", "-t", t.Pane}, args...)...)
	return err
}

// keyIf proves the pane, calls the fence, reads the pane again and sends the keys only when
// check holds on that read.
func (codexCycle) keyIf(ctx context.Context, t CycleTarget, fence CycleFence, check func(codexScreen) bool, refused error, args ...string) error {
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errCodexUnproven)
	}
	if err := fence(ctx); err != nil {
		return err
	}
	screen, err := readCodex(ctx, t)
	if err != nil {
		return err
	}
	if !check(screen) {
		return refused
	}
	_, err = t.tools.tmux(ctx, t.Socket, append([]string{"send-keys", "-t", t.Pane}, args...)...)
	return err
}

// awaitCodex reads the pane until ok holds or the key wait ends.
func awaitCodex(ctx context.Context, t CycleTarget, ok func(codexScreen) bool) bool {
	deadline := time.Now().Add(codexKeyWait)
	for {
		if screen, err := readCodex(ctx, t); err == nil && ok(screen) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// erase deletes text that the adapter typed into an empty composer: one Backspace per rune and
// two more, only while the composer still holds exactly that text, then reads back an empty
// composer.
func (a codexCycle) erase(ctx context.Context, t CycleTarget, fence CycleFence, text string) error {
	keys := []string{}
	for range len([]rune(text)) + 2 {
		keys = append(keys, "BSpace")
	}
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { return codexText(s) == text }, errCodexErase, keys...); err != nil {
		return err
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool { i := s.composer(); return i >= 0 && s.empty(i) }) {
		return errCodexErase
	}
	return nil
}

// command types a slash command, only while a fresh read shows the pane ready for it, and
// submits it with one Enter while the composer holds exactly it; the command popup runs its
// first entry, the typed command. done reads back its effect. Typed text that is not
// submitted is erased again; a draft that someone started is left as it is.
func (a codexCycle) command(ctx context.Context, t CycleTarget, fence CycleFence, text string, ready, done func(codexScreen) bool) (bool, error) {
	if err := a.keyIf(ctx, t, fence, ready, errCodexNotReady, "-l", text); err != nil {
		return false, err
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool { return codexText(s) == text }) {
		a.erase(ctx, t, fence, text)
		return false, errCodexTyped
	}
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { return codexText(s) == text }, errCodexTyped, "Enter"); err != nil {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	if !awaitCodex(ctx, t, done) {
		return false, errCodexSubmit
	}
	return true, nil
}

// Clear enters /clear and reads back the cleared screen; that is its receipt. In the same step
// it enters /status, whose card names the new thread for NewEpoch; a failure there leaves the
// clear received and the reset unproven.
func (a codexCycle) Clear(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	ready := func(s codexScreen) bool { return codexReady(t, s) }
	if ok, err := a.command(ctx, t, fence, "/clear", ready, codexCleared); !ok {
		return false, err
	}
	a.command(ctx, t, fence, "/status", codexClearedReady, func(s codexScreen) bool {
		thread, _ := codexStatusThread(s)
		return thread != ""
	})
	return true, nil
}

// NewEpoch reads, without input, the thread that the cleared pane's /status card names.
func (codexCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	screen, err := readCodex(ctx, t)
	if err != nil {
		return "", err
	}
	thread, _ := codexStatusThread(screen)
	if thread == t.Epoch || !nativeEpoch.MatchString(thread) {
		return "", nil
	}
	return thread, nil
}

// Pickup types $koinon-pickup and submits it only when the installed skill is still verified
// (codexPickupEntry) and the open menu lists exactly one koinon-pickup row, so the row that
// Codex runs is that skill. Then Escape closes the menu and one Enter submits the mention.
func (a codexCycle) Pickup(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	if probe := codexPickupEntry(t); probe.Reason != "" {
		return false, errors.New(probe.Reason)
	}
	const text = codexPickupCommand
	before, err := readCodex(ctx, t)
	if err != nil {
		return false, err
	}
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { return codexReady(t, s) }, errCodexNotReady, "-l", text); err != nil {
		return false, err
	}
	abandon := func(err error) (bool, error) {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool { _, open, _ := codexMenu(s); return open && codexText(s) == text }) {
		return abandon(errCodexTyped)
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool {
		rows, open, complete := codexMenu(s)
		return open && complete && skillRows(rows, PickupSkillName) == 1
	}) {
		return abandon(errCodexMenuEntry)
	}
	if err := a.key(ctx, t, fence, "Escape"); err != nil {
		return abandon(err)
	}
	time.Sleep(codexEscapeSettle)
	if !awaitCodex(ctx, t, func(s codexScreen) bool { _, open, _ := codexMenu(s); return !open && codexText(s) == text }) {
		return abandon(errCodexEscape)
	}
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { _, open, _ := codexMenu(s); return !open && codexText(s) == text }, errCodexEscape, "Enter"); err != nil {
		return abandon(err)
	}
	if !awaitCodex(ctx, t, func(s codexScreen) bool {
		return codexSubmitted(strings.Join(before.raw, "\n"), strings.Join(s.raw, "\n"), text)
	}) {
		return false, errCodexSubmit
	}
	return true, nil
}

// Answerable is false: every Codex dialog waits for the native session or the maintainer.
func (codexCycle) Answerable(string) bool { return false }

func (codexCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

// Dialog reads the dialog the pane shows. It sends nothing.
func (codexCycle) Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error) {
	if err := codexForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleDialog{}, proofError(err, errCodexUnproven)
	}
	screen, err := readCodex(ctx, t)
	if err != nil {
		return CycleDialog{}, err
	}
	dialog := codexDialog(screen)
	if dialog.Digest == "" {
		return CycleDialog{}, errors.New("no dialog")
	}
	return dialog, nil
}

// Choose enters one option number while the pane shows the same dialog; Codex takes a digit
// without Enter (P5). Then it reads back that the dialog is gone.
func (a codexCycle) Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	same := func(s codexScreen) bool {
		dialog := codexDialog(s)
		return dialog.Digest == digest && slices.Contains(dialog.Options, option)
	}
	if err := a.keyIf(ctx, t, fence, same, ErrCycleDialogChanged, "-l", option); err != nil {
		return false, err
	}
	return awaitCodex(ctx, t, func(s codexScreen) bool { return codexDialog(s).Digest != digest }), nil
}
