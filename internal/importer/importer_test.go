package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/legacy"
	"github.com/rwcii/koinon/internal/platform"
)

type manifest struct {
	Repositories map[string]struct{ Path, Key string } `json:"repositories"`
	Threads      map[string]string                     `json:"threads"`
	Sessions     map[string]struct {
		Name       string `json:"name"`
		Directory  string `json:"directory"`
		Messages   int64  `json:"messages"`
		AckThrough int64  `json:"ack_through"`
	} `json:"sessions"`
	Stores map[string]map[string]any `json:"stores"`
	Base   float64                   `json:"base"`
}

type fixture struct {
	state, prefix, root string
	m                   manifest
}

// copyTree copies the committed fixture, so tests never change testdata.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	copyTree(t, "testdata/python-state", filepath.Join(dir, "python"))
	var m manifest
	data, err := os.ReadFile(filepath.Join(dir, "python", "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	root, err := platform.PrivateDir(filepath.Join(dir, "go"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture{state: filepath.Join(dir, "python", "state"), prefix: filepath.Join(dir, "python", "prefix"), root: root, m: m}
}

func (f fixture) overrides() map[string]string {
	b := f.m.Repositories["b"]
	return map[string]string{b.Key: b.Path}
}

func (f fixture) install(t *testing.T) *legacy.Install {
	t.Helper()
	i, err := legacy.ReadInstall(f.prefix)
	if err != nil || i == nil {
		t.Fatalf("install.json: %v", err)
	}
	return i
}

func (f fixture) run(t *testing.T, attempt string) (Report, error) {
	t.Helper()
	p := Paths{Root: f.root, Attempt: attempt}
	prepared, err := CaptureAll(context.Background(), f.state, f.install(t), f.overrides(), p)
	if err != nil {
		return Report{}, err
	}
	r, err := Stage(context.Background(), prepared, p)
	if err != nil || r.Result != "staged" {
		return r, err
	}
	return r, Swap(p)
}

func openTarget(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, "state.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func scalar(t *testing.T, db *sql.DB, statement string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(statement, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return n
}

func TestImportFixture(t *testing.T) {
	f := newFixture(t)
	r, err := f.run(t, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sources) != 6 || r.Skipped["control"] != 1 || r.Skipped["memory-pointer"] != 1 {
		t.Fatalf("report %+v", r)
	}
	db := openTarget(t, f.root)
	codex := f.m.Threads["codex"]
	var name string
	var last, acked int64
	if err := db.QueryRow(`SELECT n.name,s.last_seq,s.acked_through FROM sessions s JOIN names n ON n.family=s.family AND n.session_id=s.id
		WHERE s.family='codex' AND s.id=?`, codex).Scan(&name, &last, &acked); err != nil {
		t.Fatal(err)
	}
	if name != f.m.Sessions["codex"].Name || last != 5 || acked != 2 {
		t.Fatalf("codex session %s %d %d", name, last, acked)
	}
	// The bodies are the source frames' content, unchanged.
	bodies := map[int64]string{}
	inbox, err := sql.Open("sqlite", "file:"+filepath.Join(f.state, "sessions", f.m.Sessions["codex"].Directory, "inbox.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	frames, err := inbox.Query(`SELECT seq,frame FROM inbox`)
	if err != nil {
		t.Fatal(err)
	}
	for frames.Next() {
		var seq int64
		var text string
		var frame struct{ Message struct{ Content string } }
		frames.Scan(&seq, &text)
		json.Unmarshal([]byte(text), &frame)
		bodies[seq] = frame.Message.Content
	}
	frames.Close()
	inbox.Close()
	if !strings.HasPrefix(bodies[4], "<cross-session-message") {
		t.Fatalf("fixture message 4 has no envelope: %q", bodies[4])
	}
	states := map[int64]string{}
	rows, err := db.Query(`SELECT seq,delivery_state,sender_name,body FROM messages WHERE recipient_id=? ORDER BY seq`, codex)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int64
		var state, sender, body string
		rows.Scan(&seq, &state, &sender, &body)
		states[seq] = state
		if seq == 4 && (sender != "codex-repo-c-91" || body != bodies[4]) {
			t.Fatalf("message 4: sender %q body %q, want the frame content %q", sender, body, bodies[4])
		}
		if seq == 3 && body != bodies[3] {
			t.Fatalf("message 3 body %q, want %q", body, bodies[3])
		}
	}
	rows.Close()
	if len(states) != 2 || states[3] != "notified" || states[4] != "waiting" {
		t.Fatalf("message states %v", states)
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM messages WHERE recipient_id=?`, f.m.Threads["legacy"]); n != 1 {
		t.Fatalf("legacy messages %d", n)
	}
	a := f.m.Repositories["a"]
	if n := scalar(t, db, `SELECT COUNT(*) FROM memory_stores`); n != 3 {
		t.Fatalf("stores %d", n)
	}
	var head, floor, works, claims int64
	if err := db.QueryRow(`SELECT head,floor,work_counter,claim_counter FROM memory_stores WHERE repository=?`, a.Path).Scan(&head, &floor, &works, &claims); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", "file:"+filepath.Join(f.state, "memory", a.Key, "memory.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	for _, c := range []struct{ goQuery, pyQuery string }{
		{`SELECT COUNT(*) FROM memory_entries WHERE repository='` + a.Path + `'`, `SELECT COUNT(*) FROM entries`},
		{`SELECT COUNT(*) FROM memory_cursors WHERE repository='` + a.Path + `'`, `SELECT COUNT(*) FROM cursors`},
		{`SELECT COUNT(*) FROM memory_retired WHERE repository='` + a.Path + `'`, `SELECT COUNT(*) FROM retired`},
		{`SELECT COUNT(*) FROM memory_snapshots WHERE repository='` + a.Path + `' AND acked=0`, `SELECT COUNT(*) FROM snapshots WHERE acked IS NULL`},
		{`SELECT COUNT(*) FROM work_items`, `SELECT COUNT(*) FROM work_items`},
		{`SELECT COUNT(*) FROM claim_bundles`, `SELECT COUNT(*) FROM claim_bundles`},
		{`SELECT COUNT(*) FROM work_events`, `SELECT COUNT(*) FROM work_events`},
		{`SELECT COUNT(*) FROM memory_idem WHERE repository='` + a.Path + `'`, `SELECT COUNT(*) FROM idem WHERE operation='note'`},
		{`SELECT COUNT(*) FROM work_replays`, `SELECT COUNT(*) FROM idem WHERE operation!='note'`},
		{`SELECT ` + itoa(head) + `*1000000+` + itoa(floor) + `*1000+` + itoa(works) + `*10+` + itoa(claims), `SELECT (SELECT value FROM meta WHERE key='head')*1000000+(SELECT value FROM meta WHERE key='floor')*1000+(SELECT value FROM meta WHERE key='work_id_counter')*10+(SELECT value FROM meta WHERE key='claim_generation')`},
	} {
		if g, p := scalar(t, db, c.goQuery), scalar(t, source, c.pyQuery); g != p || p == 0 {
			t.Fatalf("%s = %d, source %d", c.goQuery, g, p)
		}
	}
	// A second import of the same tree changes nothing.
	db.Close()
	r, err = f.run(t, "a2")
	if err != nil || r.Result != "unchanged" {
		t.Fatalf("second import %+v %v", r, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestUnresolvedRepository(t *testing.T) {
	f := newFixture(t)
	_, err := CaptureAll(context.Background(), f.state, f.install(t), nil, Paths{Root: f.root, Attempt: "u"})
	if err == nil || !strings.Contains(err.Error(), "repository_unresolved") || !strings.Contains(err.Error(), f.m.Repositories["b"].Key) {
		t.Fatalf("got %v", err)
	}
}

func gitRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	common, err := filepath.EvalSymlinks(filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, common
}

type caller struct {
	t       *testing.T
	address string
	secret  string
}

func (c caller) call(path string, body any) map[string]any {
	c.t.Helper()
	data, err := core.Call(context.Background(), c.address, c.secret, path, body)
	if err != nil {
		c.t.Fatalf("%s: %v", path, err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		c.t.Fatal(err)
	}
	return result
}

// TestImportedStateServes runs the daemon on an imported tree: the session registers
// again with its inbox and name, a pending snapshot continues and is acknowledged, and
// the first new work item and claim follow the source counters.
func TestImportedStateServes(t *testing.T) {
	f := newFixture(t)
	repo, common := gitRepo(t)
	a := f.m.Repositories["a"]
	overrides := f.overrides()
	overrides[a.Key] = common
	p := Paths{Root: f.root, Attempt: "s"}
	prepared, err := CaptureAll(context.Background(), f.state, f.install(t), overrides, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(context.Background(), prepared, p); err != nil {
		t.Fatal(err)
	}
	if err := Swap(p); err != nil {
		t.Fatal(err)
	}
	var pending string
	source, err := sql.Open("sqlite", "file:"+filepath.Join(f.state, "memory", a.Key, "memory.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.QueryRow(`SELECT id FROM snapshots WHERE acked IS NULL`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	source.Close()

	d, err := core.Start(core.Config{StateDir: f.root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(f.root)
	if err != nil {
		t.Fatal(err)
	}
	c := caller{t: t, address: d.Addresses()[0], secret: secret}
	thread := f.m.Threads["codex"]
	session := c.call("/v1/sessions/register", core.Registration{Family: "codex", ID: thread, Repository: repo, Directory: repo})
	got, _ := session["session"].(map[string]any)
	if got["name"] != f.m.Sessions["codex"].Name || got["state"] != "active" {
		t.Fatalf("registered %v", session)
	}
	me := core.Key{Family: "codex", ID: thread}
	inbox := c.call("/v1/inbox/read", map[string]any{"caller": me, "after": 0, "limit": 10})
	box, _ := inbox["inbox"].(map[string]any)
	messages, _ := box["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("inbox %v", inbox)
	}
	beta := "consumer-beta"
	page := c.call("/v1/memory/sync", map[string]any{"caller": me, "consumer": beta})
	result, _ := page["result"].(map[string]any)
	if result["snapshot_id"] != pending {
		t.Fatalf("pending snapshot not continued: %v", page)
	}
	for result["next_page_token"] != nil {
		page = c.call("/v1/memory/sync", map[string]any{"caller": me, "consumer": beta, "snapshot_id": pending, "page_token": result["next_page_token"]})
		result, _ = page["result"].(map[string]any)
	}
	if ack := c.call("/v1/memory/ack", map[string]any{"caller": me, "consumer": beta, "snapshot_id": pending}); ack["ok"] != true {
		t.Fatalf("ack %v", ack)
	}
	created := c.call("/v1/work/work-create", map[string]any{"caller": me, "title": "after import", "criteria": "c", "non_goals": "n",
		"key": "post-import", "deadline": float64(time.Now().Unix() + 600)})
	item, _ := created["result"].(map[string]any)
	if item["work_id"] != "00000000000000000000000000000006" {
		t.Fatalf("first work ID after the import: %v", created)
	}
	started := c.call("/v1/work/work-start", map[string]any{"caller": me, "work_id": item["work_id"], "if_revision": 1,
		"checkpoint": "s", "next_artifact": "n", "progress_deadline": float64(time.Now().Unix() + 3600),
		"key": "post-start", "deadline": float64(time.Now().Unix() + 600)})
	claim, _ := started["result"].(map[string]any)["claim"].(map[string]any)
	if claim["generation"] != float64(5) {
		t.Fatalf("first claim generation after the import: %v", started)
	}
	live := c.call("/v1/work/work-get", map[string]any{"caller": me, "work_id": f.m.Stores["a"]["live_work"]})
	if view, _ := live["result"].(map[string]any); view["title"] != "#1: live claim" {
		t.Fatalf("imported work item %v", live)
	}
}

func TestCaptureRefusesARunningWriter(t *testing.T) {
	f := newFixture(t)
	p := Paths{Root: f.root, Attempt: "w"}
	codex := filepath.Join(f.state, "sessions", f.m.Sessions["codex"].Directory)
	// A Python process holds its session's notifier lock.
	lock, err := os.OpenFile(filepath.Join(codex, "notifier.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureAll(context.Background(), f.state, f.install(t), f.overrides(), p); err == nil || !strings.Contains(err.Error(), "python_running") {
		t.Fatalf("held lock: %v", err)
	}
	lock.Close()
	// A writer that holds a source database's write lock blocks the capture.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(codex, "inbox.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureAll(context.Background(), f.state, f.install(t), f.overrides(), p); err == nil || !strings.Contains(err.Error(), "python_running") {
		t.Fatalf("held database: %v", err)
	}
	conn.ExecContext(context.Background(), "ROLLBACK")
	conn.Close()
	if _, err := CaptureAll(context.Background(), f.state, f.install(t), f.overrides(), p); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

// TestCaptureIncludesTheLog: rows committed only to the write-ahead log, never
// checkpointed into the database file, are in the capture.
func TestCaptureIncludesTheLog(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.state, "memory", f.m.Repositories["a"].Key, "memory.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var before int64
	db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&before)
	for i := 0; i < 50; i++ {
		if _, err := db.Exec(`INSERT INTO entries(seq,ts,type,scope,body,consumer,revision) VALUES ((SELECT MAX(seq)+1 FROM entries),1.0,'finding','repo','log row','consumer-alpha',1)`); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("rows are not in the log: %v", err)
	}
	captures, err := Capture(context.Background(), []Source{{Kind: "memory", Path: path}}, filepath.Join(f.root, "log"))
	if err != nil {
		t.Fatal(err)
	}
	capture, err := sql.Open("sqlite", "file:"+captures[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	if n := scalar(t, capture, `SELECT COUNT(*) FROM entries`); n != before+50 {
		t.Fatalf("capture has %d entries, want %d", n, before+50)
	}
}

func TestStageResumesAndRefuses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := Paths{Root: f.root, Attempt: "r"}
	prepared, err := CaptureAll(ctx, f.state, f.install(t), f.overrides(), p)
	if err != nil {
		t.Fatal(err)
	}
	// An attempt interrupted after three sources resumes at the fourth.
	if _, err := Stage(ctx, prepared[:3], p); err != nil {
		t.Fatal(err)
	}
	r, err := Stage(ctx, prepared, p)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range r.Sources {
		if want := map[bool]string{true: "resumed", false: "imported"}[i < 3]; line.Status != want {
			t.Fatalf("source %d status %s", i, line.Status)
		}
	}
	// A failure inside a source rolls that source back and leaves the others.
	broken := append([]Prepared{}, prepared...)
	src := broken[0].Import
	src.Path += ".copy"
	src.Tables = append([]core.ImportTable{}, src.Tables...)
	if _, err := Stage(ctx, append(broken, Prepared{Source: broken[0].Source, Import: src}), p); err == nil || !strings.Contains(err.Error(), "source_conflict") {
		t.Fatalf("conflicting source: %v", err)
	}
	// A source that changed after it was staged is refused.
	changed := append([]Prepared{}, prepared...)
	changed[0].Import.Digest = strings.Repeat("0", 64)
	if _, err := Stage(ctx, changed, p); err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("changed source: %v", err)
	}
	if err := Swap(p); err != nil {
		t.Fatal(err)
	}
	// Another tree is refused over the non-empty target.
	other := newFixture(t)
	q := Paths{Root: f.root, Attempt: "o"}
	more, err := CaptureAll(ctx, other.state, other.install(t), other.overrides(), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Stage(ctx, more, q); err == nil || !strings.Contains(err.Error(), "target_not_empty") {
		t.Fatalf("non-empty target: %v", err)
	}
	if err := q.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(q.Staging()); !os.IsNotExist(err) {
		t.Fatalf("discarded staging remains: %v", err)
	}
	// Verification compares the target with the sources.
	if r, err := Verify(ctx, prepared, p); err != nil || r.Result != "verified" {
		t.Fatalf("verify %+v %v", r, err)
	}
}
