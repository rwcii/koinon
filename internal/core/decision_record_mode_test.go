package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	var part []byte
	at := DecisionExportAt{}
	for i := 0; i < 1000; i++ {
		page, err := s.DecisionExport(context.Background(), caller, at, limit)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, page.Version)
		if page.Part != nil {
			if page.Part.Offset != int64(len(part)) || len(page.Records) > 0 {
				t.Fatalf("part at %d after %d bytes", page.Part.Offset, len(part))
			}
			part = append(part, page.Part.Data...)
			if page.Part.Final {
				page.Records, part = []json.RawMessage{part}, nil
			}
		}
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
	whole, _ := exportAll(t, s, f.peerKey, 0)
	for _, c := range []struct {
		limit int
		bytes int64
	}{{1, decisionExportBytes}, {0, decisionExportBytes}, {0, 100}, {2, 1000}} {
		limit := c.limit
		decisionExportBytes, c.bytes = c.bytes, decisionExportBytes
		records, versions := exportAll(t, s, f.peerKey, limit)
		decisionExportBytes = c.bytes
		if !reflect.DeepEqual(records, whole) {
			t.Fatalf("limit %d, page bytes %d: records differ from one page", limit, c.bytes)
		}
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
	if _, err := fixtureExec(s, `UPDATE decision_deep_archive SET length=length-1`); err != nil {
		t.Fatal(err)
	}
	for _, at := range []DecisionExportAt{{Phase: exportLive, AfterID: 1 << 40, Offset: 1}, {Phase: exportDeep, Offset: 1 << 30},
		{Phase: exportDeep, AfterReference: "D-1", Offset: 1}, {Phase: exportLive, Offset: -1}} {
		if _, err := s.DecisionExport(ctx, f.peerKey, at, 0); refusalCode(err) != "invalid_request" {
			t.Errorf("cursor %+v: %v", at, err)
		}
	}
	if _, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{Phase: "other"}, 0); err == nil {
		t.Fatal("unknown phase")
	}
	if _, err := s.DecisionExport(ctx, Key{"claude", "synthetic-unknown"}, DecisionExportAt{}, 0); err == nil {
		t.Fatal("unregistered caller")
	}
}

// A record larger than the client's transport limit arrives in parts through the export route
// and Call: 85 verdict snapshots of a 16 KiB context make one item of more than 8 MiB, and its
// parts put it together byte for byte.
func TestDecisionExportLargeRecordCrossesTransport(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	created := mustCreate(t, s, f.peerKey, "L-1", DecisionFields{Title: ptr("Large history"), Kind: ptr("decision"),
		Context: ptr(strings.Repeat("<", decisionContextLimit)), Recipients: &[]string{f.peer.Name}})
	revision := created.Revision
	var last DecisionVerdict
	for i := 0; i < 85; i++ {
		last = mustVerdict(t, s, "L-1", revision, "approved")
		if i < 84 {
			item, err := s.DecisionEdit(ctx, f.peerKey, "L-1", revision, DecisionFields{Title: ptr("Large history")})
			if err != nil {
				t.Fatal(err)
			}
			revision = item.Revision
		}
	}
	mustCreate(t, s, f.peerKey, "L-2", DecisionFields{Title: ptr("Small"), Kind: ptr("decision"), Recipients: &[]string{f.peer.Name}})
	// An acknowledgement between parts changes the record at one board version; the next part
	// names another digest.
	first, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{Phase: exportLive}, 0)
	if err != nil || first.Part == nil || first.Part.Final || len(first.Part.Digest) != 64 {
		t.Fatalf("first part: %+v %v", first.Part, err)
	}
	if acked, err := s.DecisionAck(ctx, f.peerKey, []int64{last.ID}); err != nil || len(acked) != 1 {
		t.Fatalf("ack: %v %v", acked, err)
	}
	second, err := s.DecisionExport(ctx, f.peerKey, *first.Next, 0)
	if err != nil || second.Version != first.Version || second.Part == nil || second.Part.Digest == first.Part.Digest {
		t.Fatalf("part after an ack: %v %q %q", err, first.Version, second.Version)
	}
	mux := http.NewServeMux()
	(&Daemon{store: s}).decisionRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	var lines [][]byte
	var part []byte
	parts := 0
	at := DecisionExportAt{}
	for i := 0; ; i++ {
		if i == 100 {
			t.Fatal("the export did not end")
		}
		data, err := Call(ctx, address, "synthetic-secret", "/v1/decisions/export", map[string]any{"caller": f.peerKey,
			"phase": at.Phase, "after_reference": at.AfterReference, "after_id": at.AfterID, "offset": at.Offset})
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		var reply struct {
			Result DecisionExportPage `json:"result"`
		}
		if err := json.Unmarshal(data, &reply); err != nil {
			t.Fatal(err)
		}
		page := reply.Result
		if p := page.Part; p != nil {
			if p.Offset != int64(len(part)) || len(page.Records) > 0 || p.Digest != second.Part.Digest {
				t.Fatalf("part at %d after %d bytes", p.Offset, len(part))
			}
			part = append(part, p.Data...)
			parts++
			if p.Final {
				lines, part = append(lines, part), nil
			}
		}
		for _, record := range page.Records {
			lines = append(lines, record)
		}
		if !page.More {
			break
		}
		at = *page.Next
	}
	if parts < 3 || len(lines) != 2 || !strings.Contains(string(lines[1]), `"reference":"L-2"`) {
		t.Fatalf("%d parts, %d records", parts, len(lines))
	}
	// The whole record, read without the page bound.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM decision_items WHERE reference='L-1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	record, err := readDecisionRecord(ctx, tx, id)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(want)
	if len(want) <= maxResponse || !bytes.Equal(lines[0], want) || hex.EncodeToString(sum[:]) != second.Part.Digest {
		t.Fatalf("record of %d bytes, assembled %d bytes", len(want), len(lines[0]))
	}
}
