package core

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

// LibraryCheckout selects a listed worktree of the registered repository. It shares
// checkout identity derivation with work_checkout and adds the library's list check.
// A copied Git pointer alone cannot introduce an unlisted inspection root.
func LibraryCheckout(ctx context.Context, registeredDirectory, directory, registeredRepository string) (Checkout, error) {
	base, err := CheckoutResource(ctx, registeredDirectory)
	if err != nil || registeredRepository != "" && base.Repository != registeredRepository {
		return Checkout{}, ErrInvalid
	}
	selected := base
	if directory != "" {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(registeredDirectory, directory)
		}
		selected, err = CheckoutResource(ctx, directory)
		if err != nil || selected.Repository != base.Repository {
			return Checkout{}, ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", base.Directory, "worktree", "list", "--porcelain", "-z")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return Checkout{}, ErrInvalid
	}
	baseListed, selectedListed := false, false
	for _, record := range bytes.Split(data, []byte{0}) {
		root, ok := bytes.CutPrefix(record, []byte("worktree "))
		if !ok {
			continue
		}
		resolved, err := filepath.EvalSymlinks(string(root))
		if err != nil {
			continue
		}
		baseListed = baseListed || resolved == base.Directory
		selectedListed = selectedListed || resolved == selected.Directory
	}
	if !baseListed || !selectedListed {
		return Checkout{}, ErrInvalid
	}
	return selected, nil
}
