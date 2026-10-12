package contributor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

// Exercise the actual shipped binary's daemon and command transports from a directory
// containing no checkout, with no executable on PATH and only temporary state/home.
func TestStandaloneBinaryCatalogQueries(t *testing.T) {
	scratch := t.TempDir()
	binary := filepath.Join(scratch, "koinon")
	build := exec.Command("go", "build", "-o", binary, "./cmd/koinon")
	build.Dir = root(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "HOME" && key != "PATH" && !strings.HasPrefix(key, "XDG_") && !strings.HasPrefix(key, "KOINON_") && key != "CODEX_HOME" && key != "CLAUDE_CONFIG_DIR" {
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+scratch, "PATH=", "XDG_STATE_HOME="+scratch, "XDG_CONFIG_HOME="+scratch, "CODEX_HOME="+scratch, "CLAUDE_CONFIG_DIR="+scratch)
	state := filepath.Join(scratch, "state")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	server := exec.CommandContext(ctx, binary, "serve", "--state-dir", state, "--listen", "127.0.0.1:0", "--listen-v6", "[::1]:0")
	server.Dir, server.Env, server.Stderr = scratch, env, io.Discard
	pipe, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	t.Cleanup(func() {
		server.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			server.Process.Kill()
			<-done
			t.Error("synthetic daemon shutdown timeout")
		}
	})
	ready := make(chan []string, 1)
	go func() {
		var value struct {
			Listeners []string `json:"listeners"`
		}
		if json.NewDecoder(pipe).Decode(&value) != nil {
			ready <- nil
			return
		}
		ready <- value.Listeners
	}()
	var listeners []string
	select {
	case listeners = <-ready:
	case <-time.After(15 * time.Second):
		t.Fatal("synthetic daemon startup timeout")
	}
	if len(listeners) == 0 {
		t.Fatal("synthetic daemon failed to report listeners")
	}
	secret, err := core.ReadSecret(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), listeners[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-standalone", Directory: scratch}); err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir, cmd.Env = scratch, env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
		return out
	}
	var fingerprint map[string]string
	if err := json.Unmarshal(command("version"), &fingerprint); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Library library.Page `json:"library"`
	}
	options := []string{"--as", "deepseek:synthetic-standalone", "--state-dir", state, "--address", listeners[0]}
	if err := json.Unmarshal(command(append([]string{"library", "list", "--limit", "32"}, options...)...), &page); err != nil {
		t.Fatal(err)
	}
	cat, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	if page.Library.More || len(page.Library.Items) != len(cat.Items) || page.Library.Digest != fingerprint["catalog_digest"] || page.Library.Digest != cat.Digest {
		t.Fatal("standalone catalog incomplete")
	}
	for _, item := range page.Library.Items {
		var read struct {
			Library library.ReadResult `json:"library"`
		}
		if err := json.Unmarshal(command(append([]string{"library", "read", item.ID}, options...)...), &read); err != nil {
			t.Fatal(err)
		}
		want, _ := cat.Read("deepseek", item.ID)
		if read.Library.Item.Content != want.Item.Content || read.Library.Item.ContentDigest != want.Item.ContentDigest {
			t.Fatalf("standalone content differs: %s", item.ID)
		}
	}
}
