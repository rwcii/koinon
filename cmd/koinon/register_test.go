package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/rwcii/koinon/internal/core"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterDeepSeekNativeCommandPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	directory := t.TempDir()
	daemon, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	credential := filepath.Join(t.TempDir(), ".credentials.yaml")
	var out bytes.Buffer
	args := []string{"register", "--as", "deepseek:synthetic-native", "--state-dir", root, "--address", daemon.Addresses()[0], "--directory", directory, "--dsh-url", "http://127.0.0.1:12345", "--dsh-credentials", credential}
	if err = run(context.Background(), args, nil, &out); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Session core.Session `json:"session"`
	}
	json.Unmarshal(out.Bytes(), &reply)
	if reply.Session.Family != "deepseek" || reply.Session.ID != "synthetic-native" || !strings.Contains(string(reply.Session.WakeTarget), credential) {
		t.Fatalf("registration %+v", reply)
	}
	name := reply.Session.Name
	out.Reset()
	if err = run(context.Background(), args, nil, &out); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out.Bytes(), &reply)
	if reply.Session.Revision != 2 || reply.Session.Name != name {
		t.Fatal("repeat registration changed identity")
	}
	for _, bad := range [][]string{{"register", "--as", "codex:synthetic"}, {"register", "--as", "deepseek:"}, {"register", "--unknown"}} {
		if err = run(context.Background(), bad, nil, &out); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}
