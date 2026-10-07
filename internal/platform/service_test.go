package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeServices(t *testing.T, backend string, fail func([]string) ([]byte, error)) (Services, *[]string) {
	calls := &[]string{}
	return Services{Backend: backend, Home: t.TempDir(), UID: 501, Run: func(_ context.Context, argv ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(argv, " "))
		if fail != nil {
			return fail(argv)
		}
		return nil, nil
	}}, calls
}

func TestRenderDaemonQuotes(t *testing.T) {
	s, _ := fakeServices(t, "systemd", nil)
	unit := string(s.RenderDaemon(`/opt/app space %n/koinon`, `/state "q"`))
	if !strings.Contains(unit, `ExecStart="/opt/app space %%n/koinon" serve --state-dir "/state \"q\""`) || !strings.HasPrefix(unit, "# "+ServiceMarker) {
		t.Fatalf("unit:\n%s", unit)
	}
	s.Backend = "launchd"
	plist := string(s.RenderDaemon(`/opt/a&b<c>/koinon`, "/state"))
	if !strings.Contains(plist, "<string>/opt/a&amp;b&lt;c&gt;/koinon</string>") || !strings.Contains(plist, ServiceMarker) ||
		s.DaemonArtifact() != filepath.Join(s.Home, "Library", "LaunchAgents", DaemonLabel+".plist") {
		t.Fatalf("plist:\n%s", plist)
	}
}

func TestDaemonManagerCalls(t *testing.T) {
	s, calls := fakeServices(t, "launchd", nil)
	if changed, err := s.WriteDaemon("/opt/koinon", "/state"); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, _ := s.WriteDaemon("/opt/koinon", "/state"); changed {
		t.Fatal("unchanged artifact rewritten")
	}
	if err := s.StartDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*calls, "\n")
	if !strings.Contains(joined, "launchctl bootstrap gui/501 "+s.DaemonArtifact()) || !strings.Contains(joined, "launchctl kickstart gui/501/"+DaemonLabel) {
		t.Fatalf("launchd calls %v", *calls)
	}
	if removed, err := s.RemoveDaemon(context.Background()); err != nil || !removed {
		t.Fatal(removed, err)
	}
	if _, err := os.Stat(s.DaemonArtifact()); !os.IsNotExist(err) {
		t.Fatal("artifact left")
	}
	unreachable, _ := fakeServices(t, "systemd", func([]string) ([]byte, error) { return nil, errors.New("no bus") })
	unreachable.WriteDaemon("/opt/koinon", "/state")
	if err := unreachable.StartDaemon(context.Background()); !errors.Is(err, ErrManualRequired) {
		t.Fatalf("unreachable manager: %v", err)
	}
}

func TestUnitStates(t *testing.T) {
	answers := map[string]string{"a.service": "active", "b.service": "inactive", "c.service": "failed", "d.service": "garbled"}
	s, calls := fakeServices(t, "systemd", func(argv []string) ([]byte, error) {
		state := answers[argv[len(argv)-1]]
		if state == "active" || argv[2] != "is-active" {
			return []byte(state), nil
		}
		return []byte(state), errors.New("exit 3")
	})
	for name, want := range map[string]bool{"a.service": true, "b.service": false, "c.service": false} {
		if got, err := s.Active(context.Background(), Unit{Name: name}); err != nil || got != want {
			t.Fatalf("%s: %v %v", name, got, err)
		}
	}
	if _, err := s.Active(context.Background(), Unit{Name: "d.service"}); err == nil {
		t.Fatal("an unreadable state was accepted")
	}
	s.Stop(context.Background(), Unit{Name: "a.service"})
	s.Start(context.Background(), Unit{Name: "a.service"})
	if joined := strings.Join(*calls, "\n"); !strings.Contains(joined, "systemctl --user stop a.service") || !strings.Contains(joined, "systemctl --user start a.service") {
		t.Fatalf("calls %v", *calls)
	}
	loaded := map[string]bool{"io.x": true}
	l, lcalls := fakeServices(t, "launchd", func(argv []string) ([]byte, error) {
		if argv[1] == "print" && !loaded[strings.TrimPrefix(argv[2], "gui/501/")] {
			return nil, errors.New("113")
		}
		if argv[1] == "bootout" {
			loaded[strings.TrimPrefix(argv[2], "gui/501/")] = false
		}
		return nil, nil
	})
	if err := l.Stop(context.Background(), Unit{Name: "io.x"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Start(context.Background(), Unit{Name: "io.x", Artifact: "/a/io.x.plist"}); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(*lcalls, "\n"); !strings.Contains(joined, "launchctl bootout gui/501/io.x") || !strings.Contains(joined, "launchctl bootstrap gui/501 /a/io.x.plist") {
		t.Fatalf("launchd calls %v", *lcalls)
	}
}
