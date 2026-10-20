package mcp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// Terminal naming (#156). After a registration or renewal, this server names its session's
// own tmux session after the session's published name: its alias while it holds one, else
// its peer name. A sub-agent never names, nor does a non-holder while another session of its
// host holds a participant address. TMUX and TMUX_PANE only select the server and a candidate pane; the pane is
// the session's own only when its process is the session's host process or an ancestor of
// it, with no other agent process between them. The session and the pane are targeted by
// ID, never by name.

const (
	tmuxTimeout = time.Second
	// maxPaneProcesses bounds the scan of another pane's process tree.
	maxPaneProcesses = 512
	maxAncestors     = 256
)

var tmuxName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// Results of one naming attempt. A result marked final is not tried again until the
// published name changes; the others, such as name_taken and attach_pane_not_found, are
// tried again at the next renewal.
var namingFinal = map[string]bool{
	"renamed": true, "unchanged": true, "pane_titled": true, "attach_pane_ambiguous": true,
	"pane_not_host": true, "nested_agent": true, "invalid_name": true, "not_in_tmux": true,
}

// namingResult is the last naming attempt of a session: its result, the published name it
// was for, and when it ended.
type namingResult struct {
	result, target string
	at             int64
}

// tmuxFields is a tmux format of the named fields, separated by spaces. Every field that
// Koinon reads this way is an ID ($N, %N) or a process ID, which holds no space; tmux before
// 3.4 prints a tab in a format as "_", so a tab cannot separate them.
func tmuxFields(names ...string) string {
	formats := make([]string, len(names))
	for i, name := range names {
		formats[i] = "#{" + name + "}"
	}
	return strings.Join(formats, " ")
}

// RunTmux runs one tmux command on the server at socket. tmux is resolved on PATH, as the
// agent's own terminal would.
func RunTmux(ctx context.Context, socket string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).Output()
	return string(out), err
}

// LookTmux is the absolute path of the tmux that RunTmux runs: tmux on this server's PATH,
// which is the agent's own. A tmux found through a relative PATH entry is not reported.
func LookTmux() (string, error) {
	path, err := exec.LookPath("tmux")
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// published is the name the session's terminal takes.
func published(session core.Session) string {
	if session.Alias != "" {
		return session.Alias
	}
	return session.Name
}

// launchOf reads what naming needs from the launch record that the daemon recorded for a
// session: whether it registered with a launch, and whether that launch is a Claude
// background job.
func launchOf(session core.Session) (launched, background bool) {
	var target struct {
		LaunchID   string `json:"launch_id"`
		Background bool   `json:"background"`
	}
	if json.Unmarshal(session.WakeTarget, &target) != nil || target.LaunchID == "" {
		return false, false
	}
	return true, target.Background
}

// hostPID is the session's host process from the wake target the daemon recorded: the
// Claude process of a Claude session, the launched CLI of a launched session.
func hostPID(session core.Session) int {
	var target struct {
		ClaudePID int    `json:"claude_pid"`
		HostPID   int    `json:"host_pid"`
		LaunchID  string `json:"launch_id"`
	}
	if json.Unmarshal(session.WakeTarget, &target) != nil {
		return 0
	}
	if session.Family == "claude" {
		return target.ClaudePID
	}
	if target.LaunchID == "" {
		return 0
	}
	return target.HostPID
}

// nameAfterRegistration names the terminal of a session this server just registered or
// renewed, when the published name changed since the last final result. It never delays
// or fails the call that registered the session.
func (s *server) nameAfterRegistration(caller core.Key, session core.Session) {
	if s.c.Tmux == nil || s.c.Parents == nil || s.c.Command == nil {
		return
	}
	s.mu.Lock()
	// Only a session that registered with its launch record is named; a Claude session
	// also needs this server to be the child of its Claude process.
	launched, background := launchOf(session)
	attributed := launched && (caller.Family != "claude" || s.claude)
	// A sub-agent, and a non-holder whose host another session's held address names, share
	// that host's terminal and leave its name alone (#228).
	if session.Subagent || session.HostHolder && !session.HoldsAddress {
		attributed = false
	}
	target := published(session)
	if !attributed || target == "" || s.named[caller] == target || s.naming[caller] {
		s.mu.Unlock()
		return
	}
	s.naming[caller] = true
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var result string
		if background {
			result = s.nameAttached(ctx, session.ID, target)
		} else {
			result = s.nameTerminal(ctx, hostPID(session), target)
		}
		s.mu.Lock()
		delete(s.naming, caller)
		if namingFinal[result] {
			s.named[caller] = target
		}
		s.mu.Unlock()
		s.obs.mu.Lock()
		if s.obs.naming == nil {
			s.obs.naming = map[core.Key]namingResult{}
		}
		s.obs.naming[caller] = namingResult{result: result, target: target, at: s.c.Now().UnixMilli()}
		// The session name changed; read it again at the next observation.
		s.obs.checked = time.Time{}
		s.obs.mu.Unlock()
	}()
}

// nameTerminal applies the naming rules for one host process in the pane that TMUX and
// TMUX_PANE select and returns the result.
func (s *server) nameTerminal(ctx context.Context, host int, name string) string {
	socket, _, _ := strings.Cut(s.c.Getenv("TMUX"), ",")
	return s.namePane(ctx, socket, s.c.Getenv("TMUX_PANE"), host, name)
}

// namePane applies the naming rules for host in the candidate pane of the server at socket.
func (s *server) namePane(ctx context.Context, socket, pane string, host int, name string) string {
	if !tmuxName.MatchString(name) {
		return "invalid_name"
	}
	socket, paneID, sessionID, parents, reason := s.hostPane(ctx, socket, pane, host)
	if reason != "" {
		return reason
	}
	shared, ok := s.otherAgentPane(ctx, socket, sessionID, paneID, parents)
	if !ok {
		return "panes_unknown"
	}
	if shared {
		if _, err := s.c.Tmux(ctx, socket, "select-pane", "-t", paneID, "-T", name); err != nil {
			return "tmux_unreadable"
		}
		return "pane_titled"
	}
	current, err := s.c.Tmux(ctx, socket, "display-message", "-p", "-t", paneID, "#{session_name}")
	if err != nil {
		return "tmux_unreadable"
	}
	if strings.TrimRight(current, "\n") == name {
		return "unchanged"
	}
	if _, err := s.c.Tmux(ctx, socket, "has-session", "-t", "="+name); err == nil {
		return "name_taken"
	}
	s.c.Tmux(ctx, socket, "rename-session", "-t", sessionID, name)
	after, err := s.c.Tmux(ctx, socket, "display-message", "-p", "-t", paneID, "#{session_name}")
	if err != nil || strings.TrimRight(after, "\n") != name {
		return "rename_unconfirmed"
	}
	return "renamed"
}

// hostPane proves the host is in the selected pane, without renaming anything.
// Registration and terminal naming share this ancestor check.
func (s *server) hostPane(ctx context.Context, socket, pane string, host int) (string, string, string, map[int]int, string) {
	if socket == "" || pane == "" {
		return "", "", "", nil, "not_in_tmux"
	}
	if host <= 1 {
		return "", "", "", nil, "pane_not_host"
	}
	out, err := s.c.Tmux(ctx, socket, "display-message", "-p", "-t", pane, tmuxFields("pane_id", "pane_pid", "session_id"))
	fields := strings.Fields(out)
	if err != nil || len(fields) != 3 {
		return "", "", "", nil, "tmux_unreadable"
	}
	paneID, sessionID := fields[0], fields[2]
	panePID, err := strconv.Atoi(fields[1])
	if err != nil || panePID <= 1 {
		return "", "", "", nil, "tmux_unreadable"
	}
	parents, err := s.c.Parents()
	if err != nil {
		return "", "", "", nil, "process_table_unreadable"
	}
	// The pane must hold the host, with no other agent between them.
	for pid, steps := host, 0; pid != panePID; steps++ {
		if pid != host && s.agentProcess(pid) {
			return "", "", "", nil, "nested_agent"
		}
		parent, found := parents[pid]
		if !found || parent <= 1 || steps >= maxAncestors {
			return "", "", "", nil, "pane_not_host"
		}
		pid = parent
	}
	if panePID != host && s.agentProcess(panePID) {
		return "", "", "", nil, "nested_agent"
	}
	return socket, paneID, sessionID, parents, ""
}

// registrationPane offers only a proven pane of this MCP server's native parent.
// The daemon binds that parent to the launch and reads the actual host's start time. The
// pane carries the absolute tmux that this server, under the agent's PATH, runs (#418).
func (s *server) registrationPane(ctx context.Context) *core.TmuxPane {
	if s.c.Tmux == nil || s.c.Parents == nil || s.c.Command == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmuxTimeout)
	defer cancel()
	envSocket, _, _ := strings.Cut(s.c.Getenv("TMUX"), ",")
	socket, pane, _, _, reason := s.hostPane(ctx, envSocket, s.c.Getenv("TMUX_PANE"), s.c.ParentPID)
	if reason != "" || !platform.CleanAbsolutePath(socket) || !registrationPaneID.MatchString(pane) {
		return nil
	}
	p := &core.TmuxPane{Socket: socket, Pane: pane}
	if s.c.TmuxExecutable != nil {
		if executable, err := s.c.TmuxExecutable(); err == nil && platform.CleanAbsolutePath(executable) {
			p.Executable = executable
		}
	}
	return p
}

var registrationPaneID = regexp.MustCompile(`^%[0-9]{1,9}$`)

// nameAttached names the terminal of a Claude background job (koinon claude --bg), whose
// server has no TMUX: the pane of the job's attach client. It searches the panes of every
// tmux server in this user's socket directory for a `claude attach <short ID>` process,
// where the short ID is the first eight characters of the job's session ID, and applies the
// naming rules with that client as the host. Servers outside that directory are not searched.
func (s *server) nameAttached(ctx context.Context, sessionID, name string) string {
	if !tmuxName.MatchString(name) {
		return "invalid_name"
	}
	if len(sessionID) < 8 {
		return "attach_pane_not_found"
	}
	short := sessionID[:8]
	dir := platform.TmuxSocketDir(s.c.Getenv)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "attach_pane_not_found"
	}
	parents, err := s.c.Parents()
	if err != nil {
		return "process_table_unreadable"
	}
	children := map[int][]int{}
	for pid, parent := range parents {
		children[parent] = append(children[parent], pid)
	}
	type match struct {
		socket, pane string
		client       int
	}
	var found []match
	for _, entry := range entries {
		if entry.Type()&fs.ModeSocket == 0 {
			continue
		}
		socket := filepath.Join(dir, entry.Name())
		out, err := s.c.Tmux(ctx, socket, "list-panes", "-a", "-F", tmuxFields("pane_id", "pane_pid"))
		if err != nil {
			// A socket whose server has ended.
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			root, err := 0, error(nil)
			if len(fields) == 2 {
				root, err = strconv.Atoi(fields[1])
			}
			if len(fields) != 2 || err != nil || !registrationPaneID.MatchString(fields[0]) {
				return "tmux_unreadable"
			}
			queue := []int{root}
			for seen := 0; len(queue) > 0; seen++ {
				if seen >= maxPaneProcesses {
					return "panes_unknown"
				}
				pid := queue[0]
				queue = append(queue[1:], children[pid]...)
				if s.attachClient(pid, short) {
					found = append(found, match{socket, fields[0], pid})
				}
			}
		}
	}
	switch len(found) {
	case 0:
		return "attach_pane_not_found"
	case 1:
		return s.namePane(ctx, found[0].socket, found[0].pane, found[0].client, name)
	}
	return "attach_pane_ambiguous"
}

// attachClient reports whether pid is a Claude Code `claude attach <short>` client.
func (s *server) attachClient(pid int, short string) bool {
	if !claudeProcess(s.c.Command, pid) {
		return false
	}
	_, args, err := s.c.Command(pid)
	if err != nil {
		return false
	}
	if len(args) == 0 {
		return false
	}
	// The native CLI's command starts after argv[0]; the npm CLI's starts after its
	// node entry point. Option values and prompts are never attach commands.
	args = args[1:]
	if len(args) > 0 && strings.Contains(args[0], "@anthropic-ai/claude-code/") {
		args = args[1:]
	}
	return len(args) >= 2 && args[0] == "attach" && args[1] == short
}

// otherAgentPane reports whether another pane of the session holds an agent process. The
// second result is false when that cannot be ruled out.
func (s *server) otherAgentPane(ctx context.Context, socket, sessionID, paneID string, parents map[int]int) (bool, bool) {
	out, err := s.c.Tmux(ctx, socket, "list-panes", "-s", "-t", sessionID, "-F", tmuxFields("pane_id", "pane_pid"))
	if err != nil {
		return false, false
	}
	children := map[int][]int{}
	for pid, parent := range parents {
		children[parent] = append(children[parent], pid)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false, false
		}
		if fields[0] == paneID {
			continue
		}
		root, err := strconv.Atoi(fields[1])
		if err != nil {
			return false, false
		}
		queue := []int{root}
		for seen := 0; len(queue) > 0; seen++ {
			if seen >= maxPaneProcesses {
				return false, false
			}
			pid := queue[0]
			queue = append(queue[1:], children[pid]...)
			if s.agentProcess(pid) {
				return true, true
			}
		}
	}
	return false, true
}

// agentProcess reports whether pid runs an agent CLI: Claude Code, Codex, agy, OpenCode or Grok.
func (s *server) agentProcess(pid int) bool {
	executable, args, err := s.c.Command(pid)
	return err == nil && platform.AgentCommand(executable, args)
}
