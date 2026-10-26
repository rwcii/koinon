package core

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"
)

// Change notices of the maintainer mode (PDR 009, Decision 3). Schema 32 keeps one version
// counter per topic: the maintainer's inbox, the decision board and each room. Every write
// that changes what a topic's maintainer read shows moves its counter in the same
// transaction, so a rolled-back write moves nothing, and a retry that writes nothing moves
// nothing. A counter is a fixed-width text of 20 digits: an increment overwrites it in place
// and allocates no page, also in a control write that uses the reserve. A room's row is
// inserted by the room's creation, an ordinary write, and deleted with the room; the
// migration inserts the rows of the topics that exist before it.

const (
	changeInbox     = "inbox"
	changeDecisions = "decisions"
	// MaintainerURI is the prefix of each maintainer resource's URI.
	MaintainerURI = "koinon://maintainer/"
	// changePoll is how often a waiting changes request reads the counters again.
	changePoll = 500 * time.Millisecond
	// maxChangeWait bounds wait_seconds of a changes request.
	maxChangeWait = 30
)

// changeVersionsTable is the schema 32 counter table. A topic is inbox, decisions or a room ID.
const changeVersionsTable = `CREATE TABLE change_versions (
	topic TEXT PRIMARY KEY CHECK (topic IN ('inbox','decisions') OR length(topic)=32),
	version TEXT NOT NULL CHECK (length(version)=20));`

// firstVersion is the value of a new counter.
const firstVersion = `printf('%020d',1)`

// bumpChange moves a topic's counter by one in the caller's write transaction.
func bumpChange(ctx context.Context, q querier, topic string) error {
	_, err := q.ExecContext(ctx, `UPDATE change_versions SET version=printf('%020d',version+1) WHERE topic=?`, topic)
	return err
}

// bumpInbox moves the inbox counter when inbox is the maintainer's.
func bumpInbox(ctx context.Context, q querier, inbox Key) error {
	if inbox != maintainerKey {
		return nil
	}
	return bumpChange(ctx, q, changeInbox)
}

// topicURI is the resource URI of a topic.
func topicURI(topic string) string {
	if topic == changeInbox || topic == changeDecisions {
		return MaintainerURI + topic
	}
	return MaintainerURI + "rooms/" + topic
}

// changeVersions reads every counter in one read transaction, by resource URI. A deleted
// room has no entry.
func (s *Store) changeVersions(ctx context.Context) (map[string]string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT topic,version FROM change_versions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string]string{}
	for rows.Next() {
		var topic, version string
		if err := rows.Scan(&topic, &version); err != nil {
			return nil, err
		}
		versions[topicURI(topic)] = version
	}
	return versions, rows.Err()
}

// changedURIs lists, in order, each URI whose version differs between the two maps, also
// one that only one of them has. Versions are compared for equality only.
func changedURIs(cursor, current map[string]string) []string {
	changed := []string{}
	for uri, version := range current {
		if seen, ok := cursor[uri]; !ok || seen != version {
			changed = append(changed, uri)
		}
	}
	for uri := range cursor {
		if _, ok := current[uri]; !ok {
			changed = append(changed, uri)
		}
	}
	slices.Sort(changed)
	return changed
}

// validCursor bounds a changes request's cursor: at most one entry per topic that can exist,
// each a maintainer URI with a 20-character version.
func validCursor(cursor map[string]string) bool {
	if len(cursor) > maxRooms+2 {
		return false
	}
	for uri, version := range cursor {
		if !strings.HasPrefix(uri, MaintainerURI) || len(uri) > 128 || len(version) != 20 {
			return false
		}
	}
	return true
}

// waitChanges answers a changes request. Without a cursor it returns the current versions at
// once. With one, it reads the versions every changePoll, each time in its own read
// transaction that ends before the wait, and returns when they differ from the cursor or
// when wait ends. A token that ends during the wait ends the request with ErrTokenExpired;
// the check does not move the token's idle limit. A daemon stop ends the wait.
func (d *Daemon) waitChanges(ctx context.Context, token string, cursor map[string]string, wait time.Duration) (map[string]string, []string, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(changePoll)
	defer ticker.Stop()
	for {
		versions, err := d.store.changeVersions(ctx)
		if err != nil {
			return nil, nil, err
		}
		if cursor == nil {
			return versions, []string{}, nil
		}
		if changed := changedURIs(cursor, versions); len(changed) > 0 {
			return versions, changed, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-d.stop:
			return versions, []string{}, nil
		case <-timer.C:
			return versions, []string{}, nil
		case <-ticker.C:
		}
		if !d.auth.live(token) {
			return nil, nil, ErrTokenExpired
		}
	}
}
