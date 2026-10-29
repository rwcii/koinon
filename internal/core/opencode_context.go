package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// OpenCode context use (#513). The daemon reads it from a launched OpenCode session's own
// loopback server, with the launch password, as it reads the session's activity. The newest
// finished assistant message names its token counts and its model, and the server's provider
// list names that model's context limit (native evidence, opencode 1.18.35). The session's
// own token counts are a sum over its turns, not its context. Only those counts and IDs are
// decoded: the message parts that the server sends with them are never kept or logged.

const (
	openCodeContextEvery   = 15 * time.Second
	openCodeContextConfirm = time.Minute // an unchanged reading is sent again, to keep it fresh
	openCodeContextTimeout = time.Second // for each request
	openCodeMessagePage    = 4
	openCodeResponseBytes  = 8 << 20
	openCodeContextWorkers = 8
)

var errOpenCodeContext = errors.New("opencode context unreadable")

// openCodeCount is one count as the server sent it: present, and valid only as a JSON
// integer from 0 to maxSafeInt. A fraction, an exponent, a sign, a string or null is not.
type openCodeCount struct {
	present, ok bool
	value       int64
}

func (c *openCodeCount) UnmarshalJSON(data []byte) error {
	value, err := strconv.ParseInt(string(bytes.TrimSpace(data)), 10, 64)
	c.present, c.value, c.ok = true, value, err == nil && value >= 0 && value <= maxSafeInt
	return nil
}

// openCodeMessage is the allowlisted part of one message: its parts are not decoded.
type openCodeMessage struct {
	Info struct {
		ID         string `json:"id"`
		Role       string `json:"role"`
		Finish     string `json:"finish"`
		ProviderID string `json:"providerID"`
		ModelID    string `json:"modelID"`
		Time       struct {
			Created   openCodeCount `json:"created"`
			Completed openCodeCount `json:"completed"`
		} `json:"time"`
		Tokens *struct {
			Total     openCodeCount `json:"total"`
			Input     openCodeCount `json:"input"`
			Output    openCodeCount `json:"output"`
			Reasoning openCodeCount `json:"reasoning"`
			Cache     *struct {
				Read  openCodeCount `json:"read"`
				Write openCodeCount `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
	} `json:"info"`
}

// openCodeProviders is the allowlisted part of the provider list: provider and model IDs and
// each model's context limit, never a provider's key, options or environment.
type openCodeProviders struct {
	Providers []struct {
		ID     string `json:"id"`
		Models map[string]struct {
			ID    string `json:"id"`
			Limit struct {
				Context openCodeCount `json:"context"`
			} `json:"limit"`
		} `json:"models"`
	} `json:"providers"`
}

// openCodeGet reads one exact-200 JSON response of at most openCodeResponseBytes from the
// launched session's server.
func openCodeGet(ctx context.Context, client *http.Client, target LaunchTarget, path string, value any) error {
	ctx, cancel := context.WithTimeout(ctx, openCodeContextTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.Address+path, nil)
	if err != nil {
		return errOpenCodeContext
	}
	request.SetBasicAuth("opencode", target.Password)
	response, err := client.Do(request)
	if err != nil {
		return errOpenCodeContext
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errOpenCodeContext
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, openCodeResponseBytes+1))
	if err != nil || len(data) > openCodeResponseBytes || json.Unmarshal(data, value) != nil {
		return errOpenCodeContext
	}
	return nil
}

// openCodeContext reads the model and context of one OpenCode session. Without a finished
// assistant message it returns nothing. A newest finished message that cannot be read is an
// error, never a fallback to an older one. last is that message's ID when it is also the
// session's newest message, by its creation time, and "" otherwise (DECISIONS.md#PULLED-IDLE-569).
func openCodeContext(ctx context.Context, target LaunchTarget, id string) (model, usage *ObservedValue, last string, err error) {
	client, close := httpClient()
	defer close()
	var messages []openCodeMessage
	if err := openCodeGet(ctx, client, target, "/session/"+url.PathEscape(id)+"/message?limit="+strconv.Itoa(openCodeMessagePage), &messages); err != nil {
		return nil, nil, "", err
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
	if newest < 0 {
		return nil, nil, "", nil
	}
	info := messages[newest].Info
	tokens := info.Tokens
	if ties > 0 || tokens == nil || tokens.Cache == nil {
		return nil, nil, "", errOpenCodeContext
	}
	var used int64
	for _, count := range []openCodeCount{tokens.Input, tokens.Output, tokens.Reasoning, tokens.Cache.Read, tokens.Cache.Write} {
		if !count.ok || count.value > maxSafeInt-used {
			return nil, nil, "", errOpenCodeContext
		}
		used += count.value
	}
	if tokens.Total.present && (!tokens.Total.ok || tokens.Total.value != used) {
		return nil, nil, "", errOpenCodeContext
	}
	name := info.ProviderID + "/" + info.ModelID
	if info.ProviderID == "" || info.ModelID == "" || !modelID.MatchString(name) {
		return nil, nil, "", errOpenCodeContext
	}
	var providers openCodeProviders
	if err := openCodeGet(ctx, client, target, "/config/providers", &providers); err != nil {
		return nil, nil, "", err
	}
	var limit int64
	found := 0
	for _, p := range providers.Providers {
		if p.ID != info.ProviderID {
			continue
		}
		found++
		if m, ok := p.Models[info.ModelID]; ok && m.ID == info.ModelID && m.Limit.Context.ok && m.Limit.Context.value > 0 {
			limit = m.Limit.Context.value
		}
	}
	if found != 1 || limit == 0 {
		return nil, nil, "", errOpenCodeContext
	}
	at := info.Time.Completed.value
	return &ObservedValue{Source: "opencode_server", At: at, ID: name},
		&ObservedValue{Source: "opencode_server", At: at, LimitTokens: &limit, UsedTokens: &used, Epoch: id}, newestMessage(messages, newest), nil
}

// newestMessage is the ID of message i when it was created after every other message of the
// page, and "" otherwise: a later user message or an unfinished reply is newer context.
func newestMessage(messages []openCodeMessage, i int) string {
	created := messages[i].Info.Time.Created
	if messages[i].Info.ID == "" || !created.ok {
		return ""
	}
	for j, m := range messages {
		if j != i && (!m.Info.Time.Created.ok || m.Info.Time.Created.value >= created.value) {
			return ""
		}
	}
	return messages[i].Info.ID
}

// openCodeSent is the last reading that the poller recorded for one session.
type openCodeSent struct {
	model, usage ObservedValue
	mark         int64 // the source's own position of the reading, such as a usage event's seq, or 0
	current      bool  // the reading's proof (DECISIONS.md#PULLED-IDLE-569)
	at           time.Time
}

type openCodeContextState struct {
	mu   sync.Mutex
	sent map[Key]openCodeSent
}

// openCodeContextRound reads every active launched OpenCode session once, at most
// openCodeContextWorkers at a time, and returns when all reads ended. A reading is recorded
// when it changed, and an unchanged one once a minute; a failed read records nothing, so it
// confirms no earlier reading, and it ends the reading's proof at once.
func (s *Store) openCodeContextRound(ctx context.Context) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `SELECT id,wake_target FROM sessions WHERE family='opencode' AND retired_at=0 AND expires_at>?`, now.UnixMilli())
	if err != nil {
		return
	}
	type launched struct {
		id, launch string
	}
	var sessions []launched
	for rows.Next() && len(sessions) < maxObservedSessions {
		var id string
		var raw []byte
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var wake struct {
			LaunchID string `json:"launch_id"`
		}
		if json.Unmarshal(raw, &wake) == nil && wake.LaunchID != "" {
			sessions = append(sessions, launched{id, wake.LaunchID})
		}
	}
	rows.Close()
	live := map[Key]bool{}
	for _, session := range sessions {
		live[Key{"opencode", session.id}] = true
	}
	s.openCodeContext.forget(live)
	slots := make(chan struct{}, openCodeContextWorkers)
	var wg sync.WaitGroup
	for _, session := range sessions {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			s.readOpenCodeContext(ctx, Key{"opencode", session.id}, session.launch)
		}()
	}
	wg.Wait()
}

// readOpenCodeContext reads one session's context. The reading is current when its message is
// the session's newest, `GET /session/status` then shows that exact session idle, and a second
// read gives the same reading and newest message (DECISIONS.md#PULLED-IDLE-569).
func (s *Store) readOpenCodeContext(ctx context.Context, k Key, launch string) {
	target, err := s.openCodeTarget(ctx, launch)
	if err != nil {
		s.dropReading(&s.openCodeContext, k)
		return
	}
	model, usage, last, err := openCodeContext(ctx, target, k.ID)
	if err != nil || usage == nil {
		s.dropReading(&s.openCodeContext, k)
		return
	}
	current := false
	if last != "" {
		status, cancel := context.WithTimeout(ctx, openCodeContextTimeout)
		state, err := openCodeStatus(status, target, k.ID)
		cancel()
		if err == nil && state == "idle" {
			_, again, lastAgain, err := openCodeContext(ctx, target, k.ID)
			current = err == nil && again != nil && lastAgain == last && sameReading(*again, *usage)
		}
	}
	s.recordReading(ctx, &s.openCodeContext, k, model, usage, 0, current, openCodeContextConfirm)
}

// recordReading records a polled reading when it changed, and an unchanged one again once
// confirm passed (#513, #514). An unchanged reading keeps its source time. mark is the
// reading's position in its source: a new mark is a new reading even with the same values.
// current is the reading's proof; a change of it is recorded at once (DECISIONS.md#PULLED-IDLE-569).
func (s *Store) recordReading(ctx context.Context, state *openCodeContextState, k Key, model, usage *ObservedValue, mark int64, current bool, confirm time.Duration) {
	now := s.now()
	state.mu.Lock()
	last, found := state.sent[k]
	state.mu.Unlock()
	if found && *model == last.model && sameReading(*usage, last.usage) && mark == last.mark && current == last.current &&
		now.Sub(last.at) < confirm && !now.Before(last.at) {
		return
	}
	newestModel, proved := *model, *usage
	newestModel.newest, proved.current, proved.newest = true, current, true
	if s.Observe(ctx, Observation{Caller: k, Model: &newestModel, Context: &proved}) != nil || !s.holdsReading(k, *usage) {
		s.dropReading(state, k)
		return
	}
	state.mu.Lock()
	if state.sent == nil {
		state.sent = map[Key]openCodeSent{}
	}
	state.sent[k] = openCodeSent{*model, *usage, mark, current, now}
	state.mu.Unlock()
}

// holdsReading reports whether the store holds reading as k's context. A reading that the
// store did not take, such as one older than the stored reading, leaves no proof standing.
func (s *Store) holdsReading(k Key, reading ObservedValue) bool {
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	v, found := s.observed.byKey[k]["context"]
	return found && v.Source == reading.Source && v.At == reading.At && v.epoch == reading.Epoch &&
		v.UsedTokens != nil && v.LimitTokens != nil && *v.UsedTokens == *reading.UsedTokens && *v.LimitTokens == *reading.LimitTokens
}

// dropReading forgets the last recorded reading of k, so that the next one is recorded, and
// ends the stored reading's proof at once (DECISIONS.md#PULLED-IDLE-569).
func (s *Store) dropReading(state *openCodeContextState, k Key) {
	state.mu.Lock()
	delete(state.sent, k)
	state.mu.Unlock()
	s.clearContextProof(k)
}

// forget drops the readings of sessions that are not live.
func (state *openCodeContextState) forget(live map[Key]bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	for k := range state.sent {
		if !live[k] {
			delete(state.sent, k)
		}
	}
}

// sameReading compares two context readings, their source times and epochs included.
func sameReading(a, b ObservedValue) bool {
	return a.Source == b.Source && a.At == b.At && a.Epoch == b.Epoch &&
		*a.LimitTokens == *b.LimitTokens && *a.UsedTokens == *b.UsedTokens
}

// maintainOpenCodeContext runs the OpenCode context rounds on their own timer, apart from the
// cycle controller's tick: a slow server delays no cycle step. Rounds never overlap.
func (d *Daemon) maintainOpenCodeContext() {
	defer d.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-d.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	ticker := time.NewTicker(openCodeContextEvery)
	defer ticker.Stop()
	for {
		d.store.openCodeContextRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
