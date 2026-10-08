package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
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

func validNested(list []NestedRepository) bool {
	if len(list) > MaxNested {
		return false
	}
	for _, n := range list {
		p := n.Path
		if p == "" || len(p) > MaxNestedPath || !utf8.ValidString(p) || strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return false
		}
		if filepath.IsAbs(p) || filepath.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
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
}

func (s *Store) CreateLaunch(ctx context.Context, target LaunchTarget) (string, error) {
	if target.LaunchID != "" {
		return "", ErrInvalid
	}
	if target.Family != "claude" && target.Family != "codex" && target.Family != "agy" && target.Family != "opencode" {
		return "", ErrInvalid
	}
	if target.HostPID <= 0 || !filepath.IsAbs(target.CLI) || len(target.CLI) > 4096 || !validNested(target.Nested) {
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

func launchTarget(ctx context.Context, tx *sql.Tx, id, family, directory string) (json.RawMessage, error) {
	if len(id) != 64 {
		return nil, ErrInvalid
	}
	var storedFamily, storedDirectory, target string
	err := tx.QueryRowContext(ctx, `SELECT family,directory,target FROM launches WHERE id=?`, id).Scan(&storedFamily, &storedDirectory, &target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	if storedFamily != family || storedDirectory != directory {
		return nil, ErrInvalid
	}
	var public LaunchTarget
	if err := json.Unmarshal([]byte(target), &public); err != nil {
		return nil, err
	}
	public.Password = ""
	public.LaunchID = id
	return json.Marshal(public)
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
