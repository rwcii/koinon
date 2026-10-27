package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// openCodeFake is a synthetic OpenCode server: the message page of each session and the
// provider list, as raw JSON, behind the launch password.
type openCodeFake struct {
	mu        sync.Mutex
	password  string
	messages  map[string]string
	providers string
	status    int           // a status other than 200 for every request, or 0
	delay     chan struct{} // when set, each request waits until it is closed or its client gives up
	calls     map[string]int
}

func newOpenCodeFake(t *testing.T) (*openCodeFake, LaunchTarget) {
	t.Helper()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("testdata", "opencode", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	f := &openCodeFake{password: strings.Repeat("ab", 32), messages: map[string]string{"ses_synthetic": read("messages.json")},
		providers: read("providers.json"), calls: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls[r.URL.Path]++
		status, delay := f.status, f.delay
		f.mu.Unlock()
		if delay != nil {
			select {
			case <-delay:
			case <-r.Context().Done():
				return
			}
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != f.password || r.Method != http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status != 0 {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(status)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == "/config/providers":
			w.Write([]byte(f.providers))
		case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message") && r.URL.Query().Get("limit") == "4":
			body, found := f.messages[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/session/"), "/message")]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(body))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return f, LaunchTarget{Family: "opencode", Address: strings.TrimPrefix(server.URL, "http://"), Password: f.password}
}

func (f *openCodeFake) set(session, messages string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[session] = messages
}

func (f *openCodeFake) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

// assistant is one message page entry with the given info members, as raw JSON.
func assistant(members string) string {
	return `{"info":{"role":"assistant","providerID":"opencode","modelID":"big-pickle",` + members + `},"parts":[{"type":"text","text":"SYNTHETIC-PART-SENTINEL"}]}`
}

const openCodeTokens = `"tokens":{"total":22314,"input":20520,"output":2,"reasoning":0,"cache":{"write":0,"read":1792}}`

// The redacted native page gives the newest finished assistant message's tokens and model,
// and the provider list its context limit; the parts' own token counts are not read.
func TestOpenCodeContextReading(t *testing.T) {
	ctx := context.Background()
	_, target := newOpenCodeFake(t)
	model, usage, err := openCodeContext(ctx, target, "ses_synthetic")
	if err != nil || model == nil || usage == nil {
		t.Fatalf("reading: %v %+v %+v", err, model, usage)
	}
	if model.Source != "opencode_server" || model.ID != "opencode/big-pickle" || model.At != 1793073206710 ||
		usage.Source != "opencode_server" || *usage.UsedTokens != 22314 || *usage.LimitTokens != 200000 ||
		usage.At != 1793073206710 || usage.Epoch != "ses_synthetic" {
		t.Fatalf("reading: %+v %+v", model, usage)
	}
	for _, v := range []*ObservedValue{model, usage} {
		data, _ := json.Marshal(v)
		if strings.Contains(string(data), "SENTINEL") || strings.Contains(string(data), "777") {
			t.Fatalf("a part or key reached the reading: %s", data)
		}
	}
}

// Messages are chosen by completion time, not by their order in the page. A stalled or
// aborted turn is no reading. A newest finished message that cannot be read is unknown,
// never an older reading.
func TestOpenCodeContextChoice(t *testing.T) {
	ctx := context.Background()
	f, target := newOpenCodeFake(t)
	older := assistant(`"finish":"stop","time":{"completed":1000},"tokens":{"input":10,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}`)
	newer := assistant(`"finish":"stop","time":{"completed":2000},"tokens":{"input":20,"output":2,"reasoning":3,"cache":{"read":4,"write":5}}`)
	aborted := assistant(`"time":{"completed":3000},"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`)
	running := assistant(`"finish":"stop","time":{},"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`)
	user := `{"info":{"role":"user","time":{"created":4000}},"parts":[]}`
	for name, page := range map[string]string{
		"in order":        "[" + older + "," + newer + "," + aborted + "," + running + "," + user + "]",
		"reversed":        "[" + user + "," + running + "," + aborted + "," + newer + "," + older + "]",
		"newest in front": "[" + newer + "," + older + "]",
	} {
		f.set("ses_synthetic", page)
		_, usage, err := openCodeContext(ctx, target, "ses_synthetic")
		if err != nil || usage == nil || *usage.UsedTokens != 34 || usage.At != 2000 {
			t.Fatalf("%s: %v %+v", name, err, usage)
		}
	}
	// No finished assistant message yet: nothing, and no error.
	for _, page := range []string{"[]", "[" + user + "]", "[" + aborted + "," + running + "]"} {
		f.set("ses_synthetic", page)
		if model, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err != nil || model != nil || usage != nil {
			t.Fatalf("no finished message %s: %v %+v", page, err, usage)
		}
	}
	// Two finished messages at the same time are ambiguous.
	f.set("ses_synthetic", "["+newer+","+newer+"]")
	if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
		t.Fatalf("tie: %v %+v", err, usage)
	}
	// A malformed newest message does not fall back to the older valid one.
	f.set("ses_synthetic", "["+older+","+assistant(`"finish":"stop","time":{"completed":2000},"tokens":{"input":1.5,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`)+"]")
	if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
		t.Fatalf("malformed newest: %v %+v", err, usage)
	}
}

// Every count must be present and a JSON integer in range, the sum too; a total, when
// present, must be that sum. The model must be the message's own provider and model.
func TestOpenCodeContextMalformed(t *testing.T) {
	ctx := context.Background()
	f, target := newOpenCodeFake(t)
	finished := func(tokens string) string {
		return "[" + assistant(`"finish":"stop","time":{"completed":2000},"tokens":`+tokens) + "]"
	}
	for name, page := range map[string]string{
		"fraction":       finished(`{"input":1.5,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"exponent":       finished(`{"input":1e3,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"negative":       finished(`{"input":-1,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"string":         finished(`{"input":"5","output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"null count":     finished(`{"input":null,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"too large":      finished(`{"input":9007199254740992,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"sum too large":  finished(`{"input":9007199254740991,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"missing count":  finished(`{"input":5,"output":0,"cache":{"read":0,"write":0}}`),
		"missing cache":  finished(`{"input":5,"output":0,"reasoning":0}`),
		"missing write":  finished(`{"input":5,"output":0,"reasoning":0,"cache":{"read":0}}`),
		"null total":     finished(`{"total":null,"input":5,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"wrong total":    finished(`{"total":6,"input":5,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}`),
		"no tokens":      "[" + assistant(`"finish":"stop","time":{"completed":2000}`) + "]",
		"wrong type":     "[" + assistant(`"finish":7,"time":{"completed":2000},`+openCodeTokens) + "]",
		"not a page":     `{"data":[]}`,
		"other provider": `[{"info":{"role":"assistant","providerID":"other","modelID":"big-pickle","finish":"stop","time":{"completed":2000},` + openCodeTokens + `}}]`,
		"other model":    `[{"info":{"role":"assistant","providerID":"opencode","modelID":"unknown-model","finish":"stop","time":{"completed":2000},` + openCodeTokens + `}}]`,
		"no model":       `[{"info":{"role":"assistant","providerID":"opencode","finish":"stop","time":{"completed":2000},` + openCodeTokens + `}}]`,
	} {
		f.set("ses_synthetic", page)
		if model, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || model != nil || usage != nil {
			t.Fatalf("%s: %v %+v %+v", name, err, model, usage)
		}
	}
	// An absent total is allowed.
	f.set("ses_synthetic", finished(`{"input":5,"output":1,"reasoning":0,"cache":{"read":2,"write":0}}`))
	if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err != nil || *usage.UsedTokens != 8 {
		t.Fatalf("no total: %v %+v", err, usage)
	}
	// The provider list must name the provider once, and the model under its own ID, with a
	// positive integer context limit.
	page := finished(`{"input":5,"output":1,"reasoning":0,"cache":{"read":2,"write":0}}`)
	f.set("ses_synthetic", page)
	for name, providers := range map[string]string{
		"no provider":   `{"providers":[],"default":{}}`,
		"twice":         `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle","limit":{"context":200000}}}},{"id":"opencode","models":{}}]}`,
		"other ID":      `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"other","limit":{"context":200000}}}}]}`,
		"zero limit":    `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle","limit":{"context":0}}}}]}`,
		"fraction":      `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle","limit":{"context":2.5}}}}]}`,
		"no limit":      `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle"}}}]}`,
		"limit too big": `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle","limit":{"context":9007199254740992}}}}]}`,
		"not JSON":      `providers`,
	} {
		f.mu.Lock()
		f.providers = providers
		f.mu.Unlock()
		if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
			t.Fatalf("providers %s: %v %+v", name, err, usage)
		}
	}
}

// Only an exact 200 from the launched server, within the time and size bounds, with the
// launch password, gives a reading; a redirect is not followed.
func TestOpenCodeContextTransport(t *testing.T) {
	ctx := context.Background()
	f, target := newOpenCodeFake(t)
	wrong := target
	wrong.Password = strings.Repeat("cd", 32)
	if _, usage, err := openCodeContext(ctx, wrong, "ses_synthetic"); err == nil || usage != nil {
		t.Fatalf("wrong password: %v", err)
	}
	for _, status := range []int{http.StatusFound, http.StatusNoContent, http.StatusInternalServerError} {
		f.status = status
		if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	f.status = 0
	if f.count("/elsewhere") != 0 {
		t.Fatal("a redirect was followed")
	}
	if _, _, err := openCodeContext(ctx, target, "ses_unknown"); err == nil {
		t.Fatal("unknown session")
	}
	// A session ID is one escaped path segment.
	if _, _, err := openCodeContext(ctx, target, "../config/providers"); err == nil || f.count("/config/providers") != 0 {
		t.Fatalf("escaped session: %v %v", err, f.calls)
	}
	f.set("ses_synthetic", "["+strings.Repeat(" ", openCodeResponseBytes)+"]")
	if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
		t.Fatalf("oversized page: %v", err)
	}
	f.set("ses_synthetic", "["+assistant(`"finish":"stop","time":{"completed":2000},`+openCodeTokens)+"]")
	f.mu.Lock()
	f.providers = `{"providers":[{"id":"opencode","models":{"big-pickle":{"id":"big-pickle","limit":{"context":200000}}}}],"pad":"` +
		strings.Repeat("x", openCodeResponseBytes) + `"}`
	f.mu.Unlock()
	if _, usage, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || usage != nil {
		t.Fatalf("oversized providers: %v", err)
	}
	f.delay = make(chan struct{})
	defer close(f.delay)
	start := time.Now()
	if _, _, err := openCodeContext(ctx, target, "ses_synthetic"); err == nil || time.Since(start) > 3*openCodeContextTimeout {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
}

// openCodeRound is a store with launched OpenCode sessions on one synthetic server.
func openCodeRound(t *testing.T) (*Store, *openCodeFake, *time.Time, func(id string, launched bool) Session) {
	t.Helper()
	s, _ := testStore(t)
	f, target := newOpenCodeFake(t)
	clock := time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return clock }
	directory := t.TempDir()
	register := func(id string, launched bool) Session {
		registration := Registration{Family: "opencode", ID: id, Directory: directory}
		if launched {
			launch, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "opencode", Directory: directory,
				CLI: filepath.Join(directory, "synthetic-cli"), HostPID: 123, Address: target.Address, Password: target.Password})
			if err != nil {
				t.Fatal(err)
			}
			registration.LaunchID, registration.Ancestors = launch, []int{123}
		}
		session, err := s.Register(context.Background(), registration)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	return s, f, &clock, register
}

func (s *Store) storedContext(k Key) (storedValue, bool) {
	s.observed.mu.Lock()
	defer s.observed.mu.Unlock()
	v, found := s.observed.byKey[k]["context"]
	return v, found
}

// A round records a launched session's reading with its native source time; peer status and
// the dashboard's views show it. An unchanged reading is recorded again only after a minute,
// a failed read confirms nothing, and a new reading replaces it. Unlaunched and retired
// sessions are not read.
func TestOpenCodeContextRound(t *testing.T) {
	ctx := context.Background()
	s, f, clock, register := openCodeRound(t)
	session := register("ses_synthetic", true)
	register("ses_unlaunched", false)
	retired := register("ses_retired", true)
	f.set("ses_retired", f.messages["ses_synthetic"])
	if _, err := fixtureExec(s, `UPDATE sessions SET retired_at=? WHERE family='opencode' AND id=?`, clock.UnixMilli(), retired.ID); err != nil {
		t.Fatal(err)
	}
	k := Key{"opencode", session.ID}
	if views := s.sessionObservations(ctx, session); views["context"].Reason != "not_observed" || views["model"].Reason != "not_observed" {
		t.Fatalf("before a reading: %+v", views)
	}
	s.openCodeContextRound(ctx)
	views := s.sessionObservations(ctx, session)
	if v := views["context"].Value; v == nil || v.Source != "opencode_server" || *v.UsedTokens != 22314 || *v.LimitTokens != 200000 || v.At != 1793073206710 {
		t.Fatalf("context: %+v", views["context"])
	}
	if v := views["model"].Value; v == nil || v.ID != "opencode/big-pickle" {
		t.Fatalf("model: %+v", views["model"])
	}
	if f.count("/session/ses_unlaunched/message") != 0 || f.count("/session/ses_retired/message") != 0 {
		t.Fatalf("unlaunched or retired session read: %v", f.calls)
	}
	first, _ := s.storedContext(k)
	if first.epoch != "ses_synthetic" || first.seen != clock.UnixMilli() {
		t.Fatalf("stored: %+v", first)
	}
	// Unchanged within a minute: not recorded again.
	*clock = clock.Add(30 * time.Second)
	s.openCodeContextRound(ctx)
	if v, _ := s.storedContext(k); v.seen != first.seen {
		t.Fatalf("confirmed within a minute: %+v", v)
	}
	// After a minute the unchanged reading confirms the value, keeping its source time.
	*clock = clock.Add(31 * time.Second)
	s.openCodeContextRound(ctx)
	confirmed, _ := s.storedContext(k)
	if confirmed.seen != clock.UnixMilli() || confirmed.At != 1793073206710 {
		t.Fatalf("confirmation: %+v", confirmed)
	}
	// A failed read confirms nothing, also after a minute.
	f.status = http.StatusInternalServerError
	*clock = clock.Add(2 * time.Minute)
	s.openCodeContextRound(ctx)
	if v, _ := s.storedContext(k); v.seen != confirmed.seen {
		t.Fatalf("failed read confirmed: %+v", v)
	}
	// It recovers, and a new reading replaces the old one at once.
	f.status = 0
	f.set("ses_synthetic", "["+assistant(`"finish":"stop","time":{"completed":1793073300000},"tokens":{"input":30000,"output":5,"reasoning":0,"cache":{"read":0,"write":0}}`)+"]")
	s.openCodeContextRound(ctx)
	if v, _ := s.storedContext(k); *v.UsedTokens != 30005 || v.At != 1793073300000 || v.seen != clock.UnixMilli() {
		t.Fatalf("new reading: %+v", v)
	}
	// No part text reached the observations or the peer status.
	status, err := s.PeerStatus(ctx, Key{"opencode", session.ID}, session.Name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(status)
	s.observed.mu.Lock()
	stored := fmt.Sprintf("%+v", s.observed.byKey)
	s.observed.mu.Unlock()
	if strings.Contains(string(data), "SENTINEL") || strings.Contains(stored, "SENTINEL") || !strings.Contains(string(data), "opencode_server") {
		t.Fatalf("peer status: %s", data)
	}
}

// A slow server delays neither the cycle controller nor the round past its request bounds;
// the round reads its sessions in parallel, a few at a time.
func TestOpenCodeContextRoundBounded(t *testing.T) {
	ctx := context.Background()
	s, f, _, register := openCodeRound(t)
	for _, id := range []string{"ses_a", "ses_b", "ses_c"} {
		register(id, true)
		f.set(id, f.messages["ses_synthetic"])
	}
	f.delay = make(chan struct{})
	defer close(f.delay)
	done := make(chan struct{})
	start := time.Now()
	go func() {
		s.openCodeContextRound(ctx)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	step := time.Now()
	if err := s.cycleStep(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(step) > 500*time.Millisecond {
		t.Fatalf("cycle step waited %v for the OpenCode round", time.Since(step))
	}
	select {
	case <-done:
	case <-time.After(5 * openCodeContextTimeout):
		t.Fatal("the round did not end at its request bounds")
	}
	if elapsed := time.Since(start); elapsed > 3*openCodeContextTimeout {
		t.Fatalf("round took %v: the reads were not parallel", elapsed)
	}
}

// Until the cycle adapter exists, a reading at the threshold admits nothing and changes no
// context setting: the mode stays as it is, and Enable is refused.
func TestOpenCodeContextObserveOnly(t *testing.T) {
	ctx := context.Background()
	s, f, _, register := openCodeRound(t)
	session := register("ses_synthetic", true)
	if err := s.SetContextPolicy(roomCtx("context-set"), "opencode", ContextHandoffCycle, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetContextEnable(roomCtx("context-enable"), session.Name, session.Revision, 0, true); err == nil {
		t.Fatal("Enable without an adapter")
	}
	f.set("ses_synthetic", "["+assistant(`"finish":"stop","time":{"completed":1793073300000},"tokens":{"input":199000,"output":5,"reasoning":0,"cache":{"read":0,"write":0}}`)+"]")
	s.openCodeContextRound(ctx)
	if v := s.sessionObservations(ctx, session)["context"].Value; v == nil || *v.UsedTokens != 199005 {
		t.Fatalf("reading: %+v", v)
	}
	if v, err := s.ContextCycleStatus(ctx, Key{"opencode", session.ID}); err != nil || v != nil {
		t.Fatalf("a cycle was admitted: %v %+v", err, v)
	}
	for _, p := range s.ContextPolicy() {
		if p.Family == "opencode" && (p.Mode != ContextHandoffCycle || p.Threshold != 1) {
			t.Fatalf("policy changed: %+v", p)
		}
	}
}
