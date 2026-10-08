package platform

import (
	"runtime"
	"testing"
)

func TestBrowserOpener(t *testing.T) {
	env := func(values map[string]string) func(string) string { return func(k string) string { return values[k] } }
	if _, ok := BrowserOpener(env(map[string]string{"DISPLAY": ":0", "SSH_CONNECTION": "a b c d"})); ok {
		t.Fatal("SSH session opened a browser")
	}
	if _, ok := BrowserOpener(env(map[string]string{"DISPLAY": ":0", "SSH_TTY": "/dev/pts/1"})); ok {
		t.Fatal("SSH terminal opened a browser")
	}
	switch runtime.GOOS {
	case "linux":
		if _, ok := BrowserOpener(env(nil)); ok {
			t.Fatal("no display opened a browser")
		}
		for _, name := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
			if argv, ok := BrowserOpener(env(map[string]string{name: "x"})); !ok || len(argv) != 1 || argv[0] != "xdg-open" {
				t.Fatalf("%s: %q %v", name, argv, ok)
			}
		}
	case "darwin":
		if argv, ok := BrowserOpener(env(nil)); !ok || len(argv) != 1 || argv[0] != "/usr/bin/open" {
			t.Fatalf("darwin: %q %v", argv, ok)
		}
	}
}
