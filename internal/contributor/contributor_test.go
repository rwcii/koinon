// Package contributor checks the repository contracts that survive runtime retirement.
package contributor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func root(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}
func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSharedSkillsAndGuidance(t *testing.T) {
	for _, name := range []string{".claude/skills", ".agents/skills", ".codex/skills"} {
		target, err := os.Readlink(filepath.Join(root(t), name))
		if err != nil || target != "../agents/skills" {
			t.Fatalf("%s: %q %v", name, target, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root(t), "agents/skills"))
	if err != nil {
		t.Fatal(err)
	}
	index := read(t, "agents/skills/README.md")
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		text := read(t, "agents/skills/"+name+"/SKILL.md")
		if !strings.HasPrefix(text, "---\nname: "+name+"\ndescription: ") || !strings.Contains(text, "agents/skills/AGENTS.md") {
			t.Errorf("%s: missing shared skill contract", name)
		}
		if !strings.Contains(index, "("+name+"/SKILL.md)") {
			t.Errorf("%s: missing index entry", name)
		}
	}
	// Reconnect/terminal skills obtain guidance from the runtime, not copied recipes.
	recipe := regexp.MustCompile(`\bkoinon (guide|setup|register|peers|send|inbox|ack|memory|work|claim)\b`)
	for _, name := range []string{"handoff", "pickup", "peer-tmux"} {
		text := read(t, "agents/skills/"+name+"/SKILL.md")
		if !strings.Contains(text, "koinon guide") {
			t.Errorf("%s: no runtime guide", name)
		}
		for _, command := range recipe.FindAllString(text, -1) {
			if command != "koinon guide" {
				t.Errorf("%s copies recipe %s", name, command)
			}
		}
	}
}

func TestRuntimeRetirementAndCICoverage(t *testing.T) {
	for _, name := range []string{"koinon", "tests", "bridge.py", "notify.py", "session.py", "memory.py", "memory_service.py", "session_service.py", "codex_launch.py", "statusline.py", "usage_report.py", "scripts/install.py", "scripts/upgrade.py", "scripts/uninstall.py", "scripts/make-import-fixtures.py"} {
		if _, err := os.Lstat(filepath.Join(root(t), name)); !os.IsNotExist(err) {
			t.Errorf("retired path remains: %s (%v)", name, err)
		}
	}
	paths, err := filepath.Glob(filepath.Join(root(t), ".github/workflows/native-*.yml"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("native workflows: %v %v", paths, err)
	}
	nativeWorkflow := read(t, ".github/workflows/native-go-lifecycle.yml")
	if strings.Contains(nativeWorkflow, "paths-ignore:") {
		t.Fatal("required native jobs must always report")
	}
	for _, contract := range []string{"--no-renames", "steps.scope.outputs.exempt", "agents/|docs/sprints/|", "fetch-depth: 0"} {
		if !strings.Contains(nativeWorkflow, contract) {
			t.Errorf("missing native scope contract: %s", contract)
		}
	}
	workflow := read(t, ".github/workflows/tests.yml")
	for _, command := range []string{"go vet ./...", "go test -race ./...", "CGO_ENABLED=0", "ubuntu-latest", "macos-latest"} {
		if !strings.Contains(workflow, command) {
			t.Errorf("missing Go CI coverage: %s", command)
		}
	}
	if strings.Contains(workflow, "paths-ignore:") || strings.Contains(workflow, "setup-python") || strings.Contains(workflow, "tests/run.py") {
		t.Error("Go contributor checks must run on every change without the retired suite")
	}
	native := read(t, "internal/nativetest/native_test.go")
	for _, contract := range []string{"git", "archive", "3d60c73c88f3110ca4bc91a9a0cc7e16f7daf827", "testdata", "TestUpgradeFromMain", "TestGoLifecycle"} {
		if !strings.Contains(native, contract) {
			t.Errorf("native independent baseline/fixture contract missing: %s", contract)
		}
	}
	for _, path := range []string{"agents/skills", "docs/sprints", ".claude", ".codex", ".agents"} {
		// Installation ships only the running binary, never repository agent-process files.
		if strings.Contains(read(t, "internal/install/install.go"), `"`+path+`"`) {
			t.Errorf("agent process path included in installation: %s", path)
		}
	}
}

func TestFreshInstallWithoutPython(t *testing.T) {
	goCLI, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "koinon")
	build := exec.Command(goCLI, "build", "-o", binary, "./cmd/koinon")
	build.Dir = root(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	prefix := filepath.Join(dir, "prefix")
	state := filepath.Join(dir, "state")
	cmd := exec.Command(binary, "install", "--no-start", "--prefix", prefix, "--state-dir", state)
	// No interpreter or service manager is discoverable, and every configuration path
	// belongs to this fixture. This checks the actual installation command.
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "HOME" && key != "PATH" && !strings.HasPrefix(key, "XDG_") && key != "CLAUDE_CONFIG_DIR" && key != "CODEX_HOME" {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, "HOME="+dir, "PATH="+filepath.Join(dir, "empty-path"), "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_STATE_HOME="+filepath.Join(dir, "states"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install with no Python: %v\n%s", err, out)
	}
	var report map[string]any
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatal(err)
	}
	if report["ok"] != true {
		t.Fatalf("install report: %s", out)
	}
	if _, err := os.Stat(filepath.Join(prefix, "bin/koinon")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(state, "state.sqlite3")); !os.IsNotExist(err) {
		t.Fatalf("staged install unexpectedly ran a daemon: %v", err)
	}
}

// TestNativeScope runs the actual workflow classifier, including a rename that would
// otherwise hide a deleted runtime path. Required jobs still run for exempt changes.
func TestNativeScope(t *testing.T) {
	workflow := read(t, ".github/workflows/native-go-lifecycle.yml")
	match := regexp.MustCompile(`(?s)run: \|\n(.*?)\n      - if:`).FindStringSubmatch(workflow)
	if len(match) != 2 {
		t.Fatal("no native scope script")
	}
	script := strings.ReplaceAll(match[1], "          ", "")
	for _, scenario := range []string{"skill", "skill-and-readme", "rename-runtime"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) string {
				command := exec.Command("git", append([]string{"-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
				command.Dir = dir
				out, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git("init", "-q")
			os.WriteFile(filepath.Join(dir, "runtime.go"), []byte("package runtime\n"), 0600)
			os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0600)
			git("add", "-A")
			git("commit", "-qm", "base")
			base := git("rev-parse", "HEAD")
			os.MkdirAll(filepath.Join(dir, "agents/skills/x"), 0700)
			os.WriteFile(filepath.Join(dir, "agents/skills/x/SKILL.md"), []byte("skill\n"), 0600)
			if scenario == "skill-and-readme" {
				os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0600)
			}
			if scenario == "rename-runtime" {
				git("mv", "runtime.go", "agents/runtime.go")
			}
			git("add", "-A")
			git("commit", "-qm", "change")
			output := filepath.Join(dir, "output")
			command := exec.Command("bash", "-c", script)
			command.Dir = dir
			command.Env = append(os.Environ(), "BASE="+base, "HEAD=HEAD", "GITHUB_OUTPUT="+output)
			if out, err := command.CombinedOutput(); err != nil {
				t.Fatalf("scope: %v %s", err, out)
			}
			out, _ := os.ReadFile(output)
			exempt := strings.Contains(string(out), "exempt=true")
			if exempt != (scenario == "skill") {
				t.Fatalf("%s exempt=%v", scenario, exempt)
			}
		})
	}
}

// TestRepositorySetup captures the actual settings payloads with a fake gh and no
// interpreter on PATH. It never contacts GitHub or changes the real checkout config.
func TestRepositorySetup(t *testing.T) {
	dir := t.TempDir()
	tools := filepath.Join(dir, "tools")
	os.Mkdir(tools, 0700)
	for _, name := range []string{"git", "cat"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
			t.Fatal(err)
		}
	}
	gh := `#!/bin/sh
case "$*" in
 *--input*)
  for arg do case "$arg" in repos/*/rulesets/*) endpoint=${arg##*/};; esac; done
  cat > "$RECORD_DIR/$endpoint.json" ;;
 *--jq*)
  case "$*" in *"develop branch policy"*) echo 1;; *) echo 2;; esac ;;
 *rulesets*) echo '[{"id":1,"name":"develop branch policy"},{"id":2,"name":"main branch policy"}]' ;;
esac
`
	os.WriteFile(filepath.Join(tools, "gh"), []byte(gh), 0700)
	init := exec.Command("git", "init", "-q", dir)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bash, filepath.Join(root(t), "scripts/setup-repo.sh"), "fixture/project")
	command.Dir = dir
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PATH=") {
			env = append(env, entry)
		}
	}
	command.Env = append(env, "PATH="+tools, "RECORD_DIR="+dir)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("setup: %v %s", err, out)
	}
	for id, branch := range map[string]string{"1": "develop", "2": "main"} {
		data, err := os.ReadFile(filepath.Join(dir, id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var policy struct {
			Name        string `json:"name"`
			Enforcement string `json:"enforcement"`
			Rules       []struct {
				Type       string `json:"type"`
				Parameters struct {
					Checks []struct {
						Context string `json:"context"`
					} `json:"required_status_checks"`
					Methods []string `json:"allowed_merge_methods"`
					Strict  bool     `json:"strict_required_status_checks_policy"`
				} `json:"parameters"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(data, &policy); err != nil {
			t.Fatal(err)
		}
		if policy.Name != branch+" branch policy" || policy.Enforcement != "active" {
			t.Fatalf("invalid policy: %s", data)
		}
		checks := map[string]bool{}
		rules := map[string]bool{}
		for _, rule := range policy.Rules {
			rules[rule.Type] = true
			for _, check := range rule.Parameters.Checks {
				checks[check.Context] = true
			}
			if rule.Type == "required_status_checks" && !rule.Parameters.Strict {
				t.Error("required checks are not strict")
			}
			if rule.Type == "pull_request" {
				method := "squash"
				if branch == "main" {
					method = "merge"
				}
				if len(rule.Parameters.Methods) != 1 || rule.Parameters.Methods[0] != method {
					t.Error("wrong merge policy")
				}
			}
		}
		for _, name := range []string{"go (ubuntu-latest)", "go (macos-latest)", "go-lifecycle (ubuntu-latest, systemd)", "go-lifecycle (macos-latest, launchd)"} {
			if !checks[name] {
				t.Errorf("missing required check %s", name)
			}
		}
		if len(checks) != 4 || !rules["non_fast_forward"] || !rules["deletion"] || !rules["pull_request"] {
			t.Fatalf("protection changed: %s", data)
		}
	}
}
