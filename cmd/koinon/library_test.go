package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/library"
)

func TestLibraryCommandNativeSessionPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Release: "v9.8.7"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, _ := core.ReadSecret(root)
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-library-cli", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	options := []string{"--as", "deepseek:synthetic-library-cli", "--state-dir", root, "--address", d.Addresses()[0]}
	call := func(args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		if err := run(context.Background(), append([]string{"library"}, args...), nil, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	var page struct {
		Library library.Page `json:"library"`
	}
	if err := json.Unmarshal(call(append([]string{"list", "--limit", "1"}, options...)...), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Library.Items) != 1 || !page.Library.More || page.Library.NextAfter == "" {
		t.Fatal("command paging")
	}
	var next struct {
		Library library.Page `json:"library"`
	}
	if err := json.Unmarshal(call(append([]string{"list", "--after", page.Library.NextAfter}, options...)...), &next); err != nil {
		t.Fatal(err)
	}
	if next.Library.Items[0].ID == page.Library.Items[0].ID {
		t.Fatal("command cursor repeated page")
	}
	var read struct {
		Library library.ReadResult `json:"library"`
	}
	if err := json.Unmarshal(call(append([]string{"read", "skill.pickup"}, options...)...), &read); err != nil {
		t.Fatal(err)
	}
	cat, _ := library.Embedded("v9.8.7")
	want, _ := cat.Read("deepseek", "skill.pickup")
	if read.Library.Item.Content != want.Item.Content || read.Library.Item.ContentDigest != want.Item.ContentDigest || read.Library.Digest != cat.Digest {
		t.Fatal("command canonical content")
	}
	for _, d := range read.Library.Item.Destinations {
		if d.Family != "deepseek" {
			t.Fatal("caller family replaced")
		}
	}
	// The item may also follow the options, matching the existing command convention.
	call(append(append([]string{"read"}, options...), "skill.pickup")...)
}

func TestLibraryCommandRequiresItsExistingIdentity(t *testing.T) {
	for _, args := range [][]string{{"library"}, {"library", "state"}, {"library", "read"}, {"library", "list"}, {"library", "read", "skill.pickup", "--family", "codex"}, {"library", "list", "--as", "wrong"}} {
		err := run(context.Background(), args, nil, &bytes.Buffer{})
		var usage usageError
		if !errors.As(err, &usage) || usage.code != "invalid_request" {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
