package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"

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

// ClientSecret reads the secret for a client command. A state directory without a secret
// has never held a running daemon, so the error says so and how to start one.
func ClientSecret(root, address string) (string, error) {
	if !validAddress(address) {
		return "", ErrInvalid
	}
	secret, err := ReadSecret(root)
	if errors.Is(err, fs.ErrNotExist) {
		return "", errors.New("no daemon has started with state directory " + root + "; " + StartDaemon(root, address))
	}
	if err != nil {
		return "", errors.New("cannot read private daemon secret")
	}
	return secret, nil
}

// StartDaemon names how docs/INSTALL.md starts a daemon with this state directory at this
// loopback address. The installed service listens only on the default port, so another
// address has only the serve command, with a listener for each loopback family.
func StartDaemon(root, address string) string {
	serve := "koinon serve --state-dir " + shellWord(root)
	host, port, _ := net.SplitHostPort(address)
	if port == "47671" && (host == "127.0.0.1" || host == "::1") {
		return "start it with koinon install --state-dir " + shellWord(root) + " from a login session, or run " + serve + " in a persistent managed session"
	}
	listen, listen6 := net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("::1", port)
	if net.ParseIP(host).To4() != nil {
		listen = address
	} else {
		listen6 = address
	}
	return "start it with " + serve + " --listen " + shellWord(listen) + " --listen-v6 " + shellWord(listen6) + " in a persistent managed session"
}

// shellWord quotes a value for a POSIX shell unless it holds only characters that need none.
func shellWord(value string) string {
	if value != "" && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=,-") == "" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
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
