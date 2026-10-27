package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #520: every dialog of the successor's pickup is handled in one prompt wait, so a pickup
// that needs many approvals stays within the cycle's event and credit bounds.

// pickupDialog shows the n-th synthetic approval dialog in the fake pane.
func pickupDialog(f *fakeAdapter, n int) {
	f.pane = PaneState{Prompt: "native_dialog"}
	f.dialog = CycleDialog{Lines: []string{fmt.Sprintf("synthetic approval %d", n)}, Options: []string{"1", "2"},
		Digest: fmt.Sprintf("%064d", n)}
}

// pickupDialogGone shows the pane without a dialog, as after the native session's own answer.
func pickupDialogGone(f *fakeAdapter) {
	f.pane, f.dialog = PaneState{Idle: true}, CycleDialog{}
}

// pickupPromptWait runs the fixture's cycle to the pickup's first dialog.
func pickupPromptWait(t *testing.T, f *cycleFixture) (CycleView, string) {
	t.Helper()
	_, name := cycleToReset(t, f)
	f.step(t)
	pickupDialog(f.adapter, 0)
	f.step(t)
	v := *f.status(t, f.k)
	if v.State != cycleWaitingPrompt || v.PromptFrom != cycleWaitingPickup || v.Reason != "native_dialog" {
		t.Fatalf("first dialog of the pickup: %+v", v)
	}
	return v, name
}

// Twenty dialogs that the native session resolves, well beyond the four that the old return
// to waiting_pickup allowed, add no event and spend no credit; a maintainer's answer spends
// its intent and its return. The wait keeps its first deadline, and the report completes the
// cycle from the revision of a fresh status read.
func TestContextCyclePickupDialogsShareOneWait(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	first, name := pickupPromptWait(t, f)
	credit := f.cycle(t, first.ID).Credit
	for n := 1; n <= 20; n++ {
		pickupDialogGone(f.adapter)
		f.clock = f.clock.Add(5 * time.Second)
		f.step(t)
		pickupDialog(f.adapter, n)
		f.clock = f.clock.Add(5 * time.Second)
		f.step(t)
		v := f.status(t, f.k)
		if v.State != cycleWaitingPrompt || v.Revision != first.Revision || len(v.Events) != len(first.Events) || v.StageDeadline != first.StageDeadline {
			t.Fatalf("dialog %d: %+v", n, v)
		}
	}
	if got := f.cycle(t, first.ID).Credit; got != credit {
		t.Fatalf("credit after native answers: %d, want %d", got, credit)
	}
	f.clock = f.clock.Add(time.Second)
	if err := f.s.AnswerCycleDialog(ctx, first.ID, first.Revision, fmt.Sprintf("%064d", 20), "1"); err != nil {
		t.Fatal(err)
	}
	v := *f.status(t, f.k)
	if v.State != cycleWaitingPrompt || v.PromptFrom != cycleWaitingPickup || v.Reason != "prompt_answered" ||
		len(v.Events) != len(first.Events)+2 || v.StageDeadline != first.StageDeadline || f.adapter.count("choose 1") != 1 {
		t.Fatalf("after the maintainer's answer: %+v", v)
	}
	if got := f.cycle(t, first.ID).Credit; got != credit-2*cycleStagePages {
		t.Fatalf("credit after the answer: %d, want %d", got, credit-2*cycleStagePages)
	}
	// The next dialog after the answer is one more in the same wait.
	pickupDialog(f.adapter, 21)
	f.step(t)
	pickupDialogGone(f.adapter)
	f.step(t)
	read := f.status(t, f.k)
	if read.Revision != v.Revision || len(read.Events) != len(v.Events) {
		t.Fatalf("dialog after the answer: %+v", read)
	}
	done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", read.ID, read.Revision, name, "resumed", "synthetic restored work")
	if err != nil || done.State != cycleComplete || len(done.Events) > maxCycleEvents {
		t.Fatalf("report: %v %+v", err, done)
	}
}

// An answer near the end of the wait, its intent and its retry after the pane showed another
// dialog keep the deadline of the wait's first dialog; the wait then ends at that deadline.
func TestContextCyclePickupPromptDeadlineHolds(t *testing.T) {
	ctx := context.Background()
	f := newCycleFixture(t)
	first, name := pickupPromptWait(t, f)
	f.clock = time.UnixMilli(first.StageDeadline - 1000)
	// Choose finds the pane's next dialog before any key: nothing is entered, and the wait
	// goes on for that dialog.
	f.adapter.beforeChoose = func() {
		f.adapter.beforeChoose = nil
		pickupDialog(f.adapter, 1)
	}
	if err := f.s.AnswerCycleDialog(ctx, first.ID, first.Revision, fmt.Sprintf("%064d", 0), "1"); !errors.Is(err, ErrCycleDialogChanged) {
		t.Fatalf("answer to a changed dialog: %v", err)
	}
	v := *f.status(t, f.k)
	if v.State != cycleWaitingPrompt || v.PromptFrom != cycleWaitingPickup || v.StageDeadline != first.StageDeadline || f.adapter.count("choose") != 0 {
		t.Fatalf("after the retry: %+v", v)
	}
	// The intent of the next answer is capped by the wait, and so is its return. A status
	// read during the intent names a revision that the report may use.
	var intent CycleView
	f.adapter.afterChoose = func() {
		f.adapter.afterChoose = nil
		intent = *f.status(t, f.k)
	}
	if err := f.s.AnswerCycleDialog(ctx, v.ID, v.Revision, fmt.Sprintf("%064d", 1), "2"); err != nil {
		t.Fatal(err)
	}
	if intent.State != cyclePromptIntent || intent.StageDeadline != first.StageDeadline {
		t.Fatalf("intent: %+v", intent)
	}
	if v = *f.status(t, f.k); v.State != cycleWaitingPrompt || v.StageDeadline != first.StageDeadline {
		t.Fatalf("after the answer: %+v", v)
	}
	f.clock = time.UnixMilli(first.StageDeadline)
	if _, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", v.ID, intent.Revision, name, "resumed", "synthetic"); !errors.Is(err, ErrCycleChanged) {
		t.Fatalf("report at the deadline: %v", err)
	}
	f.step(t)
	if v = *f.status(t, f.k); v.State != cycleFailed || !strings.HasPrefix(v.Reason, "waiting_prompt_timeout") {
		t.Fatalf("at the deadline: %+v", v)
	}
	// The same read before the deadline completes the cycle.
	f = newCycleFixture(t)
	first, name = pickupPromptWait(t, f)
	f.adapter.afterChoose = func() {
		f.adapter.afterChoose = nil
		intent = *f.status(t, f.k)
	}
	if err := f.s.AnswerCycleDialog(ctx, first.ID, first.Revision, fmt.Sprintf("%064d", 0), "1"); err != nil {
		t.Fatal(err)
	}
	if done, err := f.s.ContextCyclePickup(ctx, f.k, "synthetic-epoch-2", first.ID, intent.Revision, name, "resumed", "synthetic"); err != nil || done.State != cycleComplete {
		t.Fatalf("report from the intent's revision: %v %+v", err, done)
	}
}

// The dashboard shows a pickup's dialog with its options, nothing to answer between two
// dialogs, and then the next dialog; an answer to the earlier dialog enters nothing.
func TestContextCyclePickupDialogsOnDashboard(t *testing.T) {
	d, root := startTestDaemon(t)
	s := d.store
	adapter := &fakeAdapter{pane: PaneState{Idle: true}, clear: true, epoch: "synthetic-epoch-2", pickup: true, answered: true,
		probe: CycleProbe{Entry: "koinon-pickup"}}
	previous := cycleAdapters
	cycleAdapters = map[string]CycleAdapter{"claude": adapter}
	t.Cleanup(func() { cycleAdapters = previous })
	repo := namedRepo(t, "cycle-pickup-dialog")
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("_handoff/\n"), 0o644)
	a := join(t, s, "claude", "synthetic-pickup-a", repo)
	fixtureExec(s, `UPDATE sessions SET host_pid=4242,host_start=77,tmux_socket='/tmp/synthetic-socket',tmux_pane='%3' WHERE family='claude' AND id=?`, a.ID)
	a, _ = scanSession(s.db.QueryRow(sessionQuery+` WHERE s.family=? AND s.id=?`, "claude", a.ID), s.now().UnixMilli())
	if _, err := s.SetContextEnable(roomCtx("context-enable"), a.Name, a.Revision, 0, true); err != nil {
		t.Fatal(err)
	}
	k := Key{"claude", a.ID}
	if err := s.Observe(context.Background(), Observation{Caller: k, Context: &ObservedValue{Source: "claude_statusline",
		At: s.now().UnixMilli(), LimitTokens: int64p(1000), UsedTokens: int64p(950), Epoch: "synthetic-epoch-1"}}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.ContextCycleStatus(context.Background(), k)
	dir := filepath.Join(repo, "_handoff", "claude")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "2026-10-13-120000.md"), []byte("synthetic"), 0o600)
	if _, err := s.ContextCycleReady(context.Background(), k, "synthetic-epoch-1", v.ID, v.Revision, "2026-10-13-120000.md"); err != nil {
		t.Fatal(err)
	}
	step := func() {
		t.Helper()
		if err := s.cycleStep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		step()
	}
	pickupDialog(adapter, 1)
	step()
	v, _ = s.ContextCycleStatus(context.Background(), k)
	if v.State != cycleWaitingPrompt || v.PromptFrom != cycleWaitingPickup {
		t.Fatalf("waiting: %+v", v)
	}
	client := newActionClient(t, d, root)
	page := func() string {
		return dashboardDo(t, d, "GET", "/dashboard/sessions", client.cookie, nil, nil).body
	}
	if body := page(); !strings.Contains(body, "synthetic approval 1") || !strings.Contains(body, `name="digest" value="`+fmt.Sprintf("%064d", 1)+`"`) {
		t.Fatalf("first dialog: %s", body)
	}
	pickupDialogGone(adapter)
	step()
	if body := page(); strings.Contains(body, "native dialog:") || strings.Contains(body, "context-cycle-answer") {
		t.Fatalf("between dialogs: %s", body)
	}
	pickupDialog(adapter, 2)
	step()
	body := page()
	if !strings.Contains(body, "synthetic approval 2") || !strings.Contains(body, `name="digest" value="`+fmt.Sprintf("%064d", 2)+`"`) ||
		strings.Contains(body, fmt.Sprintf("%064d", 1)) {
		t.Fatalf("next dialog: %s", body)
	}
	answer := func(digest, option string) string {
		return client.do("context-cycle-answer", url.Values{"cycle_id": {v.ID}, "revision": {strconv.FormatInt(v.Revision, 10)}, "digest": {digest}, "option": {option}})
	}
	if got := answer(fmt.Sprintf("%064d", 1), "1"); got != "cycle_dialog_changed" || adapter.count("choose") != 0 {
		t.Fatalf("answer to the earlier dialog: %s", got)
	}
	if got := answer(fmt.Sprintf("%064d", 2), "0"); got == "cycle_dialog_answered" || adapter.count("choose") != 0 {
		t.Fatalf("invalid option: %s", got)
	}
	if got := answer(fmt.Sprintf("%064d", 2), "2"); got != "cycle_dialog_answered" || adapter.count("choose 2") != 1 {
		t.Fatalf("answer: %s", got)
	}
	// Disabling the cycle still ends the wait.
	if _, err := s.SetContextEnableNow(roomCtx("context-enable"), a.Name, false); err != nil {
		t.Fatal(err)
	}
	step()
	if v, _ = s.ContextCycleStatus(context.Background(), k); v.State != cycleCancelled {
		t.Fatalf("after Disable: %+v", v)
	}
}
