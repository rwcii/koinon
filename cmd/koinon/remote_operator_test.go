package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/remote"
)

// grantableCLIAdapter refuses each Serve write as tailscaled does on Linux until the
// account is the operator; stayDenied keeps refusing after the grant.
type grantableCLIAdapter struct {
	remoteCLIAdapter
	mu         sync.Mutex
	operator   bool
	stayDenied bool
}

func (a *grantableCLIAdapter) Run(ctx context.Context, args ...string) ([]byte, error) {
	a.mu.Lock()
	allowed := a.operator && !a.stayDenied
	a.mu.Unlock()
	if len(args) == 5 && args[3] == "--set-path=/" && !allowed {
		return nil, &remote.Diagnostic{Code: remote.OperatorRequired, Next: deniedCLINext}
	}
	return a.remoteCLIAdapter.Run(ctx, args...)
}

const operatorQuestion = "Grant synthetic-user control of this computer's Tailscale settings, including more than Serve, by running sudo tailscale set --operator=synthetic-user? [y/N] "

// operatorCase runs koinon remote enable against a daemon whose Tailscale refuses the
// account, with answer typed at a synthetic terminal and a fake grant command.
type operatorCase struct {
	answer     io.Reader
	goos       string
	account    string
	noTerminal bool
	json       bool
	grantErr   error
	stayDenied bool
	ctx        context.Context
}

type operatorResult struct {
	err     error
	out     string
	tell    string
	grants  [][]string
	adapter *grantableCLIAdapter
}

func runOperatorCase(t *testing.T, c operatorCase) operatorResult {
	t.Helper()
	adapter := &grantableCLIAdapter{stayDenied: c.stayDenied}
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}, Tailscale: adapter, SecretCost: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address := d.Addresses()[0]
	var out bytes.Buffer
	if err := run(context.Background(), []string{"maintainer", "secret", "set", "--state-dir", root, "--address", address}, strings.NewReader("synthetic-long-secret\n"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	_, slave := openPTY(t)
	tell, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer tell.Close()
	var grants [][]string
	goos, account, term, runCmd := operatorGOOS, operatorAccount, operatorTerminal, operatorRun
	t.Cleanup(func() { operatorGOOS, operatorAccount, operatorTerminal, operatorRun = goos, account, term, runCmd })
	operatorGOOS = c.goos
	operatorAccount = func() string { return c.account }
	operatorTerminal = func(ctx context.Context, in io.Reader) *terminal {
		if c.noTerminal {
			return openTerminal(ctx, in, tell)
		}
		return &terminal{ctx: ctx, file: slave, in: bufio.NewReader(in), tell: tell}
	}
	operatorRun = func(cmd *exec.Cmd) error {
		grants = append(grants, cmd.Args)
		if cmd.Path == "" || cmd.Stdin != slave || cmd.Stdout != tell || cmd.Stderr != tell || cmd.Dir != "" || cmd.Env != nil {
			t.Errorf("grant command: path %q, stdin %v, stdout %v, stderr %v, dir %q, env %v", cmd.Path, cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Dir, cmd.Env)
		}
		if c.grantErr != nil {
			return c.grantErr
		}
		adapter.mu.Lock()
		adapter.operator = true
		adapter.mu.Unlock()
		return nil
	}
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	args := []string{"remote", "enable", "--state-dir", root, "--address", address}
	if c.json {
		args = append(args, "--json")
	}
	err = run(ctx, args, c.answer, &out)
	told, _ := os.ReadFile(tell.Name())
	return operatorResult{err: err, out: out.String(), tell: string(told), grants: grants, adapter: adapter}
}

func refusedWithManualStep(err error) bool {
	return err != nil && strings.Contains(err.Error(), remote.OperatorRequired) && strings.Contains(err.Error(), deniedCLINext)
}

var grantArgs = []string{"sudo", "tailscale", "set", "--operator=synthetic-user"}

// TAILSCALE-AUTO-656: at a Linux terminal, enable asks once; yes runs the fixed sudo command
// and enables through the daemon once more.
func TestRemoteOperatorGrantAtTheTerminal(t *testing.T) {
	for _, answer := range []string{"y\n", "YES\n", " yes \r\n"} {
		r := runOperatorCase(t, operatorCase{answer: strings.NewReader(answer), goos: "linux", account: "synthetic-user"})
		if r.err != nil || !strings.Contains(r.out, "https://board.example.ts.net/dashboard/") || !strings.Contains(r.out, "Serve: ready") {
			t.Fatalf("%q: %v %s", answer, r.err, r.out)
		}
		if r.tell != operatorQuestion || len(r.grants) != 1 || !slices.Equal(r.grants[0], grantArgs) {
			t.Fatalf("%q: told %q, grants %v", answer, r.tell, r.grants)
		}
		if strings.Contains(r.out, "Grant ") || strings.Contains(r.out, "synthetic-long-secret") {
			t.Fatalf("%q: the question or the secret reached stdout: %s", answer, r.out)
		}
	}
}

// No, an empty answer, another answer and the end of input, also after an unterminated yes,
// keep the refusal and its manual step, with no grant.
func TestRemoteOperatorDeclined(t *testing.T) {
	for _, answer := range []string{"n\n", "no\n", "\n", "maybe\n", "", "y", "yes"} {
		r := runOperatorCase(t, operatorCase{answer: strings.NewReader(answer), goos: "linux", account: "synthetic-user"})
		if !refusedWithManualStep(r.err) || len(r.grants) != 0 || !strings.HasPrefix(r.tell, operatorQuestion) || strings.Count(r.tell, "Grant ") != 1 {
			t.Fatalf("%q: %v, told %q, grants %v", answer, r.err, r.tell, r.grants)
		}
	}
}

// A failed sudo names itself and keeps the refusal; a refusal after the grant is returned
// without a second question or grant.
func TestRemoteOperatorGrantFailures(t *testing.T) {
	r := runOperatorCase(t, operatorCase{answer: strings.NewReader("y\n"), goos: "linux", account: "synthetic-user", grantErr: errors.New("exit status 1")})
	if !refusedWithManualStep(r.err) || len(r.grants) != 1 || r.tell != operatorQuestion+"sudo tailscale set --operator=synthetic-user did not complete: exit status 1\n" {
		t.Fatalf("sudo failure: %v, told %q, grants %v", r.err, r.tell, r.grants)
	}
	r = runOperatorCase(t, operatorCase{answer: strings.NewReader("y\ny\n"), goos: "linux", account: "synthetic-user", stayDenied: true})
	if !refusedWithManualStep(r.err) || len(r.grants) != 1 || r.tell != operatorQuestion {
		t.Fatalf("second refusal: %v, told %q, grants %v", r.err, r.tell, r.grants)
	}
}

// blockingInput ends the context at the first read and then never answers, as Ctrl-C at the
// question does.
type blockingInput struct{ cancel context.CancelFunc }

func (b blockingInput) Read([]byte) (int, error) {
	b.cancel()
	select {}
}

func TestRemoteOperatorInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := runOperatorCase(t, operatorCase{answer: blockingInput{cancel}, goos: "linux", account: "synthetic-user", ctx: ctx})
	if !errors.Is(r.err, context.Canceled) || len(r.grants) != 0 {
		t.Fatalf("interrupted: %v, grants %v", r.err, r.grants)
	}
}

// Another platform, JSON output, no terminal and an account without a plain name get no
// question and no grant: the refusal and its manual step, as before.
func TestRemoteOperatorNotAsked(t *testing.T) {
	for name, c := range map[string]operatorCase{
		"darwin":      {goos: "darwin", account: "synthetic-user"},
		"json":        {goos: "linux", account: "synthetic-user", json: true},
		"no terminal": {goos: "linux", account: "synthetic-user", noTerminal: true},
		"no name":     {goos: "linux", account: ""},
	} {
		c.answer = strings.NewReader("y\n")
		r := runOperatorCase(t, c)
		if !refusedWithManualStep(r.err) || len(r.grants) != 0 || r.tell != "" {
			t.Fatalf("%s: %v, told %q, grants %v", name, r.err, r.tell, r.grants)
		}
	}
}

// Another refusal of enable gets no question.
func TestRemoteOperatorOtherRefusal(t *testing.T) {
	refusal := core.RefusedError{Code: "tailscale_command_failed"}
	asked := false
	goos, term := operatorGOOS, operatorTerminal
	t.Cleanup(func() { operatorGOOS, operatorTerminal = goos, term })
	operatorGOOS = "linux"
	operatorTerminal = func(context.Context, io.Reader) *terminal { asked = true; return nil }
	if err := grantOperator(context.Background(), nil, strings.NewReader("y\n"), refusal, func() error { return nil }); asked {
		t.Fatalf("other refusal: asked")
	} else if got, ok := err.(core.RefusedError); !ok || got.Code != refusal.Code {
		t.Fatalf("other refusal: %v, asked %v", err, asked)
	}
	plain := errors.New("synthetic transport failure")
	if err := grantOperator(context.Background(), nil, strings.NewReader("y\n"), plain, func() error { return nil }); err != plain || asked {
		t.Fatalf("transport failure: %v, asked %v", err, asked)
	}
}

// The grant is the fixed argument list, run without a shell.
func TestOperatorGrantCommand(t *testing.T) {
	_, slave := openPTY(t)
	tell, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer tell.Close()
	cmd := operatorGrant(context.Background(), &terminal{file: slave, tell: tell}, "synthetic-user")
	if !slices.Equal(cmd.Args, grantArgs) || filepath.Base(cmd.Path) != "sudo" && cmd.Err == nil || cmd.Stdin != slave || cmd.Stdout != tell || cmd.Stderr != tell || cmd.Env != nil {
		t.Fatalf("grant: %v %q %v", cmd.Args, cmd.Path, cmd.Err)
	}
}
