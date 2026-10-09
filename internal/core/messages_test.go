package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

var families = []string{"claude", "codex", "deepseek", "agy", "opencode"}

// namedRepo makes a synthetic repository whose directory has the given name.
func namedRepo(t *testing.T, name string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	repo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func join(t *testing.T, s *Store, family, id, repo string) Session {
	t.Helper()
	r := Registration{Family: family, ID: id, Repository: repo, Directory: repo, TTLSeconds: 60}
	if repo == "" {
		r.Directory = t.TempDir()
	}
	got, err := s.Register(context.Background(), withLaunch(t, s, r))
	if err != nil {
		t.Fatalf("register %s:%s: %v", family, id, err)
	}
	return got
}

func post(t *testing.T, address, secret, path string, body any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r, err := http.NewRequest("POST", "http://"+address+path, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Content-Type", "application/json")
	client := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, result
}

func TestLabelsAndPeerNames(t *testing.T) {
	for _, c := range [][3]string{
		{"/x/Koinon/.git", "", "koinon"}, {"/x/My Repo.git", "", "my-repo"}, {"", "/x/scratch_dir", "scratch-dir"},
		{"", "/x/---", "session"}, {"/x/" + strings.Repeat("a", 31) + "-b/.git", "", strings.Repeat("a", 31)},
	} {
		if got := label(c[0], c[1]); got != c[2] {
			t.Fatalf("label(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	first := join(t, s, "codex", "synthetic-a", repo)
	if !regexp.MustCompile(`^codex-koinon-[0-9a-f]{2}$`).MatchString(first.Name) || first.Alias != "codex-koinon" {
		t.Fatalf("names: %+v", first)
	}
	// The peer name is permanent across renewal, retirement and re-registration.
	renewed, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-a", IfRevision: first.Revision}, false)
	if err != nil || renewed.Name != first.Name {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	retired, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-a", IfRevision: renewed.Revision}, true)
	if err != nil || retired.Name != first.Name || retired.Alias != "" {
		t.Fatalf("retire: %+v %v", retired, err)
	}
	if again := join(t, s, "codex", "synthetic-a", repo); again.Name != first.Name || again.Alias != "codex-koinon" {
		t.Fatalf("re-registration: %+v", again)
	}
	// A session without a repository has a peer name and no alias.
	plain := join(t, s, "claude", "synthetic-plain", "")
	if !strings.HasPrefix(plain.Name, "claude-") || plain.Alias != "" {
		t.Fatalf("plain session: %+v", plain)
	}
	// All 256 two-hex names taken: the next name uses four hex digits.
	other := namedRepo(t, "busy")
	for i := range 256 {
		if _, err := s.db.Exec(`INSERT INTO names(name,kind,family,session_id) VALUES (?,'peer','codex',?)`, fmt.Sprintf("codex-busy-%02x", i), fmt.Sprint("filler-", i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := join(t, s, "codex", "synthetic-busy", other); !regexp.MustCompile(`^codex-busy-[0-9a-f]{4}$`).MatchString(got.Name) {
		t.Fatalf("exhausted two-hex names: %+v", got)
	}
}

func TestAliasUniquenessAcrossRepositories(t *testing.T) {
	s, root := testStore(t)
	a, b := namedRepo(t, "koinon"), namedRepo(t, "koinon")
	first, second := join(t, s, "codex", "synthetic-a", a), join(t, s, "codex", "synthetic-b", b)
	if first.Alias != "codex-koinon" || !regexp.MustCompile(`^codex-koinon-[0-9a-f]{4}$`).MatchString(second.Alias) {
		t.Fatalf("same label: %+v %+v", first, second)
	}
	// Each family has its own alias for one repository.
	if got := join(t, s, "claude", "synthetic-c", a); got.Alias != "claude-koinon" {
		t.Fatalf("family alias: %+v", got)
	}
	// A peer name that equals an alias candidate forces the suffixed alias.
	foo := join(t, s, "codex", "synthetic-foo", namedRepo(t, "foo"))
	suffix := strings.TrimPrefix(foo.Name, "codex-foo-")
	clash := join(t, s, "codex", "synthetic-clash", namedRepo(t, "foo-"+suffix))
	if clash.Alias == foo.Name || !strings.HasPrefix(clash.Alias, foo.Name+"-") {
		t.Fatalf("peer name and alias: %+v %+v", foo, clash)
	}
	// A taken four-hex suffix moves to six hex digits.
	c := namedRepo(t, "koinon")
	common, err := repository(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256Sum(common))
	if _, err := s.db.Exec(`INSERT INTO names(name,kind,family,session_id) VALUES (?,'peer','codex','filler')`, "codex-koinon-"+digest[:4]); err != nil {
		t.Fatal(err)
	}
	if got := join(t, s, "codex", "synthetic-six", c); got.Alias != "codex-koinon-"+digest[:6] {
		t.Fatalf("six hex: %q, digest %s", got.Alias, digest)
	}
	// Reservations survive a restart.
	s.db.Close()
	reopened, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if got := join(t, reopened, "codex", "synthetic-b", b); got.Alias != second.Alias || got.Name != second.Name {
		t.Fatalf("restart: %+v %+v", got, second)
	}
}

func TestAliasHolderMoves(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	clock := time.Unix(1000, 0)
	s.now = func() time.Time { return clock }
	a := join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "codex", "synthetic-b", repo)
	sender := join(t, s, "claude", "synthetic-sender", repo)
	if a.Alias != "codex-koinon" || b.Alias != "" {
		t.Fatalf("one holder: %+v %+v", a, b)
	}
	me := Key{"claude", "synthetic-sender"}
	if out, err := s.Send(context.Background(), me, "codex-koinon", "to holder"); err != nil || out.Recipient != a.Name {
		t.Fatalf("alias send: %+v %v", out, err)
	}
	if _, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-a", IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(context.Background(), me, "codex-koinon", "unheld"); !errors.Is(err, ErrAliasUnheld) {
		t.Fatalf("retired holder: %v", err)
	}
	// The next renewal of the same family and repository takes the free alias.
	b, err := s.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-b", IfRevision: b.Revision}, false)
	if err != nil || b.Alias != "codex-koinon" {
		t.Fatalf("renewal takes alias: %+v %v", b, err)
	}
	if out, err := s.Send(context.Background(), me, "codex-koinon", "to new holder"); err != nil || out.Recipient != b.Name {
		t.Fatalf("moved alias: %+v %v", out, err)
	}
	// An expired holder frees the alias for the next registration. The renewal above
	// took the default lease of 900 seconds.
	clock = clock.Add(901 * time.Second)
	if _, err := s.Mutate(context.Background(), Mutation{Family: "claude", ID: "synthetic-sender", IfRevision: sender.Revision}, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired sender renewed: %v", err)
	}
	if again := join(t, s, "codex", "synthetic-a", repo); again.Alias != "codex-koinon" {
		t.Fatalf("expired holder: %+v", again)
	}
	// A holder that moves to another repository no longer holds the old alias.
	moved := Registration{Family: "codex", ID: "synthetic-a", Repository: namedRepo(t, "other"), TTLSeconds: 60}
	moved.Directory = moved.Repository
	if _, err := s.Register(context.Background(), withLaunch(t, s, moved)); err != nil {
		t.Fatal(err)
	}
	if b := join(t, s, "codex", "synthetic-b", repo); b.Alias != "codex-koinon" {
		t.Fatalf("holder moved: %+v", b)
	}
}

func TestConcurrentRegistrationLeavesOneAliasHolder(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		r := withLaunch(t, s, Registration{Family: "codex", ID: fmt.Sprint("synthetic-", i), Repository: repo, Directory: repo, TTLSeconds: 60})
		go func() {
			defer wg.Done()
			if _, err := s.Register(context.Background(), r); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	items, _, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	holders, names := 0, map[string]bool{}
	for _, item := range items {
		if item.Alias == "codex-koinon" {
			holders++
		}
		if names[item.Name] || item.Name == "codex-koinon" {
			t.Fatalf("duplicate name %q", item.Name)
		}
		names[item.Name] = true
	}
	if holders != 1 || len(names) != 20 {
		t.Fatalf("holders %d, names %d", holders, len(names))
	}
}

func TestMessagesBetweenEveryFamilyPair(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	repo := namedRepo(t, "koinon")
	names := map[string]string{}
	for _, family := range families {
		names[family] = join(t, d.store, family, "synthetic-"+family, repo).Name
	}
	for _, from := range families {
		for _, to := range families {
			t.Run(from+"-"+to, func(t *testing.T) {
				caller, recipient := Key{from, "synthetic-" + from}, Key{to, "synthetic-" + to}
				body := "synthetic message " + from + " to " + to
				status, sent := post(t, address, secret, "/v1/messages/send", map[string]any{"caller": caller, "to": names[to], "body": body})
				if status != 200 {
					t.Fatalf("send: %d %v", status, sent)
				}
				message := sent["message"].(map[string]any)
				id, seq := message["id"].(float64), message["seq"].(float64)
				if message["delivery_state"] != "waiting" || message["recipient"] != names[to] {
					t.Fatalf("sent: %v", message)
				}
				status, read := post(t, address, secret, "/v1/inbox/read", map[string]any{"caller": recipient, "after": seq - 1})
				inbox := read["inbox"].(map[string]any)
				got := inbox["messages"].([]any)
				if status != 200 || len(got) != 1 {
					t.Fatalf("read: %d %v", status, read)
				}
				m := got[0].(map[string]any)
				if m["body"] != body || m["sender_name"] != names[from] || m["sender_family"] != from || m["seq"] != seq || m["acknowledged"] != false || m["delivery_state"] != "waiting" {
					t.Fatalf("stored: %v", m)
				}
				if status, acked := post(t, address, secret, "/v1/inbox/ack", map[string]any{"caller": recipient, "through": seq}); status != 200 || acked["acked_through"] != seq {
					t.Fatalf("ack: %d %v", status, acked)
				}
				status, outcome := post(t, address, secret, "/v1/messages/outcome", map[string]any{"caller": caller, "message_id": id})
				if o := outcome["message"].(map[string]any); status != 200 || o["acknowledged"] != true || o["delivery_state"] != "waiting" {
					t.Fatalf("outcome: %d %v", status, outcome)
				}
			})
		}
	}
	status, listing := post(t, address, secret, "/v1/peers", map[string]any{"caller": Key{"codex", "synthetic-codex"}})
	// Every family, and the built-in maintainer, which agents can send to.
	if status != 200 || len(listing["peers"].([]any)) != len(families)+1 {
		t.Fatalf("peers: %d %v", status, listing)
	}
	// Peers see names, aliases, families, states and repositories, never another
	// session's wake target, directory or key.
	for _, p := range listing["peers"].([]any) {
		for field := range p.(map[string]any) {
			switch field {
			case "name", "alias", "family", "state", "repository":
			default:
				t.Fatalf("peer field %q: %v", field, p)
			}
		}
	}
}

func TestMessageErrorsAndIsolation(t *testing.T) {
	d, root := startTestDaemon(t)
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	address := d.Addresses()[0]
	repo := namedRepo(t, "koinon")
	a := join(t, d.store, "codex", "synthetic-a", repo)
	b := join(t, d.store, "claude", "synthetic-b", repo)
	c := join(t, d.store, "deepseek", "synthetic-c", repo)
	ka, kb, kc := Key{"codex", "synthetic-a"}, Key{"claude", "synthetic-b"}, Key{"deepseek", "synthetic-c"}
	expect := func(path string, body map[string]any, status int, code string) {
		t.Helper()
		got, result := post(t, address, secret, path, body)
		if got != status || result["code"] != code {
			t.Fatalf("%s: %d %v, want %d %s", path, got, result, status, code)
		}
	}
	expect("/v1/messages/send", map[string]any{"caller": ka, "to": "codex-unknown-00", "body": "x"}, 404, "peer_not_found")
	expect("/v1/messages/send", map[string]any{"caller": Key{"codex", "synthetic-none"}, "to": b.Name, "body": "x"}, 403, "caller_inactive")
	for _, body := range []string{"", strings.Repeat("\x01", maxBody+1)} {
		expect("/v1/messages/send", map[string]any{"caller": ka, "to": b.Name, "body": body}, 400, "invalid_request")
	}
	// The largest body passes even when JSON escaping grows it sixfold.
	if status, result := post(t, address, secret, "/v1/messages/send", map[string]any{"caller": ka, "to": b.Name, "body": strings.Repeat("\x01", maxBody)}); status != 200 {
		t.Fatalf("largest body: %d %v", status, result)
	}
	status, sent := post(t, address, secret, "/v1/messages/send", map[string]any{"caller": ka, "to": b.Name, "body": "second"})
	if status != 200 {
		t.Fatal(sent)
	}
	id := sent["message"].(map[string]any)["id"]
	// Another session reads neither the recipient's inbox nor the sender's outcome.
	if _, read := post(t, address, secret, "/v1/inbox/read", map[string]any{"caller": kc}); len(read["inbox"].(map[string]any)["messages"].([]any)) != 0 {
		t.Fatalf("inbox isolation: %v", read)
	}
	expect("/v1/messages/outcome", map[string]any{"caller": kc, "message_id": id}, 404, "message_not_found")
	expect("/v1/messages/outcome", map[string]any{"caller": kb, "message_id": id}, 404, "message_not_found")
	// Acknowledgement is monotonic and bounded by the last sequence.
	expect("/v1/inbox/ack", map[string]any{"caller": kb, "through": 3}, 409, "ack_beyond_last")
	for _, through := range []int{2, 1} {
		if status, acked := post(t, address, secret, "/v1/inbox/ack", map[string]any{"caller": kb, "through": through}); status != 200 || acked["acked_through"] != float64(2) {
			t.Fatalf("monotonic ack: %d %v", status, acked)
		}
	}
	expect("/v1/inbox/read", map[string]any{"caller": kb, "limit": 101}, 400, "invalid_request")
	// A retired recipient, and an alias whose only holder retired, refuse messages.
	if _, err := d.store.Mutate(context.Background(), Mutation{Family: "deepseek", ID: "synthetic-c", IfRevision: c.Revision}, true); err != nil {
		t.Fatal(err)
	}
	expect("/v1/messages/send", map[string]any{"caller": ka, "to": c.Name, "body": "x"}, 409, "recipient_inactive")
	expect("/v1/messages/send", map[string]any{"caller": ka, "to": "deepseek-koinon", "body": "x"}, 409, "alias_unheld")
	expect("/v1/inbox/read", map[string]any{"caller": kc}, 403, "caller_inactive")
	if _, err := d.store.Mutate(context.Background(), Mutation{Family: "codex", ID: "synthetic-a", IfRevision: a.Revision}, true); err != nil {
		t.Fatal(err)
	}
	expect("/v1/peers", map[string]any{"caller": ka}, 403, "caller_inactive")
	expect("/v1/messages/outcome", map[string]any{"caller": ka, "message_id": id}, 403, "caller_inactive")
}

func TestDeliveryStateAndGaplessSequences(t *testing.T) {
	s, _ := testStore(t)
	repo := namedRepo(t, "koinon")
	join(t, s, "codex", "synthetic-a", repo)
	b := join(t, s, "claude", "synthetic-b", repo)
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Send(context.Background(), Key{"codex", "synthetic-a"}, b.Name, fmt.Sprint("message ", i)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	inbox, err := s.ReadInbox(context.Background(), Key{"claude", "synthetic-b"}, 0, 100)
	if err != nil || len(inbox.Messages) != 40 || inbox.LastSeq != 40 || inbox.More {
		t.Fatalf("inbox: %d %+v %v", len(inbox.Messages), inbox.LastSeq, err)
	}
	for i, m := range inbox.Messages {
		if m.Seq != int64(i+1) {
			t.Fatalf("gap at %d: %d", i, m.Seq)
		}
	}
	page, err := s.ReadInbox(context.Background(), Key{"claude", "synthetic-b"}, 10, 5)
	if err != nil || len(page.Messages) != 5 || page.Messages[0].Seq != 11 || !page.More {
		t.Fatalf("page: %+v %v", page, err)
	}
	id := inbox.Messages[0].ID
	for _, c := range []struct {
		state, reason string
		valid         bool
	}{{"notified", "", true}, {"uncertain", "", true}, {"failed", "receiver_unreachable", true}, {"failed", "", false}, {"notified", "reason", false}, {"delivered", "", false}} {
		err := s.SetDelivery(context.Background(), id, c.state, c.reason)
		if (err == nil) != c.valid {
			t.Fatalf("set %s %q: %v", c.state, c.reason, err)
		}
	}
	out, err := s.MessageOutcome(context.Background(), Key{"codex", "synthetic-a"}, id)
	if err != nil || out.DeliveryState != "failed" || out.DeliveryReason != "receiver_unreachable" {
		t.Fatalf("outcome: %+v %v", out, err)
	}
	if err := s.SetDelivery(context.Background(), 9999, "notified", ""); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing message: %v", err)
	}
}

func TestSchemaOneMigration(t *testing.T) {
	root, err := platform.PrivateDir(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	repo := namedRepo(t, "koinon")
	common, err := repository(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	f, err := platform.OpenPrivate(filepath.Join(root, "state.sqlite3"), os.O_RDWR|os.O_CREATE)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	// The schema 1 table as chunk 02 created it, with one active and one retired record.
	far := time.Now().Add(time.Hour).UnixMilli()
	if _, err := db.Exec(`CREATE TABLE sessions (
		family TEXT NOT NULL, id TEXT NOT NULL, repository TEXT NOT NULL,
		directory TEXT NOT NULL, wake_target TEXT NOT NULL,
		registered_at INTEGER NOT NULL, renewed_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL, retired_at INTEGER NOT NULL DEFAULT 0,
		revision INTEGER NOT NULL, PRIMARY KEY (family,id));
		INSERT INTO sessions VALUES ('codex','synthetic-old',?,?,'{}',1,1,?,5,3),('codex','synthetic-new',?,?,'{}',2,2,?,0,1);
		PRAGMA user_version=1`, common, repo, far, common, repo, far); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	items, _, err := s.List(context.Background())
	if err != nil || len(items) != 2 {
		t.Fatalf("migrated: %+v %v", items, err)
	}
	byID := map[string]Session{items[0].ID: items[0], items[1].ID: items[1]}
	if old, now := byID["synthetic-old"], byID["synthetic-new"]; old.Name == "" || old.Alias != "" || old.Revision != 3 || now.Alias != "codex-koinon" {
		t.Fatalf("names after migration: %+v %+v", old, now)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version: %d %v", version, err)
	}
}

// TestCrashHelper is the daemon process that TestMessagesSurviveCrash kills.
func TestCrashHelper(t *testing.T) {
	root := os.Getenv("KOINON_CRASH_STATE")
	if root == "" {
		t.Skip("helper process only")
	}
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("address", d.Addresses()[0])
	select {}
}

func TestMessagesSurviveCrash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "KOINON_CRASH_STATE="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scanner := bufio.NewScanner(stdout)
	var address string
	for scanner.Scan() {
		if line, found := strings.CutPrefix(scanner.Text(), "address "); found {
			address = line
			break
		}
	}
	if address == "" {
		t.Fatal("helper did not start")
	}
	secret, err := ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	var recipient string
	for _, id := range []string{"synthetic-a", "synthetic-b"} {
		status, result := post(t, address, secret, "/v1/sessions/register", httpLaunch(t, address, secret, Registration{Family: "codex", ID: id, Directory: t.TempDir(), TTLSeconds: 600}))
		if status != 200 {
			t.Fatal(result)
		}
		recipient = result["session"].(map[string]any)["name"].(string)
	}
	// Send without pause and kill the daemon at an arbitrary point after some sends.
	var confirmed atomic.Int64
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
		for {
			body, _ := json.Marshal(map[string]any{"caller": Key{"codex", "synthetic-a"}, "to": recipient, "body": "synthetic"})
			r, _ := http.NewRequest("POST", "http://"+address+"/v1/messages/send", bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+secret)
			r.Header.Set("Content-Type", "application/json")
			response, err := client.Do(r)
			if err != nil {
				return
			}
			response.Body.Close()
			if response.StatusCode == 200 {
				confirmed.Add(1)
			}
		}
	}()
	for deadline := time.Now().Add(30 * time.Second); confirmed.Load() < 5; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no sends confirmed")
		}
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	<-stopped
	count := int(confirmed.Load())
	t.Logf("confirmed sends before the crash: %d", count)
	s, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	inbox, err := s.ReadInbox(context.Background(), Key{"codex", "synthetic-b"}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var stored int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	// Every confirmed message is stored; at most the one in flight may also be stored.
	if count == 0 || stored < int64(count) || stored > int64(count)+1 || inbox.LastSeq != stored {
		t.Fatalf("confirmed %d, stored %d, last sequence %d", count, stored, inbox.LastSeq)
	}
	for i := int64(0); i < stored; i += 100 {
		page, err := s.ReadInbox(context.Background(), Key{"codex", "synthetic-b"}, i, 100)
		if err != nil {
			t.Fatal(err)
		}
		for j, m := range page.Messages {
			if m.Seq != i+int64(j)+1 {
				t.Fatalf("gap after crash at %d", m.Seq)
			}
		}
	}
}

func sha256Sum(value string) [32]byte { return sha256.Sum256([]byte(value)) }
