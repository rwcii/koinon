//go:build native

package nativetest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// TestUpgradeFromGoRelease installs the current main release (v0.4.1), seeds state through
// its own commands, and upgrades it as INSTALL.md describes: the candidate's install with
// the same prefix and state directory. The daemon restarts on the candidate and every
// seeded item reads back.
func TestUpgradeFromGoRelease(t *testing.T) {
	dir := tempDir(t)
	source := filepath.Join(dir, "release")
	extractCommit(t, goRelease, source)
	old := filepath.Join(dir, "release-bin", "koinon")
	// The extracted source has no VCS data; a .git above the temporary directory must not
	// reach the build (#412).
	build := exec.Command("go", "build", "-buildvcs=false", "-o", old, "./cmd/koinon")
	build.Dir, build.Env = source, append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", goRelease, err, out)
	}
	prefix, state := filepath.Join(dir, "prefix space"), filepath.Join(dir, "state")
	if _, err := koinonAt(t, old, "install", "--prefix", prefix, "--state-dir", state); err != nil {
		t.Fatal(err)
	}
	defer koinon(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	installed := filepath.Join(prefix, "bin", "koinon")
	if sum(t, installed) != sum(t, old) {
		t.Fatal("the release install did not install the release binary")
	}
	before := health(t, state)
	if before.Schema != goReleaseSchema || before.PID == 0 {
		t.Fatalf("release daemon: %+v", before)
	}

	// Seed through the release's own commands.
	repo := filepath.Join(dir, "repo")
	gitRepo(t, repo)
	// Go flags end at the first positional argument, so each call places --state-dir before
	// its message, query or entry text.
	release := func(args ...string) map[string]any {
		t.Helper()
		result, err := koinonAt(t, old, args...)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	names := map[string]string{}
	for _, id := range []string{"upgrade-a", "upgrade-b"} {
		registered := release("register", "--state-dir", state, "--as", "deepseek:"+id, "--repository", repo, "--directory", repo,
			"--dsh-url", "http://127.0.0.1:9", "--dsh-credentials", filepath.Join(dir, "none"))
		names[id] = text(registered, "session", "name")
		if names[id] == "" {
			t.Fatalf("register %s: %v", id, registered)
		}
	}
	const message, note = "A message sent by the release.", "A memory entry recorded by the release."
	deadline := fmt.Sprint(time.Now().Add(10 * time.Minute).Unix())
	release("send", "--state-dir", state, "--as", "deepseek:upgrade-a", names["upgrade-b"], message)
	release("memory", "record", "--state-dir", state, "--as", "deepseek:upgrade-a", "--type", "decision", "--key", "upgrade-note", "--deadline", deadline, note)
	created := release("work", "create", "--state-dir", state, "--as", "deepseek:upgrade-a", "--title", "Work claimed under the release",
		"--criteria", "Reads back after the upgrade.", "--non-goals", "None.", "--key", "upgrade-create", "--deadline", deadline)
	work := text(created, "result", "work_id")
	if work == "" {
		t.Fatalf("work create: %v", created)
	}
	release("work", "start", work, "--state-dir", state, "--as", "deepseek:upgrade-a", "--if-revision", fmt.Sprint(number(created, "result", "revision")),
		"--checkpoint", "claimed before the upgrade", "--next-artifact", "the upgrade", "--progress-deadline", deadline,
		"--key", "upgrade-start", "--deadline", deadline)

	// The claim's consumer as the release recorded it: since v0.2.0, a release records the
	// claim of a participant's session under the participant.
	claimed, _ := json.Marshal(release("work", "get", work, "--state-dir", state, "--as", "deepseek:upgrade-a"))
	consumer := regexp.MustCompile(`"consumer":"[^"]+"`).Find(claimed)
	if consumer == nil {
		t.Fatalf("work get before the upgrade: no consumer in %s", claimed)
	}

	// Upgrade with the candidate, as INSTALL.md documents.
	must(t, "install", "--prefix", prefix, "--state-dir", state)
	if sum(t, installed) != sum(t, binary) {
		t.Fatal("the upgrade did not install the candidate binary")
	}
	var after daemonHealth
	waitFor(t, "the candidate daemon", func() bool {
		after = health(t, state)
		return after.PID != 0 && after.PID != before.PID
	})
	if after.Schema < before.Schema {
		t.Fatalf("candidate daemon: %+v", after)
	}
	if _, err := os.Stat(filepath.Join(state, "state.sqlite3-shm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shared-memory file after the upgrade: %v", err)
	}
	reads := map[string][]string{
		message:                      {"inbox", "--state-dir", state, "--as", "deepseek:upgrade-b", "--after", "0"},
		note:                         {"memory", "recall", "--state-dir", state, "--as", "deepseek:upgrade-b", "release"},
		"claimed before the upgrade": {"work", "get", work, "--state-dir", state, "--as", "deepseek:upgrade-a"},
		names["upgrade-a"]:           {"peers", "--state-dir", state, "--as", "deepseek:upgrade-b"},
		names["upgrade-b"]:           {"peers", "--state-dir", state, "--as", "deepseek:upgrade-a"},
		string(consumer):             {"work", "get", work, "--state-dir", state, "--as", "deepseek:upgrade-a"},
	}
	for want, args := range reads {
		result := must(t, args...)
		data, _ := json.Marshal(result)
		if !strings.Contains(string(data), want) {
			t.Errorf("%s after the upgrade: no %q in %s", strings.Join(args[:2], " "), want, data)
		}
	}

	// The stored schema, read after a clean stop: the running daemon holds the store exclusively.
	must(t, "uninstall", "--prefix", prefix, "--state-dir", state)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "state.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var stored int
	if err := db.QueryRow("PRAGMA user_version").Scan(&stored); err != nil || stored != after.Schema {
		t.Fatalf("stored schema %d, daemon schema %d: %v", stored, after.Schema, err)
	}
}

type daemonHealth struct {
	PID     int
	Schema  int    `json:"schema"`
	Version string `json:"version"`
}

// health reads the running daemon's schema from its status and its process ID from the
// manager (v0.1.0's status had no pid), or a zero value when the daemon does not answer.
func health(t *testing.T, state string) daemonHealth {
	t.Helper()
	// One call, which may fail while the daemon restarts: the caller waits and asks again.
	var h daemonHealth
	secret, err := core.ReadSecret(state)
	if err != nil {
		return h
	}
	data, err := core.Call(context.Background(), "127.0.0.1:47671", secret, "/v1/status", nil)
	if err != nil || json.Unmarshal(data, &h) != nil {
		return daemonHealth{}
	}
	h.PID = daemonPID(t)
	return h
}

func sum(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

// text and number read a nested field of a decoded JSON object.
func text(m map[string]any, path ...string) string {
	v, _ := field(m, path).(string)
	return v
}

func number(m map[string]any, path ...string) int64 {
	v, _ := field(m, path).(float64)
	return int64(v)
}

func field(m map[string]any, path []string) any {
	var v any = m
	for _, key := range path {
		object, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = object[key]
	}
	return v
}
