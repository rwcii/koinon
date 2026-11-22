package link

import (
	"strings"
	"testing"
)

func TestParseAddress(t *testing.T) {
	long := strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + "." + strings.Repeat("e", 63) + "." + strings.Repeat("f", 61)
	if len(long) != MaxNameBytes || ValidateName(long) != nil {
		t.Fatalf("fixture %d", len(long))
	}
	participant := "codex-" + strings.Repeat("koinon-", 10) + "reviewer"
	for _, ok := range []struct{ to, name, computer string }{
		{"codex-koinon@devbox", "codex-koinon", "devbox"},
		{participant + "@" + long, participant, long},
		{strings.Repeat("n", 256) + "@" + long, strings.Repeat("n", 256), long},
	} {
		name, computer, err := ParseAddress(ok.to)
		if err != nil || name != ok.name || computer != ok.computer {
			t.Fatalf("%q: %q %q %v", ok.to, name, computer, err)
		}
	}
	if len(strings.Repeat("n", 256)+"@"+long) != MaxAddressBytes {
		t.Fatal("510-byte fixture")
	}
	for _, bad := range []string{"devbox", "@devbox", "codex@", "a@b@devbox", "codex@Devbox", "codex@dev box",
		strings.Repeat("n", 257) + "@devbox", "codex@" + long + "x", "co\ndex@devbox"} {
		if _, _, err := ParseAddress(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if IsRemoteAddress("codex-koinon") || !IsRemoteAddress("codex-koinon@devbox") {
		t.Fatal("IsRemoteAddress")
	}
}
