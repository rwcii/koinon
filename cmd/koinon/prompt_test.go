package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsk(t *testing.T) {
	for _, c := range []struct {
		input   string
		answers []bool
		prompts int
	}{
		{"\n", []bool{true}, 1},
		{"y\nYES\n", []bool{true, true}, 2},
		{"n\nNo\n", []bool{false, false}, 2},
		{"maybe\nn\n", []bool{false}, 2},
		{"", []bool{false, false}, 1},
		{"y", []bool{true, false}, 1},
	} {
		var told bytes.Buffer
		question := ask(bufio.NewReader(strings.NewReader(c.input)), &told)
		for i, want := range c.answers {
			if got, err := question("Q?"); err != nil || got != want {
				t.Fatalf("%q answer %d: %v %v", c.input, i, got, err)
			}
		}
		if n := strings.Count(told.String(), "Q? [Y/n] "); n != c.prompts {
			t.Fatalf("%q: %d prompts", c.input, n)
		}
	}
}

func TestTerminalAskNeedsTerminals(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if terminalAsk(r, w) != nil || terminalAsk(strings.NewReader("y\n"), w) != nil {
		t.Fatal("a pipe or a buffer was taken for a terminal")
	}
}

// One buffered reader serves the yes-or-no, line and secret questions of install, so an
// answer that the person typed ahead stays for its own question (#624 f).
func TestTerminalSharedReader(t *testing.T) {
	_, slave := openPTY(t)
	tell, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer tell.Close()
	in := bufio.NewReader(strings.NewReader("2\r\ntwelve chars ok\ntwelve chars ok\nn\nlast"))
	term := &terminal{ctx: context.Background(), file: slave, in: in, tell: tell}
	if got, err := term.line("Choice [1]: "); err != nil || got != "2" {
		t.Fatalf("line %q %v", got, err)
	}
	if got, err := term.newSecret(); err != nil || got != "twelve chars ok" {
		t.Fatalf("secret %q %v", got, err)
	}
	if yes, err := ask(term.in, tell)("Set up Koinon for codex?"); err != nil || yes {
		t.Fatalf("ask %v %v", yes, err)
	}
	// An answer that the end of the input cut off is no answer.
	if got, err := term.line("Port [8443]: "); err != io.EOF || got != "" {
		t.Fatalf("ended line %q %v", got, err)
	}
	if openTerminal(context.Background(), strings.NewReader("1\n"), tell) != nil || openTerminal(context.Background(), slave, tell) != nil {
		t.Fatal("a buffer or a file for the prompts was taken for a terminal")
	}
}

// Ctrl-C ends the command's context; a waiting question then ends at once, without an answer.
func TestTerminalLineEndsWithContext(t *testing.T) {
	_, slave := openPTY(t)
	tell, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer tell.Close()
	ctx, cancel := context.WithCancel(context.Background())
	term := &terminal{ctx: ctx, file: slave, in: bufio.NewReader(slave), tell: tell}
	cancel()
	if got, err := term.line("Choice [1]: "); err != context.Canceled || got != "" {
		t.Fatalf("line %q %v", got, err)
	}
}
