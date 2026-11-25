package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/rwcii/koinon/internal/library"
)

// koinon preflight brief prints the standard lens from this binary's preflight skill and
// refuses an unknown name or a wrong form (#814).
func TestPreflightBrief(t *testing.T) {
	want, err := library.EmbeddedStandardLens("wording")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"preflight", "brief", "wording"}, nil, &out); err != nil || out.String() != want {
		t.Fatalf("%v %q", err, out.String())
	}
	for _, c := range []struct {
		args []string
		code string
	}{
		{[]string{"preflight", "brief", "missing"}, "not_found"},
		{[]string{"preflight", "brief"}, "invalid_request"},
		{[]string{"preflight", "run"}, "invalid_request"},
		{[]string{"preflight", "brief", "wording", "extra"}, "invalid_request"},
	} {
		out.Reset()
		var usage usageError
		if err := run(context.Background(), c.args, nil, &out); !errors.As(err, &usage) || usage.code != c.code || out.Len() != 0 {
			t.Errorf("%v: %v %q", c.args, err, out.String())
		}
	}
}
