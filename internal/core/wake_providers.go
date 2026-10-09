package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

type wakeTarget struct {
	LaunchID       string `json:"launch_id"`
	CLI            string `json:"cli"`
	ClaudePID      int    `json:"claude_pid"`
	DSHURL         string `json:"dsh_url"`
	DSHCredentials string `json:"dsh_credentials"`
}

func waiting(reason string) wakeResult   { return wakeResult{"waiting", reason} }
func uncertain(reason string) wakeResult { return wakeResult{"uncertain", reason} }
func accepted() wakeResult               { return wakeResult{"notified", "accepted"} }
func submissionError(err error, reason string) wakeResult {
	var socket *net.OpError
	if errors.As(err, &socket) && socket.Op == "dial" {
		return waiting("receiver_unreachable")
	}
	return uncertain(reason)
}
func httpClient() (*http.Client, func()) {
	transport := &http.Transport{Proxy: nil}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections
}
func boundedJSON(response *http.Response, value any) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("invalid provider response")
	}
	return json.Unmarshal(data, value)
}
func (s *Store) providerWake(ctx context.Context, session Session, notice string) wakeResult {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ctx, expires := context.WithDeadline(ctx, time.UnixMilli(session.ExpiresAt))
	defer expires()
	var target wakeTarget
	if json.Unmarshal(session.WakeTarget, &target) != nil {
		return waiting("invalid_wake_target")
	}
	if session.Family == "codex" {
		view := s.sessionObservations(ctx, session)["activity"]
		if view.Value != nil && (view.Value.State == "busy" || view.Value.State == "waiting") {
			return waiting("receiver_busy")
		}
		cli := target.CLI
		if target.LaunchID != "" {
			var raw string
			if s.db.QueryRowContext(ctx, `SELECT target FROM launches WHERE id=? AND family='codex'`, target.LaunchID).Scan(&raw) != nil {
				return waiting("launch_unavailable")
			}
			var launch LaunchTarget
			if json.Unmarshal([]byte(raw), &launch) != nil {
				return waiting("launch_unavailable")
			}
			cli = launch.CLI
		}
		if !filepath.IsAbs(cli) {
			return waiting("cli_unavailable")
		}
		cmd := exec.CommandContext(ctx, cli, "queue", "--thread", session.ID, "--message", notice)
		// A CLI descendant can retain the output descriptors after the CLI exits.
		// Bound that drain as well as the process itself.
		cmd.WaitDelay = 100 * time.Millisecond
		cmd.Dir = session.Directory
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			return waiting("cli_unavailable")
		}
		if err := cmd.Wait(); err != nil {
			return uncertain("queue_unconfirmed")
		}
		return accepted()
	}
	switch session.Family {
	case "claude":
		return s.wakeClaude(ctx, session, target, notice)
	case "deepseek":
		return s.wakeDeepSeek(ctx, session, target, notice)
	case "opencode":
		return s.wakeOpenCode(ctx, session, target.LaunchID, notice)
	default:
		return waiting("adapter_unavailable")
	}
}
func (s *Store) wakeClaude(ctx context.Context, session Session, target wakeTarget, notice string) wakeResult {
	if target.ClaudePID <= 0 {
		return waiting("claude_target_unavailable")
	}
	registry := s.wake.registry
	if registry == "" {
		root := os.Getenv("CLAUDE_CONFIG_DIR")
		if root == "" {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, ".claude")
		}
		registry = filepath.Join(root, "sessions")
	}
	f, err := platform.OpenOwned(filepath.Join(registry, strconv.Itoa(target.ClaudePID)+".json"))
	if err != nil {
		return waiting("claude_target_unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	f.Close()
	if err != nil || len(data) > 64<<10 {
		return waiting("claude_target_unavailable")
	}
	var peer struct {
		PID        int    `json:"pid"`
		SessionID  string `json:"sessionId"`
		Entrypoint string `json:"entrypoint"`
		Socket     string `json:"messagingSocketPath"`
		Status     string `json:"status"`
	}
	// After /clear the registry names the new transcript while Koinon keeps the session
	// (live check F3): the entry still belongs to the session when its process is the
	// session's recorded host with the start time read at registration (#269).
	if json.Unmarshal(data, &peer) != nil || peer.PID != target.ClaudePID || peer.Entrypoint != "cli" ||
		peer.SessionID != session.ID && !s.sameHost(ctx, session, target.ClaudePID) {
		return waiting("claude_identity_mismatch")
	}
	// shell is the prompt with a background shell task running: the session takes the
	// next-priority notice at its turn boundary, as it does when idle.
	if peer.Status == "busy" || peer.Status == "waiting" {
		return waiting("receiver_busy")
	}
	if peer.Status != "idle" && peer.Status != "shell" {
		return waiting("receiver_state_unknown")
	}
	allowed := s.wake.allowed
	if allowed == nil {
		allowed = platform.ClaudeSocketDirs()
	}
	conn, err := platform.ConnectPeer(peer.Socket, peer.PID, allowed)
	if err != nil {
		return waiting("claude_endpoint_unavailable")
	}
	defer conn.Close()
	if s.wake.reply == "" {
		listener, path, cleanup, e := platform.ReplyListener()
		if e != nil {
			return waiting("reply_endpoint_unavailable")
		}
		s.wake.reply, s.wake.cleanup = path, cleanup
		s.wake.replyDone = make(chan struct{})
		go func() {
			defer close(s.wake.replyDone)
			for {
				incoming, e := listener.AcceptUnix()
				if e != nil {
					return
				}
				platform.PeerPID(incoming)
				incoming.Close()
			}
		}()
	}
	deadline := time.Now().Add(time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	conn.SetWriteDeadline(deadline)
	// The recipient key is selected only after its kernel PID has been verified,
	// and hashed from its unresolved path. Never read a child process's credential.
	digest := sha256.Sum256([]byte(peer.Socket))
	keyPath := filepath.Join(registry, strconv.Itoa(peer.PID)+"."+hex.EncodeToString(digest[:])+".key")
	key, err := platform.ReadCredential(keyPath, 4096)
	if err == nil {
		var auth struct {
			Token string `json:"peerToken"`
		}
		if json.Unmarshal(key, &auth) != nil || len(auth.Token) != 32 {
			return waiting("invalid_peer_credential")
		}
		decoded, e := hex.DecodeString(auth.Token)
		if e != nil || len(decoded) != 16 {
			return waiting("invalid_peer_credential")
		}
		if e = json.NewEncoder(conn).Encode(map[string]string{"type": "auth", "token": auth.Token}); e != nil {
			return uncertain("claude_transport_unconfirmed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return waiting("unsafe_peer_credential")
	}
	id, err := wakeID()
	if err != nil {
		return waiting("random_unavailable")
	}
	from := "uds:" + s.wake.reply
	content := "<cross-session-message from=\"" + from + "\" from-name=\"koinon\">\n" + notice + "\n</cross-session-message>"
	frame := map[string]any{"msgV": 1, "msg_id": id, "type": "user", "priority": "next", "from": from, "message": map[string]string{"role": "user", "content": content}}
	if json.NewEncoder(conn).Encode(frame) != nil {
		return uncertain("claude_transport_unconfirmed")
	}
	// This transport has no application acknowledgement we can verify. Its write
	// remains uncertain until the model acknowledges the inbox, permitting duplicates.
	return uncertain("claude_transport_only")
}
func wakeID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
func (s *Store) openCodeTarget(ctx context.Context, launchID string) (LaunchTarget, error) {
	var raw string
	var target LaunchTarget
	if launchID == "" {
		return target, ErrMissing
	}
	if err := s.db.QueryRowContext(ctx, `SELECT target FROM launches WHERE id=? AND family='opencode'`, launchID).Scan(&raw); err != nil {
		return target, err
	}
	if json.Unmarshal([]byte(raw), &target) != nil || !validAddress(target.Address) || len(target.Password) != 64 {
		return target, ErrInvalid
	}
	if _, err := hex.DecodeString(target.Password); err != nil {
		return target, ErrInvalid
	}
	return target, nil
}

// openCodeStatus uses a fresh status read. Native idle sessions are omitted (spike
// fact 6); omission counts only after this exact session is confirmed to exist.
func openCodeStatus(ctx context.Context, target LaunchTarget, id string) (string, error) {
	client, close := httpClient()
	defer close()
	get := func(path string, value any) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target.Address+path, nil)
		if err != nil {
			return err
		}
		request.SetBasicAuth("opencode", target.Password)
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return errors.New("provider status unavailable")
		}
		return boundedJSON(response, value)
	}
	var states map[string]struct {
		Type string `json:"type"`
	}
	if err := get("/session/status", &states); err != nil {
		return "", err
	}
	if states == nil {
		return "", ErrInvalid
	}
	if value, found := states[id]; found {
		switch value.Type {
		case "idle":
			return "idle", nil
		case "busy", "retry":
			return "busy", nil
		default:
			return "", ErrInvalid
		}
	}
	var found struct {
		ID string `json:"id"`
	}
	if err := get("/session/"+url.PathEscape(id), &found); err != nil {
		return "", err
	}
	if found.ID != id {
		return "", ErrMissing
	}
	return "idle", nil
}
func (s *Store) wakeOpenCode(ctx context.Context, session Session, launchID, notice string) wakeResult {
	target, err := s.openCodeTarget(ctx, launchID)
	if err != nil {
		return waiting("opencode_target_unavailable")
	}
	status, err := openCodeStatus(ctx, target, session.ID)
	if err != nil {
		return waiting("receiver_state_unknown")
	}
	if status != "idle" {
		return waiting("receiver_busy")
	}
	body, _ := json.Marshal(map[string]any{"parts": []map[string]string{{"type": "text", "text": notice}}})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+target.Address+"/session/"+url.PathEscape(session.ID)+"/prompt_async", bytes.NewReader(body))
	if err != nil {
		return waiting("invalid_wake_target")
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth("opencode", target.Password)
	client, close := httpClient()
	defer close()
	response, err := client.Do(request)
	if err != nil {
		return submissionError(err, "opencode_submission_unconfirmed")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 204:
		return accepted()
	case 409, 429:
		return waiting("receiver_busy")
	case 401, 403:
		return waiting("provider_auth_refused")
	default:
		return uncertain("opencode_submission_unconfirmed")
	}
}

// A pinned loopback origin is validated before any DeepSeek credential is read.
func deepSeekDestination(ctx context.Context, raw string) (*url.URL, []string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, nil, ErrInvalid
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, nil, ErrInvalid
	}
	port := u.Port()
	defaultPort := "80"
	if u.Scheme == "https" {
		defaultPort = "443"
	}
	if port == "" {
		port = defaultPort
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, nil, ErrInvalid
	}
	port = strconv.Itoa(number)
	text := host
	if strings.Contains(host, ":") {
		text = "[" + host + "]"
	}
	canonical := text
	if port != defaultPort {
		canonical = net.JoinHostPort(host, port)
	}
	if strings.ToLower(u.Host) != canonical && strings.ToLower(u.Host) != text+":"+defaultPort {
		return nil, nil, ErrInvalid
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, nil, ErrInvalid
	}
	var pinned []string
	for _, a := range addresses {
		if !a.IP.IsLoopback() || a.Zone != "" {
			return nil, nil, ErrInvalid
		}
		pinned = append(pinned, net.JoinHostPort(a.IP.String(), port))
	}
	u.Host = canonical
	u.Path = "/api/session/prompt"
	return u, pinned, nil
}

// sameHost reports whether pid is still the session's recorded host process: the host
// record names pid, and the process with that ID has the start time read at registration.
func (s *Store) sameHost(ctx context.Context, session Session, pid int) bool {
	var hostPID, start int64
	if s.db.QueryRowContext(ctx, `SELECT host_pid,host_start FROM sessions WHERE family=? AND id=?`, session.Family, session.ID).
		Scan(&hostPID, &start) != nil || hostPID != int64(pid) || start == 0 {
		return false
	}
	current, err := s.processStart(pid)
	return err == nil && current == start
}
