package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The DeepSeek cycle adapter (#514) against a synthetic DeepSeek Harness with several sessions,
// which also creates sessions and admits prompts as the harness does: a chosen session ID with
// the same cwd is created once. A prompt sent again with the same request ID is admitted again,
// as the harness does: the adapter never sends one twice.

const dshRequester = "session-synthetic-requester"

var dshModel = deepSeekModel{Provider: "openrouter", Model: "deepseek/deepseek-v4-flash"}

type dshSession struct {
	cwd       string
	running   bool
	events    []string // event i has seq i
	nextTurn  []string
	questions []string
	lastUsed  *deepSeekModel
}

type dshFake struct {
	mu       sync.Mutex
	key      []byte
	sessions map[string]*dshSession
	def      deepSeekModel
	skills   []string // skills/list entries as JSON
	calls    map[string]int
	status   map[string]int  // a status other than 200 for a method
	lose     map[string]bool // the method takes effect, then answers 500
	prompts  map[string]bool
	pageSize int
	// afterList runs after each session/list answer: a change between the quiet point's reads.
	afterList func(*dshFake)
	home      string
}

func newDSHFake(t *testing.T) (*dshFake, wakeTarget) {
	t.Helper()
	f := &dshFake{key: []byte(strings.Repeat("k", 32)), sessions: map[string]*dshSession{}, def: dshModel, calls: map[string]int{},
		status: map[string]int{}, lose: map[string]bool{}, prompts: map[string]bool{}, pageSize: 4, home: t.TempDir()}
	credential := filepath.Join(f.home, ".credentials.yaml")
	if err := os.WriteFile(credential, []byte("client-connection/browser-session:\n  kind: grant\n  version: 1\n  secret: "+base64.RawURLEncoding.EncodeToString(f.key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := dshModel
	f.sessions[dshRequester] = &dshSession{cwd: t.TempDir(), lastUsed: &model}
	f.turn(dshRequester, dshModel)
	f.installSkill(t, DeepSeekPickupSkillPath(f.home), DeepSeekPickupSkill())
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	previous := deepSeekCyclePlatforms
	deepSeekCyclePlatforms = map[string]bool{runtime.GOOS: true}
	t.Cleanup(func() { deepSeekCyclePlatforms = previous })
	return f, wakeTarget{DSHURL: server.URL, DSHCredentials: credential}
}

// installSkill writes an entry and lists it for every session, as skills/list does.
func (f *dshFake) installSkill(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	f.skills = append(f.skills, fmt.Sprintf(`{"name":"review","path":"/synthetic/review/SKILL.md"},{"name":%q,"path":%q}`, PickupSkillName, path))
}

func (f *dshFake) add(id, members string) {
	s := f.sessions[id]
	s.events = append(s.events, fmt.Sprintf(`{%s,"seq":%d,"time":%d}`, members, len(s.events), 1793077135000+int64(len(s.events))))
}

// turn adds one closed turn: a user prompt, the request header and a reply from model.
func (f *dshFake) turn(id string, model deepSeekModel) {
	f.add(id, `"type":"turn/start","data":{}`)
	f.add(id, `"type":"user/message","data":{"content":[{"type":"text","text":"SENTINEL"}],"source":{"kind":"user","rpcId":"synthetic-rpc"}}`)
	f.add(id, fmt.Sprintf(`"type":"request/header","data":{"header":{"config":{"provider":%q,"model":%q},"reason":"initial"}}`, model.Provider, model.Model))
	f.add(id, fmt.Sprintf(`"type":"assistant/message","data":{"message":{"content":"SENTINEL","source":{"kind":"model","provider":%q,"model":%q}}}`, model.Provider, model.Model))
	f.add(id, `"type":"turn/end","data":{}`)
}

func (f *dshFake) set(change func(*dshFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *dshFake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *dshFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	f.calls[method]++
	audience := sha256.Sum256([]byte(r.Host))
	cookie, err := r.Cookie("dsh-auth-" + base64.RawURLEncoding.EncodeToString(audience[:]))
	var parts []string
	if err == nil {
		parts = strings.Split(cookie.Value, ".")
	}
	mac := hmac.New(sha256.New, f.key)
	if len(parts) == 3 {
		mac.Write([]byte(parts[1]))
	}
	if len(parts) != 3 {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if signature, _ := base64.RawURLEncoding.DecodeString(parts[2]); !hmac.Equal(mac.Sum(nil), signature) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var document struct {
		RPCID   string `json:"rpcId"`
		Payload struct {
			Args map[string]json.RawMessage `json:"args"`
		} `json:"payload"`
	}
	if json.NewDecoder(r.Body).Decode(&document) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Each method's descriptor names its arguments; another field is refused (0.2.0-rc.2).
	want := "request"
	switch method {
	case "session/list":
		want = "_request"
	case "session/modelCatalog":
		want = ""
	}
	if _, found := document.Payload.Args[want]; want != "" && !found || len(document.Payload.Args) != map[bool]int{true: 0, false: 1}[want == ""] {
		fmt.Fprintf(w, `{"type":"server-response","rpcId":%q,"result":{"ok":false,"error":{"code":"gateway/arguments-invalid"}}}`, document.RPCID)
		return
	}
	if status := f.status[method]; status != 0 {
		w.WriteHeader(status)
		return
	}
	var request struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		RequestID string `json:"requestId"`
		Mode      string `json:"mode"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Address *struct {
			SessionID string `json:"sessionId"`
		} `json:"address"`
		ThroughSeq *int `json:"throughSeq"`
		BeforeSeq  *int `json:"beforeSeq"`
	}
	json.Unmarshal(document.Payload.Args["request"], &request)
	var value string
	switch method {
	case "session/list":
		var items []string
		for id, s := range f.sessions {
			items = append(items, fmt.Sprintf(`{"sessionId":%q,"updatedAt":%d,"running":%t,"cwd":%q,"title":"SENTINEL"}`, id, 1000+len(s.events), s.running, s.cwd))
		}
		value = `{"items":[` + strings.Join(items, ",") + `]}`
		if f.afterList != nil {
			defer f.afterList(f)
		}
	case deepSeekProjectionsCall:
		s := f.sessions[request.SessionID]
		if s == nil {
			value = "null"
			break
		}
		last, _ := json.Marshal(s.lastUsed)
		value = fmt.Sprintf(`{"asOfSeq":%d,"values":{"inbox":{"next-turn":[%s],"next-step":[]},"userQuestions":{"active":[%s]},"modelSelection":{"lastUsed":%s,"next":null}}}`,
			len(s.events)-1, strings.Join(s.nextTurn, ","), strings.Join(s.questions, ","), last)
	case deepSeekPageCall:
		s := f.sessions[request.Address.SessionID]
		end := *request.ThroughSeq + 1
		if s == nil || end > len(s.events) {
			fmt.Fprintf(w, `{"type":"server-response","rpcId":%q,"result":{"ok":false,"error":{"code":"gateway/bad-request"}}}`, document.RPCID)
			return
		}
		if request.BeforeSeq != nil && *request.BeforeSeq < end {
			end = *request.BeforeSeq
		}
		start := max(0, end-f.pageSize)
		var records []string
		for _, event := range s.events[start:end] {
			records = append(records, `{"type":"event","event":`+event+`}`)
		}
		value = fmt.Sprintf(`{"records":[%s],"hasMore":%t}`, strings.Join(records, ","), start > 0)
	case "session/modelCatalog":
		def, _ := json.Marshal(f.def)
		value = `{"default":` + string(def) + `,"models":[]}`
	case "skills/list":
		value = `{"skills":[` + strings.Join(f.skills, ",") + `]}`
	case "session/create":
		if s := f.sessions[request.SessionID]; s != nil && s.cwd != request.Cwd {
			fmt.Fprintf(w, `{"type":"server-response","rpcId":%q,"result":{"ok":false,"error":{"code":"session/conflict"}}}`, document.RPCID)
			return
		} else if s == nil {
			f.sessions[request.SessionID] = &dshSession{cwd: request.Cwd}
		}
		value = fmt.Sprintf(`{"sessionId":%q}`, request.SessionID)
	case "session/prompt":
		s := f.sessions[request.SessionID]
		if s == nil || request.Mode != "queue" || len(request.Content) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		{
			// The admitted prompt is spliced from the inbox, then entered at its turn's start.
			f.prompts[request.RequestID] = true
			message := fmt.Sprintf(`{"content":[{"type":"text","text":%q}],"source":{"kind":"user","rpcId":%q,"clientTimeZone":"UTC"},"role":"user"}`,
				request.Content[0].Text, request.RequestID)
			f.add(request.SessionID, `"type":"agent/inbox/spliced","data":{"target":"next-turn","inserted":[`+message+`]}`)
			f.add(request.SessionID, `"type":"turn/start","data":{"turn":1}`)
			f.add(request.SessionID, `"type":"user/message","data":`+message)
			f.add(request.SessionID, `"type":"turn/end","data":{"turn":1,"reason":{"kind":"completed"}}`)
		}
		value = `{"accepted":true}`
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.lose[method] {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, `{"type":"server-response","rpcId":%q,"result":{"ok":true,"value":%s}}`, document.RPCID, value)
}

func (f *dshFake) target(target wakeTarget, state string) CycleTarget {
	t := CycleTarget{CycleID: "synthetic-cycle", Family: "deepseek", SessionID: dshRequester, Epoch: dshRequester, Holds: true,
		Pickup: deepSeekPickupEntry(), State: state, deepSeek: &target}
	if state != cycleReady && state != cycleClearIntent {
		t.SuccessorEpoch = deepSeekSuccessorID(t)
	}
	if state == cyclePickupIntent || state == cycleWaitingPickup {
		t.pickupAt = 1793077135435
	}
	return t
}

func dshFence(context.Context) error { return nil }

// pinned returns t with the pin that its Probe gives.
func pinned(t *testing.T, a deepSeekCycle, target CycleTarget) CycleTarget {
	t.Helper()
	p := a.Probe(context.Background(), target)
	if p.Reason != "" || p.Pin == "" {
		t.Fatalf("probe: %+v", p)
	}
	target.pin = p.Pin
	return target
}

// Probe needs the platform, the harness target, the participant, the requester's harness
// row, a model that is the harness default, and DeepSeek's entry byte for byte; an admitted
// cycle's facts must still be its pinned ones.
func TestDeepSeekCycleProbe(t *testing.T) {
	a := deepSeekCycle{}
	f, target := newDSHFake(t)
	p := a.Probe(context.Background(), f.target(target, cycleReady))
	if p.Reason != "" || p.Entry != PickupSkillName || p.Pin == "" || len(p.Pin) > cyclePinMax {
		t.Fatalf("probe: %+v", p)
	}
	if strings.Contains(p.Pin, f.sessions[dshRequester].cwd) || strings.Contains(p.Pin, target.DSHURL) || strings.Contains(p.Pin, target.DSHCredentials) {
		t.Fatalf("the pin holds a private value: %s", p.Pin)
	}
	for _, c := range []struct {
		name, want string
		change     func(*dshFake, *CycleTarget)
	}{
		{"platform", "platform_unverified", func(*dshFake, *CycleTarget) { deepSeekCyclePlatforms = map[string]bool{} }},
		{"target", "harness_target_missing", func(_ *dshFake, t *CycleTarget) { t.deepSeek = nil }},
		{"participant", "participant_unheld", func(_ *dshFake, t *CycleTarget) { t.Holds = false }},
		{"credential", "harness_unreachable", func(_ *dshFake, t *CycleTarget) {
			other := *t.deepSeek
			other.DSHCredentials = "/nonexistent/credentials.yaml"
			t.deepSeek = &other
		}},
		{"row", "harness_session_unknown", func(f *dshFake, _ *CycleTarget) { f.sessions[dshRequester].cwd = "" }},
		{"no model", "model_unknown", func(f *dshFake, _ *CycleTarget) { f.sessions[dshRequester].lastUsed = nil }},
		{"catalog", "model_unknown", func(f *dshFake, _ *CycleTarget) { f.status["session/modelCatalog"] = 500 }},
		{"not default", "model_not_default", func(f *dshFake, _ *CycleTarget) { f.def.Model = "deepseek/deepseek-v4-pro" }},
		{"effort", "model_not_default", func(f *dshFake, _ *CycleTarget) { f.def.Effort = "high" }},
		{"skills", "pickup_entry_unverified", func(f *dshFake, _ *CycleTarget) { f.status["skills/list"] = 500 }},
		{"missing", "pickup_entry_missing", func(f *dshFake, _ *CycleTarget) { f.skills = nil }},
		{"differs", "pickup_entry_differs", func(f *dshFake, _ *CycleTarget) {
			os.WriteFile(DeepSeekPickupSkillPath(f.home), PickupSkill(), 0o600)
		}},
		{"pinned target", "target_changed", func(_ *dshFake, t *CycleTarget) { t.pin = strings.Replace(t.pin, `"target":"`, `"target":"0`, 1) }},
		{"pinned model", "model_changed", func(f *dshFake, t *CycleTarget) {
			f.def.Model, f.sessions[dshRequester].lastUsed = "deepseek/deepseek-v4-pro", &deepSeekModel{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, target := newDSHFake(t)
			ct := f.target(target, cycleReady)
			if strings.HasPrefix(c.name, "pinned") {
				ct.pin = a.Probe(context.Background(), ct).Pin
			}
			f.set(func(f *dshFake) { c.change(f, &ct) })
			if p := a.Probe(context.Background(), ct); p.Reason != c.want {
				t.Fatalf("%s: %+v", c.name, p)
			}
		})
	}
	// Equal copies in two roots are one entry for each listing; the harness lists the one it
	// uses, which is the one checked.
	f, target = newDSHFake(t)
	other := filepath.Join(t.TempDir(), "skills", PickupSkillName, "SKILL.md")
	f.skills = nil
	f.installSkill(t, other, DeepSeekPickupSkill())
	if p := a.Probe(context.Background(), f.target(target, cycleReady)); p.Reason != "" {
		t.Fatalf("custom root: %+v", p)
	}
}

// The quiet point is one stable read: idle, no queued input or open question, the newest turn
// closed and no compaction running after it; a change between the reads proves nothing.
func TestDeepSeekCycleQuiet(t *testing.T) {
	for _, c := range []struct {
		name, want string
		change     func(*dshFake)
	}{
		{"idle", "", func(*dshFake) {}},
		{"running", "status_busy", func(f *dshFake) { f.sessions[dshRequester].running = true }},
		{"queued", "input_queued", func(f *dshFake) {
			f.sessions[dshRequester].nextTurn = []string{`{"content":[{"type":"text","text":"SENTINEL"}],"source":{"kind":"user"}}`}
		}},
		{"question", "question_open", func(f *dshFake) { f.sessions[dshRequester].questions = []string{`{"id":"q"}`} }},
		{"turn open", "turn_open", func(f *dshFake) { f.add(dshRequester, `"type":"turn/start","data":{}`) }},
		{"compaction", "compaction_running", func(f *dshFake) { f.add(dshRequester, `"type":"compaction/start","data":{}`) }},
		{"compaction ended", "", func(f *dshFake) {
			f.add(dshRequester, `"type":"compaction/start","data":{}`)
			f.add(dshRequester, `"type":"compaction/end","data":{}`)
		}},
		{"approval orphan in a closed turn", "", func(f *dshFake) {
			f.add(dshRequester, `"type":"turn/start","data":{}`)
			f.add(dshRequester, `"type":"approval/asked","data":{}`)
			f.add(dshRequester, `"type":"turn/end","data":{}`)
		}},
		{"changed between reads", "harness_changed", func(f *dshFake) {
			f.afterList = func(f *dshFake) { f.add(dshRequester, `"type":"step/end","data":{}`); f.afterList = nil }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, target := newDSHFake(t)
			f.set(c.change)
			state, err := deepSeekCycle{}.Inspect(context.Background(), f.target(target, cycleReady), dshFence)
			if err != nil || state.Seen != c.want || state.Idle != (c.want == "") {
				t.Fatalf("%s: %+v %v", c.name, state, err)
			}
		})
	}
	// A scan that ends before the newest turn's start proves nothing.
	f, target := newDSHFake(t)
	f.set(func(f *dshFake) {
		f.pageSize = 1
		f.add(dshRequester, `"type":"turn/start","data":{}`)
		for range deepSeekCyclePages + 1 {
			f.add(dshRequester, `"type":"step/end","data":{}`)
		}
		f.add(dshRequester, `"type":"turn/end","data":{}`)
	})
	if _, err := (deepSeekCycle{}).Inspect(context.Background(), f.target(target, cycleReady), dshFence); err == nil {
		t.Fatal("a bounded scan proved the quiet point")
	}
	// An unreadable harness is an error, not a reason.
	f, target = newDSHFake(t)
	f.status["session/list"] = 500
	if _, err := (deepSeekCycle{}).Inspect(context.Background(), f.target(target, cycleReady), dshFence); !errors.Is(err, errDeepSeekHarness) {
		t.Fatalf("unreadable: %v", err)
	}
}

// The reset creates the cycle's chosen session in the requester's cwd once. A lost response
// is read back and the same create is sent again only after a verified null; a session with
// the chosen ID that is not the cycle's is a conflict; the created session is empty.
func TestDeepSeekCycleCreate(t *testing.T) {
	a := deepSeekCycle{}
	f, target := newDSHFake(t)
	ct := pinned(t, a, f.target(target, cycleClearIntent))
	id, err := a.Create(context.Background(), ct, dshFence)
	if err != nil || id != deepSeekSuccessorID(ct) || !nativeEpoch.MatchString(id) || f.sessions[id].cwd != f.sessions[dshRequester].cwd {
		t.Fatalf("create: %q %v", id, err)
	}
	// Again: the read finds the empty created session and sends nothing.
	if again, err := a.Create(context.Background(), ct, dshFence); err != nil || again != id || f.count("session/create") != 1 {
		t.Fatalf("again: %q %v %d", again, err, f.count("session/create"))
	}
	ct.State, ct.SuccessorEpoch = cycleResetVerified, id
	if epoch, err := a.NewEpoch(context.Background(), ct); err != nil || epoch != id {
		t.Fatalf("epoch: %q %v", epoch, err)
	}
	if state, err := a.Inspect(context.Background(), ct, dshFence); err != nil || !state.Idle {
		t.Fatalf("empty successor: %+v %v", state, err)
	}
	f.set(func(f *dshFake) { f.turn(id, dshModel) })
	if state, err := a.Inspect(context.Background(), ct, dshFence); err != nil || state.Seen != "successor_not_empty" {
		t.Fatalf("used successor: %+v %v", state, err)
	}
	// Not empty, or in another cwd: a conflict, never adopted.
	ct.State, ct.SuccessorEpoch = cycleClearIntent, ""
	if _, err := a.Create(context.Background(), ct, dshFence); !errors.Is(err, errDeepSeekConflict) {
		t.Fatalf("used: %v", err)
	}
	f.set(func(f *dshFake) { f.sessions[id] = &dshSession{cwd: "/elsewhere"} })
	if _, err := a.Create(context.Background(), ct, dshFence); !errors.Is(err, errDeepSeekConflict) {
		t.Fatalf("other cwd: %v", err)
	}
	// A lost response: the read back finds the created session.
	f, target = newDSHFake(t)
	ct = pinned(t, a, f.target(target, cycleClearIntent))
	f.lose["session/create"] = true
	if _, err := a.Create(context.Background(), ct, dshFence); err == nil {
		t.Fatal("a lost response was a receipt")
	}
	f.set(func(f *dshFake) { f.lose = map[string]bool{} })
	if id, err := a.Create(context.Background(), ct, dshFence); err != nil || id != deepSeekSuccessorID(ct) || f.count("session/create") != 1 {
		t.Fatalf("after loss: %q %v %d", id, err, f.count("session/create"))
	}
	// An unreadable read proves no absence: nothing is created.
	f, target = newDSHFake(t)
	ct = pinned(t, a, f.target(target, cycleClearIntent))
	f.status[deepSeekProjectionsCall] = 500
	if _, err := a.Create(context.Background(), ct, dshFence); err == nil || f.count("session/create") != 0 {
		t.Fatalf("unreadable: %v %d", err, f.count("session/create"))
	}
	// Changed facts and a refused fence send nothing.
	f, target = newDSHFake(t)
	ct = pinned(t, a, f.target(target, cycleClearIntent))
	f.def.Model = "deepseek/deepseek-v4-pro"
	if _, err := a.Create(context.Background(), ct, dshFence); err == nil || f.count("session/create") != 0 {
		t.Fatalf("changed default: %v", err)
	}
	f.def = dshModel
	if _, err := a.Create(context.Background(), ct, func(context.Context) error { return ErrCycleChanged }); !errors.Is(err, ErrCycleChanged) || f.count("session/create") != 0 {
		t.Fatalf("fence: %v", err)
	}
}

// The pickup is one prompt with the cycle's request ID, never sent again once received, and the
// receipt is the created session's only user prompt, with that ID and the exact text.
func TestDeepSeekCyclePickup(t *testing.T) {
	a := deepSeekCycle{}
	f, target := newDSHFake(t)
	ct := pinned(t, a, f.target(target, cycleClearIntent))
	id, err := a.Create(context.Background(), ct, dshFence)
	if err != nil {
		t.Fatal(err)
	}
	ct = pinned(t, a, f.target(target, cyclePickupIntent))
	if ct.SuccessorEpoch != id {
		t.Fatalf("successor %q", ct.SuccessorEpoch)
	}
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || received {
		t.Fatalf("before: %v %v", received, err)
	}
	if err := a.SendPickup(context.Background(), ct, func(context.Context) error { return ErrCycleChanged }); err == nil || f.count("session/prompt") != 0 {
		t.Fatalf("fence: %v", err)
	}
	if err := a.SendPickup(context.Background(), ct, dshFence); err != nil {
		t.Fatal(err)
	}
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || !received {
		t.Fatalf("receipt: %v %v", received, err)
	}
	// A send after the receipt sends nothing. A prompt whose response was lost is admitted, and its
	// receipt is read back.
	if err := a.SendPickup(context.Background(), ct, dshFence); err != nil || f.count("session/prompt") != 1 {
		t.Fatalf("again: %v %d", err, f.count("session/prompt"))
	}
	f, target = newDSHFake(t)
	ct = pinned(t, a, f.target(target, cycleClearIntent))
	a.Create(context.Background(), ct, dshFence)
	ct = pinned(t, a, f.target(target, cyclePickupIntent))
	f.lose["session/prompt"] = true
	if err := a.SendPickup(context.Background(), ct, dshFence); err == nil {
		t.Fatal("a lost response was accepted")
	}
	f.set(func(f *dshFake) { f.lose = map[string]bool{} })
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || !received || len(f.sessions[ct.SuccessorEpoch].events) != 4 {
		t.Fatalf("lost response: %v %v", received, err)
	}
	// The spliced prompt is the receipt before its turn starts.
	var events []string
	f.set(func(f *dshFake) {
		events = f.sessions[ct.SuccessorEpoch].events
		f.sessions[ct.SuccessorEpoch].events = events[:1]
	})
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || !received {
		t.Fatalf("spliced: %v %v", received, err)
	}
	f.set(func(f *dshFake) { f.sessions[ct.SuccessorEpoch].events = events })
	// Another prompt in the session, or another request ID, is no receipt.
	f.set(func(f *dshFake) {
		f.add(ct.SuccessorEpoch, `"type":"user/message","data":{"content":[{"type":"text","text":"SENTINEL"}],"source":{"kind":"user","rpcId":"other"}}`)
	})
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || received {
		t.Fatalf("other prompt: %v %v", received, err)
	}
	ct.pickupAt++
	f.set(func(f *dshFake) { f.sessions[ct.SuccessorEpoch].events = f.sessions[ct.SuccessorEpoch].events[:4] })
	if received, err := a.PickupReceipt(context.Background(), ct); err != nil || received {
		t.Fatalf("other request ID: %v %v", received, err)
	}
}

// The report's check: the pinned target, and the created session's requests and replies from
// the pinned model, with at least one reply.
func TestDeepSeekCycleReportCheck(t *testing.T) {
	a := deepSeekCycle{}
	setup := func(t *testing.T) (*dshFake, CycleTarget) {
		f, target := newDSHFake(t)
		ct := pinned(t, a, f.target(target, cycleClearIntent))
		if _, err := a.Create(context.Background(), ct, dshFence); err != nil {
			t.Fatal(err)
		}
		ct = pinned(t, a, f.target(target, cycleWaitingPickup))
		return f, ct
	}
	f, ct := setup(t)
	if _, err := a.ReportCheck(context.Background(), ct); err == nil {
		t.Fatal("a report without a reply")
	}
	f.set(func(f *dshFake) { f.turn(ct.SuccessorEpoch, dshModel) })
	if reason, err := a.ReportCheck(context.Background(), ct); reason != "" || err != nil {
		t.Fatalf("report: %q %v", reason, err)
	}
	other := deepSeekModel{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}
	f, ct = setup(t)
	f.set(func(f *dshFake) { f.turn(ct.SuccessorEpoch, other) })
	if reason, err := a.ReportCheck(context.Background(), ct); reason != "model_changed" || err != nil {
		t.Fatalf("other model: %q %v", reason, err)
	}
	f, ct = setup(t)
	f.set(func(f *dshFake) {
		f.turn(ct.SuccessorEpoch, dshModel)
		f.add(ct.SuccessorEpoch, `"type":"request/header","data":{"header":{"config":{"provider":"openrouter","model":"deepseek/deepseek-v4-flash","reasoningEffort":"high"},"reason":"change"}}`)
	})
	if reason, err := a.ReportCheck(context.Background(), ct); reason != "model_changed" || err != nil {
		t.Fatalf("other effort: %q %v", reason, err)
	}
	f, ct = setup(t)
	f.set(func(f *dshFake) { f.turn(ct.SuccessorEpoch, dshModel) })
	moved := *ct.deepSeek
	moved.DSHURL = "http://127.0.0.1:1"
	ct.deepSeek = &moved
	if reason, _ := a.ReportCheck(context.Background(), ct); reason != "target_changed" {
		t.Fatalf("moved target: %q", reason)
	}
}

// dshFixture is a store with a harnessed DeepSeek requester that holds its participant under
// an Enable, with the real DeepSeek adapter against the synthetic harness.
type dshFixture struct {
	s      *Store
	root   string
	clock  time.Time
	fake   *dshFake
	target wakeTarget
	repo   string
	k      Key
	name   string
}

func newDSHFixture(t *testing.T) *dshFixture {
	t.Helper()
	s, root := testStore(t)
	f, target := newDSHFake(t)
	g := &dshFixture{s: s, root: root, fake: f, target: target, k: Key{"deepseek", dshRequester}}
	g.clock = time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return g.clock }
	poll, bound := cycleReceiptPoll, cycleReceiptBound
	cycleReceiptPoll, cycleReceiptBound = 5*time.Millisecond, 2*time.Second
	t.Cleanup(func() { cycleReceiptPoll, cycleReceiptBound = poll, bound })
	t.Cleanup(func() { g.s.cycleWork.stop() })
	g.repo = namedRepo(t, "dsh-repo")
	if err := os.WriteFile(filepath.Join(g.repo, ".gitignore"), []byte("_handoff/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.SetContextPolicy(roomCtx("context-family-settings"), "deepseek", ContextHandoffCycle, 75); err != nil {
		t.Fatal(err)
	}
	session := g.register(t, dshRequester)
	if session.Address == "" || !session.HoldsAddress {
		t.Fatalf("requester: %+v", session)
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	return g
}

// register is a DeepSeek session's own koinon register, with the harness target.
func (g *dshFixture) register(t *testing.T, id string) Session {
	t.Helper()
	wake, _ := json.Marshal(g.target)
	session, err := g.s.Register(context.Background(), Registration{Family: "deepseek", ID: id, Repository: g.repo, Directory: g.repo,
		WakeTarget: wake, TTLSeconds: 3600, ToolCall: true})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// reloadSession reads a session as the views do.
func (s *Store) reloadSession(t *testing.T, k Key) Session {
	t.Helper()
	session, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func (g *dshFixture) step(t *testing.T) {
	t.Helper()
	if err := g.s.cycleStep(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (g *dshFixture) cycle(t *testing.T, id string) cycleRow {
	t.Helper()
	cycles, err := cyclesWhere(context.Background(), g.s.db, `cycle_id=?`, id)
	if err != nil || len(cycles) != 1 {
		t.Fatalf("cycle %s: %v", id, err)
	}
	return cycles[0]
}

// ready crosses the threshold and reports the requester ready.
func (g *dshFixture) ready(t *testing.T) *CycleView {
	t.Helper()
	g.clock = g.clock.Add(time.Millisecond)
	if err := g.s.Observe(context.Background(), Observation{Caller: g.k, Context: &ObservedValue{Source: deepSeekContextSource,
		At: g.clock.UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(900), Epoch: dshRequester}}); err != nil {
		t.Fatal(err)
	}
	v, err := g.s.ContextCycleStatus(context.Background(), g.k)
	if err != nil || v == nil || v.State != cycleWaitingReady {
		t.Fatalf("admission: %+v %v", v, err)
	}
	dir := filepath.Join(g.repo, "_handoff", cycleAgent("deepseek", ""))
	g.name = "2026-10-27-120000.md"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, g.name), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(filepath.Join(dir, g.name), g.clock, g.clock)
	if _, err := g.s.ContextCycleReady(context.Background(), g.k, dshRequester, v.ID, v.Revision, g.name); err != nil {
		t.Fatal(err)
	}
	return v
}

// toWaitingPickup steps a ready cycle through the reset and the pickup's receipt.
func (g *dshFixture) toWaitingPickup(t *testing.T, id string) cycleRow {
	t.Helper()
	for range 4 {
		g.step(t)
	}
	waitIdle(t, g.s, id)
	g.step(t)
	c := g.cycle(t, id)
	if c.State != cycleWaitingPickup {
		t.Fatalf("waiting: %+v %v", c, cycleReasons(t, g.s, id))
	}
	return c
}

// The whole host-less flow: the requester's quiet point, the created session with the cycle's
// ID, its pickup prompt and receipt, the created session's own registration taking the
// participant once the receipt is recorded, and its report through the command path, which
// completes the cycle and carries the Enable.
func TestDeepSeekCycleFlow(t *testing.T) {
	g := newDSHFixture(t)
	v := g.ready(t)
	g.step(t) // clear intent, create, clear_received with the created ID
	c := g.cycle(t, v.ID)
	created := c.SuccessorEpoch
	if c.State != cycleClearReceived || created == "" || g.fake.count("session/create") != 1 || g.fake.sessions[created] == nil {
		t.Fatalf("after create: %+v", c)
	}
	successor := Key{"deepseek", created}
	// The Sessions page names the created session and the harness's web origin.
	requester := g.s.reloadSession(t, g.k)
	enables, _ := g.s.contextEnables(context.Background())
	if row := g.s.contextRowOf(requester, enables[g.k], g.clock.UnixMilli()); row.CreatedSession != created || row.HarnessOrigin != g.target.DSHURL {
		t.Fatalf("row: %q %q", row.CreatedSession, row.HarnessOrigin)
	}
	g.step(t) // the select is recorded; DeepSeek shows nothing
	g.step(t) // proof
	if c = g.cycle(t, v.ID); c.State != cycleResetVerified {
		t.Fatalf("after proof: %+v %v", c, cycleReasons(t, g.s, v.ID))
	}
	// The created session can register before its pickup: it holds nothing yet.
	g.fake.set(func(f *dshFake) { f.lose["session/prompt"] = true })
	g.step(t) // pickup intent; the worker sends
	if c = g.cycle(t, v.ID); c.State != cyclePickupIntent {
		t.Fatalf("after dispatch: %+v", c)
	}
	early := g.register(t, created)
	if early.HoldsAddress || early.Succession == nil || early.Succession.Reason != "cycle_receipt_pending" || early.Succession.RetryAfterMS <= 0 {
		t.Fatalf("early registration: %+v %+v", early, early.Succession)
	}
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, created, v.ID, c.Revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleReceiptPending) {
		t.Fatalf("early report: %v", err)
	}
	// It reads its cycle before it holds the participant; another session reads none.
	if status, err := g.s.ContextCycleStatus(context.Background(), successor); err != nil || status == nil || status.ID != v.ID || status.Role != "successor" {
		t.Fatalf("early status: %+v %v", status, err)
	}
	other := Key{"deepseek", "session-synthetic-other"}
	g.register(t, other.ID)
	if status, err := g.s.ContextCycleStatus(context.Background(), other); err != nil || status != nil {
		t.Fatalf("another session's status: %+v %v", status, err)
	}
	waitIdle(t, g.s, v.ID)
	g.fake.set(func(f *dshFake) { f.lose = map[string]bool{} })
	g.step(t)
	if c = g.cycle(t, v.ID); c.State != cycleWaitingPickup || g.fake.count("session/prompt") != 1 {
		t.Fatalf("after receipt: %+v %v", c, cycleReasons(t, g.s, v.ID))
	}
	// Before its registration after the receipt, the report is still pending.
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, created, v.ID, c.Revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleReceiptPending) {
		t.Fatalf("report before the transfer: %v", err)
	}
	linked := g.register(t, created)
	if !linked.HoldsAddress || linked.Succession == nil || linked.Succession.Reason != "cycle_session" {
		t.Fatalf("registration after the receipt: %+v %+v", linked, linked.Succession)
	}
	if status, err := g.s.ContextCycleStatus(context.Background(), successor); err != nil || status == nil || status.ID != v.ID {
		t.Fatalf("successor's status: %+v %v", status, err)
	}
	// Without a reply the report proves nothing yet; a reply from another model ends it.
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, created, v.ID, c.Revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleHarness) {
		t.Fatalf("report without a reply: %v", err)
	}
	g.fake.set(func(f *dshFake) { f.turn(created, dshModel) })
	done, err := g.s.ContextCyclePickup(context.Background(), successor, created, v.ID, c.Revision, g.name, "resumed", "synthetic restored work")
	if err != nil || done.State != cycleComplete {
		t.Fatalf("report: %v %+v", err, done)
	}
	if again, err := g.s.ContextCyclePickup(context.Background(), successor, created, v.ID, c.Revision, g.name, "resumed", "synthetic restored work"); err != nil || again.State != cycleComplete {
		t.Fatalf("retry: %v %+v", err, again)
	}
	e, err := g.s.contextEnables(context.Background())
	if err != nil || !e[successor].Enabled || e[g.k].Enabled {
		t.Fatalf("Enable: %+v %v", e, err)
	}
	if g.fake.count("session/create") != 1 || g.fake.count("session/prompt") != 1 || g.fake.count("session/selectModel") != 0 {
		t.Fatalf("calls: %v", g.fake.calls)
	}
}

// A reply from another model ends the cycle uncertain at the report.
func TestDeepSeekCycleReportModel(t *testing.T) {
	g := newDSHFixture(t)
	v := g.ready(t)
	c := g.toWaitingPickup(t, v.ID)
	successor := Key{"deepseek", c.SuccessorEpoch}
	g.register(t, successor.ID)
	g.fake.set(func(f *dshFake) {
		f.turn(successor.ID, deepSeekModel{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"})
	})
	if _, err := g.s.ContextCyclePickup(context.Background(), successor, successor.ID, v.ID, c.Revision, g.name, "resumed", "synthetic"); !errors.Is(err, ErrCycleModel) {
		t.Fatalf("report: %v", err)
	}
	if c = g.cycle(t, v.ID); c.State != cycleUncertain || c.Reason != "model_changed" {
		t.Fatalf("cycle: %+v", c)
	}
}

// cycle_session takes the participant only for the cycle's created session, with the pinned
// target, under the cycle's Enable and before its deadlines, without another alias.
func TestDeepSeekCycleSuccessionRefusals(t *testing.T) {
	for _, c := range []struct {
		name, want string
		change     func(*testing.T, *dshFixture, cycleRow) string // returns the candidate's ID
	}{
		{"another session", "no_host", func(_ *testing.T, _ *dshFixture, _ cycleRow) string { return "session-synthetic-other" }},
		{"another target", "no_host", func(t *testing.T, g *dshFixture, c cycleRow) string {
			g.target.DSHURL = "http://127.0.0.1:1"
			return c.SuccessorEpoch
		}},
		{"disabled", "no_host", func(t *testing.T, g *dshFixture, c cycleRow) string {
			fixtureExec(g.s, `UPDATE context_enables SET revision=revision+1 WHERE family='deepseek'`)
			return c.SuccessorEpoch
		}},
		{"expired", "no_host", func(t *testing.T, g *dshFixture, c cycleRow) string {
			fixtureExec(g.s, `UPDATE context_cycles SET stage_deadline=? WHERE cycle_id=?`, g.clock.UnixMilli(), c.ID)
			return c.SuccessorEpoch
		}},
		{"fenced", "fenced", func(t *testing.T, g *dshFixture, c cycleRow) string {
			fixtureExec(g.s, `INSERT INTO participant_fences(address,family,session_id,at,reason) VALUES (?,?,?,?,'synthetic')`, c.Address, "deepseek", c.SuccessorEpoch, g.clock.UnixMilli())
			return c.SuccessorEpoch
		}},
		{"other alias", "no_host", func(t *testing.T, g *dshFixture, c cycleRow) string {
			fixtureExec(g.s, `INSERT INTO names(kind,name,family,session_id,holder_id,repository,role) VALUES ('alias','synthetic-other-alias','deepseek','',?,'/elsewhere','x')`, c.SuccessorEpoch)
			return c.SuccessorEpoch
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newDSHFixture(t)
			v := g.ready(t)
			row := g.toWaitingPickup(t, v.ID)
			id := c.change(t, g, row)
			session := g.register(t, id)
			if session.HoldsAddress || session.Succession == nil || session.Succession.Reason != c.want {
				t.Fatalf("%s: %+v %+v", c.name, session, session.Succession)
			}
		})
	}
}

// A restart in clear_intent reads the chosen session first: it creates it only after a
// verified null, adopts the empty created one, and ends on a conflict.
func TestDeepSeekCycleCreateRecovery(t *testing.T) {
	for _, c := range []struct {
		name, state string
		creates     int
		before      func(*dshFixture, string)
	}{
		{"not created", cycleClearReceived, 1, func(*dshFixture, string) {}},
		{"created", cycleClearReceived, 0, func(g *dshFixture, id string) {
			g.fake.sessions[id] = &dshSession{cwd: g.fake.sessions[dshRequester].cwd}
		}},
		{"conflict", cycleUncertain, 0, func(g *dshFixture, id string) { g.fake.sessions[id] = &dshSession{cwd: "/elsewhere"} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newDSHFixture(t)
			v := g.ready(t)
			row := g.cycle(t, v.ID)
			if _, err := g.s.advanceCycle(context.Background(), v.ID, row.Revision, cycleReady, cycleClearIntent, "", nil, nil); err != nil {
				t.Fatal(err)
			}
			g.fake.set(func(f *dshFake) { c.before(g, deepSeekSuccessorID(CycleTarget{CycleID: v.ID})) })
			// An unreadable harness waits within the stage deadline.
			g.fake.set(func(f *dshFake) { f.status[deepSeekProjectionsCall] = 500 })
			g.step(t)
			if row = g.cycle(t, v.ID); row.State != cycleClearIntent || g.fake.count("session/create") != 0 {
				t.Fatalf("unreadable: %+v", row)
			}
			g.fake.set(func(f *dshFake) { f.status = map[string]int{} })
			g.step(t)
			if row = g.cycle(t, v.ID); row.State != c.state || g.fake.count("session/create") != c.creates {
				t.Fatalf("%s: %+v %d", c.name, row, g.fake.count("session/create"))
			}
		})
	}
}
