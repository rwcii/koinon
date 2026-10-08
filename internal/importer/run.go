package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
)

// Prepared is one captured and mapped source.
type Prepared struct {
	Source  Source
	Import  core.ImportSource
	Skipped Skipped
}

// SourceReport is one source's line in the import report.
type SourceReport struct {
	Path       string           `json:"path"`
	Kind       string           `json:"kind"`
	Session    string           `json:"session,omitempty"`
	Repository string           `json:"repository,omitempty"`
	Resolution string           `json:"resolution,omitempty"`
	Digest     string           `json:"digest"`
	Counts     map[string]int64 `json:"counts"`
	Skipped    Skipped          `json:"skipped,omitempty"`
	Status     string           `json:"status"`
}

// Report is the JSON result of an import or a verification.
type Report struct {
	Result  string         `json:"result"`
	Target  string         `json:"target"`
	Sources []SourceReport `json:"sources"`
	Skipped Skipped        `json:"skipped"`
}

// Paths are an import attempt's files in the Go state root.
type Paths struct {
	Root, Attempt string
}

func (p Paths) Captures() string { return filepath.Join(p.Root, "import-"+p.Attempt) }
func (p Paths) StagingName() string {
	return "state.sqlite3.import-" + p.Attempt
}
func (p Paths) Staging() string { return filepath.Join(p.Root, p.StagingName()) }
func (p Paths) Target() string  { return filepath.Join(p.Root, "state.sqlite3") }

// Discard removes an attempt's captures and staging database. The target is untouched.
func (p Paths) Discard() error {
	var errs []error
	errs = append(errs, os.RemoveAll(p.Captures()))
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(p.Staging() + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CaptureAll discovers the sources under a Python state root, holds every writer lock,
// captures every source, resolves each memory store's repository and maps every
// source. The caller excludes the Python runtime's own commands first.
func CaptureAll(ctx context.Context, from string, install *legacy.Install, overrides map[string]string, p Paths) ([]Prepared, error) {
	sources, err := Discover(from)
	if err != nil {
		return nil, err
	}
	components, err := legacy.Components(from)
	if err != nil {
		return nil, err
	}
	guard, err := legacy.HoldWriters(components)
	if err != nil {
		return nil, err
	}
	captures, err := Capture(ctx, sources, p.Captures())
	guard.Release()
	if err != nil {
		return nil, err
	}
	if err := resolve(ctx, sources, captures, install, overrides); err != nil {
		return nil, err
	}
	names := map[string]bool{"maintainer": true}
	var prepared []Prepared
	for i, s := range sources {
		if s.Kind == "inbox" && s.Name != "" {
			// Two Python sessions never held one name at once, but retained records can;
			// the first source keeps it, and a later one is named again at registration.
			if names[s.Name] {
				s.Name = ""
			} else {
				names[s.Name] = true
			}
		}
		src, skipped, err := Map(ctx, captures[i], s)
		if err != nil {
			return nil, err
		}
		if s.Kind == "inbox" && sources[i].Name != "" && s.Name == "" {
			skipped["renamed"]++
		}
		prepared = append(prepared, Prepared{Source: s, Import: src, Skipped: skipped})
	}
	return prepared, nil
}

// resolve finds each memory store's repository: an explicit --repository mapping, else
// the saved memory selection, an inbox memory binding, or a session's repository through
// Git, in that order. A recorded path must hash to the store's key. An existing path is
// canonicalized, as the Go runtime keys stores by the resolved common directory.
func resolve(ctx context.Context, sources []Source, captures []string, install *legacy.Install, overrides map[string]string) error {
	candidates := map[string][][2]string{}
	addCandidate := func(path, how string) {
		if filepath.IsAbs(path) {
			key := KeyOf(path)
			candidates[key] = append(candidates[key], [2]string{path, how})
		}
	}
	if install != nil {
		repos := install.MemoryRepositories()
		keys := make([]string, 0, len(repos))
		for k := range repos {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			addCandidate(repos[k], "install")
		}
	}
	for i, s := range sources {
		if s.Kind != "inbox" {
			continue
		}
		db, err := openSource(captures[i])
		if err != nil {
			return err
		}
		if present, _ := columns(ctx, db, "memory_binding"); present["repo_path"] {
			rows, err := query(ctx, db, `SELECT repo_path FROM memory_binding ORDER BY binding`)
			if err != nil {
				db.Close()
				return err
			}
			for _, r := range rows {
				path, _ := r[0].(string)
				addCandidate(path, "binding")
			}
		}
		db.Close()
	}
	for _, s := range sources {
		if s.Kind == "inbox" && s.Repo != "" {
			if common, err := gitCommon(ctx, s.Repo); err == nil {
				addCandidate(common, "session")
			}
		}
	}
	for i := range sources {
		s := &sources[i]
		if s.Kind != "memory" {
			continue
		}
		path, how := overrides[s.Key], "explicit"
		if path == "" {
			for _, c := range candidates[s.Key] {
				path, how = c[0], c[1]
				break
			}
		}
		if path == "" {
			return fmt.Errorf("repository_unresolved: memory store %s has no recoverable repository; pass --repository %s=PATH", s.Key, s.Key)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("repository_unresolved: repository %q for store %s is not absolute", path, s.Key)
		}
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			path = canonical
		}
		s.Repository, s.Resolution = path, how
	}
	return nil
}

func gitCommon(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Env = core.CleanGitEnvironment()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(string(out))
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		common = resolved
	}
	return common, nil
}

func report(prepared []Prepared) Report {
	r := Report{Skipped: Skipped{}}
	for _, p := range prepared {
		line := SourceReport{Path: p.Source.Path, Kind: p.Source.Kind, Repository: p.Source.Repository,
			Resolution: p.Source.Resolution, Digest: p.Import.Digest, Counts: p.Import.Counts, Skipped: p.Skipped}
		if p.Source.Kind == "inbox" {
			line.Session = p.Source.Family + ":" + p.Source.Thread
		}
		for k, v := range p.Skipped {
			r.Skipped[k] += v
		}
		r.Sources = append(r.Sources, line)
	}
	return r
}

// matches reports whether a target's import records are exactly this prepared set.
func matches(records []core.ImportRecord, prepared []Prepared) bool {
	if len(records) != len(prepared) {
		return false
	}
	want := map[string]string{}
	for _, p := range prepared {
		want[p.Import.Path] = p.Import.Digest
	}
	for _, r := range records {
		if want[r.Source] != r.Digest {
			return false
		}
	}
	return true
}

// Stage imports a prepared set into the attempt's staging database. The target must be
// absent or empty, or already hold exactly this set, which is reported as unchanged.
// A rerun of the same attempt resumes at the first source the staging database lacks.
// The caller holds the Go state root's daemon lock.
func Stage(ctx context.Context, prepared []Prepared, p Paths) (Report, error) {
	r := report(prepared)
	r.Target = p.Target()
	if _, err := os.Lstat(p.Target()); err == nil {
		target, err := core.OpenImportStore(p.Root, "state.sqlite3")
		if err != nil {
			return r, err
		}
		empty, err := target.Empty(ctx)
		records, rerr := target.ImportRecords(ctx)
		cerr := target.Close()
		if err = errors.Join(err, rerr, cerr); err != nil {
			return r, err
		}
		if !empty {
			if matches(records, prepared) {
				r.Result = "unchanged"
				for i := range r.Sources {
					r.Sources[i].Status = "unchanged"
				}
				return r, nil
			}
			return r, errors.New("target_not_empty: the Go state database already holds other records")
		}
	}
	staging, err := core.OpenImportStore(p.Root, p.StagingName())
	if err != nil {
		return r, err
	}
	defer staging.Close()
	for i, item := range prepared {
		skipped, err := staging.Import(ctx, item.Import)
		if err != nil {
			return r, fmt.Errorf("%w (source %s)", err, item.Source.Path)
		}
		r.Sources[i].Status = map[bool]string{true: "resumed", false: "imported"}[skipped]
	}
	for _, item := range prepared {
		if err := staging.VerifyImport(ctx, item.Import); err != nil {
			return r, err
		}
	}
	r.Result = "staged"
	return r, nil
}

// Swap renames a verified staging database into place over an absent or empty target,
// then removes the attempt's captures. The caller holds the daemon lock.
func Swap(p Paths) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		for _, path := range []string{p.Staging() + suffix, p.Target() + suffix} {
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if info.Size() != 0 {
				return fmt.Errorf("swap_refused: %s holds uncheckpointed records", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}
	if err := os.Rename(p.Staging(), p.Target()); err != nil {
		return err
	}
	if err := syncDir(p.Root); err != nil {
		return err
	}
	return os.RemoveAll(p.Captures())
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Verify compares every prepared source with the target database.
func Verify(ctx context.Context, prepared []Prepared, p Paths) (Report, error) {
	r := report(prepared)
	r.Target = p.Target()
	target, err := core.OpenImportStore(p.Root, "state.sqlite3")
	if err != nil {
		return r, err
	}
	defer target.Close()
	for i, item := range prepared {
		if err := target.VerifyImport(ctx, item.Import); err != nil {
			return r, err
		}
		r.Sources[i].Status = "verified"
	}
	r.Result = "verified"
	return r, nil
}
