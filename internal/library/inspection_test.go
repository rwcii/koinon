package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuideMachineInspectionFourStatesKeepsPathsAndBodiesPrivate(t *testing.T) {
	c, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	ctx := InspectionContext{Family: "codex", Scope: "machine", MachineRoot: t.TempDir(), ProjectRoot: t.TempDir(), UserRoot: t.TempDir(), Repository: "/synthetic/common"}
	read, _ := c.Read("codex", "skill.koinon-guide")
	targets, _ := c.Targets("codex", "machine", read.Item.ID)
	filename := filepath.Join(ctx.MachineRoot, filepath.FromSlash(targets[0].Path))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	inspect := func(decision *Decision, want string) {
		t.Helper()
		state := (Inspector{}).InspectItem(c, ctx, read.Item.ID, decision)
		if state.State != want || state.Reason != "" {
			t.Fatalf("state %s: %+v", want, state)
		}
		data, _ := json.Marshal(state)
		if strings.Contains(string(data), ctx.MachineRoot) || strings.Contains(string(data), ctx.ProjectRoot) ||
			strings.Contains(string(data), "synthetic old private body") {
			t.Fatal("inspection returned native paths or file bodies")
		}
	}
	inspect(nil, "missing")
	old := []byte("synthetic old private body")
	if err := os.WriteFile(filename, old, 0600); err != nil {
		t.Fatal(err)
	}
	d := Decision{Marker: DecisionMarker, Version: DecisionVersion, Family: "codex", ItemID: read.Item.ID, Scope: "machine",
		ScopeDigest: ScopeDigest("machine", ctx.MachineRoot), CatalogDigest: digest([]byte("synthetic old catalog")),
		ItemDigest: digest(old), Kind: "item", Choice: "declined", Evidence: "synthetic update decline",
		Adopted: []TargetDigest{{"skill", digest(old)}}}
	inspect(&d, "outdated")
	if err := os.WriteFile(filename, []byte("synthetic local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	inspect(&d, "edited")
	if err := os.WriteFile(filename, []byte(read.Item.Content), 0600); err != nil {
		t.Fatal(err)
	}
	inspect(nil, "present")
	if state := (Inspector{}).InspectItem(c, InspectionContext{Family: "claude", Scope: "machine", ProjectRoot: ctx.ProjectRoot}, read.Item.ID, nil); state.State != "" || state.Reason != "unsupported_scope" {
		t.Fatalf("unverified native discovery became a content state: %+v", state)
	}
}

type countingReadRoot struct {
	ReadRoot
	reads *int
}

func (r countingReadRoot) ReadRegular(name string, limit int) ([]byte, error) {
	*r.reads++
	return r.ReadRoot.ReadRegular(name, limit)
}

func TestDiscoveryDirectoryConflictDoesNotTraverseOrModify(t *testing.T) {
	c, _ := Embedded("dev")
	root, outside := t.TempDir(), t.TempDir()
	custom := filepath.Join(root, ".agents", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(custom), 0700); err != nil {
		t.Fatal(err)
	}
	marker := []byte("synthetic adopter skill")
	if err := os.WriteFile(custom, marker, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".codex", "skills")); err != nil {
		t.Fatal(err)
	}
	reads := 0
	i := Inspector{OpenRoot: func(name string) (ReadRoot, error) {
		r, err := OpenReadRoot(name)
		if err != nil {
			return nil, err
		}
		return countingReadRoot{r, &reads}, nil
	}}
	state := i.InspectItem(c, InspectionContext{Family: "codex", Scope: "project", ProjectRoot: root, Repository: "/synthetic/common"}, "setup.discovery-links", nil)
	if state.Reason != "directory_conflict" || reads != 0 {
		t.Fatalf("directory conflict was traversed: %+v reads=%d", state, reads)
	}
	if state.Targets[1].Reason != "symlink_escape" {
		t.Fatalf("escaping link was inspected: %+v", state.Targets)
	}
	data, err := os.ReadFile(custom)
	if err != nil || string(data) != string(marker) {
		t.Fatal("adopter directory/file changed", err)
	}
}

func TestSidecarStateIsSeparateFromSkillBytes(t *testing.T) {
	c, _ := Embedded("dev")
	root := t.TempDir()
	ctx := InspectionContext{Family: "codex", Scope: "project", ProjectRoot: root, Repository: "/synthetic/common"}
	state := (Inspector{}).InspectItem(c, ctx, "skill.check", nil)
	if state.State != "missing" || state.Facts == nil || state.Facts.State != "missing-required" || len(state.Facts.Required) == 0 {
		t.Fatalf("required sidecar was hidden by skill state: %+v", state)
	}
	read, _ := c.Read("codex", "skill.check")
	file := filepath.Join(root, read.Item.Facts.Filename)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	facts := "# Synthetic facts\n\n## Version\n1\n## Skill\ncheck\n## Sources\n- README.md\n"
	if err := os.WriteFile(file, []byte(facts), 0600); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, "skill.check", nil)
	if state.Facts.State != "incomplete" {
		t.Fatalf("missing headings not reported: %+v", state.Facts)
	}
	for _, field := range read.Item.Facts.Fields {
		if field.Required {
			facts += "## " + field.Heading + "\nsynthetic local value\n"
		}
	}
	if err := os.WriteFile(file, []byte(facts), 0600); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, "skill.check", nil)
	if state.Facts.State != "present-valid" || state.Facts.Digest == "" || state.State != "missing" {
		t.Fatalf("fact/skill states were conflated: %+v", state)
	}
}

func TestCollisionKeepsScopeStateSeparateAndPreservesCopies(t *testing.T) {
	c, _ := Embedded("dev")
	ctx := InspectionContext{Family: "codex", Scope: "machine", MachineRoot: t.TempDir(), ProjectRoot: t.TempDir(), UserRoot: t.TempDir(), Repository: "/synthetic/common"}
	read, _ := c.Read("codex", "skill.pair")
	files := []string{}
	for _, scope := range []string{"machine", "project"} {
		targets, _ := c.Targets("codex", scope, read.Item.ID)
		root := ctx.MachineRoot
		if scope == "project" {
			root = ctx.ProjectRoot
		}
		file := filepath.Join(root, filepath.FromSlash(targets[0].Path))
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(read.Item.Content), 0600); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	state := (Inspector{}).InspectItem(c, ctx, read.Item.ID, nil)
	if state.State != "present" || state.Collision != "duplicate_discovery" || state.CollisionNote != "" {
		t.Fatalf("matching copies: %+v", state)
	}
	custom := []byte("synthetic project workflow")
	if err := os.WriteFile(files[1], custom, 0600); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, nil)
	if state.State != "present" || state.Collision != "name_conflict" || state.Recommended {
		t.Fatalf("differing copy conflated with selected bytes: %+v", state)
	}
	data, _ := os.ReadFile(files[1])
	if string(data) != string(custom) {
		t.Fatal("inspection changed project workflow")
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, nil)
	if state.State != "missing" || state.Collision != "name_conflict" || state.Recommended {
		t.Fatalf("absent selected copy concealed incoming catalog conflict: %+v", state)
	}
	ctx.ProjectRoot = ""
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, nil)
	if state.CollisionNote != "collision_scope_unavailable" || state.Recommended {
		t.Fatalf("unavailable project became no collision: %+v", state)
	}
}

func TestCollectionAddedTargetOutdatedAndAggregateReadBound(t *testing.T) {
	c, _ := Embedded("dev")
	read, _ := c.Read("codex", "setup.opencode-commands")
	var collection struct {
		Ownership string    `json:"ownership"`
		Commands  []Command `json:"commands"`
	}
	json.Unmarshal([]byte(read.Item.Content), &collection)
	collection.Commands = collection.Commands[:2]
	encoded, _ := json.Marshal(collection)
	read.Item.Content = string(encoded)
	c.Items = []Item{read.Item}
	ctx := InspectionContext{Family: "codex", Scope: "project", ProjectRoot: t.TempDir(), Repository: "/synthetic/common"}
	targets, _ := c.Targets("codex", "project", read.Item.ID)
	first := filepath.Join(ctx.ProjectRoot, targets[0].Path)
	if err := os.MkdirAll(filepath.Dir(first), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, targets[0].Expected, 0600); err != nil {
		t.Fatal(err)
	}
	d := Decision{Marker: DecisionMarker, Version: DecisionVersion, ItemID: read.Item.ID, Family: "codex", Scope: "project", ScopeDigest: ScopeDigest("project", ctx.Repository), CatalogDigest: digest([]byte("synthetic old catalog")), ItemDigest: digest([]byte("synthetic old collection")), Kind: "item", Choice: "adopted", Evidence: "synthetic old collection evidence", Result: []TargetDigest{{targets[0].ID, digest(targets[0].Expected)}}, Adopted: []TargetDigest{{targets[0].ID, digest(targets[0].Expected)}}}
	state := (Inspector{}).InspectItem(c, ctx, read.Item.ID, &d)
	if state.State != "outdated" || state.Targets[1].State != "missing" {
		t.Fatalf("new collection target: %+v", state)
	}
	if err := os.WriteFile(first, []byte("synthetic local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, &d)
	if state.State != "edited" {
		t.Fatalf("local edit did not win: %+v", state)
	}
	for _, target := range targets {
		if err := os.WriteFile(filepath.Join(ctx.ProjectRoot, target.Path), []byte(strings.Repeat("x", 40<<10)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, nil)
	if state.Reason != "item_too_large" || state.Targets[1].Reason != "item_too_large" {
		t.Fatalf("collection read more than item bound: %+v", state)
	}
}

func TestCustomAbsoluteDiscoveryLinkInsideRootIsEdited(t *testing.T) {
	c, _ := Embedded("dev")
	root := t.TempDir()
	custom := filepath.Join(root, "custom-skills")
	if err := os.Mkdir(custom, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(custom, filepath.Join(root, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	state := (Inspector{}).InspectItem(c, InspectionContext{Family: "codex", Scope: "project", ProjectRoot: root, Repository: "/synthetic/common"}, "setup.discovery-links", nil)
	if state.Targets[0].State != "edited" || state.Targets[0].Reason != "" {
		t.Fatalf("in-root custom link called an escape: %+v", state.Targets[0])
	}
	link, err := os.Readlink(filepath.Join(root, ".agents", "skills"))
	if err != nil || link != custom {
		t.Fatal("custom link changed", err)
	}
}

func TestSharedDiscoveryAliasesDoNotCreateAnUpdateConflict(t *testing.T) {
	c, _ := Embedded("dev")
	root := t.TempDir()
	ctx := InspectionContext{Family: "codex", Scope: "project", ProjectRoot: root, Repository: "/synthetic/common", MachineRoot: t.TempDir(), UserRoot: t.TempDir()}
	read, _ := c.Read("codex", "skill.pair")
	targets, _ := c.Targets("codex", "project", read.Item.ID)
	file := filepath.Join(root, targets[0].Path)
	old := []byte("synthetic unchanged adopted version")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, old, 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".agents", ".codex"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../agents/skills", filepath.Join(root, dir, "skills")); err != nil {
			t.Fatal(err)
		}
	}
	d := Decision{Marker: DecisionMarker, Version: DecisionVersion, ItemID: read.Item.ID, Family: "codex", Scope: "project", ScopeDigest: ScopeDigest("project", ctx.Repository), CatalogDigest: digest([]byte("synthetic old catalog")), ItemDigest: digest(old), Kind: "item", Choice: "adopted", Evidence: "synthetic old adoption", Result: []TargetDigest{{ID: "skill", Digest: digest(old)}}, Adopted: []TargetDigest{{ID: "skill", Digest: digest(old)}}}
	state := (Inspector{}).InspectItem(c, ctx, read.Item.ID, &d)
	if state.State != "outdated" || state.Collision != "" || state.CollisionNote != "" || !state.Recommended {
		t.Fatalf("aliases introduced an unsourced conflict: %+v", state)
	}
	// A separate shared-user copy of those old bytes would remain stale after a
	// project-only update and must be included in the proposed-change comparison.
	other := filepath.Join(ctx.UserRoot, ".agents", "skills", "pair", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, old, 0600); err != nil {
		t.Fatal(err)
	}
	state = (Inspector{}).InspectItem(c, ctx, read.Item.ID, &d)
	if state.Collision != "name_conflict" || state.Recommended {
		t.Fatalf("independent stale copy concealed: %+v", state)
	}
}
