package main

import (
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
	"github.com/rwcii/koinon/internal/platform"
	"github.com/rwcii/koinon/internal/signed"
)

// maintainerKeyCommand runs koinon maintainer key add --statement FILE, list [--all] and
// remove KEY_ID (#693; PDR 011 Decision 6). Keys are added and removed with this computer's
// own login, never across the link.
func maintainerKeyCommand(ctx context.Context, args []string, out io.Writer) error {
	usage := usageError{"invalid_request", "use koinon maintainer key add --statement FILE, key list [--all] or key remove KEY_ID"}
	if len(args) == 0 || args[0] != "add" && args[0] != "list" && args[0] != "remove" {
		return usage
	}
	action, rest := args[0], args[1:]
	keyID := ""
	if action == "remove" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		keyID, rest = rest[0], rest[1:]
	}
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("maintainer key", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := addressFlag(flags)
	statement := flags.String("statement", "", "the enrollment statement file that the maintainer client wrote")
	all := flags.Bool("all", false, "also list removed authorizations")
	if err := flags.Parse(rest); err != nil {
		return errors.New("invalid command options; use koinon --help")
	}
	if action == "remove" && keyID == "" && flags.NArg() == 1 {
		keyID = flags.Arg(0)
	} else if flags.NArg() != 0 {
		return usage
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	switch {
	case action == "add" && *statement == "":
		return usageError{"invalid_request", "give the enrollment statement with --statement FILE"}
	case action != "add" && given["statement"], action != "list" && given["all"]:
		return usage
	case action == "remove" && keyID == "":
		return usageError{"invalid_request", "give the key ID to remove, as koinon maintainer key list shows it"}
	}
	if err := resolveAddress(flags); err != nil {
		return err
	}
	request := map[string]any{"action": action, "key_id": keyID, "all": *all}
	if action == "add" {
		data, err := readStatement(*statement)
		if err != nil {
			return err
		}
		request["statement"] = data
	}
	bearer, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	data, err := core.Call(ctx, *address, bearer, "/v1/maintainer/keys", request)
	if err != nil {
		return err
	}
	var reply struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return errors.New("invalid daemon response")
	}
	if action == "list" {
		var listed struct {
			Keys []core.MaintainerKey `json:"keys"`
		}
		if err := json.Unmarshal(reply.Result, &listed); err != nil {
			return errors.New("invalid daemon response")
		}
		if len(listed.Keys) == 0 {
			_, err = fmt.Fprintln(out, "No maintainer key is authorized on this computer.")
			return err
		}
		for _, k := range listed.Keys {
			line := fmt.Sprintf("%s  %s (%s)  added %s by %s", k.KeyID, k.User, k.Client, stamp(k.AddedAt), k.AddedBy)
			if k.RemovedAt != 0 {
				line += "  removed " + stamp(k.RemovedAt)
			}
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
		return nil
	}
	var key core.MaintainerKey
	if err := json.Unmarshal(reply.Result, &key); err != nil {
		return errors.New("invalid daemon response")
	}
	if action == "add" {
		_, err = fmt.Fprintf(out, "Key %s is authorized for %s (%s) on this computer.\nAuthorization: %s\n", key.KeyID, key.User, key.Client, key.Authorization)
		return err
	}
	_, err = fmt.Fprintf(out, "Key %s of %s (%s) is removed; this computer refuses its signed actions from now on.\n", key.KeyID, key.User, key.Client)
	return err
}

// readStatement reads an enrollment statement file, at most one envelope's size.
func readStatement(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", usageError{"invalid_request", "cannot read the statement file: " + err.Error()}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, signed.MaxEnvelopeBytes+1))
	if err != nil {
		return "", usageError{"invalid_request", "cannot read the statement file: " + err.Error()}
	}
	if len(data) > signed.MaxEnvelopeBytes {
		return "", usageError{"invalid_request", "the statement file is larger than one envelope"}
	}
	return string(data), nil
}

// stamp formats Unix milliseconds as UTC RFC 3339.
func stamp(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }
