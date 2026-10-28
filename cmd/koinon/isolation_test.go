package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points every user configuration and state root at a temporary directory, so
// that no test can read or write the user's agent configuration or Koinon state, even a
// test that omits an explicit path.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "koinon-isolated-home-")
	if err != nil {
		panic(err)
	}
	// Keep the Go caches, which tests that build the binary use, at their real places.
	if cache, err := os.UserCacheDir(); err == nil && os.Getenv("GOCACHE") == "" {
		os.Setenv("GOCACHE", filepath.Join(cache, "go-build"))
	}
	if home, err := os.UserHomeDir(); err == nil && os.Getenv("GOPATH") == "" {
		os.Setenv("GOPATH", filepath.Join(home, "go"))
	}
	for _, name := range []string{"HOME", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "DSH_HOME", "GROK_HOME"} {
		os.Setenv(name, dir)
	}
	// A test run inside a launched agent session must not reach that session's daemon.
	for _, name := range []string{"KOINON_STATE_DIR", "KOINON_DAEMON_ADDRESS", "KOINON_LAUNCH_ID"} {
		os.Unsetenv(name)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
