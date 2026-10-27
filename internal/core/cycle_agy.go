package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rwcii/koinon/internal/platform"
)

// The agy cycle adapter (#512). It acts only through the session's own tmux pane, after
// proving that the pane's terminal gives input to the pinned host (agyForeground). agy starts
// a new conversation at /clear in the same host process, and the host's status-line hook
// reports it (agy_witness.go); that report, received after the clear intent, is the new epoch.
// The facts it relies on are recorded in the sprint's native evidence (Antigravity CLI 1.4.7 on
// Linux).

// agyCycleVersion is the adapter's version, stored with each cycle.
const agyCycleVersion = "agy-tmux-1"

// agyEvidencedPlatforms are the operating systems with native evidence for this adapter.
var agyEvidencedPlatforms = map[string]bool{"linux": true}

// agyKeyWait bounds the read-back after each key.
var agyKeyWait = 3 * time.Second

// Fixed reasons of an agy input step that did not complete; the cycle records them.
var (
	errAgyTyped     = errors.New("typed_not_read_back")
	errAgySubmit    = errors.New("submit_not_read_back")
	errAgyErase     = errors.New("erase_not_read_back")
	errAgyUnproven  = errors.New("pane_unproven")
	errAgyMenuEntry = errors.New("pickup_entry_unverified")
	errAgyNotReady  = errors.New("pane_not_ready")
	errAgySelect    = errors.New("option_not_read_back")
)

type agyCycle struct{}

func (agyCycle) Version() string { return agyCycleVersion }

// agyPickupCommand is the slash command that runs the koinon-pickup skill.
const agyPickupCommand = "/" + PickupSkillName

// agyIdleFooter is the footer of an empty composer with no running turn; a draft, a running
// turn and an open menu each show another one.
const agyIdleFooter = "? for shortcuts"

// agyMenuHint ends the slash-command menu.
const agyMenuHint = "↑/↓ Navigate · enter Select · tab Complete"

var (
	agyMenuRowLine = regexp.MustCompile(`^(> |  )(/\S+)(?:\s+(.*?))?\s*$`)
	agyScroll      = regexp.MustCompile(`^[↑↓] \d+ more$`)
	agyBannerLine  = regexp.MustCompile(`[▄▀]\s+Antigravity CLI \d+\.\d+\.\d+\s*$`)
)

// agyRule reports a full-width rule, a line of ─ only.
func agyRule(line string) bool {
	t := strings.TrimSpace(line)
	return utf8.RuneCountInString(t) >= 10 && strings.Trim(t, "─") == ""
}

// agyFooter is the index of the screen's last non-blank line and its left part, before the
// model name on the right.
func agyFooter(c codexScreen) (int, string) {
	for i := len(c.plain) - 1; i >= 0; i-- {
		if strings.TrimSpace(c.plain[i]) != "" {
			left, _, _ := strings.Cut(c.plain[i], "   ")
			return i, strings.TrimSpace(left)
		}
	}
	return -1, ""
}

// agyRow is one row of the slash-command menu.
type agyRow struct {
	name, description string
	selected          bool
}

// agyLayout is a screen with a composer: the input between the last two rules, and below them
// at most the slash-command menu above the footer.
type agyLayout struct {
	upper, lower, footer int
	text                 string
	menu                 []agyRow
	complete             bool // the menu shows no scroll marker
}

// agyComposer reads the composer layout, or reports false for any other screen, such as a
// dialog.
func agyComposer(c codexScreen) (agyLayout, bool) {
	footer, _ := agyFooter(c)
	lower, upper := -1, -1
	for i := footer - 1; i >= 0; i-- {
		if agyRule(c.plain[i]) {
			if lower < 0 {
				lower = i
			} else {
				upper = i
				break
			}
		}
	}
	if upper < 0 || !strings.HasPrefix(c.plain[upper+1], ">") {
		return agyLayout{}, false
	}
	l := agyLayout{upper: upper, lower: lower, footer: footer, complete: true}
	hint := false
	for i := lower + 1; i < footer; i++ {
		line := strings.TrimSpace(c.plain[i])
		switch m := agyMenuRowLine.FindStringSubmatch(c.plain[i]); {
		case line == "":
		case line == agyMenuHint:
			hint = true
		case agyScroll.MatchString(line):
			l.complete = false
		case m != nil && !hint:
			l.menu = append(l.menu, agyRow{name: m[2], description: m[3], selected: m[1] == "> "})
		default:
			return agyLayout{}, false
		}
	}
	if len(l.menu) > 0 != hint {
		return agyLayout{}, false
	}
	parts := []string{}
	if first := strings.TrimSpace(strings.TrimPrefix(c.plain[upper+1], ">")); first != "" {
		parts = append(parts, first)
	}
	for _, line := range c.plain[upper+2 : lower] {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	l.text = strings.Join(parts, "\n")
	return l, true
}

// agyText is the composer's text, or "" without a composer.
func agyText(c codexScreen) string {
	l, _ := agyComposer(c)
	return l.text
}

// agyEmpty reports an empty composer with its menu closed.
func agyEmpty(c codexScreen) bool {
	l, ok := agyComposer(c)
	return ok && l.text == "" && len(l.menu) == 0
}

// agyMenuReady reports a composer that holds exactly text and a complete menu in which one
// row is named text, that row is selected, and match accepts it.
func agyMenuReady(c codexScreen, text string, match func(agyRow) bool) bool {
	l, ok := agyComposer(c)
	if !ok || l.text != text || !l.complete {
		return false
	}
	named := 0
	for _, row := range l.menu {
		if row.name == text {
			named++
			if !row.selected || match != nil && !match(row) {
				return false
			}
		}
	}
	return named == 1
}

// agyBanner is the index of the latest session banner's first line, or -1.
func agyBanner(c codexScreen) int {
	for i := len(c.plain) - 1; i >= 0; i-- {
		if agyBannerLine.MatchString(c.plain[i]) {
			return i
		}
	}
	return -1
}

// agyIdle reports an empty composer with its menu closed and the idle footer.
func agyIdle(c codexScreen) bool {
	_, left := agyFooter(c)
	return agyEmpty(c) && left == agyIdleFooter
}

// agyCleared reports the screen right after /clear: an idle composer below the latest banner,
// with only blank rows and notice-box rows (┃) between them.
func agyCleared(c codexScreen) bool {
	l, ok := agyComposer(c)
	banner := agyBanner(c)
	if !ok || !agyIdle(c) || banner < 0 || banner >= l.upper {
		return false
	}
	for i := banner + 1; i < l.upper; i++ {
		line := strings.TrimSpace(c.plain[i])
		art := i <= banner+4 && strings.ContainsAny(line, "▄▀")
		if line != "" && !art && !strings.HasPrefix(line, "┃") {
			return false
		}
	}
	return true
}

// agyPrompts counts the submitted prompts with text below the latest banner: a rule followed
// by "> text", above the composer.
func agyPrompts(c codexScreen, text string) int {
	l, ok := agyComposer(c)
	banner := agyBanner(c)
	if !ok || banner < 0 {
		return -1
	}
	count := 0
	for i := banner + 1; i+1 < l.upper; i++ {
		if agyRule(c.plain[i]) && strings.TrimSpace(strings.TrimPrefix(c.plain[i+1], ">")) == text && strings.HasPrefix(c.plain[i+1], "> ") {
			count++
		}
	}
	return count
}

// agySubmitted reports that text left the composer as a new submitted prompt.
func agySubmitted(before, after codexScreen, text string) bool {
	l, ok := agyComposer(after)
	return ok && l.text == "" && len(l.menu) == 0 && agyPrompts(after, text) > max(agyPrompts(before, text), 0)
}

// agyOption is a numbered option of an approval dialog; "> " marks the selected one.
var agyOption = regexp.MustCompile(`^(> |  )([1-9])\. (\S.*)$`)

// The approval dialog of the evidence (N5): a command approval.
const (
	agyDialogStart    = "Requesting permission for:"
	agyDialogQuestion = "Run this command?"
	agyDialogHint     = "↑/↓ Navigate · tab Amend · ctrl+g edit/expand command"
)

// agyApproval is an approval dialog as the screen shows it: its text without the selection
// mark, the visible options, the selected one, and the digest of the text above the options.
type agyApproval struct {
	dialog   CycleDialog
	selected string
	head     string
}

// agyDialog reads the evidenced command-approval dialog: the request, the question, numbered
// options (long ones wrap onto further rows) and its hint above the footer. Any other screen,
// such as an MCP approval or the folder-trust question, is no dialog here; it stays visible for
// the native session or the maintainer.
func agyDialog(c codexScreen) agyApproval {
	footer, left := agyFooter(c)
	if footer < 1 || left != "esc to cancel" {
		return agyApproval{}
	}
	hint := footer - 1
	for hint >= 0 && strings.TrimSpace(c.plain[hint]) == "" {
		hint--
	}
	if hint < 0 || strings.TrimSpace(c.plain[hint]) != agyDialogHint {
		return agyApproval{}
	}
	start := -1
	for i := hint - 1; i >= 0; i-- {
		if strings.TrimSpace(c.plain[i]) == agyDialogStart {
			start = i
			break
		}
		if agyRule(c.plain[i]) {
			break
		}
	}
	if start < 0 {
		return agyApproval{}
	}
	var lines, head, options []string
	selected, question := "", false
	for _, raw := range c.plain[start:hint] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := agyOption.FindStringSubmatch(raw); m != nil && question {
			options = append(options, m[2])
			if m[1] == "> " {
				selected = m[2]
			}
			lines = append(lines, m[2]+". "+m[3])
			continue
		}
		if len(options) > 0 {
			lines[len(lines)-1] += " " + line // a wrapped option
			continue
		}
		question = question || line == agyDialogQuestion
		head = append(head, line)
		lines = append(lines, line)
	}
	if !question || len(options) == 0 || selected == "" {
		return agyApproval{}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	top := sha256.Sum256([]byte(strings.Join(head, "\n")))
	return agyApproval{dialog: CycleDialog{Lines: lines, Options: options, Digest: hex.EncodeToString(sum[:])},
		selected: selected, head: hex.EncodeToString(top[:])}
}

// option is the displayed text of one visible option, wrapped rows included, or "".
func (a agyApproval) option(n string) string {
	lines := a.dialog.Lines
	for _, line := range lines[len(lines)-len(a.dialog.Options):] {
		if strings.HasPrefix(line, n+". ") {
			return line
		}
	}
	return ""
}

// agyForeground proves the pane (provenPane) and that its terminal gives input to the host:
// the host shares the pane process's terminal and leads its foreground process group. Every
// other process of that group is an ancestor of the host, such as the pane's shell, or a
// descendant that is no agent CLI, such as the koinon mcp server and the status-line command,
// which agy keeps in its own group (N2).
func agyForeground(ctx context.Context, h hostTools, pid, start int64, socket, pane string) error {
	panePID, parents, err := provenPane(ctx, h, pid, start, socket, pane)
	if err != nil {
		return err
	}
	if h.processGroups == nil || h.processCommand == nil {
		return errPaneUnproven
	}
	groups, err := h.processGroups()
	if err != nil {
		return errPaneUnproven
	}
	host, shell := groups[int(pid)], groups[panePID]
	if host.Terminal == 0 || host.Terminal != shell.Terminal || host.Foreground <= 0 || host.Group != host.Foreground {
		return errPaneUnproven
	}
	ancestors := map[int]bool{}
	for current := parents[int(pid)]; current > 1 && len(ancestors) < maxGrokAncestors; current = parents[current] {
		ancestors[current] = true
	}
	descendant := func(member int) bool {
		for current, steps := parents[member], 0; current > 1 && steps < maxGrokAncestors; current, steps = parents[current], steps+1 {
			if current == int(pid) {
				return true
			}
		}
		return false
	}
	for member, g := range groups {
		if g.Group != host.Foreground || member == int(pid) || ancestors[member] {
			continue
		}
		executable, args, err := h.processCommand(member)
		if !descendant(member) || err != nil || platform.AgentCommand(executable, args) {
			return errPaneUnproven
		}
	}
	return nil
}

// agyHome is the host's HOME.
func agyHome(t CycleTarget) (string, error) {
	home, err := t.tools.processEnvironment(int(t.HostPID), "HOME")
	if err != nil || home == "" {
		return "", errors.New("host home unknown")
	}
	return home, nil
}

// AgyPickupSkillPath is the koinon-pickup skill in agy's global customization root, which agy
// lists for every session (N4).
func AgyPickupSkillPath(home string) string {
	return filepath.Join(home, ".gemini", "config", "skills", PickupSkillName, "SKILL.md")
}

// agyShadowRoots are the workspace skill roots whose koinon-pickup replaces the personal one in
// agy's menu, in agy's order (N4); a workspace .gemini/skills does not.
var agyShadowRoots = []string{".agent", ".agents"}

// agyPickupEntry checks the effective koinon-pickup entry: the personal skill must be the
// binary's skill byte for byte, and a workspace skill that would replace it must be too
// (pickup_entry_shadowed otherwise).
func agyPickupEntry(t CycleTarget) CycleProbe {
	home, err := agyHome(t)
	if err != nil || !filepath.IsAbs(t.Directory) {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	path := AgyPickupSkillPath(home)
	if reason := pickupSkillReason(path, t.Pickup.Content); reason != "" {
		return CycleProbe{Reason: reason, Step: pickupSkillStep(path)}
	}
	for _, root := range agyShadowRoots {
		shadow := filepath.Join(t.Directory, root, "skills", PickupSkillName, "SKILL.md")
		if _, err := os.Lstat(shadow); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if pickupSkillReason(shadow, t.Pickup.Content) != "" {
			return CycleProbe{Reason: "pickup_entry_shadowed", Step: "remove or rename the workspace skill " + shadow}
		}
	}
	return CycleProbe{}
}

// agyStatusLineRecord is the part of setup's agy-statusline.json that the probe reads.
type agyStatusLineRecord struct {
	State        string          `json:"state"`
	SettingsFile string          `json:"settings_file"`
	Wrapper      json.RawMessage `json:"wrapper"`
}

// agyStatusLineStep names the command that sets the status line up.
const agyStatusLineStep = "run koinon setup agy to set up the agy status line"

// agyStatusLine checks that the host's Antigravity CLI settings run Koinon's agy-status hook as
// its statusLine entry, the one that setup recorded as enabled; the witness and the context
// readings come from it.
func agyStatusLine(t CycleTarget, home string) CycleProbe {
	unverified := CycleProbe{Reason: "statusline_unverified", Step: agyStatusLineStep}
	if t.stateRoot == "" {
		return unverified
	}
	data, err := readBounded(filepath.Join(t.stateRoot, "agy-statusline.json"), 4<<20)
	var record agyStatusLineRecord
	path := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	if err != nil || json.Unmarshal(data, &record) != nil || record.State != "enabled" || filepath.Clean(record.SettingsFile) != path {
		return unverified
	}
	settings, err := readBounded(path, 1<<20)
	var members map[string]json.RawMessage
	if err != nil || json.Unmarshal(settings, &members) != nil {
		return unverified
	}
	var entry, wrapper any
	var command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if json.Unmarshal(members["statusLine"], &entry) != nil || json.Unmarshal(record.Wrapper, &wrapper) != nil ||
		!reflect.DeepEqual(entry, wrapper) || json.Unmarshal(record.Wrapper, &command) != nil ||
		command.Type != "command" || !strings.Contains(command.Command, " hook agy-status") {
		return unverified
	}
	return CycleProbe{}
}

// Probe decides whether the session's cycle can run: an evidenced platform, the pane, the
// participant address that the conversation after /clear takes over, the session's own
// conversation as the latest observed epoch, the status line and the pickup entry.
func (agyCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !agyEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	home, err := agyHome(t)
	if err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := agyForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	if !t.Holds {
		return CycleProbe{Reason: "participant_unheld"}
	}
	// Before the clear, the status line's latest reading must name this conversation.
	if t.SuccessorEpoch == "" && t.observedEpoch != t.SessionID {
		return CycleProbe{Reason: "epoch_unverified"}
	}
	if probe := agyStatusLine(t, home); probe.Reason != "" {
		return probe
	}
	if probe := agyPickupEntry(t); probe.Reason != "" {
		return probe
	}
	return CycleProbe{Entry: PickupSkillName}
}

// readAgy captures the pane with attributes and its history.
func readAgy(ctx context.Context, t CycleTarget) (codexScreen, error) {
	out, err := t.tools.tmux(ctx, t.Socket, "capture-pane", "-p", "-e", "-J", "-S", "-", "-t", t.Pane)
	if err != nil {
		return codexScreen{}, err
	}
	return readCodexScreen(out), nil
}

// agyNotReady is the fixed code of what keeps a screen from the cycle's next input, or "".
// After the clear, the screen must still be the cleared one.
func agyNotReady(t CycleTarget, c codexScreen) string {
	l, ok := agyComposer(c)
	_, left := agyFooter(c)
	switch {
	case agyDialog(c).dialog.Digest != "":
		return "native_dialog"
	case !ok:
		return "input_line_not_found"
	case l.text != "":
		return "input_not_empty"
	case len(l.menu) > 0:
		return "menu_open"
	case left != agyIdleFooter:
		return "turn_running"
	case t.SuccessorEpoch != "" && !agyCleared(c):
		return "successor_not_cleared"
	}
	return ""
}

// Inspect reports whether the pane is ready for input. A draft is reported and kept.
func (agyCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if err := agyForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return PaneState{}, proofError(err, errAgyUnproven)
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
	screen, err := readAgy(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	switch seen := agyNotReady(t, screen); seen {
	case "native_dialog":
		return PaneState{Prompt: seen}, nil
	case "":
		return PaneState{Idle: true}, nil
	default:
		return PaneState{Seen: seen}, nil
	}
}

// keyIf proves the pane, calls the fence, reads the pane again and sends the keys only when
// check holds on that read.
func (agyCycle) keyIf(ctx context.Context, t CycleTarget, fence CycleFence, check func(codexScreen) bool, refused error, args ...string) error {
	if err := agyForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errAgyUnproven)
	}
	if err := fence(ctx); err != nil {
		return err
	}
	screen, err := readAgy(ctx, t)
	if err != nil {
		return err
	}
	if !check(screen) {
		return refused
	}
	_, err = t.tools.tmux(ctx, t.Socket, append([]string{"send-keys", "-t", t.Pane}, args...)...)
	return err
}

// awaitAgy reads the pane until ok holds or the key wait ends.
func awaitAgy(ctx context.Context, t CycleTarget, ok func(codexScreen) bool) bool {
	deadline := time.Now().Add(agyKeyWait)
	for {
		if screen, err := readAgy(ctx, t); err == nil && ok(screen) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// erase removes text that the adapter typed: C-u clears the whole composer, so it is sent only
// while a fresh read shows exactly that text; then an empty composer is read back.
func (a agyCycle) erase(ctx context.Context, t CycleTarget, fence CycleFence, text string) error {
	if err := a.keyIf(ctx, t, fence, func(s codexScreen) bool { return agyText(s) == text }, errAgyErase, "C-u"); err != nil {
		return err
	}
	if !awaitAgy(ctx, t, agyEmpty) {
		return errAgyErase
	}
	return nil
}

// command types a slash command while a fresh read shows the pane ready, and submits it with
// one Enter while the composer holds exactly it and its menu selects its one row (match accepts
// that row). done reads back its effect. Typed text that is not submitted is erased again; a
// draft that someone started is never touched.
func (a agyCycle) command(ctx context.Context, t CycleTarget, fence CycleFence, text string, match func(agyRow) bool, done func(codexScreen) bool) (bool, error) {
	ready := func(s codexScreen) bool { return agyNotReady(t, s) == "" }
	if err := a.keyIf(ctx, t, fence, ready, errAgyNotReady, "-l", text); err != nil {
		return false, err
	}
	typed := func(s codexScreen) bool { return agyMenuReady(s, text, match) }
	if !awaitAgy(ctx, t, typed) {
		// erase succeeds only on the typed text: then the menu row was what failed.
		if err := a.erase(ctx, t, fence, text); err == nil && match != nil {
			return false, errAgyMenuEntry
		}
		return false, errAgyTyped
	}
	if err := a.keyIf(ctx, t, fence, typed, errAgyTyped, "Enter"); err != nil {
		a.erase(ctx, t, fence, text)
		return false, err
	}
	if !awaitAgy(ctx, t, done) {
		return false, errAgySubmit
	}
	return true, nil
}

// Clear enters /clear and reads back the cleared screen; that is its receipt. The new
// conversation is proven separately, by NewEpoch.
func (a agyCycle) Clear(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	return a.command(ctx, t, fence, "/clear", nil, agyCleared)
}

// NewEpoch returns the conversation that the host's status line reported after the clear
// intent, other than the cycle's own epoch. Before the clear's receipt is recorded it proves
// nothing; until the reset is verified it needs exactly one such conversation. After the
// reset it is the newest one, so that a later clear is seen.
func (agyCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	if t.State == cycleClearIntent || t.witnesses.Overflow {
		return "", nil
	}
	var others []agyWitness
	for _, w := range t.witnesses.List {
		if w.ID != t.Epoch && nativeEpoch.MatchString(w.ID) {
			others = append(others, w)
		}
	}
	if len(others) == 0 {
		return "", nil
	}
	if t.State == cycleClearReceived {
		if len(others) != 1 {
			return "", nil
		}
		return others[0].ID, nil
	}
	newest := others[0]
	for _, w := range others[1:] {
		if w.First > newest.First {
			newest = w
		}
	}
	return newest.ID, nil
}

// pickupRow accepts the menu row whose description is the canonical skill's, as far as the
// pane shows it before its "..." cut.
func pickupRow(want PickupEntry) func(agyRow) bool {
	return func(row agyRow) bool {
		shown := strings.TrimSpace(strings.TrimSuffix(row.description, "..."))
		return want.Description != "" && len(shown) >= 16 && strings.HasPrefix(want.Description, shown)
	}
}

// Pickup types /koinon-pickup and submits it only when the effective entry is still the
// installed skill (agyPickupEntry, read again after the clear reloaded the skills) and the menu
// selects its one row with the canonical description. The receipt is the command as a new
// submitted prompt above an empty composer.
func (a agyCycle) Pickup(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	if probe := agyPickupEntry(t); probe.Reason != "" {
		return false, errors.New(probe.Reason)
	}
	before, err := readAgy(ctx, t)
	if err != nil {
		return false, err
	}
	return a.command(ctx, t, fence, agyPickupCommand, pickupRow(t.Pickup), func(s codexScreen) bool {
		return agySubmitted(before, s, agyPickupCommand)
	})
}

// Answerable is false: every agy dialog waits for the native session or the maintainer.
func (agyCycle) Answerable(string) bool { return false }

func (agyCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

// Dialog reads the approval dialog the pane shows. It sends nothing.
func (agyCycle) Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error) {
	if err := agyForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleDialog{}, proofError(err, errAgyUnproven)
	}
	screen, err := readAgy(ctx, t)
	if err != nil {
		return CycleDialog{}, err
	}
	approval := agyDialog(screen)
	if approval.dialog.Digest == "" {
		return CycleDialog{}, errors.New("no dialog")
	}
	return approval.dialog, nil
}

// Choose selects one visible option while the pane shows the same dialog: the digit moves the
// selection, which is read back, and one Enter confirms it while the same request still shows
// that option, with the same text, selected; the option list may scroll meanwhile. Then it
// reads back that the request is gone (N5).
func (a agyCycle) Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	var head, meaning string
	same := func(s codexScreen) bool {
		approval := agyDialog(s)
		head, meaning = approval.head, approval.option(option)
		return approval.dialog.Digest == digest && meaning != ""
	}
	if err := a.keyIf(ctx, t, fence, same, ErrCycleDialogChanged, "-l", option); err != nil {
		return false, err
	}
	chosen := func(s codexScreen) bool {
		approval := agyDialog(s)
		return approval.head == head && approval.selected == option && approval.option(option) == meaning
	}
	if !awaitAgy(ctx, t, chosen) {
		return false, errAgySelect
	}
	if err := a.keyIf(ctx, t, fence, chosen, ErrCycleDialogChanged, "Enter"); err != nil {
		return false, err
	}
	return awaitAgy(ctx, t, func(s codexScreen) bool { return agyDialog(s).head != head }), nil
}
