//go:build !livecheck

package core

import (
	"maps"
	"testing"
)

// An ordinary build admits only the evidenced platforms and versions; a livecheck build is the
// only one that admits more.
func TestClaudeEvidenceOfOrdinaryBuild(t *testing.T) {
	if !maps.Equal(claudeEvidencedPlatforms, map[string]bool{"linux": true, "darwin": true}) || claudeAnyVersion {
		t.Fatalf("platforms %v, any version %v", claudeEvidencedPlatforms, claudeAnyVersion)
	}
	if !maps.Equal(codexEvidencedPlatforms, map[string]bool{"linux": true, "darwin": true}) || codexAnyVersion {
		t.Fatalf("Codex platforms %v, any version %v", codexEvidencedPlatforms, codexAnyVersion)
	}
}
