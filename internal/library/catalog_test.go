package library

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	skills "github.com/rwcii/koinon/agents/skills"
	reference "github.com/rwcii/koinon/docs"
	"github.com/rwcii/koinon/internal/capabilities"
	"github.com/rwcii/koinon/internal/skillmeta"
)

func sources(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	result := fstest.MapFS{}
	err := fs.WalkDir(skills.Source, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			b, err := fs.ReadFile(skills.Source, p)
			if err != nil {
				return err
			}
			result[p] = &fstest.MapFile{Data: b}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(reference.Permissions, "PERMISSIONS.md")
	if err != nil {
		t.Fatal(err)
	}
	return result, fstest.MapFS{"PERMISSIONS.md": {Data: b}}
}
func synthetic(name string) []byte {
	return []byte("---\nname: " + name + "\ndescription: synthetic skill\n---\n\n# Fixture\n\n<!-- koinon:repository-facts -->\n```json\n{\"version\":1,\"filename\":\".koinon/skills/" + name + ".md\",\"fields\":[]}\n```\n")
}
func TestEmbeddedExactSourceAndDescriptors(t *testing.T) {
	catalog, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	canonicalPaths, _ := fs.Glob(skills.Source, "*/SKILL.md")
	if len(canonicalPaths) < 13 || len(catalog.Items) != len(canonicalPaths)+5 || len(catalog.Digest) != 64 {
		t.Fatalf("incomplete catalog: %d/%s", len(catalog.Items), catalog.Digest)
	}
	seen := map[string]bool{}
	var commands []Command
	for _, item := range catalog.Items {
		if seen[item.ID] || item.RequiringRelease != "development" || item.ContentDigest != digest([]byte(item.Content)) {
			t.Fatalf("invalid metadata: %s", item.ID)
		}
		seen[item.ID] = true
		if item.Kind == "skill" {
			b, err := fs.ReadFile(skills.Source, item.Name+"/SKILL.md")
			if err != nil || !bytes.Equal(b, []byte(item.Content)) {
				t.Fatalf("source differs: %s", item.ID)
			}
			if len(item.Destinations) != 2*len(capabilities.Families())+3 {
				t.Fatalf("family coverage: %s", item.ID)
			}
			for _, target := range item.Destinations {
				if !strings.HasSuffix(target.Path, item.Name+"/SKILL.md") && target.Path != "" {
					t.Fatal(target)
				}
				if target.Scope == "machine" && target.Family != "codex" && target.Discovery == "verified" {
					t.Fatal("invented native proof")
				}
			}
			if item.Name == "handoff" || item.Name == "pickup" {
				if len(item.FeatureRequirements) != 0 {
					t.Fatal("advertised unimplemented context management")
				}
			}
		}
		if item.ID == "setup.opencode-commands" {
			var payload struct{ Commands []Command }
			if err := json.Unmarshal([]byte(item.Content), &payload); err != nil {
				t.Fatal(err)
			}
			commands = payload.Commands
		}
		if item.ID == "setup.permissions" {
			b, _ := fs.ReadFile(reference.Permissions, "PERMISSIONS.md")
			if !bytes.Equal(b, []byte(item.Content)) {
				t.Fatal("copied permissions source")
			}
		}
	}
	if len(commands) != len(canonicalPaths) {
		t.Fatal("incomplete wrappers")
	}
	for _, command := range commands {
		name := strings.TrimSuffix(strings.TrimPrefix(command.Path, ".opencode/commands/"), ".md")
		b, _ := fs.ReadFile(skills.Source, name+"/SKILL.md")
		_, desc, err := skillmeta.Metadata(b, name)
		if err != nil || command.Content != string(skillmeta.CommandWrapper(name, desc)) {
			t.Fatal("wrapper differs")
		}
	}
	again, err := Embedded("dev")
	if err != nil || catalog.Digest != again.Digest {
		t.Fatal("nondeterministic catalog")
	}
	release, err := Embedded("v1.2.3")
	if err != nil || release.Digest == catalog.Digest {
		t.Fatal("release metadata not fingerprinted")
	}
}
func TestSourceCompletenessBoundsAndPrivacy(t *testing.T) {
	source, perms := sources(t)
	baseline, err := Build(source, perms, "dev")
	if err != nil {
		t.Fatal(err)
	}
	source["new-example/SKILL.md"] = &fstest.MapFile{Data: synthetic("new-example")}
	// Files outside the embedding whitelist cannot be promoted into the catalog.
	source["ROOT-POLICY.md"] = &fstest.MapFile{Data: []byte("private-fixture-root-authority")}
	source["new-example/notes.md"] = &fstest.MapFile{Data: []byte("private-fixture-notes")}
	updated, err := Build(source, perms, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != len(baseline.Items)+1 || updated.Digest == baseline.Digest {
		t.Fatal("new skill omitted from catalog/fingerprint")
	}
	bytes, _ := json.Marshal(updated)
	if strings.Contains(string(bytes), "private-fixture-") {
		t.Fatal("noncanonical source leaked")
	}
	source["new-example/SKILL.md"].Data = []byte("malformed")
	if _, err := Build(source, perms, "dev"); err == nil {
		t.Fatal("invalid future skill silently omitted")
	}
	source["new-example/SKILL.md"].Data = append(synthetic("new-example"), make([]byte, MaxItemBytes)...)
	if _, err := Build(source, perms, "dev"); err == nil {
		t.Fatal("oversized item accepted")
	}
	source, perms = sources(t)
	for i := 0; i < 20; i++ {
		name := string(rune('a'+i)) + "-fixture"
		source[name+"/SKILL.md"] = &fstest.MapFile{Data: synthetic(name)}
	}
	if _, err := Build(source, perms, "dev"); err == nil {
		t.Fatal("item bound ignored")
	}
}
func TestSchemaAndFactValues(t *testing.T) {
	b, _ := fs.ReadFile(skills.Source, "check/SKILL.md")
	schema, err := ParseFacts(b, "check")
	if err != nil {
		t.Fatal(err)
	}
	prefix := "# Facts\n## Version\n1\n## Skill\ncheck\n## Sources\n- AGENTS.md\n"
	good := prefix + "## Base branch\norigin/example\n## Required checks\nfixture-check\n"
	values, err := ParseFactValues([]byte(good), "check", schema)
	if err != nil || values.Fields["Conditional checks"] != "none" {
		t.Fatalf("defaults: %v %v", values, err)
	}
	for _, bad := range []string{prefix, good + "## Unknown\nx\n", good + "## Base branch\nother\n", strings.Replace(good, "AGENTS.md", "../private.md", 1), strings.Replace(good, "fixture-check", "bad\rvalue", 1), strings.Replace(good, "check\n## Sources", "wrong\n## Sources", 1)} {
		if _, err := ParseFactValues([]byte(bad), "check", schema); err == nil {
			t.Fatalf("invalid facts accepted: %s", bad)
		}
	}
	for _, text := range []string{
		strings.ReplaceAll(good, "\n", "\r\n"),
		strings.Replace(good, "- AGENTS.md\n", "- AGENTS.md\n\n- README.md\n", 1),
		strings.Replace(good, "fixture-check", "Print the literal ${VALUE} and $(command) examples.", 1),
	} {
		parsed, err := ParseFactValues([]byte(text), "check", schema)
		if err != nil || len(parsed.Sources) == 0 || parsed.Fields["Required checks"] == "" {
			t.Fatalf("inert fact text rejected: %v", err)
		}
		if strings.Contains(text, "${VALUE}") && parsed.Fields["Required checks"] != "Print the literal ${VALUE} and $(command) examples." {
			t.Fatalf("literal text changed: %q", parsed.Fields["Required checks"])
		}
	}
	for _, bad := range [][]byte{bytes.Replace(b, []byte("\"required\": true"), []byte("\"required\": true, \"default\": \"invented\""), 1), bytes.Replace(b, []byte("\"version\": 1"), []byte("\"version\": 2"), 1), append(b, []byte("extra")...)} {
		if _, err := ParseFacts(bad, "check"); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}
