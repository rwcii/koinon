package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

func testRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	cmd := exec.Command("git", "init", "-q", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return repo
}

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	root, err := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.db.Close() })
	return s, root
}

func registration(repo, family string) Registration {
	return Registration{Family: family, ID: "synthetic-session", Repository: repo, Directory: repo, WakeTarget: json.RawMessage(`{"endpoint":"synthetic"}`), TTLSeconds: 60}
}

func TestLifecycleForEveryFamily(t *testing.T) {
	repo := testRepo(t)
	for _, family := range []string{"claude", "codex", "deepseek", "agy", "opencode"} {
		t.Run(family, func(t *testing.T) {
			s, _ := testStore(t)
			clock := time.Unix(1000, 0)
			s.now = func() time.Time { return clock }
			r, err := s.Register(context.Background(), registration(repo, family))
			if err != nil {
				t.Fatal(err)
			}
			first := r.RegisteredAt
			if r.State != "active" || r.Revision != 1 || r.Repository != filepath.Join(repo, ".git") {
				t.Fatalf("unexpected registration: %+v", r)
			}
			clock = clock.Add(20 * time.Second)
			r, err = s.Mutate(context.Background(), Mutation{Family: family, ID: r.ID, IfRevision: r.Revision}, false)
			if err != nil || r.Revision != 2 || r.ExpiresAt != clock.UnixMilli()+900000 {
				t.Fatalf("renew: %+v %v", r, err)
			}
			_, err = s.Mutate(context.Background(), Mutation{Family: family, ID: r.ID, IfRevision: 1}, true)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("stale retire: %v", err)
			}
			r, err = s.Mutate(context.Background(), Mutation{Family: family, ID: r.ID, IfRevision: r.Revision}, true)
			if err != nil || r.State != "retired" {
				t.Fatalf("retire: %+v %v", r, err)
			}
			_, err = s.Mutate(context.Background(), Mutation{Family: family, ID: r.ID, IfRevision: r.Revision}, false)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("retired renew: %v", err)
			}
			r, err = s.Register(context.Background(), registration(repo, family))
			if err != nil {
				t.Fatal(err)
			}
			if r.RegisteredAt != first || r.State != "active" {
				t.Fatalf("state lost on return: %+v", r)
			}
			clock = clock.Add(61 * time.Second)
			items, _, err := s.List(context.Background())
			if err != nil || len(items) != 1 || items[0].State != "expired" {
				t.Fatalf("expiry: %+v %v", items, err)
			}
			_, err = s.Mutate(context.Background(), Mutation{Family: family, ID: r.ID, IfRevision: r.Revision}, false)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("expired renew: %v", err)
			}
			// Wall-clock jumps change effective expiry, but never delete records.
			clock = clock.Add(-2 * time.Hour)
			items, _, err = s.List(context.Background())
			if err != nil || len(items) != 1 || items[0].State != "active" {
				t.Fatalf("backward jump: %+v %v", items, err)
			}
		})
	}
}

func TestConcurrentRegistrationAndRestart(t *testing.T) {
	s, root := testStore(t)
	repo := testRepo(t)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Register(context.Background(), registration(repo, "codex")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	items, _, err := s.List(context.Background())
	if err != nil || len(items) != 1 || items[0].Revision != 20 {
		t.Fatalf("duplicate or lost registration: %+v %v", items, err)
	}
	s.db.Close()
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	restored, _, err := reopened.List(context.Background())
	if err != nil || len(restored) != 1 || restored[0].Revision != 20 || string(restored[0].WakeTarget) != string(items[0].WakeTarget) {
		t.Fatalf("restart lost state: %+v %v", restored, err)
	}
}

func TestRegistrationValidationAndRepositoryIdentity(t *testing.T) {
	s, _ := testStore(t)
	repo := testRepo(t)
	other := testRepo(t)
	for _, change := range []func(*Registration){
		func(r *Registration) { r.Family = "unknown" }, func(r *Registration) { r.ID = "" },
		func(r *Registration) { r.TTLSeconds = 1 }, func(r *Registration) { r.TTLSeconds = 3601 },
		func(r *Registration) { r.Directory = other }, func(r *Registration) { r.Repository = "relative" },
		func(r *Registration) { r.WakeTarget = bytes.Repeat([]byte("a"), 8193) },
	} {
		r := registration(repo, "codex")
		change(&r)
		if _, err := s.Register(context.Background(), r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid accepted: %v", err)
		}
	}
	if counts, err := s.Counts(context.Background()); err != nil || counts["total"] != 0 {
		t.Fatalf("partial record: %+v %v", counts, err)
	}
	// Synthetic worktrees share the absolute Git common directory.
	for _, args := range [][]string{{"-C", repo, "-c", "user.name=Synthetic", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "fixture"}, {"-C", repo, "worktree", "add", "--detach", repo + "-worktree"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	r := registration(repo, "codex")
	r.Directory = repo + "-worktree"
	got, err := s.Register(context.Background(), r)
	if err != nil || got.Repository != filepath.Join(repo, ".git") {
		t.Fatalf("worktree identity: %+v %v", got, err)
	}
}

func startTestDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0", "[::1]:0"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d, root
}

func request(t *testing.T, d *Daemon, path, body, secret string) *http.Response {
	t.Helper()
	method := "GET"
	if body != "" {
		method = "POST"
	}
	r, err := http.NewRequest(method, "http://"+d.Addresses()[0]+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close(); client.CloseIdleConnections() })
	return response
}

func TestAuthenticatedLoopbackAPI(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "wrong-secret", strings.Repeat("a", 64)} {
		if response := request(t, d, "/v1/status", "", key); response.StatusCode != 401 {
			t.Fatalf("secret accepted: %d", response.StatusCode)
		}
	}
	for _, address := range d.Addresses() {
		data, err := GetStatus(context.Background(), address, secret)
		if err != nil || !bytes.Contains(data, []byte(`"total":0`)) {
			t.Fatalf("status: %s %v", data, err)
		}
	}
	repo := testRepo(t)
	body, _ := json.Marshal(registration(repo, "opencode"))
	response := request(t, d, "/v1/sessions/register", string(body), secret)
	if response.StatusCode != 200 {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("register: %s", data)
	}
	var result struct {
		Session Session `json:"session"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	mutation, _ := json.Marshal(Mutation{Family: result.Session.Family, ID: result.Session.ID, IfRevision: result.Session.Revision})
	if response = request(t, d, "/v1/sessions/retire", string(mutation), secret); response.StatusCode != 200 {
		t.Fatalf("retire: %d", response.StatusCode)
	}
	if response = request(t, d, "/v1/sessions/renew", string(mutation), secret); response.StatusCode != 409 {
		t.Fatalf("stale renew: %d", response.StatusCode)
	}
	for _, body := range []string{`{}`, `{"family":"codex","id":"x","unexpected":true}`, strings.Repeat("a", 20000)} {
		if response = request(t, d, "/v1/sessions/register", body, secret); response.StatusCode != 400 {
			t.Fatalf("invalid request: %d", response.StatusCode)
		}
	}
	// Refuse browser-origin traffic even when it somehow holds a bearer.
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Host = "attacker.example:80" }, func(r *http.Request) { r.Header.Set("Origin", "http://attacker.example") }} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:47671/v1/status", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		change(r)
		w := httptest.NewRecorder()
		d.handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("foreign request accepted: %d", w.Code)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0", "[::1]:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.secret != secret {
		t.Fatal("secret changed on restart")
	}
	items, _, err := restarted.store.List(context.Background())
	if err != nil || len(items) != 1 || items[0].State != "retired" {
		t.Fatalf("restart: %+v %v", items, err)
	}
}

func TestRefuseUnsafeListenersAndFiles(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "[::]:0", "192.0.2.1:0", "localhost:0", "example.com:0"} {
		root := filepath.Join(t.TempDir(), "state")
		if d, err := Start(Config{StateDir: root, Listen: []string{address}}); err == nil {
			d.Close()
			t.Fatalf("unsafe bind: %s", address)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid bind created state: %v", err)
		}
	}
	for _, file := range []string{"secret", "state.sqlite3", "state.sqlite3-wal", "daemon.lock"} {
		t.Run(file, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "target")
			os.WriteFile(target, []byte("preserve"), 0600)
			if err := os.Symlink(target, filepath.Join(root, file)); err != nil {
				t.Fatal(err)
			}
			if d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}}); err == nil {
				d.Close()
				t.Fatal("symlink accepted")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "preserve" {
				t.Fatal("unsafe target changed")
			}
		})
	}
	for _, mode := range []os.FileMode{0400, 0640, 0666} {
		root, _ := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
		path := filepath.Join(root, "secret")
		os.WriteFile(path, bytes.Repeat([]byte("a"), 64), 0600)
		os.Chmod(path, mode)
		if d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}}); err == nil {
			d.Close()
			t.Fatalf("unsafe mode: %o", mode)
		}
	}
}

func TestStatusDoesNotFollowRedirectsOrProxy(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.Write([]byte(`{}`)) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	t.Setenv("HTTP_PROXY", target.URL)
	if _, err := GetStatus(context.Background(), strings.TrimPrefix(redirect.URL, "http://"), strings.Repeat("b", 64)); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("credential sent to redirect or proxy")
	}
	if _, err := GetStatus(context.Background(), "example.com:80", "private"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-loopback status: %v", err)
	}
}

func TestStorageRefusalHasNoPartialRecord(t *testing.T) {
	for _, fault := range []string{"read-only", "full"} {
		t.Run(fault, func(t *testing.T) {
			s, _ := testStore(t)
			repo := testRepo(t)
			if fault == "read-only" {
				if _, err := s.db.Exec("PRAGMA query_only=ON"); err != nil {
					t.Fatal(err)
				}
			} else {
				var pages int
				if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec("PRAGMA max_page_count=" + fmt.Sprint(pages)); err != nil {
					t.Fatal(err)
				}
			}
			r := registration(repo, "codex")
			r.WakeTarget = json.RawMessage(`{"synthetic":"` + strings.Repeat("x", 7900) + `"}`)
			if _, err := s.Register(context.Background(), r); err == nil {
				t.Fatal("storage fault accepted mutation")
			}
			counts, err := s.Counts(context.Background())
			if err != nil || counts["total"] != 0 {
				t.Fatalf("partial mutation: %+v %v", counts, err)
			}
		})
	}
}
