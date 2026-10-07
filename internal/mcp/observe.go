package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// Session observations (sprint chunk 08). This server reports what it can read for the
// sessions it serves: the model a Codex call names, the Claude Code registry activity of
// its parent Claude process, the metadata of a Codex session's own rollout, and its tmux
// pane. It reads identifiers, numbers, states and times only, never conversation content,
// and it sends a group only when it changed.

const (
	observeEvery      = 5 * time.Second
	confirmEvery      = time.Minute // an unchanged value is sent again, to keep it fresh
	terminalEvery     = time.Minute
	rolloutSearch     = 15 * time.Second
	rolloutBatch      = 4 << 20
	rolloutLine       = 1 << 20
	maxObserved       = 8
	registryRecordMax = 64 << 10
)

var threadKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// sentValue is the last report of a group and when it was sent.
type sentValue struct {
	value core.ObservedValue
	at    time.Time
}

type observer struct {
	mu       sync.Mutex
	sent     map[core.Key]map[string]sentValue
	rollouts map[string]*rollout
	terminal *core.ObservedValue
	checked  time.Time
	naming   map[core.Key]string // the last terminal naming result of each session
}

// observeCall reports what one tool call shows: the model of a Codex turn, and that an
// agy session is busy. It never delays the call's answer.
func (s *server) observeCall(caller core.Key, meta map[string]json.RawMessage) {
	o := core.Observation{Caller: caller}
	now := s.c.Now().UnixMilli()
	switch caller.Family {
	case "codex":
		if id := codexMetaModel(meta["x-codex-turn-metadata"]); id != "" {
			o.Model = &core.ObservedValue{Source: "codex_mcp_meta", At: now, ID: id}
		}
	case "agy":
		o.Activity = &core.ObservedValue{Source: "mcp_call", At: now, State: "busy"}
	}
	if o.Model != nil || o.Activity != nil {
		go s.report(context.Background(), o)
	}
}

// codexMetaModel reads the model from Codex's turn metadata, which arrives as an object or
// as a JSON string (spike fact 1).
func codexMetaModel(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		raw = json.RawMessage(text)
	}
	var meta struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return ""
	}
	return meta.Model
}

// report sends the groups of o that changed since the last report for the caller, and
// unchanged groups once a minute, so that the daemon knows the source is still readable.
func (s *server) report(ctx context.Context, o core.Observation) {
	now := s.c.Now()
	s.obs.mu.Lock()
	if s.obs.sent == nil || len(s.obs.sent) > 4*maxObserved {
		// Forgetting only costs one repeated report per session.
		s.obs.sent = map[core.Key]map[string]sentValue{}
	}
	last := s.obs.sent[o.Caller]
	if last == nil {
		last = map[string]sentValue{}
		s.obs.sent[o.Caller] = last
	}
	changed := false
	for group, field := range map[string]**core.ObservedValue{"model": &o.Model, "context": &o.Context, "activity": &o.Activity, "terminal": &o.Terminal} {
		if *field == nil {
			continue
		}
		previous, found := last[group]
		if found && sameObservation(group, previous.value, **field) && now.Sub(previous.at) < confirmEvery {
			*field = nil
			continue
		}
		changed = true
	}
	s.obs.mu.Unlock()
	if !changed {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := s.daemon(ctx, "/v1/sessions/observe", o); err != nil {
		return // Advisory: a refused or lost report is sent again at the next change.
	}
	s.obs.mu.Lock()
	defer s.obs.mu.Unlock()
	for group, v := range map[string]*core.ObservedValue{"model": o.Model, "context": o.Context, "activity": o.Activity, "terminal": o.Terminal} {
		if v != nil {
			last[group] = sentValue{*v, now}
		}
	}
}

// sameObservation compares a terminal by its fields alone, since its time is only when this
// server looked; every other group also by the time its source recorded it.
func sameObservation(group string, a, b core.ObservedValue) bool {
	if group == "terminal" {
		a.At, b.At = 0, 0
	}
	return reflect.DeepEqual(a, b)
}

// observeLoop reports the pulled sources of the sessions this server registered.
func (s *server) observeLoop(ctx context.Context) {
	ticker := time.NewTicker(observeEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.observeOnce(ctx)
		}
	}
}

func (s *server) observeOnce(ctx context.Context) {
	s.mu.Lock()
	type served struct {
		key core.Key
		at  time.Time
	}
	var callers []served
	for k, r := range s.sessions {
		callers = append(callers, served{k, r.at})
	}
	claude := s.claude
	s.mu.Unlock()
	// The most recently registered sessions only, so a long-lived shared server stays bounded.
	for i := range callers {
		for j := i + 1; j < len(callers); j++ {
			if callers[j].at.After(callers[i].at) {
				callers[i], callers[j] = callers[j], callers[i]
			}
		}
	}
	if len(callers) > maxObserved {
		callers = callers[:maxObserved]
	}
	terminal := s.terminal(ctx)
	for _, c := range callers {
		o := core.Observation{Caller: c.key}
		switch c.key.Family {
		case "claude":
			if claude {
				o.Activity = s.claudeActivity(c.key.ID)
			}
		case "codex":
			o.Model, o.Context, o.Activity = s.codexRollout(c.key.ID)
		}
		// The daemon attributes a terminal only to a launched or a Claude session; this
		// server offers it only for those, so that a shared server's environment is not
		// reported as another session's terminal.
		if terminal != nil && (c.key.Family == "claude" && claude || s.c.Getenv("KOINON_LAUNCH_ID") != "") {
			v := *terminal
			s.obs.mu.Lock()
			v.Naming = s.obs.naming[c.key]
			s.obs.mu.Unlock()
			o.Terminal = &v
		}
		s.report(ctx, o)
	}
}

// terminal reads this server's tmux pane from its environment and asks tmux for the pane's
// session name, at most once a minute.
func (s *server) terminal(ctx context.Context) *core.ObservedValue {
	tmux, pane := s.c.Getenv("TMUX"), s.c.Getenv("TMUX_PANE")
	socket, _, _ := strings.Cut(tmux, ",")
	if socket == "" || pane == "" {
		return nil
	}
	now := s.c.Now()
	s.obs.mu.Lock()
	if s.obs.terminal != nil && now.Sub(s.obs.checked) < terminalEvery {
		v := *s.obs.terminal
		s.obs.mu.Unlock()
		return &v
	}
	s.obs.mu.Unlock()
	name := ""
	if s.c.TmuxSession != nil {
		name, _ = s.c.TmuxSession(ctx, socket, pane)
	}
	v := &core.ObservedValue{Source: "tmux_env", At: now.UnixMilli(), Socket: socket, Pane: pane, Session: name}
	s.obs.mu.Lock()
	s.obs.terminal, s.obs.checked = v, now
	s.obs.mu.Unlock()
	copy := *v
	return &copy
}

// TmuxSessionName asks the tmux server at socket for the name of the session that holds
// pane. tmux is resolved on PATH, as the agent's own terminal would.
func TmuxSessionName(ctx context.Context, socket, pane string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "-S", socket, "display-message", "-p", "-t", pane, "#{session_name}").Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimRight(string(out), "\n")
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\x00\r\n") {
		return "", errors.New("invalid tmux session name")
	}
	return name, nil
}

func (s *server) home(variable, fallback string) string {
	if v := s.c.Getenv(variable); v != "" {
		return v
	}
	if home := s.c.Getenv("HOME"); home != "" {
		return filepath.Join(home, fallback)
	}
	return ""
}

// readOwned reads a regular file owned by this user, up to limit bytes, without following
// a final symbolic link.
func readOwned(path string, limit int64) ([]byte, error) {
	f, err := platform.OpenOwned(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("file too large")
	}
	return data, err
}

// claudeActivity reads the Claude Code registry record of this server's parent Claude
// process, as participant_presence.py does: an interactive (`cli`) session whose record
// names this session, with a valid status time.
func (s *server) claudeActivity(session string) *core.ObservedValue {
	dir := s.home("CLAUDE_CONFIG_DIR", ".claude")
	if dir == "" {
		return nil
	}
	data, err := readOwned(filepath.Join(dir, "sessions", strconv.Itoa(s.c.ParentPID)+".json"), registryRecordMax)
	if err != nil {
		return nil
	}
	var record struct {
		PID             int    `json:"pid"`
		SessionID       string `json:"sessionId"`
		Status          string `json:"status"`
		StatusUpdatedAt int64  `json:"statusUpdatedAt"`
		Entrypoint      string `json:"entrypoint"`
	}
	if json.Unmarshal(data, &record) != nil || record.PID != s.c.ParentPID || record.SessionID != session || record.Entrypoint != "cli" {
		return nil
	}
	state := map[string]string{"busy": "busy", "shell": "busy", "idle": "idle", "waiting": "waiting"}[record.Status]
	if state == "" || record.StatusUpdatedAt <= 0 || record.StatusUpdatedAt > s.c.Now().UnixMilli() {
		return nil
	}
	return &core.ObservedValue{Source: "claude_registry", At: record.StatusUpdatedAt, State: state}
}

// rollout follows one Codex session's rollout file incrementally, as codex_status.py does.
type rollout struct {
	path         string
	searched     time.Time
	device, node uint64
	offset       int64
	skipping     bool // inside a record longer than rolloutLine
	turn         string
	model        *core.ObservedValue
	context      *core.ObservedValue
	activity     *core.ObservedValue
}

func (s *server) codexRollout(thread string) (model, context, activity *core.ObservedValue) {
	if !threadKey.MatchString(thread) {
		return nil, nil, nil
	}
	s.obs.mu.Lock()
	if s.obs.rollouts == nil {
		s.obs.rollouts = map[string]*rollout{}
	}
	r := s.obs.rollouts[thread]
	if r == nil {
		if len(s.obs.rollouts) >= maxObserved {
			s.obs.rollouts = map[string]*rollout{}
		}
		r = &rollout{}
		s.obs.rollouts[thread] = r
	}
	s.obs.mu.Unlock()
	now := s.c.Now()
	if r.path == "" {
		if !r.searched.IsZero() && now.Sub(r.searched) < rolloutSearch {
			return nil, nil, nil
		}
		r.searched = now
		home := s.home("CODEX_HOME", ".codex")
		if home == "" {
			return nil, nil, nil
		}
		r.path = findRollout(filepath.Join(home, "sessions"), thread)
		if r.path == "" {
			return nil, nil, nil
		}
	}
	if err := r.consume(thread); err != nil {
		*r = rollout{searched: now}
		return nil, nil, nil
	}
	return r.model, r.context, r.activity
}

// findRollout returns the one rollout file whose name holds the thread ID, or "".
func findRollout(root, thread string) string {
	var found []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if len(found) > 1 {
			return filepath.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".jsonl") && strings.Contains(d.Name(), thread) {
			found = append(found, path)
		}
		return nil
	})
	if len(found) != 1 {
		return ""
	}
	return found[0]
}

type rolloutRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// consume reads at most one batch of new complete records. A replaced or shortened file is
// read again from the start, after its first record proves the session.
func (r *rollout) consume(thread string) error {
	f, err := platform.OpenOwned(r.path)
	if err != nil {
		return err
	}
	defer f.Close()
	device, node, size, err := platform.FileIdentity(f)
	if err != nil {
		return err
	}
	// A buffer one byte longer than the longest record that is read.
	reader := bufio.NewReaderSize(f, rolloutLine+1)
	if device != r.device || node != r.node || size < r.offset {
		line, err := readRecord(reader)
		if err != nil {
			return err
		}
		var first rolloutRecord
		var meta struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(line, &first) != nil || first.Type != "session_meta" || json.Unmarshal(first.Payload, &meta) != nil || meta.ID != thread {
			return errors.New("rollout identity mismatch")
		}
		*r = rollout{path: r.path, device: device, node: node}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		reader.Reset(f)
	} else if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return err
	} else {
		reader.Reset(f)
	}
	// Each byte read is counted once toward the batch, and the offset moves once per byte
	// that is processed or discarded. A record longer than rolloutLine is discarded in
	// batch-bounded pieces, across calls if need be, without keeping its text.
	consumed := int64(0)
	for consumed < rolloutBatch {
		line, err := reader.ReadSlice('\n')
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			r.offset, consumed, r.skipping = r.offset+int64(len(line)), consumed+int64(len(line)), true
		case err != nil:
			// The end of the file inside a record: a record being discarded can move on,
			// any other waits for its end.
			if r.skipping {
				r.offset += int64(len(line))
			}
			return nil
		default:
			r.offset, consumed = r.offset+int64(len(line)), consumed+int64(len(line))
			if r.skipping {
				r.skipping = false // The tail of a discarded record.
				continue
			}
			r.event(line)
		}
	}
	return nil
}

func readRecord(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return nil, errors.New("rollout has no complete first record")
	}
	return line, nil
}

// event applies one record; any other record type, and a malformed one, is ignored.
func (r *rollout) event(line []byte) {
	var record rolloutRecord
	if json.Unmarshal(line, &record) != nil || (record.Type != "turn_context" && record.Type != "event_msg") {
		return
	}
	when, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil || when.UnixMilli() <= 0 {
		return
	}
	at := when.UnixMilli()
	var payload struct {
		Type   string `json:"type"`
		Model  string `json:"model"`
		TurnID string `json:"turn_id"`
		Info   *struct {
			Window *int64 `json:"model_context_window"`
			Last   *struct {
				Input *int64 `json:"input_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(record.Payload, &payload) != nil {
		return
	}
	if record.Type == "turn_context" {
		if payload.Model != "" {
			r.model = &core.ObservedValue{Source: "codex_rollout", At: at, ID: payload.Model}
		}
		return
	}
	switch payload.Type {
	case "token_count":
		available := payload.Info != nil && payload.Info.Last != nil && payload.Info.Last.Input != nil && payload.Info.Window != nil
		v := &core.ObservedValue{Source: "codex_rollout", At: at, UsageAvailable: &available}
		if available {
			v.LimitTokens, v.UsedTokens = payload.Info.Window, payload.Info.Last.Input
		}
		r.context = v
	case "task_started":
		if payload.TurnID != "" {
			r.turn = payload.TurnID
			r.activity = &core.ObservedValue{Source: "codex_rollout", At: at, State: "busy"}
		}
	case "task_complete", "turn_aborted":
		if payload.TurnID != "" && payload.TurnID == r.turn {
			r.activity = &core.ObservedValue{Source: "codex_rollout", At: at, State: "idle"}
		}
	}
}
