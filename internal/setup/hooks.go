package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// Hooks report session observations (sprint chunk 08) from the input that an agent gives
// its hook: identifiers, numbers and states only. A report is advisory; a hook's own
// result never depends on it.

const hookInputMax = 1 << 20

// HookEnv locates the daemon. A launched agent passes the launcher's state root and daemon
// address; otherwise the defaults apply.
type HookEnv struct {
	Getenv func(string) string
	Now    func() time.Time
	// ParentPID is the hook's parent process (the agent's host, 0 for os.Getppid), and
	// Parents reads the process table; only the Codex SessionStart hook uses them.
	ParentPID int
	Parents   func() (map[int]int, error)
}

func (e HookEnv) report(o core.Observation) {
	_, err := e.call("/v1/sessions/observe", o)
	var refused core.RefusedError
	if o.Caller.Family == "claude" && errors.As(err, &refused) && refused.Code == "session_not_found" {
		pid := os.Getpid()
		start, err := platform.ProcessStart(pid)
		if err == nil && start > 0 {
			o.ClaudeReporter = &core.ObservationReporter{PID: pid, Start: start}
			e.call("/v1/sessions/observe", o)
		}
	}
}
func (e HookEnv) call(path string, body any) (json.RawMessage, error) {
	return e.callWithin(time.Second, path, body)
}
func (e HookEnv) callWithin(limit time.Duration, path string, body any) (json.RawMessage, error) {
	state := e.Getenv("KOINON_STATE_DIR")
	if state == "" {
		var err error
		if state, err = platform.DefaultStateDir(); err != nil {
			return nil, err
		}
	}
	address, err := core.DaemonAddress(state, e.Getenv)
	if err != nil {
		return nil, err
	}
	secret, err := core.ReadSecret(state)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return core.Call(ctx, address, secret, path, body)
}

// codexClear reads one Codex SessionStart hook input. Only a thread that a /clear started
// gives a registration (#275): Codex runs the hook at that thread's first turn, as a child
// of its host process.
func codexClear(data []byte) (id, directory string, ok bool) {
	var input struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Source    string `json:"source"`
	}
	if len(data) > hookInputMax || json.Unmarshal(data, &input) != nil || input.Source != "clear" ||
		input.SessionID == "" || !filepath.IsAbs(input.Cwd) {
		return "", "", false
	}
	return input.SessionID, input.Cwd, true
}

// CodexSessionStart registers the thread that a Codex host shows after /clear, so the
// participant moves to it at its first turn rather than at its first Koinon tool call. The
// hook writes nothing (Codex would add its output to the model's context), and its result
// never depends on the daemon.
func CodexSessionStart(in io.Reader, env HookEnv) {
	data, err := io.ReadAll(io.LimitReader(in, hookInputMax+1))
	if err != nil {
		return
	}
	id, directory, ok := codexClear(data)
	launch := env.Getenv("KOINON_LAUNCH_ID")
	if !ok || launch == "" {
		return
	}
	r := core.Registration{Family: "codex", ID: id, Directory: directory, LaunchID: launch,
		Ancestors: env.ancestors(), ToolCall: true, Clear: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	git := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--git-dir")
	git.Env = core.CleanGitEnvironment()
	if git.Run() == nil {
		r.Repository = directory
	}
	env.callWithin(5*time.Second, "/v1/sessions/register", r)
}

// ancestors is the hook's parent process and its ancestors, nearest first.
func (e HookEnv) ancestors() []int {
	parent := e.ParentPID
	if parent == 0 {
		parent = os.Getppid()
	}
	parents := map[int]int{}
	if e.Parents != nil {
		if table, err := e.Parents(); err == nil {
			parents = table
		}
	}
	result := []int{}
	for pid := parent; pid > 1 && len(result) < 256; {
		result = append(result, pid)
		next, found := parents[pid]
		if !found {
			break
		}
		pid = next
	}
	return result
}

// claudeStatus reads the model and context use from one Claude Code status-line input, as
// statusline.py does; it returns the session and nil when the input names no session.
func claudeStatus(data []byte, now int64) (core.Observation, string, bool) {
	var input struct {
		SessionID string `json:"session_id"`
		Model     *struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Window *struct {
			Size         *int64          `json:"context_window_size"`
			Input        *int64          `json:"total_input_tokens"`
			CurrentUsage json.RawMessage `json:"current_usage"`
		} `json:"context_window"`
	}
	if len(data) > hookInputMax || json.Unmarshal(data, &input) != nil || input.SessionID == "" {
		return core.Observation{}, "", false
	}
	o := core.Observation{Caller: core.Key{Family: "claude", ID: input.SessionID}}
	display := ""
	if input.Model != nil {
		display = input.Model.DisplayName
		if input.Model.ID != "" {
			o.Model = &core.ObservedValue{Source: "claude_statusline", At: now, ID: input.Model.ID}
		}
	}
	if w := input.Window; w != nil {
		available := len(w.CurrentUsage) > 0 && string(w.CurrentUsage) != "null"
		// The transcript is the context's native epoch: /clear starts a new one (#196).
		o.Context = &core.ObservedValue{Source: "claude_statusline", At: now, UsageAvailable: &available, Epoch: input.SessionID}
		if w.Size != nil && w.Input != nil {
			o.Context.LimitTokens, o.Context.UsedTokens = w.Size, w.Input
		}
	}
	return o, display, o.Model != nil || o.Context != nil
}

// count reads one token count: a JSON integer from 0 to 2^53-1, else unknown.
func count(raw json.RawMessage) *int64 {
	var n int64
	if string(bytes.TrimSpace(raw)) == "null" || json.Unmarshal(raw, &n) != nil || n < 0 || n > 1<<53-1 {
		return nil
	}
	return &n
}

// agyActivity maps the status line's agent_state to an activity: idle, or busy while a turn
// or a tool runs. Any other state, such as authenticating, is unknown, never idle.
var agyActivity = map[string]string{"idle": "idle", "working": "busy", "tool_use": "busy"}

// agyStatus reads the model, context use and activity of one agy status-line input (#512). The
// caller is the conversation, which /clear replaces; an input without one gives no report.
// Counts that are absent, null or invalid, or a limit that is not positive, are unknown.
func agyStatus(data []byte, now int64) (core.Observation, string, bool) {
	var input struct {
		ConversationID string          `json:"conversation_id"`
		AgentState     string          `json:"agent_state"`
		Model          json.RawMessage `json:"model"`
		Window         *struct {
			Size         json.RawMessage `json:"context_window_size"`
			Input        json.RawMessage `json:"total_input_tokens"`
			CurrentUsage json.RawMessage `json:"current_usage"`
		} `json:"context_window"`
	}
	if len(data) > hookInputMax || json.Unmarshal(data, &input) != nil || input.ConversationID == "" {
		return core.Observation{}, "", false
	}
	o := core.Observation{Caller: core.Key{Family: "agy", ID: input.ConversationID}}
	var model struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(input.Model, &model) == nil && model.ID != "" {
		o.Model = &core.ObservedValue{Source: "agy_statusline", At: now, ID: model.ID}
	}
	if state := agyActivity[input.AgentState]; state != "" {
		o.Activity = &core.ObservedValue{Source: "agy_statusline", At: now, State: state}
	}
	if w := input.Window; w != nil {
		available := len(w.CurrentUsage) > 0 && string(w.CurrentUsage) != "null"
		o.Context = &core.ObservedValue{Source: "agy_statusline", At: now, UsageAvailable: &available, Epoch: input.ConversationID}
		if size, used := count(w.Size), count(w.Input); size != nil && *size > 0 && used != nil {
			o.Context.LimitTokens, o.Context.UsedTokens = size, used
		}
	}
	return o, "", o.Model != nil || o.Context != nil || o.Activity != nil
}

// ClaudeStatus is the Claude Code status-line command. It runs the user's own command, if
// any, through /bin/sh with the same input and returns its output and exit status
// unchanged, and meanwhile reports the session's model and context use. Without a command
// it prints the model's display name.
func ClaudeStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	return statusCommand("claude-status", hookInputMax, claudeStatus, args, in, out, errOut, env)
}

// AgyStatus is the agy status-line command, as ClaudeStatus is for Claude Code. Without a
// command it prints nothing, so agy's stacked default status line stands alone. It also gives
// the daemon each conversation with this hook process as its reporter, which ties a new
// conversation after a managed /clear to the cycle's pinned host (#512).
func AgyStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	return statusCommand("agy-status", hookInputMax, func(data []byte, now int64) (core.Observation, string, bool) {
		o, display, ok := agyStatus(data, now)
		if ok {
			env.witness("agy", o.Caller.ID)
		}
		return o, display, ok
	}, args, in, out, errOut, env)
}

// grokStatusMax bounds the Grok status-line payload that the hook decodes (#515): a larger
// input gives no observation, and the wrapped command still gets all of it.
const grokStatusMax = 64 << 10

// grokSessionID is the form of a Grok session ID that the hook reports: the context epoch's
// form (nativeEpoch in internal/core).
var grokSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// grokStatus reads the model and context use of one Grok status-line input (#515). The caller
// is the input's session_id, which is the session's MCP identity: Grok Build 1.0.50 gives
// GROK_SESSION_ID to its MCP server, not to the status-line command. A session_id that is
// absent, null, empty, not a string or not a session ID gives no report, and so does one that
// differs from a GROK_SESSION_ID in the hook's environment (env, when set). Model and context are
// read independently; the context needs both counts.
func grokStatus(env string) func([]byte, int64) (core.Observation, string, bool) {
	return func(data []byte, now int64) (core.Observation, string, bool) {
		if len(data) > grokStatusMax {
			return core.Observation{}, "", false
		}
		var input struct {
			SessionID json.RawMessage `json:"session_id"`
			Model     json.RawMessage `json:"model"`
			Window    json.RawMessage `json:"context_window"`
		}
		var session string
		if json.Unmarshal(data, &input) != nil || json.Unmarshal(input.SessionID, &session) != nil ||
			!grokSessionID.MatchString(session) || env != "" && env != session {
			return core.Observation{}, "", false
		}
		o := core.Observation{Caller: core.Key{Family: "grok", ID: session}}
		var model struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(input.Model, &model) == nil && model.ID != "" {
			o.Model = &core.ObservedValue{Source: "grok_statusline", At: now, ID: model.ID}
		}
		var window struct {
			Size json.RawMessage `json:"context_window_size"`
			Used json.RawMessage `json:"context_tokens"`
		}
		if json.Unmarshal(input.Window, &window) == nil {
			if size, used := count(window.Size), count(window.Used); size != nil && *size > 0 && used != nil {
				available := true
				o.Context = &core.ObservedValue{Source: "grok_statusline", At: now, UsageAvailable: &available,
					Epoch: session, LimitTokens: size, UsedTokens: used}
			}
		}
		return o, "", o.Model != nil || o.Context != nil
	}
}

// GrokStatus is the Grok status-line command, as ClaudeStatus is for Claude Code. Without a
// command it prints nothing. It also gives the daemon each session with this hook process as its
// reporter, also without usable counts, which ties the new session after a managed /new to the
// cycle's pinned host (#515).
func GrokStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	parse := grokStatus(env.Getenv("GROK_SESSION_ID"))
	return statusCommand("grok-status", grokStatusMax, func(data []byte, now int64) (core.Observation, string, bool) {
		o, display, ok := parse(data, now)
		if o.Caller.ID != "" {
			env.witness("grok", o.Caller.ID)
		}
		return o, display, ok
	}, args, in, out, errOut, env)
}

// witness gives the daemon one agy conversation or Grok session with this process as its
// reporter. Without a cycle that waits for one, the daemon keeps nothing.
func (e HookEnv) witness(family, conversation string) {
	pid := os.Getpid()
	start, err := platform.ProcessStart(pid)
	if err != nil || start <= 0 {
		return
	}
	e.call("/v1/cycles/"+family+"-witness", core.AgyWitness{Conversation: conversation, Reporter: core.ObservationReporter{PID: pid, Start: start}})
}

// statusCommand runs one status-line hook. It reads at most limit+1 bytes of the input for
// parse, which gives no report for a longer input; a wrapped command gets those bytes and then
// the rest of the input, unread, so its stdin is never cut short.
func statusCommand(hook string, limit int64, parse func([]byte, int64) (core.Observation, string, bool),
	args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	command, hasCommand := "", false
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--command":
		command, hasCommand = args[1], true
	default:
		io.WriteString(errOut, "usage: koinon hook "+hook+" [--command COMMAND]\n")
		return 2
	}
	data, err := io.ReadAll(io.LimitReader(in, limit+1))
	if err != nil {
		return 1
	}
	var child *exec.Cmd
	var signals chan os.Signal
	if hasCommand {
		// Claude Code runs a status-line command as `/bin/sh -c <command>`; do the same, so
		// expansions, pipes and quoting keep their meaning. agy's entry has the same type and
		// command members.
		child = exec.Command("/bin/sh", "-c", command)
		child.Stdout, child.Stderr = out, errOut
		stdin, err := child.StdinPipe()
		if err == nil {
			err = child.Start()
		}
		if err != nil {
			child = nil
		} else {
			signals = make(chan os.Signal, 4)
			signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
			go func() {
				for s := range signals {
					child.Process.Signal(s)
				}
			}()
			go func() {
				io.Copy(stdin, io.MultiReader(bytes.NewReader(data), in))
				stdin.Close()
			}()
		}
	}
	o, display, ok := parse(data, env.Now().UnixMilli())
	if ok {
		env.report(o)
	}
	if child == nil {
		if hasCommand {
			return 127
		}
		if display != "" {
			io.WriteString(out, display+"\n")
		}
		return 0
	}
	err = child.Wait()
	signal.Stop(signals)
	close(signals)
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

// agyStopObservation reads the conversation, model and the end of the turn from one agy
// Stop hook input (spike fact 4).
func agyStopObservation(data []byte, now int64) (core.Observation, bool) {
	var input struct {
		ConversationID string `json:"conversationId"`
		ModelName      string `json:"modelName"`
	}
	if json.Unmarshal(data, &input) != nil || input.ConversationID == "" {
		return core.Observation{}, false
	}
	o := core.Observation{Caller: core.Key{Family: "agy", ID: input.ConversationID},
		Activity: &core.ObservedValue{Source: "agy_hook", At: now, State: "idle"}}
	if input.ModelName != "" {
		o.Model = &core.ObservedValue{Source: "agy_hook", At: now, ID: input.ModelName}
	}
	return o, true
}
