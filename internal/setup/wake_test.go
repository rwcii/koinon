package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/rwcii/koinon/internal/core"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgyStopOffersOnlyOwnUnacknowledgedNotice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	directory := t.TempDir()
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string, body any) json.RawMessage {
		t.Helper()
		data, e := core.Call(context.Background(), d.Addresses()[0], secret, path, body)
		if e != nil {
			t.Fatal(e)
		}
		return data
	}
	var target struct {
		Session core.Session `json:"session"`
	}
	json.Unmarshal(call("/v1/sessions/register", core.Registration{Family: "agy", ID: "synthetic-turn", Directory: directory}), &target)
	call("/v1/sessions/register", core.Registration{Family: "codex", ID: "synthetic-sender", Directory: directory})
	call("/v1/messages/send", map[string]any{"caller": core.Key{Family: "codex", ID: "synthetic-sender"}, "to": target.Session.Name, "body": "PRIVATE PEER BODY $(touch sentinel)"})
	env := HookEnv{Getenv: func(k string) string {
		return map[string]string{"KOINON_STATE_DIR": root, "KOINON_DAEMON_ADDRESS": d.Addresses()[0]}[k]
	}, Now: time.Now}
	var out bytes.Buffer
	if err = AgyStop(strings.NewReader(`{"conversationId":"synthetic-turn"}`), &out, env); err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if json.Unmarshal(out.Bytes(), &result) != nil || result["decision"] != "continue" || !strings.Contains(result["reason"], "1-1") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("Stop output: %s", out.Bytes())
	}
	call("/v1/inbox/ack", map[string]any{"caller": core.Key{Family: "agy", ID: "synthetic-turn"}, "through": 1})
	for _, input := range []string{`{"conversationId":"synthetic-turn"}`, `{"conversationId":"synthetic-other"}`, `{}`, `{"conversationId":"synthetic-turn","transcriptPath":"` + strings.Repeat("x", hookInputMax) + `"}`} {
		out.Reset()
		if err = AgyStop(strings.NewReader(input), &out, env); err != nil || out.String() != "{}\n" {
			t.Fatalf("unexpected continuation %q %v", out.String(), err)
		}
	}
}
