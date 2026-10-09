package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// hosts is a test double for ProcessStart: the start time of each running synthetic host
// process; a missing process is gone, and failing cannot be read.
type hosts struct {
	mu      sync.Mutex
	start   map[int]int64
	failing map[int]bool
}

func (h *hosts) read(pid int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failing[pid] {
		return 0, errors.New("synthetic inspection error")
	}
	if start, ok := h.start[pid]; ok {
		return start, nil
	}
	return 0, platform.ErrProcessGone
}

func (h *hosts) set(pid int, start int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if start == 0 {
		delete(h.start, pid)
		return
	}
	h.start[pid] = start
}

func (h *hosts) fail(pid int, failing bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.failing[pid] = failing
}

// successionStore is a store with a controlled clock and synthetic host processes.
func successionStore(t *testing.T) (*Store, *hosts, func(time.Duration)) {
	t.Helper()
	s, _ := testStore(t)
	h := &hosts{start: map[int]int64{}, failing: map[int]bool{}}
	s.processStart = h.read
	var mu sync.Mutex
	clock := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	return s, h, func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
}

// arrival is one registration of a launched session in repo: its family, ID, host process
// and pane, whether its own tool call caused it, and whether it is a sub-agent.
type arrival struct {
	family, id, repo string
	pid              int
	pane             string
	toolCall         bool
	subagent         bool
}

func arrive(t *testing.T, s *Store, a arrival) Session {
	t.Helper()
	launch, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: a.family, Directory: a.repo, CLI: "/synthetic/cli", HostPID: a.pid})
	if err != nil {
		t.Fatal(err)
	}
	r := Registration{Family: a.family, ID: a.id, Repository: a.repo, Directory: a.repo, TTLSeconds: 900, LaunchID: launch,
		Ancestors: []int{a.pid}, ToolCall: a.toolCall, Subagent: a.subagent}
	if a.family == "claude" {
		r.WakeTarget = json.RawMessage(`{"claude_pid":` + itoa(a.pid) + `}`)
	}
	if a.pane != "" {
		r.Tmux = &TmuxPane{Socket: "/synthetic/tmux/default", Pane: a.pane}
	}
	got, err := s.Register(context.Background(), r)
	if err != nil {
		t.Fatalf("register %s: %v", a.id, err)
	}
	return got
}

func itoa(n int) string { data, _ := json.Marshal(n); return string(data) }

func sessionOf(t *testing.T, s *Store, family, id string) Session {
	t.Helper()
	got, err := scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, family, id), s.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func wantSuccession(t *testing.T, got Session, result, reason, holder string) {
	t.Helper()
	if got.Succession == nil || got.Succession.Result != result || got.Succession.Reason != reason || got.Succession.Holder != holder {
		t.Fatalf("%s succession: got %+v, want %s %s from %s", got.Name, got.Succession, result, reason, holder)
	}
}

func lastEvent(t *testing.T, s *Store, address string) (reason string, details map[string]any) {
	t.Helper()
	var text string
	if err := s.db.QueryRow(`SELECT reason,details FROM participant_events WHERE address=? ORDER BY id DESC LIMIT 1`, address).
		Scan(&reason, &text); err != nil {
		t.Fatal(err)
	}
	details = map[string]any{}
	if text != "" {
		if err := json.Unmarshal([]byte(text), &details); err != nil {
			t.Fatal(err)
		}
	}
	return reason, details
}

// Accept: the same host process with a new native session ID, once the holder's last tool
// call is older than the guard. The former holder is retired and fenced in the same change.
func TestSuccessionSameHost(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	if !held.HoldsAddress {
		t.Fatalf("first session holds nothing: %+v", held)
	}
	advance(31 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, pane: "%1", toolCall: true})
	if !next.HoldsAddress {
		t.Fatalf("successor does not hold: %+v", next)
	}
	wantSuccession(t, next, "succeeded", "same_host", held.Name)
	former := sessionOf(t, s, "codex", "thread-1")
	if former.State != "retired" || !former.Fenced {
		t.Fatalf("former holder not retired and fenced: %+v", former)
	}
	reason, details := lastEvent(t, s, held.Address)
	if reason != "same_host" || details["candidate"] != next.Name || details["host_pid"] != float64(100) || details["pane"] != "%1" {
		t.Fatalf("event: %s %v", reason, details)
	}
	// Peers report the last succession result.
	peers, _, err := s.Peers(context.Background(), Key{"codex", "thread-2"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range peers {
		if p.Name == next.Name && (p.Succession == nil || p.Succession.Reason != "same_host") {
			t.Fatalf("peer view: %+v", p)
		}
	}
}

// Refuse with holder_active while the holder made a tool call in the last 30 seconds; a
// later call of the holder refuses the retry again; the successor's first registration
// after the guard takes the participant. Renewal never extends the guard.
func TestSuccessionHolderActive(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, toolCall: true})
	advance(10 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, toolCall: true})
	wantSuccession(t, next, "refused", "holder_active", held.Name)
	if next.HoldsAddress || next.Succession.RetryAfterMS != 20_000 {
		t.Fatalf("refusal: %+v %+v", next, next.Succession)
	}
	if reason, details := lastEvent(t, s, held.Address); reason != "succession_refused" || details["refused"] != "holder_active" {
		t.Fatalf("event: %s %v", reason, details)
	}
	// The holder reads its inbox: a read-only call extends the guard.
	advance(15 * time.Second)
	s.ToolCall(Key{"codex", "thread-1"})
	advance(6 * time.Second)
	next = arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, toolCall: true})
	wantSuccession(t, next, "refused", "holder_active", held.Name)
	if next.Succession.RetryAfterMS != 24_000 {
		t.Fatalf("retry after: %d", next.Succession.RetryAfterMS)
	}
	// The holder keeps the participant and acts for it.
	if got := sessionOf(t, s, "codex", "thread-1"); !got.HoldsAddress {
		t.Fatalf("holder lost the participant: %+v", got)
	}
	// A renewal of the holder is no tool call.
	advance(25 * time.Second)
	renew(t, s, sessionOf(t, s, "codex", "thread-1"))
	next = arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, toolCall: true})
	wantSuccession(t, next, "succeeded", "same_host", held.Name)
}

// The renewal timer never registers, and a registration that no tool call caused never
// takes the participant.
func TestSuccessionNeedsToolCall(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, toolCall: true})
	advance(time.Minute)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100})
	if next.HoldsAddress || next.Succession != nil {
		t.Fatalf("succession without a tool call: %+v", next)
	}
	renew(t, s, next)
	if got := sessionOf(t, s, "codex", "thread-1"); !got.HoldsAddress || got.Name != held.Name {
		t.Fatalf("holder changed: %+v", got)
	}
}

// After a daemon start, an earlier tool call of the holder is unknown: the guard counts the
// start as a call.
func TestSuccessionGuardAfterStart(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100})
	s.calls.since = s.now().UnixMilli()
	advance(5 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, toolCall: true})
	wantSuccession(t, next, "refused", "holder_active", held.Name)
	if next.Succession.RetryAfterMS != 25_000 {
		t.Fatalf("retry after: %d", next.Succession.RetryAfterMS)
	}
}

// Accept: the same tmux server and pane with the former host process ended, also when its
// process ID now belongs to another process. Refuse while it runs or cannot be read.
func TestSuccessionSamePane(t *testing.T) {
	s, h, _ := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "old", repo: repo, pid: 100, pane: "%3", toolCall: true})
	next := arrive(t, s, arrival{family: "codex", id: "new", repo: repo, pid: 200, pane: "%3", toolCall: true})
	wantSuccession(t, next, "refused", "host_running", held.Name)
	h.fail(100, true)
	next = arrive(t, s, arrival{family: "codex", id: "new", repo: repo, pid: 200, pane: "%3", toolCall: true})
	wantSuccession(t, next, "refused", "host_running", held.Name)
	h.fail(100, false)
	// The same process ID with another start time is another process.
	h.set(100, 8)
	next = arrive(t, s, arrival{family: "codex", id: "new", repo: repo, pid: 200, pane: "%3", toolCall: true})
	wantSuccession(t, next, "succeeded", "same_pane", held.Name)
	if reason, details := lastEvent(t, s, held.Address); reason != "same_pane" || details["pane"] != "%3" {
		t.Fatalf("event: %s %v", reason, details)
	}

	// A host process that has ended with no process in its place.
	repo2 := namedRepo(t, "other")
	h.set(300, 5)
	h.set(400, 10)
	held = arrive(t, s, arrival{family: "codex", id: "old-2", repo: repo2, pid: 300, pane: "%4", toolCall: true})
	h.set(300, 0)
	next = arrive(t, s, arrival{family: "codex", id: "new-2", repo: repo2, pid: 400, pane: "%4", toolCall: true})
	wantSuccession(t, next, "succeeded", "same_pane", held.Name)
}

// Refuse: another pane, no pane recorded (a launch outside tmux), the same pane while the
// holder's host runs, a sub-agent thread of the same host.
func TestSuccessionRefusals(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "held", repo: repo, pid: 100, pane: "%1", toolCall: true})
	advance(time.Minute)
	for _, c := range []struct {
		a      arrival
		reason string
	}{
		{arrival{id: "other-pane", pid: 200, pane: "%2"}, "other_pane"},
		{arrival{id: "no-pane", pid: 200}, "other_pane"},
		{arrival{id: "pane-running", pid: 200, pane: "%1"}, "host_running"},
		{arrival{id: "sub", pid: 100, pane: "%1", subagent: true}, "subagent"},
	} {
		c.a.family, c.a.repo, c.a.toolCall = "codex", repo, true
		got := arrive(t, s, c.a)
		wantSuccession(t, got, "refused", c.reason, held.Name)
		if got.HoldsAddress {
			t.Fatalf("%s took the participant", c.a.id)
		}
	}
	if got := sessionOf(t, s, "codex", "held"); !got.HoldsAddress {
		t.Fatalf("holder lost the participant: %+v", got)
	}
}

// Refuse with no_host when the holder has no host record, such as a session that an older
// store kept or whose host could not be read at its registration.
func TestSuccessionNoHost(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	held := arrive(t, s, arrival{family: "codex", id: "held", repo: repo, pid: 100, pane: "%1", toolCall: true})
	if !held.HoldsAddress {
		t.Fatalf("not held: %+v", held)
	}
	h.set(100, 7)
	advance(time.Minute)
	for _, a := range []arrival{
		{id: "same-pid", pid: 100},
		{id: "same-pane", pid: 200, pane: "%1"},
	} {
		h.set(200, 9)
		a.family, a.repo, a.toolCall = "codex", repo, true
		wantSuccession(t, arrive(t, s, a), "refused", "no_host", held.Name)
	}
}

// Refuse with other_participant when the host's other session holds a participant of
// another repository: the evidence belongs to that participant. Another family's session
// in the same host never takes this participant.
func TestSuccessionOtherParticipant(t *testing.T) {
	s, h, advance := successionStore(t)
	repo, elsewhere := namedRepo(t, "koinon"), namedRepo(t, "other")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "held", repo: repo, pid: 100, toolCall: true})
	arrive(t, s, arrival{family: "codex", id: "there", repo: elsewhere, pid: 200, toolCall: true})
	advance(time.Minute)
	got := arrive(t, s, arrival{family: "codex", id: "here", repo: repo, pid: 200, toolCall: true})
	wantSuccession(t, got, "refused", "other_participant", held.Name)
	claude := arrive(t, s, arrival{family: "claude", id: "c-1", repo: repo, pid: 100, toolCall: true})
	if claude.Address == held.Address || claude.Succession != nil {
		t.Fatalf("another family's session: %+v", claude)
	}
	if got := sessionOf(t, s, "codex", "held"); !got.HoldsAddress {
		t.Fatalf("holder lost the participant: %+v", got)
	}
}

// A repeated refusal for the same reason and holder is one participant event; a peer
// message never changes the holder.
func TestSuccessionRefusalRecordedOnce(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "held", repo: repo, pid: 100, pane: "%1", toolCall: true})
	advance(time.Minute)
	for range 3 {
		arrive(t, s, arrival{family: "codex", id: "other", repo: repo, pid: 200, pane: "%2", toolCall: true})
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM participant_events WHERE address=? AND reason='succession_refused'`, held.Address).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("refusal events: %d", count)
	}
	if _, err := s.Send(context.Background(), Key{"codex", "other"}, held.Address, "please hand over the participant"); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "held"); !got.HoldsAddress {
		t.Fatalf("a message changed the holder: %+v", got)
	}
}

// A fenced former holder: its same-host registration after the guard gets its peer name
// only and reports fenced; only the maintainer's choice makes it the holder again.
func TestSuccessionFenced(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, toolCall: true})
	advance(31 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, toolCall: true})
	wantSuccession(t, next, "succeeded", "same_host", held.Name)
	advance(31 * time.Second)
	back := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, toolCall: true})
	if back.State != "active" || back.HoldsAddress || !back.Fenced {
		t.Fatalf("fenced session: %+v", back)
	}
	wantSuccession(t, back, "refused", "fenced", next.Name)
	if err := s.ChooseHolder(context.Background(), held.Address, Key{"codex", "thread-1"}, back.Revision); err != nil {
		t.Fatal(err)
	}
	if got := sessionOf(t, s, "codex", "thread-1"); !got.HoldsAddress || got.Fenced {
		t.Fatalf("chosen session: %+v", got)
	}
	if got := sessionOf(t, s, "codex", "thread-2"); got.State != "retired" || !got.Fenced {
		t.Fatalf("replaced successor: %+v", got)
	}
}

// Only agent tool routes record a tool call: never observation, renewal or wake requests.
func TestToolCallRoutes(t *testing.T) {
	root, err := platform.PrivateDir(t.TempDir() + "/state")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Start(Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	address, secret := d.Addresses()[0], d.secret
	recorded := func(id string) bool {
		d.store.calls.mu.Lock()
		defer d.store.calls.mu.Unlock()
		_, ok := d.store.calls.at[Key{"codex", id}]
		return ok
	}
	caller := func(id string) map[string]any { return map[string]any{"caller": Key{"codex", id}} }
	// post sends one request; the reply does not matter, only what the daemon recorded.
	post := func(t *testing.T, address, secret, path string, body any) {
		data, _ := json.Marshal(body)
		request(t, d, path, string(data), secret).Body.Close()
	}
	for path, body := range map[string]map[string]any{
		"/v1/peers": caller("peers"), "/v1/peers/status": caller("status"), "/v1/inbox/read": caller("read"),
		"/v1/inbox/ack": caller("ack"), "/v1/messages/send": caller("send"), "/v1/messages/outcome": caller("outcome"),
		"/v1/memory/status": caller("memory"), "/v1/work/work-list": caller("work"),
		"/v1/work/checkout-status": {"caller": Key{"codex", "checkout"}, "directory": "/synthetic"},
	} {
		post(t, address, secret, path, body)
		if id := body["caller"].(Key).ID; !recorded(id) {
			t.Errorf("%s recorded no tool call", path)
		}
	}
	post(t, address, secret, "/v1/sessions/observe", map[string]any{"caller": Key{"codex", "observe"}})
	post(t, address, secret, "/v1/sessions/renew", Mutation{Family: "codex", ID: "renew", IfRevision: 1})
	post(t, address, secret, "/v1/wake/agy-stop", map[string]any{"caller": Key{"agy", "stop"}})
	for _, id := range []string{"observe", "renew"} {
		if recorded(id) {
			t.Errorf("%s recorded a tool call", id)
		}
	}
	d.store.calls.mu.Lock()
	_, stop := d.store.calls.at[Key{"agy", "stop"}]
	d.store.calls.mu.Unlock()
	if stop {
		t.Fatal("wake request recorded a tool call")
	}
}

func TestSuccessionUnknownCandidateHost(t *testing.T) {
	s, h, _ := successionStore(t)
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "old", repo: repo, pid: 100, pane: "%3", toolCall: true})
	h.set(100, 0)
	h.fail(200, true)
	got := arrive(t, s, arrival{family: "codex", id: "new", repo: repo, pid: 200, pane: "%3", toolCall: true})
	wantSuccession(t, got, "refused", "no_host", held.Name)
}
func TestSuccessionHostAlreadyHoldsElsewhere(t *testing.T) {
	s, h, advance := successionStore(t)
	repo := namedRepo(t, "koinon")
	other := namedRepo(t, "elsewhere")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "old", repo: repo, pid: 100, pane: "%3", toolCall: true})
	arrive(t, s, arrival{family: "codex", id: "other", repo: other, pid: 200, pane: "%3", toolCall: true})
	h.set(100, 0)
	advance(time.Minute)
	got := arrive(t, s, arrival{family: "codex", id: "new", repo: repo, pid: 200, pane: "%3", toolCall: true})
	wantSuccession(t, got, "refused", "other_participant", held.Name)
}

// A Codex /clear (#228): a notice that the former holder's thread accepted never reaches
// the successor, so a holder change returns the participant's unacknowledged notified
// messages to waiting for a wake of the new holder. Registration and renewal replies
// report host_holder to every other session of the holding host.
func TestHolderChangeRearmsWakeAndReportsHostHolder(t *testing.T) {
	s, h, advance := successionStore(t)
	ctx := context.Background()
	repo := namedRepo(t, "koinon")
	h.set(100, 7)
	h.set(200, 9)
	held := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	if held.HostHolder {
		t.Fatalf("the holder reports a holding host-mate: %+v", held)
	}
	arrive(t, s, arrival{family: "claude", id: "synthetic-sender", repo: namedRepo(t, "other"), pid: 200, pane: "%2", toolCall: true})
	for _, body := range []string{"handled", "pending"} {
		if _, err := s.Send(ctx, Key{"claude", "synthetic-sender"}, held.Address, body); err != nil {
			t.Fatal(err)
		}
	}
	var woken []string
	s.wake.send = func(_ context.Context, target Session, _ string) wakeResult {
		woken = append(woken, target.ID)
		return accepted()
	}
	participant := Key{"codex", participantPrefix + held.Address}
	wake := func() {
		t.Helper()
		s.wake.mu.Lock()
		defer s.wake.mu.Unlock()
		if _, err := s.submitWake(ctx, participant, false); err != nil {
			t.Fatal(err)
		}
	}
	state := func(body string) (delivery string, attempts int64) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT delivery_state,wake_attempts FROM messages WHERE body=?`, body).Scan(&delivery, &attempts); err != nil {
			t.Fatal(err)
		}
		return delivery, attempts
	}
	wake()
	if _, _, err := s.AckParticipant(ctx, Key{"codex", "thread-1"}, 1); err != nil {
		t.Fatal(err)
	}
	advance(31 * time.Second)
	next := arrive(t, s, arrival{family: "codex", id: "thread-2", repo: repo, pid: 100, pane: "%1", toolCall: true})
	wantSuccession(t, next, "succeeded", "same_host", held.Name)
	if got, _ := state("handled"); got != "notified" {
		t.Fatalf("acknowledged message: %s", got)
	}
	if got, attempts := state("pending"); got != "waiting" || attempts != 0 {
		t.Fatalf("unacknowledged message after the holder change: %s, %d attempts", got, attempts)
	}
	wake()
	if len(woken) != 2 || woken[0] != "thread-1" || woken[1] != "thread-2" {
		t.Fatalf("wakes: %v", woken)
	}
	if next.HostHolder {
		t.Fatalf("the successor reports a holding host-mate: %+v", next)
	}
	advance(31 * time.Second)
	back := arrive(t, s, arrival{family: "codex", id: "thread-1", repo: repo, pid: 100, pane: "%1", toolCall: true})
	sub := arrive(t, s, arrival{family: "codex", id: "thread-3", repo: repo, pid: 100, pane: "%1", toolCall: true, subagent: true})
	other := arrive(t, s, arrival{family: "codex", id: "thread-4", repo: repo, pid: 300, pane: "%3", toolCall: true})
	if !back.HostHolder || !sub.HostHolder || other.HostHolder {
		t.Fatalf("host_holder: former %v, sub-agent %v, other host %v", back.HostHolder, sub.HostHolder, other.HostHolder)
	}
	renewed, err := s.Mutate(ctx, Mutation{Family: "codex", ID: "thread-1", IfRevision: back.Revision, TTLSeconds: 900}, false)
	if err != nil || !renewed.HostHolder {
		t.Fatalf("renewal of the former holder: %+v %v", renewed, err)
	}
}
