package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Dashboard actions (sprint chunk 09). Each action is a POST under /dashboard/actions/
// behind the chunk 08 request protection. It answers 303 to the view it changed, with a
// fixed notice code, so a reload never repeats it. After the request checks pass, each
// action writes one audit record: an accepted change in its own transaction, a refusal
// when storage can hold it.

const (
	actionFormLimit = 4096
	// Every byte of a 64 KiB body can be percent-encoded.
	sendFormLimit = 3*maxBody + 4096
	launchTimeout = 15 * time.Second
	// bulkLimit bounds the sessions of one bulk action; each selection is at most 512 bytes,
	// and form encoding can triple it.
	bulkLimit     = 100
	bulkFormLimit = bulkLimit*3*(512+3) + 4096
)

var (
	tmuxSessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	launchFamilies  = map[string]bool{"claude": true, "codex": true, "agy": true, "opencode": true, "grok": true}
)

// noticeText is every notice a view can show; any other code shows nothing.
var noticeText = map[string]string{
	"participant_chosen":        "The session now holds the participant address.",
	"settings_saved":            "The session timing was saved and applies now.",
	"context_policy_saved":      "The family's context mode and threshold were saved; no session was enabled.",
	"context_enable_saved":      "The session's context-cycle Enable was saved.",
	"context_threshold_saved":   "The session's own threshold was saved; 0 uses the family threshold.",
	"context_cycle_unavailable": "Refused: no verified handoff cycle is available for this session yet.",
	"context_enable_changed":    "Refused: the session's Enable changed since the page was loaded. Reload and try again.",
	"not_participant":           "Refused: the session does not belong to that participant.",
	"retired":                   "The session was retired.",
	"purge_marked":              "The session is marked for purge. An active session was retired; the next maintenance sweep deletes it with its whole inbox.",
	"purge_unmarked":            "The purge mark was removed. The session stays as it is.",
	"confirmation_required":     "Refused: confirm the purge first.",
	"released":                  "The claim was released.",
	"acknowledged":              "The inbox was acknowledged.",
	"cleared":                   "The inbox was cleared: every message is acknowledged.",
	"sent":                      "The message was sent as maintainer.",
	"launched":                  "The session was started in a new tmux session.",
	"invalid_request":           "Refused: the request was not valid.",
	"room_created":              "The room was created. Add members to it below.",
	"decision_approved":         "The verdict Approve was saved with the text you saw. Each named agent gets the verdict.",
	"decision_declined":         "The verdict Decline was saved with the text you saw. Each named agent gets the verdict.",
	"decision_dismissed":        "The fyi item was dismissed and archived. This is not an approval.",
	"stale_revision":            "The item changed after the page showed it. Read it again; nothing was saved.",
	"decided":                   "The item already has a verdict; nothing was saved.",
	"hidden_command":            "A stored command is hidden, so the item cannot be approved as displayed; nothing was saved.",
	"invalid_verdict":           "A decision item takes Approve or Decline; an fyi item takes Dismiss. Nothing was saved.",
	"room_refill_saved":         "The refill value was saved. It applies at the next maintainer post; the current allowance is unchanged.",
	"room_member_added":         "The session is a member of the room.",
	"room_member_removed":       "The member was removed from the room.",
	"room_deleted":              "The room was deleted with its members and posts.",
	"bulk_applied":              "The action was applied to each selected session.",
	"bulk_partial":              "Some selected sessions were refused, for example because they changed since the page was loaded; the audit log names each result.",
	"no_selection":              "Refused: select one or more sessions first. Nothing was changed.",
	"room_posted":               "The post was sent to the room's active members. The agents' allowance is set to the refill value.",
	"exchange_limit":            "Refused: the room's agent-post allowance is used up.",
	"room_post_limit":           "Refused: the daemon already retains 10,000 room posts.",
	"room_replay_limit":         "Refused: the daemon already retains 10,000 room post retry records.",
	"room_not_found":            "Refused: no such room.",
	"room_limit":                "Refused: the daemon already has 64 rooms.",
	"room_member_limit":         "Refused: the room already has 32 active members.",
	"room_history_limit":        "Refused: the room already has 128 current and former memberships.",
	"room_member_not_found":     "Refused: the room has no such current member.",
	"maintainer_session":        "Refused: the maintainer session cannot be retired or purged.",
	"revision_changed":          "Refused: the session changed since the page was loaded. Reload and try again.",
	"session_not_active":        "Refused: the session is not active.",
	"session_not_found":         "Refused: no such session.",
	"peer_not_found":            "Refused: no session has that name.",
	"alias_unheld":              "Refused: no active session holds that alias.",
	"recipient_inactive":        "Refused: the recipient is expired or retired.",
	"ack_beyond_last":           "Refused: that sequence is beyond the last message.",
	"capacity":                  "Refused: storage capacity reached. Nothing was changed.",
	"storage_blocked":           "Refused: writes wait for koinon recover.",
	"launch_refused":            "Refused: the launcher did not start the session.",
	"launched_unrecorded":       "The session was started, but its audit result is not written yet; the daemon writes it when storage allows.",
	"launch_refused_unrecorded": "Refused: the launcher did not start the session. The audit result is not written yet; the daemon writes it when storage allows.",
	"revision_conflict":         "Refused: the work item changed since the page was loaded. Reload and try again.",
	"stale_claim":               "Refused: the claim changed since the page was loaded. Reload and try again.",
	"invalid_transition":        "Refused: the work item is not in a state that can be released.",
	"work_not_found":            "Refused: the work item is gone.",
	"storage_error":             "Refused: the daemon could not complete the action.",
	"memory_recorded":           "The entry was recorded as maintainer.",
	"memory_superseded":         "The edit was recorded as a new entry that replaces the old one; the old entry stays readable.",
	"memory_revoked":            "The entry was revoked; it stays readable with its revocation.",
	"memory_conflict":           "Recorded, but another entry had already replaced or revoked that entry; both are kept and the new entry names the conflict.",
	"reason_required":           "Refused: a revocation needs a reason.",
	"store_not_found":           "Refused: no memory store has that repository.",
	"no_such_entry":             "Refused: that entry does not exist.",
	"entry_too_large":           "Refused: the entry is too large.",
	"work_created":              "The work item was created as maintainer.",
	"work_proposed":             "The proposed assignee was recorded.",
	"work_edited":               "The scope was edited; the earlier revision stays in the history.",
	"work_finished":             "The work item was finished.",
	"work_claimed":              "Refused: the item has a live claim. Release the claim first, then edit.",
	"idempotency_conflict":      "Refused: that form was already submitted with other content. Reload and try again.",
	"retry_deadline_expired":    "Refused: the form is too old. Reload and try again.",
}

var noticeCode = regexp.MustCompile(`^[a-z_]{1,40}$`)

// notice is the text of a notice code. A fixed refusal code without its own text is
// shown as the code itself; anything else shows nothing.
func notice(code string) string {
	if text, ok := noticeText[code]; ok {
		return text
	}
	if noticeCode.MatchString(code) {
		return "Refused: " + code + "."
	}
	return ""
}

// done answers an action with 303 to its view and a notice code. The redirect keeps the
// order and filters that the form's back field names, unless the action sets them.
func done(w http.ResponseWriter, r *http.Request, view string, query url.Values, code string) {
	if query == nil {
		query = url.Values{}
	}
	if back, err := url.ParseQuery(r.PostForm.Get("back")); err == nil && len(r.PostForm.Get("back")) <= backLimit {
		for name, values := range viewOrder(view, back) {
			if !query.Has(name) {
				query[name] = values
			}
		}
	}
	query.Set("notice", code)
	http.Redirect(w, r, "/dashboard/"+view+"?"+query.Encode(), http.StatusSeeOther)
}

// sessionKey reads a session key from a form: an agent session or the maintainer's.
func sessionKey(r *http.Request) (Key, bool) {
	k := Key{Family: r.PostForm.Get("family"), ID: r.PostForm.Get("id")}
	return k, validKey(k.Family, k.ID) || k == maintainerKey
}

func formInt(r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PostForm.Get(name), 10, 64)
	return v, err == nil && v >= 0
}

// dashboardActions adds the action routes; authed applies the request protection with
// the form body limit of each route.
func (d *Daemon) dashboardActions(mux *http.ServeMux, authed func(int64, func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	// run audits one action: invalid input is refused before any store call; a store
	// error becomes its stable code.
	run := func(w http.ResponseWriter, r *http.Request, action, target, view string, query url.Values, valid bool,
		apply func(ctx context.Context) error, accepted string, refusal func(error) string) {
		ctx, pending := withAudit(r.Context(), action, target)
		if !valid {
			d.store.auditRefusal(ctx, pending, "invalid_request")
			done(w, r, view, query, "invalid_request")
			return
		}
		if err := apply(ctx); err != nil {
			code := errorCode(err)
			if refusal != nil {
				code = refusal(err)
			}
			d.store.auditRefusal(ctx, pending, code)
			done(w, r, view, query, code)
			return
		}
		done(w, r, view, query, accepted)
	}

	mux.HandleFunc("POST /dashboard/actions/settings", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		var v Settings
		var ok [3]bool
		v.LifetimeSeconds, ok[0] = formInt(r, "lifetime_seconds")
		v.RenewalSeconds, ok[1] = formInt(r, "renewal_seconds")
		v.GuardSeconds, ok[2] = formInt(r, "guard_seconds")
		run(w, r, "settings", v.String(), "settings", nil, ok[0] && ok[1] && ok[2] && v.Valid(),
			func(ctx context.Context) error { return d.store.SetSettings(ctx, v) }, "settings_saved", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/context-family-settings", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		family, mode := r.PostForm.Get("family"), r.PostForm.Get("mode")
		threshold, ok := formInt(r, "threshold")
		valid := ok && contextFamily(family) && validContextPolicy(family, mode, threshold)
		target := "context invalid"
		if contextFamily(family) {
			target = "context " + family
		}
		run(w, r, "context-family-settings", target, "settings", nil, valid,
			func(ctx context.Context) error { return d.store.SetContextPolicy(ctx, family, mode, threshold) }, "context_policy_saved", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/context-enable", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		peer, state := r.PostForm.Get("peer"), r.PostForm.Get("enabled")
		revision, ok1 := formInt(r, "revision")
		ifRevision, ok2 := formInt(r, "if_revision")
		valid := ok1 && ok2 && revision > 0 && validContextPeer(peer) && (state == "on" || state == "off")
		target := "context enable invalid"
		if valid {
			target = "context enable " + peer + " " + state
		}
		run(w, r, "context-enable", target, "sessions", nil, valid,
			func(ctx context.Context) error {
				_, err := d.store.SetContextEnable(ctx, peer, revision, ifRevision, state == "on")
				return err
			}, "context_enable_saved", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/context-threshold", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		peer := r.PostForm.Get("peer")
		threshold, ok := formInt(r, "threshold")
		valid := ok && threshold >= 0 && threshold <= 100 && validContextPeer(peer)
		target := "context threshold invalid"
		if valid {
			target = "context threshold " + peer + " " + strconv.FormatInt(threshold, 10)
		}
		run(w, r, "context-threshold", target, "sessions", nil, valid,
			func(ctx context.Context) error {
				_, err := d.store.SetContextThreshold(ctx, peer, threshold)
				return err
			}, "context_threshold_saved", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/context-cycle-answer", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		id, digest, option := r.PostForm.Get("cycle_id"), r.PostForm.Get("digest"), r.PostForm.Get("option")
		revision, ok := formInt(r, "revision")
		valid := ok && revision > 0 && len(id) == 32 && len(digest) == 64 && len(option) == 1 && option >= "1" && option <= "9"
		target := "context cycle answer invalid"
		if valid {
			target = "context cycle " + id + " option " + option
		}
		run(w, r, "context-cycle-answer", target, "sessions", nil, valid,
			func(ctx context.Context) error { return d.store.AnswerCycleDialog(ctx, id, revision, digest, option) }, "cycle_dialog_answered", nil)
	}))
	mux.HandleFunc("POST /dashboard/actions/participant-holder", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		k, valid := sessionKey(r)
		revision, ok := formInt(r, "revision")
		address := r.PostForm.Get("address")
		run(w, r, "participant-holder", address, "sessions", nil,
			valid && k != maintainerKey && ok && revision > 0 && address != "" && len(address) <= 256,
			func(ctx context.Context) error {
				if err := d.store.ChooseHolder(ctx, address, k, revision); err != nil {
					return err
				}
				d.signalWorkNotices()
				return nil
			},
			"participant_chosen", func(err error) string {
				if !errors.Is(err, ErrConflict) {
					return errorCode(err)
				}
				s, readErr := scanSession(d.store.db.QueryRowContext(r.Context(), sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), d.store.now().UnixMilli())
				if readErr == nil && s.State != "active" {
					return "session_not_active"
				}
				return "revision_changed"
			})
	}))

	mux.HandleFunc("POST /dashboard/actions/retire", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		k, valid := sessionKey(r)
		revision, ok := formInt(r, "revision")
		target := k.Family + ":" + k.ID
		if valid && k == maintainerKey {
			ctx, pending := withAudit(r.Context(), "retire", target)
			d.store.auditRefusal(ctx, pending, "maintainer_session")
			done(w, r, "sessions", nil, "maintainer_session")
			return
		}
		run(w, r, "retire", target, "sessions", nil, valid && ok && revision > 0, func(ctx context.Context) error {
			_, err := d.store.Mutate(ctx, Mutation{Family: k.Family, ID: k.ID, IfRevision: revision}, true)
			return err
		}, "retired", func(err error) string {
			if !errors.Is(err, ErrConflict) {
				return errorCode(err)
			}
			// Mutate refuses both a changed revision and a session that is not active.
			s, readErr := scanSession(d.store.db.QueryRowContext(r.Context(), sessionQuery+` WHERE s.family=? AND s.id=?`, k.Family, k.ID), d.store.now().UnixMilli())
			if readErr == nil && s.State != "active" {
				return "session_not_active"
			}
			return "revision_changed"
		})
	}))

	// purge marks a session for the maintenance sweep to delete with its whole inbox, after an
	// explicit confirmation (#216); unpurge removes the mark before the sweep runs.
	purge := func(mark bool) func(http.ResponseWriter, *http.Request, string) {
		action, accepted := "purge", "purge_marked"
		if !mark {
			action, accepted = "unpurge", "purge_unmarked"
		}
		return func(w http.ResponseWriter, r *http.Request, _ string) {
			k, valid := sessionKey(r)
			revision, ok := formInt(r, "revision")
			target := k.Family + ":" + k.ID
			ctx, pending := withAudit(r.Context(), action, target)
			if valid && k == maintainerKey {
				d.store.auditRefusal(ctx, pending, "maintainer_session")
				done(w, r, "sessions", nil, "maintainer_session")
				return
			}
			if valid && ok && mark && r.PostForm.Get("confirm") != "purge" {
				d.store.auditRefusal(ctx, pending, "confirmation_required")
				done(w, r, "sessions", nil, "confirmation_required")
				return
			}
			run(w, r, action, target, "sessions", nil, valid && ok && revision > 0, func(ctx context.Context) error {
				return d.store.markPurge(ctx, k, revision, mark)
			}, accepted, func(err error) string {
				if errors.Is(err, ErrConflict) {
					return "revision_changed"
				}
				return errorCode(err)
			})
		}
	}
	// bulk applies retire, purge, unpurge or clear to the sessions selected in the list (#388),
	// each through the single action's store path under its own audit record. A purge needs
	// the same confirmation as the single purge.
	mux.HandleFunc("POST /dashboard/actions/bulk", authed(bulkFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		action, selected := r.PostForm.Get("action"), r.PostForm["s"]
		known := slices.Contains([]string{"retire", "purge", "unpurge", "clear"}, action)
		if !known || len(selected) == 0 || len(selected) > bulkLimit {
			// An empty selection has its own notice, so the person knows to select sessions (#442).
			code := "invalid_request"
			if known && len(selected) == 0 {
				code = "no_selection"
			}
			ctx, pending := withAudit(r.Context(), "bulk", "invalid")
			d.store.auditRefusal(ctx, pending, code)
			done(w, r, "sessions", nil, code)
			return
		}
		if action == "purge" && r.PostForm.Get("confirm") != "purge" {
			ctx, pending := withAudit(r.Context(), "purge", "bulk of "+strconv.Itoa(len(selected)))
			d.store.auditRefusal(ctx, pending, "confirmation_required")
			done(w, r, "sessions", nil, "confirmation_required")
			return
		}
		refused := 0
		for _, value := range selected {
			family, rest, _ := strings.Cut(value, ":")
			i := strings.LastIndex(rest, ":")
			id, revision := rest, int64(-1)
			if i >= 0 {
				id = rest[:i]
				if n, err := strconv.ParseInt(rest[i+1:], 10, 64); err == nil {
					revision = n
				}
			}
			k := Key{family, id}
			ctx, pending := withAudit(r.Context(), action, family+":"+id)
			var err error
			switch {
			case !validKey(family, id) || revision < 1 || len(value) > 512:
				err = ErrInvalid
			case k == maintainerKey:
				err = Refusal{"maintainer_session", "the maintainer session is built in"}
			case action == "retire":
				_, err = d.store.Mutate(ctx, Mutation{Family: family, ID: id, IfRevision: revision}, true)
			case action == "purge" || action == "unpurge":
				err = d.store.markPurge(ctx, k, revision, action == "purge")
			default:
				_, err = d.store.ackAt(ctx, k, revision)
			}
			if err != nil {
				code := errorCode(err)
				if errors.Is(err, ErrConflict) {
					code = "revision_changed"
				}
				d.store.auditRefusal(ctx, pending, code)
				refused++
			}
		}
		d.signalWorkNotices()
		notice := "bulk_applied"
		if refused > 0 {
			notice = "bulk_partial"
		}
		done(w, r, "sessions", nil, notice)
	}))
	mux.HandleFunc("POST /dashboard/actions/purge", authed(actionFormLimit, purge(true)))
	mux.HandleFunc("POST /dashboard/actions/unpurge", authed(actionFormLimit, purge(false)))

	mux.HandleFunc("POST /dashboard/actions/release", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		repository, workID, consumer := r.PostForm.Get("repository"), r.PostForm.Get("work_id"), r.PostForm.Get("consumer")
		revision, ok1 := formInt(r, "revision")
		generation, ok2 := formInt(r, "generation")
		valid := ok1 && ok2 && filepath.IsAbs(repository) && len(repository) <= 4096 && workID != "" && len(workID) <= 64 &&
			consumer != "" && len(consumer) <= 128 && !strings.ContainsAny(repository+workID+consumer, "\x00\r\n")
		run(w, r, "release", repository+" "+workID+" owner "+consumer, "work", nil, valid, func(ctx context.Context) error {
			fields := map[string]json.RawMessage{}
			for name, value := range map[string]any{"work_id": workID, "if_revision": revision, "claim_generation": generation,
				"checkpoint": "Released by the maintainer from the dashboard"} {
				fields[name], _ = json.Marshal(value)
			}
			// The release runs on behalf of the claim's owner; the work event names the maintainer.
			_, err := d.store.Work(ctx, MemoryCaller{Repository: repository, Family: maintainerKey.Family, Name: "maintainer", Consumer: consumer}, "work-release", fields)
			return err
		}, "released", nil)
	}))

	inbox := func(action string) func(http.ResponseWriter, *http.Request, string) {
		return func(w http.ResponseWriter, r *http.Request, _ string) {
			k, valid := sessionKey(r)
			through := int64(-1)
			if action == "acknowledge" {
				var ok bool
				through, ok = formInt(r, "through")
				valid = valid && ok
			}
			query := url.Values{}
			if to := r.PostForm.Get("to"); to != "" && len(to) <= 256 {
				query.Set("to", to)
			}
			accepted := "acknowledged"
			if action == "clear" {
				accepted = "cleared"
			}
			run(w, r, action, k.Family+":"+k.ID, "messages", query, valid, func(ctx context.Context) error {
				_, err := d.store.ack(ctx, k, through, true)
				return err
			}, accepted, nil)
		}
	}
	mux.HandleFunc("POST /dashboard/actions/acknowledge", authed(actionFormLimit, inbox("acknowledge")))
	mux.HandleFunc("POST /dashboard/actions/clear", authed(actionFormLimit, inbox("clear")))

	mux.HandleFunc("POST /dashboard/actions/send", authed(sendFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		to, body := r.PostForm.Get("to"), r.PostForm.Get("body")
		valid := to != "" && len(to) <= 256 && !strings.ContainsAny(to, "\x00\r\n") && body != "" && len(body) <= maxBody && utf8.ValidString(body)
		target := "to " + to + " bytes " + strconv.Itoa(len(body))
		if len(to) > 256 || strings.ContainsAny(to, "\x00\r\n") {
			target = "invalid recipient"
		}
		run(w, r, "send", target, "messages", url.Values{"to": {"maintainer"}}, valid, func(ctx context.Context) error {
			_, err := d.store.send(ctx, maintainerKey, to, body, true)
			return err
		}, "sent", nil)
	}))

	d.memoryActions(mux, authed)
	d.workActions(mux, authed, run)
	d.roomActions(mux, authed, run)
	d.decisionActions(mux, authed, run)

	mux.HandleFunc("POST /dashboard/actions/launch", authed(actionFormLimit, func(w http.ResponseWriter, r *http.Request, _ string) {
		family, directory, name := r.PostForm.Get("family"), r.PostForm.Get("directory"), r.PostForm.Get("name")
		valid := launchFamilies[family] && filepath.IsAbs(directory) && filepath.Clean(directory) == directory &&
			len(directory) <= 4096 && !strings.ContainsAny(directory, "\x00\r\n")
		if valid {
			info, err := os.Stat(directory)
			valid = err == nil && info.IsDir()
		}
		if name == "" && valid {
			name = defaultSessionName(family, directory)
		}
		valid = valid && tmuxSessionName.MatchString(name)
		target := family + " " + directory + " tmux " + name
		if !valid {
			ctx, pending := withAudit(r.Context(), "launch", family+" invalid")
			d.store.auditRefusal(ctx, pending, "invalid_request")
			done(w, r, "sessions", nil, "invalid_request")
			return
		}
		// The record is written first, so a launch never happens without one.
		id, err := d.store.auditLaunch(r.Context(), target)
		if err != nil {
			done(w, r, "sessions", nil, errorCode(err))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), launchTimeout)
		defer cancel()
		output, err := d.launch(ctx, []string{family, "--state-dir", d.root, "--address", d.launchAddress(),
			"--directory", directory, "--tmux-session", name})
		var reply struct {
			OK        bool   `json:"ok"`
			SessionID string `json:"session_id"`
			PaneID    string `json:"pane_id"`
		}
		if err != nil || json.Unmarshal(output, &reply) != nil || !reply.OK {
			done(w, r, "sessions", nil, d.recordLaunch(r.Context(), id, "refused", "launch_refused", "launch_refused"))
			return
		}
		reason := "tmux " + reply.SessionID + " " + reply.PaneID
		if len(reason) > 64 || strings.ContainsAny(reason, "\x00\r\n") {
			reason = "tmux"
		}
		done(w, r, "sessions", nil, d.recordLaunch(r.Context(), id, "accepted", reason, "launched"))
	}))
}

// recordLaunch writes a launch's result and returns the notice. When the update cannot be
// written, the result waits in memory for the maintenance loop, and the notice says so.
func (d *Daemon) recordLaunch(ctx context.Context, id int64, result, reason, notice string) string {
	if err := d.store.finishLaunch(ctx, id, result, reason); err == nil {
		return notice
	}
	d.launchMu.Lock()
	d.unfinished[id] = [2]string{result, reason}
	d.launchMu.Unlock()
	return notice + "_unrecorded"
}

// finishLaunches writes the launch results that recordLaunch could not.
func (d *Daemon) finishLaunches(ctx context.Context) {
	d.launchMu.Lock()
	pending := make(map[int64][2]string, len(d.unfinished))
	for id, v := range d.unfinished {
		pending[id] = v
	}
	d.launchMu.Unlock()
	for id, v := range pending {
		if d.store.finishLaunch(ctx, id, v[0], v[1]) == nil {
			d.launchMu.Lock()
			delete(d.unfinished, id)
			d.launchMu.Unlock()
		}
	}
}

// launchAddress is the daemon's IPv4 loopback listener, which the launcher reaches.
func (d *Daemon) launchAddress() string {
	addresses := d.Addresses()
	for _, a := range addresses {
		if strings.HasPrefix(a, "127.") {
			return a
		}
	}
	if len(addresses) > 0 {
		return addresses[0]
	}
	return ""
}

// defaultSessionName is FAMILY-DIRECTORY-XXXX, from the characters tmux and the launcher
// accept, with four random hex digits so that a second start in one folder gets its own.
func defaultSessionName(family, directory string) string {
	base := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, filepath.Base(directory))
	if len(base) > 40 {
		base = base[:40]
	}
	var b [2]byte
	rand.Read(b[:])
	return family + "-" + base + "-" + hex.EncodeToString(b[:])
}

// runLauncher runs this binary as the chunk 10 launcher. With --tmux-session it starts
// the agent detached and prints the tmux IDs as JSON; it never attaches.
func runLauncher(ctx context.Context, args []string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = nil
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
