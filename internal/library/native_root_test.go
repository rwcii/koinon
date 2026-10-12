package library

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRootUsesEffectiveCustomCodexHome(t *testing.T) {
	home, custom := t.TempDir(), t.TempDir()
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
