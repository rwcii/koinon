package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
	for _, selected := range []string{foreign, t.TempDir(), filepath.Join(first.Directory, ".git")} {
		if _, err := LibraryCheckout(ctx, first.Directory, selected, first.Repository); err == nil {
			t.Fatalf("accepted foreign/plain/metadata %q", selected)
		}
	}
	if _, err := LibraryCheckout(ctx, first.Directory, "", filepath.Join(foreign, ".git")); err == nil {
		t.Fatal("accepted a changed registered repository")
	}
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
	if _, err := LibraryCheckout(ctx, first.Directory, unlisted, first.Repository); err == nil {
		t.Fatal("accepted an unlisted copied Git pointer")
	}
}
