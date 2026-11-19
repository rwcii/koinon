package core

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// queryPlan is the detail column of EXPLAIN QUERY PLAN for a query.
func queryPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// An idle wake pass reads no handled message (#756). A message keeps its delivery state after
// it is read, so the wake queries start from the sessions and read each inbox's messages after
// its handled sequence, through the (recipient, sequence) index.
func TestWakeIdlePassReadsNoHandledMessage(t *testing.T) {
	s, _, sender, receiver, _, _ := wakeFixture(t, "codex")
	ctx := context.Background()
	from, inbox := Key{sender.Family, sender.ID}, Key{receiver.Family, receiver.ID}
	for i := 0; i < 120; i++ {
		if _, err := s.Send(ctx, from, receiver.Name, "synthetic handled body"); err != nil {
			t.Fatal(err)
		}
	}
	for after := int64(0); ; {
		read, err := s.ReadInbox(ctx, inbox, after, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(read.Messages) == 0 {
			break
		}
		after = read.Messages[len(read.Messages)-1].Seq
	}
	now, later := s.dueArgs()
	keys, err := s.wakeKeys(ctx, false, dueWake, now, later)
	if err != nil || len(keys) != 0 {
		t.Fatalf("handled inbox listed: %v %v", keys, err)
	}

	// 18 other inboxes with one unhandled message each: the first 16, in order.
	var want []Key
	for i := 0; i < 18; i++ {
		other := join(t, s, "codex", fmt.Sprintf("synthetic-other-%02d", i), receiver.Directory)
		if _, err := s.Send(ctx, from, other.Name, "synthetic unhandled body"); err != nil {
			t.Fatal(err)
		}
		want = append(want, Key{other.Family, other.ID})
	}
	sort.Slice(want, func(i, j int) bool {
		return want[i].Family < want[j].Family || want[i].Family == want[j].Family && want[i].ID < want[j].ID
	})
	keys, err = s.wakeKeys(ctx, false, dueWake, now, later)
	if err != nil || !reflect.DeepEqual(keys, want[:16]) {
		t.Fatalf("keys %v %v, want %v", keys, err, want[:16])
	}

	// No wake query scans the messages table.
	for name, q := range map[string]string{
		"keys":        wakeKeysQuery(`('maintainer')`, dueWake+` AND m.wake_notices>=?`),
		"batch":       wakeBatchQuery,
		"fail":        failUnhandledQuery,
		"turn":        turnFailureQuery,
		"health":      wakeHealthStatesQuery,
		"health_kind": wakeHealthReasonsQuery,
	} {
		args := make([]any, strings.Count(q, "?"))
		for i := range args {
			args[i] = 0
		}
		plan := queryPlan(t, s, q, args...)
		reads := 0
		for _, line := range strings.Split(plan, "\n") {
			if !strings.HasPrefix(line, "SCAN m") && !strings.HasPrefix(line, "SEARCH m ") {
				continue
			}
			reads++
			if !strings.HasSuffix(line, "sqlite_autoindex_messages_1 (recipient_family=? AND recipient_id=? AND seq>?)") {
				reads = -1
				break
			}
		}
		if reads < 1 {
			t.Errorf("%s plan reads messages other than by recipient and sequence:\n%s", name, plan)
		}
	}
}
