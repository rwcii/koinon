package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Record modes and the board's export (#589). All data is synthetic.

func writeShipFacts(t *testing.T, root, fact string) {
	t.Helper()
	text := "# Repository facts — ship\n\n## Version\n1\n## Skill\nship\n## Sources\n- AGENTS.md\n\n## Work target\nsynthetic\n" +
		"## Commit policy\nsynthetic\n## Required checks\nsynthetic\n## PR and merge policy\nsynthetic\n" + fact
	if err := os.MkdirAll(filepath.Join(root, ".koinon", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, shipFacts), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The mode is the first word of the Decision record fact; an absent file or fact is none, and
// an unknown word or an invalid facts file is refused.
func TestDecisionRecordMode(t *testing.T) {
	for _, c := range []struct{ name, fact, want string }{
		{"absent file", "-", RecordNone},
		{"absent fact", "", RecordNone},
		{"none", "## Decision record\nnone\n", RecordNone},
		{"short with text", "## Decision record\nshort: the board holds the card text.\n", RecordShort},
		{"full", "## Decision record\nfull\n", RecordFull},
		{"unknown", "## Decision record\nsometimes\n", ""},
		{"invalid file", "## Unknown heading\nx\n", ""},
	} {
		root := t.TempDir()
		if c.fact != "-" {
			writeShipFacts(t, root, c.fact)
		}
		got, err := DecisionRecordMode(root)
		if c.want == "" {
			if refusalCode(err) != ErrDecisionRecordMode.Code {
				t.Errorf("%s: %q %v", c.name, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %q %v", c.name, got, err)
		}
	}
}

// A short entry has the verdict, the title, the recommendation's heading and the note word for
// word, and no card text; a note that chooses another option stays as the maintainer wrote it.
func TestDecisionRecordShortEntry(t *testing.T) {
	v := recordVerdict(1, 2, "approved", 1_800_000_000_000, "Synthetic context.")
	v.Snapshot.Recommendation = "## Recommendation: take option A\n\n**What:** synthetic steps."
	v.Note = "Take option B instead.\n\nKeep the rest."
	entry := DecisionRecordShortEntry("R-1", v)
	for _, want := range []string{"\n## R-1\n", repoRecordMarker("R-1", v), "**Approved**, revision 2,", "- Title: Synthetic title\n",
		"- Recommendation: take option A\n", "Note:\n\n> Take option B instead.\n>\n> Keep the rest.\n"} {
		if !strings.Contains(entry, want) {
			t.Errorf("short entry lacks %q:\n%s", want, entry)
		}
	}
	for _, unwanted := range []string{"Synthetic context.", "synthetic steps", "make synthetic", "Chosen"} {
		if strings.Contains(entry, unwanted) {
			t.Errorf("short entry has %q:\n%s", unwanted, entry)
		}
	}
	v.Verdict, v.Note = "feedback", "Change the scope."
	if entry := DecisionRecordShortEntry("R-1", v); !strings.Contains(entry, "Feedback:\n\n> Change the scope.\n") {
		t.Errorf("feedback:\n%s", entry)
	}
	if got := recommendationHeading("\n\nA plain first line\nmore"); got != "A plain first line" {
		t.Errorf("plain heading: %q", got)
	}
	if got := recommendationHeading(strings.Repeat("é", 400)); len(got) > 304 || !strings.HasSuffix(got, "...") {
		t.Errorf("long heading: %d bytes", len(got))
	}
}

// none opens no record file, also when a link or a file in another format is there; short and
// full share the marker, so a mode change does not record a verdict twice.
func TestRecordDecisionModes(t *testing.T) {
	v := []DecisionVerdict{recordVerdict(1, 1, "approved", 1_800_000_000_000, "C.")}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Symlink(target, filepath.Join(root, DecisionRecordFile)); err != nil {
		t.Fatal(err)
	}
	got, err := RecordDecision(root, RecordNone, "R-1", "", v)
	if err != nil || got.Mode != RecordNone || got.Path != "" || len(got.Appended) != 0 || got.Instruction != decisionRecordNone {
		t.Fatalf("none: %+v %v", got, err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("none wrote through the link: %v", err)
	}
	if _, err := RecordDecision(root, "sometimes", "R-1", "", v); err != ErrDecisionRecordMode {
		t.Fatalf("unknown mode: %v", err)
	}
	os.Remove(filepath.Join(root, DecisionRecordFile))
	if got, err := RecordDecision(root, RecordShort, "R-1", "", v); err != nil || len(got.Appended) != 1 || got.Instruction != decisionRecordCommit {
		t.Fatalf("short: %+v %v", got, err)
	}
	if got, err := RecordDecision(root, RecordFull, "R-1", "", v); err != nil || len(got.Appended) != 0 || len(got.Present) != 1 {
		t.Fatalf("full after short: %+v %v", got, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, DecisionRecordFile)); strings.Contains(string(data), "> C.") {
		t.Fatalf("a mode change added the full entry:\n%s", data)
	}
}

// No text that reaches an agent before decision_record tells it to commit DECISIONS.md: the
// repository's mode decides (#589).
func TestDecisionTextsLeaveTheRecordToTheMode(t *testing.T) {
	for name, text := range map[string]string{"instruction": decisionRecordInstruction, "decision": verdictNoticeBody("R-1", "approved", 1),
		"feedback": verdictNoticeBody("R-1", "feedback", 1), "message": verdictMessageBody("R-1", "approved", 1, "\n## R-1\n")} {
		if strings.Contains(text, "commit DECISIONS.md") || strings.Contains(text, "Path: DECISIONS.md") {
			t.Errorf("%s: %s", name, text)
		}
		if name != "message" && !strings.Contains(text, "decision_record") {
			t.Errorf("%s does not name decision_record: %s", name, text)
		}
	}
	if !strings.Contains(decisionRecordNone, "maintainer's words with their date") || !strings.Contains(decisionRecordNone, "the document that holds it") {
		t.Errorf("none: %s", decisionRecordNone)
	}
}

// exportAll pages the export from the start with limit and returns the records and the
// versions that the pages read.
func exportAll(t *testing.T, s *Store, caller Key, limit int) ([]map[string]any, []string) {
	t.Helper()
	var records []map[string]any
	var versions []string
	at := DecisionExportAt{}
	for i := 0; i < 100; i++ {
		page, err := s.DecisionExport(context.Background(), caller, at, limit)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, page.Version)
		for _, raw := range page.Records {
			var r map[string]any
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			records = append(records, r)
		}
		if !page.More {
			return records, versions
		}
		at = *page.Next
	}
	t.Fatal("the export did not end")
	return nil, nil
}

// The export has every item once, deep-archived and live, with its verdicts and notes, in the
// deep archive's line format; a restore between pages changes the version; a locator that
// does not match its line fails the page.
func TestDecisionExport(t *testing.T) {
	f, v := deepArchiveFixture(t)
	s, ctx := f.s, context.Background()
	if _, err := s.DecisionAck(ctx, f.peerKey, []int64{v.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.archiveDecisions(ctx); err != nil || count(t, s, `SELECT COUNT(*) FROM decision_deep_archive`) != 1 {
		t.Fatalf("move: %v", err)
	}
	mustCreate(t, s, f.peerKey, "D-2", DecisionFields{Title: ptr("Live"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
	mustCreate(t, s, f.peerKey, "D-3", DecisionFields{Title: ptr("Noted"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
	if _, err := s.DecisionVerdict(verdictCtx(), "D-3", 1, "approved", "Synthetic note."); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 0} {
		records, versions := exportAll(t, s, f.peerKey, limit)
		var refs []string
		for _, r := range records {
			if r["record"] != decisionRecordMarker {
				t.Fatalf("record format: %v", r)
			}
			refs = append(refs, r["reference"].(string))
		}
		if strings.Join(refs, ",") != "D-1,D-2,D-3" {
			t.Fatalf("limit %d: references %v", limit, refs)
		}
		for _, version := range versions {
			if version != versions[0] {
				t.Fatalf("versions differ without a change: %v", versions)
			}
		}
		line, _ := json.Marshal(records[0])
		noted, _ := json.Marshal(records[2])
		if !strings.Contains(string(line), "Old facts.") || !strings.Contains(string(noted), "Synthetic note.") {
			t.Fatalf("record content:\n%s\n%s", line, noted)
		}
	}
	// A restore between pages changes the version that the next page reads.
	first, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionRestore(ctx, f.peerKey, "D-1", 0); err != nil {
		t.Fatal(err)
	}
	next, err := s.DecisionExport(ctx, f.peerKey, *first.Next, 1)
	if err != nil || next.Version == first.Version {
		t.Fatalf("restore between pages: %v %q %q", err, first.Version, next.Version)
	}
	// The restored item is live, once.
	if records, _ := exportAll(t, s, f.peerKey, 0); len(records) != 3 || records[0]["reference"] != "D-1" {
		t.Fatalf("after the restore: %v", records)
	}
	*f.clock = f.clock.Add(31 * 24 * time.Hour)
	if err := s.archiveDecisions(ctx); err != nil || count(t, s, `SELECT COUNT(*) FROM decision_deep_archive`) != 1 {
		t.Fatalf("second move: %v", err)
	}
	if _, err := fixtureExec(s, `UPDATE decision_deep_archive SET length=length+1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{}, 0); refusalCode(err) != "decision_unavailable" {
		t.Fatalf("mismatched locator: %v", err)
	}
	if _, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{Phase: "other"}, 0); err == nil {
		t.Fatal("unknown phase")
	}
	if _, err := s.DecisionExport(ctx, Key{"claude", "synthetic-unknown"}, DecisionExportAt{}, 0); err == nil {
		t.Fatal("unregistered caller")
	}
}
