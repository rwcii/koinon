package core

// The computers of a node and the signed changes wait (#693; decision.md Decisions 2.4, 6.2
// and 6.3). computers gives the node's own pin and name and, for each paired computer, its
// pin, name, connection state, reason and the time the state began. The topic computers of a
// changes wait on the node moves when that view changes.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

const changeComputers = "computers"

// computersTopic is the computers topic's version with the state that decides when it moves.
// The store keeps it in the settings row computersSetting, so the version survives a restart
// as the other topics' versions do (Decision 6.2; review K6-F1): the daemon reads it at its
// first use and stores each change before it answers with it.
type computersTopic struct {
	mu     sync.Mutex
	loaded bool
	saved  computersSaved
}

// computersSetting is the settings row of the computers topic.
const computersSetting = "signed.computers"

// computersSaved is the stored computers topic: its counter, the digest of the view it counts,
// and each pin's state with the time the state began.
type computersSaved struct {
	N      int64                    `json:"n"`
	Digest string                   `json:"digest"`
	States map[string]computerState `json:"states"`
}

type computerState struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	Since  int64  `json:"since"`
}

// Computer is one paired computer as computers gives it.
type Computer struct {
	Pin    string `json:"pin"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	Since  int64  `json:"since"`
}

// ComputersView is the answer of computers.
type ComputersView struct {
	Node      ComputerNode `json:"node"`
	Computers []Computer   `json:"computers"`
}

// ComputerNode is the node's own pin and name.
type ComputerNode struct {
	Pin  string `json:"pin"`
	Name string `json:"name"`
}

// computersView reads the node's pairs and their connection states, from the link state and
// the link's observations, without the Serve inspection of koinon link status. It returns the
// view and its topic version.
func (d *Daemon) computersView(ctx context.Context) (ComputersView, string, error) {
	id, err := d.linkIdentity()
	if err != nil {
		return ComputersView{}, "", err
	}
	d.link.mu.Lock()
	open := d.link.listener != nil
	gen := d.link.gen
	observed := map[string]linkObservation{}
	for k, v := range d.link.observed {
		observed[k] = v
	}
	s, err := link.ReadState(d.root)
	d.link.mu.Unlock()
	if err != nil {
		return ComputersView{}, "", err
	}
	now := d.linkNow().Unix()
	view := ComputersView{Node: ComputerNode{Pin: id.Pin, Name: s.Name}, Computers: []Computer{}}
	t := &d.computers
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		var data string
		err := d.store.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE name=?`, computersSetting).Scan(&data)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return ComputersView{}, "", err
		default:
			if err := json.Unmarshal([]byte(data), &t.saved); err != nil {
				return ComputersView{}, "", err
			}
		}
		t.loaded = true
	}
	next := computersSaved{N: t.saved.N, Digest: t.saved.Digest, States: map[string]computerState{}}
	for _, p := range s.Pairs {
		if p.State != link.Paired {
			continue
		}
		o := pairObservation(p, observed[p.ID], open, gen, now)
		state := "offline"
		if o.State == "connected" {
			state = "connected"
		}
		prior, known := t.saved.States[p.Pin]
		if !known || prior.State != state || prior.Reason != o.Error {
			since := o.At
			if known {
				since = now
			}
			prior = computerState{State: state, Reason: o.Error, Since: since}
		}
		next.States[p.Pin] = prior
		view.Computers = append(view.Computers, Computer{Pin: p.Pin, Name: p.Effective, State: state, Reason: o.Error, Since: prior.Since})
	}
	slices.SortFunc(view.Computers, func(a, b Computer) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	// The version moves when a pair, a name, a state or a reason changes.
	data, _ := json.Marshal(view)
	sum := sha256.Sum256(data)
	if digest := hex.EncodeToString(sum[:]); digest != next.Digest {
		next.Digest = digest
		next.N++
	}
	if !maps.Equal(next.States, t.saved.States) || next.N != t.saved.N {
		if err := d.store.saveComputers(ctx, next); err != nil {
			return ComputersView{}, "", err
		}
		t.saved = next
	}
	return view, fmt.Sprintf("%020d", t.saved.N), nil
}

// saveComputers stores the computers topic. It is a control write: the topic is small and
// replaced in place.
func (s *Store) saveComputers(ctx context.Context, saved computersSaved) error {
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	ctx = noAudit(ctx)
	tx, err := s.begin(ctx, control)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, computersSetting, string(data), s.now().UnixMilli()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

// validVersions bounds a signed changes request's versions: at most one per topic that can
// exist, each a topic name with a 20-character version.
func validVersions(versions map[string]any) (map[string]string, bool) {
	if len(versions) > maxRooms+3 {
		return nil, false
	}
	out := map[string]string{}
	for topic, v := range versions {
		version, ok := v.(string)
		if !ok || topic == "" || len(topic) > 64 || len(version) != 20 {
			return nil, false
		}
		out[topic] = version
	}
	return out, true
}

// changedTopics lists, in order, each topic whose version differs between the two maps, also
// one that only one of them has.
func changedTopics(held, current map[string]string) []string {
	return changedURIs(held, current)
}

// signedChanges answers a signed changes wait (Decision 6.2): the versions of every topic, and
// the topics whose version differs from held. Without held versions it answers at once. It
// reads again every changePoll until a topic changes or wait ends; a removal of the key, a
// cancelled request or a daemon stop ends it.
func (d *Daemon) signedChanges(ctx context.Context, key MaintainerKey, held map[string]string, wait time.Duration) (map[string]any, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(changePoll)
	defer ticker.Stop()
	for {
		versions, err := d.store.changeTopics(ctx)
		if err != nil {
			return nil, err
		}
		_, computers, err := d.computersView(ctx)
		if err != nil {
			return nil, err
		}
		versions[changeComputers] = computers
		changed := []string{}
		if held != nil {
			changed = changedTopics(held, versions)
		}
		if held == nil || len(changed) > 0 {
			return map[string]any{"versions": versions, "changed": changed}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.stop:
			return map[string]any{"versions": versions, "changed": changed}, nil
		case <-timer.C:
			return map[string]any{"versions": versions, "changed": changed}, nil
		case <-ticker.C:
		}
		if err := stillAuthorized(ctx, d.store.db, key); err != nil {
			return nil, err
		}
	}
}
