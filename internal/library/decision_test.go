package library

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDecisionRoundTripKeepsAnAdoptedBaselineThroughDecline(t *testing.T) {
	d := Decision{Marker: DecisionMarker, Version: DecisionVersion, ItemID: "skill.koinon-guide", Family: "codex",
		Scope: "machine", ScopeDigest: digest([]byte("synthetic scope")), CatalogDigest: digest([]byte("synthetic catalog")),
		ItemDigest: digest([]byte("synthetic new item")), Kind: "item", Choice: "declined", Evidence: "synthetic reviewed choice",
		Adopted: []TargetDigest{{"skill", digest([]byte("synthetic old adopted bytes"))}}}
	encoded, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	read, err := ParseDecision(encoded)
	if err != nil || read.Choice != "declined" || !reflect.DeepEqual(read.Adopted, d.Adopted) {
		t.Fatalf("decline lost the baseline: %+v %v", read, err)
	}
	// An unknown path/identity field cannot become part of this versioned receipt.
	withPath := bytes.Replace(encoded, []byte(`"version":1`), []byte(`"version":1,"path":"/synthetic/outside"`), 1)
	if _, err := ParseDecision(withPath); err == nil {
		t.Fatal("receipt accepted an arbitrary path")
	}
	// A removed item remains readable as inert data; no catalog lookup or file read occurs.
	d.ItemID = "skill.synthetic-removed"
	encoded, err = d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseDecision(encoded); err != nil {
		t.Fatal("removed-item receipt was discarded", err)
	}
}

func TestDecisionCanonicalTargetOrderDoesNotModifyCallerData(t *testing.T) {
	d := Decision{Marker: DecisionMarker, Version: DecisionVersion, ItemID: "setup.discovery-links", Family: "codex",
		Scope: "project", ScopeDigest: digest([]byte("synthetic scope")), CatalogDigest: digest([]byte("synthetic catalog")),
		ItemDigest: digest([]byte("synthetic item")), Kind: "item", Choice: "adopted", Evidence: "synthetic reviewed change",
		Result: []TargetDigest{{"link.codex", digest([]byte("codex"))}, {"link.agents", digest([]byte("agents"))}}}
	one, err := d.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if d.Result[0].ID != "link.codex" {
		t.Fatal("canonical encoding mutated the caller's target order")
	}
	d.Result[0], d.Result[1] = d.Result[1], d.Result[0]
	two, err := d.Encode()
	if err != nil || !bytes.Equal(one, two) {
		t.Fatal("equivalent target order would produce a different CAS body", err)
	}
}

func TestCatalogTargetsCoverActualItemsAndDoNotUseReceiptPaths(t *testing.T) {
	c, err := Embedded("dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range c.Items {
		targets, err := c.Targets("codex", "project", item.ID)
		if err != nil || len(targets) == 0 || len(targets) > MaxTargets {
			t.Fatalf("%s targets: %v %v", item.ID, targets, err)
		}
		for _, target := range targets {
			if !safeCatalogPath(target.Path) || !targetPattern.MatchString(target.ID) || len(target.Expected) == 0 {
				t.Fatalf("invalid catalog target: %+v", target)
			}
		}
		if err := CheckTargets(targets, []TargetDigest{{"unknown.target", strings.Repeat("a", 64)}}); err == nil {
			t.Fatal("unknown receipt target was resolved")
		}
	}
}

func TestReadRootKeepsEscapingLinksAndNonregularTargetsUnread(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	foreign := filepath.Join(outside, "foreign")
	if err := os.WriteFile(foreign, []byte("synthetic foreign content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(root, "leaf")); err != nil {
		t.Fatal(err)
	}
	r, err := OpenReadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, target := range []string{"escape/foreign", "leaf", "."} {
		if data, err := r.ReadRegular(target, MaxItemBytes); err == nil || len(data) != 0 {
			t.Fatalf("read an escaping/nonregular target %q: %q %v", target, data, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "owned"), []byte("synthetic declared file"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := r.ReadRegular("owned", MaxItemBytes); err != nil || string(data) != "synthetic declared file" {
		t.Fatalf("declared regular file: %q %v", data, err)
	}
}
