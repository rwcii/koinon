package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// The OpenCode cycle adapter (#513). It acts through the session's own loopback server, with
// the launch password, and reads the pinned pane without input. The reset creates a new
// session (POST /session) and shows it in the TUI (POST /tui/select-session); the pickup runs
// the koinon-pickup skill command in that session (POST /session/{id}/command), with a message
// ID that the adapter derives and the requester's exact model. The host's API proves the
// created session and the pickup's receipt. The TUI gives no read-back of the session it shows,
// so the pane proves it separately: its current view shows the successor's Koinon tool-call
// rows, which the identity plugin marks with the calling session's ID, and no other session's.
// The facts it relies on are recorded in the sprint's native evidence (opencode 1.18.35 on
// Linux).

// openCodeCycleVersion is the adapter's version, stored with each cycle.
const openCodeCycleVersion = "opencode-server-1"

// openCodeEvidencedPlatforms are the operating systems with native evidence for this adapter.
var openCodeEvidencedPlatforms = map[string]bool{"linux": true}

var (
	// openCodeCommandTimeout ends the pickup request: OpenCode answers it only after the whole
	// turn, which goes on without it (native evidence S8).
	openCodeCommandTimeout = 5 * time.Second
	// openCodeRequestTimeout bounds each other request.
	openCodeRequestTimeout = 2 * time.Second
	// openCodeReplyWait bounds the read-back of a permission reply.
	openCodeReplyWait = 3 * time.Second
)

// Fixed reasons of an OpenCode input step that did not complete; the cycle records them.
var (
	errOpenCodeUnproven = errors.New("pane_unproven")
	errOpenCodeNotReady = errors.New("pane_not_ready")
	errOpenCodeServer   = errors.New("server_unavailable")
	errOpenCodeModel    = errors.New("model_unavailable")
	errOpenCodeAsync    = errors.New("pickup_async")
)

type openCodeCycle struct{}

func (openCodeCycle) Version() string { return openCodeCycleVersion }

// openCodeTitle is the created session's title: the cycle's own, for correlation only.
func openCodeTitle(cycleID string) string { return "koinon-cycle-" + cycleID }

var openCodeSessionID = regexp.MustCompile(`^ses_[0-9A-Za-z]{1,64}$`)

// openCodeCall sends one request to the launched server and decodes an exact-200 JSON response
// of at most openCodeResponseBytes into value.
func openCodeCall(ctx context.Context, target LaunchTarget, method, path string, body, value any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return errOpenCodeServer
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://"+target.Address+path, reader)
	if err != nil {
		return errOpenCodeServer
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.SetBasicAuth("opencode", target.Password)
	client, close := httpClient()
	defer close()
	response, err := client.Do(request)
	if err != nil {
		return errOpenCodeServer
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errOpenCodeServer
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openCodeResponseBytes+1))
	if err != nil || len(data) > openCodeResponseBytes || json.Unmarshal(data, value) != nil {
		return errOpenCodeServer
	}
	return nil
}

func openCodeRead(ctx context.Context, t CycleTarget, path string, value any) error {
	if t.openCode == nil {
		return errOpenCodeServer
	}
	return openCodeCall(ctx, *t.openCode, http.MethodGet, path, nil, value, openCodeRequestTimeout)
}

func sessionPath(id string) string { return "/session/" + url.PathEscape(id) }

// openCodePair is a provider and model as OpenCode names them.
type openCodePair struct{ provider, model string }

// openCodeRequesterPair is the provider and model of the requester's newest finished assistant
// message, which the pickup asks for. A pair that the command's "provider/model" string would
// not give back unchanged, split at its first "/", is unavailable (native evidence S9).
func openCodeRequesterPair(ctx context.Context, t CycleTarget) (openCodePair, error) {
	var messages []openCodeMessage
	if err := openCodeRead(ctx, t, sessionPath(t.SessionID)+"/message?limit="+fmt.Sprint(openCodeMessagePage), &messages); err != nil {
		return openCodePair{}, err
	}
	newest, ties := -1, 0
	for i, m := range messages {
		completed := m.Info.Time.Completed
		if m.Info.Role != "assistant" || m.Info.Finish == "" || !completed.ok || completed.value <= 0 {
			continue
		}
		switch {
		case newest < 0 || completed.value > messages[newest].Info.Time.Completed.value:
			newest, ties = i, 0
		case completed.value == messages[newest].Info.Time.Completed.value:
			ties++
		}
	}
	if newest < 0 || ties > 0 {
		return openCodePair{}, errOpenCodeModel
	}
	info := messages[newest].Info
	if info.ProviderID == "" || info.ModelID == "" || strings.Contains(info.ProviderID, "/") || !modelID.MatchString(info.ProviderID+"/"+info.ModelID) {
		return openCodePair{}, errOpenCodeModel
	}
	return openCodePair{info.ProviderID, info.ModelID}, nil
}

// openCodeCommand is the allowlisted part of one entry of GET /command.
type openCodeCommand struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Template string `json:"template"`
}

// openCodeTemplateHead and openCodeTemplateTail surround the skill's base directory in the
// template that OpenCode makes from a skill's body (native evidence N2).
const (
	openCodeTemplateHead = "\n\nBase directory for this skill: "
	openCodeTemplateTail = "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
)

// pickupSkillBody is the canonical skill's body after its front matter.
func pickupSkillBody(content []byte) (string, bool) {
	rest, ok := strings.CutPrefix(string(content), "---\n")
	if !ok {
		return "", false
	}
	_, body, ok := strings.Cut(rest, "\n---\n")
	return body, ok
}

// openCodeSkillRoots are the skill directories that OpenCode reads for a session: its own
// configuration root and the person's ~/.claude and ~/.agents, then the same three in the
// session's directory.
func openCodeSkillRoots(t CycleTarget) ([]string, error) {
	if t.tools.processEnvironment == nil {
		return nil, errOpenCodeServer
	}
	home, err := t.tools.processEnvironment(int(t.HostPID), "HOME")
	if err != nil || !filepath.IsAbs(home) {
		return nil, errOpenCodeServer
	}
	xdg, _ := t.tools.processEnvironment(int(t.HostPID), "XDG_CONFIG_HOME")
	if xdg != "" && !filepath.IsAbs(xdg) {
		xdg = ""
	}
	roots := []string{filepath.Join(OpenCodeConfigRoot(xdg, home), "skills"), filepath.Join(home, ".claude", "skills"), filepath.Join(home, ".agents", "skills")}
	if filepath.IsAbs(t.Directory) {
		for _, root := range []string{".opencode", ".claude", ".agents"} {
			roots = append(roots, filepath.Join(t.Directory, root, "skills"))
		}
	}
	return roots, nil
}

// openCodePickupEntry checks the effective koinon-pickup command: exactly one skill command of
// that name, whose template is the canonical body with the base directory of a skill root that
// OpenCode reads, and whose file there is the binary's skill byte for byte. It returns that
// template. An equal copy in another root is allowed; a copy that differs never runs.
func openCodePickupEntry(ctx context.Context, t CycleTarget) (string, CycleProbe) {
	unverified := CycleProbe{Reason: "pickup_entry_unverified"}
	body, ok := pickupSkillBody(t.Pickup.Content)
	if !ok {
		return "", unverified
	}
	roots, err := openCodeSkillRoots(t)
	if err != nil {
		return "", unverified
	}
	var commands []openCodeCommand
	if err := openCodeRead(ctx, t, "/command", &commands); err != nil {
		return "", unverified
	}
	var entry *openCodeCommand
	for i := range commands {
		if commands[i].Name != PickupSkillName {
			continue
		}
		if entry != nil {
			return "", unverified
		}
		entry = &commands[i]
	}
	if entry == nil {
		path := filepath.Join(roots[0], PickupSkillName, "SKILL.md")
		return "", CycleProbe{Reason: "pickup_entry_missing", Step: pickupSkillStep(path)}
	}
	dir, ok := strings.CutPrefix(entry.Template, body+openCodeTemplateHead)
	if dir, ok = strings.CutSuffix(dir, openCodeTemplateTail); entry.Source != "skill" || !ok {
		return "", CycleProbe{Reason: "pickup_entry_differs", Step: pickupSkillStep(filepath.Join(roots[0], PickupSkillName, "SKILL.md"))}
	}
	for _, root := range roots {
		if dir != filepath.Join(root, PickupSkillName) {
			continue
		}
		path := filepath.Join(dir, "SKILL.md")
		if reason := pickupSkillReason(path, t.Pickup.Content); reason != "" {
			return "", CycleProbe{Reason: reason, Step: pickupSkillStep(path)}
		}
		return entry.Template, CycleProbe{}
	}
	return "", unverified
}

// Probe decides whether the session's cycle can run: an evidenced platform, the launched
// server, the pane, the participant address that the created session takes over, the model
// that the pickup asks for and the pickup entry.
func (openCodeCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !openCodeEvidencedPlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	if t.openCode == nil {
		return CycleProbe{Reason: "native_registry_unavailable"}
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return CycleProbe{Reason: tmuxReason(err, "pane_unproven")}
	}
	if !t.Holds {
		return CycleProbe{Reason: "participant_unheld"}
	}
	if _, err := openCodeRequesterPair(ctx, t); err != nil {
		return CycleProbe{Reason: errOpenCodeModel.Error()}
	}
	if _, probe := openCodePickupEntry(ctx, t); probe.Reason != "" {
		return probe
	}
	return CycleProbe{Entry: PickupSkillName}
}

// sgrRun is a stretch of a captured row in one foreground color ("" for the default).
type sgrRun struct{ fg, text string }

// sgrRuns splits a captured row into its foreground runs.
func sgrRuns(line string) []sgrRun {
	var runs []sgrRun
	fg := ""
	add := func(text string) {
		if n := len(runs); n > 0 && runs[n-1].fg == fg {
			runs[n-1].text += text
			return
		}
		runs = append(runs, sgrRun{fg, text})
	}
	for i := 0; i < len(line); {
		if line[i] == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			end := i + 2
			for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
				end++
			}
			if end < len(line) && line[end] == 'm' {
				fg = sgrForeground(line[i+2:end], fg)
			}
			i = end + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(line[i:])
		add(line[i : i+size])
		i += size
	}
	return runs
}

// sgrForeground applies one SGR parameter list to the foreground color fg.
func sgrForeground(params, fg string) string {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch f := fields[i]; {
		case f == "" || f == "0" || f == "39":
			fg = ""
		case f == "38" && i+1 < len(fields) && fields[i+1] == "2" && i+4 < len(fields):
			fg = strings.Join(fields[i:i+5], ";")
			i += 4
		case f == "38" && i+1 < len(fields) && fields[i+1] == "5" && i+2 < len(fields):
			fg = strings.Join(fields[i:i+3], ";")
			i += 2
		case len(f) == 2 && (f[0] == '3' || f[0] == '9') && f[1] >= '0' && f[1] <= '7':
			fg = f
		case f == "48" && i+1 < len(fields) && fields[i+1] == "2":
			i += 4
		case f == "48" && i+1 < len(fields) && fields[i+1] == "5":
			i += 2
		}
	}
	return fg
}

// openCodeView is the current view of an OpenCode session screen: the rows above the composer,
// raw and as plain text cut to the message column, and the composer's text.
type openCodeView struct {
	raw, above []string
	text       string
}

// readOpenCodeView reads a session screen. The composer is the last block of "┃" rows at the
// left margin above its "╹▀" rule; its last row names the agent, and the rule's width is the
// message column's, left of the sidebar. The home screen's centered composer is no session
// view.
func readOpenCodeView(screen string) (openCodeView, bool) {
	c := readCodexScreen(screen)
	rule := -1
	for i := len(c.plain) - 1; i >= 0; i-- {
		if strings.HasPrefix(c.plain[i], "  ╹▀") {
			rule = i
			break
		}
	}
	if rule < 2 {
		return openCodeView{}, false
	}
	width := 3
	for r := []rune(c.plain[rule]); width < len(r) && r[width] == '▀'; width++ {
	}
	cut := func(line string) string {
		if r := []rune(line); len(r) > width {
			return string(r[:width])
		}
		return line
	}
	agent := rule - 1
	if !strings.HasPrefix(c.plain[agent], "  ┃  ") || strings.TrimSpace(strings.TrimPrefix(cut(c.plain[agent]), "  ┃")) == "" {
		return openCodeView{}, false
	}
	top := agent
	for top > 0 && strings.HasPrefix(c.plain[top-1], "  ┃") {
		top--
	}
	var parts []string
	for _, line := range c.plain[top:agent] {
		if line = strings.TrimSpace(strings.TrimPrefix(cut(line), "  ┃")); line != "" {
			parts = append(parts, line)
		}
	}
	v := openCodeView{raw: c.raw[:top], text: strings.Join(parts, "\n")}
	for _, line := range c.plain[:top] {
		v.above = append(v.above, cut(line))
	}
	return v, true
}

// empty reports a view with nothing above the composer: a session without messages.
func (v openCodeView) empty() bool {
	for _, line := range v.above {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
}

var (
	openCodeToolHead = regexp.MustCompile(`^koinon_[a-z][a-z_]{0,63} \[`)
	openCodeToolID   = regexp.MustCompile(`(?:\[|, )koinon_session=(ses_[0-9A-Za-z]{1,64})\]$`)
)

// sessions returns the session IDs of the view's Koinon tool-call rows, in the evidenced
// rendering (native evidence S10): at a five-space indent, the ⚙ glyph alone in one color, a
// space in another, then "koinon_<tool> [<arguments>]" in the glyph's color. Long arguments
// wrap onto rows at a seven-space indent in the same color. The plugin's koinon_session
// argument is the last one. A user or tool-output row ("┃"), and a reply in the text's own
// color, is no tool-call row.
func (v openCodeView) sessions() []string {
	var ids []string
	for i := 0; i < len(v.raw); i++ {
		runs := sgrRuns(v.raw[i])
		if len(runs) < 4 || strings.Trim(runs[0].text, " ") != "" || len(runs[0].text) != 5 ||
			runs[1].text != "⚙" || runs[1].fg == "" || runs[2].text != " " || runs[3].fg != runs[1].fg {
			continue
		}
		color, text := runs[1].fg, strings.TrimRight(runs[3].text, " ")
		if !openCodeToolHead.MatchString(text) {
			continue
		}
		for !strings.HasSuffix(text, "]") && i+1 < len(v.raw) {
			next := sgrRuns(v.raw[i+1])
			if len(next) < 2 || next[0].text != "       " || next[1].fg != color || strings.HasPrefix(next[1].text, " ") {
				break
			}
			i++
			text += " " + strings.TrimRight(next[1].text, " ")
		}
		if m := openCodeToolID.FindStringSubmatch(text); m != nil {
			ids = append(ids, m[1])
		}
	}
	return ids
}

// readOpenCodePane captures the pane's current view, with attributes and wrapped rows joined.
func readOpenCodePane(ctx context.Context, t CycleTarget) (openCodeView, bool, error) {
	out, err := t.tools.tmux(ctx, t.Socket, "capture-pane", "-p", "-e", "-J", "-t", t.Pane)
	if err != nil {
		return openCodeView{}, false, err
	}
	v, ok := readOpenCodeView(out)
	return v, ok, nil
}

// subject is the session that the cycle's next input concerns: the created successor from the
// proven reset on, else the requester.
func (t CycleTarget) subject() string {
	if t.SuccessorEpoch != "" && t.State != cycleClearIntent && t.State != cycleClearReceived {
		return t.SuccessorEpoch
	}
	return t.SessionID
}

// openCodeNotReady is the fixed code of what keeps the session from the cycle's next input, or
// "": a dialog of the subject session, no composer, a draft, the subject's running turn, and
// after the reset a successor that is not empty.
func openCodeNotReady(ctx context.Context, t CycleTarget, v openCodeView, ok bool) string {
	subject := t.subject()
	if dialog, err := openCodeDialog(ctx, t, subject); err != nil {
		return errOpenCodeServer.Error()
	} else if dialog.dialog.Digest != "" {
		return "native_dialog"
	}
	switch {
	case !ok:
		return "input_line_not_found"
	case v.text != "":
		return "input_not_empty"
	}
	statusCtx, cancel := context.WithTimeout(ctx, openCodeRequestTimeout)
	state, err := openCodeStatus(statusCtx, *t.openCode, subject)
	cancel()
	switch {
	case err != nil:
		return errOpenCodeServer.Error()
	case state != "idle":
		return "turn_running"
	}
	if subject == t.SuccessorEpoch {
		var messages []json.RawMessage
		if err := openCodeRead(ctx, t, sessionPath(subject)+"/message", &messages); err != nil {
			return errOpenCodeServer.Error()
		}
		if len(messages) > 0 || !v.empty() {
			return "successor_not_empty"
		}
	}
	return ""
}

// ready proves the pane and reads it again, without input, right before an effect.
func (openCodeCycle) ready(ctx context.Context, t CycleTarget) error {
	if t.openCode == nil {
		return errOpenCodeServer
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return proofError(err, errOpenCodeUnproven)
	}
	v, ok, err := readOpenCodePane(ctx, t)
	if err != nil {
		return err
	}
	if openCodeNotReady(ctx, t, v, ok) != "" {
		return errOpenCodeNotReady
	}
	return nil
}

// Inspect reports whether the session is ready for input. A draft is reported and kept.
func (openCodeCycle) Inspect(ctx context.Context, t CycleTarget, fence CycleFence) (PaneState, error) {
	if t.openCode == nil {
		return PaneState{}, errOpenCodeServer
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return PaneState{}, proofError(err, errOpenCodeUnproven)
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
	v, ok, err := readOpenCodePane(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	switch seen := openCodeNotReady(ctx, t, v, ok); seen {
	case "native_dialog":
		return PaneState{Prompt: seen}, nil
	case "":
		return PaneState{Idle: true}, nil
	default:
		return PaneState{Seen: seen}, nil
	}
}

// Clear is not used: the reset creates a session (Create).
func (openCodeCycle) Clear(context.Context, CycleTarget, CycleFence) (bool, error) {
	return false, errOpenCodeAsync
}

// Create creates the successor session with the cycle's title, after a fresh read shows the
// requester ready, and returns its ID.
func (a openCodeCycle) Create(ctx context.Context, t CycleTarget, fence CycleFence) (string, error) {
	if err := a.ready(ctx, t); err != nil {
		return "", err
	}
	if err := fence(ctx); err != nil {
		return "", err
	}
	var created struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	title := openCodeTitle(t.CycleID)
	if err := openCodeCall(ctx, *t.openCode, http.MethodPost, "/session", map[string]string{"title": title}, &created, openCodeRequestTimeout); err != nil {
		return "", err
	}
	if !openCodeSessionID.MatchString(created.ID) || created.Title != title {
		return "", errOpenCodeServer
	}
	return created.ID, nil
}

// Select shows the created session in the TUI, after a fresh read shows the requester ready.
// OpenCode accepts it with true but gives no read-back (native evidence N1).
func (a openCodeCycle) Select(ctx context.Context, t CycleTarget, fence CycleFence) (bool, error) {
	if !openCodeSessionID.MatchString(t.SuccessorEpoch) {
		return false, errOpenCodeServer
	}
	if err := a.ready(ctx, t); err != nil {
		return false, err
	}
	if err := fence(ctx); err != nil {
		return false, err
	}
	var accepted bool
	if err := openCodeCall(ctx, *t.openCode, http.MethodPost, "/tui/select-session", map[string]string{"sessionID": t.SuccessorEpoch}, &accepted, openCodeRequestTimeout); err != nil {
		return false, err
	}
	return accepted, nil
}

// NewEpoch returns the recorded created session while the server shows it with the cycle's
// title and no messages, and the pane shows an empty session view with an empty composer. That
// proves the creation, not that the TUI shows that session: another empty session looks the
// same, so the pickup report needs the pane's own proof (PickupDisplay).
func (openCodeCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	if t.SuccessorEpoch == "" || t.openCode == nil {
		return "", nil
	}
	var session struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	var messages []json.RawMessage
	if err := openCodeRead(ctx, t, sessionPath(t.SuccessorEpoch), &session); err != nil {
		return "", err
	}
	if err := openCodeRead(ctx, t, sessionPath(t.SuccessorEpoch)+"/message", &messages); err != nil {
		return "", err
	}
	if session.ID != t.SuccessorEpoch || session.Title != openCodeTitle(t.CycleID) || len(messages) > 0 {
		return "", nil
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return "", err
	}
	v, ok, err := readOpenCodePane(ctx, t)
	if err != nil || !ok || v.text != "" || !v.empty() {
		return "", err
	}
	return t.SuccessorEpoch, nil
}

// Pickup is not used: the pickup is sent asynchronously (SendPickup).
func (openCodeCycle) Pickup(context.Context, CycleTarget, CycleFence) (bool, error) {
	return false, errOpenCodeAsync
}

// openCodeMessageID is the pickup's message ID. OpenCode orders a session's messages by ID, and
// its own IDs start with 12 hex digits of the creation time in milliseconds times 4096; this one
// starts with the pickup intent's time the same way, then 14 base-62 digits of the cycle and the
// successor. A restarted daemon derives the same ID.
func openCodeMessageID(t CycleTarget) string {
	sum := sha256.Sum256([]byte(t.CycleID + "\x00" + t.SuccessorEpoch))
	digits := new(big.Int).SetBytes(sum[:]).Text(62)
	for len(digits) < 14 {
		digits = "0" + digits
	}
	return fmt.Sprintf("msg_%012x%s", (uint64(t.pickupAt)*4096)&(1<<48-1), digits[:14])
}

// SendPickup sends the koinon-pickup command once, in the created session, with the derived
// message ID and the requester's model, after the entry, the model and a fresh read of the pane
// and the session pass, and after the fence. It returns when the request returns or its timeout
// ends it.
func (a openCodeCycle) SendPickup(ctx context.Context, t CycleTarget, fence CycleFence) error {
	if t.pickupAt <= 0 {
		return errOpenCodeServer
	}
	pair, err := openCodeRequesterPair(ctx, t)
	if err != nil {
		return err
	}
	if _, probe := openCodePickupEntry(ctx, t); probe.Reason != "" {
		return errors.New(probe.Reason)
	}
	if err := a.ready(ctx, t); err != nil {
		return err
	}
	if err := fence(ctx); err != nil {
		return err
	}
	body := map[string]string{"command": PickupSkillName, "arguments": "", "messageID": openCodeMessageID(t),
		"model": pair.provider + "/" + pair.model}
	var ignored json.RawMessage
	return openCodeCall(ctx, *t.openCode, http.MethodPost, sessionPath(t.SuccessorEpoch)+"/command", body, &ignored, openCodeCommandTimeout)
}

// openCodeUserMessage is the allowlisted part of one message for the receipt; part texts are
// compared and never kept.
type openCodeUserMessage struct {
	Info struct {
		ID    string `json:"id"`
		Role  string `json:"role"`
		Model *struct {
			ProviderID string `json:"providerID"`
			ModelID    string `json:"modelID"`
		} `json:"model"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

// PickupReceipt reads the created session back: its only user message must be the pickup, with
// the derived ID, the requester's model and, as its one text part, the effective entry's
// template as OpenCode enters it (trimmed).
func (openCodeCycle) PickupReceipt(ctx context.Context, t CycleTarget) (bool, error) {
	if t.pickupAt <= 0 || t.SuccessorEpoch == "" {
		return false, nil
	}
	pair, err := openCodeRequesterPair(ctx, t)
	if err != nil {
		return false, err
	}
	template, probe := openCodePickupEntry(ctx, t)
	if probe.Reason != "" {
		return false, errors.New(probe.Reason)
	}
	var messages []openCodeUserMessage
	if err := openCodeRead(ctx, t, sessionPath(t.SuccessorEpoch)+"/message", &messages); err != nil {
		return false, err
	}
	var user []openCodeUserMessage
	for _, m := range messages {
		if m.Info.Role == "user" {
			user = append(user, m)
		}
	}
	if len(user) != 1 {
		return false, nil
	}
	m := user[0]
	var texts []string
	for _, p := range m.Parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return m.Info.ID == openCodeMessageID(t) && m.Info.Model != nil && m.Info.Model.ProviderID == pair.provider &&
		m.Info.Model.ModelID == pair.model && len(texts) == 1 && texts[0] == strings.TrimSpace(template), nil
}

// PickupDisplay reads the pane's current view: shown when a Koinon tool-call row names the
// successor, foreign when one names another session.
func (openCodeCycle) PickupDisplay(ctx context.Context, t CycleTarget) (bool, bool, error) {
	if t.SuccessorEpoch == "" {
		return false, false, errOpenCodeUnproven
	}
	if err := provePane(ctx, t.tools, t.HostPID, t.HostStart, t.Socket, t.Pane); err != nil {
		return false, false, err
	}
	v, ok, err := readOpenCodePane(ctx, t)
	if err != nil || !ok {
		return false, false, errOpenCodeUnproven
	}
	shown, foreign := false, false
	for _, id := range v.sessions() {
		shown = shown || id == t.SuccessorEpoch
		foreign = foreign || id != t.SuccessorEpoch
	}
	return shown, foreign, nil
}

// openCodePending is the allowlisted part of one pending permission or question.
type openCodePending struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Permission string   `json:"permission"`
	Patterns   []string `json:"patterns"`
	Questions  []struct {
		Question string `json:"question"`
	} `json:"questions"`
}

// openCodeReplies are a permission's answers by dashboard option.
var openCodeReplies = map[string]string{"1": "once", "2": "always", "3": "reject"}

// openCodeApproval is a pending request of the subject session: its dialog, and the permission
// that a dashboard option answers ("" for a question, which waits for the pane).
type openCodeApproval struct {
	dialog     CycleDialog
	permission string
}

// maxOpenCodeDialogLines bounds the lines of one dialog; each line is cut at 512 bytes.
const maxOpenCodeDialogLines = 16

// openCodeDialog reads the oldest pending permission, else question, of exactly the session.
func openCodeDialog(ctx context.Context, t CycleTarget, session string) (openCodeApproval, error) {
	line := func(s string) string {
		if len(s) > 512 {
			s = strings.ToValidUTF8(s[:512], "")
		}
		return strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return ' '
			}
			return r
		}, s)
	}
	for _, path := range []string{"/permission", "/question"} {
		var pending []openCodePending
		if err := openCodeRead(ctx, t, path, &pending); err != nil {
			return openCodeApproval{}, err
		}
		for _, p := range pending {
			if p.SessionID != session || p.ID == "" {
				continue
			}
			var a openCodeApproval
			if path == "/permission" {
				a.permission = p.ID
				a.dialog.Lines = append(a.dialog.Lines, line("OpenCode asks permission: "+p.Permission))
				for _, pattern := range p.Patterns {
					a.dialog.Lines = append(a.dialog.Lines, line(pattern))
				}
				a.dialog.Lines = a.dialog.Lines[:min(len(a.dialog.Lines), maxOpenCodeDialogLines-3)]
				a.dialog.Lines = append(a.dialog.Lines, "1. Allow once", "2. Allow always", "3. Reject")
				a.dialog.Options = []string{"1", "2", "3"}
			} else {
				a.dialog.Lines = append(a.dialog.Lines, "OpenCode asks a question; answer it in the pane")
				for _, q := range p.Questions {
					a.dialog.Lines = append(a.dialog.Lines, line(q.Question))
				}
				a.dialog.Lines = a.dialog.Lines[:min(len(a.dialog.Lines), maxOpenCodeDialogLines)]
				a.dialog.Options = []string{}
			}
			sum := sha256.Sum256([]byte(p.ID + "\x00" + strings.Join(a.dialog.Lines, "\n")))
			a.dialog.Digest = hex.EncodeToString(sum[:])
			return a, nil
		}
	}
	return openCodeApproval{}, nil
}

// Answerable is false: every OpenCode dialog waits for the session's settings or the maintainer.
func (openCodeCycle) Answerable(string) bool { return false }

func (openCodeCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

// Dialog reads the subject session's pending permission or question. It sends nothing.
func (openCodeCycle) Dialog(ctx context.Context, t CycleTarget) (CycleDialog, error) {
	if t.openCode == nil {
		return CycleDialog{}, errOpenCodeServer
	}
	a, err := openCodeDialog(ctx, t, t.subject())
	if err != nil {
		return CycleDialog{}, err
	}
	if a.dialog.Digest == "" {
		return CycleDialog{}, errors.New("no dialog")
	}
	return a.dialog, nil
}

// Choose answers the subject session's permission with the maintainer's option while it is still
// the same request, and reads back that it is gone. A question is answered in the pane.
func (openCodeCycle) Choose(ctx context.Context, t CycleTarget, digest, option string, fence CycleFence) (bool, error) {
	if t.openCode == nil {
		return false, errOpenCodeServer
	}
	a, err := openCodeDialog(ctx, t, t.subject())
	reply, known := openCodeReplies[option]
	if err != nil || a.dialog.Digest != digest || a.permission == "" || !known {
		return false, ErrCycleDialogChanged
	}
	if err := fence(ctx); err != nil {
		return false, err
	}
	var accepted bool
	if err := openCodeCall(ctx, *t.openCode, http.MethodPost, "/permission/"+url.PathEscape(a.permission)+"/reply",
		map[string]string{"reply": reply}, &accepted, openCodeRequestTimeout); err != nil {
		return false, err
	}
	deadline := time.Now().Add(openCodeReplyWait)
	for {
		if now, err := openCodeDialog(ctx, t, t.subject()); err == nil && now.permission != a.permission {
			return true, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
