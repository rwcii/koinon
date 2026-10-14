package core

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// peers marks the calling session's own row (#348): by its resolved family and ID, never the
// maintainer row, and for two sessions of one family in one repository each sees only its own.
func TestPeersMarkTheCaller(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "peers-self")
	a := join(t, s, "claude", "synthetic-same", repo)
	b := join(t, s, "claude", "synthetic-other", repo)
	c := join(t, s, "codex", "synthetic-same", repo)
	for _, caller := range []Session{a, b, c} {
		peers, _, err := s.Peers(context.Background(), Key{caller.Family, caller.ID})
		if err != nil {
			t.Fatal(err)
		}
		var marked []string
		for _, p := range peers {
			if p.Self {
				marked = append(marked, p.Name)
			}
			if p.Family == "maintainer" && p.Self {
				t.Fatal("the maintainer row is marked")
			}
		}
		if len(marked) != 1 || marked[0] != caller.Name {
			t.Fatalf("%s/%s marked %v, want %s", caller.Family, caller.ID, marked, caller.Name)
		}
	}
	// Two sessions of one family in one repository: one holds the address, both are marked
	// only on their own rows, and the participant fields are unchanged.
	peers, _, _ := s.Peers(context.Background(), Key{b.Family, b.ID})
	for _, p := range peers {
		if p.Name == a.Name && (p.Self || p.Address != a.Address) || p.Name == b.Name && (!p.Self || p.Address != b.Address) {
			t.Fatalf("row %+v", p)
		}
	}
}

// The route returns the marker as a JSON field on the caller's row only.
func TestPeersRouteMarksTheCaller(t *testing.T) {
	d, root := startTestDaemon(t)
	a := join(t, d.store, "claude", "synthetic-route", namedRepo(t, "peers-route"))
	join(t, d.store, "claude", "synthetic-route-other", "")
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	status, reply := post(t, d.Addresses()[0], secret, "/v1/peers", map[string]any{"caller": map[string]any{"family": "claude", "id": a.ID}})
	data, _ := json.Marshal(reply)
	if status != 200 || strings.Count(string(data), `"self":true`) != 1 || !strings.Contains(string(data), `"name":"`+a.Name+`"`) {
		t.Fatalf("peers route: %d %s", status, data)
	}
}

// A truncated list keeps its first rows in order; the caller's row follows them, before the
// maintainer row, which stays last, and it is the same row, with its liveness evidence, as in
// the untruncated list.
func TestPeersTruncatedListKeepsTheCaller(t *testing.T) {
	s, _ := testStore(t)
	template := join(t, s, "agy", "synthetic-template", "")
	caller := join(t, s, "codex", "synthetic-caller", namedRepo(t, "peers-truncated"))
	if _, err := fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77 WHERE family=? AND id=?`, caller.Family, caller.ID); err != nil {
		t.Fatal(err)
	}
	s.alive.set(Key{caller.Family, caller.ID}, s.now().UnixMilli())
	ownRow := func() Peer {
		t.Helper()
		peers, _, err := s.Peers(context.Background(), Key{caller.Family, caller.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range peers {
			if p.Self {
				return p
			}
		}
		t.Fatal("no own row")
		return Peer{}
	}
	before := ownRow()
	if before.Liveness != "host" || before.HostVerifiedAt == 0 {
		t.Fatalf("setup: %+v", before)
	}
	var columns []string
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('sessions')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		columns = append(columns, name)
	}
	rows.Close()
	selected := strings.Replace(strings.Join(columns, ","), "id,", "?,", 1)
	for i := range 1001 {
		if _, err := fixtureExec(s, `INSERT INTO sessions(`+strings.Join(columns, ",")+`) SELECT `+selected+` FROM sessions WHERE family='agy' AND id=?`,
			fmt.Sprintf("synthetic-%04d", i), template.ID); err != nil {
			t.Fatal(err)
		}
	}
	listed, truncated, err := s.List(context.Background())
	if err != nil || !truncated {
		t.Fatalf("list: %v %v", truncated, err)
	}
	peers, truncated, err := s.Peers(context.Background(), Key{caller.Family, caller.ID})
	if err != nil || !truncated || len(peers) != len(listed)+2 {
		t.Fatalf("peers: %d rows, truncated %v, %v", len(peers), truncated, err)
	}
	for i, item := range listed {
		if peers[i].Family != item.Family || peers[i].Self {
			t.Fatalf("row %d: %+v", i, peers[i])
		}
	}
	if own, last := peers[len(peers)-2], peers[len(peers)-1]; !reflect.DeepEqual(own, before) || last.Family != "maintainer" || last.Self {
		t.Fatalf("tail: %+v (untruncated %+v) %+v", own, before, last)
	}
}
