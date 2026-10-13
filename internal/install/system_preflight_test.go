package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An existing invalid log directory refuses both an explicit transition and an upgrade
// detected from the existing system plist before either one changes installed artifacts.
func TestSystemLogPreflightPreservesInstalledArtifacts(t *testing.T) {
	for _, detected := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "detected"}[detected], func(t *testing.T) {
			e, f := systemEnv(t)
			e.o.System = !detected
			binary := Binary(e.o.Prefix)
			link := filepath.Join(e.o.Services.Home, ".local", "bin", "koinon")
			if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, link); err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{
				binary:                 []byte("synthetic previous binary"),
				linkMarker(e.o.Prefix): []byte(link + "\n"),
			}
			if detected {
				files[e.o.Services.SystemArtifact()] = e.o.Services.RenderSystemDaemon(binary, e.o.StateDir)
				f.SystemLoaded = true
			}
			for path, data := range files {
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			agent := e.o.Services.DaemonArtifact()
			data, err := os.ReadFile(agent)
			if err != nil {
				t.Fatal(err)
			}
			files[agent] = data
			logDir := filepath.Dir(e.o.Services.DaemonLog())
			if err := os.MkdirAll(logDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(logDir, 0755); err != nil {
				t.Fatal(err)
			}
			r, err := Install(context.Background(), e.o)
			if err == nil || !strings.HasPrefix(err.Error(), "service_log_unsafe: ") || r.OK || r.BinaryResult != "" || r.LinkResult != "" {
				t.Fatalf("preflight report: %+v %v", r, err)
			}
			for path, want := range files {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Fatalf("preflight changed %s: %q %v", path, got, err)
				}
			}
			if target, err := os.Readlink(link); err != nil || target != binary {
				t.Fatalf("preflight changed link: %q %v", target, err)
			}
			if !detected {
				if _, err := os.Stat(e.o.Services.SystemArtifact()); !os.IsNotExist(err) {
					t.Fatalf("preflight created a system plist: %v", err)
				}
			}
			if info, err := os.Stat(logDir); err != nil || info.Mode().Perm() != 0755 {
				t.Fatalf("preflight changed log directory: %v %v", info, err)
			}
			for _, call := range f.Calls {
				if strings.HasPrefix(call, "sudo ") {
					t.Fatalf("preflight ran a privileged step: %s", call)
				}
			}
		})
	}
}

func TestSystemLogPreflightLeavesCreationToSystemStep(t *testing.T) {
	e, _ := systemEnv(t)
	e.o.System = true
	dir := filepath.Dir(e.o.Services.DaemonLog())
	if p, err := systemMode(context.Background(), e.o); err != nil || p == nil {
		t.Fatalf("missing log directory preflight: %+v %v", p, err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("preflight created the log directory: %v", err)
	}
	r, err := Install(context.Background(), e.o)
	if err != nil || !r.OK || r.Service != "system" {
		t.Fatalf("system step: %+v %v", r, err)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("system step log directory: %v %v", info, err)
	}
}
