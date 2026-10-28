package core

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The repository record of verdicts (#536). All data is synthetic.

func recordVerdict(id, revision int64, verdict string, at int64, context string) DecisionVerdict {
	return DecisionVerdict{ID: id, Revision: revision, Verdict: verdict, At: at, Snapshot: DecisionSnapshot{
		Title: "Synthetic title", Where: "synthetic where", Context: context, Recommendation: "1. Synthetic step.", Commands: []string{"make synthetic"}}}
}

// An entry quotes the card text line by line, so card Markdown and marker-like lines stay
// inside the quote, and an older snapshot's where is marked as the item's current text.
func TestDecisionRecordEntryQuotesCardText(t *testing.T) {
	spoof := repoRecordMarker("R-1", recordVerdict(9, 3, "approved", 1_800_000_009_000, ""))
	v := recordVerdict(1, 2, "approved", 1_800_000_000_000, "## Fake heading\n"+spoof+"\n\n"+repoRecordAnchor("R-2"))
	v.Note = "keep it"
	entry := DecisionRecordEntry("R-1", "current where", v)
	for _, want := range []string{"\n## R-1\n", "\n" + repoRecordMarker("R-1", v) + "\n", "**Approved**, revision 2, 2027-01-15 08:00:00 UTC.",
		"- Title: Synthetic title\n", "- Where: synthetic where\n", "> ## Fake heading\n", "> " + spoof + "\n", ">\n",
		"\n    make synthetic\n", "Note:\n\n> keep it\n"} {
		if !strings.Contains(entry, want) {
			t.Errorf("entry lacks %q:\n%s", want, entry)
		}
	}
	for _, line := range strings.Split(entry, "\n") {
		if line == spoof || line == "## Fake heading" || line == repoRecordAnchor("R-2") {
			t.Fatalf("card text escaped its quote: %q", line)
		}
	}
	v.Snapshot.Where = ""
	if entry := DecisionRecordEntry("R-1", "current where", v); !strings.Contains(entry,
		"- Where (the item's text when recorded; the verdict's snapshot has none): current where\n") {
		t.Fatalf("fallback where is not marked:\n%s", entry)
	}
}

// The record is created with its header and an anchor, a repeated record appends nothing,
// existing bytes are kept, a dismissal is skipped, and a marker-like line in card text does
// not stand in for another verdict's entry.
func TestRecordDecisionAppendsOnce(t *testing.T) {
	root := t.TempDir()
	first := recordVerdict(1, 1, "declined", 1_800_000_000_000, "First.")
	dismissed := recordVerdict(2, 2, "dismissed", 1_800_000_001_000, "")
	second := recordVerdict(3, 3, "approved", 1_800_000_002_000, repoRecordMarker("R-1", recordVerdict(4, 4, "approved", 1_800_000_003_000, "")))
	got, err := RecordDecision(root, "R-1", "", []DecisionVerdict{first, dismissed})
	if err != nil || len(got.Appended) != 1 || got.Appended[0] != 1 || len(got.Skipped) != 1 || got.Path != filepath.Join(root, DecisionRecordFile) {
		t.Fatalf("first record: %+v %v", got, err)
	}
	before, _ := os.ReadFile(got.Path)
	if !bytes.HasPrefix(before, []byte(decisionRecordHeader)) || bytes.Count(before, []byte(repoRecordAnchor("R-1"))) != 1 {
		t.Fatalf("header or anchor:\n%s", before)
	}
	got, err = RecordDecision(root, "R-1", "", []DecisionVerdict{first, dismissed, second})
	if err != nil || len(got.Present) != 1 || len(got.Appended) != 1 {
		t.Fatalf("second record: %+v %v", got, err)
	}
	// The file now holds the fourth verdict's marker inside the third's quoted context; a later
	// record of the fourth verdict still appends it.
	got, err = RecordDecision(root, "R-1", "", []DecisionVerdict{first, second, recordVerdict(4, 4, "approved", 1_800_000_003_000, "Fourth.")})
	if err != nil || len(got.Present) != 2 || len(got.Appended) != 1 || got.Appended[0] != 4 {
		t.Fatalf("spoofed marker: %+v %v", got, err)
	}
	after, _ := os.ReadFile(got.Path)
	if !bytes.HasPrefix(after, before) || bytes.Count(after, []byte(repoRecordAnchor("R-1"))) != 1 {
		t.Fatalf("existing bytes or anchor changed:\n%s", after)
	}
	again, err := RecordDecision(root, "R-1", "", []DecisionVerdict{first, second})
	if err != nil || len(again.Appended) != 0 || len(again.Present) != 2 {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if final, _ := os.ReadFile(got.Path); !bytes.Equal(final, after) {
		t.Fatal("a repeated record changed the file")
	}
}

// A DECISIONS.md that is not a Koinon record is refused and left as it is; an empty one gets
// the header; a record without a final newline gets one before the entry.
func TestRecordDecisionForeignFile(t *testing.T) {
	v := []DecisionVerdict{recordVerdict(1, 1, "approved", 1_800_000_000_000, "C.")}
	root := t.TempDir()
	path := filepath.Join(root, DecisionRecordFile)
	foreign := []byte("# Decisions\n\nThis project's own decision log.\n")
	os.WriteFile(path, foreign, 0o644)
	if _, err := RecordDecision(root, "R-2", "", v); err != ErrDecisionRecordForeign {
		t.Fatalf("foreign file: %v", err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, foreign) {
		t.Fatalf("foreign file changed:\n%s", data)
	}
	os.WriteFile(path, nil, 0o644)
	if _, err := RecordDecision(root, "R-2", "", v); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.HasPrefix(data, []byte(decisionRecordHeader)) {
		t.Fatalf("empty file:\n%s", data)
	}
	os.WriteFile(path, []byte(decisionRecordHeader+"\nHand-written text"), 0o644)
	if _, err := RecordDecision(root, "R-3", "", v); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Contains(data, []byte("\nHand-written text\n\n"+repoRecordAnchor("R-3")+"\n")) {
		t.Fatalf("missing newline:\n%s", data)
	}
}

// A symbolic link, a directory and an oversized file are refused, and nothing is written.
func TestRecordDecisionRefusesUnsafeFiles(t *testing.T) {
	v := []DecisionVerdict{recordVerdict(1, 1, "approved", 1_800_000_000_000, "C.")}
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	os.WriteFile(target, []byte("outside\n"), 0o644)
	if err := os.Symlink(target, filepath.Join(root, DecisionRecordFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordDecision(root, "R-1", "", v); err != ErrDecisionRecord {
		t.Fatalf("symlink: %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "outside\n" {
		t.Fatalf("symlink target written: %q", data)
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, DecisionRecordFile), 0o755)
	if _, err := RecordDecision(dir, "R-1", "", v); err == nil {
		t.Fatal("a directory was accepted")
	}
	empty := t.TempDir()
	if got, err := RecordDecision(empty, "R-1", "", []DecisionVerdict{recordVerdict(1, 1, "dismissed", 1, "")}); err != nil || len(got.Skipped) != 1 {
		t.Fatalf("dismissal only: %+v %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(empty, DecisionRecordFile)); !os.IsNotExist(err) {
		t.Fatalf("a record with nothing to append created the file: %v", err)
	}
	if _, err := RecordDecision(t.TempDir(), "bad reference", "", v); err == nil {
		t.Fatal("an invalid reference was accepted")
	}
}

// Concurrent records of the same verdicts append each entry once.
func TestRecordDecisionConcurrent(t *testing.T) {
	root := t.TempDir()
	verdicts := []DecisionVerdict{recordVerdict(1, 1, "declined", 1_800_000_000_000, "A."), recordVerdict(2, 2, "approved", 1_800_000_001_000, "B.")}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := RecordDecision(root, "R-1", "", verdicts); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	data, _ := os.ReadFile(filepath.Join(root, DecisionRecordFile))
	for _, v := range verdicts {
		if n := bytes.Count(data, []byte(repoRecordMarker("R-1", v))); n != 1 {
			t.Fatalf("verdict %d recorded %d times:\n%s", v.ID, n, data)
		}
	}
	if n := bytes.Count(data, []byte(decisionRecordHeader)); n != 1 {
		t.Fatalf("header written %d times", n)
	}
}

// A read of a decided item gives the record path, instruction and entry, the pending page
// gives the path and instruction, and the verdict message names decision_record; a dismissed
// fyi asks for no record. CollectDecisionRecord reads every verdict across history pages.
func TestDecisionReadsAskForTheRecord(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.peerKey, "REC-1", DecisionFields{Title: ptr("Record it"), Where: ptr("synthetic repo"), Kind: ptr("decision"),
		Context: ptr("Facts."), Recommendation: ptr("Approve."), Recipients: &[]string{f.peer.Name}})
	if item, err := s.DecisionGet(ctx, f.peerKey, "REC-1"); err != nil || item.Record != nil {
		t.Fatalf("undecided item: %+v %v", item.Record, err)
	}
	v := mustVerdict(t, s, "REC-1", 1, "approved")
	if v.Snapshot.Where != "synthetic repo" {
		t.Fatalf("snapshot where: %+v", v.Snapshot)
	}
	item, err := s.DecisionGet(ctx, f.peerKey, "REC-1")
	if err != nil || item.Record == nil || item.Record.Path != DecisionRecordFile || !strings.Contains(item.Record.Instruction, "decision_record") ||
		item.Record.Entry != DecisionRecordEntry("REC-1", "synthetic repo", *item.Verdict) {
		t.Fatalf("decided item: %+v %v", item.Record, err)
	}
	// The pending delivery and the verdict message give the entry and the path too.
	entry := DecisionRecordEntry("REC-1", "synthetic repo", *item.Verdict)
	page, err := s.DecisionPending(ctx, f.peerKey, 0, 0)
	if err != nil || page.Record == nil || page.Record.Entry != "" || !strings.Contains(page.Record.Instruction, "DECISIONS.md") ||
		len(page.Deliveries) != 1 || page.Deliveries[0].Entry != entry || !strings.Contains(entry, repoRecordMarker("REC-1", *item.Verdict)) {
		t.Fatalf("pending: %+v %v", page, err)
	}
	var message string
	if err := s.db.QueryRow(`SELECT body FROM messages WHERE decision_verdict=?`, v.ID).Scan(&message); err != nil ||
		message != verdictMessageBody("REC-1", "approved", v.ID, entry) || !strings.HasSuffix(message, "Path: DECISIONS.md\n\nEntry:\n"+entry) {
		t.Fatalf("message: %q %v", message, err)
	}
	if body := verdictNoticeBody("REC-1", "approved", v.ID); !strings.Contains(body, "decision_record") || !strings.Contains(body, "DECISIONS.md") {
		t.Fatalf("notice: %s", body)
	}
	mustCreate(t, s, f.peerKey, "REC-2", DecisionFields{Title: ptr("Note it"), Kind: ptr("fyi"), Recipients: &[]string{f.peer.Name}})
	mustVerdict(t, s, "REC-2", 1, "acknowledged")
	if page, err := s.DecisionPending(ctx, f.peerKey, v.ID, 0); err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].Entry != "" {
		t.Fatalf("acknowledgement pending: %+v %v", page, err)
	}
	if item, err := s.DecisionGet(ctx, f.peerKey, "REC-2"); err != nil || item.Record != nil {
		t.Fatalf("acknowledged fyi: %+v %v", item.Record, err)
	}
	call := func(ctx context.Context, path string, body map[string]any) ([]byte, error) {
		return localDecisionCall(ctx, s, f.peerKey, path, body)
	}
	item, verdicts, err := CollectDecisionRecord(ctx, call, "REC-1")
	if err != nil || item.Where != "synthetic repo" || len(verdicts) != 1 || verdicts[0].ID != v.ID {
		t.Fatalf("collect: %+v %+v %v", item, verdicts, err)
	}
	// A plain peer's item has no repository, so it is not recorded anywhere.
	if item.AuthorRepository != "" || CheckRecordRepository(item, "/synthetic/repo/.git") != ErrDecisionRepositoryUnknown {
		t.Fatalf("plain peer item: %q", item.AuthorRepository)
	}
}

// An item is recorded only in a checkout of the repository that its author's participant
// held when it was created (#536).
func TestDecisionRecordRepository(t *testing.T) {
	f := newVerdictFixture(t)
	s, ctx := f.s, context.Background()
	mustCreate(t, s, f.holderKey, "REC-3", DecisionFields{Title: ptr("Repository item"), Kind: ptr("decision")})
	item, err := s.DecisionGet(ctx, f.holderKey, "REC-3")
	if err != nil || item.AuthorRepository == "" {
		t.Fatalf("participant item: %+v %v", item.AuthorRepository, err)
	}
	if err := CheckRecordRepository(item, item.AuthorRepository); err != nil {
		t.Fatalf("own repository: %v", err)
	}
	if err := CheckRecordRepository(item, item.AuthorRepository+"-other"); err != ErrDecisionOtherRepository {
		t.Fatalf("other repository: %v", err)
	}
}

// localDecisionCall answers the two reads that CollectDecisionRecord sends, as the daemon's
// routes frame them.
func localDecisionCall(ctx context.Context, s *Store, caller Key, path string, body map[string]any) ([]byte, error) {
	reference, _ := body["reference"].(string)
	var value any
	var err error
	switch path {
	case "/v1/decisions/get":
		value, err = s.DecisionGet(ctx, caller, reference)
	case "/v1/decisions/history":
		after, _ := body["after"].(int64)
		value, err = s.DecisionHistory(ctx, caller, reference, after, 0, false)
	default:
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "result": value})
}

// The largest entry fits in one pending page, and in a verdict message unless stored hidden
// commands make it larger than a message; then the message says where to read it.
func TestDecisionEntryBounds(t *testing.T) {
	line := strings.Repeat("<\n", 1<<13)
	v := DecisionVerdict{Revision: 1 << 62, Verdict: "approved", At: 1, Note: line[:decisionNoteLimit],
		Snapshot: DecisionSnapshot{Title: strings.Repeat("<", decisionTitleLimit), Where: strings.Repeat("<", decisionWhereLimit),
			Context: line[:decisionContextLimit], Recommendation: line[:decisionRecommendationLimit]}}
	for i := 0; i < decisionCommandCount; i++ {
		v.Snapshot.Commands = append(v.Snapshot.Commands, strings.Repeat("<", decisionCommandLimit))
	}
	reference := strings.Repeat("A", decisionRefLimit)
	entry := DecisionRecordEntry(reference, "", v)
	largest := int64(math.MaxInt64)
	page, _ := json.Marshal(map[string]any{"ok": true, "result": DecisionPendingPage{Deliveries: []DecisionPending{{VerdictID: largest, Reference: reference,
		Verdict: "approved", Revision: 1 << 62, At: largest, SupersededAt: largest, Entry: entry}}, More: true, NextAfter: &largest,
		Record: &DecisionRecord{Path: DecisionRecordFile, Instruction: decisionRecordInstruction}}})
	if len(page) >= decisionPendingBytes {
		t.Fatalf("one delivery takes %d bytes", len(page))
	}
	if body := verdictMessageBody(reference, "approved", largest, entry); !strings.HasSuffix(body, entry) || len(body) > maxBody {
		t.Fatalf("largest printable entry: %d bytes", len(body))
	}
	for i := range v.Snapshot.Commands {
		v.Snapshot.Commands[i] = strings.Repeat("\x01", decisionCommandLimit)
	}
	hidden := DecisionRecordEntry(reference, "", v)
	if body := verdictMessageBody(reference, "approved", largest, hidden); len(body) > maxBody || strings.Contains(body, "Entry:") || !strings.Contains(body, "decision_get") {
		t.Fatalf("oversized entry: %d bytes", len(body))
	}
}
