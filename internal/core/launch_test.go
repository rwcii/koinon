package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchBindingAndRestart(t *testing.T) {
	s, root := testStore(t)
	directory := testRepo(t)
	for _, family := range []string{"codex", "agy", "opencode"} {
		target := LaunchTarget{Family: family, Directory: directory, CLI: "/synthetic/cli", HostPID: 123}
		if family == "opencode" {
			target.Address = "127.0.0.1:12345"
			target.Password = strings.Repeat("ab", 32)
		}
		id, err := s.CreateLaunch(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		r := Registration{Family: family, ID: "synthetic-real-session", Directory: directory, LaunchID: id}
		session, err := s.Register(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		var stored LaunchTarget
		public := target
		public.Password = ""
		public.LaunchID = id
		if json.Unmarshal(session.WakeTarget, &stored) != nil || stored != public {
			t.Fatal("launch target not bound")
		}
		var private string
		if err := s.db.QueryRow("SELECT target FROM launches WHERE id=?", id).Scan(&private); err != nil {
			t.Fatal(err)
		}
		stored = LaunchTarget{}
		if json.Unmarshal([]byte(private), &stored) != nil || stored != target {
			t.Fatal("private launch target was changed")
		}
		// One live CLI can serve another native identity after a context reset.
		r.ID = "synthetic-next-session"
		if _, err := s.Register(context.Background(), r); err != nil {
			t.Fatal(err)
		}
		r.Family = "claude"
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatal("wrong launch family accepted")
		}
		r.Family = family
		r.Directory = t.TempDir()
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrInvalid) {
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
		r.WakeTarget = nil
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
	registration, _ := json.Marshal(Registration{Family: "opencode", ID: "synthetic-private-target", Directory: target.Directory, LaunchID: launch.ID})
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
	for _, address := range []string{"0.0.0.0:12345", "[::]:12345", "example.com:12345", "127.0.0.1:0/path"} {
		target.Address = address
		if _, err := d.store.CreateLaunch(context.Background(), target); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe target accepted: %s", address)
		}
	}
	// Rejected registrations must not insert a partial session.
	if _, err := d.store.Register(context.Background(), Registration{Family: "codex", ID: "synthetic-missing-launch", Directory: target.Directory, LaunchID: strings.Repeat("a", 64)}); !errors.Is(err, ErrMissing) {
		t.Fatal("unknown launch accepted")
	}
	counts, err := d.store.Counts(context.Background())
	if err != nil || counts["total"] != 1 {
		t.Fatal("partial registration inserted")
	}
}

func TestLaunchMigrationPreservesSessions(t *testing.T) {
	s, root := testStore(t)
	r := registration(testRepo(t), "codex")
	before, err := s.Register(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE launches; PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	s, err = openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	items, _, err := s.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Revision != before.Revision || items[0].ID != before.ID {
		t.Fatal("migration changed session")
	}
	if _, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "codex", Directory: r.Directory, CLI: filepath.Join(r.Directory, "synthetic-cli"), HostPID: 123}); err != nil {
		t.Fatal(err)
	}
}
