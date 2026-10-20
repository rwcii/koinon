package contributor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

func TestRepositoryFactsMatchCanonicalSchemasAndPolicy(t *testing.T) {
	catalog, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Items {
		if item.Kind != "skill" {
			continue
		}
		var required bool
		for _, field := range item.Facts.Fields {
			required = required || field.Required
		}
		b, err := os.ReadFile(filepath.Join(root(t), item.Facts.Filename))
		if os.IsNotExist(err) && !required {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		values, err := library.ParseFactValues(b, item.Name, item.Facts)
		if err != nil {
			t.Fatalf("%s: %v", item.Name, err)
		}
		for _, source := range values.Sources {
			info, err := os.Lstat(filepath.Join(root(t), source))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("%s evidence %s: %v", item.Name, source, err)
			}
		}
		if item.Name == "ship" || item.Name == "check" {
			if err := requiredGoCommands(values.Fields["Required checks"], read(t, "AGENTS.md")); err != nil {
				t.Errorf("%s: %v", item.Name, err)
			}
		}
	}
	// Facts keep the project's concrete gates while generic procedures no longer hardcode
	// its Go checks or branches. Authoritative policy remains in root instructions.
	for name, terms := range map[string][]string{
		"check":   {"origin/develop", raceTests, "go vet ./...", "gofmt -l .", "CGO_ENABLED=0", "bash -n scripts/setup-repo.sh", "sh -n .githooks/pre-commit", "Unreleased"},
		"ship":    {"isolated worktree", "git commit -S -s", "One open PR per target", "peer-review", "squash auto-merge", "never\nrewrite"},
		"release": {"develop to main", "release approval", "Dated changelog", "scratch prefix", "Homebrew"},
	} {
		facts := read(t, ".koinon/skills/"+name+".md")
		for _, term := range terms {
			if !strings.Contains(facts, term) {
				t.Errorf("%s facts lack %s", name, term)
			}
		}
		body := read(t, "agents/skills/"+name+"/SKILL.md")
		for _, specific := range []string{"origin/develop", "go test -race", "CGO_ENABLED=0"} {
			if strings.Contains(body, specific) {
				t.Errorf("portable %s embeds project fact %s", name, specific)
			}
		}
	}
	for _, term := range []string{"WDR 004", "ADR 001", "WDR 002", "WDR 005", "WDR 006", "Git-ignored"} {
		if !strings.Contains(read(t, "AGENTS.md"), term) {
			t.Errorf("authoritative policy lost %s", term)
		}
	}
}

// Compare the actual field, not a word elsewhere in the sidecar, with the commands
// declared by the project's authoritative instructions.
func requiredGoCommands(value, policy string) error {
	for _, line := range strings.Split(policy, "\n") {
		command := strings.TrimSpace(line)
		if strings.HasPrefix(command, "go vet ") || strings.HasPrefix(command, "go test ") {
			if !strings.Contains(value, command) {
				return fmt.Errorf("Required checks lacks authoritative command %q", command)
			}
		}
	}
	return nil
}

func TestRequiredChecksDetectSourceValueDrift(t *testing.T) {
	catalog, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	policy := read(t, "AGENTS.md")
	for _, item := range catalog.Items {
		if item.Name != "ship" && item.Name != "check" {
			continue
		}
		values, err := library.ParseFactValues([]byte(read(t, item.Facts.Filename)), item.Name, item.Facts)
		if err != nil {
			t.Fatal(err)
		}
		good := values.Fields["Required checks"]
		if err := requiredGoCommands(good, policy); err != nil {
			t.Fatal(err)
		}
		for _, change := range [][2]string{{raceTests, "go test ./..."}, {"go vet ./...", "go vet ./internal/..."}} {
			bad := strings.ReplaceAll(good, change[0], change[1])
			if bad == good || requiredGoCommands(bad, policy) == nil {
				t.Fatalf("%s failed to detect altered required command %q", item.Name, change[0])
			}
		}
	}
}

// Compile the actual whitelist in a temporary source tree after adding a canonical skill.
// This proves the embed pattern includes future skills and excludes adjacent private files.
func TestEmbedRebuildAfterNewCanonicalSkill(t *testing.T) {
	// An empty .git above the fixture, as a sandbox's mount point in a shared /tmp leaves it,
	// must not reach the build (#412).
	parent := t.TempDir()
	dir := filepath.Join(parent, "fixture")
	for _, d := range []string{filepath.Join(parent, ".git"), dir} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	assets := filepath.Join(dir, "assets")
	os.MkdirAll(filepath.Join(assets, "example"), 0700)
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.26.6\n", "assets/embed.go": read(t, "agents/skills/embed.go"), "assets/AGENTS.md": "authoring\n", "assets/example/SKILL.md": "synthetic canonical bytes\n", "assets/example/private.md": "fixture-private-notes\n", "assets/root-policy.md": "fixture-private-policy\n", "main.go": `package main
import("encoding/json";"fmt";"io/fs";skills "fixture/assets")
func main(){paths:=[]string{};fs.WalkDir(skills.Source,".",func(p string,e fs.DirEntry,err error)error{if err!=nil{return err};if !e.IsDir(){paths=append(paths,p)};return nil});b,_:=skills.Source.ReadFile("example/SKILL.md");out,_:=json.Marshal(struct{Paths []string;Content string}{paths,string(b)});fmt.Print(string(out))}
`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(dir, "fixture"), ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("embed rebuild: %v %s", err, out)
	}
	cmd := exec.Command(filepath.Join(dir, "fixture"))
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Paths   []string
		Content string
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if result.Content != "synthetic canonical bytes\n" || len(result.Paths) != 2 || bytes.Contains(out, []byte("private")) {
		t.Fatalf("whitelist differs: %s", out)
	}
}
