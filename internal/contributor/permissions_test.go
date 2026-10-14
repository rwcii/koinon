package contributor

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

// Use the daemon's admitted-family source, including command-only families, rather
// than a second hard-coded list that could silently omit a future family.
func TestAdmittedFamiliesHavePermissionGuidance(t *testing.T) {
	for _, family := range core.SupportedFamilies() {
		t.Run(family, func(t *testing.T) {
			var out bytes.Buffer
			if err := setup.Guide(family, &out); err != nil {
				t.Fatalf("admitted family has no guide: %v", err)
			}
			for _, required := range []string{"### Recommended permissions", "docs/PERMISSIONS.md", "authorized upgrade"} {
				if !strings.Contains(out.String(), required) {
					t.Errorf("admitted family %s guide omits %s", family, required)
				}
			}
			if strings.Count(out.String(), "### Recommended permissions") != 1 {
				t.Fatal("guide should contain only its own family's recommendations")
			}
		})
	}
}
