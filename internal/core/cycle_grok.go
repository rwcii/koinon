package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// The Grok cycle adapter (#515). It acts only through the session's own tmux pane, after
// proving that the pane's terminal gives input to the pinned Grok host (grokForeground). Grok starts
// a new session at /new in the same host process, with a new session ID and MCP server, and
// keeps the earlier session live; the host's status-line hook reports the new session
// (agy_witness.go), and that report, received after the clear intent, is the new epoch. The
// facts it relies on are recorded in the sprint's native evidence (Grok Build 1.0.50 on Linux).

// grokCycleVersion is the adapter's version, stored with each cycle.
const grokCycleVersion = "grok-tmux-1"

// grokEvidencedPlatforms are the operating systems with native evidence for this adapter.
var grokEvidencedPlatforms = map[string]bool{"linux": true}

// grokKeyWait bounds the read-back after each key.
var grokKeyWait = 3 * time.Second

// errGrokChoice is a dialog whose answer was not read back: the dialog stayed.
var errGrokChoice = errors.New("option_not_read_back")

type grokCycle struct{}

func (grokCycle) Version() string { return grokCycleVersion }

// grokPickupCommand is the slash command that runs the koinon-pickup skill.
const grokPickupCommand = "/" + PickupSkillName

var (
	// grokMenuRow is a slash-command menu row; "❯ " marks the selected one.
	grokMenuRow = regexp.MustCompile(`^    (❯ |  )(/\S+)\s+(\S.*?)\s*$`)
	// grokSessionLine is the notice of the session that /new started.
	grokSessionLine = regexp.MustCompile(`^\s*Session (\S+), use /dashboard to switch between sessions\s*$`)
	// grokOption is a numbered dialog option; (●) marks the selected one.
	grokOption = regexp.MustCompile(`^\s*┃\s+([1-9]) \((●|○)\) (\S.*?)\s*$`)
	// grokSelect is the dialog's footer, with its number of options.
	grokSelect = regexp.MustCompile(`^\s*1/([1-9]):select\s`)
	// grokRuleLine is a full-width rule; the menu's upper rule carries its count of matches.
	grokRuleLine = regexp.MustCompile(`^─{10,}(?:\d+─)?$`)
)

// grokRule reports a full-width rule.
func grokRule(line string) bool { return grokRuleLine.MatchString(strings.TrimSpace(line)) }

// grokLines are the screen's plain lines without the scrollbar thumb, which Grok draws in the
// last column once the transcript is longer than the screen.
func grokLines(c codexScreen) []string {
	lines := make([]string, len(c.plain))
	for i, line := range c.plain {
		line = strings.TrimRight(line, " ")
		if rest, ok := strings.CutSuffix(line, "█"); ok && strings.HasSuffix(rest, " ") {
			line = strings.TrimRight(rest, " ")
		}
		lines[i] = line
	}
	return lines
}

// grokText is the screen as plain lines joined, for grokComposer and grokIdle.
func grokText(c codexScreen) string { return strings.Join(grokLines(c), "\n") }

// grokRow is one row of the slash-command menu.
type grokRow struct {
	name, description string
	selected          bool
}

// grokMenu reads the slash-command menu: the rows between the two rules right above the input
// box (N4). A description that wraps continues on further rows.
func grokMenu(c codexScreen) []grokRow {
	lines := grokLines(c)
	box := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "╭") {
			box = i
			break
		}
	}
	if box < 2 || !grokRule(lines[box-1]) {
		return nil
	}
	upper := box - 2
	for upper >= 0 && !grokRule(lines[upper]) {
		upper--
	}
	if upper < 0 {
		return nil
	}
	var rows []grokRow
	for _, line := range lines[upper+1 : box-1] {
		if m := grokMenuRow.FindStringSubmatch(line); m != nil {
			rows = append(rows, grokRow{name: m[2], description: m[3], selected: m[1] == "❯ "})
		} else if text := strings.TrimSpace(line); text != "" {
			if len(rows) == 0 {
				return nil
			}
			rows[len(rows)-1].description += " " + text
		}
	}
	return rows
}

// grokSession is the session that the screen's latest /new notice names, or "".
func grokSession(c codexScreen) string {
	lines := grokLines(c)
	for i := len(lines) - 1; i >= 0; i-- {
		if m := grokSessionLine.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

// grokPrompts counts the transcript lines that show text as a submitted prompt: the prompt
// mark and text, then nothing or, after a gap, the time on the right (N7).
func grokPrompts(c codexScreen, text string) int {
	lines := grokLines(c)
	count := 0
	for _, line := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "❯ "+text)
		if ok && (rest == "" || strings.HasPrefix(rest, "   ")) {
			count++
		}
	}
	return count
}

// grokApproval is an approval dialog as the screen shows it: its text without the selection
// mark, and the visible options.
type grokApproval struct {
	dialog CycleDialog
}

// grokDialog reads an approval dialog (shell command or MCP tool, N8): the ┃ box above the
// 1/N:select footer, with exactly N numbered options. Any other screen is no dialog.
func grokDialog(c codexScreen) grokApproval {
	screen := grokLines(c)
	footer := -1
	n := ""
	for i := len(screen) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(screen[i]); line != "" {
			if m := grokSelect.FindStringSubmatch(screen[i]); m != nil {
				footer, n = i, m[1]
			}
			break
		}
	}
	if footer < 0 {
		return grokApproval{}
	}
	end := footer - 1
	for end >= 0 && strings.TrimSpace(screen[end]) == "" {
		end--
	}
	start := end
	for start >= 0 && strings.HasPrefix(strings.TrimSpace(screen[start]), "┃") {
		start--
	}
	var lines, options []string
	for _, raw := range screen[start+1 : end+1] {
		if m := grokOption.FindStringSubmatch(raw); m != nil {
			options = append(options, m[1])
			lines = append(lines, m[1]+". "+m[3])
			continue
		}
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "┃"))
		if line == "" {
			continue
		}
		if len(options) > 0 {
			lines[len(lines)-1] += " " + line // a wrapped option
			continue
		}
		lines = append(lines, line)
	}
	if len(options) == 0 || n != string(rune('0'+len(options))) || len(lines) == len(options) {
		return grokApproval{}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return grokApproval{dialog: CycleDialog{Lines: lines, Options: options, Digest: hex.EncodeToString(sum[:])}}
}

// option reports whether n is a visible option.
func (a grokApproval) option(n string) bool {
	for _, o := range a.dialog.Options {
		if o == n {
			return true
		}
	}
	return false
}

// grokNotReady is the fixed code of what keeps a screen from the cycle's next input, or "".
// After the reset, the screen must show the successor's /new notice.
func grokNotReady(t CycleTarget, c codexScreen) string {
	text, _, found := grokComposer(grokText(c))
	switch {
	case grokDialog(c).dialog.Digest != "":
		return "native_dialog"
	case !found:
		return "input_line_not_found"
	case text != "":
		return "input_not_empty"
	case len(grokMenu(c)) > 0:
		return "menu_open"
	case !grokIdle(grokText(c)):
		return "turn_running"
	case t.SuccessorEpoch != "" && grokSession(c) != t.SuccessorEpoch:
		return "successor_not_cleared"
	}
	return ""
}

// grokMenuReady reports a composer that holds exactly text and a menu that selects its one row
// named text, which match accepts.
func grokMenuReady(c codexScreen, text string, match func(grokRow) bool) bool {
	composer, _, found := grokComposer(grokText(c))
	if !found || composer != text {
		return false
	}
	named := 0
	for _, row := range grokMenu(c) {
		if row.name == text {
			named++
			if !row.selected || match != nil && !match(row) {
				return false
			}
		}
	}
	return named == 1
}

// grokEmpty reports an empty composer with its menu closed.
func grokEmpty(c codexScreen) bool {
	text, _, found := grokComposer(grokText(c))
	return found && text == "" && len(grokMenu(c)) == 0
}

// grokHome is the host's HOME and Grok home ($GROK_HOME, else ~/.grok).
func grokHome(t CycleTarget) (home, grok string, err error) {
	home, err = t.tools.processEnvironment(int(t.HostPID), "HOME")
	if err != nil || home == "" {
		return "", "", errors.New("host home unknown")
	}
	// A failed read is not an unset variable: only a read that finds none falls back to ~/.grok.
	grok, err = t.tools.processEnvironment(int(t.HostPID), "GROK_HOME")
	if err != nil {
		return "", "", err
	}
	return home, GrokHome(grok, home), nil
}

// grokForeground proves the pane's foreground (agyForeground) and that the pinned host is still
// a Grok terminal session: grok-native, or the grok link, and not Grok's leader process
// (`grok-native agent leader`). An exec keeps the PID and start, so the command is read at each
// proof.
func grokForeground(ctx context.Context, t CycleTarget) error {
	if err := agyForeground(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return err
	}
	executable, args, err := t.tools.processCommand(int(t.HostPID))
	if err != nil {
		return errPaneUnproven
	}
	if name := filepath.Base(executable); name != "grok-native" && name != "grok" {
		return errPaneUnproven
	}
	if len(args) >= 3 && args[1] == "agent" && args[2] == "leader" {
		return errPaneUnproven
	}
	return nil
}

// grokSkillSettings is the [skills] table of Grok's config.toml.
type grokSkillSettings struct {
	Skills struct {
		Paths    []string `toml:"paths"`
		Ignore   []string `toml:"ignore"`
		Disabled []string `toml:"disabled"`
	} `toml:"skills"`
}

// grokPickupEntry checks the effective koinon-pickup entry in the host's Grok (N1). The
// working directory's .agents copy wins over every user copy, and among the user skills ~/.grok
// wins over ~/.agents, which wins over ~/.claude; the first of these that exists is the
// effective entry and must be the installed skill byte for byte. A later user skill is replaced
// and is not checked. A copy whose order is not verified (other project copies up to the
// repository root, .cursor, commands files, [skills] paths) may be the effective one, so it
// must be the skill too; without a verified copy, such a copy is the entry. An entry that
// [skills] disabled names is not listed at all. An ignored copy is not a candidate.
func grokPickupEntry(t CycleTarget) CycleProbe {
	home, grokDir, err := grokHome(t)
	if err != nil || !filepath.IsAbs(t.Directory) {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	var settings grokSkillSettings
	if data, err := readBounded(filepath.Join(grokDir, "config.toml"), 4<<20); err == nil {
		if _, err := toml.Decode(string(data), &settings); err != nil {
			return CycleProbe{Reason: "pickup_entry_unverified"}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	for _, name := range settings.Skills.Disabled {
		if name == PickupSkillName {
			return CycleProbe{Reason: "pickup_entry_disabled", Step: "remove " + PickupSkillName + " from [skills] disabled in " + filepath.Join(grokDir, "config.toml")}
		}
	}
	expand := func(p string) string {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			return filepath.Join(home, rest)
		}
		return filepath.Clean(p)
	}
	ignored := func(path string) bool {
		for _, p := range settings.Skills.Ignore {
			p = expand(p)
			if path == p || strings.HasPrefix(path, p+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	entry := func(root string) []string {
		return []string{filepath.Join(root, "skills", PickupSkillName, "SKILL.md"), filepath.Join(root, "commands", PickupSkillName+".md")}
	}
	var strict []string
	for dir, steps := filepath.Clean(t.Directory), 0; steps < 64; steps++ {
		for _, vendor := range []string{".grok", ".agents", ".claude", ".cursor"} {
			strict = append(strict, entry(filepath.Join(dir, vendor))...)
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir || dir == home {
			break
		}
		dir = filepath.Dir(dir)
	}
	winner := filepath.Join(filepath.Clean(t.Directory), ".agents", "skills", PickupSkillName, "SKILL.md")
	strict = append(strict, entry(filepath.Join(home, ".cursor"))...)
	for _, root := range []string{grokDir, filepath.Join(home, ".agents"), filepath.Join(home, ".claude")} {
		strict = append(strict, filepath.Join(root, "commands", PickupSkillName+".md"))
	}
	for _, p := range settings.Skills.Paths {
		p = expand(p)
		strict = append(strict, filepath.Join(p, PickupSkillName, "SKILL.md"), filepath.Join(p, "skills", PickupSkillName, "SKILL.md"))
		if filepath.Base(p) == "SKILL.md" && filepath.Base(filepath.Dir(p)) == PickupSkillName {
			strict = append(strict, p)
		}
	}
	present := map[string]bool{}
	for _, path := range strict {
		if ignored(path) {
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if pickupSkillReason(path, t.Pickup.Content) != "" {
			return CycleProbe{Reason: "pickup_entry_shadowed", Step: "remove or rename the skill " + path}
		}
		present[path] = true
	}
	if present[winner] {
		return CycleProbe{}
	}
	installed := filepath.Join(grokDir, "skills", PickupSkillName, "SKILL.md")
	for _, root := range []string{grokDir, filepath.Join(home, ".agents"), filepath.Join(home, ".claude")} {
		path := filepath.Join(root, "skills", PickupSkillName, "SKILL.md")
		if ignored(path) {
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if reason := pickupSkillReason(path, t.Pickup.Content); reason != "" {
			if path == installed {
				return CycleProbe{Reason: reason, Step: pickupSkillStep(path)}
			}
			return CycleProbe{Reason: "pickup_entry_shadowed", Step: "remove or rename the skill " + path}
		}
		return CycleProbe{}
	}
	if len(present) > 0 {
		return CycleProbe{}
	}
	return CycleProbe{Reason: "pickup_entry_missing", Step: pickupSkillStep(installed)}
}

// grokStatusLineStep names the command that sets the status line up.
const grokStatusLineStep = "run koinon setup grok to set up the Grok status line"

// grokStatusLine checks that the host's Grok config runs Koinon's grok-status hook as its
// [ui.status_line] command, the one that setup recorded as enabled; the witness and the
// context readings come from it.
func grokStatusLine(t CycleTarget, grokDir string) CycleProbe {
	unverified := CycleProbe{Reason: "statusline_unverified", Step: grokStatusLineStep}
	if t.stateRoot == "" {
		return unverified
	}
	data, err := readBounded(filepath.Join(t.stateRoot, "grok-statusline.json"), 4<<20)
	var record agyStatusLineRecord
	path := filepath.Join(grokDir, "config.toml")
	if err != nil || json.Unmarshal(data, &record) != nil || record.State != "enabled" || filepath.Clean(record.SettingsFile) != path {
		return unverified
	}
	config, err := readBounded(path, 4<<20)
	var value struct {
		UI struct {
			StatusLine map[string]any `toml:"status_line"`
		} `toml:"ui"`
	}
	var wrapper struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if err != nil || json.Unmarshal(record.Wrapper, &wrapper) != nil || wrapper.Type != "command" ||
		!strings.Contains(wrapper.Command, " hook grok-status") {
		return unverified
	}
	if _, err := toml.Decode(string(config), &value); err != nil ||
		value.UI.StatusLine["type"] != wrapper.Type || value.UI.StatusLine["command"] != wrapper.Command {
		return unverified
	}
	return CycleProbe{}
}

// Probe decides whether the session's cycle can run: an evidenced platform, the pane, the
// participant address that the session after /new takes over, the session itself as the
// latest observed epoch, the status line and the pickup entry.
func (grokCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !grokEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	_, grokDir, err := grokHome(t)
	if err != nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := grokForeground(ctx, t); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	if !t.Holds {
		return CycleProbe{Reason: "participant_unheld"}
	}
	// Before the reset, the status line's latest reading must name this session.
	if t.SuccessorEpoch == "" && t.observedEpoch != t.SessionID {
		return CycleProbe{Reason: "epoch_unverified"}
	}
	if probe := grokStatusLine(t, grokDir); probe.Reason != "" {
		return probe
	}
	if probe := grokPickupEntry(t); probe.Reason != "" {
		return probe
	}
	return CycleProbe{Entry: PickupSkillName}
}

// Inspect reports whether the pane is ready for input. A draft is reported and kept.
func (grokCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if err := grokForeground(ctx, t); err != nil {
		return PaneState{}, proofError(err, errAgyUnproven)
	}
	screen, err := readAgy(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	switch seen := grokNotReady(t, screen); seen {
	case "native_dialog":
		return PaneState{Prompt: seen}, nil
	case "":
		return PaneState{Idle: true}, nil
	default:
		return PaneState{Seen: seen}, nil
	}
}

// awaitGrok reads the pane until ok holds or the key wait ends.
func awaitGrok(ctx context.Context, t CycleTarget, ok func(codexScreen) bool) bool {
	deadline := time.Now().Add(grokKeyWait)
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

// keyIf is agy's keyIf with the Grok host proof (grokForeground) before every key.
func (grokCycle) keyIf(ctx context.Context, t CycleTarget, fence CycleFence, check func(codexScreen) bool, refused error, args ...string) error {
	return provenKeyIf(ctx, t, fence, func() error { return grokForeground(ctx, t) }, check, refused, args...)
}

// erase removes text that the adapter typed: C-u clears the whole composer (N3), so it is sent
// only while a fresh read shows exactly that text; then an empty composer is read back.
func (g grokCycle) erase(ctx context.Context, t CycleTarget, fence CycleFence, text string) error {
	typed := func(s codexScreen) bool {
		composer, _, found := grokComposer(grokText(s))
		return found && composer == text
	}
	if err := g.keyIf(ctx, t, fence, typed, errAgyErase, "C-u"); err != nil {
		return err
	}
	if !awaitGrok(ctx, t, grokEmpty) {
		return errAgyErase
	}
	return nil
}

// command types a slash command while a fresh read shows the pane ready, and submits it with
// one Enter while the composer holds exactly it and its menu selects its one row (match accepts
// that row). done compares the screen read before the Enter with later reads, so only a fresh
// effect is a receipt. Typed text that is not submitted is erased again; a draft that someone
// started is never touched.
func (g grokCycle) command(ctx context.Context, t CycleTarget, fence CycleFence, text string, match func(grokRow) bool,
	done func(before, after codexScreen) bool) (bool, error) {
	ready := func(s codexScreen) bool { return grokNotReady(t, s) == "" }
	if err := g.keyIf(ctx, t, fence, ready, errAgyNotReady, "-l", text); err != nil {
		return false, err
	}
	typed := func(s codexScreen) bool { return grokMenuReady(s, text, match) }
	if !awaitGrok(ctx, t, typed) {
		// erase succeeds only on the typed text: then the menu row was what failed.
		if err := g.erase(ctx, t, fence, text); err == nil && match != nil {
			return false, errAgyMenuEntry
		}
		return false, errAgyTyped
	}
	var before codexScreen
	check := func(s codexScreen) bool {
		before = s
		return typed(s)
	}
	if err := g.keyIf(ctx, t, fence, check, errAgyTyped, "Enter"); err != nil {
		g.erase(ctx, t, fence, text)
		return false, err
	}
	if !awaitGrok(ctx, t, func(s codexScreen) bool { return done(before, s) }) {
		return false, errAgySubmit
	}
	return true, nil
}

// Clear enters /new and reads back the notice of a session other than the epoch and other than
// any that the screen named before the Enter (N5); that is its receipt. The new session is
// proven separately, by NewEpoch.
func (g grokCycle) Clear(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	newRow := func(row grokRow) bool { return row.description == "Start a new session" }
	return g.command(ctx, t, fence, "/new", newRow, func(before, after codexScreen) bool {
		session := grokSession(after)
		return grokEmpty(after) && session != "" && session != t.Epoch && session != grokSession(before)
	})
}

// NewEpoch returns the session that the host's status line reported after the clear intent,
// other than the cycle's own epoch, as for agy.
func (grokCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	return agyCycle{}.NewEpoch(ctx, t)
}

// grokPickupRow accepts the menu row whose description is the canonical skill's, as far as the
// pane shows it.
func grokPickupRow(want PickupEntry) func(grokRow) bool {
	return func(row grokRow) bool {
		shown := strings.TrimSpace(row.description)
		return want.Description != "" && len(shown) >= 16 && strings.HasPrefix(want.Description, shown)
	}
}

// Pickup types /koinon-pickup and submits it only when the effective entry is still the
// installed skill (grokPickupEntry, read again) and the menu selects its one row with the
// canonical description. The receipt is one more submitted /koinon-pickup line than the screen
// showed before the Enter, above an empty composer (N7).
func (g grokCycle) Pickup(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	if probe := grokPickupEntry(t); probe.Reason != "" {
		return false, errors.New(probe.Reason)
	}
	return g.command(ctx, t, fence, grokPickupCommand, grokPickupRow(t.Pickup), func(before, after codexScreen) bool {
		composer, _, found := grokComposer(grokText(after))
		return found && composer == "" && len(grokMenu(after)) == 0 &&
			grokPrompts(after, grokPickupCommand) > grokPrompts(before, grokPickupCommand)
	})
}

// Answerable is false: every Grok dialog waits for the native session or the maintainer.
func (grokCycle) Answerable(string) bool { return false }

func (grokCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

// Dialog reads the approval dialog the pane shows. It sends nothing.
func (grokCycle) Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error) {
	if err := grokForeground(ctx, t); err != nil {
		return CycleDialog{}, proofError(err, errAgyUnproven)
	}
	screen, err := readAgy(ctx, t)
	if err != nil {
		return CycleDialog{}, err
	}
	approval := grokDialog(screen)
	if approval.dialog.Digest == "" {
		return CycleDialog{}, errors.New("no dialog")
	}
	return approval.dialog, nil
}

// Choose enters one visible option while a fresh read shows the same dialog. In Grok the digit
// answers the dialog at once (N8): no Enter follows, and nothing is selected first. The receipt
// is that dialog gone.
func (g grokCycle) Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	same := func(s codexScreen) bool {
		approval := grokDialog(s)
		return approval.dialog.Digest == digest && approval.option(option)
	}
	if err := g.keyIf(ctx, t, fence, same, ErrCycleDialogChanged, "-l", option); err != nil {
		return false, err
	}
	if !awaitGrok(ctx, t, func(s codexScreen) bool { return grokDialog(s).dialog.Digest != digest }) {
		return false, errGrokChoice
	}
	return true, nil
}
