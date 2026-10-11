package core

import (
	"os/exec"
	"strings"
	"testing"
)

// The start command keeps the client's state directory and address, and a shell passes
// each value back as one argument (#279).
func TestStartDaemon(t *testing.T) {
	for _, c := range []struct{ root, address, want string }{
		{"/s/go", "127.0.0.1:47671", "start it with koinon install --state-dir /s/go from a login session, or run koinon serve --state-dir /s/go in a persistent managed session"},
		{"/s/go", "[::1]:47671", "start it with koinon install --state-dir /s/go from a login session, or run koinon serve --state-dir /s/go in a persistent managed session"},
		{"/s/a b", "127.0.0.1:5000", "start it with koinon serve --state-dir '/s/a b' --listen 127.0.0.1:5000 --listen-v6 '[::1]:5000' in a persistent managed session"},
		{"/s/go", "[::1]:5000", "start it with koinon serve --state-dir /s/go --listen 127.0.0.1:5000 --listen-v6 '[::1]:5000' in a persistent managed session"},
		{"/s/go", "127.0.0.2:47671", "start it with koinon serve --state-dir /s/go --listen 127.0.0.2:47671 --listen-v6 '[::1]:47671' in a persistent managed session"},
	} {
		if got := StartDaemon(c.root, c.address); got != c.want {
			t.Errorf("%s %s: %s", c.root, c.address, got)
		}
	}
	for _, value := range []string{"/s/a b", "/s/it's", `/s/"q"$HOME`, "/s/*", "[::1]:5000", "/s/plain", ""} {
		out, err := exec.Command("sh", "-c", `printf '%s\n' `+ShellWord(value)).Output()
		if err != nil || strings.TrimSuffix(string(out), "\n") != value {
			t.Errorf("%q came back as %q: %v", value, out, err)
		}
	}
	if _, err := ClientSecret(t.TempDir(), "example.com:47671"); err != ErrInvalid {
		t.Fatalf("non-loopback address: %v", err)
	}
}
