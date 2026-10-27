package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// DeepSeek context use (#514). The daemon reads it from the session's own harness, with the
// wake's pinned loopback origin and signed cookie, through two reads that start no agent
// (native evidence, DeepSeek Harness 0.2.0-rc.2):
//
//   - session/projections: the token meter's contextPressure {pressureTokens, contextWindow},
//     the model selection's lastUsed {provider, model} and the log position asOfSeq.
//   - session/page, back from asOfSeq: the assistant event whose usage gave pressureTokens.
//     Its time is the source time.
//
// The harness folds the pressure and the window as independent slots, so a reading needs the
// usage event's own prompt-side count to equal pressureTokens, and no model or context change
// after it. Only event types, seqs, times, usage counts and the model and context fields are
// decoded: message text and other projections never are.

const (
	deepSeekContextEvery    = 15 * time.Second
	deepSeekContextTimeout  = time.Second // for each request
	deepSeekContextScan     = 5 * time.Second
	deepSeekResponseBytes   = 8 << 20
	deepSeekScanBytes       = 16 << 20
	deepSeekPageMessages    = 8
	deepSeekPages           = 4
	deepSeekContextWorkers  = 8
	deepSeekContextConfirm  = time.Minute // an unchanged reading is sent again, to keep it fresh
	deepSeekContextSource   = "dsh_projections"
	deepSeekAssistantEvent  = "assistant/message"
	deepSeekAttemptEvent    = "assistant/attempt"
	deepSeekRequestContext  = "request/context"
	deepSeekModelSelection  = "model/selection"
	deepSeekProjectionsCall = "session/projections"
	deepSeekPageCall        = "session/page"
)

var errDeepSeekContext = errors.New("deepseek context unreadable")

// deepSeekUsage is the allowlisted part of one usage sample. inputTokens is required; an
// absent cache count is 0, as the harness counts it, and a present one must be valid.
type deepSeekUsage struct {
	Input      openCodeCount `json:"inputTokens"`
	CacheRead  openCodeCount `json:"cacheReadTokens"`
	CacheWrite openCodeCount `json:"cacheWriteTokens"`
}

// pressure is the prompt side of the usage, as the harness's token meter computes it.
func (u *deepSeekUsage) pressure() (int64, bool) {
	if !u.Input.ok {
		return 0, false
	}
	used := u.Input.value
	for _, count := range []openCodeCount{u.CacheRead, u.CacheWrite} {
		if !count.present {
			continue
		}
		if !count.ok || count.value > maxSafeInt-used {
			return 0, false
		}
		used += count.value
	}
	return used, true
}

// deepSeekEvent is the allowlisted part of one page record's event.
type deepSeekEvent struct {
	Type string        `json:"type"`
	Seq  openCodeCount `json:"seq"`
	Time openCodeCount `json:"time"`
	Data struct {
		Usage  json.RawMessage `json:"usage"`
		Stream []struct {
			Type  string          `json:"type"`
			Usage json.RawMessage `json:"usage"`
		} `json:"stream"`
	} `json:"data"`
}

// usage returns the event's usage sample as the harness's token meter takes it: the
// message's own usage, or else the last usage chunk of its stream. found is false for an
// event without one; a sample that cannot be read is an error.
func (e deepSeekEvent) usage() (usage deepSeekUsage, found bool, err error) {
	raw := e.Data.Usage
	if e.Type != deepSeekAssistantEvent {
		raw = nil
	}
	if len(raw) == 0 {
		for _, chunk := range e.Data.Stream {
			if chunk.Type == "usage" {
				raw = chunk.Usage
			}
		}
		if raw == nil {
			return usage, false, nil
		}
	}
	if json.Unmarshal(raw, &usage) != nil {
		return usage, true, errDeepSeekContext
	}
	return usage, true, nil
}

// deepSeekProjections is the allowlisted part of session/projections' value.
type deepSeekProjections struct {
	AsOfSeq openCodeCount `json:"asOfSeq"`
	Values  struct {
		ContextPressure *struct {
			PressureTokens openCodeCount `json:"pressureTokens"`
			ContextWindow  openCodeCount `json:"contextWindow"`
		} `json:"contextPressure"`
		ModelSelection *struct {
			LastUsed *struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"lastUsed"`
		} `json:"modelSelection"`
	} `json:"values"`
}

// deepSeekPage is the allowlisted part of session/page's value.
type deepSeekPage struct {
	Records []struct {
		Type  string        `json:"type"`
		Event deepSeekEvent `json:"event"`
	} `json:"records"`
	HasMore *bool `json:"hasMore"`
}

// deepSeekClient sends the harness's client requests to one pinned loopback origin.
type deepSeekClient struct {
	destination string
	authority   string
	secret      []byte
	http        *http.Client
	close       func()
	read        int // response bytes read, for the scan's bound
}

func newDeepSeekClient(ctx context.Context, target wakeTarget) (*deepSeekClient, error) {
	destination, pinned, err := deepSeekDestination(ctx, target.DSHURL)
	if err != nil {
		return nil, err
	}
	secret, err := deepSeekSecret(target.DSHCredentials)
	if err != nil {
		return nil, err
	}
	client, close := deepSeekHTTP(pinned)
	destination.Path = ""
	return &deepSeekClient{destination: destination.String(), authority: destination.Host, secret: secret, http: client, close: close}, nil
}

// call sends one request and decodes the value of its exact-200, ok result. A null value is
// errDeepSeekContext.
func (c *deepSeekClient) call(ctx context.Context, method string, request, value any) error {
	ctx, cancel := context.WithTimeout(ctx, deepSeekContextTimeout)
	defer cancel()
	rpcID, err := wakeID()
	if err != nil {
		return errDeepSeekContext
	}
	body, _ := json.Marshal(map[string]any{"type": "client-request", "rpcId": rpcID, "method": method, "payload": map[string]any{"args": map[string]any{"request": request}}})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.destination+"/api/"+method, bytes.NewReader(body))
	if err != nil {
		return errDeepSeekContext
	}
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(deepSeekCookie(c.secret, c.authority, time.Now()))
	response, err := c.http.Do(r)
	if err != nil {
		return errDeepSeekContext
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errDeepSeekContext
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, deepSeekResponseBytes+1))
	c.read += len(data)
	if err != nil || len(data) > deepSeekResponseBytes || c.read > deepSeekScanBytes {
		return errDeepSeekContext
	}
	var document struct {
		Type   string `json:"type"`
		RPCID  string `json:"rpcId"`
		Result *struct {
			OK    *bool           `json:"ok"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(data, &document) != nil || document.Type != "server-response" || document.RPCID != rpcID ||
		document.Result == nil || document.Result.OK == nil || !*document.Result.OK ||
		len(document.Result.Value) == 0 || bytes.Equal(document.Result.Value, []byte("null")) {
		return errDeepSeekContext
	}
	if json.Unmarshal(document.Result.Value, value) != nil {
		return errDeepSeekContext
	}
	return nil
}

// deepSeekContext reads the model and context of one DeepSeek session. Before its first
// usage it returns nothing; an incoherent or unreadable reading is an error, never a
// fallback to an older one.
func deepSeekContext(ctx context.Context, target wakeTarget, id string) (model, usage *ObservedValue, err error) {
	ctx, cancel := context.WithTimeout(ctx, deepSeekContextScan)
	defer cancel()
	client, err := newDeepSeekClient(ctx, target)
	if err != nil {
		return nil, nil, errDeepSeekContext
	}
	defer client.close()
	var projections deepSeekProjections
	if err := client.call(ctx, deepSeekProjectionsCall, map[string]any{"sessionId": id}, &projections); err != nil {
		return nil, nil, err
	}
	pressure := projections.Values.ContextPressure
	if pressure == nil || !pressure.PressureTokens.present {
		return nil, nil, nil
	}
	selection := projections.Values.ModelSelection
	if !projections.AsOfSeq.ok || !pressure.PressureTokens.ok || !pressure.ContextWindow.ok ||
		pressure.ContextWindow.value <= 0 || selection == nil || selection.LastUsed == nil {
		return nil, nil, errDeepSeekContext
	}
	name := selection.LastUsed.Provider + "/" + selection.LastUsed.Model
	if selection.LastUsed.Provider == "" || selection.LastUsed.Model == "" || !modelID.MatchString(name) {
		return nil, nil, errDeepSeekContext
	}
	at, err := deepSeekUsageTime(ctx, client, id, projections.AsOfSeq.value, pressure.PressureTokens.value)
	if err != nil {
		return nil, nil, err
	}
	used, limit := pressure.PressureTokens.value, pressure.ContextWindow.value
	return &ObservedValue{Source: deepSeekContextSource, At: at, ID: name},
		&ObservedValue{Source: deepSeekContextSource, At: at, LimitTokens: &limit, UsedTokens: &used, Epoch: id}, nil
}

// deepSeekUsageTime scans the session's log back from asOfSeq to the newest event with a
// usage sample and returns its time. The sample's prompt side must be pressure, and no model
// or context change may follow it; a bounded scan that finds no sample is an error.
func deepSeekUsageTime(ctx context.Context, client *deepSeekClient, id string, asOfSeq, pressure int64) (int64, error) {
	next := asOfSeq + 1 // the exclusive end of the next page
	for range deepSeekPages {
		request := map[string]any{"address": map[string]any{"kind": "session", "sessionId": id}, "throughSeq": asOfSeq, "maxMessages": deepSeekPageMessages}
		if next <= asOfSeq {
			request["beforeSeq"] = next
		}
		var page deepSeekPage
		if err := client.call(ctx, deepSeekPageCall, request, &page); err != nil {
			return 0, err
		}
		// A page is a contiguous run of seqs that ends just before next.
		if len(page.Records) == 0 || page.HasMore == nil {
			return 0, errDeepSeekContext
		}
		for i := len(page.Records) - 1; i >= 0; i-- {
			record := page.Records[i]
			event := record.Event
			if record.Type != "event" || !event.Seq.ok || event.Seq.value != next-1 || !event.Time.ok || event.Time.value <= 0 {
				return 0, errDeepSeekContext
			}
			next--
			if event.Type == deepSeekRequestContext || event.Type == deepSeekModelSelection {
				return 0, errDeepSeekContext
			}
			if event.Type != deepSeekAssistantEvent && event.Type != deepSeekAttemptEvent {
				continue
			}
			sample, found, err := event.usage()
			if err != nil {
				return 0, err
			}
			if !found {
				continue
			}
			if got, ok := sample.pressure(); !ok || got != pressure {
				return 0, errDeepSeekContext
			}
			return event.Time.value, nil
		}
		if !*page.HasMore || next <= 0 {
			return 0, errDeepSeekContext
		}
	}
	return 0, errDeepSeekContext
}

// deepSeekContextRound reads every active DeepSeek session with a harness target once, at
// most deepSeekContextWorkers at a time, and returns when all reads ended. A reading is
// recorded when it changed, and an unchanged one once a minute; a failed read records
// nothing, so it confirms no earlier reading.
func (s *Store) deepSeekContextRound(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,wake_target FROM sessions WHERE family='deepseek' AND retired_at=0 AND expires_at>?`, s.now().UnixMilli())
	if err != nil {
		return
	}
	type harnessed struct {
		id     string
		target wakeTarget
	}
	var sessions []harnessed
	for rows.Next() && len(sessions) < maxObservedSessions {
		var id string
		var raw []byte
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var target wakeTarget
		if json.Unmarshal(raw, &target) == nil && target.DSHURL != "" && target.DSHCredentials != "" && nativeEpoch.MatchString(id) {
			sessions = append(sessions, harnessed{id, target})
		}
	}
	rows.Close()
	live := map[Key]bool{}
	for _, session := range sessions {
		live[Key{"deepseek", session.id}] = true
	}
	s.deepSeekContext.forget(live)
	slots := make(chan struct{}, deepSeekContextWorkers)
	var wg sync.WaitGroup
	for _, session := range sessions {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-slots; wg.Done() }()
			model, usage, err := deepSeekContext(ctx, session.target, session.id)
			if err == nil && usage != nil {
				s.recordReading(ctx, &s.deepSeekContext, Key{"deepseek", session.id}, model, usage, deepSeekContextConfirm)
			}
		}()
	}
	wg.Wait()
}

// maintainDeepSeekContext runs the DeepSeek context rounds on their own timer, apart from the
// cycle controller's tick. Rounds never overlap.
func (d *Daemon) maintainDeepSeekContext() {
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
	ticker := time.NewTicker(deepSeekContextEvery)
	defer ticker.Stop()
	for {
		d.store.deepSeekContextRound(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
