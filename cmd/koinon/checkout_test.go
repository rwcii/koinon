package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

func TestCheckoutCommandWithoutDaemon(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"work", "checkout", "--directory", repo}, nil, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		OK     bool
		Result core.Checkout
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || !got.OK {
		t.Fatalf("%s %v", out.String(), err)
	}
	want, err := core.CheckoutResource(context.Background(), repo)
	if err != nil || got.Result != want {
		t.Fatalf("result: %+v %v", got, err)
	}
	for _, args := range [][]string{{"work", "checkout", "extra"}, {"work", "checkout", "--as", "codex:synthetic"},
		{"work", "checkout", "--directory", filepath.Join(repo, "missing")}} {
		if err := run(context.Background(), args, nil, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
