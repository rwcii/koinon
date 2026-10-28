package core

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

// deepSeekFake is a synthetic DeepSeek harness: one session's projections and event log, as
// raw JSON, behind the signed cookie. It answers session/page as the harness does: a
// contiguous run of events that ends before min(throughSeq+1, beforeSeq), here pageSize
// events long.
type deepSeekFake struct {
	mu          sync.Mutex
	t           *testing.T
	key         []byte
	session     string
	projections string   // the value of session/projections, or "null"
	events      []string // event i has seq i
	pageSize    int
	status      int    // a status other than 200 for every request, or 0
	result      string // a result to send in place of the real one, or ""
	rpcID       string // an rpcId to send in place of the request's, or ""
	page        func(records []string) []string
	delay       chan struct{}
	calls       map[string]int
}

func newDeepSeekFake(t *testing.T) (*deepSeekFake, wakeTarget) {
	t.Helper()
	f := &deepSeekFake{t: t, key: []byte(strings.Repeat("k", 32)), session: "session-synthetic", pageSize: 4, calls: map[string]int{}}
	credential := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(credential, []byte("client-connection/browser-session:\n  kind: grant\n  version: 1\n  secret: "+base64.RawURLEncoding.EncodeToString(f.key)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.sample()
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, wakeTarget{DSHURL: server.URL, DSHCredentials: credential}
}

// sample is the recorded dev-container session's shape (DeepSeek Harness 0.2.0-rc.2):
// its model and window at seq 11, the newest usage at seq 58 (728 input and 8960 cache read,
// so 9688) and the turn's end at 60. Message text is a sentinel.
func (f *deepSeekFake) sample() {
	f.events = nil
	for seq := range 61 {
		switch seq {
		case 11:
			f.add(`"type":"request/context","data":{"provider":"openrouter","model":"deepseek/deepseek-v4-flash","contextWindow":1024000}`)
		case 51:
			f.add(`"type":"assistant/message","data":{"message":"SENTINEL","usage":{"inputTokens":9408,"outputTokens":217,"totalTokens":9625}}`)
		case 58:
			f.add(`"type":"assistant/message","data":{"message":"SENTINEL","usage":{"inputTokens":728,"outputTokens":122,"totalTokens":9810,"cacheReadTokens":8960}}`)
		default:
			f.add(`"type":"step/end","data":{"text":"SENTINEL"}`)
		}
	}
	f.projections = deepSeekValue(60, `"pressureTokens":9688,"contextWindow":1024000,"projectedTokens":9808`, `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`)
}

func deepSeekValue(asOfSeq int, pressure, lastUsed string) string {
	return fmt.Sprintf(`{"asOfSeq":%d,"values":{"title":"SENTINEL","contextPressure":{%s},"modelSelection":{"lastUsed":%s,"next":null}}}`, asOfSeq, pressure, lastUsed)
}

// add appends an event with the next seq and a time that grows with it.
func (f *deepSeekFake) add(members string) {
	seq := len(f.events)
	f.events = append(f.events, fmt.Sprintf(`{%s,"seq":%d,"time":%d}`, members, seq, 1791916440000+int64(seq)*1000))
}

func (f *deepSeekFake) set(change func(*deepSeekFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *deepSeekFake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *deepSeekFake) serve(w http.ResponseWriter, r *http.Request) {
	t := f.t
	f.mu.Lock()
	defer f.mu.Unlock()
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	f.calls[method]++
	if delay := f.delay; delay != nil {
		f.mu.Unlock()
		select {
		case <-delay:
		case <-r.Context().Done():
		}
		f.mu.Lock()
	}
	if method != deepSeekProjectionsCall && method != deepSeekPageCall || r.Method != http.MethodPost {
		t.Errorf("a request that is not a read: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	audience := sha256.Sum256([]byte(r.Host))
	cookie, err := r.Cookie("dsh-auth-" + base64.RawURLEncoding.EncodeToString(audience[:]))
	parts := strings.Split(func() string {
		if err != nil {
			return ""
		}
		return cookie.Value
	}(), ".")
	mac := hmac.New(sha256.New, f.key)
	if len(parts) == 3 {
		mac.Write([]byte(parts[1]))
	}
	signature, _ := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
	if len(parts) != 3 || !hmac.Equal(mac.Sum(nil), signature) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var document struct {
		Type    string `json:"type"`
		RPCID   string `json:"rpcId"`
		Method  string `json:"method"`
		Payload struct {
			Args struct {
				Request struct {
					SessionID string `json:"sessionId"`
					Address   *struct {
						Kind      string `json:"kind"`
						SessionID string `json:"sessionId"`
					} `json:"address"`
					ThroughSeq  *int `json:"throughSeq"`
					BeforeSeq   *int `json:"beforeSeq"`
					MaxMessages int  `json:"maxMessages"`
				} `json:"request"`
			} `json:"args"`
		} `json:"payload"`
	}
	if json.NewDecoder(r.Body).Decode(&document) != nil || document.Type != "client-request" || document.Method != method || document.RPCID == "" {
		t.Errorf("wrong envelope: %+v", document)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rpcID := document.RPCID
	if f.rpcID != "" {
		rpcID = f.rpcID
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	respond := func(result string) {
		if f.result != "" {
			result = f.result
		}
		fmt.Fprintf(w, `{"type":"server-response","rpcId":%q,"result":%s}`, rpcID, result)
	}
	request := document.Payload.Args.Request
	if method == deepSeekProjectionsCall {
		if request.SessionID != f.session {
			respond(`{"ok":true,"value":null}`)
			return
		}
		respond(`{"ok":true,"value":` + f.projections + `}`)
		return
	}
	if request.Address == nil || request.Address.Kind != "session" || request.Address.SessionID != f.session || request.ThroughSeq == nil || request.MaxMessages <= 0 {
		t.Errorf("wrong page request: %+v", request)
		respond(`{"ok":false,"error":{"code":"gateway/bad-request"}}`)
		return
	}
	end := *request.ThroughSeq + 1
	if end > len(f.events) {
		respond(`{"ok":false,"error":{"code":"gateway/bad-request"}}`)
		return
	}
	if request.BeforeSeq != nil && *request.BeforeSeq < end {
		end = *request.BeforeSeq
	}
	start := max(0, end-f.pageSize)
	var records []string
	for _, event := range f.events[start:end] {
		records = append(records, `{"type":"event","event":`+event+`}`)
	}
	if f.page != nil {
		records = f.page(records)
	}
	respond(fmt.Sprintf(`{"ok":true,"value":{"records":[%s],"hasMore":%t}}`, strings.Join(records, ","), start > 0))
}

func readDeepSeek(t *testing.T, target wakeTarget) (model, usage *ObservedValue, err error) {
	t.Helper()
	model, usage, _, err = deepSeekContext(context.Background(), target, "session-synthetic")
	return model, usage, err
}

// The recorded sample gives the model, the prompt-side tokens of the newest usage, the
// window, and the usage event's own time; the scan crosses page boundaries.
func TestDeepSeekContextReading(t *testing.T) {
	f, target := newDeepSeekFake(t)
	for _, size := range []int{1, 4, 61} {
		f.set(func(f *deepSeekFake) { f.pageSize = size })
		model, usage, err := readDeepSeek(t, target)
		if err != nil || model == nil || usage == nil {
			t.Fatalf("page size %d: %v", size, err)
		}
		if model.Source != "dsh_projections" || model.ID != "openrouter/deepseek/deepseek-v4-flash" || model.At != 1791916498000 {
			t.Fatalf("model: %+v", model)
		}
		if usage.Source != "dsh_projections" || *usage.UsedTokens != 9688 || *usage.LimitTokens != 1024000 || usage.At != 1791916498000 || usage.Epoch != "session-synthetic" {
			t.Fatalf("usage: %+v", usage)
		}
	}
	if f.count(deepSeekProjectionsCall) != 3 || f.count("session/prompt") != 0 {
		t.Fatalf("calls: %v", f.calls)
	}
}

// Before its first usage a session has no reading, and no error. Events written after asOfSeq
// while the scan runs are outside the pinned page.
func TestDeepSeekContextBoundaries(t *testing.T) {
	f, target := newDeepSeekFake(t)
	f.set(func(f *deepSeekFake) {
		f.projections = deepSeekValue(60, `"contextWindow":1024000`, `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`)
	})
	if model, usage, err := readDeepSeek(t, target); err != nil || model != nil || usage != nil || f.count(deepSeekPageCall) != 0 {
		t.Fatalf("before a usage: %+v %+v %v", model, usage, err)
	}
	// Without the token meter's projection there is no reading either.
	f.set(func(f *deepSeekFake) {
		f.projections = `{"asOfSeq":60,"values":{"modelSelection":{"lastUsed":{"provider":"p","model":"m"}}}}`
	})
	if model, usage, err := readDeepSeek(t, target); err != nil || model != nil || usage != nil || f.count(deepSeekPageCall) != 0 {
		t.Fatalf("without the token meter: %+v %+v %v", model, usage, err)
	}
	f.set(func(f *deepSeekFake) {
		f.sample()
		f.add(`"type":"request/context","data":{"provider":"other","model":"other","contextWindow":5}`)
		f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":1}}`)
	})
	if _, usage, err := readDeepSeek(t, target); err != nil || *usage.UsedTokens != 9688 || usage.At != 1791916498000 {
		t.Fatalf("newer events: %+v %v", usage, err)
	}
	// A usage exactly at asOfSeq, with no cache counts: absent ones count as 0.
	f.set(func(f *deepSeekFake) {
		f.sample()
		f.events = f.events[:60]
		f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":9688}}`)
	})
	if _, usage, err := readDeepSeek(t, target); err != nil || *usage.UsedTokens != 9688 || usage.At != 1791916500000 {
		t.Fatalf("usage at asOfSeq: %+v %v", usage, err)
	}
	// An assistant attempt's stream gives its usage, as the harness's token meter takes it.
	f.set(func(f *deepSeekFake) {
		f.sample()
		f.events = f.events[:60]
		f.add(`"type":"assistant/attempt","data":{"stream":[{"type":"text","text":"SENTINEL"},{"type":"usage","usage":{"inputTokens":1}},{"type":"usage","usage":{"inputTokens":9000,"cacheWriteTokens":688}}]}`)
	})
	if _, usage, err := readDeepSeek(t, target); err != nil || *usage.UsedTokens != 9688 || usage.At != 1791916500000 {
		t.Fatalf("stream usage: %+v %v", usage, err)
	}
}

// Each incoherent or malformed reading is an error, never a fallback to an older usage.
func TestDeepSeekContextMalformed(t *testing.T) {
	model := `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`
	window := `,"contextWindow":1024000`
	for name, change := range map[string]func(*deepSeekFake){
		"null projections": func(f *deepSeekFake) { f.projections = "null" },
		"no model selection": func(f *deepSeekFake) {
			f.projections = `{"asOfSeq":60,"values":{"contextPressure":{"pressureTokens":9688` + window + `}}}`
		},
		"no last used": func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":9688`+window, "null") },
		"empty provider": func(f *deepSeekFake) {
			f.projections = deepSeekValue(60, `"pressureTokens":9688`+window, `{"provider":"","model":"m"}`)
		},
		"control in model": func(f *deepSeekFake) {
			f.projections = deepSeekValue(60, `"pressureTokens":9688`+window, `{"provider":"p","model":"m\n"}`)
		},
		"no window": func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":9688`, model) },
		"zero window": func(f *deepSeekFake) {
			f.projections = deepSeekValue(60, `"pressureTokens":9688,"contextWindow":0`, model)
		},
		"negative pressure":   func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":-1`+window, model) },
		"fractional pressure": func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":9688.5`+window, model) },
		"string pressure":     func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":"9688"`+window, model) },
		"null pressure":       func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":null`+window, model) },
		"unsafe pressure": func(f *deepSeekFake) {
			f.projections = deepSeekValue(60, `"pressureTokens":9007199254740992`+window, model)
		},
		"negative asOfSeq":     func(f *deepSeekFake) { f.projections = deepSeekValue(-1, `"pressureTokens":9688`+window, model) },
		"asOfSeq past the log": func(f *deepSeekFake) { f.projections = deepSeekValue(70, `"pressureTokens":9688`+window, model) },
		"usage differs":        func(f *deepSeekFake) { f.projections = deepSeekValue(60, `"pressureTokens":9689`+window, model) },
		"context after usage": func(f *deepSeekFake) {
			f.events[59] = strings.Replace(f.events[11], `"seq":11,"time":1791916451000`, `"seq":59,"time":1791916499000`, 1)
		},
		"selection after usage": func(f *deepSeekFake) {
			f.events[60] = `{"type":"model/selection","data":{"provider":"p","model":"m"},"seq":60,"time":1791916500000}`
		},
		"no input tokens": func(f *deepSeekFake) { f.events[58] = strings.Replace(f.events[58], `"inputTokens":728,`, ``, 1) },
		"null cache write": func(f *deepSeekFake) {
			f.events[58] = strings.Replace(f.events[58], `"cacheReadTokens":8960`, `"cacheReadTokens":8960,"cacheWriteTokens":null`, 1)
		},
		"fractional cache write": func(f *deepSeekFake) {
			f.events[58] = strings.Replace(f.events[58], `"cacheReadTokens":8960`, `"cacheReadTokens":8960,"cacheWriteTokens":0.0`, 1)
		},
		"negative cache read": func(f *deepSeekFake) { f.events[58] = strings.Replace(f.events[58], `8960`, `-8960`, 1) },
		"null usage": func(f *deepSeekFake) {
			f.events[58] = strings.Replace(f.events[58], `"usage":{`, `"usage":null,"other":{`, 1)
		},
		"no usage in budget": func(f *deepSeekFake) {
			for _, seq := range []int{51, 58} {
				f.events[seq] = fmt.Sprintf(`{"type":"step/end","seq":%d,"time":%d}`, seq, 1791916440000+int64(seq)*1000)
			}
		},
		"gap in a page":       func(f *deepSeekFake) { f.page = func(r []string) []string { return append(r[:1], r[2:]...) } },
		"duplicate in a page": func(f *deepSeekFake) { f.page = func(r []string) []string { return append(r, r[len(r)-1]) } },
		"event past asOfSeq": func(f *deepSeekFake) {
			f.page = func(r []string) []string {
				return append(r, `{"type":"event","event":{"type":"step/end","seq":61,"time":1791916501000}}`)
			}
		},
		"empty page": func(f *deepSeekFake) { f.page = func([]string) []string { return nil } },
		// The usage at seq 58 is in the first page (57-60); each defect is in its earlier part.
		"event past asOfSeq before the page": func(f *deepSeekFake) {
			f.page = func(r []string) []string {
				return append([]string{`{"type":"event","event":{"type":"step/end","seq":61,"time":1791916501000}}`}, r...)
			}
		},
		"conflicting seq before the usage": func(f *deepSeekFake) {
			f.page = func(r []string) []string {
				r[0] = `{"type":"event","event":{"type":"request/context","seq":58,"time":1791916497000,"data":{"provider":"p","model":"m","contextWindow":5}}}`
				return r
			}
		},
		"not an event before the usage": func(f *deepSeekFake) { f.page = func(r []string) []string { r[0] = `{"type":"other"}`; return r } },
		"not an event": func(f *deepSeekFake) {
			f.page = func(r []string) []string { r[len(r)-1] = `{"type":"other"}`; return r }
		},
		"zero event time": func(f *deepSeekFake) {
			f.events[58] = strings.Replace(f.events[58], `"time":1791916498000`, `"time":0`, 1)
		},
		"refused":      func(f *deepSeekFake) { f.result = `{"ok":false,"error":{"code":"session/not-found"}}` },
		"other rpc ID": func(f *deepSeekFake) { f.rpcID = "other" },
		"unauthorized": func(f *deepSeekFake) { f.status = http.StatusUnauthorized },
		"redirect":     func(f *deepSeekFake) { f.status = http.StatusFound },
		"oversized result": func(f *deepSeekFake) {
			f.result = `{"ok":true,"value":"` + strings.Repeat("x", deepSeekResponseBytes) + `"}`
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, target := newDeepSeekFake(t)
			f.set(change)
			if model, usage, err := readDeepSeek(t, target); err == nil || model != nil || usage != nil {
				t.Fatalf("accepted: %+v %+v %v", model, usage, err)
			}
		})
	}
}

// Requests go only to a loopback origin, with a credential file that can be read; each is
// bounded, and so is the whole scan.
func TestDeepSeekContextTransport(t *testing.T) {
	f, target := newDeepSeekFake(t)
	other := target
	other.DSHURL = "http://example.invalid"
	if _, _, _, err := deepSeekContext(context.Background(), other, "session-synthetic"); err == nil {
		t.Fatal("a non-loopback origin")
	}
	other = target
	other.DSHCredentials = filepath.Join(t.TempDir(), "missing.yaml")
	if _, _, _, err := deepSeekContext(context.Background(), other, "session-synthetic"); err == nil || f.count(deepSeekProjectionsCall) != 0 {
		t.Fatal("a missing credential file")
	}
	f.set(func(f *deepSeekFake) { f.delay = make(chan struct{}) })
	defer close(f.delay)
	start := time.Now()
	if _, _, err := readDeepSeek(t, target); err == nil || time.Since(start) > 3*deepSeekContextTimeout {
		t.Fatalf("a slow harness: %v after %v", err, time.Since(start))
	}
}

func deepSeekRound(t *testing.T) (*Store, *deepSeekFake, *time.Time, func(id string, harnessed bool) Session) {
	t.Helper()
	s, _ := testStore(t)
	f, target := newDeepSeekFake(t)
	clock := time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return clock }
	directory := t.TempDir()
	register := func(id string, harnessed bool) Session {
		registration := Registration{Family: "deepseek", ID: id, Directory: directory}
		if harnessed {
			registration.WakeTarget, _ = json.Marshal(target)
		}
		session, err := s.Register(context.Background(), registration)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	return s, f, &clock, register
}

// A round records a harnessed session's reading with its usage event's time; peer status and
// the dashboard's views show it. An unchanged reading is recorded again only after a minute
// and keeps its source time, also after unrelated events; a failed read confirms nothing.
// A session without a harness target has no source.
func TestDeepSeekContextRound(t *testing.T) {
	ctx := context.Background()
	s, f, clock, register := deepSeekRound(t)
	session := register("session-synthetic", true)
	bare := register("session-bare", false)
	k := Key{"deepseek", session.ID}
	if views := s.sessionObservations(ctx, session); views["context"].Reason != "not_observed" || views["model"].Reason != "not_observed" {
		t.Fatalf("before a reading: %+v", views)
	}
	if views := s.sessionObservations(ctx, bare); views["context"].Reason != "no_source" || views["model"].Reason != "no_source" {
		t.Fatalf("without a harness: %+v", views)
	}
	s.deepSeekContextRound(ctx)
	views := s.sessionObservations(ctx, session)
	if v := views["context"].Value; v == nil || v.Source != "dsh_projections" || *v.UsedTokens != 9688 || *v.LimitTokens != 1024000 || v.At != 1791916498000 {
		t.Fatalf("context: %+v", views["context"])
	}
	if v := views["model"].Value; v == nil || v.ID != "openrouter/deepseek/deepseek-v4-flash" {
		t.Fatalf("model: %+v", views["model"])
	}
	first, _ := s.storedContext(k)
	if first.epoch != "session-synthetic" || first.seen != clock.UnixMilli() {
		t.Fatalf("stored: %+v", first)
	}
	// An unrelated event, such as a new title, changes neither the reading nor its time.
	f.set(func(f *deepSeekFake) {
		f.add(`"type":"session/title","data":{"title":"SENTINEL"}`)
		f.projections = deepSeekValue(61, `"pressureTokens":9688,"contextWindow":1024000`, `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`)
	})
	*clock = clock.Add(30 * time.Second)
	s.deepSeekContextRound(ctx)
	if v, _ := s.storedContext(k); v.seen != first.seen {
		t.Fatalf("confirmed within a minute: %+v", v)
	}
	*clock = clock.Add(31 * time.Second)
	s.deepSeekContextRound(ctx)
	confirmed, _ := s.storedContext(k)
	if confirmed.seen != clock.UnixMilli() || confirmed.At != 1791916498000 {
		t.Fatalf("confirmation: %+v", confirmed)
	}
	// A new usage event with the same counts, model and millisecond is a new reading, recorded
	// at once.
	f.set(func(f *deepSeekFake) {
		f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":9688}}`)
		f.events[len(f.events)-1] = strings.Replace(f.events[len(f.events)-1], `"time":1791916502000`, `"time":1791916498000`, 1)
		f.projections = deepSeekValue(62, `"pressureTokens":9688,"contextWindow":1024000`, `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`)
	})
	*clock = clock.Add(time.Second)
	s.deepSeekContextRound(ctx)
	if v, _ := s.storedContext(k); v.seen != clock.UnixMilli() || v.At != 1791916498000 {
		t.Fatalf("a new usage with the same values: %+v", v)
	}
	confirmed, _ = s.storedContext(k)
	f.set(func(f *deepSeekFake) { f.status = http.StatusInternalServerError })
	*clock = clock.Add(2 * time.Minute)
	s.deepSeekContextRound(ctx)
	if v, _ := s.storedContext(k); v.seen != confirmed.seen {
		t.Fatalf("failed read confirmed: %+v", v)
	}
	// It recovers, and a new usage with new counts replaces the reading at once.
	f.set(func(f *deepSeekFake) {
		f.status = 0
		f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":20000,"cacheReadTokens":10000}}`)
		f.projections = deepSeekValue(63, `"pressureTokens":30000,"contextWindow":1024000`, `{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}`)
	})
	s.deepSeekContextRound(ctx)
	if v, _ := s.storedContext(k); *v.UsedTokens != 30000 || v.At != 1791916503000 || v.seen != clock.UnixMilli() {
		t.Fatalf("new reading: %+v", v)
	}
	if f.count("session/prompt") != 0 {
		t.Fatalf("calls: %v", f.calls)
	}
	// No message text reached the observations or the peer status.
	status, err := s.PeerStatus(ctx, k, session.Name)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(status)
	s.observed.mu.Lock()
	stored := fmt.Sprintf("%+v", s.observed.byKey)
	s.observed.mu.Unlock()
	if strings.Contains(string(data), "SENTINEL") || strings.Contains(stored, "SENTINEL") || !strings.Contains(string(data), "dsh_projections") {
		t.Fatalf("peer status: %s", data)
	}
}

// A slow harness delays neither the cycle controller nor the round past its bounds.
func TestDeepSeekContextRoundBounded(t *testing.T) {
	ctx := context.Background()
	s, f, _, register := deepSeekRound(t)
	register("session-synthetic", true)
	f.set(func(f *deepSeekFake) { f.delay = make(chan struct{}) })
	defer close(f.delay)
	done := make(chan struct{})
	go func() {
		s.deepSeekContextRound(ctx)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	step := time.Now()
	if err := s.cycleStep(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(step) > 500*time.Millisecond {
		t.Fatalf("cycle step waited %v for the DeepSeek round", time.Since(step))
	}
	select {
	case <-done:
	case <-time.After(5 * deepSeekContextTimeout):
		t.Fatal("the round did not end at its request bounds")
	}
}
