package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, root, path, text string) {
	t.Helper()
	file := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func skill(t *testing.T, root, name, desc string) {
	t.Helper()
	quoted, _ := json.Marshal(desc)
	put(t, root, "agents/skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: "+string(quoted)+"\n---\nSynthetic instructions that must not be copied.\n")
}
func get(t *testing.T, root, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func run(t *testing.T, root string, check bool) {
	t.Helper()
	if err := generate(root, check); err != nil {
		t.Fatal(err)
	}
}
func drift(t *testing.T, root string) {
	t.Helper()
	if err := generate(root, true); err == nil {
		t.Fatal("check accepted drift")
	}
}
func TestGenerateLifecycle(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "alpha", "Initial description")
	drift(t, root)
	run(t, root, false)
	run(t, root, true)
	first := get(t, root, ".opencode/commands/alpha.md")
	if strings.Count(first, "$ARGUMENTS") != 1 || !strings.Contains(first, "Load the `alpha` skill") || strings.Contains(first, "Synthetic instructions") || strings.Contains(first, "\r") || !strings.HasSuffix(first, "\n") {
		t.Fatal(first)
	}
	info, _ := os.Stat(filepath.Join(root, ".opencode/commands/alpha.md"))
	if info.Mode().Perm() != 0644 {
		t.Fatal(info.Mode())
	}
	run(t, root, false)
	if get(t, root, ".opencode/commands/alpha.md") != first {
		t.Fatal("repeat generation changed bytes")
	}
	// Changed metadata and a new skill are both detected.
	skill(t, root, "alpha", "New description")
	skill(t, root, "beta", "Other")
	drift(t, root)
	run(t, root, false)
	run(t, root, true)
	if !strings.Contains(get(t, root, ".opencode/commands/alpha.md"), "New description") {
		t.Fatal("stale description")
	}
	// Rename beta to gamma, retiring beta; custom commands and native files survive.
	put(t, root, ".opencode/commands/custom.md", "Custom content\n")
	put(t, root, ".opencode/package.json", "{\"private\":true}\n")
	if err := os.RemoveAll(filepath.Join(root, "agents/skills/beta")); err != nil {
		t.Fatal(err)
	}
	skill(t, root, "gamma", "Other")
	drift(t, root)
	run(t, root, false)
	run(t, root, true)
	if _, err := os.Stat(filepath.Join(root, ".opencode/commands/beta.md")); !os.IsNotExist(err) {
		t.Fatal("retired wrapper remains", err)
	}
	if get(t, root, ".opencode/commands/custom.md") != "Custom content\n" || get(t, root, ".opencode/package.json") != "{\"private\":true}\n" {
		t.Fatal("custom artifact changed")
	}
	// A generated wrapper can be repaired, but --check never writes it.
	put(t, root, ".opencode/commands/alpha.md", first+"Edited\n")
	drift(t, root)
	if get(t, root, ".opencode/commands/alpha.md") != first+"Edited\n" {
		t.Fatal("check wrote a file")
	}
	run(t, root, false)
	run(t, root, true)
	if err := os.Chmod(filepath.Join(root, ".opencode/commands/alpha.md"), 0755); err != nil {
		t.Fatal(err)
	}
	drift(t, root)
	run(t, root, false)
	run(t, root, true)
}
func TestDescriptionEscaping(t *testing.T) {
	for _, desc := range []string{"Colon: value, \"quotes\", 'single', `backticks` - dash \\ path", strings.Repeat("Long description ", 100), "Unicode naïve 日本語"} {
		root := t.TempDir()
		skill(t, root, "alpha", desc)
		run(t, root, false)
		text := get(t, root, ".opencode/commands/alpha.md")
		raw := strings.Split(text, "\n")[1]
		var got string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "description: ")), &got); err != nil || got != desc {
			t.Fatalf("description roundtrip: %q %v", got, err)
		}
	}
	// Both quoted input forms are strings; the output always quotes them safely.
	name, desc, err := metadata([]byte("---\nname: alpha\ndescription: 'naïve: it''s `fine`'\n---\n"), "alpha")
	if err != nil || name != "alpha" || desc != "naïve: it's `fine`" {
		t.Fatalf("single quote parsing: %s %s %v", name, desc, err)
	}
}
func TestLegacyMigration(t *testing.T) {
	root := t.TempDir()
	for _, name := range legacyNames {
		skill(t, root, name, "Canonical "+name)
		put(t, root, ".opencode/commands/"+name+".md", "---\ndescription: Original wrapper description\n---\n"+template(name))
	}
	run(t, root, false)
	run(t, root, true)
	for _, name := range legacyNames {
		if !strings.Contains(get(t, root, ".opencode/commands/"+name+".md"), marker) {
			t.Fatal(name)
		}
	}
	// A similar custom wrapper outside the known legacy set is never adopted.
	skill(t, root, "extra", "Extra")
	put(t, root, ".opencode/commands/extra.md", "---\ndescription: Custom\n---\n"+template("extra"))
	if err := generate(root, false); err == nil {
		t.Fatal("adopted unknown legacy command")
	}
	if strings.Contains(get(t, root, ".opencode/commands/extra.md"), marker) {
		t.Fatal("changed custom command")
	}
}
func TestRefusalsPreserveFiles(t *testing.T) {
	cases := map[string]string{
		"directory mismatch": "---\nname: other\ndescription: Example\n---\n",
		"unsafe name":        "---\nname: ../alpha\ndescription: Example\n---\n",
		"missing close":      "---\nname: alpha\ndescription: Example\n",
		"unknown key":        "---\nname: alpha\ndescription: Example\nagent: build\n---\n",
		"duplicate":          "---\nname: alpha\nname: alpha\ndescription: Example\n---\n",
		"blank":              "---\nname: alpha\ndescription: \"\"\n---\n",
		"object":             "---\nname: alpha\ndescription: {text: value}\n---\n",
		"boolean":            "---\nname: alpha\ndescription: true\n---\n",
		"unquoted colon":     "---\nname: alpha\ndescription: Reason: value\n---\n",
		"unterminated quote": "---\nname: alpha\ndescription: \"bad\n---\n",
		"block":              "---\nname: alpha\ndescription: |\n  Example\n---\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			skill(t, root, "safe", "Safe")
			put(t, root, "agents/skills/alpha/SKILL.md", data)
			put(t, root, ".opencode/commands/custom.md", "Preserve\n")
			if err := generate(root, false); err == nil {
				t.Fatal("accepted invalid metadata")
			}
			if get(t, root, ".opencode/commands/custom.md") != "Preserve\n" {
				t.Fatal("changed custom")
			}
			if _, err := os.Stat(filepath.Join(root, ".opencode/commands/safe.md")); !os.IsNotExist(err) {
				t.Fatal("wrote before preflight completed")
			}
		})
	}
	for _, name := range reserved {
		root := t.TempDir()
		skill(t, root, name, "Reserved")
		if err := generate(root, false); err == nil {
			t.Fatal("accepted reserved name", name)
		}
	}
	root := t.TempDir()
	skill(t, root, "alpha", "Safe")
	put(t, root, ".opencode/commands/alpha.md", "Unknown content\n")
	if err := generate(root, false); err == nil {
		t.Fatal("overwrote unknown ownership")
	}
	if get(t, root, ".opencode/commands/alpha.md") != "Unknown content\n" {
		t.Fatal("changed collision")
	}
}
func TestSymlinksPreserved(t *testing.T) {
	for _, path := range []string{".opencode", ".opencode/commands", ".opencode/commands/alpha.md", "agents/skills/alpha/SKILL.md"} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			skill(t, root, "alpha", "Safe")
			outside := t.TempDir()
			target := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			if err := generate(root, false); err == nil {
				t.Fatal("followed managed symlink")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("changed external target", err)
			}
		})
	}
}

func TestCaseCollisionPreservesCustomCommand(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "alpha", "Safe")
	put(t, root, ".opencode/commands/Alpha.MD", "Custom command\n")
	if err := generate(root, false); err == nil {
		t.Fatal("accepted case collision")
	}
	if get(t, root, ".opencode/commands/Alpha.MD") != "Custom command\n" {
		t.Fatal("changed custom command")
	}
}

func TestCheckAcceptsCheckoutUmask(t *testing.T) {
	root := t.TempDir()
	skill(t, root, "alpha", "Safe")
	run(t, root, false)
	file := filepath.Join(root, ".opencode/commands/alpha.md")
	if err := os.Chmod(file, 0664); err != nil {
		t.Fatal(err)
	}
	before := get(t, root, ".opencode/commands/alpha.md")
	run(t, root, true)
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0664 || get(t, root, ".opencode/commands/alpha.md") != before {
		t.Fatal("check changed checkout permissions", err)
	}
	run(t, root, false)
	info, err = os.Stat(file)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("generation did not emit 0644", err)
	}
}
