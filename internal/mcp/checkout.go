package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/rwcii/koinon/internal/core"
)

func init() {
	toolList = append(toolList, map[string]any{"name": "work_checkout",
		"description": "Read this checkout's advisory writer role: canonical resource, holder, work ID, generation token, expiry and checkpoint. Defaults to the MCP working directory. Pass the resource to work_start for explicit acceptance. Reading claims nothing; an expired role grants no takeover.",
		"inputSchema": object(map[string]any{"directory": text})}, map[string]any{"name": "work_checkout_request",
		"description": "Notify the currently observed checkout writer that this session requests the role, with an optional note. Uses its exact native peer; changes no ownership. The writer can work_release with handoff_to and checkout_resource to notify this requester to explicitly pick up the role.",
		"inputSchema": object(map[string]any{"directory": text, "note": text})})
}

func (s *server) checkoutCall(ctx context.Context, name string, args map[string]json.RawMessage, body map[string]any) (string, string) {
	var a struct {
		Directory *string `json:"directory"`
		Note      *string `json:"note"`
	}
	if decodeArgs(args, &a) != nil {
		return "", "invalid_arguments"
	}
	if _, present := args["directory"]; present && (a.Directory == nil || *a.Directory == "") {
		return "", "invalid_arguments"
	}
	if _, present := args["note"]; present && (name != "work_checkout_request" || a.Note == nil) {
		return "", "invalid_arguments"
	}
	base, err := core.CheckoutResource(ctx, s.c.Directory)
	if err != nil {
		return "", "repo_unresolved"
	}
	checkout := base
	if a.Directory != nil {
		path := *a.Directory
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.c.Directory, path)
		}
		checkout, err = core.CheckoutResource(ctx, path)
		if err != nil || checkout.Repository != base.Repository {
			return "", "repo_unresolved"
		}
	}
	body["directory"] = checkout.Directory
	if name == "work_checkout_request" {
		if a.Note != nil {
			body["note"] = *a.Note
		}
		return "/v1/work/checkout-request", ""
	}
	return "/v1/work/checkout-status", ""
}
