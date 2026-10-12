package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/libraryclient"
	"github.com/rwcii/koinon/internal/platform"
)

func libraryStateCommand(ctx context.Context, args []string, out io.Writer) error {
	op, rest := args[0], args[1:]
	root, err := platform.DefaultStateDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("library "+op, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state-dir", root, "private Go state directory")
	address := flags.String("address", "127.0.0.1:47671", "daemon loopback address")
	as := flags.String("as", "", "calling native session as FAMILY:ID")
	scope := flags.String("scope", "machine", "machine or project inspection scope")
	directory := flags.String("directory", "", "listed worktree of the registered repository")
	choice := flags.String("decision", "", "reported adopted or declined outcome")
	evidence := flags.String("evidence", "", "inert exact-change evidence reference")
	catalogDigest := flags.String("catalog-digest", "", "catalog digest from the reviewed state")
	itemDigest := flags.String("item-digest", "", "item digest from the reviewed catalog")
	compared := flags.Int64("if-revision", -1, "current machine decision revision; zero if absent")
	before := flags.String("pre-edit", "[]", "JSON typed target digests before the change")
	result := flags.String("result", "[]", "JSON typed target digests after the change")
	ids := []string{}
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		ids = append(ids, rest[0])
		rest = rest[1:]
	}
	if err := flags.Parse(rest); err != nil {
		return usageError{"invalid_request", err.Error()}
	}
	ids = append(ids, flags.Args()...)
	family, id, ok := strings.Cut(*as, ":")
	if !ok || family == "" || id == "" {
		return usageError{"invalid_request", "--as must name the calling native session as FAMILY:ID"}
	}
	secret, err := core.ClientSecret(*state, *address)
	if err != nil {
		return err
	}
	client := libraryclient.Client{Caller: core.Key{Family: family, ID: id}, Getenv: os.Getenv, Call: func(ctx context.Context, path string, body any) (json.RawMessage, error) {
		return core.Call(ctx, *address, secret, path, body)
	}}
	var value any
	if op == "state" {
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "decision" || f.Name == "evidence" || f.Name == "catalog-digest" || f.Name == "item-digest" || f.Name == "if-revision" || f.Name == "pre-edit" || f.Name == "result" {
				err = usageError{"invalid_request", "decision option supplied to state"}
			}
		})
		if err != nil {
			return err
		}
		value, err = client.State(ctx, libraryclient.StateRequest{Scope: *scope, Directory: *directory, ItemIDs: ids})
	} else {
		if len(ids) != 1 || *compared < 0 || *scope != "machine" || *directory != "" {
			return usageError{"invalid_request", "machine decision needs one ITEM_ID and --if-revision; project receipts use memory record"}
		}
		r := libraryclient.DecisionRequest{ItemID: ids[0], Choice: *choice, Evidence: *evidence, CatalogDigest: *catalogDigest, ItemDigest: *itemDigest, Compared: *compared}
		if json.Unmarshal([]byte(*before), &r.Before) != nil || json.Unmarshal([]byte(*result), &r.Result) != nil {
			return usageError{"invalid_request", "pre-edit and result must be typed target digest arrays"}
		}
		value, err = client.Decision(ctx, r)
	}
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"ok": true, "library": value})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
