package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// koinon link against a real daemon with synthetic state. Pairing itself is tested in
// internal/core with two daemons; these tests cover the command: its arguments, the secret,
// the invitation on standard input and the daemon's refusals.

func TestLinkCommandArguments(t *testing.T) {
	for _, args := range [][]string{{"link"}, {"link", "bogus"}, {"link", "unpair"}, {"link", "status", "extra"},
		{"link", "rename"}, {"link", "rename", "a", "b", "c"}, {"link", "invite", "--port", "1"}, {"link", "status", "--secret-file", "x"}} {
		var out bytes.Buffer
		err := run(context.Background(), args, strings.NewReader(""), &out)
		var usage usageError
		if !errors.As(err, &usage) || usage.code != "invalid_request" || out.Len() != 0 {
			t.Fatalf("%v: %v %q", args, err, out.String())
		}
	}
}

func TestLinkCommandWithDaemon(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d := maintainerMCPDaemon(t, root, "127.0.0.1:0", true)
	address := d.Addresses()[0]
	secretFile := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretFile, []byte(mcpSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	common := []string{"--state-dir", root, "--address", address}
	linkRun := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := run(context.Background(), append(append([]string{"link"}, args...), common...), strings.NewReader(stdin), &out)
		return out.String(), err
	}

	out, err := linkRun("", "status", "--json")
	var status struct {
		OK     bool            `json:"ok"`
		Result core.LinkStatus `json:"result"`
	}
	if err != nil || json.Unmarshal([]byte(out), &status) != nil || !status.OK || status.Result.Remote || status.Result.Configured {
		t.Fatalf("status: %v %s", err, out)
	}
	if out, err := linkRun("", "status"); err != nil || !strings.Contains(out, "Remote access: off") || !strings.Contains(out, "Paired computers: none") {
		t.Fatalf("status text: %v %s", err, out)
	}
	// A change needs the secret: no file and no terminal is refused before any call.
	if _, err := linkRun("", "invite"); err == nil || !strings.Contains(err.Error(), "--secret-file") {
		t.Fatalf("invite without a secret: %v", err)
	}
	refusal := func(err error) string {
		var refused core.RefusedError
		if errors.As(err, &refused) {
			return refused.Code
		}
		return ""
	}
	if _, err := linkRun("", "invite", "--secret-file", secretFile); refusal(err) != "link_remote_required" {
		t.Fatalf("invite with Remote off: %v", err)
	}
	// join reads the invitation from the first line of standard input.
	if _, err := linkRun("koinon-link-1:short\n", "join", "--secret-file", secretFile); refusal(err) != "link_invitation_invalid" {
		t.Fatalf("join: %v", err)
	}
	if _, err := linkRun("", "join", "--secret-file", secretFile); err == nil || !strings.Contains(err.Error(), "standard input") {
		t.Fatalf("join without input: %v", err)
	}
	if _, err := linkRun("", "unpair", "nobody", "--secret-file", secretFile); refusal(err) != "link_pair_not_found" {
		t.Fatalf("unpair: %v", err)
	}
	wrong := filepath.Join(t.TempDir(), "wrong")
	if err := os.WriteFile(wrong, []byte("not the secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := linkRun("", "unpair", "nobody", "--secret-file", wrong); refusal(err) == "" || refusal(err) == "link_pair_not_found" {
		t.Fatalf("wrong secret: %v", err)
	}
}
