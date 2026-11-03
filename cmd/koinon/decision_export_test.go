package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// koinon decision export (#589). The daemon is a synthetic server whose pages can change
// version.

type exportServer struct {
	url      string
	calls    int
	versions func(call int) string
	onLast   func()
}

func newExportServer(t *testing.T) *exportServer {
	e := &exportServer{versions: func(int) string { return "1" }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		e.calls++
		page := map[string]any{"version": e.versions(e.calls)}
		switch request["phase"] {
		case "":
			page["records"] = []any{map[string]any{"record": "koinon.decision.v1", "reference": "D-1"}}
			page["more"], page["next"] = true, map[string]any{"phase": "live"}
		default:
			page["records"] = []any{map[string]any{"record": "koinon.decision.v1", "reference": "D-2"}}
			page["more"] = false
			if e.onLast != nil {
				e.onLast()
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": page})
	}))
	t.Cleanup(server.Close)
	e.url = strings.TrimPrefix(server.URL, "http://")
	return e
}

func runExport(t *testing.T, e *exportServer, args ...string) (map[string]any, error) {
	t.Helper()
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "secret"), []byte(strings.Repeat("1", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args = append(append([]string{"export"}, args...), "--as", "deepseek:synthetic", "--state-dir", state, "--address", e.url)
	if err := decisionCommand(context.Background(), args, &out); err != nil {
		return errorReport(err), err
	}
	var reply map[string]any
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	return reply["result"].(map[string]any), nil
}

// The export writes every page's records as private JSON lines, from the argument or --path,
// and leaves no temporary file.
func TestDecisionExportWritesLines(t *testing.T) {
	e := newExportServer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "board.jsonl")
	got, err := runExport(t, e, path)
	if err != nil || got["records"] != float64(2) || got["version"] != "1" {
		t.Fatalf("export: %v %v", got, err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"D-1"`) || !strings.Contains(lines[1], `"D-2"`) {
		t.Fatalf("lines:\n%s", data)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("files left: %v", entries)
	}
	if got, err := runExport(t, e, "--path", filepath.Join(dir, "flag.jsonl")); err != nil || got["records"] != float64(2) {
		t.Fatalf("--path: %v %v", got, err)
	}
}

// A version change between pages starts the export again; changes on every attempt fail it
// after four attempts, with no file.
func TestDecisionExportRestarts(t *testing.T) {
	e := newExportServer(t)
	// Attempt 1 reads versions 1 and 2; attempt 2 reads 2 and 2.
	e.versions = func(call int) string {
		if call == 1 {
			return "1"
		}
		return "2"
	}
	path := filepath.Join(t.TempDir(), "board.jsonl")
	if got, err := runExport(t, e, path); err != nil || got["version"] != "2" || e.calls != 4 {
		t.Fatalf("restart: %v %v calls %d", got, err, e.calls)
	}
	e.calls = 0
	e.versions = func(call int) string { return string(rune('a' + call)) }
	other := filepath.Join(t.TempDir(), "board.jsonl")
	if _, err := runExport(t, e, other); err == nil || !strings.Contains(err.Error(), "4 times") || e.calls != 8 {
		t.Fatalf("always changing: %v calls %d", err, e.calls)
	}
	if entries, _ := os.ReadDir(filepath.Dir(other)); len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
}

// An existing file, a file that appears during the export, a directory in a Git work tree
// (also through a symbolic link) and a missing argument are refused, and nothing is replaced.
func TestDecisionExportRefusals(t *testing.T) {
	e := newExportServer(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "board.jsonl")
	os.WriteFile(path, []byte("keep\n"), 0o600)
	if got, err := runExport(t, e, path); err == nil || got["code"] != "export_exists" {
		t.Fatalf("existing file: %v", got)
	}
	late := filepath.Join(dir, "late.jsonl")
	e.onLast = func() { os.WriteFile(late, []byte("late\n"), 0o600) }
	if got, err := runExport(t, e, late); err == nil || got["code"] != "export_exists" {
		t.Fatalf("file created during the export: %v", got)
	}
	if data, _ := os.ReadFile(late); string(data) != "late\n" {
		t.Fatalf("late file replaced: %q", data)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep\n" {
		t.Fatalf("existing file replaced: %q", data)
	}
	e.onLast = nil
	repo := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	os.Mkdir(filepath.Join(repo, "sub"), 0o755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(repo, "sub"), link); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{filepath.Join(repo, "sub", "board.jsonl"), filepath.Join(link, "board.jsonl")} {
		if got, err := runExport(t, e, target); err == nil || got["code"] != "export_in_worktree" {
			t.Fatalf("%s: %v", target, got)
		}
	}
	if got, err := runExport(t, e); err == nil || got["code"] != "invalid_request" {
		t.Fatalf("no file: %v", got)
	}
	if got, err := runExport(t, e, filepath.Join(t.TempDir(), "missing", "board.jsonl")); err == nil || got["code"] != "invalid_request" {
		t.Fatalf("missing directory: %v", got)
	}
}
