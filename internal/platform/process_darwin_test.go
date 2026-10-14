package platform

import (
	"encoding/binary"
	"testing"
)

// procargsEnvironment skips the executable path, its padding and exactly argc arguments
// (empty ones included), and stops at the empty string that ends the environment.
func TestProcargsEnvironment(t *testing.T) {
	buffer := func(argc uint32, rest string) []byte {
		data := binary.NativeEndian.AppendUint32(nil, argc)
		return append(data, rest...)
	}
	data := buffer(3, "/bin/sleep\x00\x00\x00\x00sleep\x00\x00HOME=/fake\x00HOME=/home/u\x00\x00ptr_munge=1\x00")
	if got, err := procargsEnvironment(data, "HOME"); err != nil || got != "/home/u" {
		t.Fatalf("HOME: %q, %v", got, err)
	}
	if got, err := procargsEnvironment(data, "ptr_munge"); err != nil || got != "" {
		t.Fatalf("value after the environment: %q, %v", got, err)
	}
	for _, bad := range [][]byte{nil, {1, 0}, buffer(1, "/bin/sleep"), buffer(4, "/bin/sleep\x00a\x00")} {
		if _, err := procargsEnvironment(bad, "HOME"); err == nil {
			t.Fatalf("buffer %q read", bad)
		}
	}
}
