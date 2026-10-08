package core

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWakeCodexExactCLIAndArguments(t *testing.T) {
	s, _, _, session, _, _ := wakeFixture(t, "codex")
	record := filepath.Join(t.TempDir(), "arguments")
	cli := filepath.Join(t.TempDir(), "configured cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + strings.ReplaceAll(record, "'", "'\\''") + "'\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	session.WakeTarget, _ = json.Marshal(wakeTarget{CLI: cli})
	session.ID = "synthetic-id; $(touch do-not-create)"
	result := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1")
	data, err := os.ReadFile(record)
	want := "queue\n--thread\n" + session.ID + "\n--message\nKoinon inbox synthetic range 1-1\n"
	if err != nil || string(data) != want || result.State != "notified" {
		t.Fatalf("queue: %q %+v %v", data, result, err)
	}
	session.WakeTarget = json.RawMessage(`{"cli":"codex"}`)
	if result = s.providerWake(context.Background(), session, "notice"); result.Reason != "cli_unavailable" {
		t.Fatal(result)
	}
	session.WakeTarget, _ = json.Marshal(wakeTarget{CLI: cli})
	if err := os.WriteFile(cli, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if result = s.providerWake(context.Background(), session, "notice"); result.State != "uncertain" {
		t.Fatal(result)
	}
	// The configured CLI can exit while its child retains stdout/stderr. An
	// unconfirmed attempt must still return within a bounded drain interval.
	childFile := filepath.Join(t.TempDir(), "child-pid")
	script = "#!/bin/sh\nsleep 30 &\nprintf '%s' \"$!\" > '" + strings.ReplaceAll(childFile, "'", "'\\''") + "'\nexit 1\n"
	os.WriteFile(cli, []byte(script), 0700)
	started := time.Now()
	result = s.providerWake(context.Background(), session, "notice")
	pidData, err := os.ReadFile(childFile)
	if err == nil {
		pid, _ := strconv.Atoi(string(pidData))
		if pid > 1 {
			if child, e := os.FindProcess(pid); e == nil {
				child.Kill()
			}
		}
	}
	if err != nil || result.State != "uncertain" || time.Since(started) > time.Second {
		t.Fatalf("unbounded CLI output drain: %+v %v", result, err)
	}
}

func TestWakeClaudeProtocolAndIdentity(t *testing.T) {
	s, _, _, session, _, _ := wakeFixture(t, "claude")
	dir, err := os.MkdirTemp("/tmp", "kwt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "receiver.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	s.wake.registry = t.TempDir()
	s.wake.allowed = []string{dir}
	defer s.closeWake()
	writeRecord := func(id, status string, pid int) {
		data, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": id, "entrypoint": "cli", "status": status, "messagingSocketPath": socket})
		if err := os.WriteFile(filepath.Join(s.wake.registry, strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	target := wakeTarget{ClaudePID: os.Getpid()}
	session.WakeTarget, _ = json.Marshal(target)
	writeRecord("different-native-session", "idle", os.Getpid())
	if got := s.providerWake(context.Background(), session, "notice"); got.Reason != "claude_identity_mismatch" {
		t.Fatal(got)
	}
	writeRecord(session.ID, "busy", os.Getpid())
	if got := s.providerWake(context.Background(), session, "notice"); got.Reason != "receiver_busy" {
		t.Fatal(got)
	}
	writeRecord(session.ID, "idle", os.Getpid())
	digest := sha256.Sum256([]byte(socket))
	token := strings.Repeat("ab", 16)
	if err := os.WriteFile(filepath.Join(s.wake.registry, strconv.Itoa(os.Getpid())+"."+hex.EncodeToString(digest[:])+".key"), []byte(`{"peerToken":"`+token+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	frames := make(chan []map[string]any, 1)
	go func() {
		conn, e := listener.AcceptUnix()
		if e != nil {
			frames <- nil
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		scanner := bufio.NewScanner(conn)
		var result []map[string]any
		for scanner.Scan() {
			var frame map[string]any
			if json.Unmarshal(scanner.Bytes(), &frame) == nil {
				result = append(result, frame)
			}
		}
		frames <- result
	}()
	got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1")
	if got.State != "uncertain" || got.Reason != "claude_transport_only" {
		t.Fatal(got)
	}
	data := <-frames
	if len(data) != 2 || data[0]["type"] != "auth" || data[0]["token"] != token || data[1]["msgV"] != float64(1) || data[1]["priority"] != "next" {
		t.Fatalf("frames: %+v", data)
	}
	content := data[1]["message"].(map[string]any)["content"].(string)
	if !strings.Contains(content, `from-name="koinon"`) || !strings.Contains(content, "range 1-1") || strings.Contains(content, "PRIVATE") {
		t.Fatal(content)
	}
	if data[1]["from"] != "uds:"+s.wake.reply {
		t.Fatal("reply endpoint mismatch")
	}
}

func TestWakeDeepSeekAuthenticatedQueue(t *testing.T) {
	s, _, _, session, _, _ := wakeFixture(t, "deepseek")
	key := []byte(strings.Repeat("s", 32))
	credential := filepath.Join(t.TempDir(), "credentials.yaml")
	if err := os.WriteFile(credential, []byte("client-connection/browser-session:\n  kind: grant\n  version: 1\n  secret: "+base64.RawURLEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status, acceptedValue, calls := 200, true, 0
	malformed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/session/prompt" || r.Method != "POST" || r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			t.Error("wrong queue request")
		}
		audience := sha256.Sum256([]byte(r.Host))
		cookie, err := r.Cookie("dsh-auth-" + base64.RawURLEncoding.EncodeToString(audience[:]))
		if err != nil {
			t.Error(err)
			w.WriteHeader(403)
			return
		}
		parts := strings.Split(cookie.Value, ".")
		if len(parts) != 3 || parts[0] != "v1" {
			t.Error("bad cookie")
			return
		}
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(parts[1]))
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		if !hmac.Equal(mac.Sum(nil), sig) {
			t.Error("bad signature")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		if fields["authority"] != r.Host || fields["version"] != float64(1) || fields["expiresAt"].(float64)-fields["issuedAt"].(float64) != 60000 {
			t.Error("cookie audience/lifetime")
		}
		var document map[string]any
		if json.NewDecoder(r.Body).Decode(&document) != nil {
			t.Error("bad body")
			return
		}
		request := document["payload"].(map[string]any)["args"].(map[string]any)["request"].(map[string]any)
		text := request["content"].([]any)[0].(map[string]any)["text"]
		if document["type"] != "client-request" || document["method"] != "session/prompt" || request["sessionId"] != session.ID || request["mode"] != "queue" || text != "Koinon inbox synthetic range 1-1" {
			t.Errorf("wrong queue body %+v", document)
		}
		w.WriteHeader(status)
		if malformed {
			io.WriteString(w, `{"bogus":true}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"type": "server-response", "result": map[string]any{"ok": true, "value": map[string]any{"accepted": acceptedValue}}})
	}))
	defer server.Close()
	target := wakeTarget{DSHURL: server.URL, DSHCredentials: credential}
	session.WakeTarget, _ = json.Marshal(target)
	if got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1"); got.State != "notified" {
		t.Fatal(got)
	}
	acceptedValue = false
	if got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1"); got.State != "waiting" {
		t.Fatal(got)
	}
	malformed = true
	if got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1"); got.State != "uncertain" {
		t.Fatal(got)
	}
	malformed = false
	status = 403
	if got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1"); got.Reason != "provider_auth_refused" {
		t.Fatal(got)
	}
	before := calls
	target.DSHURL = "http://example.invalid"
	session.WakeTarget, _ = json.Marshal(target)
	if got := s.providerWake(context.Background(), session, "notice"); got.Reason != "invalid_harness_origin" || calls != before {
		t.Fatal(got)
	}
}

func TestWakeOpenCodeFreshIdleExactSession(t *testing.T) {
	s, _, _, session, _, _ := wakeFixture(t, "opencode")
	password := strings.Repeat("cd", 32)
	status := "busy"
	exists := true
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "opencode" || pass != password {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/session/status":
			if status == "" {
				io.WriteString(w, "{}")
			} else {
				json.NewEncoder(w).Encode(map[string]any{session.ID: map[string]string{"type": status}})
			}
		case "/session/" + session.ID:
			if !exists {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"id": session.ID})
		case "/session/" + session.ID + "/prompt_async":
			if r.Method != "POST" || status != "" {
				t.Error("submitted busy session")
			}
			var body struct{ Parts []struct{ Type, Text string } }
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Parts) != 1 || body.Parts[0].Type != "text" || body.Parts[0].Text != "Koinon inbox synthetic range 1-1" {
				t.Error("wrong prompt")
			}
			posts++
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	launch, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "opencode", Directory: session.Directory, CLI: "/opt/synthetic/opencode", HostPID: 123, Address: strings.TrimPrefix(server.URL, "http://"), Password: password})
	if err != nil {
		t.Fatal(err)
	}
	session.WakeTarget, _ = json.Marshal(wakeTarget{LaunchID: launch})
	for _, state := range []string{"busy", "retry", "invalid"} {
		status = state
		if got := s.providerWake(context.Background(), session, "notice"); got.State != "waiting" || posts != 0 {
			t.Fatal(got)
		}
	}
	status = ""
	if got := s.providerWake(context.Background(), session, "Koinon inbox synthetic range 1-1"); got.State != "notified" || posts != 1 {
		t.Fatal(got)
	}
	exists = false
	if got := s.providerWake(context.Background(), session, "notice"); got.State != "waiting" || posts != 1 {
		t.Fatal(got)
	}
}

func TestDeepSeekOriginsAndCredentialFences(t *testing.T) {
	for _, raw := range []string{"http://example.com", "http://user@127.0.0.1", "http://127.0.0.1/path", "http://localhost/?query=x", "http://2130706433", "http://127.0.0.1:00080", "http://[::1%25lo0]", "ftp://localhost", "http://localhost:0"} {
		if _, _, err := deepSeekDestination(context.Background(), raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"http://localhost", "127.0.0.1:80", "http://[::1]:80/"} {
		if _, _, err := deepSeekDestination(context.Background(), raw); err != nil {
			t.Errorf("refused %s: %v", raw, err)
		}
	}
	file := filepath.Join(t.TempDir(), "key")
	good := "client-connection/browser-session:\n  secret: " + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32))) + "\n"
	for _, body := range []string{good + "  kind: other\n", good + "  version: 2\n", good + "  secret: duplicate\n", "other:\n  secret: ignored\n"} {
		os.WriteFile(file, []byte(body), 0600)
		if _, err := deepSeekSecret(file); err == nil {
			t.Fatal("accepted invalid credential")
		}
	}
	os.WriteFile(file, []byte(good), 0600)
	os.Chmod(file, 0644)
	if _, err := deepSeekSecret(file); err == nil {
		t.Fatal("accepted public credential")
	}
	os.Chmod(file, 0600)
	link := file + "-link"
	os.Symlink(file, link)
	if _, err := deepSeekSecret(link); err == nil {
		t.Fatal("accepted credential symlink")
	}
}

func TestWakeHTTPRedirectsNeverForwardCredentials(t *testing.T) {
	s, _, _, session, _, _ := wakeFixture(t, "deepseek")
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; t.Error("redirected credential request") }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/status" {
			json.NewEncoder(w).Encode(map[string]any{session.ID: map[string]string{"type": "idle"}})
			return
		}
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "credential")
	os.WriteFile(file, []byte("client-connection/browser-session:\n  secret: "+base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))+"\n"), 0600)
	session.WakeTarget, _ = json.Marshal(wakeTarget{DSHURL: server.URL, DSHCredentials: file})
	if got := s.providerWake(context.Background(), session, "notice"); got.State != "uncertain" {
		t.Fatal(got)
	}
	session.Family = "opencode"
	launch, err := s.CreateLaunch(context.Background(), LaunchTarget{Family: "opencode", Directory: session.Directory, CLI: "/opt/synthetic/opencode", HostPID: 123, Address: strings.TrimPrefix(server.URL, "http://"), Password: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	session.WakeTarget, _ = json.Marshal(wakeTarget{LaunchID: launch})
	if got := s.providerWake(context.Background(), session, "notice"); got.State != "uncertain" {
		t.Fatal(got)
	}
	if leaked {
		t.Fatal("credential forwarded")
	}
	server.Close()
	session.Family = "deepseek"
	session.WakeTarget, _ = json.Marshal(wakeTarget{DSHURL: server.URL, DSHCredentials: file})
	if got := s.providerWake(context.Background(), session, "notice"); got.State != "waiting" || got.Reason != "receiver_unreachable" {
		t.Fatal(got)
	}
}
