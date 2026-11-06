package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rwcii/koinon/internal/platform"
)

// terminalAsk returns a yes-or-no question for the person at the terminal, written to tell,
// or nil when in or tell is not a terminal: scripts and agents get no question (#449).
func terminalAsk(in io.Reader, tell *os.File) func(string) (bool, error) {
	t := openTerminal(context.Background(), in, tell)
	if t == nil {
		return nil
	}
	return ask(t.in, tell)
}

// terminal is the person at the terminal. Its one buffered reader serves every question of
// a command, so no answer that the person typed ahead is lost between them (#624).
type terminal struct {
	ctx  context.Context
	file *os.File
	in   *bufio.Reader
	tell *os.File
}

// openTerminal returns the terminal, or nil when in or tell is not a terminal. The end of
// ctx, as after Ctrl-C, ends a question at once.
func openTerminal(ctx context.Context, in io.Reader, tell *os.File) *terminal {
	file, ok := in.(*os.File)
	if !ok || !platform.IsTerminal(int(file.Fd())) || !platform.IsTerminal(int(tell.Fd())) {
		return nil
	}
	return &terminal{ctx: ctx, file: file, in: bufio.NewReader(file), tell: tell}
}

// line shows prompt and reads one answer. It returns io.EOF after the input ends, also for
// an answer that the end cut off, and the context's error when it ends first: the command then
// ends, and no later question reads the input.
func (t *terminal) line(prompt string) (string, error) {
	fmt.Fprint(t.tell, prompt)
	type answer struct {
		line string
		err  error
	}
	read := make(chan answer, 1)
	go func() {
		line, err := t.in.ReadString('\n')
		read <- answer{line, err}
	}()
	select {
	case <-t.ctx.Done():
		fmt.Fprintln(t.tell)
		return "", t.ctx.Err()
	case a := <-read:
		if a.err != nil {
			fmt.Fprintln(t.tell)
			return "", a.err
		}
		return strings.TrimSuffix(strings.TrimSuffix(a.line, "\n"), "\r"), nil
	}
}

// newSecret reads a new maintainer secret, typed twice with echo off.
func (t *terminal) newSecret() (string, error) {
	return readNewSecretFrom(int(t.file.Fd()), t.in, t.tell)
}

// ask reads one answer per question: an empty answer or y/yes is yes, n/no is no, and
// another answer asks again. After the input ends, every answer is no.
func ask(in *bufio.Reader, tell io.Writer) func(string) (bool, error) {
	ended := false
	return func(question string) (bool, error) {
		for !ended {
			fmt.Fprint(tell, question+" [Y/n] ")
			line, err := in.ReadString('\n')
			if err != nil {
				ended = true
				fmt.Fprintln(tell)
				if err != io.EOF {
					return false, err
				}
			}
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "", "y", "yes":
				if !ended || strings.TrimSpace(line) != "" {
					return true, nil
				}
			case "n", "no":
				return false, nil
			}
		}
		return false, nil
	}
}
