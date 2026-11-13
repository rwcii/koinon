package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/mcp"
	"github.com/rwcii/koinon/internal/platform"
	qrcode "github.com/skip2/go-qrcode"
)

// The link between Koinon computers (#670, LINK-670-PAIR). status reads with the daemon's
// bearer; every change needs the maintainer secret, from --secret-file or typed at the
// terminal, which the command exchanges for a maintainer token. An invitation is never an
// argument: join reads it from the terminal or the first line of standard input.

const linkUsage = "use koinon link status|name [NAME]|invite|join|unpair PAIR|cancel PAIR|cancel invitations|rename PAIR [NAME]|queue [COMPUTER]|cancel-message LINK_ID"

// linkCallTimeout covers a join or unpair, which waits for the other computer.
const linkCallTimeout = 45 * time.Second

// linkClient is the maintainer client name that the audit records for these commands.
const linkClient = "koinon-link"

var linkStderr = os.Stderr

func linkCommand(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return usageError{"invalid_request", linkUsage}
	}
	action := args[0]
	maxArgs := map[string]int{"status": 0, "name": 1, "invite": 0, "join": 0, "unpair": 1, "cancel": 1, "rename": 2, "queue": 1, "cancel-message": 1}
	most, ok := maxArgs[action]
	if !ok {
		return usageError{"invalid_request", linkUsage}
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("link "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	asJSON := flags.Bool("json", false, "print the result as JSON")
	secretFile := flags.String("secret-file", "", "read the maintainer secret from the first line of this private file")
	port := flags.Int("port", 0, "the link port for name; 0 keeps the saved port, or the default")
	// Positional values and options can come in any order.
	var positional []string
	rest := args[1:]
	for {
		if err := flags.Parse(rest); err != nil {
			return usageError{"invalid_request", "invalid link command options; use koinon --help"}
		}
		if flags.NArg() == 0 {
			break
		}
		positional = append(positional, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	need := map[string]int{"unpair": 1, "cancel": 1, "rename": 1, "cancel-message": 1}[action]
	if len(positional) < need || len(positional) > most {
		return usageError{"invalid_request", linkUsage}
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	if given["port"] && action != "name" {
		return usageError{"invalid_request", "--port applies only to koinon link name"}
	}
	if given["secret-file"] && action == "status" {
		return usageError{"invalid_request", "koinon link status needs no secret"}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil {
		return usageError{"daemon_unavailable", "daemon unavailable: run koinon install, then repeat this command: " + err.Error()}
	}
	status := func() (core.LinkStatus, error) {
		var reply struct {
			Result core.LinkStatus `json:"result"`
		}
		data, err := core.Call(ctx, *address, bearer, "/v1/link/status", nil)
		if err == nil {
			err = json.Unmarshal(data, &reply)
		}
		return reply.Result, err
	}
	if action == "status" {
		s, err := status()
		if err != nil {
			return linkCallError(action, err)
		}
		if *asJSON {
			return printJSON(out, struct {
				OK     bool            `json:"ok"`
				Result core.LinkStatus `json:"result"`
			}{true, s})
		}
		printLinkStatus(out, s)
		return nil
	}

	t := openTerminal(ctx, in, linkStderr)
	var secret string
	switch {
	case *secretFile != "":
		secret, err = readSecretFile(*secretFile)
	case t != nil:
		var entries []string
		entries, err = readHidden(int(t.file.Fd()), t.in, t.tell, "Maintainer secret: ")
		if err == nil {
			secret = entries[0]
		}
	default:
		return usageError{"invalid_request", "give the maintainer secret with --secret-file PATH, or run the command at a terminal"}
	}
	var refusal core.Refusal
	if errors.As(err, &refusal) {
		return usageError{refusal.Code, refusal.Message}
	}
	if err != nil {
		return err
	}

	body := map[string]any{}
	switch action {
	case "name":
		name := ""
		if len(positional) == 1 {
			name = positional[0]
		} else {
			if t == nil {
				return usageError{"invalid_request", "give the name: koinon link name NAME"}
			}
			s, err := status()
			if err != nil {
				return linkCallError(action, err)
			}
			suggest := s.NameSuggestion()
			prompt := "Name for this computer"
			if s.SuggestLong != "" && s.SuggestLong != suggest {
				prompt += " (long name: " + s.SuggestLong + ")"
			}
			if name, err = t.line(prompt + " [" + suggest + "]: "); err != nil {
				return err
			}
			if strings.TrimSpace(name) == "" {
				name = suggest
			}
		}
		body["name"], body["port"] = strings.TrimSpace(name), *port
	case "join":
		var text string
		if t != nil {
			text, err = t.line("Paste the invitation: ")
		} else {
			text, err = readInvitationLine(in)
		}
		if err != nil {
			return err
		}
		body["invitation"] = strings.TrimSpace(text)
	case "unpair", "cancel":
		body["pair"] = positional[0]
	case "queue":
		if len(positional) == 1 {
			body["pair"] = positional[0]
		}
	case "cancel-message":
		body["link_id"] = positional[0]
	case "rename":
		body["pair"] = positional[0]
		if len(positional) == 2 {
			body["name"] = positional[1]
		}
	}

	token, err := mcp.MaintainerSession(ctx, *state, *address, secret, linkClient)
	if err != nil {
		return linkCallError(action, err)
	}
	body["token"] = token
	data, err := core.CallTimeout(ctx, *address, bearer, "/v1/maintainer/link-"+action, body, linkCallTimeout)
	if err != nil {
		return linkCallError(action, err)
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return err
	}
	if *asJSON {
		return printJSON(out, struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result,omitempty"`
		}{true, reply.Result})
	}
	switch action {
	case "invite":
		var r core.LinkInvitationResult
		if err := json.Unmarshal(reply.Result, &r); err != nil {
			return err
		}
		fmt.Fprintln(out, r.Invitation)
		if qr, err := qrcode.New(r.Invitation, qrcode.Low); err == nil {
			fmt.Fprintln(out, qr.ToSmallString(false))
		}
		fmt.Fprintf(out, "On the other computer, run koinon link join and paste the invitation, or paste it in the dashboard's Add a computer. It works once, for %d minutes. Keep it private.\n", r.ExpiresSeconds/60)
	case "join":
		var r core.LinkJoinResult
		if err := json.Unmarshal(reply.Result, &r); err != nil {
			return err
		}
		fmt.Fprintf(out, "Paired with %s.\n", r.Name)
	case "name":
		fmt.Fprintf(out, "This computer is %s; its agents are AGENT@%s on your other computers.\n", body["name"], body["name"])
	case "unpair":
		fmt.Fprintln(out, "Unpaired. The other computer removes the pair when it answers, or shows it as rejected at its next connection.")
	case "cancel":
		fmt.Fprintln(out, "Cancelled.")
	case "rename":
		fmt.Fprintln(out, "Renamed.")
	case "queue":
		var queues []core.LinkQueue
		if err := json.Unmarshal(reply.Result, &queues); err != nil {
			return err
		}
		printLinkQueues(out, queues)
	case "cancel-message":
		var q core.LinkQueued
		if err := json.Unmarshal(reply.Result, &q); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s to %s: %s\n", q.LinkID, q.Recipient, linkStateText(q.State))
	}
	return nil
}

// linkStateText is a queued message's state in words.
func linkStateText(state string) string {
	return map[string]string{"queued": "queued here", "received": "received there", "refused": "refused there",
		"cancelled": "cancelled", "may_have_been_delivered": "may have been delivered"}[state]
}

func printLinkQueues(out io.Writer, queues []core.LinkQueue) {
	if len(queues) == 0 {
		fmt.Fprintln(out, "Paired computers: none")
	}
	for _, q := range queues {
		line := fmt.Sprintf("%s: %d waiting, %d finished", q.Computer, q.Waiting, q.Finished)
		if q.Truncated {
			line += fmt.Sprintf(" (older finished messages not shown; %d shown)", len(q.Messages))
		}
		fmt.Fprintln(out, line)
		for _, m := range q.Messages {
			line := fmt.Sprintf("  %s  %s  %s  %d bytes  %s", m.LinkID, m.Recipient, linkStateText(m.State), m.Bytes,
				time.UnixMilli(m.Created).UTC().Format("2006-01-02 15:04:05Z"))
			if m.Reason != "" {
				line += "  " + m.Reason
			}
			fmt.Fprintln(out, line)
		}
	}
}

// readInvitationLine reads the first line of standard input, at most 4 KiB.
func readInvitationLine(in io.Reader) (string, error) {
	line, err := bufio.NewReaderSize(io.LimitReader(in, 4096), 4096).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", usageError{"invalid_request", "give the invitation on the first line of standard input"}
	}
	return line, nil
}

// linkCallError keeps the daemon's refusal and its code; any other failure means that the
// daemon did not answer.
func linkCallError(action string, err error) error {
	var refused core.RefusedError
	if errors.As(err, &refused) {
		return fmt.Errorf("link %s: %w", action, err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return usageError{"daemon_unavailable", fmt.Sprintf("daemon unavailable or too old: install this version, then repeat koinon link %s: %v", action, err)}
}

func printLinkStatus(out io.Writer, s core.LinkStatus) {
	if !s.Remote {
		fmt.Fprintln(out, "Remote access: off (run koinon remote enable first)")
	}
	if s.Name == "" {
		fmt.Fprintf(out, "This computer: not named; run koinon link name (suggested: %s)\n", s.SuggestShort)
	} else {
		fmt.Fprintf(out, "This computer: %s (agents are AGENT@%s)\n", s.Name, s.Name)
	}
	if s.Port != 0 {
		fmt.Fprintf(out, "Link: port %d, listener %s, Serve %s\n", s.Port, s.Listener, s.Serve)
	}
	if s.Fingerprint != "" {
		fmt.Fprintln(out, "Fingerprint: "+s.Fingerprint)
	}
	if s.Invitations > 0 {
		fmt.Fprintf(out, "Open invitations: %d\n", s.Invitations)
	}
	if len(s.Pairs) == 0 {
		fmt.Fprintln(out, "Paired computers: none")
	}
	for _, p := range s.Pairs {
		line := fmt.Sprintf("%s  %s", p.Name, p.Observed.State)
		if p.Observed.At != 0 {
			line += " since " + time.Unix(p.Observed.At, 0).UTC().Format("2006-01-02 15:04:05Z")
		}
		if p.Observed.Error != "" {
			line += " (" + p.Observed.Error + ")"
		}
		if p.Name != p.Advertised {
			line += "  advertises " + p.Advertised
		}
		fmt.Fprintln(out, line+"  id "+p.ID)
	}
	if s.Rejected != nil {
		fmt.Fprintf(out, "Last refused key: %s at %s\n", s.Rejected.Fingerprint, time.Unix(s.Rejected.At, 0).UTC().Format("2006-01-02 15:04:05Z"))
	}
	if s.Error != "" {
		fmt.Fprintln(out, "Error: "+s.Error)
	}
}
