package core

// SupportedFamilies lists native caller families accepted by this runtime. DeepSeek's
// command path is supported even while its live MCP/wake proof remains deferred.
func SupportedFamilies() []string {
	return []string{"agy", "claude", "codex", "deepseek", "grok", "opencode"}
}

// SupportedFeatures describes implemented coordination operations for library metadata.
// Guidance requirements are informational; they never grant permissions. Add future
// features only when their runtime operations exist.
func SupportedFeatures() []string {
	return []string{"peer-messaging", "work-claims"}
}
