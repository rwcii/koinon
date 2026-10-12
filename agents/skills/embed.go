// Package skills provides the canonical skill files embedded in the installed binary.
// The whitelist excludes repository policy, sidecar values, plans and private handoffs.
package skills

import "embed"

// Source is shared with the catalog builder. Skill bodies are never copied to another tree.
//
//go:embed */SKILL.md AGENTS.md
var Source embed.FS
