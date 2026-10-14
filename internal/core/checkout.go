package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Checkout describes a Git working tree and its advisory exact resource. Derivation
// reads Git metadata; it never takes a lease, installs a hook or fences a filesystem.
type Checkout struct {
	Repository string    `json:"repository"`
	Directory  string    `json:"directory"`
	Resource   [2]string `json:"resource"`
}

// CheckoutResource derives a versioned key from the resolved Git common directory and
// worktree root. Subdirectories and symlink spellings converge; linked worktrees differ.
// Inherited GIT_ overrides cannot retarget either part of the identity.
func CheckoutResource(ctx context.Context, directory string) (Checkout, error) {
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return Checkout{}, ErrInvalid
		}
	}
	path, err := filepath.Abs(directory)
	if err != nil {
		return Checkout{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	common, err := repository(ctx, path)
	if err != nil {
		return Checkout{}, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return Checkout{}, ErrInvalid // includes bare repositories and Git metadata directories
	}
	root, err := filepath.EvalSymlinks(strings.TrimSuffix(string(data), "\n"))
	if err != nil || !filepath.IsAbs(root) || !utf8.ValidString(common) || !utf8.ValidString(root) {
		return Checkout{}, ErrInvalid
	}
	// JSON frames both paths without separator ambiguity. The fixed-size opaque key
	// fits existing resource bounds and does not put checkout paths in claim keys.
	identity, _ := json.Marshal([2]string{common, root})
	sum := sha256.Sum256(identity)
	return Checkout{common, root, [2]string{"exact", "checkout:v1:" + hex.EncodeToString(sum[:])}}, nil
}
