package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// checkEvery is how often an idle server checks its executable, as the daemon does.
const checkEvery = 5 * time.Second

// ResumeVersion is the version of the state that a replaced program passes; a program
// that reads it reports it in `koinon version` as mcp_resume.
const ResumeVersion = 1

// resumeState is what the new program needs to continue the client's MCP session without
// a second initialize, and the sessions it keeps active (#440).
type resumeState struct {
	Version      int              `json:"v"`
	Protocol     string           `json:"protocol_version"`
	Client       string           `json:"client"`
	Capabilities json.RawMessage  `json:"capabilities,omitempty"`
	Sessions     []resumedSession `json:"sessions,omitempty"`
	Latest       core.Key         `json:"latest"`
	Renewal      int64            `json:"renewal_seconds,omitempty"`
	Subagents    []core.Key       `json:"subagents,omitempty"`
	// ClaudeHostStart is the start of the Claude host that initialize pinned.
	ClaudeHostStart int64 `json:"claude_host_start,omitempty"`
}

type resumedSession struct {
	core.Key
	Revision int64 `json:"revision"`
	// RetryAfterMS is what remains of a holder_active wait.
	RetryAfterMS int64 `json:"retry_after_ms,omitempty"`
}

// loops are the renewal and observation loops. Stopping them waits for their current
// daemon call, so the new program renews from the revision that call returned.
type loops struct {
	cancel context.CancelFunc
	done   sync.WaitGroup
}

// startLoops starts the loops; renewNow renews the latest session first, as a replaced
// program's renewals continue.
func (s *server) startLoops(ctx context.Context, renewNow bool) {
	ctx, cancel := context.WithCancel(ctx)
	l := &loops{cancel: cancel}
	l.done.Add(2)
	go func() {
		defer l.done.Done()
		if renewNow {
			s.renew(context.WithoutCancel(ctx))
		}
		s.renewLoop(ctx)
	}()
	go func() {
		defer l.done.Done()
		s.observeLoop(ctx)
	}()
	s.running = l
}

func (l *loops) stop() {
	l.cancel()
	l.done.Wait()
}

// idleWait returns how Serve waits for input when the server can replace its program: it
// polls the input and, while no byte waits, checks the executable every CheckEvery. A
// request is read and answered before the next check, and a byte that arrives during the
// replacement stays in the pipe for the new program. Nil keeps the program.
func (s *server) idleWait(in io.Reader) func(context.Context) (bool, error) {
	file, ok := in.(*os.File)
	if !ok || s.c.Changed == nil || s.c.Replace == nil {
		return nil
	}
	fd := int(file.Fd())
	every := s.c.CheckEvery
	if every <= 0 {
		every = checkEvery
	}
	var checked time.Time
	return func(ctx context.Context) (bool, error) {
		for {
			ready, err := platform.Readable(fd, 0)
			if err != nil || ready {
				return false, err
			}
			// The new program continues a session that initialize began, never starts one.
			if time.Since(checked) >= every && s.initialized() {
				checked = time.Now()
				if s.c.Changed() && s.replace(ctx) {
					return true, nil
				}
			}
			if ready, err = platform.Readable(fd, every); err != nil || ready {
				return false, err
			}
		}
	}
}

// replace runs the new program with this server's state. When that does not happen, the
// server keeps serving on its program and starts its loops again.
func (s *server) replace(ctx context.Context) bool {
	s.running.stop()
	state, err := json.Marshal(s.state())
	if err == nil {
		// No answer is half written when the program changes.
		s.outMu.Lock()
		err = s.c.Replace(state)
		s.outMu.Unlock()
		if err == nil {
			return true
		}
	}
	s.note("koinon mcp: kept the running program: " + err.Error())
	s.startLoops(ctx, false)
	return false
}

func (s *server) initialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protocol != ""
}

func (s *server) state() resumeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.c.Now()
	r := resumeState{Version: ResumeVersion, Protocol: s.protocol, Client: s.client, Capabilities: s.capabilities,
		Latest: s.latest, Renewal: int64(s.renewal / time.Second), ClaudeHostStart: s.claudeHostStart}
	for key, current := range s.sessions {
		session := resumedSession{Key: key, Revision: current.revision}
		if wait := current.retryAt.Sub(now); !current.retryAt.IsZero() {
			session.RetryAfterMS = max(wait.Milliseconds(), 1)
		}
		r.Sessions = append(r.Sessions, session)
	}
	for key, subagent := range s.subagents {
		if subagent {
			r.Subagents = append(r.Subagents, key)
		}
	}
	less := func(a, b core.Key) bool { return a.Family < b.Family || a.Family == b.Family && a.ID < b.ID }
	sort.Slice(r.Sessions, func(i, j int) bool { return less(r.Sessions[i].Key, r.Sessions[j].Key) })
	sort.Slice(r.Subagents, func(i, j int) bool { return less(r.Subagents[i], r.Subagents[j]) })
	return r
}

// resume restores the replaced program's state, as its initialize and registrations left it.
func (s *server) resume(data []byte) error {
	var r resumeState
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Version != ResumeVersion {
		return fmt.Errorf("state version %d", r.Version)
	}
	if r.Protocol == "" {
		return errors.New("no initialized session")
	}
	s.setClient(r.Protocol, r.Client, r.Capabilities)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claude && r.ClaudeHostStart > 0 {
		// The host stays the one pinned at the first initialize, never a reused PID.
		s.claudeHostStart = r.ClaudeHostStart
	}
	now := s.c.Now()
	for _, session := range r.Sessions {
		current := registered{revision: session.Revision, at: now}
		if session.RetryAfterMS > 0 {
			current.retryAt = now.Add(time.Duration(session.RetryAfterMS) * time.Millisecond)
		}
		s.sessions[session.Key] = current
	}
	s.latest = r.Latest
	s.setInterval(r.Renewal)
	for _, key := range r.Subagents {
		if s.subagents == nil {
			s.subagents = map[core.Key]bool{}
		}
		s.subagents[key] = true
	}
	return nil
}

func (s *server) note(line string) {
	if s.c.Stderr != nil {
		fmt.Fprintln(s.c.Stderr, line)
	}
}
