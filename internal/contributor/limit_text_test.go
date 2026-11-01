package contributor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

// The text that agents read says what to do and carries no limit or caveat on their work
// (#487). Each source file below holds such text: the guide, the MCP instructions and tool
// descriptions, the daemon's messages and the skill entries it writes, the catalog block for
// a project's AGENTS.md and the setup notes.
func TestAgentTextHasNoLimitPhrases(t *testing.T) {
	for _, name := range []string{"internal/setup/guide.go", "internal/setup/setup.go", "internal/setup/codex_hooks.go",
		"internal/mcp/server.go", "internal/mcp/tools.go", "internal/mcp/decision_tools.go", "internal/mcp/work_tools.go",
		"internal/mcp/checkout.go", "internal/core/context_cycle.go", "internal/core/checkout_role.go",
		"internal/core/cycle_pickup.go", "internal/core/team_skills.go", "internal/library/catalog.go"} {
		data, err := os.ReadFile(filepath.Join(root(t), name))
		if err != nil {
			t.Fatal(err)
		}
		for _, phrase := range core.RemovedLimitPhrases {
			if strings.Contains(string(data), phrase) {
				t.Errorf("%s has %q", name, phrase)
			}
		}
	}
	// The pickup entries check the cycle's catalog digest and stop when they cannot read the
	// procedure (DECISIONS.md#RESTORE-STEPS-487).
	for _, want := range []string{"Call the koinon library_read tool for item skill.pickup. When context_cycle_status shows a cycle,\n" +
		"the catalog_digest that library_read returns must equal the cycle's catalog_digest; if it differs,\n" +
		"or the tool is unavailable, stop and report that. Otherwise follow the returned procedure."} {
		if !strings.Contains(string(core.PickupSkill()), want) {
			t.Errorf("koinon-pickup lacks %q", want)
		}
	}
	for _, want := range []string{"Run koinon library read skill.pickup --as deepseek:$DSH_SESSION_ID. When koinon cycle-status\n" +
		"--as deepseek:$DSH_SESSION_ID shows a cycle, the catalog_digest that library read returns must\n" +
		"equal the cycle's catalog_digest; if it differs, or the command fails, stop and report that.\n" +
		"Otherwise follow the returned procedure."} {
		if !strings.Contains(string(core.DeepSeekPickupSkill()), want) {
			t.Errorf("DeepSeek koinon-pickup lacks %q", want)
		}
	}
}

// The shared skills, their authoring file and index, the project's fact files and the OpenCode
// wrappers say what to do (#487 PR B). The rules that the maintainer gave in words stay as
// steps: the handoff location, one open PR, auto-merge, the commit identity and the
// refusal-review gate.
func TestSkillTextHasNoLimitPhrases(t *testing.T) {
	for _, dir := range []string{"agents/skills", ".koinon/skills", ".opencode/commands"} {
		err := filepath.WalkDir(filepath.Join(root(t), dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, phrase := range core.RemovedLimitPhrases {
				if strings.Contains(string(data), phrase) {
					t.Errorf("%s has %q", path, phrase)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := library.Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	steps := map[string][]string{
		"check":        {"When a required check\nfails, fix the cause and run the check again."},
		"handoff":      {"`_handoff/` directory of the main checkout", "When the output is `not ready`, stop: tell the maintainer which check failed, with the output,\nand write no handoff.", "Report the path of the new file."},
		"koinon-guide": {"report the command and its output to the maintainer."},
		"pair": {"The driver edits,\nstages and commits.", "takes a material disagreement to the maintainer.",
			"When the increment is committed, tested and has no accepted blocker, hand over the exact hash",
			"Do not swap during an unfinished fix or check."},
		"peer-review":    {"Report each failure that a test or a command reproduces", "Read back the status and the\nevidence link."},
		"peer-tmux":      {"An authorized cycle runs\nthrough all its steps", "ask the\nmaintainer which pane to use.", "ask the peer to save it again"},
		"pickup":         {"Then start the next action.", "ask the maintainer what to resume."},
		"recover-team":   {"earlier approval counts."},
		"refusal-review": {"The reviewer records its own gate.", "Report a finding for each constraint with no source"},
		"release":        {"Verify the exact head and merge it under the declared promotion\npolicy."},
		"ship":           {"Follow\nthe project's PR limit", "with auto-merge when the policy uses it.", "with no automated attribution."},
		"spin-down":      {"Stop each named, verified session through a\nsupported interface."},
		"spin-up":        {"Launch through the supported interfaces."},
		"sprint":         {"Build the chunks with ship and the project's Build checks, in dependency order."},
	}
	var skills int
	for _, item := range catalog.Items {
		if item.Kind != "skill" {
			continue
		}
		skills++
		want, ok := steps[item.Name]
		if !ok {
			t.Errorf("%s: no expected steps", item.Name)
		}
		want = append(want, "Invoking this skill or its task authorizes its steps\n(WDR 002, Decision 1).")
		for _, step := range want {
			if !strings.Contains(item.Content, step) {
				t.Errorf("%s lacks %q", item.Name, step)
			}
		}
	}
	if skills != len(steps) {
		t.Errorf("catalog has %d skills, want %d", skills, len(steps))
	}
}
