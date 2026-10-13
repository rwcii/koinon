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

// claudeEvidencedVersions are the Claude Code versions whose native behavior the evidence
// proves; another version is unavailable until its own evidence run.
var claudeEvidencedVersions = map[string]bool{"2.1.296": true}

// claudeAnyVersion admits every Claude Code version; only a livecheck build sets it.
var claudeAnyVersion bool

// claudeEvidencedPlatforms are the operating systems with native evidence; macOS follows its
// own evidence run (docs/sprints/2026-10-12-context-handoff/macos-live-check.md).
var claudeEvidencedPlatforms = map[string]bool{"linux": true}

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
	errClaudeDialog    = errors.New("dialog_not_shown")
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
	return filepath.Join(home, ".claude"), nil
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

// Probe decides whether the session's cycle can run and which pickup entry a bare /pickup
// runs. Personal skills rank over project skills (documented); a project skill ranks over an
// added directory (evidenced). Only the effective entry is checked: it must be the catalog
// item or the loader, byte for byte.
func (claudeCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !claudeEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	record, err := readClaudeRecord(t)
	if err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if !claudeEvidencedVersions[record.Version] && !claudeAnyVersion {
		return CycleProbe{Reason: "native_version_unverified"}
	}
	if !provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane) {
		return CycleProbe{Reason: "pane_unproven"}
	}
	root, err := claudeConfigRoot(t)
	if err != nil || t.Directory == "" || t.Checkout == "" {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	// The directories whose .claude Claude Code reads for this session: the session's
	// directory and each parent up to its worktree root.
	var dirs []string
	for dir := filepath.Clean(t.Directory); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if dir == filepath.Clean(t.Checkout) || dir == filepath.Dir(dir) {
			break
		}
	}
	// Personal entries rank first; a personal legacy command beside a personal skill leaves
	// their order unknown. Project entries matter only without a personal skill: there a
	// legacy command, or two project skills, leaves the effective entry unknown.
	personal := filepath.Join(root, "skills", "pickup", "SKILL.md")
	hasPersonal := exists(personal)
	if exists(filepath.Join(root, "commands", "pickup.md")) {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	var project []string
	for _, dir := range dirs {
		if hasPersonal {
			break
		}
		if exists(filepath.Join(dir, ".claude", "commands", "pickup.md")) {
			return CycleProbe{Reason: "pickup_entry_unverified"}
		}
		if path := filepath.Join(dir, ".claude", "skills", "pickup", "SKILL.md"); exists(path) {
			project = append(project, path)
		}
	}
	if len(project) > 1 {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	matches := func(path string, want []byte) string {
		data, err := readBounded(path, 1<<20)
		if err != nil {
			return "pickup_entry_unreadable"
		}
		if string(data) != string(want) {
			return "pickup_entry_differs"
		}
		return ""
	}
	loader := filepath.Join(t.LoaderDir, ".claude", "skills", "pickup", "SKILL.md")
	switch {
	case hasPersonal:
		return CycleProbe{Reason: matches(personal, t.Pickup.Content), Bootstrap: "personal"}
	case len(project) == 1:
		return CycleProbe{Reason: matches(project[0], t.Pickup.Content), Bootstrap: "project"}
	}
	reason := matches(loader, t.Loader.Content)
	if reason != "" {
		return CycleProbe{Reason: reason}
	}
	if launchedWithLoader(t) {
		return CycleProbe{Bootstrap: "loader"}
	}
	return CycleProbe{Bootstrap: "interactive"}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// launchedWithLoader reports whether the host was started with --add-dir naming the loader.
func launchedWithLoader(t CycleTarget) bool {
	_, args, err := t.tools.processCommand(int(t.HostPID))
	if err != nil {
		return false
	}
	for i, arg := range args {
		if arg == "--add-dir" && i+1 < len(args) && filepath.Clean(args[i+1]) == filepath.Clean(t.LoaderDir) ||
			arg == "--add-dir="+t.LoaderDir {
			return true
		}
	}
	return false
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
	switch {
	case strings.Contains(plain, "Add directory to workspace") && strings.Contains(plain, "1. Yes, for this session"):
		screen.dialog = "add_dir_scope"
	case !screen.found && (strings.Contains(plain, "Enter to confirm") || dialogOf(screen).Digest != ""):
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
			for _, param := range strings.Split(line[i+2:i+end], ";") {
				switch param {
				case "2":
					dim = true
				case "", "0", "22":
					dim = false
				}
			}
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

// Inspect reports whether the pane is ready for input: the host is idle in its registry
// record and the input line is empty. A pane in copy mode is returned to its prompt first.
func (claudeCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if !provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane) {
		return PaneState{}, errClaudeUnproven
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
	return PaneState{Idle: record.Status == "idle" && screen.found && screen.input == ""}, nil
}

// key proves the pane, calls the fence and sends one key or literal text.
func (claudeCycle) key(ctx context.Context, t CycleTarget, fence CycleFence, args ...string) error {
	if !provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane) {
		return errClaudeUnproven
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
	if !provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane) {
		return errClaudeUnproven
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
	if !await(ctx, t, func(s claudeScreen) bool { return s.input == text && menuFirst(s) == "" }) {
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

// menuFirst is the description of the first pickup entry that the open command menu lists.
func menuFirst(s claudeScreen) string {
	for _, line := range s.lines {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "❯ /pickup"); ok && !strings.HasPrefix(line, claudeInputMark) {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// describes reports whether a menu description is the start of want's description.
func describes(menu string, want PickupEntry) bool {
	menu = strings.TrimSuffix(strings.TrimSuffix(menu, "(project)"), "…")
	menu = strings.TrimSpace(menu)
	return menu != "" && want.Description != "" && strings.HasPrefix(want.Description, menu)
}

func (a claudeCycle) Pickup(ctx context.Context, t CycleTarget, bootstrap string, fence CycleFence) (bool, error) {
	want := t.Pickup
	if bootstrap == "loader" || bootstrap == "interactive" {
		want = t.Loader
	}
	return a.command(ctx, t, fence, "/pickup", func(s claudeScreen) bool { return describes(menuFirst(s), want) }, errClaudeMenuEntry)
}

// Bootstrap adds the loader for this session only, then proves that /pickup lists the
// loader first and erases that text without submitting it.
func (a claudeCycle) Bootstrap(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	text := "/add-dir " + t.LoaderDir
	if err := a.key(ctx, t, fence, "-l", text); err != nil {
		return false, err
	}
	if !await(ctx, t, func(s claudeScreen) bool { return s.input == text }) {
		a.erase(ctx, t, fence, text)
		return false, errClaudeTyped
	}
	if err := a.escape(ctx, t, fence); err != nil {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	if err := a.key(ctx, t, fence, "Enter"); err != nil {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	// A directory that this session already added (an earlier bootstrap; /clear keeps it) gets
	// Claude Code's reply instead of the dialog (native evidence, #196).
	added := false
	if !await(ctx, t, func(s claudeScreen) bool {
		added = claudeAlreadyAdded(s, t.LoaderDir)
		return added || s.dialog == "add_dir_scope"
	}) {
		return false, errClaudeDialog
	}
	if !added {
		if ok, err := a.Answer(ctx, t, "add_dir_scope", fence); err != nil || !ok {
			return false, err
		}
	}
	if err := a.key(ctx, t, fence, "-l", "/pickup"); err != nil {
		return false, err
	}
	listed := await(ctx, t, func(s claudeScreen) bool { return s.input == "/pickup" && describes(menuFirst(s), t.Loader) })
	if err := a.erase(ctx, t, fence, "/pickup"); err != nil {
		return false, err
	}
	if !listed {
		return false, errClaudeMenuEntry
	}
	return true, nil
}

// claudeAlreadyAdded reports Claude Code's reply to /add-dir for a directory that the session
// already has: the input line is empty again, and the last /add-dir of dir in the transcript is
// followed by that reply.
func claudeAlreadyAdded(s claudeScreen, dir string) bool {
	if !s.found || s.input != "" || s.dialog != "" {
		return false
	}
	for i := len(s.lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(s.lines[i]) == "❯ /add-dir "+dir {
			for _, line := range s.lines[i+1:] {
				if strings.Contains(line, dir+" is already added as a working directory.") {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (claudeCycle) Answerable(prompt string) bool { return prompt == "add_dir_scope" }

// Answer chooses option 1, "Yes, for this session", in the add-directory dialog; option 2
// would change the persistent setup and is never chosen.
func (a claudeCycle) Answer(ctx context.Context, t CycleTarget, prompt string, fence CycleFence) (bool, error) {
	if prompt != "add_dir_scope" {
		return false, nil
	}
	if err := a.key(ctx, t, fence, "-l", "1"); err != nil {
		return false, err
	}
	return await(ctx, t, func(s claudeScreen) bool { return s.dialog == "" && s.found }), nil
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

// claudeLoaderContent is the loader's pickup entry: it reads the canonical catalog item and
// checks it against the cycle's pinned catalog digest. It holds no procedure of its own.
const claudeLoaderContent = `---
name: pickup
description: Koinon session loader for the canonical pickup procedure. Resume work from this agent's newest handoff.
---

Call the koinon library_read tool for item skill.pickup. When context_cycle_status shows a cycle,
the catalog_digest that library_read returns must equal the cycle's catalog_digest; if it differs,
or the tool is unavailable, stop and report that. Otherwise follow the returned procedure.
`

// claudeLoaderEntry is the loader's bytes and its menu description.
func claudeLoaderEntry() PickupEntry {
	return PickupEntry{Content: []byte(claudeLoaderContent),
		Description: "Koinon session loader for the canonical pickup procedure. Resume work from this agent's newest handoff."}
}

// CycleLoaderDir is the directory that a Claude launch adds with --add-dir, under the state root.
func CycleLoaderDir(stateRoot string) string {
	return filepath.Join(stateRoot, "cycle", "claude-loader")
}

// EnsureCycleLoader writes the loader's pickup entry under the state root when it is missing
// or different. The directories are private to this account.
func EnsureCycleLoader(stateRoot string) error {
	dir := filepath.Join(CycleLoaderDir(stateRoot), ".claude", "skills", "pickup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "SKILL.md")
	if data, err := os.ReadFile(path); err == nil && string(data) == claudeLoaderContent {
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".SKILL-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(claudeLoaderContent); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
	if !provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane) {
		return CycleDialog{}, errClaudeUnproven
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
