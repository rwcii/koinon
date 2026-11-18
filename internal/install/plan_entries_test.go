package install

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// entriesEnv is planEnv with OpenCode's, agy's and Grok's folders and DeepSeek's harness
// home, so the install writes every kind of entry; its plan is current.
func entriesEnv(t *testing.T) *env {
	t.Helper()
	e := planEnv(t, "systemd")
	for _, dir := range []string{e.o.Opencode, e.o.AgyRoot, e.o.GrokHome, e.o.DSHHome} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := installPickupSkills(e.o, &Report{}); err != nil {
		t.Fatal(err)
	}
	if steps := plan(t, e); len(steps) != 0 {
		t.Fatalf("current entries: %q", steps)
	}
	return e
}

// A missing or earlier entry gives the install step alone; a kept file gives none; the
// install then repairs it and the next plan is current (#741).
func TestPlanSkillEntries(t *testing.T) {
	claude := func(e *env, name string) string { return core.ClaudeSkillPath(e.o.ClaudeConfig, name) }
	codex := func(e *env, name string) string { return core.CodexSkillPath(e.o.Services.Home, name) }
	command := func(e *env, name string) string { return core.OpenCodeCommandPath(e.o.Opencode, name) }
	write := func(t *testing.T, path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(t *testing.T, target, path string) {
		t.Helper()
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name   string
		change func(t *testing.T, e *env)
		repair bool
	}{
		{"missing pickup entry", func(t *testing.T, e *env) { os.Remove(claude(e, core.PickupSkillName)) }, true},
		{"earlier skill entry", func(t *testing.T, e *env) { write(t, codex(e, "koinon-record"), "an earlier entry\n") }, true},
		{"missing skill entry in grok", func(t *testing.T, e *env) {
			os.RemoveAll(filepath.Join(e.o.GrokHome, "skills", "koinon-ship"))
		}, true},
		{"earlier deepseek pickup entry", func(t *testing.T, e *env) {
			write(t, core.DeepSeekPickupSkillPath(e.o.DSHHome), "an earlier entry\n")
		}, true},
		{"missing opencode command", func(t *testing.T, e *env) { os.Remove(command(e, "koinon-check")) }, true},
		{"earlier koinon command", func(t *testing.T, e *env) {
			// An intact Koinon file of another version, here another command's: the install
			// replaces it.
			commands, err := core.OpenCodeCommands()
			if err != nil {
				t.Fatal(err)
			}
			write(t, command(e, "koinon-resume"), string(commands[0].Content))
		}, true},
		{"edited command", func(t *testing.T, e *env) { write(t, command(e, "koinon-check"), "my own command\n") }, false},
		{"own guide at the shared name", func(t *testing.T, e *env) {
			write(t, claude(e, "koinon-guide"), "---\nname: koinon-guide\n---\nmine\n")
		}, false},
		{"linked entry file", func(t *testing.T, e *env) {
			mine := filepath.Join(t.TempDir(), "SKILL.md")
			write(t, mine, "mine\n")
			link(t, mine, codex(e, "koinon-sprint"))
		}, false},
		{"linked entry folder", func(t *testing.T, e *env) {
			link(t, t.TempDir(), filepath.Dir(claude(e, "koinon-pair")))
		}, false},
		{"linked commands folder", func(t *testing.T, e *env) {
			link(t, t.TempDir(), filepath.Dir(command(e, "koinon-check")))
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := entriesEnv(t)
			c.change(t, e)
			steps := plan(t, e)
			if !c.repair {
				if len(steps) != 0 {
					t.Fatalf("kept file gave %q", steps)
				}
				return
			}
			if want := []string{e.o.Source + " install"}; !reflect.DeepEqual(steps, want) {
				t.Fatalf("plan %q, want %q", steps, want)
			}
			// The install repairs it; the next plan is current.
			if err := installPickupSkills(e.o, &Report{}); err != nil {
				t.Fatal(err)
			}
			if steps := plan(t, e); len(steps) != 0 {
				t.Fatalf("after the install: %q", steps)
			}
		})
	}
}

// An entry that cannot be read is an unfinished check, never a current plan; a FIFO does not
// block it, and a missing entry before it does not hide it.
func TestPlanSkillEntryUnreadable(t *testing.T) {
	for name, create := range map[string]func(path string) error{
		"folder": func(path string) error { return os.Mkdir(path, 0o700) },
		"fifo":   func(path string) error { return syscall.Mkfifo(path, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			e := entriesEnv(t)
			// The first pickup entry is missing; a later skill entry cannot be read.
			os.Remove(core.ClaudeSkillPath(e.o.ClaudeConfig, core.PickupSkillName))
			path := core.CodexSkillPath(e.o.Services.Home, "koinon-standards-review")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := create(path); err != nil {
				t.Fatal(err)
			}
			steps, err := Plan(context.Background(), e.o)
			if err == nil || !strings.HasPrefix(err.Error(), "skill_entry_unreadable: ") || !strings.Contains(err.Error(), path) || steps != nil {
				t.Fatalf("%q %v", steps, err)
			}
		})
	}
}

// Without OpenCode's, agy's, Grok's and DeepSeek's folders the plan reads none of them, and
// the Claude Code and Codex roots are always read.
func TestPlanSkillEntriesAbsentRoots(t *testing.T) {
	e := planEnv(t, "systemd")
	for _, dir := range []string{e.o.Opencode, e.o.AgyRoot, e.o.GrokHome, e.o.DSHHome} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s exists: %v", dir, err)
		}
	}
	if steps := plan(t, e); len(steps) != 0 {
		t.Fatalf("absent roots gave %q", steps)
	}
	os.RemoveAll(filepath.Join(e.o.Services.Home, ".agents"))
	if steps, want := plan(t, e), []string{e.o.Source + " install"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("missing Codex root: %q, want %q", steps, want)
	}
}
