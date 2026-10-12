package mcp

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// outage stops the daemon, ends the session's lifetime in the state database, as an outage
// longer than the session lifetime does, and starts the daemon again at the same address.
func (h *harness) outage(id string) {
	h.t.Helper()
	address := h.d.Addresses()[0]
	if err := h.d.Close(); err != nil {
		h.t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(h.root, "state.sqlite3"))
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sessions SET expires_at=1 WHERE family='codex' AND id=?`, id); err != nil {
		h.t.Fatal(err)
	}
	db.Close()
	restarted, err := core.Start(core.Config{StateDir: h.root, Listen: []string{address}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.d = restarted
	h.t.Cleanup(func() { restarted.Close() })
}

// otherServer is a second koinon mcp in the same directory under another host process, with
// its own launch.
func (h *harness) otherServer(parent int) *server {
	h.t.Helper()
	secret, err := core.ReadSecret(h.root)
	if err != nil {
		h.t.Fatal(err)
	}
	id, err := core.CreateLaunch(context.Background(), h.d.Addresses()[0], secret,
		core.LaunchTarget{Family: "codex", Directory: h.s.c.Directory, CLI: "/synthetic/cli", HostPID: parent})
	if err != nil {
		h.t.Fatal(err)
	}
	env := map[string]string{"KOINON_LAUNCH_ID": id}
	c := h.s.c
	c.ParentPID, c.Getenv = parent, func(k string) string { return env[k] }
	return &server{c: c, sessions: map[core.Key]registered{}}
}

func (h *harness) session(id string) core.Session {
	h.t.Helper()
	for _, s := range h.sessions() {
		if s.ID == id {
			return s
		}
	}
	h.t.Fatalf("no session %s", id)
	return core.Session{}
}

// A session whose registration expired while the daemon was down registers again at its next
// renewal, without a tool call, and takes back its participant address when no other session
// took it. When another session took the address, the holder change retired and fenced the
// expired one, and its renewal leaves it dropped (#283 chunk 03).
func TestRenewalRegistersExpiredSession(t *testing.T) {
	h := newHarness(t, "codex-mcp-client")
	if out, err := exec.Command("git", "init", "-q", h.s.c.Directory).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	// Both hosts are live processes, so neither session's host is proven ended.
	h.s.c.ParentPID = os.Getpid()
	h.launch("codex")
	a := map[string]any{"threadId": "synthetic-a"}
	if _, isError := h.tool("peers", map[string]any{}, a); isError {
		t.Fatal("peers")
	}
	first := h.session("synthetic-a")
	if !first.HoldsAddress {
		t.Fatalf("a does not hold its address: %+v", first)
	}
	h.outage("synthetic-a")
	if s := h.session("synthetic-a"); s.State != "expired" || s.HoldsAddress {
		t.Fatalf("not expired: %+v", s)
	}
	// The renewal timer alone brings it back, with the same name and the address.
	h.s.renew(context.Background())
	again := h.session("synthetic-a")
	if again.State != "active" || again.Name != first.Name || !again.HoldsAddress {
		t.Fatalf("after renewal: %+v", again)
	}
	if _, kept := h.s.sessions[core.Key{Family: "codex", ID: "synthetic-a"}]; !kept {
		t.Fatal("the renewal set lost the session")
	}
	// After another outage, the next tool call registers it again, also with the address.
	h.outage("synthetic-a")
	if _, isError := h.tool("peers", map[string]any{}, a); isError {
		t.Fatal("peers after the outage")
	}
	if s := h.session("synthetic-a"); s.State != "active" || s.Name != first.Name || !s.HoldsAddress {
		t.Fatalf("after a tool call: %+v", s)
	}
	// Expired again while a session of another host process took the address. The holder
	// change retired and fenced a, so its renewal leaves it dropped and b keeps the address.
	h.outage("synthetic-a")
	other := h.otherServer(os.Getppid())
	if err := other.ensure(context.Background(), core.Key{Family: "codex", ID: "synthetic-b"}); err != nil {
		t.Fatal(err)
	}
	if !h.session("synthetic-b").HoldsAddress {
		t.Fatal("b did not take the unheld address")
	}
	h.s.renew(context.Background())
	if _, kept := h.s.sessions[core.Key{Family: "codex", ID: "synthetic-a"}]; kept {
		t.Fatal("a fenced session stayed in the renewal set")
	}
	for _, s := range h.sessions() {
		if s.ID == "synthetic-a" && s.State == "active" {
			t.Fatalf("a fenced session came back: %+v", s)
		}
	}
	if !h.session("synthetic-b").HoldsAddress {
		t.Fatal("b lost the address to a renewal")
	}
}
