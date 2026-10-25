package contributor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
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
	for _, want := range []string{"Call the koinon library_read tool for item skill.pickup and follow the returned procedure."} {
		if !strings.Contains(string(core.PickupSkill()), want) {
			t.Errorf("koinon-pickup lacks %q", want)
		}
	}
}
