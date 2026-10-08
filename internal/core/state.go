package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/rwcii/koinon/internal/platform"
)

func loadSecret(root string) (string, error) {
	path := filepath.Join(root, "secret")
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if err == nil {
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, 65))
		if err != nil {
			return "", err
		}
		if len(data) != 64 {
			return "", errors.New("invalid secret file")
		}
		decoded, err := hex.DecodeString(string(data))
		if err != nil || len(decoded) != 32 {
			return "", errors.New("invalid secret file")
		}
		return string(data), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	secret := hex.EncodeToString(data)
	// Publish only the complete, synced secret; an interrupted first start never
	// leaves a partial secret in the established path.
	f, err = os.CreateTemp(root, ".secret-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = io.WriteString(f, secret); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	if err := platform.SyncDir(root); err != nil {
		return "", err
	}
	return secret, nil
}

// ReadSecret reads an existing secret without creating state or starting a daemon.
func ReadSecret(root string) (string, error) {
	f, err := platform.OpenPrivate(filepath.Join(root, "secret"), os.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65))
	if err != nil {
		return "", err
	}
	if len(data) != 64 {
		return "", errors.New("invalid secret file")
	}
	if _, err := hex.DecodeString(string(data)); err != nil {
		return "", errors.New("invalid secret file")
	}
	return string(data), nil
}
