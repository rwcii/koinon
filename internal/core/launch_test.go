package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchBindingAndRestart(t *testing.T) {
	s, root := testStore(t)
	directory := testRepo(t)
	for _, family := range []string{"claude", "codex", "agy", "opencode"} {
		target := LaunchTarget{Family: family, Directory: directory, CLI: "/synthetic/cli", HostPID: 123}
		if family == "opencode" {
			target.Address = "127.0.0.1:12345"
			target.Password = strings.Repeat("ab", 32)
		}
		if family == "codex" {
			target.Nested = []NestedRepository{{Path: "vendor/library", Kind: "submodule"}, {Path: "clone", Kind: "repository"}}
			target.NestedIncomplete = true
		}
		id, err := s.CreateLaunch(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		// The caller's ancestry holds the launch's host; Claude also reports it as claude_pid.
		var own json.RawMessage
		if family == "claude" {
			own = json.RawMessage(`{"claude_pid":123}`)
		}
		r := Registration{Family: family, ID: "synthetic-real-session", Directory: directory, LaunchID: id, Ancestors: []int{77, 123}, WakeTarget: own}
		session, err := s.Register(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		var stored LaunchTarget
		public := target
		public.Password = ""
		public.LaunchID = id
		if json.Unmarshal(session.WakeTarget, &stored) != nil || !reflect.DeepEqual(stored, public) {
			t.Fatal("launch target not bound")
		}
		var private string
		if err := s.db.QueryRow("SELECT target FROM launches WHERE id=?", id).Scan(&private); err != nil {
			t.Fatal(err)
		}
		stored = LaunchTarget{}
		if json.Unmarshal([]byte(private), &stored) != nil || !reflect.DeepEqual(stored, target) {
			t.Fatal("private launch target was changed")
		}
		// One live CLI can serve another native identity after a context reset.
		r.ID = "synthetic-next-session"
		if _, err := s.Register(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		r.Family = "agy"
		if family == "agy" {
			r.Family = "codex"
		}
		if family == "claude" {
			r.WakeTarget = nil
		}
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrNotLaunched) {
			t.Fatal("wrong launch family accepted")
		}
		r.Family, r.WakeTarget = family, own
		r.Directory = t.TempDir()
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrNotLaunched) {
			t.Fatal("wrong launch directory accepted")
		}
		r.Directory = directory
		r.WakeTarget = json.RawMessage(`{"override":true}`)
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatal("launch target override accepted")
		}
		if err := s.db.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = openStore(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.db.Close() })
		r.WakeTarget = own
		if _, err := s.Register(context.Background(), r); err != nil {
			t.Fatalf("launch target lost on restart: %v", err)
		}
	}
}

func TestLaunchAuthenticationAndCredentialPrivacy(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	target := LaunchTarget{Family: "opencode", Directory: testRepo(t), CLI: "/synthetic/cli", HostPID: 123, Address: "127.0.0.1:12345", Password: strings.Repeat("ab", 32)}
	body, _ := json.Marshal(target)
	response := request(t, d, "/v1/launches", string(body), "")
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated launch accepted")
	}
	response = request(t, d, "/v1/launches", string(body), secret)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || strings.Contains(string(data), target.Password) {
		t.Fatal("launch failed or response leaked credential")
	}
	var launch struct {
		ID string `json:"launch_id"`
	}
	if json.Unmarshal(data, &launch) != nil || launch.ID == "" {
		t.Fatal("missing launch id")
	}
	registration, _ := json.Marshal(Registration{Family: "opencode", ID: "synthetic-private-target", Directory: target.Directory, LaunchID: launch.ID, Ancestors: []int{123}})
	response = request(t, d, "/v1/sessions/register", string(registration), secret)
	data, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || strings.Contains(string(data), target.Password) || strings.Contains(string(data), `"password"`) {
		t.Fatal("registration response leaked credential")
	}
	request, _ := http.NewRequest("GET", "http://"+d.Addresses()[0]+"/v1/sessions", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || strings.Contains(string(data), target.Password) || strings.Contains(string(data), `"password"`) {
		t.Fatal("session list leaked credential")
	}
	peers, err := Call(context.Background(), d.Addresses()[0], secret, "/v1/peers", map[string]any{"caller": Key{"opencode", "synthetic-private-target"}})
	if err != nil || strings.Contains(string(peers), target.Password) || strings.Contains(string(peers), `"password"`) || strings.Contains(string(peers), `"wake_target"`) {
		t.Fatal("peer list leaked credential or target")
	}
	for _, address := range []string{"0.0.0.0:12345", "[::]:12345", "example.com:12345", "127.0.0.1:0/path"} {
		target.Address = address
		if _, err := d.store.CreateLaunch(context.Background(), target); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe target accepted: %s", address)
		}
	}
	// Rejected registrations must not insert a partial session.
	if _, err := d.store.Register(context.Background(), Registration{Family: "codex", ID: "synthetic-missing-launch", Directory: target.Directory, LaunchID: strings.Repeat("a", 64), Ancestors: []int{123}}); !errors.Is(err, ErrNotLaunched) {
		t.Fatal("unknown launch accepted")
	}
	counts, err := d.store.Counts(context.Background())
	if err != nil || counts["total"] != 1 {
		t.Fatal("partial registration inserted")
	}
}

func TestLaunchMigrationPreservesSessions(t *testing.T) {
	s, root := testStore(t)
	r := registration(t, s, testRepo(t), "codex")
	before, err := s.Register(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.Send(context.Background(), Key{r.Family, r.ID}, before.Name, "synthetic preserved inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ack(context.Background(), Key{r.Family, r.ID}, message.Seq); err != nil {
		t.Fatal(err)
	}
	// Schema 2 is the merged message runtime, with no launch table.
	if _, err := s.db.Exec(undoSchemaTen + undoSchemaNine + undoSchemaEight + undoSchemaSeven + "DROP INDEX messages_wake; ALTER TABLE messages DROP COLUMN wake_attempts; ALTER TABLE messages DROP COLUMN wake_next_at; ALTER TABLE messages DROP COLUMN wake_reason; DROP TABLE launches; DROP TABLE work_items; DROP TABLE work_scope_revisions; DROP TABLE claim_bundles; DROP TABLE claim_resources; DROP TABLE work_events; DROP TABLE work_replays; DROP TABLE memory_stores; DROP TABLE memory_entries; DROP TABLE memory_idem; DROP TABLE memory_cursors; DROP TABLE memory_retired; DROP TABLE memory_snapshots; DROP TABLE memory_snapshot_items; PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	items, _, err := s.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Revision != before.Revision || items[0].ID != before.ID || items[0].Name != before.Name || items[0].Alias != before.Alias {
		t.Fatal("migration changed session")
	}
	inbox, err := s.ReadInbox(context.Background(), Key{r.Family, r.ID}, 0, 100)
	if err != nil || inbox.LastSeq != 1 || inbox.AckedThrough != 1 || len(inbox.Messages) != 1 || !inbox.Messages[0].Acknowledged || inbox.Messages[0].Body != "synthetic preserved inbox" {
		t.Fatal("migration changed inbox or acknowledgement")
	}
	if _, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "codex", Directory: r.Directory, CLI: filepath.Join(r.Directory, "synthetic-cli"), HostPID: 123}); err != nil {
		t.Fatal(err)
	}
}

// A launch's nested list is bounded and holds only clean relative paths and known kinds
// (#252); the session view reads it from the bound launch target.
func TestLaunchNestedRepositories(t *testing.T) {
	s, _ := testStore(t)
	directory := testRepo(t)
	long := strings.Repeat("a", MaxNestedPath+1)
	many := make([]NestedRepository, MaxNested+1)
	for i := range many {
		many[i] = NestedRepository{Path: "r" + strings.Repeat("x", i), Kind: "repository"}
	}
	for _, list := range [][]NestedRepository{
		{{Path: "", Kind: "repository"}}, {{Path: "/abs", Kind: "repository"}}, {{Path: "..", Kind: "repository"}},
		{{Path: filepath.Join("..", "up"), Kind: "repository"}}, {{Path: "a//b", Kind: "repository"}}, {{Path: ".", Kind: "repository"}},
		{{Path: "line\nbreak", Kind: "repository"}}, {{Path: long, Kind: "repository"}}, {{Path: "ok", Kind: "clone"}}, many,
	} {
		target := LaunchTarget{Family: "agy", Directory: directory, CLI: "/synthetic/cli", HostPID: 123, Nested: list}
		if _, err := s.CreateLaunch(context.Background(), target); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nested list accepted: %.60q", list)
		}
	}
	list := []NestedRepository{{Path: "vendor-infra", Kind: "submodule"}, {Path: filepath.Join("tools", "wt"), Kind: "worktree"}}
	id, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "agy", Directory: directory, CLI: "/synthetic/cli", HostPID: 123, Nested: list})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.Register(context.Background(), Registration{Family: "agy", ID: "synthetic-nested", Directory: directory, LaunchID: id, Ancestors: []int{123}})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.NestedRepositories(); got == nil || !reflect.DeepEqual(got.List, list) || got.Incomplete {
		t.Fatalf("session nested report: %+v", got)
	}
	if (Session{WakeTarget: json.RawMessage(`{"claude_pid":1}`)}).NestedRepositories() != nil {
		t.Fatal("report without a nested list")
	}
}

func TestDashboardShowsNestedRepositories(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := testRepo(t)
	list := []NestedRepository{{Path: "vendor-infra", Kind: "submodule"}, {Path: "<b>clone", Kind: "repository"}}
	id, err := d.store.CreateLaunch(context.Background(), LaunchTarget{Family: "agy", Directory: directory, CLI: "/synthetic/cli", HostPID: 123, Nested: list, NestedIncomplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.store.Register(context.Background(), Registration{Family: "agy", ID: "synthetic-nested", Directory: directory, LaunchID: id, Ancestors: []int{123}}); err != nil {
		t.Fatal(err)
	}
	// The largest list, with characters that JSON escapes, fits the launch route.
	largest := make([]NestedRepository, MaxNested)
	for i := range largest {
		largest[i] = NestedRepository{Path: fmt.Sprintf("%03d", i) + strings.Repeat("<", MaxNestedPath-3), Kind: "repository"}
	}
	if _, err := CreateLaunch(context.Background(), d.Addresses()[0], secret, LaunchTarget{Family: "agy", Directory: directory, CLI: "/" + strings.Repeat("c", 4095), HostPID: 123, Nested: largest}); err != nil {
		t.Fatalf("largest nested list refused: %v", err)
	}
	page := dashboardDo(t, d, "GET", "/dashboard/sessions", dashboardLogin(t, d, secret), nil, nil).body
	for _, want := range []string{"2+ nested repositories", "<code>vendor-infra</code> submodule", "<code>&lt;b&gt;clone</code> repository", "list incomplete", "Koinon repository is the start repository"} {
		if !strings.Contains(page, want) {
			t.Fatalf("session view lacks %q", want)
		}
	}
}
