package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Lifecycle command tests (sprint chunk 11). None reaches a service manager: install,
// uninstall and upgrade are tested with patched managers in their packages, and the
// import here runs without a Python installation, which needs no manager.

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"version"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal(out.Bytes(), &v); err != nil || v["version"] != "dev" || v["os"] == "" {
		t.Fatalf("version %s %v", out.String(), err)
	}
}

func TestLifecycleUsageRefusals(t *testing.T) {
	for _, args := range [][]string{{"upgrade"}, {"import", "--repository", "nopath"}, {"install", "extra"}, {"uninstall", "--from", "x"}} {
		err := run(context.Background(), args, nil, &bytes.Buffer{})
		var usage usageError
		if !errors.As(err, &usage) || usage.code != "invalid_request" {
			t.Fatalf("%v: %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"upgrade", "--status", "--state-dir", t.TempDir()}, nil, &out); err != nil || !strings.Contains(out.String(), `"journal": null`) {
		t.Fatalf("status %s %v", out.String(), err)
	}
}

func TestImportCommandWithoutPython(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "python")
	filepath.WalkDir("../../internal/importer/testdata/python-state", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("../../internal/importer/testdata/python-state", path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(from, rel), 0700)
		}
		data, _ := os.ReadFile(path)
		return os.WriteFile(filepath.Join(from, rel), data, 0600)
	})
	var m struct {
		Repositories map[string]struct{ Path, Key string } `json:"repositories"`
	}
	data, _ := os.ReadFile(filepath.Join(from, "fixture.json"))
	json.Unmarshal(data, &m)
	args := []string{"import", "--from", filepath.Join(from, "state"), "--python-prefix", filepath.Join(dir, "no-python"),
		"--state-dir", filepath.Join(dir, "go"), "--repository", m.Repositories["a"].Key + "=" + m.Repositories["a"].Path,
		"--repository", m.Repositories["b"].Key + "=" + m.Repositories["b"].Path}
	var out bytes.Buffer
	if err := run(context.Background(), args, nil, &out); err != nil || !strings.Contains(out.String(), `"result": "imported"`) {
		t.Fatalf("import %s %v", out.String(), err)
	}
	out.Reset()
	if err := run(context.Background(), append(args, "--verify"), nil, &out); err != nil || !strings.Contains(out.String(), `"result": "verified"`) {
		t.Fatalf("verify %s %v", out.String(), err)
	}
	// Without a resolvable repository the import is refused with its code.
	err := run(context.Background(), []string{"import", "--from", filepath.Join(from, "state"), "--python-prefix", filepath.Join(dir, "no-python"),
		"--state-dir", filepath.Join(dir, "go2")}, nil, &bytes.Buffer{})
	var usage usageError
	if !errors.As(err, &usage) || usage.code != "repository_unresolved" {
		t.Fatalf("unresolved: %v", err)
	}
}
