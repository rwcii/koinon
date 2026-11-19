package core

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LibraryCheckout selects a listed worktree of the registered repository. It shares
// checkout identity derivation with work_checkout and adds the library's list check.
// A copied Git pointer alone cannot introduce an unlisted inspection root. A refusal is a
// Refusal whose code names the cause, without paths (#753):
// project_context_unavailable (the registered checkout cannot be read),
// directory_unresolved (not a Git checkout), directory_other_repository (another
// repository, such as a submodule of the registered one) and directory_not_listed.
func LibraryCheckout(ctx context.Context, registeredDirectory, directory, registeredRepository string) (Checkout, error) {
	base, err := CheckoutResource(ctx, registeredDirectory)
	if err != nil || registeredRepository != "" && base.Repository != registeredRepository {
		return Checkout{}, checkoutRefusal("project_context_unavailable", "the session's registered checkout cannot be read as a Git checkout of its registered repository")
	}
	selected := base
	if directory != "" {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(registeredDirectory, directory)
		}
		selected, err = CheckoutResource(ctx, directory)
		if err != nil {
			return Checkout{}, checkoutRefusal("directory_unresolved", "the directory is not in a Git checkout")
		}
		if selected.Repository != base.Repository {
			return Checkout{}, checkoutRefusal("directory_other_repository",
				"the directory is in another Git repository than the session's registered repository; a submodule is its own repository, so start the session in it")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", base.Directory, "worktree", "list", "--porcelain", "-z")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return Checkout{}, checkoutRefusal("project_context_unavailable", "git worktree list failed for the registered checkout")
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
		if resolved == base.Repository {
			// Git lists the main worktree of a submodule by its Git directory; its work tree
			// is the one that the repository's own core.worktree names.
			if resolved, err = mainWorktree(ctx, resolved); err != nil {
				continue
			}
		}
		baseListed = baseListed || resolved == base.Directory
		selectedListed = selectedListed || resolved == selected.Directory
	}
	if !baseListed {
		return Checkout{}, checkoutRefusal("project_context_unavailable", "Git does not list the session's registered checkout as a worktree")
	}
	if !selectedListed {
		return Checkout{}, checkoutRefusal("directory_not_listed", "Git does not list the directory as a worktree of the registered repository")
	}
	return selected, nil
}

// mainWorktree is the work tree of the Git directory gitDir, from its own configuration.
func mainWorktree(ctx context.Context, gitDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--git-dir="+gitDir, "rev-parse", "--show-toplevel")
	cmd.Env = CleanGitEnvironment()
	data, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSuffix(string(data), "\n"))
}

func checkoutRefusal(code, message string) error {
	return Refusal{Code: code, Message: message}
}
