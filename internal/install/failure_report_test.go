package install

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rwcii/koinon/internal/setup"
)

// A later error keeps the report's completed steps, but never reports overall success.
func TestFailedLifecycleReportKeepsPartialProgress(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		e := newEnv(t, "systemd")
		e.o.NoStart, e.o.Agents = true, []string{"claude"}
		e.o.Setup = func(context.Context, setup.Options) (setup.Report, error) {
			return setup.Report{Family: "claude"}, errors.New("synthetic setup failure")
		}
		r, err := Install(context.Background(), e.o)
		if err == nil || r.OK || r.BinaryResult != "replaced" || r.Service != "staged" || len(r.Agents) != 1 {
			t.Fatalf("partial install report: %+v %v", r, err)
		}
		if data, err := os.ReadFile(r.Binary); err != nil || string(data) != "synthetic binary" {
			t.Fatalf("completed binary step: %q %v", data, err)
		}
		if len(e.r.calls) != 0 {
			t.Fatalf("staged install called a manager: %v", e.r.calls)
		}
	})
	t.Run("uninstall", func(t *testing.T) {
		e := newEnv(t, "systemd")
		e.o.NoStart = true
		installed, err := Install(context.Background(), e.o)
		if err != nil || !installed.OK {
			t.Fatalf("fixture install: %+v %v", installed, err)
		}
		// A malformed marker is discovered after service and binary removal.
		if err := os.WriteFile(linkMarker(e.o.Prefix), []byte("synthetic invalid marker\n"), 0600); err != nil {
			t.Fatal(err)
		}
		r, err := Uninstall(context.Background(), e.o)
		if err == nil || r.OK || r.Service != "removed" || r.BinaryResult != "removed" || len(r.Agents) != len(Families) {
			t.Fatalf("partial uninstall report: %+v %v", r, err)
		}
		if _, err := os.Stat(installed.Binary); !os.IsNotExist(err) {
			t.Fatalf("completed binary removal: %v", err)
		}
		if _, err := os.Readlink(installed.Link); err != nil {
			t.Fatalf("failed link removal should leave the link: %v", err)
		}
	})
}
