package core

import (
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
	"strconv"
	"strings"
	"time"
)

// The Claude Code cycle adapter (#196, chunk 03). It acts only through the session's own tmux
// pane, after proving that the pane still holds the pinned host, and reads the host's own
// Claude Code registry record for its version, its idle state and its native epoch. The facts
// it relies on are recorded in the chunk's native evidence (Claude Code 2.1.296 on Linux).

// claudeCycleVersion is the adapter's version, stored with each cycle.
const claudeCycleVersion = "claude-tmux-1"

// claudeEvidencedPlatforms are the operating systems with native evidence
// (docs/sprints/2026-10-12-context-handoff/native-evidence.md).
var claudeEvidencedPlatforms = map[string]bool{"linux": true, "darwin": true}

// claudeRegistryMax bounds a registry record read.
const claudeRegistryMax = 64 << 10

// claudeInputMark starts Claude Code's input line: the prompt mark and a no-break space.
// Transcript lines use an ordinary space.
const claudeInputMark = "❯ "

// claudeKeyWait bounds the read-back after each key.
var claudeKeyWait = 3 * time.Second

// claudeEscapeSettle separates Escape from the next key: an escape byte followed at once by
// another key reaches Claude Code as one Alt combination, and Alt+Enter does not submit (native
// evidence, #196 chunk 03).
var claudeEscapeSettle = 400 * time.Millisecond

// Fixed reasons of an input step that did not complete; the cycle records them.
var (
	errClaudeTyped     = errors.New("typed_not_read_back")
	errClaudeEscape    = errors.New("escape_changed_input")
	errClaudeSubmit    = errors.New("submit_not_read_back")
	errClaudeErase     = errors.New("erase_not_read_back")
	errClaudeUnproven  = errors.New("pane_unproven")
	errClaudeMenuEntry = errors.New("pickup_entry_not_listed")
)

type claudeCycle struct{}

func (claudeCycle) Version() string { return claudeCycleVersion }

// claudeRecord is the part of a Claude Code registry record that the adapter reads.
type claudeRecord struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	ProcStart  string `json:"procStart"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	Entrypoint string `json:"entrypoint"`
}

// claudeConfigRoot is the host's Claude Code configuration directory: its CLAUDE_CONFIG_DIR,
// else ~/.claude in its HOME.
func claudeConfigRoot(t CycleTarget) (string, error) {
	if dir, err := t.tools.processEnvironment(int(t.HostPID), "CLAUDE_CONFIG_DIR"); err != nil {
		return "", err
	} else if dir != "" {
		return dir, nil
	}
	home, err := t.tools.processEnvironment(int(t.HostPID), "HOME")
	if err != nil || home == "" {
		return "", errors.New("host home unknown")
	}
	return ClaudeConfigRoot("", home), nil
}

// readClaudeRecord reads the registry record of the pinned host. It must name the host's
// PID, its process start (when the record has one) and the interactive CLI.
func readClaudeRecord(t CycleTarget) (claudeRecord, error) {
	root, err := claudeConfigRoot(t)
	if err != nil {
		return claudeRecord{}, err
	}
	data, err := readBounded(filepath.Join(root, "sessions", strconv.FormatInt(t.HostPID, 10)+".json"), claudeRegistryMax)
	if err != nil {
		return claudeRecord{}, err
	}
	var r claudeRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return claudeRecord{}, err
	}
	if int64(r.PID) != t.HostPID || r.Entrypoint != "cli" || r.ProcStart != "" && !claudeSameStart(runtime.GOOS, r.ProcStart, t.HostStart) {
		return claudeRecord{}, errors.New("registry record of another process")
	}
	return r, nil
}

// claudeSameStart reports whether a registry record's procStart names the process start
// that Koinon read. On Linux both are field 22 of /proc/<pid>/stat. On macOS Claude Code
// writes `LC_ALL=C TZ=UTC ps -o lstart=` (the C locale's %c, whole seconds) and Koinon reads
// p_starttime in microseconds.
func claudeSameStart(goos, recorded string, start int64) bool {
	if goos != "darwin" {
		return recorded == strconv.FormatInt(start, 10)
	}
	at, err := time.Parse("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(recorded), " "))
	return err == nil && start >= 0 && at.Unix() == start/1_000_000
}

// readBounded reads a regular file of at most limit bytes.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("not a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// Probe decides whether the session's cycle can run. Its pickup entry is the koinon-pickup
// skill under the host's configuration root, which must be the binary's skill byte for byte
// (#406); project skills and other personal skills are not read.
func (claudeCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !claudeEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	// The host must have its own registry record; its version is not checked (PDR 006, Decision 5).
	if _, err := readClaudeRecord(t); err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	root, err := claudeConfigRoot(t)
	if err != nil {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	path := ClaudePickupSkillPath(root)
	if reason := pickupSkillReason(path, t.Pickup.Content); reason != "" {
		return CycleProbe{Reason: reason, Step: pickupSkillStep(path)}
	}
	return CycleProbe{Entry: PickupSkillName}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// claudeScreen is one read of the pane: its plain lines and the input line's state.
type claudeScreen struct {
	lines []string
	// input is the visible, not dim text of the input line; found reports an input line.
	input string
	found bool
	// dialog is the code of a recognized dialog, "native_dialog" for another one, or "".
	dialog string
}

// readClaude captures the pane with attributes. Dim text in the input line is a placeholder
// or a suggestion, never typed input.
func readClaude(ctx context.Context, t CycleTarget) (claudeScreen, error) {
	out, err := t.tools.tmux(ctx, t.Socket, "capture-pane", "-p", "-e", "-t", t.Pane)
	if err != nil {
		return claudeScreen{}, err
	}
	return parseClaudeScreen(out), nil
}

func parseClaudeScreen(out string) claudeScreen {
	var screen claudeScreen
	raw := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, line := range raw {
		screen.lines = append(screen.lines, stripSGR(line))
	}
	for i := len(raw) - 1; i >= 0; i-- {
		if idx := strings.Index(raw[i], claudeInputMark); idx >= 0 {
			screen.input, screen.found = strings.TrimSpace(visibleText(raw[i][idx+len(claudeInputMark):])), true
			break
		}
	}
	plain := strings.Join(screen.lines, "\n")
	if !screen.found && (strings.Contains(plain, "Enter to confirm") || dialogOf(screen).Digest != "") {
		screen.dialog = "native_dialog"
	}
	return screen
}

// visibleText returns the text of a captured line that is not drawn dim.
func visibleText(line string) string {
	var out strings.Builder
	dim := false
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				break
			}
			dim = sgrDim(line[i+2:i+end], dim)
			i += end + 1
			continue
		}
		if !dim {
			out.WriteByte(line[i])
		}
		i++
	}
	return out.String()
}

func stripSGR(line string) string {
	var out strings.Builder
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			if end := strings.IndexByte(line[i:], 'm'); end >= 0 {
				i += end + 1
				continue
			}
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
}

// Inspect reports whether the pane is ready for input: the host is at its prompt in its
// registry record and the input line is empty. A pane in copy mode is returned to its prompt
// first.
func (claudeCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return PaneState{}, proofError(err, errClaudeUnproven)
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
	screen, err := readClaude(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	if screen.dialog != "" {
		return PaneState{Prompt: screen.dialog}, nil
	}
	record, err := readClaudeRecord(t)
	if err != nil {
		return PaneState{}, err
	}
	pane := claudePaneState(record.Status, screen)
	if pane.Idle && t.ReadyAt > 0 {
		if root, err := claudeConfigRoot(t); err == nil && claudeSubagentsSince(root, record.SessionID, t.ReadyAt) {
			return PaneState{Seen: subagentAfterReady}, nil
		}
	}
	return pane, nil
}

// subagentAfterReady is the Seen code of a session whose background subagent wrote its
// transcript after the ready report: its results came after the handoff (#456).
const subagentAfterReady = "subagent_after_ready"

// claudeTranscriptID is the form of a Claude Code session ID, a UUID.
var claudeTranscriptID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// maxSubagentFiles bounds the subagent transcripts that one inspection reads.
const maxSubagentFiles = 1024

// claudeSubagentsSince reports whether a subagent transcript of the session changed after
// at (Unix milliseconds). Claude Code keeps them at projects/PROJECT/SESSION/subagents/ in
// its configuration root. Only file times are read, never content; an unreadable or absent
// directory proves no subagent work.
func claudeSubagentsSince(root, session string, at int64) bool {
	if !claudeTranscriptID.MatchString(session) {
		return false
	}
	dirs, err := filepath.Glob(filepath.Join(root, "projects", "*", session, "subagents"))
	if err != nil {
		return false
	}
	seen := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if seen++; seen > maxSubagentFiles {
				return false
			}
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().UnixMilli() > at {
				return true
			}
		}
	}
	return false
}

// claudePaneState reads the host's registry status with its screen. The host is at its prompt
// when the status is idle, or shell: the prompt with a background shell task running, such
// as a monitor of the session's inbox. Claude Code 2.1.303 takes /clear there without a dialog,
// and the task, with its status, stays through the clear (#417). A turn in progress (busy) or
// a native wait (waiting) is not at the prompt.
func claudePaneState(status string, screen claudeScreen) PaneState {
	switch {
	case status == "busy" || status == "waiting":
		return PaneState{Seen: "status_" + status}
	case status != "idle" && status != "shell":
		return PaneState{Seen: "status_unknown"}
	case !screen.found:
		return PaneState{Seen: "input_line_not_found"}
	case screen.input != "":
		return PaneState{Seen: "input_not_empty"}
	}
	return PaneState{Idle: true}
}

// key proves the pane, calls the fence and sends one key or literal text.
func (claudeCycle) key(ctx context.Context, t CycleTarget, fence CycleFence, args ...string) error {
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errClaudeUnproven)
	}
	if err := fence(ctx); err != nil {
		return err
	}
	_, err := t.tools.tmux(ctx, t.Socket, append([]string{"send-keys", "-t", t.Pane}, args...)...)
	return err
}

// keyIf proves the pane, calls the fence, reads the pane again and sends the keys only when
// check holds on that read, so the keys reach the screen that the check saw.
func (claudeCycle) keyIf(ctx context.Context, t CycleTarget, fence CycleFence, check func(claudeScreen) bool, refused error, args ...string) error {
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errClaudeUnproven)
	}
	if err := fence(ctx); err != nil {
		return err
	}
	screen, err := readClaude(ctx, t)
	if err != nil {
		return err
	}
	if !check(screen) {
		return refused
	}
	_, err = t.tools.tmux(ctx, t.Socket, append([]string{"send-keys", "-t", t.Pane}, args...)...)
	return err
}

// erase deletes text that the adapter typed into an input line that was empty before it: one
// Backspace per byte and two more, whatever the cursor did, then reads back an empty line. Like
// every key it passes the fence, and it runs only while the input line still holds exactly that
// text: a rejected fence or another draft leaves the input line as it is.
func (a claudeCycle) erase(ctx context.Context, t CycleTarget, fence CycleFence, text string) error {
	keys := []string{}
	for range len(text) + 2 {
		keys = append(keys, "BSpace")
	}
	if err := a.keyIf(ctx, t, fence, func(s claudeScreen) bool { return s.found && s.input == text }, errClaudeErase, keys...); err != nil {
		return err
	}
	if !await(ctx, t, func(s claudeScreen) bool { return s.found && s.input == "" }) {
		return errClaudeErase
	}
	return nil
}

// escape closes an open menu and waits out the escape settle before any next key.
func (a claudeCycle) escape(ctx context.Context, t CycleTarget, fence CycleFence) error {
	if err := a.key(ctx, t, fence, "Escape"); err != nil {
		return err
	}
	time.Sleep(claudeEscapeSettle)
	return nil
}

// await reads the pane until ok holds or the key wait ends.
func await(ctx context.Context, t CycleTarget, ok func(claudeScreen) bool) bool {
	deadline := time.Now().Add(claudeKeyWait)
	for {
		if screen, err := readClaude(ctx, t); err == nil && ok(screen) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// command types a slash command, closes its menu, checks the input line holds exactly the
// command, and submits it with one Enter. check runs on the screen with the menu open. Typed
// text that is not submitted, also after a failed fence, is erased again.
func (a claudeCycle) command(ctx context.Context, t CycleTarget, fence CycleFence, text string, check func(claudeScreen) bool, refused error) (bool, error) {
	if err := a.key(ctx, t, fence, "-l", text); err != nil {
		return false, err
	}
	abandon := func(err error) (bool, error) {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	if !await(ctx, t, func(s claudeScreen) bool { return s.input == text }) {
		return abandon(errClaudeTyped)
	}
	if check != nil && !await(ctx, t, check) {
		return abandon(refused)
	}
	if err := a.escape(ctx, t, fence); err != nil {
		return abandon(err)
	}
	if !await(ctx, t, func(s claudeScreen) bool { return s.input == text && menuFirst(s, text) == "" }) {
		return abandon(errClaudeEscape)
	}
	if err := a.key(ctx, t, fence, "Enter"); err != nil {
		return abandon(err)
	}
	if !await(ctx, t, func(s claudeScreen) bool { return s.found && s.input == "" }) {
		return false, errClaudeSubmit
	}
	return true, nil
}

func (a claudeCycle) Clear(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	return a.command(ctx, t, fence, "/clear", nil, nil)
}

// claudePickupCommand is the command that runs the koinon-pickup skill.
const claudePickupCommand = "/" + PickupSkillName

// menuFirst is the description of the first entry for command that the open command menu
// lists, the selected row.
func menuFirst(s claudeScreen, command string) string {
	for _, line := range s.lines {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "❯ "+command); ok && !strings.HasPrefix(line, claudeInputMark) &&
			(rest == "" || rest[0] == ' ') {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// claudeMenuScope is the scope that the menu may add after a description, such as (project).
var claudeMenuScope = regexp.MustCompile(`\s*\([a-z]+\)$`)

// describes reports whether a menu description is the start of want's description. The menu
// may add the skill's scope and shortens a long description with an ellipsis.
func describes(menu string, want PickupEntry) bool {
	menu = strings.TrimSpace(claudeMenuScope.ReplaceAllString(menu, ""))
	menu = strings.TrimSpace(strings.TrimSuffix(menu, "…"))
	return menu != "" && want.Description != "" && strings.HasPrefix(want.Description, menu)
}

// Pickup enters /koinon-pickup, only while the menu lists the installed skill's description
// first.
func (a claudeCycle) Pickup(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	return a.command(ctx, t, fence, claudePickupCommand, func(s claudeScreen) bool {
		return describes(menuFirst(s, claudePickupCommand), t.Pickup)
	}, errClaudeMenuEntry)
}

// Answerable is false: every Claude Code dialog during the cycle waits for the native session
// or the maintainer.
func (claudeCycle) Answerable(string) bool { return false }

func (claudeCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

// NewEpoch reads the host's registry session ID, which /clear replaces in the same process.
func (claudeCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	record, err := readClaudeRecord(t)
	if err != nil {
		return "", err
	}
	if record.SessionID == t.Epoch {
		return "", nil
	}
	return record.SessionID, nil
}

// claudeOption is a numbered dialog option, the selected one marked with the prompt mark.
var claudeOption = regexp.MustCompile(`^\s*(?:❯\s*)?([1-9])\.\s+\S`)

// claudeDialogLines bounds the dialog text read above its first option.
const claudeDialogLines = 8

// dialogOf is the dialog that a screen shows: the lines above its options and the options.
func dialogOf(s claudeScreen) CycleDialog {
	first, last := -1, -1
	var options []string
	for i, line := range s.lines {
		if m := claudeOption.FindStringSubmatch(line); m != nil {
			if first < 0 {
				first = i
			}
			last = i
			options = append(options, m[1])
		}
	}
	if first < 0 {
		return CycleDialog{}
	}
	var lines []string
	for _, line := range s.lines[max(0, first-claudeDialogLines) : last+1] {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "──") {
			lines = append(lines, line)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return CycleDialog{Lines: lines, Options: options, Digest: hex.EncodeToString(sum[:])}
}

// Dialog reads the dialog the pane shows. It sends nothing.
func (claudeCycle) Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error) {
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleDialog{}, proofError(err, errClaudeUnproven)
	}
	screen, err := readClaude(ctx, t)
	if err != nil {
		return CycleDialog{}, err
	}
	if screen.dialog == "" {
		return CycleDialog{}, errors.New("no dialog")
	}
	return dialogOf(screen), nil
}

// Choose enters one option number while the pane shows the same dialog, then reads back that
// the dialog is gone.
func (a claudeCycle) Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	same := func(s claudeScreen) bool {
		dialog := dialogOf(s)
		return s.dialog != "" && dialog.Digest == digest && slices.Contains(dialog.Options, option)
	}
	if err := a.keyIf(ctx, t, fence, same, ErrCycleDialogChanged, "-l", option); err != nil {
		return false, err
	}
	return await(ctx, t, func(s claudeScreen) bool { return s.dialog == "" || dialogOf(s).Digest != digest }), nil
}
