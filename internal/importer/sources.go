// Package importer reads a Python-era Koinon state tree and writes it into the Go
// daemon's database (sprint chunk 11, PROTOCOL.md "Import of Python-era state"). It
// captures every source while the Python writers are excluded, maps each source to the
// Go schema, stages the whole set in a separate database, verifies it and renames the
// staging database into place.
package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Source is one Python-era database to import.
type Source struct {
	Kind string `json:"kind"` // inbox or memory
	Path string `json:"path"`
	// An inbox's session: family, native session ID, Python peer name, and the
	// sequence through which its notifier checkpoint recorded a notice.
	Family     string `json:"family,omitempty"`
	Thread     string `json:"thread,omitempty"`
	Name       string `json:"name,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Checkpoint int64  `json:"checkpoint,omitempty"`
	// A memory store's 16-hex key and its resolved repository.
	Key        string `json:"key,omitempty"`
	Repository string `json:"repository,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

var (
	storeKey  = regexp.MustCompile(`^[0-9a-f]{16}$`)
	families  = map[string]bool{"codex": true, "deepseek": true}
	errNoTree = errors.New("source_missing: no Python-era state tree at that path")
)

// KeyOf is the Python runtime's memory store key for a Git common directory string.
func KeyOf(common string) string {
	sum := sha256.Sum256([]byte(common))
	return hex.EncodeToString(sum[:])[:16]
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, fmt.Errorf("source_invalid: %s is not valid JSON", path)
	}
	return true, nil
}

func exists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// Discover lists the importable sources under a Python state root: each Codex or
// DeepSeek session inbox, a legacy single-thread inbox, and each memory store.
func Discover(root string) ([]Source, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errNoTree
	}
	var sources []Source
	seen := map[string]string{}
	add := func(s Source) error {
		key := s.Family + "\x00" + s.Thread
		if other, ok := seen[key]; ok {
			return fmt.Errorf("source_conflict: %s and %s hold the inbox of one session", other, s.Path)
		}
		seen[key] = s.Path
		sources = append(sources, s)
		return nil
	}
	homes, _ := filepath.Glob(filepath.Join(root, "sessions", "*"))
	sort.Strings(homes)
	for _, home := range homes {
		inbox := filepath.Join(home, "inbox.sqlite3")
		if !exists(inbox) {
			continue
		}
		var record struct {
			Thread string  `json:"thread"`
			Name   string  `json:"name"`
			Repo   *string `json:"repo"`
			Agent  string  `json:"agent"`
		}
		found, err := readJSON(filepath.Join(home, "session.json"), &record)
		if err != nil {
			return nil, err
		}
		if !found || record.Thread == "" {
			return nil, fmt.Errorf("source_invalid: inbox %s has no session record", inbox)
		}
		if record.Agent == "" {
			record.Agent = "codex"
		}
		if !families[record.Agent] {
			return nil, fmt.Errorf("source_invalid: inbox %s belongs to unsupported agent %q", inbox, record.Agent)
		}
		s := Source{Kind: "inbox", Path: inbox, Family: record.Agent, Thread: record.Thread, Name: record.Name}
		if record.Repo != nil {
			s.Repo = *record.Repo
		}
		if s.Checkpoint, err = checkpoint(home, record.Thread); err != nil {
			return nil, err
		}
		if err := add(s); err != nil {
			return nil, err
		}
	}
	if inbox := filepath.Join(root, "inbox.sqlite3"); exists(inbox) {
		// A legacy single-thread installation used the state root as its bridge directory;
		// its notifier checkpoint names the Codex thread that owns it.
		var cursor struct {
			Thread string `json:"thread"`
		}
		if _, err := readJSON(filepath.Join(root, "notify-cursor.json"), &cursor); err != nil {
			return nil, err
		}
		if cursor.Thread == "" {
			return nil, fmt.Errorf("legacy_inbox_unowned: %s has no notifier checkpoint naming its thread", inbox)
		}
		s := Source{Kind: "inbox", Path: inbox, Family: "codex", Thread: cursor.Thread}
		if s.Checkpoint, err = checkpoint(root, cursor.Thread); err != nil {
			return nil, err
		}
		if err := add(s); err != nil {
			return nil, err
		}
	}
	stores, _ := filepath.Glob(filepath.Join(root, "memory", "*", "memory.sqlite3"))
	sort.Strings(stores)
	for _, path := range stores {
		key := filepath.Base(filepath.Dir(path))
		if !storeKey.MatchString(key) || !exists(path) {
			return nil, fmt.Errorf("source_invalid: %s is not a memory store", path)
		}
		sources = append(sources, Source{Kind: "memory", Path: path, Key: key})
	}
	return sources, nil
}

// checkpoint is the sequence a notifier checkpoint records as notified for thread.
func checkpoint(home, thread string) (int64, error) {
	var cursor struct {
		Thread  string `json:"thread"`
		Through int64  `json:"through"`
	}
	found, err := readJSON(filepath.Join(home, "notify-cursor.json"), &cursor)
	if err != nil || !found || cursor.Thread != thread || cursor.Through < 0 {
		return 0, err
	}
	return cursor.Through, nil
}
