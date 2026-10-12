// Package reference embeds only the published permission recommendations.
package reference

import "embed"

// Permissions is inert reference content; reading it grants no permission.
//
//go:embed PERMISSIONS.md
var Permissions embed.FS
