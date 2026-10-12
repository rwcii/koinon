package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistrationReportsGitExecutionFailures(t *testing.T) {
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
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := fmt.Sprintf("#!/bin/sh\nif [ \"$2\" = %s ]; then\n printf '%%s\\n' %s\n exit 0\nfi\nexec /bin/sleep 30\n",
		quote(repo), quote(filepath.Join(repo, ".git")))
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
