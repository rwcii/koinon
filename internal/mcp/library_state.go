package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
	"github.com/rwcii/koinon/internal/libraryclient"
)

func (s *server) libraryClient(caller core.Key) libraryclient.Client {
	return libraryclient.Client{Caller: caller, Getenv: s.c.Getenv, Inspector: library.Inspector{OpenRoot: s.c.LibraryReadRoot},
		Call: func(ctx context.Context, path string, body any) (json.RawMessage, error) {
			data, err := s.daemon(ctx, path, body)
			if code(err) == "caller_inactive" {
				if err = s.register(ctx, caller); err == nil {
					data, err = s.daemon(ctx, path, body)
				}
			}
			return data, err
		}}
}

func (s *server) libraryStateCall(ctx context.Context, caller core.Key, name string, args map[string]json.RawMessage, meta map[string]json.RawMessage) map[string]any {
	for _, raw := range args {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return failure("invalid_arguments")
		}
	}
	client := s.libraryClient(caller)
	var request any
	if name == "library_state" {
		request = &libraryclient.StateRequest{}
	} else {
		request = &libraryclient.DecisionRequest{}
	}
	if decodeArgs(args, request) != nil {
		return failure("invalid_arguments")
	}
	if name == "library_state" {
		r := request.(*libraryclient.StateRequest)
		if args["scope"] != nil && r.Scope == "" || args["directory"] != nil && r.Directory == "" {
			return failure("invalid_arguments")
		}
	}
	if name == "library_decision" {
		for _, field := range []string{"item_id", "decision", "evidence", "catalog_digest", "item_digest", "if_revision"} {
			if args[field] == nil {
				return failure("invalid_arguments")
			}
		}
	}
	if err := s.ensure(ctx, caller); err != nil {
		return refusal(caller, err)
	}
	s.observeCall(caller, meta)
	var value any
	var err error
	if r, ok := request.(*libraryclient.StateRequest); ok {
		value, err = client.State(ctx, *r)
	} else {
		value, err = client.Decision(ctx, *request.(*libraryclient.DecisionRequest))
	}
	if err != nil {
		switch {
		case errors.Is(err, library.ErrInvalidQuery), errors.Is(err, library.ErrInvalidDecision), errors.Is(err, core.ErrInvalid):
			return failure("invalid_arguments")
		case errors.Is(err, library.ErrUnsupportedScope):
			return failure("unsupported_scope")
		case errors.Is(err, library.ErrItemNotFound):
			return failure("library_item_not_found")
		default:
			return refusal(caller, err)
		}
	}
	return result(map[string]any{"ok": true, "library": value}, false)
}
