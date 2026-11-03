package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rwcii/koinon/internal/core"
)

// decisionExportAttempts bounds the restarts of an export whose pages read different board
// versions.
const decisionExportAttempts = 4

// errExportChanged ends an export attempt whose pages read different board versions.
var errExportChanged = errors.New("the board changed during the export")

// decisionExport writes every decision item, live and deep-archived, as JSON lines to a new
// private file at path, outside a Git work tree (#589). The pages must all read one board
// version, so the file has each item once; a change between pages starts the export again.
// The file appears only when it is complete, and an existing file is never replaced.
func decisionExport(ctx context.Context, out io.Writer, address, secret string, caller any, path string) error {
	if path == "" {
		return usageError{"invalid_request", "koinon decision export needs the file: koinon decision export PATH"}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return usageError{"invalid_request", "the export path cannot be resolved"}
	}
	if _, err := os.Lstat(path); err == nil {
		return usageError{"export_exists", path + " exists; name a new file"}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return usageError{"invalid_request", "the export directory must exist: " + err.Error()}
	}
	// The export holds the board's private card text: it does not go into a work tree, where
	// it could be committed.
	if _, err := core.CheckoutResource(ctx, dir); err == nil {
		return usageError{"export_in_worktree", dir + " is in a Git work tree; name a file outside it"}
	}
	path = filepath.Join(dir, filepath.Base(path))
	for attempt := 1; ; attempt++ {
		records, version, err := exportAttempt(ctx, address, secret, caller, dir, path)
		if errors.Is(err, errExportChanged) && attempt < decisionExportAttempts {
			continue
		}
		if errors.Is(err, errExportChanged) {
			return fmt.Errorf("%w %d times; run the export again when the board is quiet", errExportChanged, attempt)
		}
		if err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"path": path, "records": records, "version": version}})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
}

// exportAttempt writes the pages to a private temporary file in dir and links it to path,
// which fails when path exists. The temporary file is removed in every case.
func exportAttempt(ctx context.Context, address, secret string, caller any, dir, path string) (int, string, error) {
	f, err := os.CreateTemp(dir, ".koinon-decisions-*.tmp")
	if err != nil {
		return 0, "", err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return 0, "", err
	}
	records, version := 0, ""
	at := core.DecisionExportAt{}
	for {
		data, err := core.Call(ctx, address, secret, "/v1/decisions/export", map[string]any{"caller": caller,
			"phase": at.Phase, "after_reference": at.AfterReference, "after_id": at.AfterID})
		if err != nil {
			return 0, "", err
		}
		var reply struct {
			Result core.DecisionExportPage `json:"result"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			return 0, "", err
		}
		page := reply.Result
		if version == "" {
			version = page.Version
		}
		if page.Version != version {
			return 0, "", errExportChanged
		}
		for _, record := range page.Records {
			if _, err := f.Write(append(append([]byte{}, record...), '\n')); err != nil {
				return 0, "", err
			}
			records++
		}
		if !page.More {
			break
		}
		if page.Next == nil {
			return 0, "", errors.New("the daemon gave no cursor for the next export page")
		}
		at = *page.Next
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	if err := f.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Link(temp, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, "", usageError{"export_exists", path + " was created during the export; name a new file"}
		}
		return 0, "", err
	}
	return records, version, nil
}
