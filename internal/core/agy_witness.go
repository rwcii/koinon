package core

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/rwcii/koinon/internal/platform"
)

// Host-linked conversation witnesses for the agy (#512) and Grok (#515) cycles. agy starts a new
// conversation at /clear, and Grok a new session at /new, in the same host process, before any
// turn or Koinon tool call, and the status-line command, which runs as a descendant of the
// host, then reports the new conversation_id or session_id (native evidence, agy 1.4.7 and
// Grok Build 1.0.50). A witness ties one such report to the pinned host of one cycle. It is
// kept in memory only, from the cycle's clear intent until the cycle ends; it registers no
// session and moves no holder, claim or cursor.

// agyWitnessMax bounds the conversations kept per cycle; a cycle that sees more is ambiguous.
const agyWitnessMax = 8

// agyWitnessStates are the cycle states that keep witnesses: from the clear intent, so that a
// report that arrives before the clear's receipt is recorded is kept, until the pickup.
var agyWitnessStates = []string{cycleClearIntent, cycleClearReceived, cycleResetVerified, cyclePickupIntent,
	cycleWaitingPickup, cycleWaitingPrompt, cyclePromptIntent}

// agyWitness is one conversation that the host's status line reported during a cycle.
type agyWitness struct {
	ID    string
	First int64 // the daemon's receipt of its first report, Unix milliseconds
	Last  int64 // and of its latest
}

// agyWitnesses are one cycle's witnesses in order of first receipt; Overflow reports that more
// conversations came than the bound keeps.
type agyWitnesses struct {
	List     []agyWitness
	Overflow bool
}

type agyWitnessState struct {
	mu      sync.Mutex
	byCycle map[string]agyWitnesses
}

// of returns a copy of one cycle's witnesses.
func (w *agyWitnessState) of(cycle string) agyWitnesses {
	w.mu.Lock()
	defer w.mu.Unlock()
	kept := w.byCycle[cycle]
	return agyWitnesses{List: slices.Clone(kept.List), Overflow: kept.Overflow}
}

// keep drops the witnesses of every cycle that is not in live.
func (w *agyWitnessState) keep(live map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.byCycle {
		if !live[id] {
			delete(w.byCycle, id)
		}
	}
}

// add records a report of conversation for cycle at now.
func (w *agyWitnessState) add(cycle, conversation string, now int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.byCycle == nil {
		w.byCycle = map[string]agyWitnesses{}
	}
	kept := w.byCycle[cycle]
	for i := range kept.List {
		if kept.List[i].ID == conversation {
			kept.List[i].Last = now
			w.byCycle[cycle] = kept
			return
		}
	}
	if len(kept.List) >= agyWitnessMax {
		kept.Overflow = true
	} else {
		kept.List = append(kept.List, agyWitness{ID: conversation, First: now, Last: now})
	}
	w.byCycle[cycle] = kept
}

// AgyWitness is one status-line report: the conversation and the reporting hook process.
type AgyWitness struct {
	Conversation string              `json:"conversation"`
	Reporter     ObservationReporter `json:"reporter"`
}

func agyWitnessRefusal() error {
	return Refusal{Code: "witness_host_unverified", Message: "the report cannot be tied to the pinned host of a cycle"}
}

// agyPinned is the pinned host of a cycle that keeps witnesses.
type agyPinned struct {
	id         string
	pid, start int64
}

// WitnessAgy keeps a status-line report for the agy cycle whose pinned host is the reporter's
// nearest native host.
func (s *Store) WitnessAgy(ctx context.Context, w AgyWitness) error { return s.witness(ctx, "agy", w) }

// WitnessGrok keeps a status-line report for the Grok cycle whose pinned host is the
// reporter's nearest native host; Conversation is the Grok session_id (#515).
func (s *Store) WitnessGrok(ctx context.Context, w AgyWitness) error {
	return s.witness(ctx, "grok", w)
}

// witness keeps a status-line report for the family's cycle whose pinned host is the
// reporter's nearest native host. The reporter's start is read before and after the daemon
// reads the ancestry itself. The nearest ancestor that hosts a live session or a cycle, or runs
// an agent CLI, must be that cycle's host, with its pinned start. A conversation that is already
// a session must be that host's own. Without such a cycle the report changes nothing.
func (s *Store) witness(ctx context.Context, family string, w AgyWitness) error {
	if !nativeEpoch.MatchString(w.Conversation) || w.Reporter.PID <= 1 || w.Reporter.Start <= 0 {
		return ErrInvalid
	}
	cycles, err := s.witnessCycles(ctx, family)
	if err != nil || len(cycles) == 0 {
		return err
	}
	if s.processStart == nil || s.processParents == nil || s.processCommand == nil {
		return agyWitnessRefusal()
	}
	if start, err := s.processStart(w.Reporter.PID); err != nil || start != w.Reporter.Start {
		return agyWitnessRefusal()
	}
	parents, err := s.processParents()
	if err != nil {
		return agyWitnessRefusal()
	}
	now := s.now().UnixMilli()
	var host *agyPinned
	seen := map[int]bool{}
	for pid, steps := parents[w.Reporter.PID], 0; pid > 1 && steps < 256 && host == nil; pid, steps = parents[pid], steps+1 {
		if seen[pid] {
			return agyWitnessRefusal()
		}
		seen[pid] = true
		var matched []agyPinned
		for _, c := range cycles {
			if c.pid == int64(pid) {
				matched = append(matched, c)
			}
		}
		var sessions int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE host_pid=? AND retired_at=0 AND expires_at>?`,
			pid, now).Scan(&sessions); err != nil {
			return err
		}
		switch {
		case len(matched) > 1:
			return agyWitnessRefusal()
		case len(matched) == 1:
			host = &matched[0]
		case sessions > 0:
			// The nearest native host is another session's: never pass it for a farther one.
			return agyWitnessRefusal()
		default:
			// An unregistered nested agent CLI is islanded, not a reporter for a farther host.
			executable, args, err := s.processCommand(pid)
			if err != nil || platform.AgentCommand(executable, args) {
				return agyWitnessRefusal()
			}
		}
	}
	if host == nil {
		return agyWitnessRefusal()
	}
	// A known conversation, registered or retired, belongs to its own host: a report never
	// passes another host's conversation as this host's new one.
	var hostPID, hostStart int64
	switch err := s.db.QueryRowContext(ctx, `SELECT host_pid,host_start FROM sessions WHERE family=? AND id=?`, family, w.Conversation).
		Scan(&hostPID, &hostStart); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	case hostPID != host.pid || hostStart != host.start:
		return agyWitnessRefusal()
	}
	if start, err := s.processStart(int(host.pid)); err != nil || start != host.start {
		return agyWitnessRefusal()
	}
	// The reporter must still be the same process after the ancestry read.
	if start, err := s.processStart(w.Reporter.PID); err != nil || start != w.Reporter.Start {
		return agyWitnessRefusal()
	}
	s.agyWitness.add(host.id, w.Conversation, now)
	return nil
}

// witnessCycles lists the family's cycles that keep witnesses, and drops the witnesses of every
// other cycle of a witnessed family.
func (s *Store) witnessCycles(ctx context.Context, family string) ([]agyPinned, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(agyWitnessStates)), ",")
	args := []any{}
	for _, state := range agyWitnessStates {
		args = append(args, state)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT cycle_id,family,host_pid,host_start FROM context_cycles WHERE family IN ('agy','grok') AND state IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cycles []agyPinned
	live := map[string]bool{}
	for rows.Next() {
		var c agyPinned
		var kind string
		if err := rows.Scan(&c.id, &kind, &c.pid, &c.start); err != nil {
			return nil, err
		}
		if kind == family {
			cycles = append(cycles, c)
		}
		live[c.id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.agyWitness.keep(live)
	return cycles, nil
}

func (d *Daemon) agyWitnessRoutes(mux *http.ServeMux) {
	for family, keep := range map[string]func(context.Context, AgyWitness) error{"agy": d.store.WitnessAgy, "grok": d.store.WitnessGrok} {
		mux.HandleFunc("POST /v1/cycles/"+family+"-witness", func(w http.ResponseWriter, r *http.Request) {
			var request AgyWitness
			if err := decode(w, r, &request); err != nil {
				failure(w, err)
				return
			}
			if err := keep(r.Context(), request); err != nil {
				failure(w, err)
				return
			}
			respond(w, 200, map[string]any{"ok": true})
		})
	}
}
