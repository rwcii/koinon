package remote

import (
	"errors"
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
