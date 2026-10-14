package platform

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func runLockHelper(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedAfterProcessDeath$")
	cmd.Env = append(os.Environ(), "KOINON_SYNTHETIC_LOCK_HELPER=1", "KOINON_SYNTHETIC_LOCK_PATH="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
}

func TestProcessParentsHoldsThisProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	parents, err := ProcessParents()
	if err != nil {
		t.Fatal(err)
	}
	if parents[os.Getpid()] != os.Getppid() {
		t.Fatalf("parent of this process: got %d, want %d", parents[os.Getpid()], os.Getppid())
	}
	if parents[cmd.Process.Pid] != os.Getpid() {
		t.Fatalf("parent of the child: got %d, want %d", parents[cmd.Process.Pid], os.Getpid())
	}
}

// ProcessStart identifies a process with its ID: the same process reads the same value, a
// child has its own, and an ended process is gone.
func TestProcessStart(t *testing.T) {
	self, err := ProcessStart(os.Getpid())
	if err != nil || self <= 0 {
		t.Fatalf("this process: %d %v", self, err)
	}
	if again, err := ProcessStart(os.Getpid()); err != nil || again != self {
		t.Fatalf("read again: %d %v", again, err)
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	child, err := ProcessStart(pid)
	if err != nil || child < self {
		t.Fatalf("child: %d (this process %d) %v", child, self, err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	if _, err := ProcessStart(pid); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("ended child: %v", err)
	}
	if _, err := ProcessStart(0); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("pid 0: %v", err)
	}
}

func TestProcessGroups(t *testing.T) {
	groups, err := ProcessGroups()
	if err != nil {
		t.Fatal(err)
	}
	own, found := groups[os.Getpid()]
	if !found || own.Group != syscall.Getpgrp() {
		t.Fatalf("own group %+v, found %v, want %d", own, found, syscall.Getpgrp())
	}
	// A terminal always has a foreground group; without a terminal the device is 0.
	if (own.Terminal != 0) != (own.Foreground > 0) {
		t.Fatalf("own terminal %+v", own)
	}
	// A child started in its own group reports that group.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if groups, err = ProcessGroups(); err != nil || groups[cmd.Process.Pid].Group != cmd.Process.Pid {
		t.Fatalf("child group %+v: %v", groups[cmd.Process.Pid], err)
	}
}

// A process's start time is on the wall clock: this test's process started before now, and
// a child started now reads within a second of now (#275).
func TestProcessStartTime(t *testing.T) {
	own, err := ProcessStartTime(os.Getpid())
	if err != nil || own.After(time.Now()) || time.Since(own) > 24*time.Hour {
		t.Fatalf("own start %v: %v", own, err)
	}
	before := time.Now()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	child, err := ProcessStartTime(cmd.Process.Pid)
	if err != nil || child.Before(before.Add(-time.Second)) || child.After(time.Now().Add(time.Second)) {
		t.Fatalf("child start %v, started at %v: %v", child, before, err)
	}
	if _, err := ProcessStartTime(-1); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("no process: %v", err)
	}
}

// ProcessEnvironment reads a variable from another process of this user; a missing variable
// is "" with no error.
func TestProcessEnvironment(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = append(os.Environ(), "KOINON_TEST_VALUE=a b=c")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	// The child has its own environment once it has run exec.
	got, err := "", error(nil)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, err = ProcessEnvironment(cmd.Process.Pid, "KOINON_TEST_VALUE"); err == nil && got != "" {
			break
		}
	}
	if err != nil || got != "a b=c" {
		t.Fatalf("variable: %q, %v", got, err)
	}
	if got, err := ProcessEnvironment(cmd.Process.Pid, "KOINON_TEST_ABSENT"); err != nil || got != "" {
		t.Fatalf("absent variable: %q, %v", got, err)
	}
}
