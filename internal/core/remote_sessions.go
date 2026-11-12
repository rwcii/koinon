package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const remoteSessionLifetime = 48 * time.Hour
const maxRemoteSessions = 32

var ErrRemoteCapacity = errors.New("remote_session_capacity: forget a signed-in device before signing in another browser")
var ErrRevocationIncomplete = errors.New("Revocation incomplete; private storage could not be updated")

// RemoteDevice describes a browser session, never physical device identity.
type RemoteDevice struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	CreatedAt    int64  `json:"created_at"`
	LastObserved int64  `json:"last_observed"`
	ExpiresAt    int64  `json:"expires_at"`
	ThisBrowser  bool   `json:"this_browser"`
}

// The random PBKDF2 salt changes on every secret replacement (including replacing
// it with the same text). Its digest binds sessions without copying a reusable key.
func (a *dashboardAuth) credentialGeneration() (string, error) {
	var salt []byte
	err := a.store.db.QueryRow(`SELECT salt FROM maintainer_secret WHERE id=1`).Scan(&salt)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("remote-session-generation-v2:"), salt...))
	return hex.EncodeToString(digest[:]), nil
}

type durableRemoteSession struct {
	Version int `json:"version"`
	RemoteDevice
	Origin     string `json:"origin"`
	Generation string `json:"generation"`
	Hash       string `json:"token_hash"`
	Revoked    string `json:"revoked"`
}

const remoteSessionPrefix = "remote.v2.session:"

func (a *dashboardAuth) readRemoteLocked() ([]durableRemoteSession, error) {
	rows, err := a.store.db.Query(`SELECT value FROM settings WHERE name GLOB 'remote.v2.session:*'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []durableRemoteSession
	for rows.Next() {
		var value string
		var s durableRemoteSession
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(value), &s); err != nil {
			return nil, err
		}
		_, idOK := tokenHash(s.ID)
		_, hashOK := tokenHash(s.Hash)
		_, generationOK := tokenHash(s.Generation)
		if s.Version != 2 || !idOK || !hashOK || !generationOK || s.ExpiresAt != s.CreatedAt+remoteSessionLifetime.Milliseconds() || s.LastObserved < s.CreatedAt {
			return nil, ErrUnavailable
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}
func (a *dashboardAuth) saveRemoteLocked(s durableRemoteSession) error {
	return a.saveRemoteClassLocked(s, ordinary)
}
func (a *dashboardAuth) saveRemoteClassLocked(s durableRemoteSession, class writeClass) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := a.store.begin(ctx, class)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, remoteSessionPrefix+s.ID, string(data), a.now().UnixMilli()); err != nil {
		return tx.fail(err)
	}
	return tx.Commit()
}

func (a *dashboardAuth) startRemoteLocked(hash [32]byte, origin string) error {
	now := a.now().UnixMilli()
	if a.store == nil { // isolated auth unit fixtures only; daemons always bind Store
		n := 0
		for _, s := range a.sessions {
			if strings.HasPrefix(s.client, "remote:") {
				n++
			}
		}
		if n >= maxRemoteSessions {
			return ErrRemoteCapacity
		}
		a.sessions[hash] = &dashboardSession{created: a.now(), used: a.now(), client: "remote:" + origin}
		return nil
	}
	generation, err := a.credentialGeneration()
	if err != nil {
		return err
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return err
	}
	count := 0
	for _, s := range sessions {
		if now > s.ExpiresAt+7*24*time.Hour.Milliseconds() {
			tx, err := a.store.begin(context.Background(), control)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`DELETE FROM settings WHERE name=?`, remoteSessionPrefix+s.ID); err != nil {
				tx.Rollback()
				return err
			}
			if err = tx.Commit(); err != nil {
				return err
			}
			continue
		}
		if s.Revoked == "" && s.ExpiresAt > now && s.Generation == generation {
			count++
		}
	}
	if count >= maxRemoteSessions {
		return ErrRemoteCapacity
	}
	id, err := randomToken()
	if err != nil {
		return err
	}
	return a.saveRemoteLocked(durableRemoteSession{Version: 2, RemoteDevice: RemoteDevice{ID: id, Label: "Browser", CreatedAt: now, LastObserved: now, ExpiresAt: now + remoteSessionLifetime.Milliseconds()}, Origin: origin, Generation: generation, Hash: hex.EncodeToString(hash[:])})
}
func (a *dashboardAuth) remoteSessionLocked(hash [32]byte, origin string) bool {
	if a.denyAllRemote || a.denied[hash] {
		a.authReason = "device_revoked"
		return false
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.storeHealth = "unavailable"
		a.authReason = "unknown_credential"
		return false
	}
	current, err := a.credentialGeneration()
	if err != nil {
		a.storeHealth = "unavailable"
		return false
	}
	for _, s := range sessions {
		if s.Hash != hex.EncodeToString(hash[:]) {
			continue
		}
		if s.Origin != origin {
			a.authReason = "unknown_credential"
			return false
		}
		reason := s.Revoked
		now := a.now().UnixMilli()
		if reason == "" && s.Generation != current {
			reason = "password_changed"
		}
		if reason == "" && (now >= s.ExpiresAt || now < s.LastObserved) {
			reason = "session_expired"
		}
		if reason != "" {
			a.authReason = reason
			a.denied[hash] = true
			s.Revoked = reason
			if a.saveRemoteClassLocked(s, control) != nil {
				a.storeHealth = "unavailable"
			}
			return false
		}
		s.LastObserved = now
		if a.saveRemoteClassLocked(s, control) != nil {
			a.storeHealth = "unavailable"
			return false
		}
		a.storeHealth = "healthy"
		a.lastPhone = now
		return true
	}
	a.authReason = "unknown_credential"
	return false
}
func (a *dashboardAuth) revokeRemote(id, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revokeRemoteLocked(id, reason)
}
func (a *dashboardAuth) revokeRemoteLocked(id, reason string) (result error) {
	defer func() {
		if a.pendingRevocations == nil {
			a.pendingRevocations = map[string]bool{}
		}
		if result != nil {
			a.pendingRevocations[id] = true
		} else if id == "" {
			clear(a.pendingRevocations)
		} else {
			delete(a.pendingRevocations, id)
		}
	}()
	clear(a.tickets)
	for h, s := range a.sessions {
		if strings.HasPrefix(s.client, "remote:") {
			delete(a.sessions, h)
		}
	}
	if a.store == nil {
		return nil
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.denyAllRemote = true
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	for _, s := range sessions {
		if id != "" && s.ID != id {
			continue
		}
		hash, err := hex.DecodeString(s.Hash)
		if err != nil {
			a.denyAllRemote = true
			return ErrRevocationIncomplete
		}
		var h [32]byte
		copy(h[:], hash)
		a.denied[h] = true
	}
	failed := false
	for _, s := range sessions {
		if (id == "" || s.ID == id) && s.Revoked == "" {
			s.Revoked = reason
			if a.saveRemoteClassLocked(s, control) != nil {
				failed = true
			}
		}
	}
	if failed {
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	a.storeHealth = "healthy"
	if id == "" {
		a.denyAllRemote = false
	}
	return nil
}
func (a *dashboardAuth) sessionInfo(token string) RemoteDevice {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessionInfoLocked(token)
}
func (a *dashboardAuth) sessionInfoLocked(token string) RemoteDevice {
	hash, ok := tokenHash(token)
	if !ok || a.store == nil {
		return RemoteDevice{}
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return RemoteDevice{}
	}
	for _, s := range sessions {
		if s.Hash == hex.EncodeToString(hash[:]) {
			return s.RemoteDevice
		}
	}
	return RemoteDevice{}
}
func (a *dashboardAuth) remoteSessionID(token string) string { return a.sessionInfo(token).ID }
func (a *dashboardAuth) remoteDevices(token string) ([]RemoteDevice, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil, ErrUnavailable
	}
	generation, err := a.credentialGeneration()
	if err != nil {
		return nil, err
	}
	current := a.sessionInfoLocked(token).ID
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return nil, err
	}
	devices := []RemoteDevice{}
	for _, s := range sessions {
		if s.Revoked == "" && s.Generation == generation && s.ExpiresAt > a.now().UnixMilli() {
			s.ThisBrowser = s.ID == current
			devices = append(devices, s.RemoteDevice)
		}
	}
	return devices, nil
}

func (d *Daemon) dashboardDeviceRoutes(mux *http.ServeMux, authed func(func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc) {
	mux.HandleFunc("GET /dashboard/remote/devices", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		devices, err := d.auth.remoteDevices(id)
		if err != nil {
			http.Error(w, "Private session storage unavailable", 503)
			return
		}
		respond(w, 200, map[string]any{"devices": devices, "current": d.auth.sessionInfo(id), "csrf": d.auth.csrf(id)})
	}))
	mux.HandleFunc("POST /dashboard/remote/devices", authed(func(w http.ResponseWriter, r *http.Request, id string) {
		target := r.PostForm.Get("id")
		var err error
		switch r.PostForm.Get("action") {
		case "revoke":
			if target == "" {
				err = ErrInvalid
			} else {
				err = d.auth.revokeRemote(target, "device_revoked")
			}
		case "revoke-all":
			err = d.auth.revokeRemote("", "device_revoked")
		case "rename":
			label := strings.TrimSpace(r.PostForm.Get("label"))
			if target == "" || label == "" || len(label) > 80 || strings.ContainsAny(label, "\r\n\x00") {
				err = ErrInvalid
			} else {
				d.auth.mu.Lock()
				var sessions []durableRemoteSession
				sessions, err = d.auth.readRemoteLocked()
				if err == nil {
					for _, s := range sessions {
						if s.ID == target && s.Revoked == "" {
							s.Label = label
							err = d.auth.saveRemoteLocked(s)
							break
						}
					}
				}
				d.auth.mu.Unlock()
			}
		default:
			err = ErrInvalid
		}
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		respond(w, 200, map[string]any{"ok": true})
	}))
}

// These observations describe host requests, not cellular connectivity or a
// person's current presence. Store health is unavailable until a successful read.
func (a *dashboardAuth) remoteDiagnostics() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := map[string]any{"store_health": a.storeHealth, "phone_connected_at": a.lastPhone, "last_auth_end_reason": a.authReason, "revocation_incomplete": len(a.pendingRevocations) > 0}
	if a.store == nil {
		result["store_health"] = "unavailable"
		return result
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		result["store_health"] = "unavailable"
		return result
	}
	generation, err := a.credentialGeneration()
	if err != nil {
		result["store_health"] = "unavailable"
		return result
	}
	count := 0
	for _, s := range sessions {
		if s.Revoked == "" && s.Generation == generation && s.ExpiresAt > a.now().UnixMilli() {
			count++
		}
	}
	result["active_sessions"] = count
	return result
}

// Re-read only the rejected credential's tombstone; never publish another
// request's latest authentication diagnostic on this response.
func (a *dashboardAuth) remoteRejectionReason(r *http.Request) string {
	cookie, err := r.Cookie(cookieName(r))
	if err != nil {
		return "unknown_credential"
	}
	hash, ok := tokenHash(cookie.Value)
	if !ok {
		return "unknown_credential"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return "unknown_credential"
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		return "unknown_credential"
	}
	for _, s := range sessions {
		if s.Hash == hex.EncodeToString(hash[:]) && s.Revoked != "" {
			return s.Revoked
		}
	}
	return "unknown_credential"
}

// Only trusted saved configuration drives origin revocation. A request carrying
// a copied cookie on a different listener must never revoke the original login.
func (a *dashboardAuth) reconcileRemoteOrigin(origin string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.store == nil {
		return nil
	}
	sessions, err := a.readRemoteLocked()
	if err != nil {
		a.denyAllRemote = true
		a.storeHealth = "unavailable"
		return ErrRevocationIncomplete
	}
	for _, s := range sessions {
		if s.Revoked == "" && s.Origin != origin {
			if err := a.revokeRemoteLocked(s.ID, "origin_changed"); err != nil {
				return err
			}
		}
	}
	if len(a.pendingRevocations) == 0 {
		a.denyAllRemote = false
	}
	return nil
}
