package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The DeepSeek cycle adapter (#514) acts only through the session's DeepSeek Harness API, with
// the wake's pinned loopback origin and signed cookie: a DeepSeek session has no host process
// and no pane. Its reset creates a new harness session with an ID derived from the cycle, and
// its pickup is a /koinon-pickup prompt in it; the successor is that session, which registers
// with its own ID and reports the pickup through the command path. Native contracts: DeepSeek
// Harness 0.2.0-rc.2 (#514 comments 6436881952 and 6438221775).
//
//   - A new session takes the harness's default model; session/selectModel would also save a
//     new default, so the adapter never calls it. A cycle runs only while the requester's model
//     is that default (model_not_default otherwise).
//   - Admission pins the harness target, the requester's harness cwd and the model in the
//     cycle's adapter pin; every later Probe compares them.
//   - The quiet point is one stable read: the session list row, the projections at one
//     asOfSeq, and the history back to the newest turn's start, read again unchanged.

const (
	deepSeekCycleVersion = "dsh-session-1"
	deepSeekPickupText   = "/" + PickupSkillName
	// cycleReceiptRetry is when a created session that registered before the pickup's receipt
	// may register again (succession cycle_receipt_pending).
	cycleReceiptRetry = time.Second
	// deepSeekTurnMessages bounds one history page of the quiet point and the report's model
	// check; deepSeekCyclePages bounds the pages of one read.
	deepSeekTurnMessages = 200
	deepSeekCyclePages   = 4
)

var deepSeekCyclePlatforms = map[string]bool{"linux": true}

var errDeepSeekHarness = errors.New("harness_unreadable")

type deepSeekCycle struct{}

func init() { cycleAdapters["deepseek"] = deepSeekCycle{} }

// deepSeekModel is one model selection as the harness names it.
type deepSeekModel struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"reasoningEffort,omitempty"`
}

// deepSeekPin is a cycle's pinned facts of its requester (#514): fingerprints of the harness
// target and of the requester's harness cwd, and the model, which is the harness default.
type deepSeekPin struct {
	Target string        `json:"target"`
	Cwd    string        `json:"cwd"`
	Model  deepSeekModel `json:"model"`
}

func deepSeekPrint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func deepSeekTargetPrint(t wakeTarget) string { return deepSeekPrint("dsh", t.DSHURL, t.DSHCredentials) }

// deepSeekPinTarget reports whether a registered target is the one that pin names.
func deepSeekPinTarget(pin string, t wakeTarget) bool {
	var p deepSeekPin
	return json.Unmarshal([]byte(pin), &p) == nil && p.Target != "" && p.Target == deepSeekTargetPrint(t)
}

// deepSeekCycleID derives a UUID-shaped harness ID from the cycle, so that a request sent again
// names the same session or prompt.
func deepSeekCycleID(parts ...string) string {
	h := deepSeekPrint(parts...)
	b := []byte(h[:32])
	b[12] = '4'
	b[16] = "89ab"[strings.IndexByte("0123456789abcdef", b[16])%4]
	s := string(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func deepSeekSuccessorID(t CycleTarget) string { return "session-" + deepSeekCycleID("successor", t.CycleID) }

func deepSeekRequestID(t CycleTarget) string {
	return deepSeekCycleID("pickup", t.CycleID, time.UnixMilli(t.pickupAt).UTC().Format(time.RFC3339Nano))
}

// deepSeekRow is the allowlisted part of one session/list row.
type deepSeekRow struct {
	SessionID string        `json:"sessionId"`
	UpdatedAt openCodeCount `json:"updatedAt"`
	Running   *bool         `json:"running"`
	Cwd       string        `json:"cwd"`
}

// deepSeekCycleView is the allowlisted part of session/projections for the cycle.
type deepSeekCycleView struct {
	AsOfSeq openCodeCount `json:"asOfSeq"`
	Values  struct {
		Inbox *struct {
			NextTurn []deepSeekUserMessage `json:"next-turn"`
			NextStep []deepSeekUserMessage `json:"next-step"`
		} `json:"inbox"`
		UserQuestions *struct {
			Active []json.RawMessage `json:"active"`
		} `json:"userQuestions"`
		ModelSelection *struct {
			LastUsed *deepSeekModel `json:"lastUsed"`
			Next     *deepSeekModel `json:"next"`
		} `json:"modelSelection"`
	} `json:"values"`
}

// deepSeekUserMessage is the allowlisted part of a user message: its source, and its text only
// to compare with the pickup's.
type deepSeekUserMessage struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Source struct {
		Kind  string `json:"kind"`
		RPCID string `json:"rpcId"`
	} `json:"source"`
}

// deepSeekCycleEvent is the allowlisted part of one history event for the cycle.
type deepSeekCycleEvent struct {
	Type string          `json:"type"`
	Seq  openCodeCount   `json:"seq"`
	Data json.RawMessage `json:"data"`
}

type deepSeekHistoryPage struct {
	Records []struct {
		Type  string             `json:"type"`
		Event deepSeekCycleEvent `json:"event"`
	} `json:"records"`
	HasMore *bool `json:"hasMore"`
}

func (deepSeekCycle) Version() string { return deepSeekCycleVersion }

// client opens the requester's harness: its origin is pinned before the credential is read.
func (deepSeekCycle) client(ctx context.Context, t CycleTarget) (*deepSeekClient, error) {
	if t.deepSeek == nil {
		return nil, errDeepSeekHarness
	}
	client, err := newDeepSeekClient(ctx, *t.deepSeek)
	if err != nil {
		return nil, errDeepSeekHarness
	}
	return client, nil
}

// row reads the session list row of id; found is false when a verified list has no such row.
func deepSeekListRow(ctx context.Context, c *deepSeekClient, id string) (row deepSeekRow, found bool, err error) {
	var list struct {
		Items []deepSeekRow `json:"items"`
	}
	if err := c.call(ctx, "session/list", map[string]any{}, &list); err != nil {
		return row, false, err
	}
	for _, item := range list.Items {
		if item.SessionID == id {
			if found {
				return row, false, errDeepSeekHarness
			}
			row, found = item, true
		}
	}
	if found && (row.Running == nil || !row.UpdatedAt.ok) {
		return row, false, errDeepSeekHarness
	}
	return row, found, nil
}

// view reads the cycle's projections of id; found is false for a verified null.
func deepSeekView(ctx context.Context, c *deepSeekClient, id string) (v deepSeekCycleView, found bool, err error) {
	found, err = c.read(ctx, deepSeekProjectionsCall, map[string]any{"sessionId": id}, &v)
	if err == nil && found && !v.AsOfSeq.ok {
		return v, false, errDeepSeekHarness
	}
	return v, found, err
}

// history reads id's events back from asOfSeq, newest first, a whole page at a time, and calls
// visit for each until it returns false; the pages must be contiguous. It reports whether visit
// stopped the scan (false when the log's start was reached).
func deepSeekHistory(ctx context.Context, c *deepSeekClient, id string, asOfSeq int64, turnWindow bool, visit func(deepSeekCycleEvent) bool) (stopped bool, err error) {
	next := asOfSeq + 1
	for range deepSeekCyclePages {
		request := map[string]any{"address": map[string]any{"kind": "session", "sessionId": id}, "throughSeq": asOfSeq, "maxMessages": deepSeekTurnMessages}
		if turnWindow {
			request["turnWindow"] = map[string]any{"minMessages": 1, "minTurns": 1}
		}
		if next <= asOfSeq {
			request["beforeSeq"] = next
		}
		var page deepSeekHistoryPage
		if err := c.call(ctx, deepSeekPageCall, request, &page); err != nil {
			return false, err
		}
		if page.HasMore == nil || len(page.Records) == 0 && next > 0 {
			return false, errDeepSeekHarness
		}
		first := next - int64(len(page.Records))
		for i, record := range page.Records {
			if record.Type != "event" || !record.Event.Seq.ok || record.Event.Seq.value != first+int64(i) {
				return false, errDeepSeekHarness
			}
		}
		for i := len(page.Records) - 1; i >= 0; i-- {
			if !visit(page.Records[i].Event) {
				return true, nil
			}
		}
		next = first
		if !*page.HasMore || next <= 0 {
			return false, nil
		}
	}
	return false, errDeepSeekHarness
}

// quiet is the quiet point of session id: seen is "" when it is idle, with no queued input, no
// open question, its newest turn closed and no compaction running, in one stable read. users
// counts its user messages in that turn, or in its whole log when it had no turn.
func deepSeekQuiet(ctx context.Context, c *deepSeekClient, id string) (seen string, users int, err error) {
	before, found, err := deepSeekListRow(ctx, c, id)
	if err != nil || !found {
		return "", 0, errDeepSeekHarness
	}
	if *before.Running {
		return "status_busy", 0, nil
	}
	view, found, err := deepSeekView(ctx, c, id)
	if err != nil || !found || view.Values.Inbox == nil {
		return "", 0, errDeepSeekHarness
	}
	if len(view.Values.Inbox.NextTurn)+len(view.Values.Inbox.NextStep) > 0 {
		return "input_queued", 0, nil
	}
	if q := view.Values.UserQuestions; q != nil && len(q.Active) > 0 {
		return "question_open", 0, nil
	}
	closed, compactionEnded := false, false
	var open string
	_, err = deepSeekHistory(ctx, c, id, view.AsOfSeq.value, true, func(e deepSeekCycleEvent) bool {
		switch e.Type {
		case "compaction/end":
			compactionEnded = true
		case "compaction/start":
			if !compactionEnded {
				open = "compaction_running"
				return false
			}
			compactionEnded = false
		case "turn/end":
			closed = true
		case "turn/start":
			if !closed {
				open = "turn_open"
			}
			return false
		case "user/message":
			var m deepSeekUserMessage
			if json.Unmarshal(e.Data, &m) == nil && m.Source.Kind == "user" {
				users++
			}
		}
		return true
	})
	if err != nil {
		return "", 0, err
	}
	if open != "" {
		return open, users, nil
	}
	again, _, err := deepSeekView(ctx, c, id)
	if err != nil {
		return "", 0, err
	}
	after, found, err := deepSeekListRow(ctx, c, id)
	if err != nil || !found {
		return "", 0, errDeepSeekHarness
	}
	if again.AsOfSeq.value != view.AsOfSeq.value || *after.Running || after.UpdatedAt.value != before.UpdatedAt.value {
		return "harness_changed", users, nil
	}
	return "", users, nil
}

// requester reads the facts that the cycle pins: the requester's cwd and model, and the
// harness default, which must be the same model.
func (a deepSeekCycle) facts(ctx context.Context, c *deepSeekClient, t CycleTarget) (cwd string, pin deepSeekPin, reason string) {
	row, found, err := deepSeekListRow(ctx, c, t.SessionID)
	if err != nil || !found || row.Cwd == "" || !filepath.IsAbs(row.Cwd) {
		return "", pin, "harness_session_unknown"
	}
	view, found, err := deepSeekView(ctx, c, t.SessionID)
	if err != nil || !found || view.Values.ModelSelection == nil {
		return "", pin, "model_unknown"
	}
	model := view.Values.ModelSelection.Next
	if model == nil {
		model = view.Values.ModelSelection.LastUsed
	}
	var catalog struct {
		Default *deepSeekModel `json:"default"`
	}
	if model == nil || model.Provider == "" || model.Model == "" || c.call(ctx, "session/modelCatalog", map[string]any{}, &catalog) != nil || catalog.Default == nil {
		return "", pin, "model_unknown"
	}
	if *model != *catalog.Default {
		return "", pin, "model_not_default"
	}
	return row.Cwd, deepSeekPin{Target: deepSeekTargetPrint(*t.deepSeek), Cwd: deepSeekPrint("cwd", row.Cwd), Model: *model}, ""
}

// entry checks the koinon-pickup entry that the harness lists for the requester's session: it
// must be DeepSeek's entry, byte for byte, in whichever skill root the harness found it.
func (deepSeekCycle) entry(ctx context.Context, c *deepSeekClient, t CycleTarget) CycleProbe {
	step := pickupSkillStep(DeepSeekPickupSkillPath(filepath.Dir(t.deepSeek.DSHCredentials)))
	var list struct {
		Skills []struct {
			Name string `json:"name"`
			Path string `json:"path"`
		} `json:"skills"`
	}
	if c.call(ctx, "skills/list", map[string]any{"sessionId": t.SessionID}, &list) != nil {
		return CycleProbe{Reason: "pickup_entry_unverified"}
	}
	var path string
	for _, skill := range list.Skills {
		if skill.Name == PickupSkillName {
			if path != "" {
				return CycleProbe{Reason: "pickup_entry_unverified"}
			}
			path = skill.Path
		}
	}
	if path == "" {
		return CycleProbe{Reason: "pickup_entry_missing", Step: step}
	}
	data, err := os.ReadFile(path)
	if !filepath.IsAbs(path) || err != nil || !bytes.Equal(data, t.Pickup.Content) {
		return CycleProbe{Reason: "pickup_entry_differs", Step: step}
	}
	return CycleProbe{Entry: PickupSkillName}
}

// Probe decides whether the session's cycle can run, and returns the pin of its requester's
// facts; for an admitted cycle the facts must still be the pinned ones.
func (a deepSeekCycle) Probe(ctx context.Context, t CycleTarget) CycleProbe {
	if !deepSeekCyclePlatforms[runtime.GOOS] {
		return CycleProbe{Reason: "platform_unverified"}
	}
	if t.deepSeek == nil {
		return CycleProbe{Reason: "harness_target_missing"}
	}
	if !t.Holds {
		return CycleProbe{Reason: "participant_unheld"}
	}
	c, err := a.client(ctx, t)
	if err != nil {
		return CycleProbe{Reason: "harness_unreachable"}
	}
	defer c.close()
	_, pin, reason := a.facts(ctx, c, t)
	if reason != "" {
		return CycleProbe{Reason: reason, Step: deepSeekModelStep(reason)}
	}
	data, _ := json.Marshal(pin)
	if t.pin != "" && string(data) != t.pin {
		var pinned deepSeekPin
		if json.Unmarshal([]byte(t.pin), &pinned) == nil && pinned.Target == pin.Target && pinned.Cwd == pin.Cwd {
			return CycleProbe{Reason: "model_changed"}
		}
		return CycleProbe{Reason: "target_changed"}
	}
	probe := a.entry(ctx, c, t)
	probe.Pin = string(data)
	return probe
}

// deepSeekModelStep names what the person can do about a model reason.
func deepSeekModelStep(reason string) string {
	if reason == "model_not_default" {
		return "select this session's model as the default model in the DeepSeek Harness settings"
	}
	return ""
}

// Inspect reads the quiet point without input: the requester's before the reset, the created
// session's after it, which must also be empty.
func (a deepSeekCycle) Inspect(ctx context.Context, t CycleTarget, _ CycleFence) (PaneState, error) {
	c, err := a.client(ctx, t)
	if err != nil {
		return PaneState{}, err
	}
	defer c.close()
	id := t.SessionID
	if t.State == cycleResetVerified {
		id = t.SuccessorEpoch
	}
	seen, users, err := deepSeekQuiet(ctx, c, id)
	if err != nil {
		return PaneState{}, errDeepSeekHarness
	}
	if seen == "" && id == t.SuccessorEpoch && users > 0 {
		seen = "successor_not_empty"
	}
	return PaneState{Idle: seen == "", Seen: seen}, nil
}

// Create creates the successor session with the cycle's ID in the requester's cwd. A session
// with that ID that already exists is the created one only while it is empty in that cwd.
func (a deepSeekCycle) Create(ctx context.Context, t CycleTarget, fence CycleFence) (string, error) {
	c, err := a.client(ctx, t)
	if err != nil {
		return "", err
	}
	defer c.close()
	cwd, pin, reason := a.facts(ctx, c, t)
	data, _ := json.Marshal(pin)
	if reason != "" || string(data) != t.pin {
		return "", errDeepSeekHarness
	}
	id := deepSeekSuccessorID(t)
	if existing, err := a.created(ctx, c, id, pin.Cwd); err != nil || existing {
		if existing {
			return id, nil
		}
		return "", err
	}
	if err := fence(ctx); err != nil {
		return "", err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "session/create", map[string]any{"sessionId": id, "cwd": cwd}, &created); err != nil {
		return "", err
	}
	if created.SessionID != id {
		return "", errDeepSeekHarness
	}
	return id, nil
}

// created reports whether the harness holds id as the cycle's created session: in the pinned
// cwd and without a user message. A session that differs is an error; only a verified null
// proves that it does not exist.
func (deepSeekCycle) created(ctx context.Context, c *deepSeekClient, id, cwd string) (bool, error) {
	_, found, err := deepSeekView(ctx, c, id)
	if err != nil || !found {
		return false, err
	}
	row, listed, err := deepSeekListRow(ctx, c, id)
	if err != nil || !listed || deepSeekPrint("cwd", row.Cwd) != cwd {
		return false, errDeepSeekHarness
	}
	if _, users, err := deepSeekQuiet(ctx, c, id); err != nil || users > 0 {
		return false, errDeepSeekHarness
	}
	return true, nil
}

// Select has nothing to show: the harness has no server-side record of the session that a
// browser shows. The created session is in the harness's session list.
func (deepSeekCycle) Select(context.Context, CycleTarget, CycleFence) (bool, error) { return true, nil }

// NewEpoch proves the created session: it exists in the pinned cwd.
func (a deepSeekCycle) NewEpoch(ctx context.Context, t CycleTarget) (string, error) {
	if t.SuccessorEpoch == "" || t.SuccessorEpoch != deepSeekSuccessorID(t) {
		return "", nil
	}
	c, err := a.client(ctx, t)
	if err != nil {
		return "", err
	}
	defer c.close()
	var pin deepSeekPin
	if json.Unmarshal([]byte(t.pin), &pin) != nil {
		return "", errDeepSeekHarness
	}
	if _, found, err := deepSeekView(ctx, c, t.SuccessorEpoch); err != nil || !found {
		return "", err
	}
	row, listed, err := deepSeekListRow(ctx, c, t.SuccessorEpoch)
	if err != nil || !listed || deepSeekPrint("cwd", row.Cwd) != pin.Cwd {
		return "", errDeepSeekHarness
	}
	return t.SuccessorEpoch, nil
}

// SendPickup sends the /koinon-pickup prompt to the created session once: its request ID
// derives from the cycle and the intent, and the harness admits one prompt per request ID.
func (a deepSeekCycle) SendPickup(ctx context.Context, t CycleTarget, fence CycleFence) error {
	c, err := a.client(ctx, t)
	if err != nil {
		return err
	}
	defer c.close()
	if received, err := a.receipt(ctx, c, t); err != nil || received {
		return err
	}
	if _, pin, reason := a.facts(ctx, c, t); reason != "" || func() bool { data, _ := json.Marshal(pin); return string(data) != t.pin }() {
		return errDeepSeekHarness
	}
	if err := fence(ctx); err != nil {
		return err
	}
	var value struct {
		Accepted *bool `json:"accepted"`
	}
	request := map[string]any{"requestId": deepSeekRequestID(t), "sessionId": t.SuccessorEpoch, "mode": "queue",
		"content": []map[string]string{{"type": "text", "text": deepSeekPickupText}}, "clientTimeZone": "UTC"}
	if err := c.call(ctx, "session/prompt", request, &value); err != nil {
		return err
	}
	if value.Accepted == nil || !*value.Accepted {
		return errDeepSeekHarness
	}
	return nil
}

// PickupReceipt reads the created session back: true when its only user prompt is the pickup
// with the cycle's request ID, queued or entered.
func (a deepSeekCycle) PickupReceipt(ctx context.Context, t CycleTarget) (bool, error) {
	c, err := a.client(ctx, t)
	if err != nil {
		return false, err
	}
	defer c.close()
	return a.receipt(ctx, c, t)
}

func (deepSeekCycle) receipt(ctx context.Context, c *deepSeekClient, t CycleTarget) (bool, error) {
	view, found, err := deepSeekView(ctx, c, t.SuccessorEpoch)
	if err != nil || !found || view.Values.Inbox == nil {
		return false, errDeepSeekHarness
	}
	want := deepSeekRequestID(t)
	matched, others := 0, 0
	count := func(m deepSeekUserMessage) {
		if m.Source.Kind != "user" {
			return
		}
		if m.Source.RPCID == want && len(m.Content) == 1 && m.Content[0].Type == "text" && strings.TrimSpace(m.Content[0].Text) == deepSeekPickupText {
			matched++
		} else {
			others++
		}
	}
	for _, m := range append(view.Values.Inbox.NextTurn, view.Values.Inbox.NextStep...) {
		count(m)
	}
	_, err = deepSeekHistory(ctx, c, t.SuccessorEpoch, view.AsOfSeq.value, false, func(e deepSeekCycleEvent) bool {
		if e.Type == "user/message" {
			var m deepSeekUserMessage
			if json.Unmarshal(e.Data, &m) == nil {
				count(m)
			}
		}
		return true
	})
	if err != nil {
		return false, err
	}
	return matched == 1 && others == 0, nil
}

// ReportCheck reads the harness again for the successor's report: the requester's target is
// still the pinned one, and the created session's requests and replies came from the pinned
// model, with at least one reply.
func (a deepSeekCycle) ReportCheck(ctx context.Context, t CycleTarget) (string, error) {
	var pin deepSeekPin
	if t.deepSeek == nil || json.Unmarshal([]byte(t.pin), &pin) != nil {
		return "target_changed", nil
	}
	if pin.Target != deepSeekTargetPrint(*t.deepSeek) {
		return "target_changed", nil
	}
	c, err := a.client(ctx, t)
	if err != nil {
		return "", err
	}
	defer c.close()
	view, found, err := deepSeekView(ctx, c, t.SuccessorEpoch)
	if err != nil || !found {
		return "", errDeepSeekHarness
	}
	replies, other := 0, false
	stopped, err := deepSeekHistory(ctx, c, t.SuccessorEpoch, view.AsOfSeq.value, false, func(e deepSeekCycleEvent) bool {
		switch e.Type {
		case "request/header":
			var h struct {
				Header struct {
					Config struct {
						Provider string  `json:"provider"`
						Model    string  `json:"model"`
						Effort   *string `json:"reasoningEffort"`
					} `json:"config"`
				} `json:"header"`
			}
			effort := ""
			if json.Unmarshal(e.Data, &h) == nil && h.Header.Config.Effort != nil {
				effort = *h.Header.Config.Effort
			}
			other = other || h.Header.Config.Provider != pin.Model.Provider || h.Header.Config.Model != pin.Model.Model || effort != pin.Model.Effort
		case "assistant/message":
			var m struct {
				Message struct {
					Source struct {
						Provider string `json:"provider"`
						Model    string `json:"model"`
					} `json:"source"`
				} `json:"message"`
			}
			replies++
			other = other || json.Unmarshal(e.Data, &m) != nil || m.Message.Source.Provider != pin.Model.Provider || m.Message.Source.Model != pin.Model.Model
		}
		return true
	})
	if err != nil || stopped {
		return "", errDeepSeekHarness
	}
	if other {
		return "model_changed", nil
	}
	if replies == 0 {
		return "", errDeepSeekHarness
	}
	return "", nil
}

// The pane methods: a DeepSeek session has no pane, so they enter nothing.

func (deepSeekCycle) Clear(context.Context, CycleTarget, CycleFence) (bool, error) {
	return false, errDeepSeekHarness
}

func (deepSeekCycle) Pickup(context.Context, CycleTarget, CycleFence) (bool, error) {
	return false, errDeepSeekHarness
}

func (deepSeekCycle) Answerable(string) bool { return false }

func (deepSeekCycle) Answer(context.Context, CycleTarget, string, CycleFence) (bool, error) {
	return false, nil
}

func (deepSeekCycle) Dialog(context.Context, CycleTarget) (CycleDialog, error) {
	return CycleDialog{}, errDeepSeekHarness
}

func (deepSeekCycle) Choose(context.Context, CycleTarget, string, string, CycleFence) (bool, error) {
	return false, ErrCycleDialogChanged
}
