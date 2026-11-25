// Package scripts embeds the scripts that the catalog ships to adopting projects.
package scripts

import _ "embed"

// PreflightRunner is scripts/preflight-run.sh, the catalog item setup.preflight-runner (#814).
//
//go:embed preflight-run.sh
var PreflightRunner []byte
