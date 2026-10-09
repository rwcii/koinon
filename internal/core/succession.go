package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/rwcii/koinon/internal/platform"
)

// Succession (participants sprint, chunk 04). A session S that registers through its own tool
// call takes its participant from the active holder H only on evidence from the host records:
// the same host process (ID and start time) with H idle for activeGuard, or the same tmux
// server and pane with H's host process ended. Anything else leaves H the holder and reports
// the refusal; a refused succession never refuses the registration.

// activeGuard is how long after the holder's last tool call same-host evidence is refused.
const activeGuard = 30_000

// TmuxPane is the tmux server socket and pane that hold a session's host process, as
// koinon mcp reports them.
type TmuxPane struct {
	Socket string `json:"socket"`
	Pane   string `json:"pane"`
}

var paneID = regexp.MustCompile(`^%[0-9]{1,9}$`)

func (p *TmuxPane) valid() bool {
	return p == nil || paneID.MatchString(p.Pane) && filepath.IsAbs(p.Socket) && len(p.Socket) <= 4096 &&
		filepath.Clean(p.Socket) == p.Socket && !strings.ContainsFunc(p.Socket, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// Succession is a session's last succession result. Result is "succeeded" or "refused";
// Reason is the evidence (same_host, same_pane) or the refusal: no_host, host_running,
// other_pane, subagent, holder_active, fenced or other_participant. Holder is the peer name
// of the session that held the participant. After holder_active, the session may register
// again RetryAfterMS after At.
type Succession struct {
	Result       string `json:"result"`
	Reason       string `json:"reason"`
	Address      string `json:"address"`
	Holder       string `json:"holder,omitempty"`
	At           int64  `json:"at"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
}

// toolCalls is the daemon's record of each session's last tool call, in daemon time. It is
// kept in memory: since is the daemon's start, before which no call is known.
type toolCalls struct {
	mu    sync.Mutex
	at    map[Key]int64
	since int64
}

// ToolCall records a tool call of caller now. Only the routes of agent tools record one;
// observation, wake, renewal and maintenance requests never do.
func (s *Store) ToolCall(caller Key) {
	now := s.now().UnixMilli()
	s.calls.mu.Lock()
	defer s.calls.mu.Unlock()
	if s.calls.at == nil {
		s.calls.at = map[Key]int64{}
	}
	if len(s.calls.at) >= 4096 {
		for k, at := range s.calls.at {
			if now-at >= activeGuard {
				delete(s.calls.at, k)
			}
		}
	}
	s.calls.at[caller] = now
}

// guardLeft is how long the guard still refuses same-host evidence against k: 0 when k
// made no tool call in the last activeGuard, counting a daemon start as an unknown call.
func (s *Store) guardLeft(k Key, now int64) int64 {
	s.calls.mu.Lock()
	defer s.calls.mu.Unlock()
	left := int64(0)
	for _, at := range []int64{s.calls.at[k], s.calls.since} {
		if at != 0 && activeGuard-(now-at) > left {
			left = activeGuard - (now - at)
		}
	}
	return left
}

// host is a session's host record.
type host struct {
	pid, start   int64
	socket, pane string
}

// hostPID is the host process of a launched session: the Claude process that its server
// reports (the job's process for a background job), or the launch's host process.
func hostPID(target json.RawMessage) int64 {
	var t struct {
		HostPID   int64 `json:"host_pid"`
		ClaudePID int64 `json:"claude_pid"`
	}
	if json.Unmarshal(target, &t) != nil {
		return 0
	}
	if t.ClaudePID > 0 {
		return t.ClaudePID
	}
	return t.HostPID
}

// recordHost reads the host process's start time; a host that cannot be read has no host
// record, so it gives no evidence.
func (s *Store) recordHost(pid int64, pane *TmuxPane) host {
	h := host{}
	if pid > 0 {
		if start, err := s.processStart(int(pid)); err == nil && start > 0 {
			h.pid, h.start = pid, start
		}
	}
	if pane != nil {
		h.socket, h.pane = pane.Socket, pane.Pane
	}
	return h
}

// hostEnded reports whether h's host process has ended: no process has its ID, or another
// process with another start time does. A process that cannot be read is not proven ended.
func (s *Store) hostEnded(h host) bool {
	start, err := s.processStart(int(h.pid))
	if errors.Is(err, platform.ErrProcessGone) {
		return true
	}
	return err == nil && start != h.start
}

// succeed decides, in S's registration transaction, whether S takes its participant from
// its active holder. It returns nil when no other session holds the participant.
func (s *Store) succeed(ctx context.Context, tx *sql.Tx, now int64, k Key, repository, role string, subagent bool) (*Succession, error) {
	if repository == "" {
		return nil, nil
	}
	var address, holder string
	err := tx.QueryRowContext(ctx, `SELECT name,holder_id FROM names WHERE kind='alias' AND family=? AND repository=? AND role=?`,
		k.Family, repository, role).Scan(&address, &holder)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || holder == "" || holder == k.ID {
		return nil, err
	}
	if valid, err := holds(ctx, tx, now, k.Family, holder, repository, role); err != nil || !valid {
		return nil, err
	}
	sh, err := hostOf(ctx, tx, k.Family, k.ID)
	if err != nil {
		return nil, err
	}
	hh, err := hostOf(ctx, tx, k.Family, holder)
	if err != nil {
		return nil, err
	}
	var fenced int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM participant_fences WHERE address=? AND family=? AND session_id=?`, address, k.Family, k.ID).Scan(&fenced)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	result := &Succession{Result: "refused", Address: address, At: now}
	evidence := ""
	sameHost := sh.start != 0 && sh.pid == hh.pid && sh.start == hh.start
	samePane := sh.pane != "" && sh.socket == hh.socket && sh.pane == hh.pane
	other, err := holdsElsewhere(ctx, tx, now, k, sh, address)
	if err != nil {
		return nil, err
	}
	switch {
	case fenced == 1:
		result.Reason = "fenced"
	case subagent:
		result.Reason = "subagent"
	case sh.start == 0 || hh.start == 0:
		result.Reason = "no_host"
	case other:
		result.Reason = "other_participant"
	case sameHost:
		if left := s.guardLeft(Key{k.Family, holder}, now); left > 0 {
			result.Reason, result.RetryAfterMS = "holder_active", left
		} else {
			evidence = "same_host"
		}
	case samePane:
		if s.hostEnded(hh) {
			evidence = "same_pane"
		} else {
			result.Reason = "host_running"
		}
	default:
		result.Reason = "other_pane"
	}
	if result.Holder, err = peerOf(ctx, tx, k.Family, holder); err != nil {
		return nil, err
	}
	candidate, err := peerOf(ctx, tx, k.Family, k.ID)
	if err != nil {
		return nil, err
	}
	details, err := json.Marshal(map[string]any{"candidate": candidate, "evidence": evidence, "refused": result.Reason,
		"host_pid": sh.pid, "pane": sh.pane})
	if err != nil {
		return nil, err
	}
	if evidence != "" {
		result.Result, result.Reason = "succeeded", evidence
		return result, setHolder(ctx, tx, now, address, holder, k.ID, evidence, "session", string(details))
	}
	// A repeated refusal for the same reason and holder is recorded once.
	var stored string
	if err := tx.QueryRowContext(ctx, `SELECT succession FROM sessions WHERE family=? AND id=?`, k.Family, k.ID).Scan(&stored); err != nil {
		return nil, err
	}
	var last Succession
	if stored != "" && json.Unmarshal([]byte(stored), &last) == nil && last.Result == result.Result &&
		last.Reason == result.Reason && last.Address == result.Address && last.Holder == result.Holder {
		return result, nil
	}
	return result, participantEvent(ctx, tx, now, address, "", result.Holder, "succession_refused", "session", string(details))
}

// hostOf reads a session's host record.
func hostOf(ctx context.Context, tx *sql.Tx, family, id string) (host, error) {
	var h host
	err := tx.QueryRowContext(ctx, `SELECT host_pid,host_start,tmux_socket,tmux_pane FROM sessions WHERE family=? AND id=?`,
		family, id).Scan(&h.pid, &h.start, &h.socket, &h.pane)
	return h, err
}

// holdsElsewhere reports whether another active session of S's host holds a participant
// other than address: the host's evidence belongs to that participant, not to S's.
func holdsElsewhere(ctx context.Context, tx *sql.Tx, now int64, k Key, h host, address string) (bool, error) {
	if h.start == 0 {
		return false, nil
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM sessions s JOIN names n ON n.kind='alias' AND n.family=s.family AND n.holder_id=s.id
		WHERE s.family=? AND s.id!=? AND s.host_pid=? AND s.host_start=? AND s.retired_at=0 AND s.expires_at>? AND n.name!=? LIMIT 1`,
		k.Family, k.ID, h.pid, h.start, now, address).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
