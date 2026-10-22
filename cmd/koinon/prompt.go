package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rwcii/koinon/internal/platform"
)

// terminalAsk returns a yes-or-no question for the person at the terminal, written to tell,
// or nil when in or tell is not a terminal: scripts and agents get no question (#449).
func terminalAsk(in io.Reader, tell *os.File) func(string) (bool, error) {
	file, ok := in.(*os.File)
	if !ok || !platform.IsTerminal(int(file.Fd())) || !platform.IsTerminal(int(tell.Fd())) {
		return nil
	}
	return ask(bufio.NewReader(in), tell)
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
