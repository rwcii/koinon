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

// agyStatus reads the model and context use of one agy status-line input (#512). The caller
// is the conversation, which /clear replaces; an input without one gives no report. Counts
// that are absent, null or invalid, or a limit that is not positive, are unknown.
func agyStatus(data []byte, now int64) (core.Observation, string, bool) {
	var input struct {
		ConversationID string          `json:"conversation_id"`
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
	if w := input.Window; w != nil {
		available := len(w.CurrentUsage) > 0 && string(w.CurrentUsage) != "null"
		o.Context = &core.ObservedValue{Source: "agy_statusline", At: now, UsageAvailable: &available, Epoch: input.ConversationID}
		if size, used := count(w.Size), count(w.Input); size != nil && *size > 0 && used != nil {
			o.Context.LimitTokens, o.Context.UsedTokens = size, used
		}
	}
	return o, "", o.Model != nil || o.Context != nil
}

// ClaudeStatus is the Claude Code status-line command. It runs the user's own command, if
// any, through /bin/sh with the same input and returns its output and exit status
// unchanged, and meanwhile reports the session's model and context use. Without a command
// it prints the model's display name.
func ClaudeStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	return statusCommand("claude-status", claudeStatus, args, in, out, errOut, env)
}

// AgyStatus is the agy status-line command, as ClaudeStatus is for Claude Code. Without a
// command it prints nothing, so agy's stacked default status line stands alone.
func AgyStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	return statusCommand("agy-status", agyStatus, args, in, out, errOut, env)
}

func statusCommand(hook string, parse func([]byte, int64) (core.Observation, string, bool),
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
	data, err := io.ReadAll(io.LimitReader(in, 16<<20))
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
				stdin.Write(data)
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
