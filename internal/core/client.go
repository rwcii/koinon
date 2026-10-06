package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// CreateLaunch calls only the selected loopback daemon: no proxy or redirects.
func CreateLaunch(ctx context.Context, address, secret string, target LaunchTarget) (string, error) {
	if !validAddress(address) {
		return "", ErrInvalid
	}
	body, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/v1/launches", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return "", errors.New("daemon unavailable; agent was not started")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", errors.New("daemon refused launch; agent was not started")
	}
	var result struct {
		OK bool   `json:"ok"`
		ID string `json:"launch_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&result); err != nil || !result.OK || len(result.ID) != 64 {
		return "", errors.New("invalid daemon launch response")
	}
	return result.ID, nil
}
