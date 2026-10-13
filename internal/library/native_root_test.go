package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRootUsesEffectiveCustomCodexHome(t *testing.T) {
	home, custom := t.TempDir(), t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	custom, _ = filepath.EvalSymlinks(custom)
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home, "CODEX_HOME": custom}
	getenv := func(name string) string { return env[name] }
	root, reason := NativeRoot("codex", getenv)
	if root != custom || reason != "" {
		t.Fatalf("effective home: %q %q", root, reason)
	}
	delete(env, "CODEX_HOME")
	if root, reason := NativeRoot("codex", getenv); root != "" || reason != "native_root_unverified" {
		t.Fatal("direct missing environment guessed a default root", root, reason)
	}
	env["KOINON_LAUNCH_ID"] = "synthetic verified managed launch"
	root, reason = NativeRoot("codex", getenv)
	if root != filepath.Join(home, ".codex") || reason != "" {
		t.Fatalf("default home: %q %q", root, reason)
	}
	for _, family := range []string{"claude", "opencode", "agy", "grok", "deepseek"} {
		if root, reason := NativeRoot(family, getenv); root != "" || reason != "native_root_unverified" {
			t.Fatalf("guessed %s discovery: %q %q", family, root, reason)
		}
	}
	env["CODEX_HOME"] = filepath.Join(custom, "absent")
	if root, reason := NativeRoot("codex", getenv); root != "" || reason != "native_root_unavailable" {
		t.Fatalf("unavailable custom root: %q %q", root, reason)
	}
}

func TestRelativeNativeRootUsesRegisteredDirectory(t *testing.T) {
	parent := t.TempDir()
	parent, _ = filepath.EvalSymlinks(parent)
	native, registered := filepath.Join(parent, "native"), filepath.Join(parent, "project")
	for _, root := range []string{native, registered} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"CODEX_HOME": "../native", "HOME": parent}
	root, reason := NativeRootAt("codex", func(name string) string { return env[name] }, registered)
	if root != native || reason != "" {
		t.Fatalf("relative verified root: %q %q", root, reason)
	}
	file := filepath.Join(parent, "not-a-root")
	if err := os.WriteFile(file, []byte("synthetic non-directory native home"), 0600); err != nil {
		t.Fatal(err)
	}
	env["CODEX_HOME"] = file
	if root, reason := NativeRootAt("codex", func(name string) string { return env[name] }, registered); root != "" || reason != "native_root_unavailable" {
		t.Fatal("non-directory native root accepted", root, reason)
	}
}
