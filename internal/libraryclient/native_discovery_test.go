package libraryclient

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
)

// Opt-in native metadata verification. It creates no thread/turn, uses no credentials
// and changes only temporary fixture homes. Ordinary CI has synthetic reader coverage.
func TestCodexNativeSkillDiscoveryMetadata(t *testing.T) {
	if os.Getenv("KOINON_CODEX_SKILL_PROBE") != "1" {
		t.Skip("set KOINON_CODEX_SKILL_PROBE=1 for isolated native metadata verification")
	}
	cli, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	home, native, project := filepath.Join(root, "home"), filepath.Join(root, "native"), filepath.Join(root, "project")
	for _, p := range []string{home, native, project} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "-q", project).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	name := "koinon-state-native-probe"
	skill := func(root string) string {
		t.Helper()
		p := filepath.Join(root, name, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: Synthetic Koinon native metadata probe.\n---\n\nSynthetic fixture for discovery metadata.\n"
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	machine := skill(filepath.Join(native, "skills"))
	shared := skill(filepath.Join(home, ".agents", "skills"))
	legacy := skill(filepath.Join(project, ".codex", "skills"))
	defaultMachine := skill(filepath.Join(home, ".codex", "skills"))
	canonical := skill(filepath.Join(project, "agents", "skills"))
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(project, ".agents", "skills")
	if err := os.Symlink("../agents/skills", pointer); err != nil {
		t.Fatal(err)
	}
	probe := func(custom string, expected []string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, "app-server")
		cmd.Dir = project
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + root, "TERM=dumb"}
		if custom != "" {
			cmd.Env = append(cmd.Env, "CODEX_HOME="+custom)
		}
		cmd.Stderr = io.Discard
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { input.Close(); cmd.Process.Kill(); cmd.Wait() }()
		encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
		send := func(value any) {
			t.Helper()
			if err := encoder.Encode(value); err != nil {
				t.Fatal(err)
			}
		}
		read := func(id int) json.RawMessage {
			t.Helper()
			for {
				var reply struct {
					ID     int             `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := decoder.Decode(&reply); err != nil {
					t.Fatal("native metadata response", err)
				}
				if reply.ID != id {
					continue
				}
				if reply.Error != nil {
					t.Fatal("native metadata method refused", reply.Error.Message)
				}
				return reply.Result
			}
		}
		send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "koinon_metadata_test", "version": "1"}}})
		read(1)
		send(map[string]any{"method": "initialized", "params": map[string]any{}})
		send(map[string]any{"id": 2, "method": "skills/list", "params": map[string]any{"cwds": []string{project}, "forceReload": true}})
		var response struct {
			Data []struct {
				Skills []struct {
					Name  string `json:"name"`
					Path  string `json:"path"`
					Scope string `json:"scope"`
				} `json:"skills"`
			} `json:"data"`
		}
		if err := json.Unmarshal(read(2), &response); err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, page := range response.Data {
			for _, s := range page.Skills {
				if s.Name == name {
					found[s.Path] = s.Scope
				}
			}
		}
		for _, p := range expected {
			want := "user"
			if strings.HasPrefix(p, project+string(os.PathSeparator)) {
				want = "repo"
			}
			if found[p] != want {
				t.Fatalf("native discovery did not report declared %s fixture target", want)
			}
		}
		if custom != "" && found[defaultMachine] != "" {
			t.Fatal("CODEX_HOME did not replace the legacy default user root")
		}
	}
	probe(native, []string{machine, shared, canonical, legacy})
	if err := os.Remove(pointer); err != nil {
		t.Fatal(err)
	}
	independent := skill(pointer)
	probe(native, []string{machine, shared, independent, legacy})
	probe("../native", []string{machine, shared, independent, legacy})
	probe("", []string{defaultMachine, shared, independent, legacy})
}
