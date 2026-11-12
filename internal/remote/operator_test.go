package remote

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The native stderr chain of a refused Serve write at Tailscale v1.104.5:
// client/local/serve.go wraps client/local/local.go's 403 prefix around the
// ipn/localapi/serve.go text.
const deniedServeWrite = "sending serve config: Access denied: serve config denied\n"

func named(name string) func() (string, error) { return func() (string, error) { return name, nil } }

func TestCommandFailureNamesTheLiteralOperatorAccount(t *testing.T) {
	d := commandFailure("linux", []byte(deniedServeWrite), named("synthetic-user"))
	if d.Code != OperatorRequired {
		t.Fatalf("code = %q", d.Code)
	}
	want := "Tailscale refused this account. Run sudo tailscale set --operator=synthetic-user, then run koinon remote enable"
	if d.Next != want {
		t.Fatalf("next = %q", d.Next)
	}
	if strings.Contains(d.Error(), "$USER") || strings.Contains(d.Error(), "serve config denied") {
		t.Fatalf("diagnostic copies stderr or names $USER: %q", d.Error())
	}
}

func TestCommandFailureWithoutAPlainAccountNameSaysWhereToRunIDUN(t *testing.T) {
	for _, account := range []func() (string, error){
		func() (string, error) { return "", errors.New("synthetic lookup failure") },
		named("name with spaces"),
		named("x;rm -rf /"),
		named(""),
	} {
		d := commandFailure("linux", []byte(deniedServeWrite), account)
		if d.Code != OperatorRequired {
			t.Fatalf("code = %q", d.Code)
		}
		for _, part := range []string{"As the account that runs Koinon, outside a root shell, run id -un", "sudo tailscale set --operator=NAME", "then run koinon remote enable"} {
			if !strings.Contains(d.Next, part) {
				t.Fatalf("next %q lacks %q", d.Next, part)
			}
		}
		if strings.Contains(d.Next, "rm -rf") || strings.Contains(d.Next, "spaces") {
			t.Fatalf("unsafe name copied: %q", d.Next)
		}
	}
}

func TestCommandFailureKeepsTheGenericDiagnosticOtherwise(t *testing.T) {
	for _, tc := range []struct{ goos, stderr string }{
		{"linux", "synthetic private account failure\n"},
		{"linux", ""},
		{"darwin", deniedServeWrite},
	} {
		d := commandFailure(tc.goos, []byte(tc.stderr), named("synthetic-user"))
		if d.Code != "tailscale_command_failed" {
			t.Fatalf("%s %q: code = %q", tc.goos, tc.stderr, d.Code)
		}
		if strings.Contains(d.Error(), "synthetic") {
			t.Fatalf("diagnostic copies stderr or the account: %q", d.Error())
		}
	}
}

func TestOperatorCommandRejectsUnsafeNames(t *testing.T) {
	if got := operatorCommand(named("synthetic.user-1")); got != "sudo tailscale set --operator=synthetic.user-1" {
		t.Fatalf("command = %q", got)
	}
	for _, name := range []string{"", "-flag", "a b", "a$b", strings.Repeat("a", 65)} {
		if got := operatorCommand(named(name)); got != "" {
			t.Fatalf("%q: command = %q", name, got)
		}
	}
}

// The account is the passwd entry of the process UID. $USER never names it, also when
// the entry is missing, as Go's user.Current does without cgo (#652 review).
func TestAccountByUIDNeverTrustsUSER(t *testing.T) {
	t.Setenv("USER", "root")
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	passwd := filepath.Join(dir, "passwd")
	if err := os.WriteFile(passwd, []byte("root:x:0:0:root:/root:/bin/sh\nsynthetic-user:x:4242:4242::/home/synthetic-user:/bin/sh\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if name, err := accountByUID(4242, passwd); err != nil || name != "synthetic-user" {
		t.Fatalf("listed UID: %q %v", name, err)
	}
	account := func() (string, error) { return accountByUID(5151, passwd) }
	if name, err := account(); err == nil || name != "" {
		t.Fatalf("unlisted UID named %q", name)
	}
	if got := operatorCommand(account); got != "" {
		t.Fatalf("unlisted UID got a command: %q", got)
	}
	d := commandFailure("linux", []byte(deniedServeWrite), account)
	if d.Code != OperatorRequired || strings.Contains(d.Next, "--operator=root") || !strings.Contains(d.Next, "outside a root shell, run id -un") {
		t.Fatalf("unlisted UID: %+v", d)
	}
	if _, err := accountByUID(4242, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("unreadable passwd named an account")
	}
}
