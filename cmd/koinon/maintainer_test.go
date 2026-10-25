package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/platform"
)

// koinon maintainer secret (PDR 009, Decision 1): standard input, the terminal, and the
// daemon's replies.
func TestMaintainerSecretCommand(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	command := func(input string, args ...string) (string, error) {
		var out bytes.Buffer
		err := run(context.Background(), append(append([]string{"maintainer"}, args...), "--state-dir", root, "--address", d.Addresses()[0]),
			strings.NewReader(input), &out)
		return out.String(), err
	}
	const secret = "  a secret with spaces  "
	if _, err := command("", "secret", "set", secret); err == nil {
		t.Fatal("a secret as an argument was accepted")
	}
	for _, input := range []string{"", "short\n", strings.Repeat("a", 1025) + "\n", "twelve chars\xff\n"} {
		if out, err := command(input, "secret", "set"); err == nil || strings.Contains(err.Error(), strings.TrimSpace(input)) && len(input) > 1 {
			t.Fatalf("set %q: %q %v", input, out, err)
		}
	}
	if _, err := command("", "secret", "rotate"); err == nil {
		t.Fatal("an unknown action was accepted")
	}
	out, err := command(secret+"\r\nnext line\n", "secret", "set")
	if err != nil || out != "The maintainer secret is set. Every dashboard login has ended.\n" {
		t.Fatalf("set: %q %v", out, err)
	}
	out, err = command("", "secret", "generate")
	if err != nil || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}\n$`).MatchString(out) {
		t.Fatalf("generate: %q %v", out, err)
	}
	if out, err = command("", "secret", "clear"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("clear: %q %v", out, err)
	}
	if _, err = command("", "secret", "clear"); err == nil || !strings.Contains(err.Error(), "no maintainer secret is set") {
		t.Fatalf("second clear: %v", err)
	}
}

// waitNoEcho waits until the terminal stops echoing, which readNewSecret does before its
// first prompt.
func waitNoEcho(t *testing.T, tty *os.File) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		echo, err := platform.Echoes(int(tty.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		if !echo {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("echo stayed on")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReadNewSecretTerminal(t *testing.T) {
	for _, c := range []struct {
		name, input, want string
		fails             bool
	}{
		{"match", "twelve chars ok\ntwelve chars ok\n", "twelve chars ok", false},
		{"mismatch", "twelve chars ok\ntwelve chars no\n", "", true},
		{"end of input", "\x04", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			master, slave := openPTY(t)
			go func() {
				// Drain the prompts, so that the terminal never blocks on output.
				buf := make([]byte, 256)
				for {
					if _, err := master.Read(buf); err != nil {
						return
					}
				}
			}()
			type result struct {
				secret string
				err    error
			}
			done := make(chan result, 1)
			go func() {
				secret, err := readNewSecret(slave, slave)
				done <- result{secret, err}
			}()
			waitNoEcho(t, slave)
			master.Write([]byte(c.input))
			r := <-done
			if c.fails != (r.err != nil) || r.secret != c.want {
				t.Fatalf("got %q %v", r.secret, r.err)
			}
			if r.err != nil && strings.Contains(r.err.Error(), "twelve") {
				t.Fatalf("the error repeats the secret: %v", r.err)
			}
			if echo, err := platform.Echoes(int(slave.Fd())); err != nil || !echo {
				t.Fatalf("echo not restored: %v %v", echo, err)
			}
		})
	}
}

// TestReadNewSecretSignal runs readNewSecret in a child on a terminal and stops it with
// SIGINT or SIGTERM: the terminal echoes again, and the child ends by the signal.
func TestReadNewSecretSignal(t *testing.T) {
	if os.Getenv("KOINON_TEST_SECRET_PROMPT") == "1" {
		readNewSecret(os.Stdin, os.Stderr)
		os.Exit(3)
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			master, slave := openPTY(t)
			go func() {
				buf := make([]byte, 256)
				for {
					if _, err := master.Read(buf); err != nil {
						return
					}
				}
			}()
			child := exec.Command(os.Args[0], "-test.run=^TestReadNewSecretSignal$")
			child.Env = append(os.Environ(), "KOINON_TEST_SECRET_PROMPT=1")
			child.Stdin, child.Stdout, child.Stderr = slave, slave, slave
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waitNoEcho(t, slave)
			// The prompt is written after echo goes off and the handler is in place.
			time.Sleep(200 * time.Millisecond)
			child.Process.Signal(sig)
			err := child.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("child: %v", err)
			}
			status, ok := exit.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != sig {
				t.Fatalf("child ended with %v", err)
			}
			if echo, err := platform.Echoes(int(slave.Fd())); err != nil || !echo {
				t.Fatalf("echo not restored after %v: %v %v", sig, echo, err)
			}
		})
	}
}
