package core

import (
	"maps"
	"testing"
)

// Both cycle adapters run on Linux and macOS, with any native CLI version (PDR 006, Decision 5).
func TestCycleAdapterPlatforms(t *testing.T) {
	for name, platforms := range map[string]map[string]bool{"claude": claudeEvidencedPlatforms, "codex": codexEvidencedPlatforms} {
		if !maps.Equal(platforms, map[string]bool{"linux": true, "darwin": true}) {
			t.Fatalf("%s platforms %v", name, platforms)
		}
	}
}
