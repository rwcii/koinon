package core

// Authorized maintainer keys and signed-action receipts (#693, LINK-693-SIGNED; PDR 011
// Decision 6; docs/sprints/2026-11-14-signed-maintainer/decision.md, Decisions 1 and 4).

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"errors"
	"net/http"

	"github.com/rwcii/koinon/internal/signed"
)

// Schema 39: each authorization of a maintainer client key, the receipts of signed writes,
// and the signer of a verdict. Authorization rows are never deleted, so an enrollment
// statement adds a key once on this computer (statement_used).
const maintainerKeyTablesV39 = `
	CREATE TABLE maintainer_authorizations (
		id TEXT PRIMARY KEY,
		key_id TEXT NOT NULL,
		public_key BLOB NOT NULL,
		user TEXT NOT NULL,
		client TEXT NOT NULL,
		created INTEGER NOT NULL,
		added_at INTEGER NOT NULL,
		added_by TEXT NOT NULL,
		removed_at INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX maintainer_authorizations_current ON maintainer_authorizations(key_id) WHERE removed_at=0;
	CREATE TABLE signed_receipts (
		authorization TEXT NOT NULL,
		request TEXT NOT NULL,
		op TEXT NOT NULL,
		digest TEXT NOT NULL,
		state TEXT NOT NULL CHECK (state IN ('recorded','refused')),
		code TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		PRIMARY KEY (authorization, request)
	);
	CREATE INDEX signed_receipts_expiry ON signed_receipts(expires_at);
	ALTER TABLE decision_verdicts ADD COLUMN signer_user TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_verdicts ADD COLUMN signer_key TEXT NOT NULL DEFAULT '';
	ALTER TABLE decision_verdicts ADD COLUMN signer_client TEXT NOT NULL DEFAULT '';`

// MaintainerKey is one authorization of a maintainer client key.
type MaintainerKey struct {
	Authorization string `json:"authorization"`
	KeyID         string `json:"key_id"`
	User          string `json:"user"`
	Client        string `json:"client"`
	AddedAt       int64  `json:"added_at"`
	AddedBy       string `json:"added_by"`
	RemovedAt     int64  `json:"removed_at,omitempty"`
	public        ed25519.PublicKey
}

var (
	// ErrStatementUsed refuses an enrollment statement that this computer added before.
	ErrStatementUsed = Refusal{"statement_used", "this computer added that enrollment statement before; sign a new one"}
	// ErrKeyNotFound refuses the removal of a key that has no current authorization.
	ErrKeyNotFound = Refusal{"key_unknown", "no current authorization has that key"}
)

// AddMaintainerKey stores an authorization from a checked enrollment statement. A key that has
// a current authorization gets the new one in its place, in the same transaction, with the
// audit record of ctx.
func (s *Store) AddMaintainerKey(ctx context.Context, en signed.Enrollment, addedBy string) (MaintainerKey, error) {
	tx, err := s.begin(ctx, ordinary)
	if err != nil {
		return MaintainerKey{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	var seen int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM maintainer_authorizations WHERE id=?`, en.Authorization).Scan(&seen); err != nil {
		return MaintainerKey{}, err
	}
	if seen > 0 {
		return MaintainerKey{}, ErrStatementUsed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maintainer_authorizations SET removed_at=? WHERE key_id=? AND removed_at=0`, now, en.KeyID); err != nil {
		return MaintainerKey{}, tx.fail(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintainer_authorizations(id,key_id,public_key,user,client,created,added_at,added_by)
		VALUES(?,?,?,?,?,?,?,?)`, en.Authorization, en.KeyID, []byte(en.PublicKey), en.User, en.Client, en.Created, now, addedBy); err != nil {
		return MaintainerKey{}, tx.fail(err)
	}
	setAuditTarget(ctx, "key "+en.KeyID+" for "+en.User+" ("+en.Client+")")
	if err := tx.Commit(); err != nil {
		return MaintainerKey{}, err
	}
	return MaintainerKey{Authorization: en.Authorization, KeyID: en.KeyID, User: en.User, Client: en.Client, AddedAt: now, AddedBy: addedBy}, nil
}

// RemoveMaintainerKey ends the key's current authorization. The removal is stored before it
// returns; an action that waits for the store afterwards finds no current authorization. It is
// a control write, so a full store still takes it.
func (s *Store) RemoveMaintainerKey(ctx context.Context, keyID string) (MaintainerKey, error) {
	tx, err := s.begin(ctx, control)
	if err != nil {
		return MaintainerKey{}, err
	}
	defer tx.Rollback()
	tx.audited = true
	now := s.now().UnixMilli()
	key, err := currentAuthorization(ctx, tx.Tx, keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return MaintainerKey{}, ErrKeyNotFound
	}
	if err != nil {
		return MaintainerKey{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maintainer_authorizations SET removed_at=? WHERE id=?`, now, key.Authorization); err != nil {
		return MaintainerKey{}, tx.fail(err)
	}
	setAuditTarget(ctx, "key "+key.KeyID+" for "+key.User+" ("+key.Client+")")
	if err := tx.Commit(); err != nil {
		return MaintainerKey{}, err
	}
	key.RemovedAt = now
	return key, nil
}

// MaintainerKeys lists the current authorizations, and the removed ones when all is true.
func (s *Store) MaintainerKeys(ctx context.Context, all bool) ([]MaintainerKey, error) {
	query := `SELECT id,key_id,public_key,user,client,added_at,added_by,removed_at FROM maintainer_authorizations`
	if !all {
		query += ` WHERE removed_at=0`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY added_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []MaintainerKey{}
	for rows.Next() {
		var k MaintainerKey
		var public []byte
		if err := rows.Scan(&k.Authorization, &k.KeyID, &public, &k.User, &k.Client, &k.AddedAt, &k.AddedBy, &k.RemovedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// currentAuthorization reads the key's current authorization in q, a transaction when the
// caller acts on it.
func currentAuthorization(ctx context.Context, q querier, keyID string) (MaintainerKey, error) {
	var k MaintainerKey
	var public []byte
	err := q.QueryRowContext(ctx, `SELECT id,key_id,public_key,user,client,added_at,added_by FROM maintainer_authorizations
		WHERE key_id=? AND removed_at=0`, keyID).Scan(&k.Authorization, &k.KeyID, &public, &k.User, &k.Client, &k.AddedAt, &k.AddedBy)
	k.public = ed25519.PublicKey(public)
	return k, err
}

// maintainerKeyRoutes add koinon maintainer key add, list and remove, with the computer's own
// login (Decision 1 of the signed maintainer contract): a key is added only from an enrollment
// statement for this computer's pin, signed within 10 minutes, once.
func (d *Daemon) maintainerKeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/maintainer/keys", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Action    string `json:"action"`
			Statement string `json:"statement"`
			KeyID     string `json:"key_id"`
			All       bool   `json:"all"`
		}
		// JSON escaping can grow a statement to six times its size.
		if err := decodeLimit(w, r, &request, 6*signed.MaxEnvelopeBytes+256); err != nil {
			failure(w, err)
			return
		}
		ctx := withOrigin(r.Context(), "cli")
		switch request.Action {
		case "list":
			keys, err := d.store.MaintainerKeys(ctx, request.All)
			maintainerRead(w, map[string]any{"keys": keys}, err)
		case "add":
			ctx, pending := withAudit(ctx, "maintainer-key-add", "statement")
			key, err := d.addMaintainerKey(ctx, request.Statement)
			if err != nil {
				d.store.auditRefusal(ctx, pending, errorCode(err))
			} else {
				// A replaced authorization's sessions end with it.
				d.auth.endKey(key.KeyID)
			}
			maintainerRead(w, key, err)
		case "remove":
			ctx, pending := withAudit(ctx, "maintainer-key-remove", "key "+auditName(request.KeyID))
			key, err := d.store.RemoveMaintainerKey(ctx, request.KeyID)
			if err != nil {
				d.store.auditRefusal(ctx, pending, errorCode(err))
			} else {
				d.auth.endKey(key.KeyID)
			}
			maintainerRead(w, key, err)
		default:
			failure(w, ErrInvalid)
		}
	})
}

// addMaintainerKey checks an enrollment statement for this computer's pin and stores its
// authorization.
func (d *Daemon) addMaintainerKey(ctx context.Context, statement string) (MaintainerKey, error) {
	e, err := signed.Parse([]byte(statement))
	if err != nil {
		return MaintainerKey{}, signedError(err)
	}
	id, err := d.linkIdentity()
	if err != nil {
		return MaintainerKey{}, err
	}
	en, err := signed.CheckEnrollment(e, id.Pin, d.store.now().Unix())
	if err != nil {
		return MaintainerKey{}, signedError(err)
	}
	return d.store.AddMaintainerKey(ctx, en, "cli")
}
