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
)

// LaunchTarget is supplied by the local launcher, never by peer message content.
// The credential stays in private daemon state and is not returned by launch APIs.
type LaunchTarget struct {
	Family    string `json:"family"`
	Directory string `json:"directory"`
	CLI       string `json:"cli"`
	HostPID   int    `json:"host_pid"`
	Address   string `json:"address,omitempty"`
	Password  string `json:"password,omitempty"`
}

func (s *Store) CreateLaunch(ctx context.Context, target LaunchTarget) (string, error) {
	if target.Family != "codex" && target.Family != "agy" && target.Family != "opencode" {
		return "", ErrInvalid
	}
	if target.HostPID <= 0 || !filepath.IsAbs(target.CLI) || len(target.CLI) > 4096 {
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO launches (id,family,directory,target,created_at) VALUES (?,?,?,?,?)`, id, target.Family, dir, string(data), s.now().UnixMilli())
	return id, err
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
	return json.RawMessage(target), nil
}
