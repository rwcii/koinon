package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"

	"modernc.org/sqlite"
)

// WorkRole is a routing proposal in the caller's repository, never a caller identity.
type WorkRole struct {
	Family string `json:"family"`
	Role   string `json:"role"`
}

func (r *WorkRole) encoded() string {
	if r == nil {
		return ""
	}
	b, _ := json.Marshal(r)
	return string(b)
}

// WorkRoleResolution reports current routing without guessing a native session. A
// conflicting registry stays explicit without copying an unbounded conflict list.
type WorkRoleResolution struct {
	State   string `json:"state"`
	Address string `json:"address,omitempty"`
	Holder  string `json:"holder,omitempty"`
}

func roleResolution(ctx context.Context, q querier, repo string, role *WorkRole, now float64) (*WorkRoleResolution, error) {
	if role == nil {
		return nil, nil
	}
	r := &WorkRoleResolution{State: "unheld"}
	var holder, conflict string
	err := q.QueryRowContext(ctx, `SELECT name,holder_id,conflict FROM names WHERE kind='alias' AND family=? AND repository=? AND role=?`,
		role.Family, repo, role.Role).Scan(&r.Address, &holder, &conflict)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	held, err := holds(ctx, q, int64(now*1000), role.Family, holder, repo, role.Role)
	if err != nil {
		return nil, err
	}
	if held {
		r.Holder, err = peerOf(ctx, q, role.Family, holder)
		r.State = "held"
	} else if conflict != "" && conflict != "[]" {
		r.State = "conflict"
	}
	return r, err
}

func nativeWorkActor(m MemoryCaller) string {
	if m.Native.Family != m.Family {
		return ""
	}
	return nativeWorkActorForKey(m.Native)
}

func nativeWorkActorForKey(k Key) string {
	if !validKey(k.Family, k.ID) {
		return ""
	}
	// JSON tuple encoding preserves the family/ID boundary for every valid native ID.
	b, _ := json.Marshal([2]string{k.Family, k.ID})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func init() {
	// Retention compares the same digest against recorded session keys in its
	// deleting transaction. This pure function adds no table, index or identity data.
	sqlite.MustRegisterDeterministicScalarFunction("koinon_native_work_actor", 2,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			family, familyOK := args[0].(string)
			id, idOK := args[1].(string)
			if !familyOK || !idOK {
				return "", nil
			}
			return nativeWorkActorForKey(Key{family, id}), nil
		})
}

func nativeWorkConsumer(m MemoryCaller) string { return "native-work:" + nativeWorkActor(m) }

// The normalization read can precede a wait for memory serialization. Revalidate
// the bound actor in the replay/owner transaction at its processing time as well.
func requireNativeWorkCaller(ctx context.Context, tx *sql.Tx, m MemoryCaller, actor string, now float64) error {
	// This internal caller is used by authenticated dashboard controls and an
	// explicitly marked purge. validKey refuses it on every native API route.
	if actor == "" || m.Family == maintainerKey.Family {
		return nil
	}
	if actor != nativeWorkActor(m) {
		return workError("stale_claim", "this ownership belongs to another native session")
	}
	if err := active(ctx, tx, int64(now*1000), m.Native); err != nil {
		return err
	}
	var repo string
	if err := tx.QueryRowContext(ctx, `SELECT repository FROM sessions WHERE family=? AND id=?`, m.Native.Family, m.Native.ID).Scan(&repo); err != nil {
		return err
	}
	if repo != m.Repository {
		return workError("role_not_held", "the native session belongs to another repository")
	}
	return nil
}

func ownerOperation(op string) bool {
	return op == "work-update" || op == "work-edit" || op == "work-release" || op == "work-finish" || op == "claim-renew" ||
		op == "work-report"
}

// roleWorkCaller chooses the new path before replay or maintenance. It is called
// under participant serialization and keeps legacy/default consumers unchanged.
func (s *Store) roleWorkCaller(ctx context.Context, m MemoryCaller, r *workRequest, now float64) (MemoryCaller, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	store, err := storeOf(ctx, tx, m.Repository)
	if err != nil || store == "" || r.op == "work-create" || workRead(r.op) || r.op == "work-propose" {
		return m, err
	}
	actor, consumer := nativeWorkActor(m), nativeWorkConsumer(m)
	bound := false
	// A retained native replay still resolves after its claim was reclaimed or its
	// proposal changed. The replay row itself rechecks its actor before returning.
	if r.keyed() && actor != "" && !m.ConsumerExplicit {
		var replayActor string
		err = tx.QueryRowContext(ctx, `SELECT native_actor FROM work_replays WHERE store=? AND consumer=? AND key=?`,
			store, consumer, r.text["key"]).Scan(&replayActor)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return m, err
		}
		bound = err == nil && replayActor != ""
	}
	held, err := bundleFor(ctx, tx, store, r.text["work_id"])
	if err != nil {
		return m, err
	}
	if ownerOperation(r.op) && held != nil && held.NativeActor != "" &&
		(r.op != "work-edit" || held.Active && held.ExpiresAt > now || r.has("claim_generation")) {
		if held.NativeActor != actor {
			return m, workError("stale_claim", "this claim belongs to another native session")
		}
		bound = true
	}
	if r.op == "work-start" && !bound {
		w, err := current(ctx, tx, store, r.text["work_id"], now)
		if err != nil {
			return m, err
		}
		bound = w.ProposedRole != nil
	}
	if !bound {
		return m, nil
	}
	if actor == "" {
		return m, workError("role_not_held", "a role claim requires a verified native participant holder")
	}
	if err := requireNativeWorkCaller(ctx, tx, m, actor, now); err != nil {
		return m, err
	}
	if m.ConsumerExplicit && m.Consumer != consumer {
		return m, invalid("a role claim's explicit consumer must match its derived native-work key")
	}
	m.Consumer = consumer
	r.actor = actor
	return m, nil
}

func requireWorkRole(ctx context.Context, tx *sql.Tx, m MemoryCaller, role *WorkRole, now float64) error {
	resolved, err := roleResolution(ctx, tx, m.Repository, role, now)
	if err != nil {
		return err
	}
	if resolved.State != "held" {
		return WorkRefusal{Code: "role_" + resolved.State, Message: "the proposed role has no current holder", Details: map[string]any{"role_resolution": resolved}}
	}
	if role.Family != m.Native.Family || nativeWorkActor(m) == "" {
		return workError("role_not_held", "this native session does not hold the proposed role")
	}
	if err := requireHolder(ctx, tx, int64(now*1000), m.Native, resolved.Address); err != nil {
		return workError("role_not_held", "this native session does not hold the proposed role")
	}
	return nil
}
