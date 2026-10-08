package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// RecordCLI records the resolved CLI during setup. Cooperating writers hold one private
// lock, preserve other entries and replace the file atomically. Launches only read it.
func RecordCLI(ctx context.Context, stateDir, family, cli string) (bool, error) {
	if family != "codex" && family != "agy" && family != "opencode" {
		return false, errors.New("unsupported launcher family")
	}
	cli, err := ConfiguredCLI(stateDir, family, cli)
	if err != nil {
		return false, err
	}
	root, err := platform.PrivateDir(stateDir)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var lock *os.File
	for {
		lock, err = platform.Lock(filepath.Join(root, "launchers.lock"))
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return false, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer platform.Unlock(lock)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path := filepath.Join(root, "launchers.json")
	config := map[string]string{}
	f, err := platform.OpenPrivate(path, os.O_RDONLY)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, 16385))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			return false, errors.Join(readErr, closeErr)
		}
		if len(data) > 16384 || json.Unmarshal(data, &config) != nil || config == nil {
			return false, errors.New("invalid launcher configuration; left unchanged")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if config[family] == cli {
		return false, nil
	}
	config[family] = cli
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if len(data) > 16384 {
		return false, errors.New("launcher configuration exceeds 16 KiB; left unchanged")
	}
	if err := platform.WriteAtomic(path, data, 0600); err != nil {
		return false, err
	}
	return true, nil
}
