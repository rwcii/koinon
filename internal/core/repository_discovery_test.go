package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scaleRepositoryGitTimeout sets the discovery bound for one test, so that a hung fake Git
// ends quickly. Set it before the test starts a daemon: cleanups run in reverse order, so
// the daemon stops before the bound is restored.
func scaleRepositoryGitTimeout(t *testing.T, bound time.Duration) {
	t.Helper()
	previous := repositoryGitTimeout
	repositoryGitTimeout = bound
	t.Cleanup(func() { repositoryGitTimeout = previous })
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// A registration's first Git call that is slower than half the bound, as on a freshly
// started daemon on a loaded machine (#410), still registers: both calls share the bound.
// At the real scale, the bound is 6 seconds and the first call took more than the earlier
// 3 seconds per call.
func TestRegistrationToleratesSlowFirstGitCall(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	scaleRepositoryGitTimeout(t, 2*time.Second)
	repo := testRepo(t)
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	cold := filepath.Join(bin, "cold")
	script := fmt.Sprintf("#!/bin/sh\nif [ ! -e %s ]; then\n : > %s\n /bin/sleep 1.2\nfi\nexec %s \"$@\"\n",
		shellQuote(cold), shellQuote(cold), shellQuote(git))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	started := time.Now()
	status, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register",
		Registration{Family: "deepseek", ID: "synthetic-slow-discovery", Directory: repo, Repository: repo})
	if status != http.StatusOK || reply["ok"] != true {
		t.Fatalf("slow first Git call refused: status=%d reply=%v", status, reply)
	}
	if _, err := os.Stat(cold); err != nil {
		t.Fatalf("fake Git did not run: %v", err)
	}
	if elapsed := time.Since(started); elapsed < repositoryGitTimeout/2 {
		t.Fatalf("first Git call was not slower than half the bound: %s", elapsed)
	}
}

// A hung Git still ends with ErrRepositoryGitTimeout once the bound passes.
func TestRepositoryDiscoveryTimesOutHungGit(t *testing.T) {
	scaleRepositoryGitTimeout(t, 300*time.Millisecond)
	repo := testRepo(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	started := time.Now()
	_, err := repository(context.Background(), repo)
	var timeout RepositoryGitTimeout
	if !errors.Is(err, ErrRepositoryGitTimeout) || !errors.As(err, &timeout) || len(timeout.Calls) != 1 || timeout.Calls[0] < repositoryGitTimeout {
		t.Fatalf("hung Git: %v %v", err, timeout.Calls)
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("discovery ignored its bound: %s", elapsed)
	}
}

// A timeout reports the time of each Git call of the request, so that a refusal shows which
// call was slow (#410). The first call answers at once and the second hangs.
func TestRepositoryTimeoutReportsEachGitCall(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	scaleRepositoryGitTimeout(t, time.Second)
	repo := testRepo(t)
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	first := filepath.Join(bin, "first")
	script := fmt.Sprintf("#!/bin/sh\nif [ -e %s ]; then\n exec /bin/sleep 30\nfi\n: > %s\nexec %s \"$@\"\n",
		shellQuote(first), shellQuote(first), shellQuote(git))
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	check := func(name string, calls []time.Duration) {
		t.Helper()
		if len(calls) != 2 || calls[0] >= repositoryGitTimeout/2 || calls[0]+calls[1] < repositoryGitTimeout {
			t.Fatalf("%s: call times %v", name, calls)
		}
	}

	status, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register",
		Registration{Family: "deepseek", ID: "synthetic-timed-discovery", Directory: repo, Repository: repo})
	details, _ := reply["details"].(map[string]any)
	ms, _ := details["calls_ms"].([]any)
	if status != http.StatusServiceUnavailable || reply["code"] != "repository_git_timeout" || details["limit_ms"] != float64(1000) || len(ms) != 2 {
		t.Fatalf("registration timeout: status=%d reply=%v", status, reply)
	}
	calls := []time.Duration{}
	for _, value := range ms {
		calls = append(calls, time.Duration(value.(float64))*time.Millisecond)
	}
	check("registration", calls)

	os.Remove(first)
	_, err = Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register",
		Registration{Family: "deepseek", ID: "synthetic-timed-discovery", Directory: repo, Repository: repo})
	if err == nil || !strings.Contains(err.Error(), "repository_git_timeout") || !strings.Contains(err.Error(), `"calls_ms":[`) {
		t.Fatalf("client error lost the call times: %v", err)
	}

	// The second call of a checkout lookup is Git too: its timeout is not a refusal of the
	// directory.
	os.Remove(first)
	_, err = CheckoutResource(context.Background(), repo)
	var timeout RepositoryGitTimeout
	if !errors.As(err, &timeout) {
		t.Fatalf("checkout timeout: %v", err)
	}
	check("checkout", timeout.Calls)
}

func TestRegistrationReportsGitExecutionFailures(t *testing.T) {
	scaleRepositoryGitTimeout(t, 500*time.Millisecond)
	for _, test := range []struct {
		name, script, want string
	}{
		{"missing", "", "repository_git_unavailable"},
		{"cannot-execute", "#!/synthetic-missing-interpreter\n", "repository_git_unavailable"},
		{"timeout", "#!/bin/sh\necho synthetic-private-stderr >&2\nexec /bin/sleep 30\n", "repository_git_timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := testRepo(t)
			d, root := startTestDaemon(t)
			secret, err := ReadSecret(root)
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			if test.script != "" {
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte(test.script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			registration := Registration{Family: "deepseek", ID: "synthetic-discovery", Directory: repo, Repository: repo}
			status, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register", registration)
			if status != http.StatusServiceUnavailable || reply["code"] != test.want {
				t.Fatalf("discovery failure: status=%d reply=%v", status, reply)
			}
			encoded, _ := json.Marshal(reply)
			for _, private := range []string{repo, bin, "synthetic-private-stderr"} {
				if strings.Contains(string(encoded), private) {
					t.Fatal("discovery response includes command or path content")
				}
			}
			counts, err := d.store.Counts(context.Background())
			if err != nil || counts["total"] != 0 {
				t.Fatalf("failed registration left a session: %v %v", counts, err)
			}
			// Malformed input still fails validation before attempting Git.
			registration.Repository = "relative"
			status, reply = post(t, d.Addresses()[0], secret, "/v1/sessions/register", registration)
			if status != http.StatusBadRequest || reply["code"] != "invalid_request" {
				t.Fatalf("invalid input changed: status=%d reply=%v", status, reply)
			}
			if test.name == "missing" {
				registration.Repository = repo
				_, err := Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", registration)
				var refused RefusedError
				if !errors.As(err, &refused) || refused.Code != test.want || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "refused") {
					t.Fatalf("CLI client lost cause: %v", err)
				}
			}
		})
	}
}

func TestNonRepositoryRemainsInvalidInput(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	status, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register",
		Registration{Family: "deepseek", ID: "synthetic-not-repo", Directory: directory, Repository: directory})
	if status != http.StatusBadRequest || reply["code"] != "invalid_request" {
		t.Fatalf("non-repository validation changed: status=%d reply=%v", status, reply)
	}
}

func TestRegistrationReportsSecondGitDiscoveryTimeout(t *testing.T) {
	scaleRepositoryGitTimeout(t, 500*time.Millisecond)
	repo := testRepo(t)
	directory := filepath.Join(repo, "child")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$2\" = %s ]; then\n printf '%%s\\n' %s\n exit 0\nfi\nexec /bin/sleep 30\n",
		shellQuote(repo), shellQuote(filepath.Join(repo, ".git")))
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	status, reply := post(t, d.Addresses()[0], secret, "/v1/sessions/register",
		Registration{Family: "deepseek", ID: "synthetic-second-discovery", Repository: repo, Directory: directory})
	if status != http.StatusServiceUnavailable || reply["code"] != "repository_git_timeout" {
		t.Fatalf("second lookup lost cause: status=%d reply=%v", status, reply)
	}
	counts, err := d.store.Counts(context.Background())
	if err != nil || counts["total"] != 0 {
		t.Fatalf("second lookup left a session: %v %v", counts, err)
	}
}
