package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Current readings (DECISIONS.md#PULLED-IDLE-569): in handoff_cycle, a reading older than the
// source age counts when its newest confirmation shows that no newer context exists: a Codex
// caught-up mark, or the proof of the daemon's OpenCode or DeepSeek reader.

func handoffReason(e ContextEvidence, threshold int64, now int64) string {
	policy := ContextFamilyPolicy{Mode: ContextHandoffCycle, Threshold: threshold}
	_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true, Evidence: e, Now: now})
	return reason
}

// The trigger accepts an old reading only with a current, fresh confirmation of its epoch at the
// threshold; native_compaction keeps the Codex mark only.
func TestCurrentReadingTrigger(t *testing.T) {
	now := time.Now().UnixMilli()
	old := now - 30*time.Minute.Milliseconds()
	evidence := func(change func(*ContextEvidence)) ContextEvidence {
		e := ContextEvidence{Found: true, UsedTokens: int64p(800), LimitTokens: int64p(1000), Epoch: "e1", SourceAt: old, ConfirmedAt: now - 1000}
		change(&e)
		return e
	}
	for name, c := range map[string]struct {
		mode      string
		e         ContextEvidence
		latched   string
		want      string
		threshold int64
	}{
		"proof":      {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.Current = true }), "", "", 75},
		"codex mark": {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.End = rolloutEnd(10) }), "", "", 75},
		"no proof":   {ContextHandoffCycle, evidence(func(*ContextEvidence) {}), "", "source_stale", 75},
		"old confirmation": {ContextHandoffCycle, evidence(func(e *ContextEvidence) {
			e.Current, e.ConfirmedAt = true, now-contextConfirmationMaxAge.Milliseconds()-1
		}), "", "confirmation_stale", 75},
		"below threshold":    {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.Current = true }), "", "below_threshold", 90},
		"latched epoch":      {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.Current = true }), "e1", "already_selected_for_epoch", 75},
		"compaction proof":   {ContextNativeCompaction, evidence(func(e *ContextEvidence) { e.Current = true }), "", "source_stale", 75},
		"compaction mark":    {ContextNativeCompaction, evidence(func(e *ContextEvidence) { e.End = rolloutEnd(10) }), "", "", 75},
		"observe":            {ContextObserve, evidence(func(e *ContextEvidence) { e.Current = true }), "", "mode_observe", 75},
		"no epoch":           {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.Current, e.Epoch = true, "" }), "", "epoch_unverified", 75},
		"future source":      {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.Current, e.SourceAt = true, now+1 }), "", "observation_future", 75},
		"fresh, no proof":    {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.SourceAt = now - 1000 }), "", "", 75},
		"stale, both clocks": {ContextHandoffCycle, evidence(func(e *ContextEvidence) { e.ConfirmedAt = old }), "", "source_stale", 75},
	} {
		policy := ContextFamilyPolicy{Mode: c.mode, Threshold: c.threshold}
		_, reason := SelectContextTrigger(ContextTriggerInput{Active: true, Enabled: true, Policy: policy, CycleAvailable: true,
			Evidence: c.e, Now: now, LatchedEpoch: c.latched})
		if reason != c.want {
			t.Errorf("%s: reason %q, want %q", name, reason, c.want)
		}
	}
}

// The store keeps the proof of the newest confirmation only: a confirmation without it ends it,
// clearContextProof ends it without confirming, a new reading or epoch starts without it, no
// report can carry it, no view shows it, and a restart forgets it.
func TestContextProofInTheStore(t *testing.T) {
	ctx := context.Background()
	s, root := testStore(t)
	clock := time.Now().Truncate(time.Millisecond)
	s.now = func() time.Time { return clock }
	session, err := s.Register(ctx, Registration{Family: "opencode", ID: "ses_synthetic", Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	k := Key{"opencode", session.ID}
	old := clock.Add(-30 * time.Minute).UnixMilli()
	report := func(at int64, epoch string, current bool) {
		t.Helper()
		clock = clock.Add(time.Second)
		v := ObservedValue{Source: "opencode_server", At: at, LimitTokens: int64p(1000), UsedTokens: int64p(800), Epoch: epoch, current: current}
		if err := s.Observe(ctx, Observation{Caller: k, Context: &v}); err != nil {
			t.Fatal(err)
		}
	}
	report(old, "ses_synthetic", true)
	if e := s.contextEvidence(k); !e.Current || e.ConfirmedAt != clock.UnixMilli() || handoffReason(e, 75, clock.UnixMilli()) != "" {
		t.Fatalf("proof: %+v", e)
	}
	report(old, "ses_synthetic", false)
	if e := s.contextEvidence(k); e.Current || e.ConfirmedAt != clock.UnixMilli() || handoffReason(e, 75, clock.UnixMilli()) != "source_stale" {
		t.Fatalf("a confirmation without the proof: %+v", e)
	}
	report(old, "ses_synthetic", true)
	confirmed := clock.UnixMilli()
	clock = clock.Add(time.Second)
	s.clearContextProof(k)
	if e := s.contextEvidence(k); e.Current || e.ConfirmedAt != confirmed {
		t.Fatalf("cleared: %+v", e)
	}
	report(old, "ses_synthetic", true)
	report(old+1, "ses_other", false)
	if e := s.contextEvidence(k); e.Current || e.Epoch != "ses_other" {
		t.Fatalf("new epoch: %+v", e)
	}
	// A different report at the same source time changes nothing, unless a daemon reader read
	// it as its source's newest: then it replaces the reading, with its own proof.
	tied := func(used int64, newest, current bool) {
		t.Helper()
		clock = clock.Add(time.Second)
		v := ObservedValue{Source: "opencode_server", At: old + 1, LimitTokens: int64p(1000), UsedTokens: int64p(used), Epoch: "ses_other",
			current: current, newest: newest}
		if err := s.Observe(ctx, Observation{Caller: k, Context: &v}); err != nil {
			t.Fatal(err)
		}
	}
	tied(800, true, true)
	tied(100, false, false)
	if e := s.contextEvidence(k); !e.Current || *e.UsedTokens != 800 {
		t.Fatalf("a tied report: %+v", e)
	}
	tied(100, true, false)
	if e := s.contextEvidence(k); e.Current || *e.UsedTokens != 100 {
		t.Fatalf("a tied newest report: %+v", e)
	}
	// An old, different report changes nothing, its proof included.
	report(old-1, "ses_synthetic", true)
	if e := s.contextEvidence(k); e.Current || e.Epoch != "ses_other" {
		t.Fatalf("older report: %+v", e)
	}
	// A report decodes no proof.
	var o Observation
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"caller":{"family":"opencode","id":"ses_synthetic"},"context":{"source":"opencode_server","at":%d,"limit_tokens":1000,"used_tokens":900,"epoch":"ses_synthetic","current":true}}`,
		old+2)), &o); err != nil || o.Context == nil || o.Context.current {
		t.Fatalf("decoded: %+v %v", o.Context, err)
	}
	report(old+3, "ses_synthetic", true)
	data, _ := json.Marshal(s.sessionObservations(ctx, s.reloadSession(t, k)))
	if strings.Contains(string(data), "current") || strings.Contains(string(data), "ses_synthetic\"") {
		t.Fatalf("view: %s", data)
	}
	s.db.Close()
	restarted, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.db.Close() })
	if e := restarted.contextEvidence(k); e.Found || e.Current {
		t.Fatalf("after a restart: %+v", e)
	}
}

// In handoff_cycle the Codex caught-up mark admits an old reading. Its clearing report is best
// effort: when it is lost, the last mark admits until its confirmation is 90 seconds old.
func TestCodexMarkInHandoffCycle(t *testing.T) {
	f := newCompactFixture(t)
	old := f.clock.Add(-30 * time.Minute)
	reason := func() string { return handoffReason(f.s.contextEvidence(f.k), 75, f.clock.UnixMilli()) }
	f.caught(t, 800, f.k.ID, old, rolloutEnd(5000))
	if r := reason(); r != "" {
		t.Fatalf("caught up: %q", r)
	}
	// A delivered clearing report ends it at once.
	f.caught(t, 800, f.k.ID, old, nil)
	if r := reason(); r != "source_stale" {
		t.Fatalf("cleared: %q", r)
	}
	// Below the threshold, a marked reading admits nothing.
	f.caught(t, 700, f.k.ID, old.Add(time.Second), rolloutEnd(5100))
	if r := reason(); r != "below_threshold" {
		t.Fatalf("below the threshold: %q", r)
	}
	// The read failed and its clearing report was lost: the last mark admits until its
	// confirmation is 90 seconds old.
	f.caught(t, 800, f.k.ID, old.Add(2*time.Second), rolloutEnd(5200))
	f.clock = f.clock.Add(contextConfirmationMaxAge)
	if r := reason(); r != "" {
		t.Fatalf("lost clearing report at 90 s: %q", r)
	}
	f.clock = f.clock.Add(time.Millisecond)
	if r := reason(); r != "confirmation_stale" {
		t.Fatalf("lost clearing report after 90 s: %q", r)
	}
}

// openCodeIdlePage is a finished reply after its prompt, the session's newest message.
const openCodeIdlePage = `[{"info":{"id":"msg_1","role":"user","time":{"created":1793073204875}}},` +
	`{"info":{"id":"msg_2","role":"assistant","providerID":"opencode","modelID":"big-pickle","finish":"stop","time":{"created":1793073204905,"completed":1793073206710},` +
	openCodeTokens + `},"parts":[{"type":"text","text":"SYNTHETIC-PART-SENTINEL"}]}]`

// The OpenCode reader proves a reading current when its finished reply is the newest message and
// the exact session reads idle in the same round, with the same reading after the status read.
func TestOpenCodeCurrentReading(t *testing.T) {
	ctx := context.Background()
	s, f, clock, register := openCodeRound(t)
	session := register("ses_synthetic", true)
	k := Key{"opencode", session.ID}
	reason := func() string { return handoffReason(s.contextEvidence(k), 10, clock.UnixMilli()) }
	f.set("ses_synthetic", openCodeIdlePage)
	// An idle session is omitted from the status, and exists.
	f.sessionStatus = `{}`
	s.openCodeContextRound(ctx)
	if e := s.contextEvidence(k); !e.Current || reason() != "" {
		t.Fatalf("idle, omitted: %+v %q", e, reason())
	}
	// Unchanged, it is confirmed each minute with its proof: the confirmation stays fresh.
	for range 4 {
		*clock = clock.Add(30 * time.Second)
		s.openCodeContextRound(ctx)
		if r := reason(); r != "" {
			t.Fatalf("idle after %v: %q", clock.Sub(time.Now()), r)
		}
	}
	// A busy status ends the proof at once, within the minute.
	f.sessionStatus = `{"ses_synthetic":{"type":"busy"}}`
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "source_stale" {
		t.Fatalf("busy: %q", r)
	}
	f.sessionStatus = `{"ses_synthetic":{"type":"idle"}}`
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "" {
		t.Fatalf("idle, listed: %q", r)
	}
	// A failed read ends the proof at once and confirms nothing.
	confirmed := s.contextEvidence(k).ConfirmedAt
	f.status = http.StatusInternalServerError
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if e := s.contextEvidence(k); e.Current || e.ConfirmedAt != confirmed {
		t.Fatalf("failed read: %+v", e)
	}
	f.status = 0
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "" {
		t.Fatalf("recovered: %q", r)
	}
	// A failed status read, and a session that the server does not have, prove nothing.
	f.sessionStatus = ""
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "source_stale" {
		t.Fatalf("status unreadable: %q", r)
	}
	// A newer prompt, or an unfinished reply after the reading, is newer context.
	f.sessionStatus = `{}`
	for name, page := range map[string]string{
		"prompt":  strings.TrimSuffix(openCodeIdlePage, "]") + `,{"info":{"id":"msg_3","role":"user","time":{"created":1793073210000}}}]`,
		"reply":   strings.TrimSuffix(openCodeIdlePage, "]") + `,{"info":{"id":"msg_3","role":"assistant","time":{"created":1793073210000}}}]`,
		"tie":     strings.TrimSuffix(openCodeIdlePage, "]") + `,{"info":{"id":"msg_3","role":"user","time":{"created":1793073204905}}}]`,
		"no time": strings.TrimSuffix(openCodeIdlePage, "]") + `,{"info":{"id":"msg_3","role":"user","time":{}}}]`,
		"no id":   strings.Replace(openCodeIdlePage, `"id":"msg_2",`, ``, 1),
	} {
		f.set("ses_synthetic", openCodeIdlePage)
		*clock = clock.Add(time.Second)
		s.openCodeContextRound(ctx)
		f.set("ses_synthetic", page)
		*clock = clock.Add(time.Second)
		s.openCodeContextRound(ctx)
		if r := reason(); r != "source_stale" {
			t.Fatalf("%s: %q", name, r)
		}
	}
	// A new turn that starts between the reads of one round proves nothing.
	f.set("ses_synthetic", openCodeIdlePage)
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	f.onStatus = func(f *openCodeFake) {
		f.messages["ses_synthetic"] = strings.TrimSuffix(openCodeIdlePage, "]") + `,{"info":{"id":"msg_3","role":"user","time":{"created":1793073210000}}}]`
	}
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "source_stale" {
		t.Fatalf("changed between the reads: %q", r)
	}
	// A reading that the store does not take, here one older than the stored reading, leaves no
	// proof standing and is not cached as recorded.
	f.onStatus = nil
	f.set("ses_synthetic", openCodeIdlePage)
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "" {
		t.Fatalf("before an older reading: %q", r)
	}
	f.set("ses_synthetic", strings.Replace(openCodeIdlePage, `"completed":1793073206710`, `"completed":1793073206000`, 1))
	for range 2 {
		*clock = clock.Add(time.Second)
		s.openCodeContextRound(ctx)
		if e := s.contextEvidence(k); e.Current || e.SourceAt != 1793073206710 {
			t.Fatalf("older reading: %+v", e)
		}
	}
	// A daemon restart forgets the proof; the first current round after it admits again.
	f.onStatus = nil
	f.set("ses_synthetic", openCodeIdlePage)
	s.openCodeContext.forget(nil)
	s.observed.mu.Lock()
	delete(s.observed.byKey, k)
	s.observed.mu.Unlock()
	if r := reason(); r != "context_unknown" {
		t.Fatalf("restart: %q", r)
	}
	*clock = clock.Add(time.Second)
	s.openCodeContextRound(ctx)
	if r := reason(); r != "" {
		t.Fatalf("after the restart: %q", r)
	}
}

// deepSeekQuietSample is the recorded sample as a closed turn: the turn starts at 12 and ends at
// 60, after the newest usage at 58. The projections carry the quiet point's inbox and questions.
func (f *deepSeekFake) quietSample() {
	f.sample()
	f.events = f.events[:12]
	f.add(`"type":"turn/start","data":{}`)
	for len(f.events) < 61 {
		switch len(f.events) {
		case 51:
			f.add(`"type":"assistant/message","data":{"message":"SENTINEL","usage":{"inputTokens":9408,"outputTokens":217,"totalTokens":9625}}`)
		case 58:
			f.add(`"type":"assistant/message","data":{"message":"SENTINEL","usage":{"inputTokens":728,"outputTokens":122,"totalTokens":9810,"cacheReadTokens":8960}}`)
		case 60:
			f.add(`"type":"turn/end","data":{}`)
		default:
			f.add(`"type":"step/end","data":{"text":"SENTINEL"}`)
		}
	}
	// The quiet point reads the newest turn whole, as the harness's turn window returns it.
	f.listed, f.running, f.pageSize = true, false, 128
	f.quietProjections(60, "", "")
}

func (f *deepSeekFake) quietProjections(asOfSeq int, nextTurn, questions string) {
	f.projections = fmt.Sprintf(`{"asOfSeq":%d,"values":{"title":"SENTINEL","contextPressure":{"pressureTokens":9688,"contextWindow":1024000},`+
		`"modelSelection":{"lastUsed":{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"},"next":null},`+
		`"inbox":{"next-turn":[%s],"next-step":[]},"userQuestions":{"active":[%s]}}}`, asOfSeq, nextTurn, questions)
}

// The DeepSeek reader proves a reading current when the quiet-point check passes in the same
// round and the projections are read again at the same log position. Events after the closed
// turn that change nothing do not end the proof.
func TestDeepSeekCurrentReading(t *testing.T) {
	ctx := context.Background()
	s, f, clock, register := deepSeekRound(t)
	session := register("session-synthetic", true)
	k := Key{"deepseek", session.ID}
	// The usage of 2026-10-15 is far older than the source age.
	reason := func() string { return handoffReason(s.contextEvidence(k), 0, clock.UnixMilli()) }
	round := func() string {
		t.Helper()
		*clock = clock.Add(time.Second)
		s.deepSeekContextRound(ctx)
		return reason()
	}
	f.set(func(f *deepSeekFake) { f.quietSample() })
	if r := round(); r != "" {
		t.Fatalf("quiet: %q", r)
	}
	f.set(func(f *deepSeekFake) {
		f.add(`"type":"session/title","data":{"title":"SENTINEL"}`)
		f.quietProjections(61, "", "")
	})
	if r := round(); r != "" {
		t.Fatalf("a neutral event after the turn: %q", r)
	}
	for name, change := range map[string]func(f *deepSeekFake){
		"running": func(f *deepSeekFake) { f.running = true },
		"queued": func(f *deepSeekFake) {
			f.quietProjections(60, `{"content":[{"type":"text","text":"SENTINEL"}],"source":{"kind":"user"}}`, "")
		},
		"question": func(f *deepSeekFake) { f.quietProjections(60, "", `{"id":"q1"}`) },
		"turn open": func(f *deepSeekFake) {
			f.add(`"type":"turn/start","data":{}`)
			f.quietProjections(61, "", "")
		},
		"compaction": func(f *deepSeekFake) {
			f.add(`"type":"compaction/start","data":{}`)
			f.quietProjections(61, "", "")
		},
		"not listed": func(f *deepSeekFake) { f.listed = false },
		"changed between the reads": func(f *deepSeekFake) {
			f.afterList = func(f *deepSeekFake) {
				if f.calls["session/list"]%2 == 0 {
					f.add(`"type":"session/title","data":{"title":"SENTINEL"}`)
					f.quietProjections(len(f.events)-1, "", "")
				}
			}
		},
	} {
		f.set(func(f *deepSeekFake) { f.quietSample(); f.afterList = nil })
		if r := round(); r != "" {
			t.Fatalf("%s, before: %q", name, r)
		}
		f.set(change)
		if r := round(); r != "source_stale" {
			t.Fatalf("%s: %q", name, r)
		}
	}
	// A newer usage event in the same millisecond as the reading's, with other counts, is the
	// new reading: quiet, it gets its own proof; busy, none, and the old proof ends.
	for _, busy := range []bool{false, true} {
		f.set(func(f *deepSeekFake) { f.quietSample(); f.afterList = nil })
		if r := round(); r != "" {
			t.Fatalf("busy %t, before: %q", busy, r)
		}
		f.set(func(f *deepSeekFake) {
			f.add(`"type":"turn/start","data":{}`)
			f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":100}}`)
			f.events[len(f.events)-1] = strings.Replace(f.events[len(f.events)-1], fmt.Sprintf(`"time":%d`, 1791916440000+int64(len(f.events)-1)*1000), `"time":1791916498000`, 1)
			f.add(`"type":"turn/end","data":{}`)
			f.projections = strings.Replace(f.projections, `"pressureTokens":9688`, `"pressureTokens":100`, 1)
			f.projections = strings.Replace(f.projections, `"asOfSeq":60`, `"asOfSeq":63`, 1)
			f.running = busy
		})
		r := round()
		e := s.contextEvidence(k)
		if *e.UsedTokens != 100 || e.SourceAt != 1791916498000 || e.Current == busy {
			t.Fatalf("busy %t: tied newer usage: %+v", busy, e)
		}
		if want := map[bool]string{false: "", true: "source_stale"}[busy]; r != want {
			t.Fatalf("busy %t: %q", busy, r)
		}
	}
	// A failed read ends the proof at once and confirms nothing.
	f.set(func(f *deepSeekFake) { f.quietSample(); f.afterList = nil; f.running = false })
	round()
	confirmed := s.contextEvidence(k).ConfirmedAt
	f.set(func(f *deepSeekFake) { f.status = http.StatusInternalServerError })
	round()
	if e := s.contextEvidence(k); e.Current || e.ConfirmedAt != confirmed {
		t.Fatalf("failed read: %+v", e)
	}
	// A newer usage is a new reading, current again once the session is quiet.
	f.set(func(f *deepSeekFake) {
		f.status = 0
		f.add(`"type":"turn/start","data":{}`)
		f.add(`"type":"assistant/message","data":{"usage":{"inputTokens":20000,"cacheReadTokens":10000}}`)
		f.add(`"type":"turn/end","data":{}`)
		f.projections = strings.Replace(f.projections, `"pressureTokens":9688`, `"pressureTokens":30000`, 1)
		f.projections = strings.Replace(f.projections, `"asOfSeq":60`, `"asOfSeq":63`, 1)
	})
	if r := round(); r != "" || *s.contextEvidence(k).UsedTokens != 30000 {
		t.Fatalf("a newer usage: %q %+v", r, s.contextEvidence(k))
	}
}

// End to end: a quiet DeepSeek session above its threshold, whose newest usage is old, gets its
// cycle from the reader's round; a busy one does not.
func TestDeepSeekIdleSessionGetsItsCycle(t *testing.T) {
	for _, busy := range []bool{false, true} {
		g := newDSHFixture(t)
		g.fake.set(func(f *dshFake) {
			s := f.sessions[dshRequester]
			f.add(dshRequester, `"type":"turn/start","data":{}`)
			f.add(dshRequester, `"type":"assistant/message","data":{"usage":{"inputTokens":900}}`)
			f.add(dshRequester, `"type":"turn/end","data":{}`)
			s.pressure = `"pressureTokens":900,"contextWindow":1000`
			s.running = busy
		})
		g.s.deepSeekContextRound(context.Background())
		e := g.s.contextEvidence(g.k)
		if !e.Found || g.clock.UnixMilli()-e.SourceAt <= contextSourceMaxAge.Milliseconds() {
			t.Fatalf("busy %t: reading %+v", busy, e)
		}
		v, err := g.s.ContextCycleStatus(context.Background(), g.k)
		if err != nil || busy != (v == nil) || !busy && v.State != cycleWaitingReady {
			t.Fatalf("busy %t: cycle %+v %v", busy, v, err)
		}
	}
}

// A daemon reading that the store does not take, because it is older than the stored reading,
// ends the stored proof inside the report, before admission reads it: no cycle starts from the
// stored reading. This holds for other counts while busy, and for the same counts while quiet,
// which would otherwise confirm the stored reading with the new proof (codex-koinon's
// reproduction, 2026-10-29).
func TestRejectedReaderReadingAdmitsNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		used int64
		busy bool
	}{{"other counts, busy", 100, true}, {"same counts, quiet", 900, false}} {
		ctx := context.Background()
		g := newDSHFixture(t)
		g.fake.set(func(f *dshFake) {
			f.add(dshRequester, `"type":"turn/start","data":{}`)
			f.add(dshRequester, fmt.Sprintf(`"type":"assistant/message","data":{"usage":{"inputTokens":%d}}`, c.used))
			f.add(dshRequester, `"type":"turn/end","data":{}`)
			f.sessions[dshRequester].pressure = fmt.Sprintf(`"pressureTokens":%d,"contextWindow":1000`, c.used)
			f.sessions[dshRequester].running = c.busy
		})
		_, usage, _, current, err := deepSeekContext(ctx, g.target, g.k.ID)
		if err != nil || usage == nil || current == c.busy {
			t.Fatalf("%s: native reading %+v %t %v", c.name, usage, current, err)
		}
		// The stored reading is newer than the native one and carries a proof, as one can while
		// the cycle's other conditions are not met.
		stored := ObservedValue{Source: usage.Source, At: usage.At + 1000, UsedTokens: int64p(900), LimitTokens: int64p(1000)}
		g.s.observed.mu.Lock()
		if g.s.observed.byKey == nil {
			g.s.observed.byKey = map[Key]map[string]storedValue{}
		}
		g.s.observed.byKey[g.k] = map[string]storedValue{"context": {ObservedValue: stored, seen: g.clock.UnixMilli(), epoch: dshRequester, current: true}}
		g.s.observed.mu.Unlock()
		g.s.deepSeekContextRound(ctx)
		if e := g.s.contextEvidence(g.k); e.Current || e.SourceAt != stored.At || e.ConfirmedAt != g.clock.UnixMilli() {
			t.Fatalf("%s: stored reading %+v", c.name, e)
		}
		if v, err := g.s.ContextCycleStatus(ctx, g.k); err != nil || v != nil {
			t.Fatalf("%s: a cycle started: %+v %v", c.name, v, err)
		}
		if n, err := cyclesWhere(ctx, g.s.db, `family='deepseek'`); err != nil || len(n) != 0 {
			t.Fatalf("%s: cycles %+v %v", c.name, n, err)
		}
	}
}
