package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rwcii/koinon/internal/core"
	"github.com/rwcii/koinon/internal/setup"
)

// The room commands in the installed DeepSeek guide reach the room API as written: flags may
// come before, between or after ROOM_ID and BODY, and "--" ends the flags.
func TestRoomCommandsOfTheDeepSeekGuide(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	d, err := core.Start(core.Config{StateDir: root, Listen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	secret, err := core.ReadSecret(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Call(context.Background(), d.Addresses()[0], secret, "/v1/sessions/register", core.Registration{Family: "deepseek", ID: "synthetic-guide", Directory: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var guide bytes.Buffer
	if err := setup.Guide("deepseek", &guide); err != nil {
		t.Fatal(err)
	}
	room := strings.Repeat("a", 32)
	deadline := strconv.FormatInt(time.Now().Unix()+600, 10)
	run := func(args []string) error {
		args = append([]string{args[0], "--state-dir", root, "--address", d.Addresses()[0]}, args[1:]...)
		return roomCommand(context.Background(), args, &bytes.Buffer{})
	}
	refusal := func(err error) string {
		var refused core.RefusedError
		if errors.As(err, &refused) {
			return refused.Code
		}
		var usage usageError
		if errors.As(err, &usage) {
			return usage.code
		}
		return "error: " + err.Error()
	}
	commands := 0
	for _, line := range strings.Split(guide.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "koinon room ") {
			continue
		}
		commands++
		line = strings.NewReplacer("$DSH_SESSION_ID", "synthetic-guide", "ROOM_ID", room, "'message'", "synthetic", "KEY", "synthetic-key", "UNIX_SECONDS", deadline).Replace(line)
		err := run(strings.Fields(line)[2:])
		if strings.HasPrefix(line, "koinon room list") {
			if err != nil {
				t.Fatalf("%s: %v", line, err)
			}
		} else if refusal(err) != "not_room_member" {
			t.Fatalf("%s: %s", line, refusal(err))
		}
	}
	if commands != 4 {
		t.Fatalf("guide room commands: %d", commands)
	}
	for _, args := range [][]string{
		{"post", room, "synthetic", "--as", "deepseek:synthetic-guide", "--key", "k", "--deadline", deadline},
		{"post", "--key", "k", room, "--as", "deepseek:synthetic-guide", "synthetic", "--deadline", deadline},
		{"post", "--as", "deepseek:synthetic-guide", "--key", "k", "--deadline", deadline, "--", room, "-synthetic"},
	} {
		if code := refusal(run(args)); code != "not_room_member" {
			t.Fatalf("%v: %s", args, code)
		}
	}
	if code := refusal(run([]string{"post", "--as", "deepseek:synthetic-guide", room, "a", "b", "--key", "k", "--deadline", deadline})); code != "invalid_request" {
		t.Fatalf("three positional arguments: %s", code)
	}
	// As a member of a dashboard room, the guide's commands read the maintainer's post and
	// post a reply.
	address := d.Addresses()[0]
	link, err := core.Call(context.Background(), address, secret, "/v1/dashboard/links", map[string]any{})
	var login struct{ Path string }
	if err != nil || json.Unmarshal(link, &login) != nil {
		t.Fatalf("dashboard link: %v", err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var cookie string
	dashboard := func(method, path string, form url.Values) (*http.Response, string) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		r, _ := http.NewRequest(method, "http://"+address+path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", "http://"+address)
		}
		if cookie != "" {
			_, port, _ := net.SplitHostPort(address)
			r.AddCookie(&http.Cookie{Name: "koinon_dashboard_" + port, Value: cookie})
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response, string(data)
	}
	response, _ := dashboard("GET", login.Path, nil)
	for _, c := range response.Cookies() {
		cookie = c.Value
	}
	_, page := dashboard("GET", "/dashboard/rooms", nil)
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]{64})"`).FindStringSubmatch(page)
	if csrf == nil {
		t.Fatal("dashboard login")
	}
	action := func(name string, form url.Values) {
		form.Set("csrf", csrf[1])
		if r, _ := dashboard("POST", "/dashboard/actions/"+name, form); r.StatusCode != http.StatusSeeOther || !strings.Contains(r.Header.Get("Location"), "notice=room_") {
			t.Fatalf("%s: %d %s", name, r.StatusCode, r.Header.Get("Location"))
		}
	}
	action("room-create", url.Values{"name": {"synthetic command room"}, "key": {"create"}})
	_, page = dashboard("GET", "/dashboard/rooms", nil)
	room = regexp.MustCompile(`room=([0-9a-f]{32})`).FindStringSubmatch(page)[1]
	var registered struct{ Sessions []core.Session }
	list, _ := core.Call(context.Background(), address, secret, "/v1/sessions", nil)
	json.Unmarshal(list, &registered)
	for _, session := range registered.Sessions {
		if session.Family == "deepseek" {
			action("room-add", url.Values{"room_id": {room}, "peer": {session.Name}})
		}
	}
	action("room-post", url.Values{"room_id": {room}, "body": {"synthetic task"}, "key": {"task"}, "deadline": {deadline}})
	var out bytes.Buffer
	if err := roomCommand(context.Background(), []string{"read", "--state-dir", root, "--address", address, "--as", "deepseek:synthetic-guide", room}, &out); err != nil ||
		!strings.Contains(out.String(), `"body":"synthetic task"`) || !strings.Contains(out.String(), `"origin":"maintainer"`) {
		t.Fatalf("command read: %s %v", out.String(), err)
	}
	out.Reset()
	if err := roomCommand(context.Background(), []string{"post", "--state-dir", root, "--address", address, "--as", "deepseek:synthetic-guide", room, "synthetic reply", "--key", "reply", "--deadline", deadline}, &out); err != nil ||
		!strings.Contains(out.String(), `"seq":2`) {
		t.Fatalf("command post: %s %v", out.String(), err)
	}
	if _, page = dashboard("GET", "/dashboard/rooms?room="+room, nil); !strings.Contains(page, "synthetic reply") {
		t.Fatal("the reply is not on the room page")
	}
}
