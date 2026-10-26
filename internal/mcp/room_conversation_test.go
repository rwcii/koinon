package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
)

// roomDashboard drives the harness daemon's dashboard room actions over HTTP as the
// maintainer: login link, session cookie, CSRF token and same-origin form posts.
type roomDashboard struct {
	t                     *testing.T
	address, cookie, csrf string
	client                *http.Client
}

func newRoomDashboard(t *testing.T, h *harness) *roomDashboard {
	t.Helper()
	secret, err := core.ReadSecret(h.root)
	if err != nil {
		t.Fatal(err)
	}
	address := h.d.Addresses()[0]
	data, err := core.Call(context.Background(), address, secret, "/v1/dashboard/links", map[string]any{})
	var link struct{ Path string }
	if err != nil || json.Unmarshal(data, &link) != nil || link.Path == "" {
		t.Fatalf("dashboard link: %s %v", data, err)
	}
	d := &roomDashboard{t: t, address: address, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	response, _ := d.do("GET", link.Path, nil)
	for _, c := range response.Cookies() {
		d.cookie = c.Value
	}
	_, page := d.do("GET", "/dashboard/rooms", nil)
	match := regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`).FindStringSubmatch(page)
	if d.cookie == "" || match == nil {
		t.Fatalf("dashboard login: %d", response.StatusCode)
	}
	d.csrf = match[1]
	return d
}

func (d *roomDashboard) do(method, path string, form url.Values) (*http.Response, string) {
	d.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, err := http.NewRequest(method, "http://"+d.address+path, body)
	if err != nil {
		d.t.Fatal(err)
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://"+d.address)
	}
	if d.cookie != "" {
		_, port, _ := net.SplitHostPort(d.address)
		r.AddCookie(&http.Cookie{Name: "koinon_dashboard_" + port, Value: d.cookie})
	}
	response, err := d.client.Do(r)
	if err != nil {
		d.t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response, string(data)
}

// action posts a room action and returns its notice code.
func (d *roomDashboard) action(name string, form url.Values) string {
	d.t.Helper()
	form.Set("csrf", d.csrf)
	response, _ := d.do("POST", "/dashboard/actions/"+name, form)
	location, err := url.Parse(response.Header.Get("Location"))
	if response.StatusCode != http.StatusSeeOther || err != nil {
		d.t.Fatalf("%s: %d", name, response.StatusCode)
	}
	return location.Query().Get("notice")
}

// A maintainer post from the dashboard reaches a member of each MCP family and a DeepSeek
// member on its command route; each reads the room through its own interface and replies,
// and the other reads the reply.
func TestRoomConversationThroughNativeInterfaces(t *testing.T) {
	for _, c := range callers {
		t.Run(c.family, func(t *testing.T) {
			h := c.harness(t)
			ctx := context.Background()
			with := func(args map[string]any) map[string]any {
				for k, v := range c.args {
					args[k] = v
				}
				return args
			}
			peers, failed := c.peers(h)
			if failed {
				t.Fatalf("peers: %v", peers)
			}
			var name string
			for _, p := range peers["peers"].([]any) {
				if p := p.(map[string]any); p["family"] == c.family {
					name = p["name"].(string)
				}
			}
			secret, err := core.ReadSecret(h.root)
			if err != nil {
				t.Fatal(err)
			}
			address := h.d.Addresses()[0]
			data, err := core.Call(ctx, address, secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-dsh", Directory: t.TempDir(), TTLSeconds: 60})
			var registered struct{ Session struct{ Name string } }
			if err != nil || json.Unmarshal(data, &registered) != nil || registered.Session.Name == "" || name == "" {
				t.Fatalf("members: %q %s %v", name, data, err)
			}
			dash := newRoomDashboard(t, h)
			if got := dash.action("room-create", url.Values{"name": {"synthetic native room"}, "key": {"create-" + c.family}}); got != "room_created" {
				t.Fatalf("room-create: %s", got)
			}
			_, page := dash.do("GET", "/dashboard/rooms", nil)
			match := regexp.MustCompile(`room=([0-9a-f]{32})`).FindStringSubmatch(page)
			if match == nil {
				t.Fatal("no room on the page")
			}
			room := match[1]
			for _, peer := range []string{name, registered.Session.Name} {
				if got := dash.action("room-add", url.Values{"room_id": {room}, "peer": {peer}}); got != "room_member_added" {
					t.Fatalf("room-add %s: %s", peer, got)
				}
			}
			deadline := strconv.FormatInt(time.Now().Unix()+600, 10)
			if got := dash.action("room-post", url.Values{"room_id": {room}, "body": {"synthetic task"}, "key": {"post-" + c.family}, "deadline": {deadline}}); got != "room_posted" {
				t.Fatalf("room-post: %s", got)
			}
			// The MCP member reads the task and replies; the reply reaches DeepSeek only.
			read, failed := h.tool("room_read", with(map[string]any{"room_id": room}), c.meta)
			posts := read["result"].(map[string]any)["posts"].([]any)
			if failed || len(posts) != 1 || posts[0].(map[string]any)["origin"] != "maintainer" {
				t.Fatalf("room_read: %v", read)
			}
			reply, failed := h.tool("room_post", with(map[string]any{"room_id": room, "body": "synthetic reply from " + c.family, "key": "reply", "deadline": float64(time.Now().Unix() + 600)}), c.meta)
			if failed || reply["result"].(map[string]any)["queued"] != float64(1) {
				t.Fatalf("room_post: %v", reply)
			}
			// DeepSeek reads the reply on its command route and answers.
			caller := core.Key{Family: "deepseek", ID: "synthetic-dsh"}
			data, err = core.Call(ctx, address, secret, "/v1/rooms/read", map[string]any{"caller": caller, "room_id": room})
			if err != nil || !strings.Contains(string(data), "synthetic reply from "+c.family) || !strings.Contains(string(data), `"sender_family":"`+c.family+`"`) {
				t.Fatalf("deepseek read: %s %v", data, err)
			}
			if _, err := core.Call(ctx, address, secret, "/v1/rooms/post", map[string]any{"caller": caller, "room_id": room, "body": "synthetic deepseek answer", "key": "answer", "deadline": time.Now().Unix() + 600}); err != nil {
				t.Fatalf("deepseek post: %v", err)
			}
			read, failed = h.tool("room_read", with(map[string]any{"room_id": room, "after": 2}), c.meta)
			posts = read["result"].(map[string]any)["posts"].([]any)
			if failed || len(posts) != 1 || posts[0].(map[string]any)["sender_family"] != "deepseek" || posts[0].(map[string]any)["origin"] != "member" {
				t.Fatalf("reading the answer: %v", read)
			}
			// The room page shows the conversation with each post's recipients.
			_, page = dash.do("GET", "/dashboard/rooms?room="+room, nil)
			for _, want := range []string{"synthetic task", "synthetic reply from " + c.family, "synthetic deepseek answer", name + " <small>" + c.family + ":", registered.Session.Name + " <small>deepseek:"} {
				if !strings.Contains(page, want) {
					t.Fatalf("room page lacks %q", want)
				}
			}
		})
	}
}
