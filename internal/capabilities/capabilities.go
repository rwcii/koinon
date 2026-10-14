// Package capabilities describes the native families and operations in this runtime.
package capabilities

func Families() []string {
	return []string{"agy", "claude", "codex", "deepseek", "grok", "opencode"}
}

func Features() []string {
	return []string{"peer-messaging", "work-claims"}
}
