package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// wantCheckoutRefusal checks that err is a local refusal with code (#753).
func wantCheckoutRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refused Refusal
	if !errors.As(err, &refused) || refused.Code != code || refused.Message == "" {
		t.Fatalf("got %v, want refusal %s", err, code)
	}
}

func TestLibraryCheckoutOnlyRegisteredListedWorktrees(t *testing.T) {
	first, linked := checkoutFixture(t)
	ctx := context.Background()
	sub := filepath.Join(linked.Directory, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"", first.Directory, linked.Directory, sub} {
		got, err := LibraryCheckout(ctx, first.Directory, selected, first.Repository)
		if err != nil || got.Repository != first.Repository {
			t.Fatalf("listed %q: %+v %v", selected, got, err)
		}
	}
	foreign := testRepo(t)
	for selected, code := range map[string]string{foreign: "directory_other_repository", t.TempDir(): "directory_unresolved",
		filepath.Join(first.Directory, ".git"): "directory_unresolved"} {
		_, err := LibraryCheckout(ctx, first.Directory, selected, first.Repository)
		wantCheckoutRefusal(t, err, code)
	}
	_, err := LibraryCheckout(ctx, first.Directory, "", filepath.Join(foreign, ".git"))
	wantCheckoutRefusal(t, err, "project_context_unavailable")
	// Copying a linked .git pointer resolves the same common directory but must not
	// admit a directory that Git does not list as a worktree.
	unlisted := t.TempDir()
	data, err := os.ReadFile(filepath.Join(linked.Directory, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unlisted, ".git"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err := CheckoutResource(ctx, unlisted); err != nil || raw.Repository != first.Repository {
		t.Fatalf("fixture does not reach listed-worktree check: %+v %v", raw, err)
	}
	_, err = LibraryCheckout(ctx, first.Directory, unlisted, first.Repository)
	wantCheckoutRefusal(t, err, "directory_not_listed")
}

// TestLibraryCheckoutSubmodule: Git lists a submodule's main worktree by its Git directory
// (#753). A session registered in the submodule selects it and its linked worktree; from the
// parent, the submodule is another repository.
func TestLibraryCheckoutSubmodule(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	child, parent := filepath.Join(root, "child"), filepath.Join(root, "parent")
	for _, dir := range []string{child, parent} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		checkoutGit(t, dir, "init", "-q")
		checkoutGit(t, dir, "commit", "-q", "--allow-empty", "-m", "synthetic fixture")
	}
	checkoutGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", child, "sub")
	sub := filepath.Join(parent, "sub")
	linked := filepath.Join(root, "sub-linked")
	checkoutGit(t, sub, "worktree", "add", "-q", "--detach", linked)
	base, err := CheckoutResource(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{"", sub, linked, filepath.Join(sub, ".")} {
		got, err := LibraryCheckout(ctx, sub, selected, base.Repository)
		if err != nil || got.Repository != base.Repository {
			t.Fatalf("submodule %q: %+v %v", selected, got, err)
		}
	}
	if got, err := LibraryCheckout(ctx, linked, sub, base.Repository); err != nil || got.Directory != sub {
		t.Fatalf("from the linked worktree: %+v %v", got, err)
	}
	top, err := CheckoutResource(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	_, err = LibraryCheckout(ctx, parent, sub, top.Repository)
	wantCheckoutRefusal(t, err, "directory_other_repository")
}
