package core

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// A participant inbox whose address has no live holder is not listed for a wake, so it takes
// none of the 16 places, and nothing is written to its messages (#759). When a holder takes
// the address, the next pass wakes it. Messages at the notice limit still fail without a
// holder.
func TestWakeHolderlessParticipant(t *testing.T) {
	s, _, sender, receiver, _, clock := wakeFixture(t, "codex")
	ctx := context.Background()
	from := Key{sender.Family, sender.ID}
	if receiver.Address == "" {
		t.Fatal("the receiver holds no address")
	}
	held, err := s.Send(ctx, from, receiver.Address, "synthetic participant body")
	if err != nil {
		t.Fatal(err)
	}
	inbox := Key{"codex", participantPrefix + receiver.Address}
	setHolder := func(holder string) {
		t.Helper()
		if _, err := fixtureExec(s, `UPDATE names SET holder_id=? WHERE kind='alias' AND name=?`, holder, receiver.Address); err != nil {
			t.Fatal(err)
		}
	}
	setHolder("")
	// 16 other due inboxes, whose IDs sort after the participant's.
	var others []int64
	for i := 0; i < 16; i++ {
		other := join(t, s, "codex", fmt.Sprintf("synthetic-other-%02d", i), "")
		sent, err := s.Send(ctx, from, other.Name, "synthetic body")
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, sent.ID)
	}
	now, later := s.dueArgs()
	listed := func(held bool) []Key {
		t.Helper()
		keys, err := s.wakeKeys(ctx, false, held, dueWake, now, later)
		if err != nil {
			t.Fatal(err)
		}
		return keys
	}
	if keys := listed(false); len(keys) != 16 || keys[0] != inbox {
		t.Fatalf("without the holder check the participant takes the first place: %v", keys)
	}
	keys := listed(true)
	if len(keys) != 16 || slices.Contains(keys, inbox) {
		t.Fatalf("holderless participant listed: %v", keys)
	}
	before := messageRow(t, s, held.ID)
	calls := countingWake(s, accepted())
	step(t, s)
	if after := messageRow(t, s, held.ID); after != before {
		t.Fatalf("holderless message changed: %+v, was %+v", after, before)
	}
	// One pass wakes all 16 other inboxes: the holderless one took no place.
	for i, id := range others {
		if row := messageRow(t, s, id); row.state != "notified" {
			t.Fatalf("other inbox %d not woken in the pass (%d wakes): %+v", i, *calls, row)
		}
	}

	// A holder that expires exactly now, or that is retired, is no holder.
	setHolder(receiver.ID)
	at := clock.UnixMilli()
	if _, err := fixtureExec(s, `UPDATE sessions SET expires_at=? WHERE family='codex' AND id=?`, at, receiver.ID); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(listed(true), inbox) {
		t.Fatal("listed with a holder that expires now")
	}
	if _, err := fixtureExec(s, `UPDATE sessions SET expires_at=?,retired_at=? WHERE family='codex' AND id=?`, at+60000, at, receiver.ID); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(listed(true), inbox) {
		t.Fatal("listed with a retired holder")
	}
	// A live holder: the inbox is listed and its waiting message gets its notice.
	if _, err := fixtureExec(s, `UPDATE sessions SET retired_at=0 WHERE family='codex' AND id=?`, receiver.ID); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(listed(true), inbox) {
		t.Fatal("not listed with a live holder")
	}
	*calls = 0
	step(t, s)
	if row := messageRow(t, s, held.ID); row.state != "notified" || row.notices != 1 || *calls == 0 {
		t.Fatalf("held participant not woken: %+v after %d calls", row, *calls)
	}

	// At the notice limit, a holderless participant's due message fails; one below the limit
	// in another holderless inbox stays as it is.
	setHolder("")
	if _, err := fixtureExec(s, `UPDATE messages SET wake_notices=?,wake_next_at=? WHERE id=?`, wakeNoticeLimit, at-1, held.ID); err != nil {
		t.Fatal(err)
	}
	second := join(t, s, "codex", "synthetic-second", testRepo(t))
	waiting, err := s.Send(ctx, from, second.Address, "synthetic participant body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureExec(s, `UPDATE names SET holder_id='' WHERE kind='alias' AND name=?`, second.Address); err != nil {
		t.Fatal(err)
	}
	untouched := messageRow(t, s, waiting.ID)
	step(t, s)
	if row := messageRow(t, s, held.ID); row.state != "failed" {
		t.Fatalf("exhausted holderless message: %+v", row)
	}
	if row := messageRow(t, s, waiting.ID); row != untouched {
		t.Fatalf("below-limit holderless message changed: %+v, was %+v", row, untouched)
	}
}
