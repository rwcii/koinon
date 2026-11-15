package core

// The computers of a node and the signed changes wait (#693; decision.md Decisions 2.4, 6.2
// and 6.3). computers gives the node's own pin and name and, for each paired computer, its
// pin, name, connection state, reason and the time the state began. The topic computers of a
// changes wait on the node moves when that view changes.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rwcii/koinon/internal/link"
)

const changeComputers = "computers"

// computersTopic keeps the computers topic's version in the daemon's memory. Its versions
// start at the daemon's start time in microseconds, so a version after a restart is larger
// than every version before it, and a client's held version never matches a new view by
// chance.
type computersTopic struct {
	mu     sync.Mutex
	base   int64
	n      int64
	digest string
	// states holds each pin's state and when it began.
	states map[string]computerState
}

type computerState struct {
	state, reason string
	since         int64
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
func (d *Daemon) computersView() (ComputersView, string, error) {
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
	if t.states == nil {
		t.base, t.states = time.Now().UnixMicro(), map[string]computerState{}
	}
	seen := map[string]bool{}
	for _, p := range s.Pairs {
		if p.State != link.Paired {
			continue
		}
		o := pairObservation(p, observed[p.ID], open, gen, now)
		state := "offline"
		if o.State == "connected" {
			state = "connected"
		}
		prior, known := t.states[p.Pin]
		if !known || prior.state != state || prior.reason != o.Error {
			since := o.At
			if known {
				since = now
			}
			prior = computerState{state: state, reason: o.Error, since: since}
			t.states[p.Pin] = prior
		}
		seen[p.Pin] = true
		view.Computers = append(view.Computers, Computer{Pin: p.Pin, Name: p.Effective, State: state, Reason: o.Error, Since: prior.since})
	}
	for pin := range t.states {
		if !seen[pin] {
			delete(t.states, pin)
		}
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
	if digest := hex.EncodeToString(sum[:]); digest != t.digest {
		t.digest = digest
		t.n++
	}
	return view, fmt.Sprintf("%020d", t.base+t.n), nil
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
		_, computers, err := d.computersView()
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
