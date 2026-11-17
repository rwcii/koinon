package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/link"
	"github.com/rwcii/koinon/internal/signed"
)

// koinon maintainer key add, list and remove (#693; PDR 011 Decision 6). Keys are synthetic.
func TestMaintainerKeyCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	id, err := link.LoadIdentity(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	command := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(context.Background(), append(append([]string{"maintainer", "key"}, args...), "--state-dir", root, "--address", d.Addresses()[0]),
			strings.NewReader(""), &out)
		return out.String(), err
	}
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32))
	public := private.Public().(ed25519.PublicKey)
	keyID := signed.KeyID(public)
	statement := func(name, pin string, created int64) string {
		data, err := signed.Sign(map[string]any{"v": signed.Version, "key": keyID, "target": pin, "op": "enroll", "created": created,
			"args": map[string]any{"user": "maintainer", "client": "voice", "public_key": base64.StdEncoding.EncodeToString(public)}}, private)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if out, err := command("list"); err != nil || out != "No maintainer key is authorized on this computer.\n" {
		t.Fatalf("empty list: %q %v", out, err)
	}
	for name, path := range map[string]string{
		"other computer": statement("other", strings.Repeat("cd", 32), time.Now().Unix()),
		"old statement":  statement("old", id.Pin, time.Now().Unix()-700),
	} {
		if out, err := command("add", "--statement", path); err == nil || !strings.Contains(err.Error(), "signature_refused") {
			t.Fatalf("%s: %q %v", name, out, err)
		}
	}
	good := statement("good", id.Pin, time.Now().Unix())
	out, err := command("add", "--statement", good)
	if err != nil || !strings.Contains(out, "Key "+keyID+" is authorized for maintainer (voice) on this computer.") {
		t.Fatalf("add: %q %v", out, err)
	}
	if _, err := command("add", "--statement", good); err == nil || !strings.Contains(err.Error(), "statement_used") {
		t.Fatalf("add again: %v", err)
	}
	if out, err = command("list"); err != nil || !strings.HasPrefix(out, keyID+"  maintainer (voice)  added ") || !strings.Contains(out, " by cli\n") {
		t.Fatalf("list: %q %v", out, err)
	}
	if out, err = command("remove", keyID); err != nil || !strings.Contains(out, "is removed") {
		t.Fatalf("remove: %q %v", out, err)
	}
	if _, err := command("remove", keyID); err == nil || !strings.Contains(err.Error(), "key_unknown") {
		t.Fatalf("remove again: %v", err)
	}
	if out, err = command("list"); err != nil || !strings.HasPrefix(out, "No maintainer key") {
		t.Fatalf("list after removal: %q %v", out, err)
	}
	if out, err = command("list", "--all"); err != nil || !strings.Contains(out, "  removed ") {
		t.Fatalf("list --all: %q %v", out, err)
	}
	for _, args := range [][]string{{"add"}, {"remove"}, {"list", "extra"}, {"rotate"}, {"remove", keyID, "--all"}, {"list", "--statement", good}} {
		if _, err := command(args...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}
