package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkoutGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	options := []string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Synthetic",
		"-c", "user.email=synthetic@example.com", "-C", directory}
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Env = CleanGitEnvironment()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic git %v: %v %s", args, err, out)
	}
}

func checkoutFixture(t *testing.T) (Checkout, Checkout) {
	t.Helper()
	parent := t.TempDir()
	repo := filepath.Join(parent, "checkout with spaces ")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, repo, "init", "-q")
	checkoutGit(t, repo, "commit", "--allow-empty", "-m", "synthetic fixture")
	linked := filepath.Join(parent, "linked")
	checkoutGit(t, repo, "worktree", "add", "--detach", linked)
	first, err := CheckoutResource(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CheckoutResource(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

func TestCheckoutResourceIdentity(t *testing.T) {
	first, linked := checkoutFixture(t)
	ctx := context.Background()
	if first.Repository != linked.Repository || first.Directory == linked.Directory || first.Resource == linked.Resource {
		t.Fatalf("linked worktrees: %+v %+v", first, linked)
	}
	if first.Resource[0] != "exact" || len(first.Resource[1]) != len("checkout:v1:")+64 || !strings.HasPrefix(first.Resource[1], "checkout:v1:") {
		t.Fatalf("resource: %v", first.Resource)
	}
	sub := filepath.Join(first.Directory, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(first.Directory, alias); err != nil {
		t.Fatal(err)
	}
	foreign := testRepo(t)
	// All Git environment overrides must be ignored, including configuration injection.
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_COMMON_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bare")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	for _, path := range []string{first.Directory, sub, alias, filepath.Join(alias, "sub", ".."), first.Directory} {
		got, err := CheckoutResource(ctx, path)
		if err != nil || got != first {
			t.Fatalf("%q: %+v %v", path, got, err)
		}
	}
	other, err := CheckoutResource(ctx, foreign)
	if err != nil || other.Repository == first.Repository || other.Resource == first.Resource {
		t.Fatalf("other repository: %+v %v", other, err)
	}
	bare := filepath.Join(t.TempDir(), "bare.git")
	checkoutGit(t, t.TempDir(), "init", "--bare", bare)
	for _, path := range []string{bare, filepath.Join(first.Directory, ".git"), t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := CheckoutResource(ctx, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	// JSON framing must not collapse distinct invalid UTF-8 path bytes to replacement
	// characters. Refuse such paths instead of deriving an ambiguous opaque resource.
	invalidPath := filepath.Join(t.TempDir(), "checkout-\xff")
	if err := os.Mkdir(invalidPath, 0700); err != nil {
		t.Fatal(err)
	}
	checkoutGit(t, invalidPath, "init", "-q")
	if _, err := CheckoutResource(ctx, invalidPath); err == nil {
		t.Fatal("accepted a path whose JSON identity is lossy")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := CheckoutResource(ctx, first.Directory); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestCheckoutClaimsAcrossWorkItems(t *testing.T) {
	first, linked := checkoutFixture(t)
	s, _ := testStore(t)
	m := MemoryCaller{Repository: first.Repository, Family: "codex", Name: "synthetic-a", Consumer: "codex:synthetic-a"}
	other := MemoryCaller{Repository: first.Repository, Family: "claude", Name: "synthetic-b", Consumer: "claude:synthetic-b"}
	a, b := create(t, s, m, "first task"), create(t, s, other, "second task")
	resources := func(c Checkout) map[string]any { return map[string]any{"resources": [][2]string{c.Resource}} }
	genA := mustStart(t, s, m, a, resources(first))
	if _, err := start(s, other, b, 1, resources(first)); err == nil {
		t.Fatal("different work items acquired the same checkout")
	} else {
		var refusal WorkRefusal
		if !errors.As(err, &refusal) || refusal.Code != "claim_conflict" || refusal.Details["work_id"] != a {
			t.Fatalf("conflict: %v", err)
		}
	}
	if item := get(t, s, other, b); item["revision"] != float64(1) || item["current_claim"] != nil {
		t.Fatalf("conflicting start partially committed: %v", item)
	}
	genB := mustStart(t, s, other, b, resources(linked)) // separate worktrees coexist
	if _, err := work(s, other, "work-release", map[string]any{"work_id": b, "if_revision": revision(t, s, other, b),
		"claim_generation": genB, "checkpoint": "ready for handoff"}); err != nil {
		t.Fatal(err)
	}
	if _, err := work(s, m, "work-release", map[string]any{"work_id": a, "if_revision": revision(t, s, m, a),
		"claim_generation": genA, "checkpoint": "committed checkpoint; explicitly handed back"}); err != nil {
		t.Fatal(err)
	}
	mustStart(t, s, other, b, resources(first)) // explicit acceptance after release
	if item := get(t, s, m, a); item["current_claim"] != nil || item["checkpoint"] != "committed checkpoint; explicitly handed back" {
		t.Fatalf("handoff checkpoint: %v", item)
	}
}
