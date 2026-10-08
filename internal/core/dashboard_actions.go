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
)

var (
	tmuxSessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
	launchFamilies  = map[string]bool{"codex": true, "agy": true, "opencode": true}
)

// noticeText is every notice a view can show; any other code shows nothing.
var noticeText = map[string]string{
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

// done answers an action with 303 to its view and a notice code.
func done(w http.ResponseWriter, r *http.Request, view string, query url.Values, code string) {
	if query == nil {
		query = url.Values{}
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
