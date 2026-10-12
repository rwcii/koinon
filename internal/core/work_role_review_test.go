package core

import (
	"context"
	"net/url"
	"strconv"
	"testing"
	"time"
)

// R1: the existing authenticated maintainer controls retain their administrative
// actions on native role claims; native-session ownership does not replace them.
func TestWorkRoleMaintainerDashboardControls(t *testing.T) {
	for _, action := range []string{"release", "work-finish", "unclaimed-finish"} {
		t.Run(action, func(t *testing.T) {
			d, root := startTestDaemon(t)
			s := d.store
			repo := namedRepo(t, "dashboard-role-controls")
			a := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
			m := participantCaller(t, s, Key{a.Family, a.ID})
			id := createRoleWork(t, s, m, "codex", "worker")
			var generation int64
			if action != "unclaimed-finish" {
				generation = mustStart(t, s, m, id, nil)
			}
			client := newActionClient(t, d, root)
			form := url.Values{"repository": {m.Repository}, "work_id": {id},
				"revision": {strconv.FormatInt(revision(t, s, m, id), 10)}, "generation": {strconv.FormatInt(generation, 10)},
				"consumer": {nativeWorkConsumer(m)}, "key": {"synthetic-admin-finish"}, "deadline": {strconv.FormatInt(int64(now(s))+600, 10)},
				"outcome": {"completed"}, "references": {"synthetic evidence"}}
			endpoint, expected := action, "released"
			if action != "release" {
				endpoint, expected = "work-finish", "work_finished"
			}
			if got := client.do(endpoint, form); got != expected {
				t.Fatalf("maintainer %s: %s", action, got)
			}
			if action != "release" {
				if got := client.do(endpoint, form); got != expected {
					t.Fatalf("maintainer finish retry: %s", got)
				}
			}
			item := get(t, s, m, id)
			if item["current_claim"] != nil || action == "release" && item["lifecycle"] != "open" ||
				action != "release" && item["lifecycle"] != "finished" {
				t.Fatalf("admin control left a live claim: %v", item)
			}
			// HTTP native callers cannot select the internal maintainer identity.
			secret, err := ReadSecret(root)
			if err != nil {
				t.Fatal(err)
			}
			status, _ := post(t, d.Addresses()[0], secret, "/v1/work/work-get", map[string]any{"caller": maintainerKey, "work_id": id})
			if status == 200 {
				t.Fatal("native API admitted a forged maintainer caller")
			}
		})
	}
}

func TestWorkRolePurgeReleasesActualNativeConsumer(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "purge-role-claim")
	a := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
	k := Key{a.Family, a.ID}
	m := participantCaller(t, s, k)
	id := createRoleWork(t, s, m, "codex", "worker")
	mustStart(t, s, m, id, nil)
	if err := s.markPurge(context.Background(), k, a.Revision, true); err != nil {
		t.Fatal(err)
	}
	released, marked, err := s.releasePurged(context.Background(), k)
	if err != nil || !released || !marked {
		t.Fatalf("marked native release: %v %v %v", released, marked, err)
	}
	if claim := get(t, s, maintainerWork(m.Repository, maintainerConsumer), id)["current_claim"]; claim != nil {
		t.Fatal("purge reported success while the native claim remained live")
	}
	if mustDebt(t, s) != 0 {
		t.Fatal("purge left native control credit behind")
	}
}

func TestWorkRoleRetentionKeepsNativeClaimOwner(t *testing.T) {
	s, clock, _ := workStore(t)
	repo := namedRepo(t, "retained-native-owner")
	a := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
	m := participantCaller(t, s, Key{a.Family, a.ID})
	id := createRoleWork(t, s, m, "codex", "worker")
	mustStart(t, s, m, id, nil)
	*clock = clock.Add(sessionRetention + time.Hour)
	var counts RetentionCounts
	if err := s.deleteInactive(context.Background(), &counts); err != nil {
		t.Fatal(err)
	}
	if !sessionExists(t, s, m.Native) || counts.HeldClaims != 1 {
		t.Fatalf("retention lost the still-unreconciled native claim owner: %+v", counts)
	}
}

// R2: reproduce the two serialized phases without timing sleeps. Maintainer
// proposals can change after normalization's read but before the work transaction.
func TestWorkRoleProposalChangesBetweenNormalizationAndStart(t *testing.T) {
	for _, clear := range []bool{true, false} {
		t.Run(strconv.FormatBool(clear), func(t *testing.T) {
			s, _ := testStore(t)
			repo := namedRepo(t, "proposal-race")
			a := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
			m := participantCaller(t, s, Key{a.Family, a.ID})
			var id string
			if clear {
				id = createRoleWork(t, s, m, "codex", "worker")
			} else {
				id = create(t, s, m, "synthetic unassigned")
			}
			r, err := parseWork("work-start", workFieldsOf(map[string]any{"work_id": id, "if_revision": 2, "checkpoint": "synthetic start",
				"next_artifact": "synthetic artifact", "progress_deadline": now(s) + 600, "key": key(), "deadline": now(s) + 600}))
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := s.roleWorkCaller(context.Background(), m, r, now(s))
			if err != nil {
				t.Fatal(err)
			}
			var role any = WorkRole{"codex", "worker"}
			if clear {
				role = nil
			}
			mustWork(t, s, maintainerWork(m.Repository, maintainerConsumer), "work-propose", map[string]any{
				"work_id": id, "if_revision": 1, "proposed_role": role})
			err = s.workWrite(context.Background(), m.Repository, ordinary, func(tx *writeTx, store string) error {
				_, err := s.apply(context.Background(), tx, store, normalized, r, now(s), r.fingerprint(normalized.Consumer), new(bool))
				return err
			})
			if clear {
				if err != nil {
					t.Fatal(err)
				}
				claim := get(t, s, m, id)["current_claim"].(map[string]any)
				if claim["ownership"] != "native" || claim["consumer"] != nativeWorkConsumer(m) {
					t.Fatalf("clearing a proposal erased the chosen native binding: %v", claim)
				}
			} else {
				if wcode(err) != "revision_conflict" || get(t, s, m, id)["current_claim"] != nil {
					t.Fatalf("role appeared after legacy normalization: %v", err)
				}
			}
		})
	}
}

func TestWorkRoleMaintainerFinishReplayRetainsNativeBinding(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "admin-replay")
	a := joinAs(t, s, "codex", "synthetic-worker", repo, "worker")
	b := joinAs(t, s, "claude", "synthetic-other", repo, "")
	m := participantCaller(t, s, Key{a.Family, a.ID})
	id := createRoleWork(t, s, m, "codex", "worker")
	gen := mustStart(t, s, m, id, nil)
	consumer := nativeWorkConsumer(m)
	admin := maintainerWork(m.Repository, consumer)
	fields := map[string]any{"work_id": id, "if_revision": 2, "claim_generation": gen, "outcome": "completed",
		"references": []string{"synthetic evidence"}, "key": "synthetic-admin-finish", "deadline": now(s) + 600}
	mustWork(t, s, admin, "work-finish", fields)
	if retry := mustWork(t, s, admin, "work-finish", fields); retry["duplicate"] != true {
		t.Fatal("maintainer finish retry failed")
	}
	if err := s.reclaimInactive(context.Background(), m.Repository, id); err != nil {
		t.Fatal(err)
	}
	wrong, err := s.ResolveWorkCaller(context.Background(), Key{b.Family, b.ID}, &consumer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work(s, wrong, "work-finish", fields); wcode(err) != "stale_claim" {
		t.Fatalf("native successor copied an administrative ownership replay: %v", err)
	}
	var actor string
	if err := s.db.QueryRow(`SELECT native_actor FROM work_replays WHERE key='synthetic-admin-finish'`).Scan(&actor); err != nil || actor != nativeWorkActor(m) {
		t.Fatalf("admin ownership replay lost generation binding: %q %v", actor, err)
	}
}
