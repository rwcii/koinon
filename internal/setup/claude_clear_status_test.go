package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

func TestClaudeStatusBinaryAfterClear(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	binary := filepath.Join(t.TempDir(), "koinon")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/koinon")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	root, dir := filepath.Join(t.TempDir(), "state"), t.TempDir()
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	ctx := context.Background()
	launch, err := core.CreateLaunch(ctx, address, secret, core.LaunchTarget{Family: "claude", Directory: dir, CLI: "/synthetic/cli", HostPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	data, err := core.Call(ctx, address, secret, "/v1/sessions/register", core.Registration{
		Family: "claude", ID: "synthetic-before-clear", Directory: dir, LaunchID: launch, Ancestors: []int{os.Getpid()},
		WakeTarget: json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, os.Getpid())), ToolCall: true})
	if err != nil {
		t.Fatal(err)
	}
	var registered struct {
		Session core.Session `json:"session"`
	}
	if err := json.Unmarshal(data, &registered); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "hook", "claude-status", "--command", "printf preserved")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "KOINON_STATE_DIR=" + root, "KOINON_DAEMON_ADDRESS=" + address}
	cmd.Stdin = strings.NewReader(`{"session_id":"synthetic-after-clear","model":{"id":"claude-synthetic","display_name":"Synthetic"},"context_window":{"context_window_size":200000,"total_input_tokens":700,"current_usage":{"input_tokens":1}}}`)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil || out.String() != "preserved" || errOut.Len() != 0 {
		t.Fatalf("hook: %v stdout=%q stderr=%q", err, out.String(), errOut.String())
	}
	data, err = core.Call(ctx, address, secret, "/v1/peers/status", map[string]any{"caller": core.Key{Family: "claude", ID: "synthetic-before-clear"}, "peer": registered.Session.Name})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Peer core.PeerStatus `json:"peer"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Peer.Model.Known || result.Peer.Model.ID != "claude-synthetic" || !result.Peer.Context.Known || *result.Peer.Context.UsedTokens != 700 || result.Peer.Name != registered.Session.Name {
		t.Fatalf("post-clear native hook report: %+v", result.Peer)
	}
	data, err = core.Call(ctx, address, secret, "/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	var sessions struct {
		Sessions []core.Session `json:"sessions"`
	}
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != "synthetic-before-clear" {
		t.Fatal("status line created or moved a registration")
	}
}
