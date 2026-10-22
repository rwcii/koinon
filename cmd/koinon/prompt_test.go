package main

import (
	"bufio"
	"bytes"
	"os"
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
