package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// koinon context reads and changes the settings through the daemon (#391).
func TestContextCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-context", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		args = append([]string{args[0], "--state-dir", root, "--address", d.Addresses()[0]}, args[1:]...)
		err := contextCommand(context.Background(), args, &out)
		return out.String(), err
	}
	show := func() core.ContextSessionView {
		t.Helper()
		out, err := run("show", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Sessions []core.ContextSessionView `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(out), &reply); err != nil || len(reply.Sessions) != 1 {
			t.Fatalf("show: %v %s", err, out)
		}
		return reply.Sessions[0]
	}
	peer := show().Peer
	if out, err := run("set", "deepseek", "--mode", "handoff_cycle", "--threshold", "70"); err != nil || !strings.Contains(out, `"threshold":70`) {
		t.Fatalf("set: %v %s", err, out)
	}
	if _, err := run("threshold", peer, "55"); err != nil {
		t.Fatal(err)
	}
	if v := show(); v.Threshold != 55 || v.ThresholdSource != "session" || v.Mode != core.ContextHandoffCycle {
		t.Fatalf("session threshold: %+v", v)
	}
	if _, err := run("threshold", peer, "family"); err != nil {
		t.Fatal(err)
	}
	if v := show(); v.Threshold != 70 || v.ThresholdSource != "family" {
		t.Fatalf("family threshold: %+v", v)
	}
	if out, err := run("show"); err != nil || !strings.Contains(out, peer) || !strings.Contains(out, "70% (family)") {
		t.Fatalf("table: %v %s", err, out)
	}
	// DeepSeek has no cycle adapter, so Enable is refused; disable is accepted.
	if _, err := run("enable", peer); err == nil || !strings.Contains(err.Error(), "context_cycle_unavailable") {
		t.Fatalf("enable: %v", err)
	}
	if _, err := run("disable", peer); err != nil {
		t.Fatalf("disable: %v", err)
	}
	for _, args := range [][]string{{"threshold", peer, "0"}, {"threshold", peer}, {"set", "deepseek", "--mode", "observe"}, {"answer", peer, "1"}, {"nope"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}
