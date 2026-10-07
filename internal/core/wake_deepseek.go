package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rwcii/koinon/internal/platform"
)

// This is a narrow reader for the harness's generated credential record, not a
// YAML interpreter. All selected scalars are validated before use.
func deepSeekSecret(path string) ([]byte, error) {
	data, err := platform.ReadCredential(path, 64<<10)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	found, indent := false, 0
	for _, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(line)
		depth := len(line) - len(strings.TrimLeft(line, " \t"))
		if !found {
			if text == "client-connection/browser-session:" {
				found = true
				indent = depth
			}
			continue
		}
		if text != "" && depth <= indent {
			break
		}
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '\'' || value[0] == '"') {
			value = value[1 : len(value)-1]
		}
		key = strings.TrimSpace(key)
		if key == "secret" || key == "kind" || key == "version" {
			if _, duplicate := values[key]; duplicate {
				return nil, ErrInvalid
			}
			values[key] = value
		}
	}
	if !found || (values["kind"] != "" && values["kind"] != "grant") || (values["version"] != "" && values["version"] != "1") {
		return nil, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(values["secret"])
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != values["secret"] {
		return nil, ErrInvalid
	}
	return raw, nil
}
func deepSeekCookie(secret []byte, authority string, now time.Time) *http.Cookie {
	raw, _ := json.Marshal(map[string]any{"version": 1, "authority": authority, "issuedAt": now.UnixMilli(), "expiresAt": now.Add(time.Minute).UnixMilli()})
	body := base64.RawURLEncoding.EncodeToString(raw)
	signature := hmac.New(sha256.New, secret)
	signature.Write([]byte(body))
	audience := sha256.Sum256([]byte(authority))
	return &http.Cookie{Name: "dsh-auth-" + base64.RawURLEncoding.EncodeToString(audience[:]), Value: "v1." + body + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil))}
}
func (s *Store) wakeDeepSeek(ctx context.Context, session Session, target wakeTarget, notice string) wakeResult {
	destination, pinned, err := deepSeekDestination(ctx, target.DSHURL)
	if err != nil {
		return waiting("invalid_harness_origin")
	}
	secret, err := deepSeekSecret(target.DSHCredentials)
	if err != nil {
		return waiting("harness_credential_unavailable")
	}
	rpcID, err := wakeID()
	if err != nil {
		return waiting("random_unavailable")
	}
	requestID, err := wakeID()
	if err != nil {
		return waiting("random_unavailable")
	}
	body, _ := json.Marshal(map[string]any{"type": "client-request", "rpcId": rpcID, "method": "session/prompt", "payload": map[string]any{"args": map[string]any{"request": map[string]any{"requestId": requestID, "sessionId": session.ID, "mode": "queue", "content": []map[string]string{{"type": "text", "text": notice}}, "clientTimeZone": "UTC"}}}})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, destination.String(), bytes.NewReader(body))
	if err != nil {
		return waiting("invalid_harness_origin")
	}
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(deepSeekCookie(secret, destination.Host, time.Now()))
	// Resolve only in preflight. The HTTP client can connect exclusively to those
	// loopback addresses; neither proxies nor redirects can carry the cookie away.
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var last error
		for _, address := range pinned {
			conn, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return submissionError(err, "harness_submission_unconfirmed")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 401, 403:
		return waiting("provider_auth_refused")
	case 409, 429:
		return waiting("receiver_busy")
	}
	if response.StatusCode != 200 {
		return uncertain("harness_submission_unconfirmed")
	}
	var document map[string]json.RawMessage
	if boundedJSON(response, &document) != nil || document == nil {
		return uncertain("harness_submission_unconfirmed")
	}
	var kind string
	json.Unmarshal(document["type"], &kind)
	if kind == "server-response" {
		if json.Unmarshal(document["result"], &document) != nil || document == nil {
			return uncertain("harness_submission_unconfirmed")
		}
	}
	var ok *bool
	if json.Unmarshal(document["ok"], &ok) != nil || ok == nil {
		return uncertain("harness_submission_unconfirmed")
	}
	if !*ok {
		return waiting("harness_refused")
	}
	var value struct {
		Accepted *bool `json:"accepted"`
	}
	if json.Unmarshal(document["value"], &value) != nil || value.Accepted == nil {
		return uncertain("harness_submission_unconfirmed")
	}
	if !*value.Accepted {
		return waiting("harness_refused")
	}
	return accepted()
}
