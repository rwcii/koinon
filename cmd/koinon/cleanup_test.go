package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rwcii/koinon/internal/core"
)

// koinon sessions, inbox clear and maintenance sweep (#388): a change without --yes shows
// the selection and changes nothing; with --yes it changes each selected session.
func TestCleanupCommands(t *testing.T) {
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
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: id, Directory: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	command := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(context.Background(), append(args, "--state-dir", root, "--address", d.Addresses()[0]), nil, &out)
		return out.String(), err
	}
	list := func(args ...string) []core.CleanupSession {
		t.Helper()
		out, err := command(append([]string{"sessions", "list", "--json"}, args...)...)
		var reply struct {
			Sessions []core.CleanupSession `json:"sessions"`
		}
		if err != nil || json.Unmarshal([]byte(out), &reply) != nil {
			t.Fatalf("list: %v %s", err, out)
		}
		return reply.Sessions
	}
	all := list()
	if len(all) != 2 || all[0].State != "active" {
		t.Fatalf("list: %+v", all)
	}
	a := all[0].Name
	if out, err := command("sessions", "list"); err != nil || !strings.Contains(out, a) || !strings.Contains(out, "UNACKED") {
		t.Fatalf("table: %v %s", err, out)
	}
	// Without --yes, the selection only.
	if out, err := command("sessions", "retire", a); err != nil || !strings.Contains(out, `"applied":false`) || list(a)[0].State != "active" {
		t.Fatalf("preview: %v %s", err, out)
	}
	if out, err := command("sessions", "retire", a, "--yes"); err != nil || !strings.Contains(out, `"result":"accepted"`) || list(a)[0].State != "retired" {
		t.Fatalf("retire: %v %s", err, out)
	}
	if out, err := command("sessions", "purge", "--state", "retired", "--yes"); err != nil || !strings.Contains(out, `"result":"accepted"`) || !list(a)[0].PurgeMarked {
		t.Fatalf("purge: %v %s", err, out)
	}
	if out, err := command("sessions", "unpurge", a); err != nil || list(a)[0].PurgeMarked {
		t.Fatalf("unpurge: %v %s", err, out)
	}
	if out, err := command("inbox", "clear", "--family", "deepseek", "--yes"); err != nil || strings.Count(out, `"result":"accepted"`) != 2 {
		t.Fatalf("clear: %v %s", err, out)
	}
	if out, err := command("sessions", "purge", a, "--yes"); err != nil {
		t.Fatalf("purge again: %v %s", err, out)
	}
	if out, err := command("maintenance", "sweep"); err != nil || !strings.Contains(out, `"sessions_by_purge":1`) || len(list()) != 1 {
		t.Fatalf("sweep: %v %s", err, out)
	}
	// Claims and rooms: nothing is held or inactive here, so each lists nothing.
	if out, err := command("claims", "release", "--yes"); err != nil || !strings.Contains(out, `"claims":[]`) {
		t.Fatalf("claims release: %v %s", err, out)
	}
	if out, err := command("rooms", "prune"); err != nil || !strings.Contains(out, `"members":[]`) || !strings.Contains(out, `"applied":false`) {
		t.Fatalf("rooms prune: %v %s", err, out)
	}
	for _, args := range [][]string{{"sessions", "purge", "--yes"}, {"sessions", "remove"}, {"sessions", "list", "--inactive-for", "soon"},
		{"sessions", "list", "synthetic-nobody"}, {"maintenance", "sweep", "extra"}, {"claims", "release", "extra"},
		{"claims", "release", "--held-by", "synthetic-nobody"}, {"rooms", "delete"}, {"rooms", "delete", "0123456789abcdef0123456789abcdef", "--yes"},
		{"rooms", "prune", "0123456789abcdef0123456789abcdef"}, {"rooms", "list"}} {
		if _, err := command(args...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}

func TestParseInactivity(t *testing.T) {
	for value, want := range map[string]int64{"30d": 30 * 86400, "72h": 72 * 3600, "90s": 90} {
		if got, ok := parseInactivity(value); !ok || got != want {
			t.Fatalf("%s: %d %v", value, got, ok)
		}
	}
	for _, value := range []string{"", "0d", "-1d", "d", "1ms", "soon"} {
		if _, ok := parseInactivity(value); ok {
			t.Fatalf("%q accepted", value)
		}
	}
}
