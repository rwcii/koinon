package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withoutRepository registers f's session without a repository, in a new directory that is
// not a Git checkout, and points f's handoff writer there (#485).
func (f *cycleFixture) withoutRepository(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(f.s, `UPDATE sessions SET repository='',directory=? WHERE family=? AND id=?`, dir, f.k.Family, f.k.ID); err != nil {
		t.Fatal(err)
	}
	f.repo = dir
	return dir
}

// A session without a repository writes and picks up its handoff in its registered
// directory, which Git does not check (#485).
func TestContextCycleWithoutRepositoryCompletes(t *testing.T) {
	f := newCycleFixture(t)
	f.withoutRepository(t)
	v, name := f.readyCycle(t)
	if v.State != cycleReady {
		t.Fatalf("ready: %+v", v)
	}
	f.step(t)
	f.step(t)
	f.step(t)
	v = *f.status(t, f.k)
	done, err := f.s.ContextCyclePickup(context.Background(), f.k, "synthetic-epoch-2", v.ID, v.Revision, name, "resumed", "synthetic restored work")
	if err != nil || done.State != cycleComplete {
		t.Fatalf("pickup: %v %+v", err, done)
	}
}

// Without a repository, the Handoff path fact and the role suffix apply in the registered
// directory, the file checks stay, and a relative directory is refused (#485).
func TestContextCycleArtifactWithoutRepository(t *testing.T) {
	f := newCycleFixture(t)
	dir := f.withoutRepository(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	check := func(name string) error {
		_, err := f.s.verifyArtifact(context.Background(), f.cycle(t, v.ID), name)
		return err
	}
	if err := check("2026-10-13-120000.md"); !artifactRefused(err, "must be in _handoff/claude/ of the registered directory") {
		t.Fatalf("missing directory: %v", err)
	}
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if err := check(name); err != nil {
		t.Fatalf("default directory: %v", err)
	}
	// A link to a file outside the directory and an empty file are refused.
	outside := filepath.Join(t.TempDir(), "outside.md")
	os.WriteFile(outside, []byte("synthetic"), 0o600)
	os.Symlink(outside, filepath.Join(dir, "_handoff", "claude", "2026-10-13-120001.md"))
	if err := check("2026-10-13-120001.md"); !artifactRefused(err, "is not a regular file in the registered directory") {
		t.Fatalf("link: %v", err)
	}
	empty := f.handoff(t, "claude", "2026-10-13-120002.md", "")
	if err := check(empty); !artifactRefused(err, "must hold 1 byte to 1 MiB") {
		t.Fatalf("empty: %v", err)
	}
	// The role suffix names the directory.
	fixtureExec(f.s, `UPDATE sessions SET role='review' WHERE family=? AND id=?`, f.k.Family, f.k.ID)
	if err := check(name); !artifactRefused(err, "_handoff/claude-review/ of the registered directory") {
		t.Fatalf("role: %v", err)
	}
	role := f.handoff(t, "claude-review", "2026-10-13-120003.md", "synthetic")
	if err := check(role); err != nil {
		t.Fatalf("role directory: %v", err)
	}
	// The Handoff path fact in the registered directory moves it.
	facts := filepath.Join(dir, ".koinon", "skills", "handoff.md")
	os.MkdirAll(filepath.Dir(facts), 0o700)
	os.WriteFile(facts, []byte("# Repository facts — handoff\n\n## Version\n1\n## Skill\nhandoff\n## Sources\n- AGENTS.md\n## Handoff path\n.snapshots/<agent>/\n"), 0o644)
	if err := check(role); !artifactRefused(err, ".snapshots/claude-review/ of the registered directory") {
		t.Fatalf("fact: %v", err)
	}
	moved := filepath.Join(dir, ".snapshots", "claude-review")
	os.MkdirAll(moved, 0o700)
	os.WriteFile(filepath.Join(moved, role), []byte("synthetic"), 0o600)
	os.Chtimes(filepath.Join(moved, role), f.clock, f.clock)
	if err := check(role); err != nil {
		t.Fatalf("fact directory: %v", err)
	}
	fixtureExec(f.s, `UPDATE sessions SET directory='relative/dir' WHERE family=? AND id=?`, f.k.Family, f.k.ID)
	if err := check(role); !artifactRefused(err, "has no absolute registered directory") {
		t.Fatalf("relative: %v", err)
	}
}

// A session with a repository whose checkout is gone keeps the repository checks (#485).
func TestContextCycleArtifactBrokenRepositoryKeepsChecks(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	fixtureExec(f.s, `UPDATE sessions SET directory=? WHERE family=? AND id=?`, dir, f.k.Family, f.k.ID)
	f.repo = dir
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic")
	if _, err := f.s.verifyArtifact(context.Background(), f.cycle(t, v.ID), name); !artifactRefused(err, "has no main checkout for this session's repository") {
		t.Fatalf("broken repository: %v", err)
	}
}

// Each refused ready report is kept on the cycle with its code and text, without a revision
// or an event, across a store reopen; the stage timeout names the newest one's code, and the
// audit keeps the code only (#485).
func TestContextCycleReadyRefusalsAreRecorded(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	events := count(t, f.s, `SELECT COUNT(*) FROM context_cycle_events`)
	ready := func(epoch string, revision int64, name string) error {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, epoch, v.ID, revision, name)
		return err
	}
	os.MkdirAll(filepath.Join(f.repo, "_handoff", "claude"), 0o700)
	if err := ready("synthetic-epoch-1", v.Revision, "2026-10-13-120000-private-label.md"); !artifactRefused(err, "private-label") {
		t.Fatalf("missing file: %v", err)
	}
	if err := ready("synthetic-epoch-9", v.Revision, "2026-10-13-120000.md"); !errors.Is(err, ErrCycleEpoch) {
		t.Fatalf("epoch: %v", err)
	}
	// A report for another revision is not this revision's diagnosis.
	if err := ready("synthetic-epoch-1", v.Revision+1, "2026-10-13-120000.md"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("stale revision: %v", err)
	}
	f.handoff(t, "claude", "2026-10-13-120001.md", "")
	if err := ready("synthetic-epoch-1", v.Revision, "2026-10-13-120001.md"); !artifactRefused(err, "must hold 1 byte to 1 MiB") {
		t.Fatalf("empty: %v", err)
	}
	// The store is reopened: the history stays.
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now = f.s.now
	f.s = reopened
	got := f.status(t, f.k)
	if got.Revision != v.Revision || got.State != cycleWaitingReady || count(t, f.s, `SELECT COUNT(*) FROM context_cycle_events`) != events {
		t.Fatalf("a refusal changed the cycle: %+v", got)
	}
	if len(got.Refusals) != 3 || got.Refusals[0].Code != ErrCycleArtifact.Code || !strings.Contains(got.Refusals[0].Message, "private-label") ||
		got.Refusals[1].Code != ErrCycleEpoch.Code || got.Refusals[2].Code != ErrCycleArtifact.Code || got.Refusals[2].Revision != v.Revision {
		t.Fatalf("refusals: %+v", got.Refusals)
	}
	f.clock = time.UnixMilli(got.StageDeadline)
	f.step(t)
	done := f.status(t, f.k)
	if done.State != cycleFailed || done.Reason != "waiting_ready_timeout.refused.cycle_artifact_invalid" || len(done.Refusals) != 3 {
		t.Fatalf("timeout: %+v", done)
	}
	// The failure ends the Enable, as each failed cycle does.
	if e, _ := f.s.contextEnables(context.Background()); e[f.k].Enabled {
		t.Fatal("Enable still on after the failure")
	}
	var reason string
	if err := f.s.db.QueryRow(`SELECT reason FROM audit WHERE action='context-cycle' AND result='failed'`).Scan(&reason); err != nil ||
		reason != done.Reason {
		t.Fatalf("audit %q: %v", reason, err)
	}
	if n := count(t, f.s, `SELECT COUNT(*) FROM audit WHERE reason LIKE '%private-label%' OR target LIKE '%private-label%'`); n != 0 {
		t.Fatalf("audit holds the refusal text: %d", n)
	}
}

// A refusal answered at an earlier revision does not name a later stage's timeout, and the
// newest maxCycleRefusals reports are kept (#485).
func TestContextCycleReadyRefusalBelongsToItsRevision(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	for i := 0; i < maxCycleRefusals+2; i++ {
		if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, "2026-10-13-120000.md"); !artifactRefused(err, "") {
			t.Fatalf("refusal %d: %v", i, err)
		}
	}
	if got := f.status(t, f.k); len(got.Refusals) != maxCycleRefusals {
		t.Fatalf("kept %d refusals", len(got.Refusals))
	}
	name := f.handoff(t, "claude", "2026-10-13-120000.md", "synthetic handoff")
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision, name); err != nil {
		t.Fatal(err)
	}
	// The reported file changes: the cycle waits for a new ready report at a new revision.
	if err := os.WriteFile(filepath.Join(f.repo, "_handoff", "claude", name), []byte("synthetic changed handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.step(t)
	again := f.status(t, f.k)
	if again.State != cycleWaitingReady || again.Revision == v.Revision {
		t.Fatalf("artifact change: %+v", again)
	}
	f.clock = time.UnixMilli(again.StageDeadline)
	f.step(t)
	if done := f.status(t, f.k); done.State != cycleFailed || strings.Contains(done.Reason, "refused") {
		t.Fatalf("an old refusal named the timeout: %+v", done)
	}
}

// A schema 29 store gains the empty refusal table without changing its state; a schema 29
// writer refuses the result (#485).
func TestSchemaThirtyKeepsState(t *testing.T) {
	f := newCycleFixture(t)
	f.readyCycle(t)
	rows := rowsOf(t, f.s.db)
	if _, err := fixtureExec(f.s, undoSchemaThirtySix+undoSchemaThirtyFour+undoSchemaThirtyThree+undoSchemaThirtyTwo+undoSchemaThirtyOne+undoSchemaThirty+`PRAGMA user_version=29`); err != nil {
		t.Fatal(err)
	}
	if err := checkCatalog(f.s.db, 29); err != nil {
		t.Fatal(err)
	}
	f.s.db.Close()
	upgraded, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.db.Close()
	if count(t, upgraded, `PRAGMA user_version`) != schemaVersion || rowsOf(t, upgraded.db) != rows ||
		count(t, upgraded, `SELECT COUNT(*) FROM context_cycle_refusals`) != 0 {
		t.Fatal("schema 30 changed retained state")
	}
	if err := checkCatalog(upgraded.db, 29); err == nil {
		t.Fatal("a schema 29 writer would accept the schema 30 store")
	}
}

// With ordinary storage full, a refused ready report still names the timeout: its code is
// kept in the cycle's slot, written in place without a new page, across a store reopen; a
// report for another revision does not take the slot (#485).
func TestContextCycleReadyRefusalAtOrdinaryCeiling(t *testing.T) {
	f := newCycleFixture(t)
	f.observe(t, f.k, 900, "synthetic-epoch-1")
	v := f.status(t, f.k)
	f.s.storage.maxPages = count(t, f.s, `PRAGMA page_count`) + reservePages + commitSlack + f.debt(t) - 1
	if tx, err := f.s.begin(context.Background(), ordinary); err == nil {
		tx.Rollback()
		t.Fatal("ordinary capacity is not full")
	} else if !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	pages, free := count(t, f.s, `PRAGMA page_count`), count(t, f.s, `PRAGMA freelist_count`)
	for _, epoch := range []string{"synthetic-epoch-9", "synthetic-epoch-1"} {
		_, err := f.s.ContextCycleReady(context.Background(), f.k, epoch, v.ID, v.Revision, "2026-10-26-060000.md")
		if !errors.Is(err, ErrCycleEpoch) && !artifactRefused(err, "") {
			t.Fatalf("ready: %v", err)
		}
	}
	if _, err := f.s.ContextCycleReady(context.Background(), f.k, "synthetic-epoch-1", v.ID, v.Revision+3, "2026-10-26-060000.md"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("stale revision: %v", err)
	}
	if count(t, f.s, `PRAGMA page_count`) != pages || count(t, f.s, `PRAGMA freelist_count`) != free {
		t.Fatal("the slot write allocated a page")
	}
	if got := f.status(t, f.k); len(got.Refusals) != 0 {
		t.Fatalf("history written at the ordinary ceiling: %+v", got.Refusals)
	}
	maxPages := f.s.storage.maxPages
	f.s.db.Close()
	reopened, err := openStore(f.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.db.Close() })
	reopened.now, reopened.storage.maxPages = f.s.now, maxPages
	f.s = reopened
	f.clock = time.UnixMilli(v.StageDeadline)
	f.step(t)
	if got := f.status(t, f.k); got.State != cycleFailed || got.Reason != "waiting_ready_timeout.refused.cycle_artifact_invalid" {
		t.Fatalf("timeout: %+v", got)
	}
}
