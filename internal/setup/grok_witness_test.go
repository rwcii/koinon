package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// The Grok status-line hook gives the daemon each session with itself as the reporter, also an
// input without counts, as the new session after /new sends before it has any; an input whose
// session cannot be the caller gives none (#515).
func TestGrokStatusWitness(t *testing.T) {
	var mu sync.Mutex
	var witnesses []core.AgyWitness
	witnessed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report core.AgyWitness
		if r.URL.Path == "/v1/cycles/grok-witness" && json.NewDecoder(r.Body).Decode(&report) == nil {
			mu.Lock()
			witnesses = append(witnesses, report)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(witnessed.Close)
	daemon, _ := reportingDaemon(t)
	address, state := strings.TrimPrefix(witnessed.URL, "http://"), daemon.Getenv("KOINON_STATE_DIR")
	run := func(input, session string) []core.AgyWitness {
		mu.Lock()
		witnesses = nil
		mu.Unlock()
		env := HookEnv{Getenv: func(k string) string {
			return map[string]string{"KOINON_STATE_DIR": state, "KOINON_DAEMON_ADDRESS": address, "GROK_SESSION_ID": session}[k]
		}, Now: time.Now}
		var out, errOut bytes.Buffer
		if status := GrokStatus(nil, strings.NewReader(input), &out, &errOut, env); status != 0 || out.Len() != 0 {
			t.Fatalf("status %d %q", status, out.String())
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]core.AgyWitness(nil), witnesses...)
	}
	full := `{"session_id":"` + grokSynthetic + `","model":{"id":"m"},"context_window":{"context_window_size":10,"context_tokens":1}}`
	if got := run(full, ""); len(got) != 1 || got[0].Conversation != grokSynthetic || got[0].Reporter.PID != os.Getpid() || got[0].Reporter.Start <= 0 {
		t.Fatalf("full input: %+v", got)
	}
	if got := run(`{"session_id":"`+grokSynthetic+`"}`, ""); len(got) != 1 || got[0].Conversation != grokSynthetic {
		t.Fatalf("no counts: %+v", got)
	}
	for name, c := range map[string]struct{ input, env string }{
		"absent":       {`{"model":{"id":"m"}}`, ""},
		"not a string": {`{"session_id":7}`, ""},
		"malformed":    {`{"session_id":"../x"}`, ""},
		"env mismatch": {full, "another-session"},
		"not json":     {`session`, ""},
	} {
		if got := run(c.input, c.env); len(got) != 0 {
			t.Fatalf("%s: %+v", name, got)
		}
	}
}
