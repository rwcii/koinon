package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Plain peers are excluded from address assignment, conflict lists and holder choices,
// both before and after a launched qualifier appears. Renewal and retirement still work.
func TestPlainPeersDoNotQualifyForParticipants(t *testing.T) {
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		t.Run(family, func(t *testing.T) {
			s, _ := testStore(t)
			repo, ctx := testRepo(t), context.Background()
			register := func(id string) Session {
				t.Helper()
				r := Registration{Family: family, ID: id, Directory: repo, Repository: repo, ToolCall: true}
				if family == "claude" {
					r.WakeTarget = json.RawMessage(fmt.Sprintf(`{"claude_pid":%d}`, syntheticHost))
				}
				plain, err := s.Register(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				if plain.Address != "" || plain.Role != "" || plain.Succession != nil || plain.Liveness != "none" {
					t.Fatalf("plain registration: %+v", plain)
				}
				return plain
			}
			first := register("synthetic-plain-first")
			if ps, _, err := s.Participants(ctx); err != nil || len(ps) != 0 {
				t.Fatalf("plain registration created participant: %+v %v", ps, err)
			}
			holder := join(t, s, family, "synthetic-launched", repo)
			register("synthetic-plain-second")
			if p := participantOf(t, s, holder.Address); p.Holder != holder.Name || len(p.Conflict) != 0 {
				t.Fatalf("plain peer changed holder/conflict: %+v", p)
			}
			if err := s.ChooseHolder(ctx, holder.Address, Key{family, first.ID}, first.Revision); !errors.Is(err, ErrNotParticipant) {
				t.Fatalf("plain holder choice: %v", err)
			}
			renewed, err := s.Mutate(ctx, Mutation{Family: family, ID: first.ID, IfRevision: first.Revision}, false)
			if err != nil || renewed.Address != "" || renewed.Succession != nil {
				t.Fatalf("plain renewal: %+v %v", renewed, err)
			}
			if retired, err := s.Mutate(ctx, Mutation{Family: family, ID: first.ID, IfRevision: renewed.Revision}, true); err != nil || retired.State != "retired" {
				t.Fatalf("plain retirement: %+v %v", retired, err)
			}
		})
	}
}

// A legacy holder can have both participant-owned and native-owned state. Becoming plain
// changes its defaults, while a later launched holder resumes the participant's state.
func TestLegacyPlainPeerKeepsNativeAndParticipantState(t *testing.T) {
	s, _ := testStore(t)
	ctx, repo := context.Background(), namedRepo(t, "koinon")
	old := join(t, s, "codex", "synthetic-old", repo)
	k := Key{old.Family, old.ID}
	participant := participantCaller(t, s, k)
	nativeMemory, err := s.ResolveMemoryCaller(ctx, k, &old.Name)
	if err != nil {
		t.Fatal(err)
	}
	nativeConsumer := old.Family + ":" + old.ID
	nativeWork, err := s.ResolveWorkCaller(ctx, k, &nativeConsumer)
	if err != nil {
		t.Fatal(err)
	}
	note(t, s, participant, MemoryRecordRequest{Type: "status", Body: "synthetic retained note"})
	drain(t, s, participant)
	drain(t, s, nativeMemory)
	participantID := create(t, s, participant, "synthetic participant task")
	nativeID := create(t, s, nativeWork, "synthetic native task")
	mustStart(t, s, participant, participantID, nil)
	mustStart(t, s, nativeWork, nativeID, nil)
	participantClaim := get(t, s, participant, participantID)["current_claim"]
	nativeClaim := get(t, s, nativeWork, nativeID)["current_claim"]
	// Model the retained pre-launch row and a persistent fence. No real runtime data is used.
	if _, err := s.db.Exec(`UPDATE sessions SET wake_target='{}' WHERE family=? AND id=?`, k.Family, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO participant_fences(address,family,session_id,at,reason) VALUES (?,?,?,?,'synthetic')`, old.Address, k.Family, k.ID, s.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	plain, err := s.Mutate(ctx, Mutation{Family: k.Family, ID: k.ID, IfRevision: old.Revision}, false)
	if err != nil || plain.State != "active" || plain.Address != "" {
		t.Fatalf("legacy plain renewal: %+v %v", plain, err)
	}
	plainMemory, err := s.ResolveMemoryCaller(ctx, k, nil)
	if err != nil || plainMemory.Consumer != old.Name {
		t.Fatalf("plain memory default: %+v %v", plainMemory, err)
	}
	plainWork := participantCaller(t, s, k)
	if plainWork.Consumer != nativeConsumer || !reflect.DeepEqual(get(t, s, plainWork, nativeID)["current_claim"], nativeClaim) {
		t.Fatal("plain transition changed native claim")
	}
	if delta, err := s.MemorySync(ctx, plainMemory, MemorySyncRequest{}); err != nil || delta["kind"] != "delta" || delta["cursor"] != int64(1) {
		t.Fatalf("native cursor lost: %v %v", delta, err)
	}
	if _, err := s.ResolveWorkCaller(ctx, k, &participant.Consumer); !errors.Is(err, ErrStaleHolder) {
		t.Fatalf("plain peer still acts for participant: %v", err)
	}
	if _, err := s.MemorySync(ctx, participant, MemorySyncRequest{}); !errors.Is(err, ErrStaleHolder) {
		t.Fatalf("old resolved participant caller: %v", err)
	}
	successor := join(t, s, "codex", "synthetic-successor", repo)
	successorCaller := participantCaller(t, s, Key{successor.Family, successor.ID})
	if successor.Address != old.Address || !successor.HoldsAddress || !reflect.DeepEqual(get(t, s, successorCaller, participantID)["current_claim"], participantClaim) {
		t.Fatal("successor did not retain participant claim and address")
	}
	if delta, err := s.MemorySync(ctx, successorCaller, MemorySyncRequest{}); err != nil || delta["kind"] != "delta" || delta["cursor"] != int64(1) {
		t.Fatalf("participant cursor lost: %v %v", delta, err)
	}
	if fences := count(t, s, `SELECT COUNT(*) FROM participant_fences WHERE address=? AND family=? AND session_id=?`, old.Address, k.Family, k.ID); fences != 1 {
		t.Fatalf("plain transition removed a fence: %d", fences)
	}
}
