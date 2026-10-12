package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

// The room commands in the installed DeepSeek guide reach the room API as written: flags may
// come before, between or after ROOM_ID and BODY, and "--" ends the flags.
func TestRoomCommandsOfTheDeepSeekGuide(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-guide", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var guide bytes.Buffer
	if err := setup.Guide("deepseek", &guide); err != nil {
		t.Fatal(err)
	}
	room := strings.Repeat("a", 32)
	deadline := strconv.FormatInt(time.Now().Unix()+600, 10)
	run := func(args []string) error {
		args = append([]string{args[0], "--state-dir", root, "--address", d.Addresses()[0]}, args[1:]...)
		return roomCommand(context.Background(), args, &bytes.Buffer{})
	}
	refusal := func(err error) string {
		var refused core.RefusedError
		if errors.As(err, &refused) {
			return refused.Code
		}
		var usage usageError
		if errors.As(err, &usage) {
			return usage.code
		}
		return "error: " + err.Error()
	}
	commands := 0
	for _, line := range strings.Split(guide.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "koinon room ") {
			continue
		}
		commands++
		line = strings.NewReplacer("$DSH_SESSION_ID", "synthetic-guide", "ROOM_ID", room, "'message'", "synthetic", "KEY", "synthetic-key", "UNIX_SECONDS", deadline).Replace(line)
		err := run(strings.Fields(line)[2:])
		if strings.HasPrefix(line, "koinon room list") {
			if err != nil {
				t.Fatalf("%s: %v", line, err)
			}
		} else if refusal(err) != "not_room_member" {
			t.Fatalf("%s: %s", line, refusal(err))
		}
	}
	if commands != 4 {
		t.Fatalf("guide room commands: %d", commands)
	}
	for _, args := range [][]string{
		{"post", room, "synthetic", "--as", "deepseek:synthetic-guide", "--key", "k", "--deadline", deadline},
		{"post", "--key", "k", room, "--as", "deepseek:synthetic-guide", "synthetic", "--deadline", deadline},
		{"post", "--as", "deepseek:synthetic-guide", "--key", "k", "--deadline", deadline, "--", room, "-synthetic"},
	} {
		if code := refusal(run(args)); code != "not_room_member" {
			t.Fatalf("%v: %s", args, code)
		}
	}
	if code := refusal(run([]string{"post", "--as", "deepseek:synthetic-guide", room, "a", "b", "--key", "k", "--deadline", deadline})); code != "invalid_request" {
		t.Fatalf("three positional arguments: %s", code)
	}
}
