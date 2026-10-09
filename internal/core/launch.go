package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxNested and MaxNestedPath bound the nested repositories that a launch reports (#252).
const (
	MaxNested     = 64
	MaxNestedPath = 256
)

// NestedRepository is a repository inside a launch's start directory that is not part of
// the start repository: Path is relative to the start directory, Kind is submodule,
// worktree (a linked worktree of another repository) or repository.
type NestedRepository struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// NestedReport is a launch's list of nested repositories; Incomplete says that the scan
// stopped at a bound or could not read a subtree.
type NestedReport struct {
	List       []NestedRepository
	Incomplete bool
}

// ValidNestedPath reports whether a launch record can hold p: a clean relative path of at
// most MaxNestedPath bytes of UTF-8 with no control character.
func ValidNestedPath(p string) bool {
	if p == "" || len(p) > MaxNestedPath || !utf8.ValidString(p) || strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	return !filepath.IsAbs(p) && filepath.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, ".."+string(filepath.Separator))
}

func validNested(list []NestedRepository) bool {
	if len(list) > MaxNested {
		return false
	}
	for _, n := range list {
		if !ValidNestedPath(n.Path) {
			return false
		}
		if n.Kind != "submodule" && n.Kind != "worktree" && n.Kind != "repository" {
			return false
		}
	}
	return true
}

// LaunchTarget is supplied by the local launcher, never by peer message content.
// The credential stays in private daemon state and is not returned by launch APIs.
type LaunchTarget struct {
	LaunchID  string `json:"launch_id,omitempty"`
	Family    string `json:"family"`
	Directory string `json:"directory"`
	CLI       string `json:"cli"`
	HostPID   int    `json:"host_pid"`
	Address   string `json:"address,omitempty"`
	Password  string `json:"password,omitempty"`
	// Nested lists the repositories inside Directory that are not part of its repository;
	// the session's Koinon repository is still Directory's (#252).
	Nested           []NestedRepository `json:"nested,omitempty"`
	NestedIncomplete bool               `json:"nested_incomplete,omitempty"`
	// Background marks a Claude background job (koinon claude --bg). Its host is the job,
	// not the launcher: JobID, recorded once after the job starts, admits only the Claude
	// session whose ID begins with it.
	Background bool   `json:"background,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	// Role is the maintainer's role for the launched session: its participant is the
	// family, repository and role (participants sprint, chunk 02).
	Role string `json:"role,omitempty"`
}

var roleChars = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var hexOnly = regexp.MustCompile(`^[0-9a-f-]+$`)

// ValidRole reports whether role can name a participant: lower-case letters, digits and
// hyphens, 1 to 24 characters, starting with a letter. A role made only of hexadecimal
// digits (and hyphens) is refused, so an address never looks like a peer name's suffix.
func ValidRole(role string) bool {
	return roleChars.MatchString(role) && !hexOnly.MatchString(role)
}

// maxAncestors bounds a registration's process ancestry.
const maxAncestors = 256

func (s *Store) CreateLaunch(ctx context.Context, target LaunchTarget) (string, error) {
	if target.LaunchID != "" || target.JobID != "" || target.Background && target.Family != "claude" {
		return "", ErrInvalid
	}
	if target.Family != "claude" && target.Family != "codex" && target.Family != "agy" && target.Family != "opencode" {
		return "", ErrInvalid
	}
	if target.HostPID <= 0 || !filepath.IsAbs(target.CLI) || len(target.CLI) > 4096 || !validNested(target.Nested) ||
		target.Role != "" && !ValidRole(target.Role) {
		return "", ErrInvalid
	}
	dir, err := filepath.EvalSymlinks(target.Directory)
	if err != nil || !filepath.IsAbs(target.Directory) || len(dir) > 4096 {
		return "", ErrInvalid
	}
	target.Directory = dir
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", ErrInvalid
	}
	if target.Family == "opencode" {
		_, port, addressErr := net.SplitHostPort(target.Address)
		number, portErr := strconv.Atoi(port)
		if addressErr != nil || portErr != nil || number < 1 || number > 65535 {
			return "", ErrInvalid
		}
		if !validAddress(target.Address) || len(target.Password) != 64 {
			return "", ErrInvalid
		}
		if _, err := hex.DecodeString(target.Password); err != nil {
			return "", ErrInvalid
		}
	} else if target.Address != "" || target.Password != "" {
		return "", ErrInvalid
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(nonce[:])
	data, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO launches (id,family,directory,target,created_at) VALUES (?,?,?,?,?)`, id, target.Family, dir, string(data), s.now().UnixMilli()); err != nil {
		return "", tx.fail(err)
	}
	return id, tx.Commit()
}

// admitLaunch returns the wake target of a launcher-family registration: the public target
// of its launch, and for Claude the claude_pid that its server reports. The launch must be
// of the caller's family and directory and must belong to the caller: a foreground launch's
// host process is among the caller's ancestors (for Claude, it is the Claude process); a
// background launch's recorded job ID begins the caller's session ID. Anything else is
// not_launched, so a direct start never registers.
func admitLaunch(ctx context.Context, tx *sql.Tx, r Registration, directory string) (json.RawMessage, string, error) {
	if len(r.LaunchID) != 64 || len(r.Ancestors) > maxAncestors {
		return nil, "", ErrNotLaunched
	}
	claudePID := 0
	if r.Family == "claude" {
		var own struct {
			ClaudePID int `json:"claude_pid"`
		}
		dec := json.NewDecoder(bytes.NewReader(r.WakeTarget))
		dec.DisallowUnknownFields()
		if dec.Decode(&own) != nil || own.ClaudePID <= 0 {
			return nil, "", ErrInvalid
		}
		claudePID = own.ClaudePID
	} else if len(r.WakeTarget) != 0 && string(r.WakeTarget) != "{}" {
		return nil, "", ErrInvalid
	}
	var family, storedDirectory, stored string
	err := tx.QueryRowContext(ctx, `SELECT family,directory,target FROM launches WHERE id=?`, r.LaunchID).Scan(&family, &storedDirectory, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotLaunched
	}
	if err != nil {
		return nil, "", err
	}
	var target LaunchTarget
	if err := json.Unmarshal([]byte(stored), &target); err != nil {
		return nil, "", err
	}
	if family != r.Family || storedDirectory != directory {
		return nil, "", ErrNotLaunched
	}
	if target.Background {
		if target.JobID == "" {
			return nil, "", ErrLaunchPending
		}
		// A full job ID is the session ID; a short one is the first eight characters of it.
		full := FullJobID.MatchString(target.JobID) && r.ID == target.JobID
		short := ShortJobID.MatchString(target.JobID) && FullJobID.MatchString(r.ID) && strings.HasPrefix(r.ID, target.JobID+"-")
		if !full && !short {
			return nil, "", ErrNotLaunched
		}
	} else if !slices.Contains(r.Ancestors, target.HostPID) || r.Family == "claude" && claudePID != target.HostPID {
		return nil, "", ErrNotLaunched
	}
	target.Password = ""
	target.LaunchID = r.LaunchID
	public, err := json.Marshal(target)
	if err != nil || claudePID == 0 {
		return public, target.Role, err
	}
	var merged map[string]any
	if err := json.Unmarshal(public, &merged); err != nil {
		return nil, "", err
	}
	merged["claude_pid"] = claudePID
	public, err = json.Marshal(merged)
	return public, target.Role, err
}

// launched reports whether a session registered with its launch.
func launched(s Session) bool {
	var target struct {
		LaunchID string `json:"launch_id"`
	}
	return json.Unmarshal(s.WakeTarget, &target) == nil && target.LaunchID != ""
}

// ShortJobID and FullJobID are the job IDs that claude --bg reports: a session's first eight
// characters, or its whole ID.
var (
	ShortJobID = regexp.MustCompile(`^[0-9a-f]{8}$`)
	FullJobID  = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
)

// SetLaunchJob records the job ID of a background launch, once.
func (s *Store) SetLaunchJob(ctx context.Context, id, job string) error {
	if len(id) != 64 || !ShortJobID.MatchString(job) && !FullJobID.MatchString(job) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	target, err := backgroundLaunch(ctx, tx.Tx, id)
	if err != nil {
		return err
	}
	if target.JobID == job {
		return tx.Commit()
	}
	if target.JobID != "" {
		return ErrConflict
	}
	target.JobID = job
	data, err := json.Marshal(target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE launches SET target=? WHERE id=?`, string(data), id); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// RetireLaunch deletes a background launch whose job never started, so it admits nothing.
func (s *Store) RetireLaunch(ctx context.Context, id string) error {
	if len(id) != 64 {
		return ErrInvalid
	}
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	target, err := backgroundLaunch(ctx, tx.Tx, id)
	if err != nil {
		return err
	}
	if target.JobID != "" {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM launches WHERE id=?`, id); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

func backgroundLaunch(ctx context.Context, tx *sql.Tx, id string) (LaunchTarget, error) {
	var stored string
	var target LaunchTarget
	err := tx.QueryRowContext(ctx, `SELECT target FROM launches WHERE id=?`, id).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrMissing
	}
	if err != nil {
		return target, err
	}
	if err := json.Unmarshal([]byte(stored), &target); err != nil {
		return target, err
	}
	if !target.Background {
		return target, ErrInvalid
	}
	return target, nil
}

// NestedRepositories returns the nested repositories that the session's launch record
// reports, or nil when it reports none.
func (s Session) NestedRepositories() *NestedReport {
	var target LaunchTarget
	if json.Unmarshal(s.WakeTarget, &target) != nil || len(target.Nested) == 0 && !target.NestedIncomplete {
		return nil
	}
	return &NestedReport{List: target.Nested, Incomplete: target.NestedIncomplete}
}
