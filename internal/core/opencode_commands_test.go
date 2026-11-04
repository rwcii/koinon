package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Each koinon-NAME skill entry gets one OpenCode command that loads the entry and lists the
// entry's description; koinon-guide comes once. koinon-pickup, which the managed cycle runs as
// the skill command, gets no command of its name; koinon-resume, which no skill has, loads it
// (#603).
func TestOpenCodeCommands(t *testing.T) {
	commands, err := OpenCodeCommands()
	if err != nil {
		t.Fatal(err)
	}
	skills, err := TeamSkills()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	descriptions := map[string]string{PickupSkillName: pickupSkillDescription}
	for _, skill := range skills {
		want = append(want, skill.Name)
		descriptions[skill.Name] = skill.Description
	}
	want = append(want, OpenCodeResumeCommand)
	var names []string
	for _, command := range commands {
		names = append(names, command.Name)
		entry := command.Name
		if command.Name == OpenCodeResumeCommand {
			entry = PickupSkillName
		}
		if command.Entry != entry {
			t.Fatalf("%s loads %s, want %s", command.Name, command.Entry, entry)
		}
		content := string(command.Content)
		for _, part := range []string{
			"---\ndescription: " + strconv.Quote(descriptions[entry]) + "\n---\n\n",
			"\nLoad the `" + entry + "` skill with the skill tool and follow it exactly. $ARGUMENTS\n",
		} {
			if descriptions[entry] == "" || !strings.Contains(content, part) {
				t.Fatalf("%s lacks %q:\n%s", command.Name, part, content)
			}
		}
		if !openCodeCommandIntact(command.Content) {
			t.Fatalf("%s is not intact:\n%s", command.Name, content)
		}
	}
	if len(names) != 14 || slices.Contains(names, PickupSkillName) || strings.Join(names, " ") != strings.Join(want, " ") || strings.Count(strings.Join(names, " "), "koinon-guide") != 1 {
		t.Fatalf("commands %q, want %q", names, want)
	}
}

// A file is Koinon's while the rest of it matches the digest in its one marker line, whatever
// version wrote it; an edit, a second marker or no marker makes it the person's (#603).
func TestOpenCodeCommandIntact(t *testing.T) {
	current := openCodeCommandContent("koinon-check", "Run the checks.")
	older := olderOpenCodeCommand("koinon-check")
	edited := bytes.Replace(current, []byte("exactly."), []byte("exactly, then stop."), 1)
	for _, c := range []struct {
		name string
		data []byte
		want bool
	}{
		{"current", current, true},
		{"older", older, true},
		{"edited", edited, false},
		{"no marker", []byte("---\ndescription: mine\n---\n\nDo my thing.\n"), false},
		{"two markers", append(append([]byte{}, current...), current...), false},
		{"marker mid-line", bytes.Replace(current, []byte("\n"+openCodeCommandMark), []byte(" "+openCodeCommandMark), 1), false},
		{"empty", nil, false},
	} {
		if got := openCodeCommandIntact(c.data); got != c.want {
			t.Errorf("%s: intact %v, want %v", c.name, got, c.want)
		}
	}
}

// olderOpenCodeCommand is a command file that an earlier Koinon version wrote: another body
// with its own valid marker.
func olderOpenCodeCommand(name string) []byte {
	head, tail := "---\ndescription: \"an older description\"\n---\n\n", "Use the "+name+" skill. $ARGUMENTS\n"
	sum := sha256.Sum256([]byte(head + tail))
	return []byte(head + openCodeCommandMark + hex.EncodeToString(sum[:]) + " -->\n" + tail)
}

// The install writes a missing command and its commands directory, leaves an equal one,
// replaces an older Koinon file, and keeps a custom file, an edited file and a linked file or
// commands directory with their targets; the uninstall removes only Koinon files of any
// version, and the commands directory only when it is empty (#603).
func TestInstallAndRemoveOpenCodeCommand(t *testing.T) {
	root := t.TempDir()
	command := OpenCodeCommand{Name: "koinon-check", Content: openCodeCommandContent("koinon-check", "Run the checks.")}
	path := OpenCodeCommandPath(root, command.Name)
	if path != filepath.Join(root, "commands", "koinon-check.md") {
		t.Fatalf("path %s", path)
	}
	step := func(change func(string) (string, error), want string) {
		t.Helper()
		if got, err := change(path); err != nil || got != want {
			t.Fatalf("result %q %v, want %q", got, err, want)
		}
	}
	install := func(p string) (string, error) { return InstallOpenCodeCommand(p, command) }
	content := func(want []byte) {
		t.Helper()
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
			t.Fatalf("file %q %v, want %q", data, err, want)
		}
	}
	step(install, "created")
	content(command.Content)
	step(install, "unchanged")
	os.WriteFile(path, olderOpenCodeCommand(command.Name), 0o644)
	step(install, "replaced")
	content(command.Content)

	for _, mine := range [][]byte{
		[]byte("---\ndescription: mine\n---\n\nMy own check.\n"),
		bytes.Replace(command.Content, []byte("exactly."), []byte("exactly, twice."), 1),
	} {
		os.WriteFile(path, mine, 0o644)
		step(install, "kept")
		step(RemoveOpenCodeCommand, "kept")
		content(mine)
	}

	// A linked file and a linked commands directory, and what they point at, stay.
	target := filepath.Join(t.TempDir(), "mine.md")
	os.WriteFile(target, olderOpenCodeCommand(command.Name), 0o644)
	os.Remove(path)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	step(install, "kept")
	step(RemoveOpenCodeCommand, "kept")
	os.Remove(path)
	elsewhere := t.TempDir()
	os.WriteFile(filepath.Join(elsewhere, "koinon-check.md"), olderOpenCodeCommand(command.Name), 0o644)
	os.Remove(filepath.Join(root, "commands"))
	if err := os.Symlink(elsewhere, filepath.Join(root, "commands")); err != nil {
		t.Fatal(err)
	}
	step(install, "kept")
	step(RemoveOpenCodeCommand, "kept")
	for _, p := range []string{target, filepath.Join(elsewhere, "koinon-check.md")} {
		if data, _ := os.ReadFile(p); !bytes.Equal(data, olderOpenCodeCommand(command.Name)) {
			t.Fatalf("link target %s changed: %q", p, data)
		}
	}
	os.Remove(filepath.Join(root, "commands"))

	// The uninstall removes an older version too; another file keeps the directory.
	step(install, "created")
	other := filepath.Join(root, "commands", "mine.md")
	os.WriteFile(other, []byte("mine\n"), 0o644)
	os.WriteFile(path, olderOpenCodeCommand(command.Name), 0o644)
	step(RemoveOpenCodeCommand, "removed")
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other command: %v", err)
	}
	step(RemoveOpenCodeCommand, "absent")
	os.Remove(other)
	step(install, "created")
	step(RemoveOpenCodeCommand, "removed")
	if _, err := os.Lstat(filepath.Join(root, "commands")); !os.IsNotExist(err) {
		t.Fatalf("empty commands directory left: %v", err)
	}
}

// The managed cycle's probe still finds koinon-pickup as the skill command when OpenCode lists
// Koinon's command files: a command file of a skill's name takes the skill's place there, as
// OpenCode 1.18.35 does, so none of them may be named koinon-pickup (#603).
func TestOpenCodeCommandsKeepPickupProbe(t *testing.T) {
	f := newFakeOpenCode(t)
	commands, err := OpenCodeCommands()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		f.commands = slices.DeleteFunc(f.commands, func(c openCodeCommand) bool { return c.Name == command.Name })
		f.commands = append(f.commands, openCodeCommand{Name: command.Name, Source: "command", Template: string(command.Content)})
	}
	if p := (openCodeCycle{}).Probe(context.Background(), f.target(cycleReady)); p.Reason != "" || p.Entry != PickupSkillName {
		t.Fatalf("probe with the command files: %+v", p)
	}
}
