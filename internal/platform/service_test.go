package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// managerFake is a stateful manager: units run after a start and stop after a stop.
// refuse makes the named command fail and leaves the state unchanged.
func managerFake(t *testing.T, backend string, refuse string) (Services, *[]string) {
	running := map[string]bool{}
	return fakeServices(t, backend, func(argv []string) ([]byte, error) {
		command := strings.Join(argv, " ")
		if refuse != "" && strings.Contains(command, refuse) {
			return []byte("refused"), errors.New("exit 1")
		}
		last := argv[len(argv)-1]
		switch {
		case argv[0] == "launchctl" && argv[1] == "print" && strings.Count(last, "/") == 1:
			return nil, nil
		case argv[0] == "launchctl" && argv[1] == "print":
			if running[strings.TrimPrefix(last, "gui/501/")] {
				return nil, nil
			}
			return nil, errors.New("113")
		case argv[0] == "launchctl" && argv[1] == "bootstrap":
			running[DaemonLabel] = true
		case argv[0] == "launchctl" && argv[1] == "bootout":
			if !running[strings.TrimPrefix(last, "gui/501/")] {
				return []byte("No such process"), errors.New("3")
			}
			running[strings.TrimPrefix(last, "gui/501/")] = false
		case argv[0] == "systemctl" && argv[2] == "is-active":
			if running[last] {
				return []byte("active"), nil
			}
			return []byte("inactive"), errors.New("exit 3")
		case argv[0] == "systemctl" && (argv[2] == "restart" || argv[2] == "start"):
			running[last] = true
		case argv[0] == "systemctl" && (argv[2] == "stop" || argv[2] == "disable"):
			running[last] = false
		}
		return nil, nil
	})
}

func TestRemoveDaemonRefusesAFailedStop(t *testing.T) {
	for backend, refuse := range map[string]string{"systemd": "disable --now", "launchd": "bootout"} {
		s, _ := managerFake(t, backend, refuse)
		s.StopWait = time.Millisecond
		s.WriteDaemon("/opt/koinon", "/state")
		if err := s.StartDaemon(context.Background()); err != nil {
			t.Fatal(err)
		}
		if removed, err := s.RemoveDaemon(context.Background()); err == nil || removed || !strings.Contains(err.Error(), "service_stop_failed") {
			t.Fatalf("%s: a refused stop removed the service: %v %v", backend, removed, err)
		}
		if _, err := os.Stat(s.DaemonArtifact()); err != nil {
			t.Fatalf("%s: the artifact of a running service was removed: %v", backend, err)
		}
	}
	// An unknown observation is a failure, never a stopped service, on both managers: a
	// refused stop followed by an observation that fails for another reason.
	s, _ := fakeServices(t, "systemd", func(argv []string) ([]byte, error) {
		if argv[2] == "is-active" {
			return []byte("garbled"), errors.New("exit 4")
		}
		return nil, nil
	})
	s.WriteDaemon("/opt/koinon", "/state")
	if _, err := s.RemoveDaemon(context.Background()); err == nil || !strings.Contains(err.Error(), "service_stop_failed") {
		t.Fatalf("unknown observation: %v", err)
	}
	l, _ := fakeServices(t, "launchd", func(argv []string) ([]byte, error) {
		switch {
		case argv[1] == "print" && strings.Count(argv[2], "/") == 1:
			return nil, nil
		case argv[1] == "bootout":
			return []byte("Boot-out failed: 5: Input/output error"), errors.New("exit status 5")
		case argv[1] == "print":
			return []byte("IPC error"), errors.New("exit status 5")
		}
		return nil, nil
	})
	l.WriteDaemon("/opt/koinon", "/state")
	if removed, err := l.RemoveDaemon(context.Background()); err == nil || removed || !strings.Contains(err.Error(), "service_stop_failed") {
		t.Fatalf("launchd unknown observation: %v %v", removed, err)
	}
	if _, err := os.Stat(l.DaemonArtifact()); err != nil {
		t.Fatalf("launchd artifact removed after an unconfirmed stop: %v", err)
	}
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
	s, calls := managerFake(t, "launchd", "")
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
	s.StopWait = time.Millisecond
	if err := s.Stop(context.Background(), Unit{Name: "a.service"}); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("a unit that keeps running after its stop: %v", err)
	}
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
