//go:build livecheck

package core

// A livecheck build is a private test build for a native evidence run of the Claude adapter
// on a platform or Claude Code version without evidence yet
// (docs/sprints/2026-10-12-context-handoff/macos-live-check.md). It admits every platform and
// every version; a released build never has this tag.
func init() {
	claudeEvidencedPlatforms = map[string]bool{"linux": true, "darwin": true}
	claudeAnyVersion = true
}
