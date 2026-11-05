package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/library"
)

// The retired Decision record fact and the board's export (#589, #623). All data is synthetic.

// A ship facts file that still holds the retired Decision record fact, with any value, stays
// a valid facts file: Koinon ignores the value and writes no record file (#623).
func TestRetiredDecisionRecordFactStaysValid(t *testing.T) {
	catalog, err := library.Embedded("")
	if err != nil {
		t.Fatal(err)
	}
	var schema *library.Facts
	for _, item := range catalog.Items {
		if item.Kind == "skill" && item.Name == "ship" {
			schema = item.Facts
		}
	}
	if schema == nil {
		t.Fatal("the catalog declares no ship facts")
	}
	for _, fact := range []string{"", "## Decision record\nnone\n", "## Decision record\nfull\n", "## Decision record\nshort: the board holds the card text.\n",
		"## Decision record\nsometimes\n"} {
		text := "# Repository facts — ship\n\n## Version\n1\n## Skill\nship\n## Sources\n- AGENTS.md\n\n## Work target\nsynthetic\n" +
			"## Commit policy\nsynthetic\n## Required checks\nsynthetic\n## PR and merge policy\nsynthetic\n" + fact
		if _, err := library.ParseFactValues([]byte(text), "ship", schema); err != nil {
			t.Errorf("%q: %v", fact, err)
		}
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
	// A cursor inside a record: the version and the digest of its part, else refused.
	saved := decisionExportBytes
	decisionExportBytes = 100
	part, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{}, 0)
	decisionExportBytes = saved
	if err != nil || part.Part == nil || part.Next == nil || part.Next.Version != part.Version || part.Next.Digest != part.Part.Digest {
		t.Fatalf("part cursor: %+v %v", part.Next, err)
	}
	version, digest := part.Version, part.Part.Digest
	for _, c := range []struct {
		at   DecisionExportAt
		code string
	}{
		{DecisionExportAt{Phase: exportDeep, Offset: 1}, "invalid_request"},
		{DecisionExportAt{Phase: exportDeep, Offset: 1, Version: version}, "invalid_request"},
		{DecisionExportAt{Phase: exportLive, Offset: -1}, "invalid_request"},
		{DecisionExportAt{Phase: exportDeep, Offset: 1 << 30, Version: version, Digest: digest}, "invalid_request"},
		{DecisionExportAt{Phase: exportLive, AfterID: 1 << 40, Offset: 1, Version: version, Digest: digest}, "invalid_request"},
		{DecisionExportAt{Phase: exportDeep, AfterReference: "D-1", Offset: 1, Version: version, Digest: digest}, "invalid_request"},
		{DecisionExportAt{Phase: exportDeep, Offset: 1, Version: "synthetic-version", Digest: digest}, "export_changed"},
		{DecisionExportAt{Phase: exportDeep, Offset: 1, Version: version, Digest: strings.Repeat("0", 64)}, "export_changed"},
	} {
		if _, err := s.DecisionExport(ctx, f.peerKey, c.at, 0); refusalCode(err) != c.code {
			t.Errorf("cursor %+v: %v", c.at, err)
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
	// is export_changed.
	first, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{Phase: exportLive}, 0)
	if err != nil || first.Part == nil || first.Part.Final || len(first.Part.Digest) != 64 {
		t.Fatalf("first part: %+v %v", first.Part, err)
	}
	if acked, err := s.DecisionAck(ctx, f.peerKey, []int64{last.ID}); err != nil || len(acked) != 1 {
		t.Fatalf("ack: %v %v", acked, err)
	}
	if _, err := s.DecisionExport(ctx, f.peerKey, *first.Next, 0); refusalCode(err) != "export_changed" {
		t.Fatalf("part after an ack: %v", err)
	}
	second, err := s.DecisionExport(ctx, f.peerKey, DecisionExportAt{Phase: exportLive}, 0)
	if err != nil || second.Version != first.Version || second.Part == nil || second.Part.Digest == first.Part.Digest {
		t.Fatalf("first part again: %v %q %q", err, first.Version, second.Version)
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
			"phase": at.Phase, "after_reference": at.AfterReference, "after_id": at.AfterID, "offset": at.Offset,
			"version": at.Version, "digest": at.Digest})
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

// A move between the parts of a record changes the version: the next part through the route
// and Call is export_changed, so the client starts again, and a new export has the moved item
// once.
func TestDecisionExportMoveBetweenParts(t *testing.T) {
	f, v := deepArchiveFixture(t)
	s, ctx := f.s, context.Background()
	if _, err := s.DecisionAck(ctx, f.peerKey, []int64{v.ID}); err != nil {
		t.Fatal(err)
	}
	saved := decisionExportBytes
	decisionExportBytes = 100
	defer func() { decisionExportBytes = saved }()
	mux := http.NewServeMux()
	(&Daemon{store: s}).decisionRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	call := func(at DecisionExportAt) (DecisionExportPage, error) {
		data, err := Call(ctx, strings.TrimPrefix(server.URL, "http://"), "synthetic-secret", "/v1/decisions/export", map[string]any{"caller": f.peerKey,
			"phase": at.Phase, "after_reference": at.AfterReference, "after_id": at.AfterID, "offset": at.Offset, "version": at.Version, "digest": at.Digest})
		if err != nil {
			return DecisionExportPage{}, err
		}
		var reply struct {
			Result DecisionExportPage `json:"result"`
		}
		err = json.Unmarshal(data, &reply)
		return reply.Result, err
	}
	first, err := call(DecisionExportAt{Phase: exportLive})
	if err != nil || first.Part == nil || first.Part.Final {
		t.Fatalf("first part: %+v %v", first, err)
	}
	if err := s.archiveDecisions(ctx); err != nil || count(t, s, `SELECT COUNT(*) FROM decision_deep_archive`) != 1 {
		t.Fatalf("move: %v", err)
	}
	var refused RefusedError
	if _, err := call(*first.Next); !errors.As(err, &refused) || refused.Code != "export_changed" {
		t.Fatalf("part after a move: %v", err)
	}
	if records, _ := exportAll(t, s, f.peerKey, 0); len(records) != 1 || records[0]["reference"] != "D-1" {
		t.Fatalf("export after the move: %v", records)
	}
}
