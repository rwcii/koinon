package core

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/link"
	qrcode "github.com/skip2/go-qrcode"
)

// The link's routes (#670). Status is an agent API read. Name, invite, join, unpair, cancel
// and rename need the maintainer: a maintainer token on the agent API, or the dashboard login
// (LINK-670-PAIR, PDR 009). Each change writes one audit record; no target names an
// invitation's text.

const linkCallTimeout = 2*linkIOTimeout + 4*time.Second

// LinkInvitationResult is a new invitation and how long it is valid.
type LinkInvitationResult struct {
	Invitation     string `json:"invitation"`
	ExpiresSeconds int64  `json:"expires_seconds"`
}

// LinkJoinResult is the pair that a join made.
type LinkJoinResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Advertised string `json:"advertised"`
}

func linkTarget(prefix, value string) string {
	return prefix + " " + auditName(value)
}

func (d *Daemon) linkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/link/status", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		respond(w, 200, map[string]any{"ok": true, "result": d.linkStatus(ctx)})
	})
}

// ErrLinkAuditFailed is a link change that was made without its audit record.
var ErrLinkAuditFailed = Refusal{"link_audit_failed", "the change was made, but its audit record could not be written"}

// linkAct runs one link change as a maintainer action and records it as accepted with the
// token's origin: the change is in the link's files, not in the store.
func (d *Daemon) linkAct(ctx context.Context, w http.ResponseWriter, action, target string, valid bool, apply func(ctx context.Context) (any, error)) {
	d.maintainerAct(ctx, w, action, target, valid, func(ctx context.Context) (any, error) {
		result, err := apply(ctx)
		if err != nil {
			return nil, err
		}
		if d.store.auditPendingAccepted(ctx) != nil {
			return nil, ErrLinkAuditFailed
		}
		return result, nil
	})
}

// linkMaintainerRoutes are the maintainer token routes of the link.
func (d *Daemon) linkMaintainerRoutes(mux *http.ServeMux) {
	maintainerRoute(d, mux, "link-name", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Name string `json:"name"`
		Port int    `json:"port"`
	}) {
		d.linkAct(ctx, w, "link-name", linkTarget("name", q.Name), q.Name != "" && q.Port >= 0, func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, linkCallTimeout)
			defer cancel()
			return nil, d.linkSetup(ctx, q.Name, q.Port)
		})
	})
	maintainerRoute(d, mux, "link-invite", 16384, func(ctx context.Context, w http.ResponseWriter, q struct{ maintainerToken }) {
		d.linkAct(ctx, w, "link-invite", "link invitation", true, func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, linkCallTimeout)
			defer cancel()
			text, err := d.linkInvite(ctx)
			if err != nil {
				return nil, err
			}
			return LinkInvitationResult{Invitation: text, ExpiresSeconds: int64(linkInviteLife / time.Second)}, nil
		})
	})
	maintainerRoute(d, mux, "link-join", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Invitation string `json:"invitation"`
	}) {
		d.linkAct(ctx, w, "link-join", "link invitation", q.Invitation != "", func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, linkCallTimeout)
			defer cancel()
			p, err := d.linkJoin(ctx, q.Invitation)
			if err != nil {
				return nil, err
			}
			return LinkJoinResult{ID: p.ID, Name: p.Effective, Advertised: p.Advertised}, nil
		})
	})
	maintainerRoute(d, mux, "link-unpair", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Pair string `json:"pair"`
	}) {
		d.linkAct(ctx, w, "link-unpair", linkTarget("pair", q.Pair), q.Pair != "", func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, linkCallTimeout)
			defer cancel()
			return nil, d.linkUnpair(ctx, q.Pair)
		})
	})
	maintainerRoute(d, mux, "link-cancel", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Pair string `json:"pair"`
	}) {
		d.linkAct(ctx, w, "link-cancel", linkTarget("pair", q.Pair), q.Pair != "", func(ctx context.Context) (any, error) {
			return nil, d.linkCancel(q.Pair)
		})
	})
	maintainerRoute(d, mux, "link-queue", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Pair string `json:"pair"`
	}) {
		queues, err := d.linkQueues(ctx, q.Pair)
		maintainerRead(w, queues, err)
	})
	maintainerRoute(d, mux, "link-cancel-message", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		LinkID string `json:"link_id"`
	}) {
		d.linkAct(ctx, w, "link-cancel-message", linkTarget("message", q.LinkID), q.LinkID != "", func(ctx context.Context) (any, error) {
			return d.linkCancelMessage(ctx, q.LinkID)
		})
	})
	maintainerRoute(d, mux, "link-rename", 16384, func(ctx context.Context, w http.ResponseWriter, q struct {
		maintainerToken
		Pair string `json:"pair"`
		Name string `json:"name"`
	}) {
		d.linkAct(ctx, w, "link-rename", linkTarget("pair", q.Pair)+linkTarget(" name", q.Name), q.Pair != "", func(ctx context.Context) (any, error) {
			return nil, d.linkRename(q.Pair, q.Name)
		})
	})
}

// The notices of the dashboard's Computers section: each result, and each refusal with the
// step that its message names.
func init() {
	for code, text := range map[string]string{
		"link_named":             "This computer's name and link port were saved, and the link is open.",
		"link_joined":            "The computers are paired.",
		"link_unpaired":          "The computer was unpaired. The other computer removes the pair when it answers, or shows it as rejected at its next connection.",
		"link_cancelled":         "The pending pair or the open invitations were cancelled.",
		"link_renamed":           "The computer's name on this computer was saved.",
		"link_message_cancelled": "The message was cancelled, or its cancel waits for the other computer's answer.",
		"link_audit_failed":      "The change was made, but its audit record could not be written.",
	} {
		noticeText[code] = text
	}
	for _, r := range []Refusal{ErrLinkRemoteOff, ErrLinkNotSetUp, ErrLinkNoPair, ErrLinkSelf, ErrLinkPaired, ErrLinkNameUsed,
		ErrLinkPortRange, ErrLinkUnreachable, ErrLinkWrongKey, ErrLinkInvitation, ErrLinkLimit, ErrLinkNameInvalid, ErrLinkTextInvalid,
		ErrLinkComputerUnknown, ErrLinkMaintainerRemote, ErrLinkAddressInvalid, ErrLinkQueueFull, ErrLinkMessageGone,
		ErrLinkReplyPairGone, ErrLinkReplyTarget, ErrLinkReplyLocal} {
		noticeText[r.Code] = "Refused: " + r.Message + "."
	}
}

// linkInvitationPage shows one new invitation. It answers the POST itself, so the secret is
// never in a URL; a reload asks the browser before it makes another invitation.
var linkInvitationPage = template.Must(template.New("invitation").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Koinon · Add a computer</title>
<link rel="stylesheet" href="/dashboard/static/app.css"><script src="/dashboard/static/app.js" defer></script></head>
<body><main><h1>Add a computer</h1>
<p>On the other computer, open Koinon's Settings, choose <b>Add a computer</b>, then <b>Paste invitation</b>, and paste this text. Or run <code>koinon link join</code> there and paste it. It works once, for {{.Minutes}} minutes.</p>
<div class="remote-url"><code>{{.Text}}</code> <button type="button" class="copy">Copy invitation</button></div>
{{if .QR}}<p>{{.QR}}</p>{{end}}
{{if .Unaudited}}<p>The invitation is open, but its audit record could not be written.</p>{{end}}
<p>Keep this text private: it lets one computer pair with this one until it expires.</p>
<p><a href="/dashboard/settings#computers">Back to Settings</a></p></main></body></html>
`))

// linkQRSVG draws text as an inline SVG QR code. The dashboard's policy allows no data:
// images, and an inline SVG with presentation attributes needs no style or script.
func linkQRSVG(text string) (template.HTML, error) {
	qr, err := qrcode.New(text, qrcode.Low)
	if err != nil {
		return "", err
	}
	bits := qr.Bitmap()
	var b strings.Builder
	n := len(bits)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Invitation QR code" width="288" height="288" viewBox="0 0 ` + strconv.Itoa(n) + " " + strconv.Itoa(n) + `" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y, row := range bits {
		for x, on := range row {
			if on {
				b.WriteString("M" + strconv.Itoa(x) + " " + strconv.Itoa(y) + "h1v1h-1z")
			}
		}
	}
	b.WriteString(`"/></svg>`)
	// The path holds only numbers and fixed letters.
	return template.HTML(b.String()), nil
}

// dashboardLinkRoutes are the Computers section's actions. Each answers 303 to the section
// with a notice, except invite, which shows the invitation.
func (d *Daemon) dashboardLinkRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	back := func(w http.ResponseWriter, r *http.Request, code string) {
		http.Redirect(w, r, "/dashboard/settings?"+url.Values{"notice": {code}}.Encode()+"#computers", http.StatusSeeOther)
	}
	act := func(name string, target func(*http.Request) string, done string, apply func(ctx context.Context, r *http.Request) error) {
		mux.HandleFunc("POST /dashboard/actions/"+name, authed(func(w http.ResponseWriter, r *http.Request, _ string) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), linkCallTimeout)
			defer cancel()
			t := target(r)
			ctx, pending := withAudit(ctx, name, t)
			if err := apply(ctx, r); err != nil {
				code := errorCode(err)
				d.store.auditRefusal(ctx, pending, code)
				back(w, r, code)
				return
			}
			if d.store.auditPendingAccepted(ctx) != nil {
				done = "link_audit_failed"
			}
			back(w, r, done)
		}))
	}
	pair := func(r *http.Request) string { return linkTarget("pair", r.PostFormValue("pair")) }
	act("link-name", func(r *http.Request) string { return linkTarget("name", r.PostFormValue("name")) }, "link_named", func(ctx context.Context, r *http.Request) error {
		port := 0
		if v := strings.TrimSpace(r.PostFormValue("port")); v != "" {
			var err error
			if port, err = strconv.Atoi(v); err != nil {
				return Refusal{"link_port_invalid", "use a link port of 1 to 65535"}
			}
		}
		return d.linkSetup(ctx, r.PostFormValue("name"), port)
	})
	act("link-join", func(*http.Request) string { return "link invitation" }, "link_joined", func(ctx context.Context, r *http.Request) error {
		_, err := d.linkJoin(ctx, r.PostFormValue("invitation"))
		return err
	})
	act("link-unpair", pair, "link_unpaired", func(ctx context.Context, r *http.Request) error {
		return d.linkUnpair(ctx, r.PostFormValue("pair"))
	})
	act("link-cancel", pair, "link_cancelled", func(ctx context.Context, r *http.Request) error {
		return d.linkCancel(r.PostFormValue("pair"))
	})
	act("link-cancel-message", func(r *http.Request) string { return linkTarget("message", r.PostFormValue("link_id")) }, "link_message_cancelled",
		func(ctx context.Context, r *http.Request) error {
			_, err := d.linkCancelMessage(ctx, r.PostFormValue("link_id"))
			return err
		})
	act("link-rename", func(r *http.Request) string {
		return pair(r) + linkTarget(" name", r.PostFormValue("name"))
	}, "link_renamed", func(ctx context.Context, r *http.Request) error {
		return d.linkRename(r.PostFormValue("pair"), r.PostFormValue("name"))
	})
	mux.HandleFunc("POST /dashboard/actions/link-invite", authed(func(w http.ResponseWriter, r *http.Request, _ string) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), linkCallTimeout)
		defer cancel()
		ctx, pending := withAudit(ctx, "link-invite", "link invitation")
		text, err := d.linkInvite(ctx)
		if err != nil {
			code := errorCode(err)
			d.store.auditRefusal(ctx, pending, code)
			back(w, r, code)
			return
		}
		unaudited := d.store.auditPendingAccepted(ctx) != nil
		qr, err := linkQRSVG(text)
		if err != nil {
			qr = ""
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		linkInvitationPage.Execute(w, struct {
			Text      string
			QR        template.HTML
			Minutes   int
			Unaudited bool
		}{text, qr, int(linkInviteLife / time.Minute), unaudited})
	}))
}

// dashboardLinkState is the Computers section's data, read with the settings page.
func (d *Daemon) dashboardLinkState(ctx context.Context) *LinkStatus {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s := d.linkStatus(ctx)
	return &s
}

// NameSuggestion is the name that the Computers section fills in: the saved name, or the
// lower-cased short host name.
func (s LinkStatus) NameSuggestion() string {
	if s.Name != "" {
		return s.Name
	}
	return s.SuggestShort
}

// DefaultLinkPort is the link port that setup uses when none is given.
func (s LinkStatus) DefaultPort() int {
	if s.Port != 0 {
		return s.Port
	}
	return link.DefaultPort
}
