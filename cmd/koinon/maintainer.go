package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/platform"
)

// maintainerCommand runs koinon maintainer secret set|generate|clear (PDR 009, Decision 1).
// A secret is never an argument: set reads it from the terminal with echo off, or from
// the first line of standard input.
func maintainerCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) < 2 || args[0] != "secret" || args[1] != "set" && args[1] != "generate" && args[1] != "clear" {
		return usageError{"invalid_request", "use koinon maintainer secret set, generate or clear"}
	}
	action := args[1]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("maintainer", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return errors.New("invalid command options; use koinon --help")
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	secret := ""
	if action == "set" {
		if secret, err = readNewSecret(in, os.Stderr); err != nil {
			return err
		}
		if !core.ValidMaintainerSecret(secret) {
			return usageError{"invalid_request", "a maintainer secret is valid UTF-8 with at least 12 characters and at most 1024 bytes"}
		}
	}
	data, err := core.Call(ctx, *address, bearer, "/v1/maintainer/secret", map[string]any{"action": action, "secret": secret})
	if err != nil {
		return err
	}
	var reply struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return errors.New("invalid daemon response")
	}
	switch action {
	case "generate":
		// Only the secret, so that a script can keep it; it is not shown again.
		_, err = fmt.Fprintln(out, reply.Secret)
	case "set":
		_, err = fmt.Fprintln(out, "The maintainer secret is set. Every dashboard login has ended.")
	default:
		_, err = fmt.Fprintln(out, "The maintainer secret is removed. Every dashboard login has ended; koinon dashboard gives a login link.")
	}
	return err
}

// readNewSecret reads a new secret: from a terminal twice with echo off, with the prompts on
// tell, also when tell is redirected; else the first line of in. The terminal gets its
// settings back after an error, a mismatch, SIGINT or SIGTERM.
func readNewSecret(in io.Reader, tell *os.File) (string, error) {
	file, ok := in.(*os.File)
	if !ok || !platform.IsTerminal(int(file.Fd())) {
		return core.ReadSecretLine(in)
	}
	restore, err := platform.NoEcho(int(file.Fd()))
	if err != nil {
		return "", err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	defer func() {
		signal.Stop(signals)
		close(done)
		restore()
	}()
	go func() {
		select {
		case s := <-signals:
			// Restore, then end with the signal's own status.
			restore()
			fmt.Fprintln(tell)
			signal.Reset(s)
			syscall.Kill(os.Getpid(), s.(syscall.Signal))
		case <-done:
		}
	}()
	var entries [2]string
	for i, prompt := range []string{"New maintainer secret: ", "Repeat it: "} {
		fmt.Fprint(tell, prompt)
		entries[i], err = core.ReadSecretLine(file)
		fmt.Fprintln(tell)
		if err != nil {
			// The rest of a refused line can stay in the terminal's input; discard it, so that
			// the next program does not get it.
			platform.FlushInput(int(file.Fd()))
			return "", err
		}
	}
	if entries[0] != entries[1] {
		return "", usageError{"invalid_request", "the two entries differ; nothing was changed"}
	}
	return entries[0], nil
}

// maintainerFlags are the options of koinon mcp --maintainer (PDR 009, Decision 3).
type maintainerFlags struct {
	on, stdin    *bool
	client, file *string
}

func (m *maintainerFlags) add(flags *flag.FlagSet) {
	m.on = flags.Bool("maintainer", false, "act as the maintainer with the maintainer secret")
	m.client = flags.String("client", "", "the maintainer client's name, recorded as via mcp:NAME")
	m.stdin = flags.Bool("secret-stdin", false, "read the maintainer secret from the first line of standard input")
	m.file = flags.String("secret-file", "", "read the maintainer secret from the first line of this private file")
}

// used reports whether any maintainer-mode option was given.
func (m maintainerFlags) used(flags *flag.FlagSet) bool {
	used := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "maintainer", "client", "secret-stdin", "secret-file":
			used = true
		}
	})
	return used
}

// maintainerMCP runs koinon mcp --maintainer: it reads the secret, from the first line of
// standard input before the MCP stream or from a private file, exchanges it once for a
// maintainer token and then serves the maintainer tools. A refusal ends the command before
// any MCP output. It registers no agent session.
func maintainerMCP(ctx context.Context, state, address string, resolve func() (string, error), m maintainerFlags, flags *flag.FlagSet, in io.Reader, out io.Writer) error {
	fileGiven := false
	flags.Visit(func(f *flag.Flag) { fileGiven = fileGiven || f.Name == "secret-file" })
	switch {
	case !*m.on:
		return usageError{"invalid_request", "--client, --secret-stdin and --secret-file are options of koinon mcp --maintainer"}
	case !core.MaintainerClientName.MatchString(*m.client):
		return usageError{"invalid_request", "--client NAME is 1 to 32 characters of a-z, 0-9 and -, and starts with a letter or digit"}
	case *m.stdin == fileGiven:
		return usageError{"invalid_request", "give the maintainer secret with exactly one of --secret-stdin and --secret-file PATH"}
	}
	var secret string
	var err error
	if *m.stdin {
		secret, err = core.ReadSecretLine(in)
	} else {
		secret, err = readSecretFile(*m.file)
	}
	var refusal core.Refusal
	if errors.As(err, &refusal) {
		return usageError{refusal.Code, refusal.Message}
	}
	if err != nil {
		return err
	}
	token, err := mcp.MaintainerSession(ctx, state, address, secret, *m.client)
	if err != nil {
		return err
	}
	return mcp.ServeMaintainer(ctx, mcp.MaintainerConfig{StateDir: state, Address: address, Resolve: resolve, Token: token, Client: *m.client, Version: version}, in, out)
}

// readSecretFile reads the first line of a secret file: a regular file, not a symbolic link,
// owned by this user, with mode 0600 or stricter. The read is bounded as on standard input.
func readSecretFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", usageError{"invalid_request", "cannot read the secret file: " + err.Error()}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return "", usageError{"invalid_request", "the secret file is a symbolic link; give the path of the file itself"}
	case !info.Mode().IsRegular():
		return "", usageError{"invalid_request", "the secret file is not a regular file"}
	case !ok || stat.Uid != uint32(os.Geteuid()):
		return "", usageError{"invalid_request", "the secret file is owned by another user"}
	case info.Mode().Perm()&0177 != 0:
		return "", usageError{"invalid_request", fmt.Sprintf("the secret file has mode %04o; use mode 0600 or 0400", info.Mode().Perm())}
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", usageError{"invalid_request", "cannot read the secret file: " + err.Error()}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	// The opened file must be the file that was checked.
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return "", usageError{"invalid_request", "the secret file changed while it was read"}
	}
	return core.ReadSecretLine(f)
}
