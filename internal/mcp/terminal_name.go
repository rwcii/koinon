package mcp

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// Terminal naming (#156). After a registration or renewal, this server names its session's
// own tmux session after the session's published name: its alias while it holds one, else
// its peer name. TMUX and TMUX_PANE only select the server and a candidate pane; the pane is
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
// published name changes; the others are tried again at the next renewal.
var namingFinal = map[string]bool{
	"renamed": true, "unchanged": true, "pane_titled": true, "name_taken": true,
	"pane_not_host": true, "nested_agent": true, "invalid_name": true, "not_in_tmux": true,
}

// RunTmux runs one tmux command on the server at socket. tmux is resolved on PATH, as the
// agent's own terminal would.
func RunTmux(ctx context.Context, socket string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmuxTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).Output()
	return string(out), err
}

// published is the name the session's terminal takes.
func published(session core.Session) string {
	if session.Alias != "" {
		return session.Alias
	}
	return session.Name
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
	// Only the sessions whose terminal the daemon attributes: a Claude session whose
	// server is the child of its Claude process, and a launched session.
	attributed := caller.Family == "claude" && s.claude ||
		caller.Family != "claude" && s.c.Getenv("KOINON_LAUNCH_ID") != ""
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
		result := s.nameTerminal(ctx, hostPID(session), target)
		s.mu.Lock()
		delete(s.naming, caller)
		if namingFinal[result] {
			s.named[caller] = target
		}
		s.mu.Unlock()
		s.obs.mu.Lock()
		if s.obs.naming == nil {
			s.obs.naming = map[core.Key]string{}
		}
		s.obs.naming[caller] = result
		// The session name changed; read it again at the next observation.
		s.obs.checked = time.Time{}
		s.obs.mu.Unlock()
	}()
}

// nameTerminal applies the naming rules for one host process and returns the result.
func (s *server) nameTerminal(ctx context.Context, host int, name string) string {
	if !tmuxName.MatchString(name) {
		return "invalid_name"
	}
	socket, paneID, sessionID, parents, reason := s.hostPane(ctx, host)
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
func (s *server) hostPane(ctx context.Context, host int) (socket, paneID, sessionID string, parents map[int]int, reason string) {
	socket, _, _ = strings.Cut(s.c.Getenv("TMUX"), ",")
	pane := s.c.Getenv("TMUX_PANE")
	if socket == "" || pane == "" {
		return "", "", "", nil, "not_in_tmux"
	}
	if host <= 1 {
		return "", "", "", nil, "pane_not_host"
	}
	out, err := s.c.Tmux(ctx, socket, "display-message", "-p", "-t", pane, "#{pane_id}\t#{pane_pid}\t#{session_id}")
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if err != nil || len(fields) != 3 {
		return "", "", "", nil, "tmux_unreadable"
	}
	paneID, sessionID = fields[0], fields[2]
	panePID, err := strconv.Atoi(fields[1])
	if err != nil || panePID <= 1 {
		return "", "", "", nil, "tmux_unreadable"
	}
	parents, err = s.c.Parents()
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
// The daemon binds that parent to the launch and reads the actual host's start time.
func (s *server) registrationPane(ctx context.Context) *core.TmuxPane {
	if s.c.Tmux == nil || s.c.Parents == nil || s.c.Command == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, tmuxTimeout)
	defer cancel()
	socket, pane, _, _, reason := s.hostPane(ctx, s.c.ParentPID)
	if reason != "" || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 4096 ||
		strings.ContainsFunc(socket, func(r rune) bool { return r < 0x20 || r == 0x7f }) || !registrationPaneID.MatchString(pane) {
		return nil
	}
	return &core.TmuxPane{Socket: socket, Pane: pane}
}

var registrationPaneID = regexp.MustCompile(`^%[0-9]{1,9}$`)

// otherAgentPane reports whether another pane of the session holds an agent process. The
// second result is false when that cannot be ruled out.
func (s *server) otherAgentPane(ctx context.Context, socket, sessionID, paneID string, parents map[int]int) (bool, bool) {
	out, err := s.c.Tmux(ctx, socket, "list-panes", "-s", "-t", sessionID, "-F", "#{pane_id}\t#{pane_pid}")
	if err != nil {
		return false, false
	}
	children := map[int][]int{}
	for pid, parent := range parents {
		children[parent] = append(children[parent], pid)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fields := strings.Split(line, "\t")
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

// agentProcess reports whether pid runs an agent CLI: Claude Code, Codex, agy or OpenCode.
func (s *server) agentProcess(pid int) bool {
	if claudeProcess(s.c.Command, pid) {
		return true
	}
	executable, args, err := s.c.Command(pid)
	if err != nil {
		return false
	}
	names := []string{filepath.Base(executable)}
	if len(args) > 0 {
		names = append(names, filepath.Base(args[0]))
	}
	for _, name := range names {
		switch name {
		case "codex", "agy", "opencode":
			return true
		}
	}
	return false
}
