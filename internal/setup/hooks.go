package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
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
}

func (e HookEnv) report(o core.Observation) {
	state := e.Getenv("KOINON_STATE_DIR")
	if state == "" {
		var err error
		if state, err = platform.DefaultStateDir(); err != nil {
			return
		}
	}
	address := e.Getenv("KOINON_DAEMON_ADDRESS")
	if address == "" {
		address = "127.0.0.1:47671"
	}
	secret, err := core.ReadSecret(state)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	core.Call(ctx, address, secret, "/v1/sessions/observe", o)
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
		o.Context = &core.ObservedValue{Source: "claude_statusline", At: now, UsageAvailable: &available}
		if w.Size != nil && w.Input != nil {
			o.Context.LimitTokens, o.Context.UsedTokens = w.Size, w.Input
		}
	}
	return o, display, o.Model != nil || o.Context != nil
}

// ClaudeStatus is the Claude Code status-line command. It runs the user's own command, if
// any, through /bin/sh with the same input and returns its output and exit status
// unchanged, and meanwhile reports the session's model and context use. Without a command
// it prints the model's display name.
func ClaudeStatus(args []string, in io.Reader, out, errOut io.Writer, env HookEnv) int {
	command, hasCommand := "", false
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--command":
		command, hasCommand = args[1], true
	default:
		io.WriteString(errOut, "usage: koinon hook claude-status [--command COMMAND]\n")
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
		// expansions, pipes and quoting keep their meaning.
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
	o, display, ok := claudeStatus(data, env.Now().UnixMilli())
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
