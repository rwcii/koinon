package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// A synthetic daemon returns a remaining guard duration. Its wall clock deliberately
// differs from the MCP clock: only the duration may schedule a retry.
func TestSuccessionRetriesOnToolCall(t *testing.T) {
	for _, reason := range []string{"holder_active", "fenced", "host_running", "no_host", "other_pane", "subagent", "other_participant"} {
		t.Run(reason, func(t *testing.T) {
			h := newHarness(t, "codex-mcp-client")
			h.d.Close()
			h.env["KOINON_LAUNCH_ID"] = strings.Repeat("a", 64)
			h.env["TMUX"], h.env["TMUX_PANE"] = "/synthetic/tmux/default,1,2", "%3"
			h.s.c.Parents = func() (map[int]int, error) { return map[int]int{4242: 100}, nil }
			h.s.c.Command = func(int) (string, []string, error) { return "/synthetic/sh", nil, nil }
			h.s.c.Tmux = func(_ context.Context, socket string, args ...string) (string, error) {
				if len(args) != 5 || args[0] != "display-message" || socket != "/synthetic/tmux/default" {
					t.Errorf("non-observation tmux call: %s %v", socket, args)
				}
				return "%3\t100\t$1\n", nil
			}
			now := time.Unix(1000, 0)
			h.s.c.Now = func() time.Time { return now }
			var mu sync.Mutex
			var arrivals []core.Registration
			toolCalls := 0
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/sessions/register":
					var request core.Registration
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					arrivals = append(arrivals, request)
					wait := int64(20_000)
					if len(arrivals) == 2 {
						wait = 25_000
					}
					session := core.Session{Family: "codex", Revision: int64(len(arrivals)), Succession: &core.Succession{Result: "refused", Reason: reason, At: 9_000_000, RetryAfterMS: wait}}
					if reason == "holder_active" && len(arrivals) == 3 {
						session.HoldsAddress = true
						session.Succession = &core.Succession{Result: "succeeded", Reason: "same_host"}
					}
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "session": session})
				case "/v1/sessions/renew":
					// A historical refusal on a renewal must not reset the retry deadline.
					json.NewEncoder(w).Encode(map[string]any{"ok": true, "session": core.Session{Revision: 100, Succession: &core.Succession{Result: "refused", Reason: reason, At: 9_000_000, RetryAfterMS: 20_000}}})
				default:
					toolCalls++
					fmt.Fprint(w, `{"ok":true,"peers":[]}`)
				}
			}))
			defer daemon.Close()
			h.s.c.Address = strings.TrimPrefix(daemon.URL, "http://")
			peers := func(want int) {
				t.Helper()
				if out, bad := h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-successor"}); bad {
					t.Fatal(out)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(arrivals) != want {
					t.Fatalf("registrations: got %d want %d", len(arrivals), want)
				}
			}
			peers(1)
			now = now.Add(10 * time.Second)
			h.s.renew(context.Background())
			peers(1)
			now = now.Add(10 * time.Second)
			if reason != "holder_active" {
				peers(1)
				return
			}
			peers(2) // Same holder called again: daemon extends the wait.
			now = now.Add(24 * time.Second)
			h.s.renew(context.Background())
			peers(2)
			now = now.Add(time.Second)
			peers(3)
			now = now.Add(time.Second)
			peers(3)
			mu.Lock()
			defer mu.Unlock()
			for _, r := range arrivals {
				if r.Tmux == nil || r.Tmux.Socket != "/synthetic/tmux/default" || r.Tmux.Pane != "%3" {
					t.Fatalf("registration lacks proven own pane: %+v", r)
				}
				if !r.ToolCall || r.Family != "codex" || r.ID != "synthetic-successor" {
					t.Fatalf("registration lacks native tool provenance: %+v", r)
				}
			}
			if toolCalls != 6 {
				t.Fatalf("tools did not continue during refusals: %d", toolCalls)
			}
		})
	}
}

// Both initial registration and a caller_inactive retry are caused by an actual native
// tool call; a renewal failure deletes the cache but never registers on its own.
func TestSuccessionRegistrationAfterInactiveAndRenewal(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	h.d.Close()
	h.env["KOINON_LAUNCH_ID"] = strings.Repeat("a", 64)
	var mu sync.Mutex
	arrivals := 0
	tools := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions/register":
			var request core.Registration
			json.NewDecoder(r.Body).Decode(&request)
			if !request.ToolCall {
				t.Error("registration has no tool_call")
			}
			arrivals++
			fmt.Fprint(w, `{"ok":true,"session":{"revision":1}}`)
		case "/v1/sessions/renew":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"ok":false,"code":"caller_inactive"}`)
		default:
			tools++
			if tools == 1 {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"ok":false,"code":"caller_inactive"}`)
			} else {
				fmt.Fprint(w, `{"ok":true,"peers":[]}`)
			}
		}
	}))
	defer daemon.Close()
	h.s.c.Address = strings.TrimPrefix(daemon.URL, "http://")
	peers := func() {
		t.Helper()
		if out, bad := h.tool("peers", map[string]any{}, map[string]any{"threadId": "synthetic-successor"}); bad {
			t.Fatal(out)
		}
	}
	peers()
	h.s.renew(context.Background())
	h.s.renew(context.Background())
	mu.Lock()
	if arrivals != 2 {
		t.Fatalf("renewal registered: %d", arrivals)
	}
	mu.Unlock()
	peers()
	mu.Lock()
	defer mu.Unlock()
	if arrivals != 3 {
		t.Fatalf("first tool after failed renewal did not register: %d", arrivals)
	}
}

// Registration uses the naming ancestor proof without sending any terminal input or
// renaming a terminal. Foreign, nested and unobservable panes provide no evidence.
func TestSuccessionRegistrationPane(t *testing.T) {
	for _, c := range []struct {
		name    string
		panePID int
		parents map[int]int
		nested  bool
		want    bool
	}{
		{"own", 100, map[int]int{200: 100}, false, true},
		{"foreign", 300, map[int]int{200: 100}, false, false},
		{"nested", 100, map[int]int{200: 150, 150: 100}, true, false},
		{"unknown", 100, map[int]int{}, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := &server{c: Config{ParentPID: 200, Getenv: func(k string) string {
				if k == "TMUX" {
					return "/synthetic/tmux/default,1,2"
				}
				if k == "TMUX_PANE" {
					return "%3"
				}
				return ""
			}, Parents: func() (map[int]int, error) { return c.parents, nil }, Command: func(pid int) (string, []string, error) {
				if c.nested && pid == 150 {
					return "/synthetic/claude", nil, nil
				}
				return "/synthetic/sh", nil, nil
			}, Tmux: func(ctx context.Context, socket string, args ...string) (string, error) {
				if len(args) != 5 || args[0] != "display-message" || socket != "/synthetic/tmux/default" {
					t.Fatalf("non-observation tmux call: %s %v", socket, args)
				}
				return fmt.Sprintf("%%3\t%d\t$1\n", c.panePID), nil
			}}}
			got := s.registrationPane(context.Background())
			if (got != nil) != c.want {
				t.Fatalf("pane: %+v", got)
			}
			if got != nil && (got.Socket != "/synthetic/tmux/default" || got.Pane != "%3") {
				t.Fatalf("pane: %+v", got)
			}
		})
	}
}
